package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// privateNetworkFabricStore applies the dark-launch gate and keeps the new
// resource surface compatible with older Store test doubles.
func (s *server) privateNetworkFabricStore(w http.ResponseWriter, r *http.Request) (state.PrivateNetworkStore, bool) {
	if !api.PrivateNetworkFabricEnabled() {
		api.WriteProblem(w, api.ErrPrivateNetworkNotEnabled())
		return nil, false
	}
	store, ok := s.store.(state.PrivateNetworkStore)
	if !ok {
		api.WriteProblem(w, api.ErrPrivateNetworkNotEnabled())
		return nil, false
	}
	return store, true
}

func (s *server) listPrivateNetworks(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.privateNetworkFabricStore(w, r)
	if !ok {
		return
	}
	networks, err := store.ListPrivateNetworks(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list private networks"))
		return
	}
	out := api.PrivateNetworkListResponse{Networks: make([]api.PrivateNetwork, 0, len(networks))}
	for _, network := range networks {
		out.Networks = append(out.Networks, privateNetworkResponse(network))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createPrivateNetwork(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.privateNetworkFabricStore(w, r)
	if !ok {
		return
	}
	if !acct.Plan.PrivateNetworkAllowed() {
		api.WriteProblem(w, api.ErrPlanPrivateNetworkNotAllowed(acct.Plan))
		return
	}
	var req api.CreatePrivateNetworkRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrPrivateNetworkInvalid("body", "", "invalid JSON"))
		return
	}
	name := strings.TrimSpace(req.Name)
	region := strings.TrimSpace(req.Region)
	if err := api.ValidatePrivateNetworkIdentifier(name); err != nil {
		api.WriteProblem(w, api.ErrPrivateNetworkInvalid("name", req.Name, err.Error()))
		return
	}
	if err := api.ValidatePrivateNetworkIdentifier(region); err != nil {
		api.WriteProblem(w, api.ErrPrivateNetworkInvalid("region", req.Region, err.Error()))
		return
	}
	cidr, err := api.ValidatePrivateNetworkCIDR(req.CIDR)
	if err != nil {
		api.WriteProblem(w, api.ErrPrivateNetworkInvalid("cidr", req.CIDR, err.Error()))
		return
	}
	network, err := store.CreatePrivateNetwork(r.Context(), state.PrivateNetwork{
		ID:        "net-" + uuid.NewString(),
		AccountID: acct.ID,
		Name:      name,
		Region:    region,
		CIDR:      cidr,
		Status:    api.PrivateNetworkStatusReady,
	})
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
				"Private network conflicts with an existing network",
				"choose a different name or a non-overlapping CIDR in this region").WithDocs("https://gregale.dev/docs/networking"))
			return
		}
		if errors.Is(err, state.ErrInvalidArgument) {
			api.WriteProblem(w, api.ErrPrivateNetworkInvalid("network", name, "the network definition is not accepted"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not create private network"))
		return
	}
	s.audit.Emit(r.Context(), "private_network.created", &acct.ID, map[string]any{
		"network_id": network.ID, "name": network.Name, "region": network.Region, "cidr": network.CIDR.String(),
	})
	writeJSON(w, http.StatusCreated, privateNetworkResponse(network))
}

func (s *server) getPrivateNetwork(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.privateNetworkFabricStore(w, r)
	if !ok {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if err := api.ValidatePrivateNetworkIdentifier(id); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Private network not found", "the requested network does not exist"))
		return
	}
	network, err := store.GetPrivateNetwork(r.Context(), acct.ID, id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Private network not found", "the requested network does not exist"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not read private network"))
		return
	}
	writeJSON(w, http.StatusOK, privateNetworkResponse(network))
}

func (s *server) deletePrivateNetwork(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.privateNetworkFabricStore(w, r)
	if !ok {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if err := api.ValidatePrivateNetworkIdentifier(id); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Private network not found", "the requested network does not exist"))
		return
	}
	err := store.DeletePrivateNetwork(r.Context(), acct.ID, id)
	if err != nil {
		switch {
		case errors.Is(err, state.ErrNotFound):
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Private network not found", "the requested network does not exist"))
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Private network is still attached", "detach all apps before deleting the network"))
		default:
			api.WriteProblem(w, api.ErrCapacity("could not delete private network"))
		}
		return
	}
	s.audit.Emit(r.Context(), "private_network.deleted", &acct.ID, map[string]any{"network_id": id})
	w.WriteHeader(http.StatusNoContent)
}

func privateNetworkResponse(network state.PrivateNetwork) api.PrivateNetwork {
	createdAt, updatedAt := network.CreatedAt, network.UpdatedAt
	return api.PrivateNetwork{
		ID:           network.ID,
		Name:         network.Name,
		Region:       network.Region,
		CIDR:         network.CIDR.String(),
		Status:       network.Status,
		StatusDetail: network.StatusDetail,
		CreatedAt:    &createdAt,
		UpdatedAt:    &updatedAt,
	}
}
