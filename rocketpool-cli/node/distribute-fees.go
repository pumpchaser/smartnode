package node

import (
	"fmt"

	"github.com/urfave/cli/v2"

	"github.com/rocket-pool/rocketpool-go/utils/eth"
	"github.com/rocket-pool/smartnode/rocketpool-cli/utils/client"
	"github.com/rocket-pool/smartnode/rocketpool-cli/utils/tx"
)

func distribute(c *cli.Context) error {
	// Get RP client
	rp, err := client.NewClientFromCtx(c).WithReady()
	if err != nil {
		return err
	}

	// Check if it's already initialized
	initResponse, err := rp.Api.Node.InitializeFeeDistributor()
	if err != nil {
		return err
	}
	if !initResponse.Data.IsInitialized {
		fmt.Println("Your fee distributor has not been initialized yet so you cannot distribute its balance.\nPlease run `rocketpool node initialize-fee-distributor` to create it first.")
		return nil
	}

	// Build the TX
	response, err := rp.Api.Node.Distribute()
	if err != nil {
		return err
	}

	// Verify
	balance := eth.WeiToEth(response.Data.Balance)
	if balance == 0 {
		fmt.Printf("Your fee distributor does not have any ETH.")
		return nil
	}

	// Print info
	rEthShare := balance - eth.WeiToEth(response.Data.NodeShare)
	fmt.Printf("Your fee distributor's balance of %.6f ETH will be distributed as follows:\n", balance)
	fmt.Printf("\tYour withdrawal address will receive %.6f ETH.\n", eth.WeiToEth(response.Data.NodeShare))
	fmt.Printf("\trETH pool stakers will receive %.6f ETH.\n\n", rEthShare)

	// Run the TX
	err = tx.HandleTx(c, rp, response.Data.TxInfo,
		"Are you sure you want to distribute the ETH from your node's fee distributor?",
		"distributing rewards",
		"Distributing rewards...",
	)

	// Log & return
	fmt.Println("Successfully distributed your fee distributor's balance. Your rewards should arrive in your withdrawal address shortly.")
	return nil
}
