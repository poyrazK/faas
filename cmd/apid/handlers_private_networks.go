package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
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

func (s *server) privateNetworkPeeringStore(w http.ResponseWriter, r *http.Request) (state.PrivateNetworkPeeringStore, bool) {
	base, ok := s.privateNetworkFabricStore(w, r)
	if !ok {
		return nil, false
	}
	store, ok := base.(state.PrivateNetworkPeeringStore)
	if !ok {
		api.WriteProblem(w, api.ErrPrivateNetworkNotEnabled())
		return nil, false
	}
	return store, true
}

func (s *server) listPrivateNetworkPeerings(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.privateNetworkPeeringStore(w, r)
	if !ok {
		return
	}
	networkID := strings.TrimSpace(r.PathValue("id"))
	if err := api.ValidatePrivateNetworkIdentifier(networkID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Private network not found", "the requested network does not exist"))
		return
	}
	if _, err := store.GetPrivateNetwork(r.Context(), acct.ID, networkID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Private network not found", "the requested network does not exist"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not read private network"))
		return
	}
	peerings, err := store.ListPrivateNetworkPeerings(r.Context(), acct.ID, networkID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list private network peerings"))
		return
	}
	out := api.PrivateNetworkPeeringListResponse{Peerings: make([]api.PrivateNetworkPeering, 0, len(peerings))}
	for _, peering := range peerings {
		out.Peerings = append(out.Peerings, privateNetworkPeeringResponse(peering, networkID))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createPrivateNetworkPeering(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.privateNetworkPeeringStore(w, r)
	if !ok {
		return
	}
	if !acct.Plan.PrivateNetworkAllowed() {
		api.WriteProblem(w, api.ErrPlanPrivateNetworkNotAllowed(acct.Plan))
		return
	}
	networkID := strings.TrimSpace(r.PathValue("id"))
	if err := api.ValidatePrivateNetworkIdentifier(networkID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Private network not found", "the requested network does not exist"))
		return
	}
	network, err := store.GetPrivateNetwork(r.Context(), acct.ID, networkID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Private network not found", "the requested network does not exist"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not read private network"))
		return
	}
	var req api.CreatePrivateNetworkPeeringRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrPrivateNetworkInvalid("body", "", "invalid JSON"))
		return
	}
	peerNetworkID := strings.TrimSpace(req.PeerNetworkID)
	if err := api.ValidatePrivateNetworkIdentifier(peerNetworkID); err != nil {
		api.WriteProblem(w, api.ErrPrivateNetworkInvalid("peer_network_id", req.PeerNetworkID, err.Error()))
		return
	}
	peerNetwork, err := store.GetPrivateNetwork(r.Context(), acct.ID, peerNetworkID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Peer private network not found", "the requested peer network does not exist"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not read peer private network"))
		return
	}
	peering, err := store.CreatePrivateNetworkPeering(r.Context(), state.PrivateNetworkPeering{
		ID:             "peer-" + uuid.NewString(),
		AccountID:      acct.ID,
		LeftNetworkID:  network.ID,
		RightNetworkID: peerNetwork.ID,
		Region:         network.Region,
		Status:         api.PrivateNetworkPeeringStatusPending,
		StatusDetail:   "network route convergence is pending",
	})
	if err != nil {
		switch {
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Private network peering already exists", "the two networks are already peered"))
		case errors.Is(err, state.ErrInvalidArgument):
			api.WriteProblem(w, api.ErrPrivateNetworkInvalid("peer_network_id", peerNetworkID, "the networks must be in the same region and use non-overlapping CIDRs"))
		case errors.Is(err, state.ErrNotFound):
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Private network not found", "the requested network does not exist"))
		default:
			api.WriteProblem(w, api.ErrCapacity("could not create private network peering"))
		}
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyPrivateNetworkChanged, fmt.Sprintf(`{"kind":"private_network_peering","account_id":"%s","peering_id":"%s","region":"%s","left_network_id":"%s","right_network_id":"%s","status":"%s"}`, acct.ID, peering.ID, peering.Region, peering.LeftNetworkID, peering.RightNetworkID, peering.Status))
	s.audit.Emit(r.Context(), "private_network.peering_created", &acct.ID, map[string]any{"peering_id": peering.ID, "network_id": networkID, "peer_network_id": peerNetworkID, "status": peering.Status})
	converted := privateNetworkPeeringResponse(peering, networkID)
	writeJSON(w, http.StatusAccepted, converted)
}

