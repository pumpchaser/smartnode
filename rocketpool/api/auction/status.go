package auction

import (
	"fmt"
	"math/big"

	batch "github.com/rocket-pool/batch-query"
	"github.com/rocket-pool/rocketpool-go/auction"
	"github.com/rocket-pool/rocketpool-go/network"
	"github.com/rocket-pool/rocketpool-go/settings"
	"github.com/rocket-pool/rocketpool-go/utils/eth"

	"github.com/rocket-pool/smartnode/shared/types/api"
)

// ===============
// === Factory ===
// ===============

type auctionStatusContextFactory struct {
	h *AuctionHandler
}

func (f *auctionStatusContextFactory) Create(vars map[string]string) (*auctionStatusContext, error) {
	c := &auctionStatusContext{
		h: f.h,
	}
	return c, nil
}

func (f *auctionStatusContextFactory) Run(c *auctionStatusContext) (*api.ApiResponse[api.AuctionStatusData], error) {
	return runAuctionCall[api.AuctionStatusData](c)
}

// ===============
// === Context ===
// ===============

// Lot count details
type lotCountDetails struct {
	AddressHasBid   bool
	Cleared         bool
	HasRemainingRpl bool
	RplRecovered    bool
}

type auctionStatusContext struct {
	h             *AuctionHandler
	auctionMgr    *auction.AuctionManager
	pSettings     *settings.ProtocolDaoSettings
	networkPrices *network.NetworkPrices
	*commonContext
}

func (c *auctionStatusContext) CreateBindings(ctx *commonContext) error {
	var err error
	c.commonContext = ctx

	c.auctionMgr, err = auction.NewAuctionManager(c.rp)
	if err != nil {
		return fmt.Errorf("error creating auction manager binding: %w", err)
	}
	c.pSettings, err = settings.NewProtocolDaoSettings(c.rp)
	if err != nil {
		return fmt.Errorf("error creating pDAO settings binding: %w", err)
	}
	c.networkPrices, err = network.NewNetworkPrices(c.rp)
	if err != nil {
		return fmt.Errorf("error creating network prices binding: %w", err)
	}
	return nil
}

func (c *auctionStatusContext) GetState(mc *batch.MultiCaller) {
	c.auctionMgr.GetTotalRPLBalance(mc)
	c.auctionMgr.GetAllottedRPLBalance(mc)
	c.auctionMgr.GetRemainingRPLBalance(mc)
	c.auctionMgr.GetLotCount(mc)
	c.pSettings.GetAuctionLotMinimumEthValue(mc)
	c.networkPrices.GetRplPrice(mc)
	c.pSettings.GetCreateAuctionLotEnabled(mc)
}

func (c *auctionStatusContext) PrepareData(data *api.AuctionStatusData) error {
	// Check the balance requirement
	lotMinimumRplAmount := big.NewInt(0).Mul(c.pSettings.Details.Auction.LotMinimumEthValue, eth.EthToWei(1))
	lotMinimumRplAmount.Quo(lotMinimumRplAmount, c.networkPrices.Details.RplPrice.RawValue)
	sufficientRemainingRplForLot := (c.auctionMgr.Details.RemainingRplBalance.Cmp(lotMinimumRplAmount) >= 0)

	// Get lot counts
	lotCountDetails, err := c.getAllLotCountDetails(c.auctionMgr.Details.LotCount.Formatted())
	if err != nil {
		return fmt.Errorf("error getting auction lot count details: %w", err)
	}
	for _, details := range lotCountDetails {
		if details.AddressHasBid && details.Cleared {
			data.LotCounts.ClaimAvailable++
		}
		if !details.Cleared && details.HasRemainingRpl {
			data.LotCounts.BiddingAvailable++
		}
		if details.Cleared && details.HasRemainingRpl && !details.RplRecovered {
			data.LotCounts.RplRecoveryAvailable++
		}
	}

	// Set response details
	data.TotalRplBalance = c.auctionMgr.Details.TotalRplBalance
	data.AllottedRplBalance = c.auctionMgr.Details.AllottedRplBalance
	data.RemainingRplBalance = c.auctionMgr.Details.RemainingRplBalance
	data.CanCreateLot = sufficientRemainingRplForLot
	return nil
}

// Get all lot count details
func (c *auctionStatusContext) getAllLotCountDetails(lotCount uint64) ([]lotCountDetails, error) {
	details := make([]lotCountDetails, lotCount)
	lots := make([]*auction.AuctionLot, lotCount)
	addressBids := make([]*big.Int, lotCount)

	// Load details
	err := c.rp.BatchQuery(int(lotCount), int(lotCountDetailsBatchSize), func(mc *batch.MultiCaller, i int) error {
		lot, err := auction.NewAuctionLot(c.rp, uint64(i))
		if err != nil {
			return fmt.Errorf("error creating lot %d binding: %w", i, err)
		}
		lots[i] = lot

		lot.GetLotAddressBidAmount(mc, &addressBids[i], c.nodeAddress)
		lot.GetLotIsCleared(mc)
		lot.GetLotRemainingRplAmount(mc)
		lot.GetLotRplRecovered(mc)
		return nil
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("error getting lot count details: %w", err)
	}

	for i := 0; i < int(lotCount); i++ {
		details[i].AddressHasBid = (addressBids[i].Cmp(big.NewInt(0)) > 0)
		details[i].Cleared = lots[i].Details.IsCleared
		details[i].HasRemainingRpl = (lots[i].Details.RemainingRplAmount.Cmp(big.NewInt(0)) > 0)
		details[i].RplRecovered = lots[i].Details.RplRecovered
	}

	// Return
	return details, nil
}
