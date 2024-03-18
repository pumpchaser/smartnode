package node

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	batch "github.com/rocket-pool/batch-query"
	"github.com/rocket-pool/node-manager-core/eth"
	"github.com/rocket-pool/node-manager-core/utils/log"
	"github.com/rocket-pool/rocketpool-go/minipool"
	"github.com/rocket-pool/rocketpool-go/node"
	"github.com/rocket-pool/rocketpool-go/rocketpool"
	"github.com/rocket-pool/rocketpool-go/types"
	"golang.org/x/sync/errgroup"

	"github.com/rocket-pool/node-manager-core/node/wallet"
	rpstate "github.com/rocket-pool/rocketpool-go/utils/state"
	"github.com/rocket-pool/smartnode/rocketpool-daemon/common/alerting"
	"github.com/rocket-pool/smartnode/rocketpool-daemon/common/gas"
	"github.com/rocket-pool/smartnode/rocketpool-daemon/common/services"
	"github.com/rocket-pool/smartnode/rocketpool-daemon/common/state"
	"github.com/rocket-pool/smartnode/rocketpool-daemon/common/tx"
	"github.com/rocket-pool/smartnode/shared/config"
)

const (
	bondReductionBatchSize int = 200
)

// Reduce bonds task
type ReduceBonds struct {
	sp             *services.ServiceProvider
	log            log.ColorLogger
	cfg            *config.SmartNodeConfig
	w              *wallet.Wallet
	rp             *rocketpool.RocketPool
	mpMgr          *minipool.MinipoolManager
	gasThreshold   float64
	maxFee         *big.Int
	maxPriorityFee *big.Int
}

// Details required to check for bond reduction eligibility
type minipoolBondReductionDetails struct {
	Address             common.Address
	DepositBalance      *big.Int
	ReduceBondTime      time.Time
	ReduceBondCancelled bool
	Status              types.MinipoolStatus
}

// Create reduce bonds task
func NewReduceBonds(sp *services.ServiceProvider, logger log.ColorLogger) *ReduceBonds {
	return &ReduceBonds{
		sp:  sp,
		log: logger,
	}
}

// Reduce bonds
func (t *ReduceBonds) Run(state *state.NetworkState) error {
	// Get services
	t.cfg = t.sp.GetConfig()
	t.w = t.sp.GetWallet()
	t.rp = t.sp.GetRocketPool()
	t.w = t.sp.GetWallet()
	nodeAddress, _ := t.w.GetAddress()
	t.maxFee, t.maxPriorityFee = getAutoTxInfo(t.cfg, &t.log)
	t.gasThreshold = t.cfg.AutoTxGasThreshold.Value

	// Check if auto-bond-reduction is disabled
	if t.gasThreshold == 0 {
		t.log.Println("Automatic tx gas threshold is 0, disabling auto-reduce.")
		return nil
	}

	// Log
	t.log.Println("Checking for minipool bonds to reduce...")

	// Get the latest state
	opts := &bind.CallOpts{
		BlockNumber: big.NewInt(0).SetUint64(state.ElBlockNumber),
	}

	// Get the bond reduction details
	windowStart := state.NetworkDetails.BondReductionWindowStart
	windowLength := state.NetworkDetails.BondReductionWindowLength

	// Get the time of the latest block
	latestEth1Block, err := t.rp.Client.HeaderByNumber(context.Background(), opts.BlockNumber)
	if err != nil {
		return fmt.Errorf("can't get the latest block time: %w", err)
	}
	latestBlockTime := time.Unix(int64(latestEth1Block.Time), 0)

	// Make the minipool manager
	t.mpMgr, err = minipool.NewMinipoolManager(t.rp)
	if err != nil {
		return fmt.Errorf("error creating minipool manager: %w", err)
	}

	// Get reduceable minipools
	minipools, mpBindings, err := t.getReduceableMinipools(nodeAddress, windowStart, windowLength, latestBlockTime, state, opts)
	if err != nil {
		return err
	}
	if len(minipools) == 0 {
		return nil
	}

	// Log
	t.log.Printlnf("%d minipool(s) are ready for bond reduction...", len(minipools))

	// Workaround for the fee distribution issue
	success, err := t.forceFeeDistribution(state)
	if err != nil {
		return err
	}
	if !success {
		return nil
	}

	// Get reduce bonds submissions
	txSubmissions := make([]*eth.TransactionSubmission, len(minipools))
	for i, mpd := range minipools {
		txSubmissions[i], err = t.createReduceBondTx(mpd)
		if err != nil {
			t.log.Println(fmt.Errorf("error preparing submission to reduce bond for minipool %s: %w", mpd.MinipoolAddress.Hex(), err))
			return err
		}
	}

	// Reduce bonds
	err = t.reduceBonds(txSubmissions, mpBindings, windowStart+windowLength, latestBlockTime)
	if err != nil {
		return fmt.Errorf("error reducing minipool bonds: %w", err)
	}

	// Return
	return nil

}

