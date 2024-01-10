package minipool

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/urfave/cli/v2"

	"github.com/rocket-pool/rocketpool-go/core"
	"github.com/rocket-pool/smartnode/rocketpool-cli/utils"
	"github.com/rocket-pool/smartnode/rocketpool-cli/utils/client"
	"github.com/rocket-pool/smartnode/rocketpool-cli/utils/tx"
	"github.com/rocket-pool/smartnode/shared/types/api"
)

func stakeMinipools(c *cli.Context) error {
	// Get RP client
	rp, err := client.NewClientFromCtx(c).WithReady()
	if err != nil {
		return err
	}

	// Get minipool statuses
	status, err := rp.Api.Minipool.Status()
	if err != nil {
		return err
	}

	// Get stakeable minipools
	stakeableMinipools := []api.MinipoolDetails{}
	for _, minipool := range status.Data.Minipools {
		if minipool.CanStake {
			stakeableMinipools = append(stakeableMinipools, minipool)
		}
	}

	// Check for stakeable minipools
	if len(stakeableMinipools) == 0 {
		fmt.Println("No minipools can be staked.")
		return nil
	}

	// Get selected minipools
	options := make([]utils.SelectionOption[api.MinipoolDetails], len(stakeableMinipools))
	for i, mp := range stakeableMinipools {
		option := &options[i]
		option.Element = &mp
		option.ID = fmt.Sprint(mp.Address)
		option.Display = fmt.Sprintf("%s (%s until dissolved)", mp.Address.Hex(), mp.TimeUntilDissolve)
	}
	selectedMinipools, err := utils.GetMultiselectIndices(c, minipoolsFlag, options, "Please select a minipool to stake:")
	if err != nil {
		return fmt.Errorf("error determining minipool selection: %w", err)
	}

	// Build the TXs
	addresses := make([]common.Address, len(selectedMinipools))
	for i, lot := range selectedMinipools {
		addresses[i] = lot.Address
	}
	response, err := rp.Api.Minipool.Stake(addresses)
	if err != nil {
		return fmt.Errorf("error during TX generation: %w", err)
	}

	// Validation
	txs := make([]*core.TransactionInfo, len(selectedMinipools))
	for i, minipool := range selectedMinipools {
		txInfo := response.Data.TxInfos[i]
		if txInfo.SimError != "" {
			return fmt.Errorf("error simulating stake for minipool %s: %s", minipool.Address.Hex(), txInfo.SimError)
		}
		txs[i] = txInfo
	}

	fmt.Println("\nNOTE: Your validator container will be restarted after this process so it loads the new validator key.\n")

	// Run the TXs
	err = tx.HandleTxBatch(c, rp, txs,
		fmt.Sprintf("Are you sure you want to stake %d minipools?", len(selectedMinipools)),
		"Staking minipools...",
	)
	if err != nil {
		return err
	}

	// Log & return
	fmt.Println("Successfully staked all selected minipools.")
	return nil
}
