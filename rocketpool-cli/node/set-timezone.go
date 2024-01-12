package node

import (
	"fmt"

	"github.com/urfave/cli/v2"

	"github.com/rocket-pool/smartnode/rocketpool-cli/utils/client"
	"github.com/rocket-pool/smartnode/rocketpool-cli/utils/tx"
)

const (
	timezoneFlag string = "timezone"
)

func setTimezoneLocation(c *cli.Context) error {
	// Get RP client
	rp, err := client.NewClientFromCtx(c).WithReady()
	if err != nil {
		return err
	}

	// Prompt for timezone location
	var timezoneLocation string
	if c.String(timezoneFlag) != "" {
		timezoneLocation = c.String(timezoneFlag)
	} else {
		timezoneLocation = promptTimezone()
	}

	// Get the TX
	response, err := rp.Api.Node.SetTimezone(timezoneLocation)
	if err != nil {
		return err
	}

	// Run the TX
	err = tx.HandleTx(c, rp, response.Data.TxInfo,
		"Are you sure you want to set your timezone?",
		"timezone change",
		"Setting timezone...",
	)

	// Log & return
	fmt.Printf("The node's timezone location was successfully updated to '%s'.\n", timezoneLocation)
	return nil
}
