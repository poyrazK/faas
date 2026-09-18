// handlers_apps_private_network.go — provider-neutral private-network
// attachment intent for customer apps.
//
// The API records provider-neutral intent. A runtime connector advances
// pending rows to ready only after route activation succeeds; until then
// traffic remains fail-closed and the API exposes the status to operators and
// customers.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const privateNetworkPendingDetail = "connector not provisioned; traffic remains blocked until the attachment is ready"

func (s *server) privateNetworkAttachments() (state.AppPrivateNetworkAttachmentStore, bool) {
	store, ok := s.store.(state.AppPrivateNetworkAttachmentStore)
	return store, ok
}

func (s *server) getAppPrivateNetworkAttachment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !api.PrivateNetworkEnabled() {
		api.WriteProblem(w, api.ErrPrivateNetworkNotEnabled())
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.privateNetworkAttachments()
	if !ok {
		api.WriteProblem(w, api.ErrPrivateNetworkNotEnabled())
		return
	}
	limits, _ := api.LimitsFor(acct.Plan)
	attachment, err := store.GetAppPrivateNetworkAttachment(r.Context(), acct.ID, app.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrCapacity("could not read private network attachment"))
		return
	}
	var out *api.AppPrivateNetworkAttachment
	if err == nil {
		converted := privateNetworkAttachmentResponse(attachment, privateNetworkAddress(r.Context(), s.store, acct.ID, app.ID, attachment.NetworkID))
		out = &converted
	}
	writeJSON(w, http.StatusOK, api.AppPrivateNetworkAttachmentResponse{
		FeatureEnabled: true,
		PlanAllowed:    limits.PrivateNetworkAllowed,
		MaxCIDRs:       limits.PrivateNetworkCIDRsMax,
		Attachment:     out,
	})
}

func (s *server) setAppPrivateNetworkAttachment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !api.PrivateNetworkEnabled() {
		api.WriteProblem(w, api.ErrPrivateNetworkNotEnabled())
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || !limits.PrivateNetworkAllowed {
		api.WriteProblem(w, api.ErrPlanPrivateNetworkNotAllowed(acct.Plan))
		return
	}
	store, ok := s.privateNetworkAttachments()
	if !ok {
		api.WriteProblem(w, api.ErrPrivateNetworkNotEnabled())
		return
	}
	var fabric state.PrivateNetworkStore
	var req api.AppPrivateNetworkAttachmentRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}
	if err := api.ValidatePrivateNetworkIdentifier(req.NetworkID); err != nil {
		api.WriteProblem(w, api.ErrPrivateNetworkInvalid("network_id", req.NetworkID, err.Error()))
		return
	}
	if api.PrivateNetworkFabricEnabled() {
		var fabricOK bool
		fabric, fabricOK = s.store.(state.PrivateNetworkStore)
		if !fabricOK {
			api.WriteProblem(w, api.ErrPrivateNetworkNotEnabled())
			return
		}
		network, networkErr := fabric.GetPrivateNetwork(r.Context(), acct.ID, req.NetworkID)
		if networkErr != nil {
			if errors.Is(networkErr, state.ErrNotFound) {
				api.WriteProblem(w, api.ErrPrivateNetworkInvalid("network_id", req.NetworkID, "the network is not owned by this account"))
				return
			}
			api.WriteProblem(w, api.ErrCapacity("could not read private network"))
			return
		}
		if strings.TrimSpace(req.Region) == "" {
			req.Region = network.Region
		} else if req.Region != network.Region {
			api.WriteProblem(w, api.ErrPrivateNetworkInvalid("region", req.Region, "the region must match the Gregale network"))
			return
		}
		if len(req.CIDRs) == 0 {
			req.CIDRs = []string{network.CIDR.String()}
		} else if len(req.CIDRs) != 1 || strings.TrimSpace(req.CIDRs[0]) != network.CIDR.String() {
			api.WriteProblem(w, api.ErrPrivateNetworkInvalid("cidrs", strings.Join(req.CIDRs, ","), "Gregale-owned attachments must route the network CIDR"))
			return
		}
	}
	if err := api.ValidatePrivateNetworkIdentifier(req.Region); err != nil {
		api.WriteProblem(w, api.ErrPrivateNetworkInvalid("region", req.Region, err.Error()))
		return
	}
	cidrs, err := api.ValidatePrivateNetworkCIDRs(req.CIDRs, limits.PrivateNetworkCIDRsMax)
	if err != nil {
		api.WriteProblem(w, api.ErrPrivateNetworkInvalid("cidrs", strings.Join(req.CIDRs, ","), err.Error()))
		return
	}
	allowedCIDRs, err := api.ValidatePrivateNetworkPolicyCIDRs(req.AllowedCIDRs, cidrs)
	if err != nil {
		api.WriteProblem(w, api.ErrPrivateNetworkInvalid("allowed_cidrs", strings.Join(req.AllowedCIDRs, ","), err.Error()))
		return
	}
	var previous state.AppPrivateNetworkAttachment
	if fabric != nil {
		previous, _ = store.GetAppPrivateNetworkAttachment(r.Context(), acct.ID, app.ID)
		if _, allocErr := fabric.AllocatePrivateNetworkAddress(r.Context(), acct.ID, req.NetworkID, "app", app.ID); allocErr != nil {
			switch {
			case errors.Is(allocErr, state.ErrNotFound):
				api.WriteProblem(w, api.ErrPrivateNetworkInvalid("network_id", req.NetworkID, "the network is no longer available"))
			case errors.Is(allocErr, state.ErrConflict):
				api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Private network address capacity reached", "no member address is available in this network"))
			default:
				api.WriteProblem(w, api.ErrCapacity("could not reserve private network address"))
			}
			return
		}
	}
	attachment, err := store.UpsertAppPrivateNetworkAttachment(r.Context(), state.AppPrivateNetworkAttachment{
		AccountID:    acct.ID,
		AppID:        app.ID,
		NetworkID:    req.NetworkID,
		Region:       req.Region,
		CIDRs:        cidrs,
		AllowedCIDRs: allowedCIDRs,
		Status:       api.PrivateNetworkAttachmentStatusPending,
		StatusDetail: privateNetworkPendingDetail,
	})
	if err != nil {
		if fabric != nil {
			_ = fabric.ReleasePrivateNetworkAddress(r.Context(), acct.ID, req.NetworkID, "app", app.ID)
		}
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.ErrPrivateNetworkInvalid("network_id", req.NetworkID, "the attachment conflicts with another account binding"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not save private network attachment"))
		return
	}
	if fabric != nil && previous.NetworkID != "" && previous.NetworkID != req.NetworkID {
		_ = fabric.ReleasePrivateNetworkAddress(r.Context(), acct.ID, previous.NetworkID, "app", app.ID)
	}
	_ = s.notif.Notify(r.Context(), "app_changed", fmt.Sprintf(
		`{"kind":"private_network_attachment","app_id":"%s","account_id":"%s","network_id":%q,"region":%q,"status":%q}`,
		app.ID, acct.ID, attachment.NetworkID, attachment.Region, attachment.Status))
	s.audit.Emit(r.Context(), "app.private_network_attachment_requested", &acct.ID, map[string]any{
		"app_id": app.ID, "network_id": attachment.NetworkID, "region": attachment.Region,
		"cidrs": prefixesToStrings(attachment.CIDRs), "allowed_cidrs": prefixesToStrings(attachment.AllowedCIDRs), "status": attachment.Status,
	})
	converted := privateNetworkAttachmentResponse(attachment, privateNetworkAddress(r.Context(), s.store, acct.ID, app.ID, attachment.NetworkID))
	writeJSON(w, http.StatusAccepted, api.AppPrivateNetworkAttachmentResponse{
		FeatureEnabled: true,
		PlanAllowed:    limits.PrivateNetworkAllowed,
		MaxCIDRs:       limits.PrivateNetworkCIDRsMax,
		Attachment:     &converted,
	})
}

