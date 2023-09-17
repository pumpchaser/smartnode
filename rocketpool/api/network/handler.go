package network

import (
	"github.com/gorilla/mux"

	"github.com/rocket-pool/smartnode/rocketpool/common/server"
	"github.com/rocket-pool/smartnode/rocketpool/common/services"
)

type NetworkHandler struct {
	serviceProvider *services.ServiceProvider
	factories       []server.IContextFactory
}

func NewNetworkHandler(serviceProvider *services.ServiceProvider) *NetworkHandler {
	h := &NetworkHandler{
		serviceProvider: serviceProvider,
	}
	h.factories = []server.IContextFactory{
		&networkProposalContextFactory{h},
		&networkDelegateContextFactory{h},
		&networkDepositInfoContextFactory{h},
		&networkDownloadRewardsContextFactory{h},
		&networkRewardsFileContextFactory{h},
		&networkGenerateRewardsContextFactory{h},
		&networkFeeContextFactory{h},
		&networkPriceContextFactory{h},
		&networkStatsContextFactory{h},
		&networkTimezoneContextFactory{h},
	}
	return h
}

func (h *NetworkHandler) RegisterRoutes(router *mux.Router) {
	for _, factory := range h.factories {
		factory.RegisterRoute(router)
	}
}
