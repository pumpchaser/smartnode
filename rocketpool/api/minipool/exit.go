package minipool

import (
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gorilla/mux"
	batch "github.com/rocket-pool/batch-query"
	"github.com/rocket-pool/rocketpool-go/minipool"
	"github.com/rocket-pool/rocketpool-go/node"
	"github.com/rocket-pool/rocketpool-go/rocketpool"

	"github.com/rocket-pool/smartnode/rocketpool/common/beacon"
	"github.com/rocket-pool/smartnode/rocketpool/common/server"
	"github.com/rocket-pool/smartnode/rocketpool/common/wallet"
	"github.com/rocket-pool/smartnode/shared/types/api"
	cliutils "github.com/rocket-pool/smartnode/shared/utils/cli"
	"github.com/rocket-pool/smartnode/shared/utils/validator"
	eth2types "github.com/wealdtech/go-eth2-types/v2"
)

// ===============
// === Factory ===
// ===============

type minipoolExitContextFactory struct {
	handler *MinipoolHandler
}

func (f *minipoolExitContextFactory) Create(vars map[string]string) (*minipoolExitContext, error) {
	c := &minipoolExitContext{
		handler: f.handler,
	}
	inputErrs := []error{
		server.ValidateArg("addresses", vars, cliutils.ValidateAddresses, &c.minipoolAddresses),
	}
	return c, errors.Join(inputErrs...)
}

func (f *minipoolExitContextFactory) RegisterRoute(router *mux.Router) {
	server.RegisterMinipoolRoute[*minipoolExitContext, api.SuccessData](
		router, "exit", f, f.handler.serviceProvider,
	)
}

// ===============
// === Context ===
// ===============

type minipoolExitContext struct {
	handler *MinipoolHandler
	rp      *rocketpool.RocketPool
	w       *wallet.LocalWallet
	bc      beacon.Client

	minipoolAddresses []common.Address
}

func (c *minipoolExitContext) Initialize() error {
	sp := c.handler.serviceProvider
	c.rp = sp.GetRocketPool()
	c.w = sp.GetWallet()
	c.bc = sp.GetBeaconClient()

	// Requirements
	err := errors.Join(
		sp.RequireNodeRegistered(),
		sp.RequireBeaconClientSynced(),
		sp.RequireWalletReady(),
	)
	if err != nil {
		return err
	}
	return nil
}

func (c *minipoolExitContext) GetState(node *node.Node, mc *batch.MultiCaller) {
}

func (c *minipoolExitContext) CheckState(node *node.Node, response *api.SuccessData) bool {
	return true
}

func (c *minipoolExitContext) GetMinipoolDetails(mc *batch.MultiCaller, mp minipool.Minipool, index int) {
	mpCommon := mp.GetMinipoolCommon()
	mpCommon.GetPubkey(mc)
}

func (c *minipoolExitContext) PrepareData(addresses []common.Address, mps []minipool.Minipool, data *api.SuccessData) error {
	// Get beacon head
	head, err := c.bc.GetBeaconHead()
	if err != nil {
		return fmt.Errorf("error getting beacon head: %w", err)
	}

	// Get voluntary exit signature domain
	signatureDomain, err := c.bc.GetDomainData(eth2types.DomainVoluntaryExit[:], head.Epoch, false)
	if err != nil {
		return fmt.Errorf("error getting beacon domain data: %w", err)
	}

	for _, mp := range mps {
		mpCommon := mp.GetMinipoolCommon()
		minipoolAddress := mpCommon.Details.Address
		validatorPubkey := mpCommon.Details.Pubkey

		// Get validator private key
		validatorKey, err := c.w.GetValidatorKeyByPubkey(validatorPubkey)
		if err != nil {
			return fmt.Errorf("error getting private key for minipool %s (pubkey %s): %w", minipoolAddress.Hex(), validatorPubkey.Hex(), err)
		}

		// Get validator index
		validatorIndex, err := c.bc.GetValidatorIndex(validatorPubkey)
		if err != nil {
			return fmt.Errorf("error getting index of minipool %s (pubkey %s): %w", minipoolAddress.Hex(), validatorPubkey.Hex(), err)
		}

		// Get signed voluntary exit message
		signature, err := validator.GetSignedExitMessage(validatorKey, validatorIndex, head.Epoch, signatureDomain)
		if err != nil {
			return fmt.Errorf("error getting exit message signature for minipool %s (pubkey %s): %w", minipoolAddress.Hex(), validatorPubkey.Hex(), err)
		}

		// Broadcast voluntary exit message
		if err := c.bc.ExitValidator(validatorIndex, head.Epoch, signature); err != nil {
			return fmt.Errorf("error submitting exit message for minipool %s (pubkey %s): %w", minipoolAddress.Hex(), validatorPubkey.Hex(), err)
		}
	}
	data.Success = true
	return nil
}