func (s *server) clearAppPrivateNetworkAttachment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !api.PrivateNetworkEnabled() {
		api.WriteProblem(w, api.ErrPrivateNetworkNotEnabled())
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.privateNetworkAttachments()
	if !ok {
		api.WriteProblem(w, api.ErrPrivateNetworkNotEnabled())
		return
	}
	if api.PrivateNetworkFabricEnabled() {
		fabric, fabricOK := s.store.(state.PrivateNetworkStore)
		if !fabricOK {
			api.WriteProblem(w, api.ErrPrivateNetworkNotEnabled())
			return
		}
		if attachment, getErr := store.GetAppPrivateNetworkAttachment(r.Context(), acct.ID, app.ID); getErr == nil {
			_ = fabric.ReleasePrivateNetworkAddress(r.Context(), acct.ID, attachment.NetworkID, "app", app.ID)
		}
	}
	err := store.DeleteAppPrivateNetworkAttachment(r.Context(), acct.ID, app.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrCapacity("could not clear private network attachment"))
		return
	}
	if err == nil {
		_ = s.notif.Notify(r.Context(), "app_changed", fmt.Sprintf(
			`{"kind":"private_network_attachment","app_id":"%s","account_id":"%s","status":"detached"}`,
			app.ID, acct.ID))
		s.audit.Emit(r.Context(), "app.private_network_attachment_detached", &acct.ID, map[string]any{"app_id": app.ID})
	}
	w.WriteHeader(http.StatusNoContent)
}

func privateNetworkAttachmentResponse(in state.AppPrivateNetworkAttachment, address string) api.AppPrivateNetworkAttachment {
	out := api.AppPrivateNetworkAttachment{
		ID:           in.ID,
		NetworkID:    in.NetworkID,
		Region:       in.Region,
		CIDRs:        prefixesToStrings(in.CIDRs),
		AllowedCIDRs: prefixesToStrings(in.AllowedCIDRs),
		Address:      address,
		Status:       in.Status,
		StatusDetail: in.StatusDetail,
	}
	if !in.CreatedAt.IsZero() {
		t := in.CreatedAt.UTC()
		out.CreatedAt = &t
	}
	if !in.UpdatedAt.IsZero() {
		t := in.UpdatedAt.UTC()
		out.UpdatedAt = &t
	}
	return out
}

func privateNetworkAddress(ctx context.Context, store state.Store, accountID, appID, networkID string) string {
	if !api.PrivateNetworkFabricEnabled() {
		return ""
	}
	fabric, ok := store.(state.PrivateNetworkStore)
	if !ok || networkID == "" {
		return ""
	}
	address, err := fabric.AllocatePrivateNetworkAddress(ctx, accountID, networkID, "app", appID)
	if err != nil {
		return ""
	}
	return address.Address.String()
}

func prefixesToStrings(prefixes []netip.Prefix) []string {
	out := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		out = append(out, prefix.String())
	}
	return out
}
