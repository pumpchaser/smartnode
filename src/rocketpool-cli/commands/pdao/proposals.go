package pdao

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/rocket-pool/rocketpool-go/types"
	"github.com/urfave/cli/v2"

	"github.com/rocket-pool/smartnode/rocketpool-cli/client"
	"github.com/rocket-pool/smartnode/shared/types/api"
)

var proposalsListStatesFlag *cli.StringFlag = &cli.StringFlag{
	Name:    "states",
	Aliases: []string{"s"},
	Usage:   "Comma separated list of states to filter ('pending', 'active', 'succeeded', 'executed', 'cancelled', 'defeated', or 'expired')",
	Value:   "",
}

func filterProposalState(state string, stateFilter string) bool {
	// Easy out
	if stateFilter == "" {
		return false
	}

	// Check comma separated list for the state
	filterStates := strings.Split(stateFilter, ",")
	for _, fs := range filterStates {
		if fs == state {
			return false
		}
	}

	// Not found
	return true
}

func getProposals(c *cli.Context, stateFilter string) error {
	// Get RP client
	rp, err := client.NewClientFromCtx(c).WithReady()
	if err != nil {
		return err
	}

	// Get Protocol DAO proposals
	allProposals, err := rp.Api.PDao.Proposals()
	if err != nil {
		return err
	}

	// Get proposals by state
	stateProposals := map[string][]api.ProtocolDaoProposalDetails{}
	for _, proposal := range allProposals.Data.Proposals {
		stateName := types.ProtocolDaoProposalStates[proposal.State]
		if _, ok := stateProposals[stateName]; !ok {
			stateProposals[stateName] = []api.ProtocolDaoProposalDetails{}
		}
		stateProposals[stateName] = append(stateProposals[stateName], proposal)
	}

	// Proposal states print order
	proposalStates := []string{"Pending", "Active (Phase 1)", "Active (Phase 2)", "Succeeded", "Executed", "Destroyed", "Vetoed", "Quorum not Met", "Defeated", "Expired"}
	proposalStateInputs := []string{"pending", "phase1", "phase2", "succeeded", "executed", "destroyed", "vetoed", "quorum-not-met", "defeated", "expired"}

	// Print & return
	count := 0
	for i, stateName := range proposalStates {
		proposals, ok := stateProposals[stateName]
		if !ok {
			continue
		}

		// Check filter
		if filterProposalState(proposalStateInputs[i], stateFilter) {
			continue
		}

		// Proposal state count
		fmt.Printf("%d %s proposal(s):\n", len(proposals), stateName)
		fmt.Println("")

		// Proposals
		for _, proposal := range proposals {
			fmt.Printf("%d: %s - Proposed by: %s\n", proposal.ID, proposal.Message, proposal.ProposerAddress)
		}

		count += len(proposals)

		fmt.Println()
	}
	if count == 0 {
		fmt.Println("There are no matching Protocol DAO proposals.")
	}
	return nil
}

func getProposal(c *cli.Context, id uint64) error {
	// Get RP client
	rp, err := client.NewClientFromCtx(c).WithReady()
	if err != nil {
		return err
	}

	// Get protocol DAO proposals
	allProposals, err := rp.Api.PDao.Proposals()
	if err != nil {
		return err
	}

	// Find the proposal
	var proposal *api.ProtocolDaoProposalDetails

	for i, p := range allProposals.Data.Proposals {
		if p.ID == id {
			proposal = &allProposals.Data.Proposals[i]
			break
		}
	}

	if proposal == nil {
		fmt.Printf("Proposal with ID %d does not exist.\n", id)
		return nil
	}

	// Main details
	fmt.Printf("Proposal ID:            %d\n", proposal.ID)
	fmt.Printf("Message:                %s\n", proposal.Message)
	fmt.Printf("Payload:                %s\n", proposal.PayloadStr)
	fmt.Printf("Payload (bytes):        %s\n", hex.EncodeToString(proposal.Payload))
	fmt.Printf("Proposed by:            %s\n", proposal.ProposerAddress.Hex())
	fmt.Printf("Created at:             %s\n", proposal.CreatedTime.Format(time.RFC822))
	fmt.Printf("State:                  %s\n", types.ProtocolDaoProposalStates[proposal.State])

	// Start block - pending proposals
	if proposal.State == types.ProtocolDaoProposalState_Pending {
		fmt.Printf("Voting start:           %s\n", proposal.VotingStartTime.Format(time.RFC822))
	}
	if proposal.State == types.ProtocolDaoProposalState_Pending {
		fmt.Printf("Challenge window:       %s\n", proposal.ChallengeWindow)
	}

	// End block - active proposals
	if proposal.State == types.ProtocolDaoProposalState_ActivePhase1 {
		fmt.Printf("Phase 1 end:            %s\n", proposal.Phase1EndTime.Format(time.RFC822))
	}
	if proposal.State == types.ProtocolDaoProposalState_ActivePhase2 {
		fmt.Printf("Phase 2 end:            %s\n", proposal.Phase2EndTime.Format(time.RFC822))
	}

	// Expiry block - succeeded proposals
	if proposal.State == types.ProtocolDaoProposalState_Succeeded {
		fmt.Printf("Expires at:             %s\n", proposal.ExpiryTime.Format(time.RFC822))
	}

	// Vote details
	fmt.Printf("Voting power required:  %s\n", proposal.VotingPowerRequired.String())
	fmt.Printf("Voting power for:       %s\n", proposal.VotingPowerFor.String())
	fmt.Printf("Voting power against:   %s\n", proposal.VotingPowerAgainst.String())
	fmt.Printf("Voting power abstained: %s\n", proposal.VotingPowerAbstained.String())
	fmt.Printf("Voting power against:   %s\n", proposal.VotingPowerToVeto.String())
	if proposal.NodeVoteDirection != types.VoteDirection_NoVote {
		fmt.Printf("Node has voted:         %s\n", types.VoteDirections[proposal.NodeVoteDirection])
	} else {
		fmt.Printf("Node has voted:         no\n")
	}

	return nil
}