func (s *server) getPrivateNetworkPeering(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.privateNetworkPeeringStore(w, r)
	if !ok {
		return
	}
	networkID := strings.TrimSpace(r.PathValue("id"))
	peeringID := strings.TrimSpace(r.PathValue("peer_id"))
	if api.ValidatePrivateNetworkIdentifier(networkID) != nil || api.ValidatePrivateNetworkIdentifier(peeringID) != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Private network peering not found", "the requested peering does not exist"))
		return
	}
	peering, err := store.GetPrivateNetworkPeering(r.Context(), acct.ID, peeringID)
	if err != nil || (peering.LeftNetworkID != networkID && peering.RightNetworkID != networkID) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Private network peering not found", "the requested peering does not exist"))
		return
	}
	writeJSON(w, http.StatusOK, privateNetworkPeeringResponse(peering, networkID))
}

func (s *server) deletePrivateNetworkPeering(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.privateNetworkPeeringStore(w, r)
	if !ok {
		return
	}
	networkID := strings.TrimSpace(r.PathValue("id"))
	peeringID := strings.TrimSpace(r.PathValue("peer_id"))
	if api.ValidatePrivateNetworkIdentifier(networkID) != nil || api.ValidatePrivateNetworkIdentifier(peeringID) != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Private network peering not found", "the requested peering does not exist"))
		return
	}
	peering, err := store.GetPrivateNetworkPeering(r.Context(), acct.ID, peeringID)
	if err != nil || (peering.LeftNetworkID != networkID && peering.RightNetworkID != networkID) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Private network peering not found", "the requested peering does not exist"))
		return
	}
	if err := store.DeletePrivateNetworkPeering(r.Context(), acct.ID, peeringID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Private network peering not found", "the requested peering does not exist"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not delete private network peering"))
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyPrivateNetworkChanged, fmt.Sprintf(`{"kind":"private_network_peering","account_id":"%s","peering_id":"%s","region":"%s","left_network_id":"%s","right_network_id":"%s","status":"deleted"}`, acct.ID, peeringID, peering.Region, peering.LeftNetworkID, peering.RightNetworkID))
	s.audit.Emit(r.Context(), "private_network.peering_deleted", &acct.ID, map[string]any{"peering_id": peeringID, "network_id": networkID})
	w.WriteHeader(http.StatusNoContent)
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
	allowedCIDRs, err := api.ValidatePrivateNetworkPolicyCIDRs(req.AllowedCIDRs, []netip.Prefix{cidr})
	if err != nil {
		api.WriteProblem(w, api.ErrPrivateNetworkInvalid("allowed_cidrs", "", err.Error()))
		return
	}
	firewallRules, err := api.ValidatePrivateNetworkFirewallRules(req.FirewallRules, cidr)
	if err != nil {
		api.WriteProblem(w, api.ErrPrivateNetworkInvalid("firewall_rules", "", err.Error()))
		return
	}
	network, err := store.CreatePrivateNetwork(r.Context(), state.PrivateNetwork{
		ID:            "net-" + uuid.NewString(),
		AccountID:     acct.ID,
		Name:          name,
		Region:        region,
		CIDR:          cidr,
		AllowedCIDRs:  allowedCIDRs,
		FirewallRules: firewallRules,
		Status:        api.PrivateNetworkStatusReady,
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

func (s *server) updatePrivateNetworkPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	baseStore, ok := s.privateNetworkFabricStore(w, r)
	if !ok {
		return
	}
	store, ok := baseStore.(state.PrivateNetworkPolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrPrivateNetworkNotEnabled())
		return
	}
	if !acct.Plan.PrivateNetworkAllowed() {
		api.WriteProblem(w, api.ErrPlanPrivateNetworkNotAllowed(acct.Plan))
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if err := api.ValidatePrivateNetworkIdentifier(id); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Private network not found", "the requested network does not exist"))
		return
	}
	var req api.UpdatePrivateNetworkPolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrPrivateNetworkInvalid("body", "", "invalid JSON"))
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
	allowedCIDRs, err := api.ValidatePrivateNetworkPolicyCIDRs(req.AllowedCIDRs, []netip.Prefix{network.CIDR})
	if err != nil {
		api.WriteProblem(w, api.ErrPrivateNetworkInvalid("allowed_cidrs", "", err.Error()))
		return
	}
	firewallRules, err := api.ValidatePrivateNetworkFirewallRules(req.FirewallRules, network.CIDR)
	if err != nil {
		api.WriteProblem(w, api.ErrPrivateNetworkInvalid("firewall_rules", "", err.Error()))
		return
	}
	var updated state.PrivateNetwork
	if firewallStore, supported := baseStore.(state.PrivateNetworkFirewallPolicyStore); supported {
		updated, err = firewallStore.UpdatePrivateNetworkFirewallPolicy(r.Context(), acct.ID, id, allowedCIDRs, firewallRules)
	} else {
		updated, err = store.UpdatePrivateNetworkPolicy(r.Context(), acct.ID, id, allowedCIDRs)
	}
	if err != nil {
		switch {
		case errors.Is(err, state.ErrNotFound):
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Private network not found", "the requested network does not exist"))
		case errors.Is(err, state.ErrInvalidArgument):
			api.WriteProblem(w, api.ErrPrivateNetworkInvalid("firewall_rules", "", "the network policy is not accepted"))
		default:
			api.WriteProblem(w, api.ErrCapacity("could not update private network policy"))
		}
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyPrivateNetworkChanged, fmt.Sprintf(`{"kind":"private_network","account_id":"%s","network_id":"%s","region":"%s","status":"policy_updated"}`, acct.ID, updated.ID, updated.Region))
	s.audit.Emit(r.Context(), "private_network.policy_updated", &acct.ID, map[string]any{"network_id": id, "allowed_cidrs": req.AllowedCIDRs, "firewall_rules": req.FirewallRules})
	writeJSON(w, http.StatusOK, privateNetworkResponse(updated))
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