// Temp mitigation for the Dybsy bug
func (t *ReduceBonds) forceFeeDistribution(state *state.NetworkState) (bool, error) {
	nodeAddress, _ := t.w.GetAddress()
	distributorAddress := state.NodeDetailsByAddress[nodeAddress].FeeDistributorAddress

	// Get fee distributor
	distributor, err := node.NewNodeDistributor(t.rp, nodeAddress, distributorAddress)
	if err != nil {
		return false, fmt.Errorf("error creating fee distributor binding for node %s: %w", nodeAddress.Hex(), err)
	}

	// Sync
	var wg errgroup.Group
	var balanceRaw *big.Int
	var nodeShare float64

	// Get the contract's balance
	wg.Go(func() error {
		var err error
		balanceRaw, err = t.rp.Client.BalanceAt(context.Background(), distributorAddress, nil)
		return err
	})

	// Get the node share of the balance
	wg.Go(func() error {
		err = t.rp.Query(nil, nil, distributor.NodeShare)
		if err != nil {
			return fmt.Errorf("error getting node share for distributor %s: %w", distributorAddress.Hex(), err)
		}
		nodeShare = eth.WeiToEth(distributor.NodeShare.Get())
		return nil
	})

	// Wait for data
	if err := wg.Wait(); err != nil {
		return false, err
	}

	balance := eth.WeiToEth(balanceRaw)
	if balance == 0 {
		t.log.Println("Your fee distributor does not have any ETH and does not need to be distributed.")
		return true, nil
	}
	t.log.Println("NOTE: prior to bond reduction, you must distribute the funds in your fee distributor.")

	// Print info
	rEthShare := balance - nodeShare
	t.log.Printlnf("Your fee distributor's balance of %.6f ETH will be distributed as follows:\n", balance)
	t.log.Printlnf("\tYour withdrawal address will receive %.6f ETH.", nodeShare)
	t.log.Printlnf("\trETH pool stakers will receive %.6f ETH.\n", rEthShare)

	opts, err := t.w.GetTransactor()
	if err != nil {
		return false, err
	}

	// Get the gas limit
	txInfo, err := distributor.Distribute(opts)
	if err != nil {
		return false, fmt.Errorf("could not get TX info for distributing node fees: %w", err)
	}
	if txInfo.SimulationResult.SimulationError != "" {
		return false, fmt.Errorf("simulating distribute node fees failed: %s", txInfo.SimulationResult.SimulationError)
	}

	// Get the max fee
	maxFee := t.maxFee
	if maxFee == nil || maxFee.Uint64() == 0 {
		maxFee, err = gas.GetMaxFeeWeiForDaemon(&t.log)
		if err != nil {
			return false, err
		}
	}

	// Print the gas info
	if !gas.PrintAndCheckGasInfo(txInfo.SimulationResult, true, t.gasThreshold, &t.log, maxFee, txInfo.SimulationResult.SafeGasLimit) {
		return false, nil
	}

	opts.GasFeeCap = maxFee
	opts.GasTipCap = t.maxPriorityFee
	opts.GasLimit = txInfo.SimulationResult.SafeGasLimit

	// Print TX info and wait for it to be included in a block
	err = tx.PrintAndWaitForTransaction(t.cfg, t.rp, &t.log, txInfo, opts)
	if err != nil {
		return false, err
	}

	// Log & return
	fmt.Println("Successfully distributed your fee distributor's balance. Your rewards should arrive in your withdrawal address shortly.")
	return true, nil
}

// Get reduceable minipools
func (t *ReduceBonds) getReduceableMinipools(nodeAddress common.Address, windowStart time.Duration, windowLength time.Duration, latestBlockTime time.Time, state *state.NetworkState, opts *bind.CallOpts) ([]*rpstate.NativeMinipoolDetails, []*minipool.MinipoolV3, error) {
	// Get MP bindings for each details
	mps := []*minipool.MinipoolV3{}
	mpMap := map[*rpstate.NativeMinipoolDetails]*minipool.MinipoolV3{}
	for _, mpd := range state.MinipoolDetailsByNode[nodeAddress] {
		mp, err := t.mpMgr.NewMinipoolFromVersion(mpd.MinipoolAddress, mpd.Version)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating minipool %s binding: %w", mpd.MinipoolAddress.Hex(), err)
		}
		mpv3, success := minipool.GetMinipoolAsV3(mp)
		if !success {
			continue
		}
		mps = append(mps, mpv3)
		mpMap[mpd] = mpv3
	}

	// Get bond reduction details
	err := t.rp.BatchQuery(len(mps), bondReductionBatchSize, func(mc *batch.MultiCaller, i int) error {
		eth.AddQueryablesToMulticall(mc,
			mps[i].ReduceBondTime,
			mps[i].IsBondReduceCancelled,
		)
		return nil
	}, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("error retrieving minipool bond reduction details: %w", err)
	}

	// Filter minipools
	reduceableMinipools := []*rpstate.NativeMinipoolDetails{}
	mpBindings := []*minipool.MinipoolV3{}
	for _, mpd := range state.MinipoolDetailsByNode[nodeAddress] {
		mpv3 := mpMap[mpd]
		depositBalance := eth.WeiToEth(mpd.NodeDepositBalance)
		timeSinceReductionStart := latestBlockTime.Sub(mpv3.ReduceBondTime.Formatted())

		if depositBalance == 16 &&
			timeSinceReductionStart < (windowStart+windowLength) &&
			!mpv3.IsBondReduceCancelled.Get() &&
			mpd.Status == types.MinipoolStatus_Staking {
			if timeSinceReductionStart > windowStart {
				reduceableMinipools = append(reduceableMinipools, mpd)
				mpBindings = append(mpBindings, mpv3)
			} else {
				remainingTime := windowStart - timeSinceReductionStart
				t.log.Printlnf("Minipool %s has %s left until it can have its bond reduced.", mpd.MinipoolAddress.Hex(), remainingTime)
			}
		}
	}

	// Return
	return reduceableMinipools, mpBindings, nil
}

// Get submission info for reducing a minipool's bond
func (t *ReduceBonds) createReduceBondTx(mpd *rpstate.NativeMinipoolDetails) (*eth.TransactionSubmission, error) {
	// Log
	t.log.Printlnf("Preparing to reduce bond for minipool %s...", mpd.MinipoolAddress.Hex())

	// Get transactor
	opts, err := t.w.GetTransactor()
	if err != nil {
		return nil, err
	}

	// Make the minipool binding
	mpBinding, err := t.mpMgr.NewMinipoolFromVersion(mpd.MinipoolAddress, mpd.Version)
	if err != nil {
		return nil, fmt.Errorf("error creating minipool %s binding: %w", mpd.MinipoolAddress.Hex(), err)
	}
	mpv3, success := minipool.GetMinipoolAsV3(mpBinding)
	if !success {
		return nil, fmt.Errorf("cannot reduce bond for minipool %s because its delegate version is too low (v%d); please update the delegate", mpd.MinipoolAddress.Hex(), mpd.Version)
	}

	// Get the tx info
	txInfo, err := mpv3.ReduceBondAmount(opts)
	if err != nil {
		return nil, fmt.Errorf("error getting reduce bond TX info for minipool %s: %w", mpd.MinipoolAddress.Hex(), err)
	}
	if txInfo.SimulationResult.SimulationError != "" {
		return nil, fmt.Errorf("simulating reduce bond TX for minipool %s failed: %s", mpd.MinipoolAddress.Hex(), txInfo.SimulationResult.SimulationError)
	}

	submission, err := eth.CreateTxSubmissionFromInfo(txInfo, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating distribute tx submission for minipool %s: %w", mpd.MinipoolAddress.Hex(), err)
	}
	return submission, nil
}

// Reduce bonds for all available minipools
func (t *ReduceBonds) reduceBonds(submissions []*eth.TransactionSubmission, minipools []*minipool.MinipoolV3, windowDuration time.Duration, latestBlockTime time.Time) error {
	// Get transactor
	opts, err := t.w.GetTransactor()
	if err != nil {
		return err
	}

	// Get the max fee
	maxFee := t.maxFee
	if maxFee == nil || maxFee.Uint64() == 0 {
		maxFee, err = gas.GetMaxFeeWeiForDaemon(&t.log)
		if err != nil {
			return err
		}
	}
	opts.GasFeeCap = maxFee
	opts.GasTipCap = t.maxPriorityFee

	// Print the gas info
	if !gas.PrintAndCheckGasInfoForBatch(submissions, true, t.gasThreshold, &t.log, maxFee) {
		for _, mp := range minipools {
			timeSinceReductionStart := latestBlockTime.Sub(mp.ReduceBondTime.Formatted())
			remainingTime := windowDuration - timeSinceReductionStart
			t.log.Printlnf("Time until bond reduction times out for minipool %s: %s", mp.Address.Hex(), remainingTime)
		}
		return nil
	}

	// Create callbacks
	callbacks := make([]func(err error), len(minipools))
	for i, mp := range minipools {
		callbacks[i] = func(err error) {
			alerting.AlertMinipoolBondReduced(t.cfg, mp.Address, err == nil)
		}
	}

	// Print TX info and wait for them to be included in a block
	err = tx.PrintAndWaitForTransactionBatch(t.cfg, t.rp, &t.log, submissions, callbacks, opts)
	if err != nil {
		return err
	}

	// Log
	t.log.Println("Successfully reduced bond of all minipools.")
	return nil
}