func (s *server) listPrivateNetworkMembers(w http.ResponseWriter, r *http.Request, acct state.Account) {
	baseStore, ok := s.privateNetworkFabricStore(w, r)
	if !ok {
		return
	}
	store, ok := baseStore.(state.PrivateNetworkAddressListStore)
	if !ok {
		api.WriteProblem(w, api.ErrPrivateNetworkNotEnabled())
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
	addresses, err := store.ListPrivateNetworkAddresses(r.Context(), acct.ID, id)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list private network members"))
		return
	}
	members := make([]api.PrivateNetworkMember, 0, len(addresses))
	for _, address := range addresses {
		member := api.PrivateNetworkMember{
			ID: address.ID, OwnerType: address.OwnerType, OwnerID: address.OwnerID,
			Address: address.Address.String(),
		}
		if !address.CreatedAt.IsZero() {
			createdAt := address.CreatedAt.UTC()
			member.CreatedAt = &createdAt
		}
		members = append(members, member)
	}
	capacity := state.PrivateNetworkAddressCapacity(network.CIDR)
	available := capacity - len(members)
	if available < 0 {
		available = 0
	}
	writeJSON(w, http.StatusOK, api.PrivateNetworkMembersResponse{
		NetworkID: id, CIDR: network.CIDR.String(), Capacity: capacity,
		Used: len(members), Available: available, Members: members,
	})
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
	network, err := store.GetPrivateNetwork(r.Context(), acct.ID, id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Private network not found", "the requested network does not exist"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not read private network"))
		return
	}
	durableStore, atomicDeletion := store.(state.PrivateNetworkDurableDeletionStore)
	if atomicDeletion {
		err = durableStore.DeletePrivateNetworkDurably(r.Context(), acct.ID, id)
	} else {
		err = store.DeletePrivateNetwork(r.Context(), acct.ID, id)
	}
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
	if !atomicDeletion {
		// MemStore and older test doubles keep their notifier seam. PgStore
		// already committed the durable outbox row with the deletion.
		_ = s.notif.Notify(r.Context(), db.NotifyPrivateNetworkChanged, fmt.Sprintf(`{"kind":"private_network_deleted","account_id":"%s","network_id":"%s","region":"%s","cidr":"%s"}`, acct.ID, network.ID, network.Region, network.CIDR.String()))
	}
	s.audit.Emit(r.Context(), "private_network.deleted", &acct.ID, map[string]any{"network_id": id})
	w.WriteHeader(http.StatusNoContent)
}

func privateNetworkResponse(network state.PrivateNetwork) api.PrivateNetwork {
	createdAt, updatedAt := network.CreatedAt, network.UpdatedAt
	return api.PrivateNetwork{
		ID:            network.ID,
		Name:          network.Name,
		Region:        network.Region,
		CIDR:          network.CIDR.String(),
		AllowedCIDRs:  privateNetworkPolicyStrings(network.AllowedCIDRs),
		FirewallRules: network.FirewallRules,
		Status:        network.Status,
		StatusDetail:  network.StatusDetail,
		CreatedAt:     &createdAt,
		UpdatedAt:     &updatedAt,
	}
}

func privateNetworkPeeringResponse(peering state.PrivateNetworkPeering, networkID string) api.PrivateNetworkPeering {
	peerID := peering.LeftNetworkID
	if peerID == networkID {
		peerID = peering.RightNetworkID
	}
	createdAt, updatedAt := peering.CreatedAt, peering.UpdatedAt
	return api.PrivateNetworkPeering{ID: peering.ID, NetworkID: networkID, PeerNetworkID: peerID, Region: peering.Region, Status: peering.Status, StatusDetail: peering.StatusDetail, CreatedAt: &createdAt, UpdatedAt: &updatedAt}
}

func privateNetworkPolicyStrings(prefixes []netip.Prefix) []string {
	if len(prefixes) == 0 {
		return nil
	}
	out := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		out = append(out, prefix.String())
	}
	return out
}
