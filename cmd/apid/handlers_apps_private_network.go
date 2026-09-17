// handlers_apps_private_network.go — provider-neutral private-network
// attachment intent for customer apps.
//
// The API records provider-neutral intent. A runtime connector advances
// pending rows to ready only after route activation succeeds; until then
// traffic remains fail-closed and the API exposes the status to operators and
// customers.
package main

import (
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
		converted := privateNetworkAttachmentResponse(attachment)
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
	var req api.AppPrivateNetworkAttachmentRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}
	if err := api.ValidatePrivateNetworkIdentifier(req.NetworkID); err != nil {
		api.WriteProblem(w, api.ErrPrivateNetworkInvalid("network_id", req.NetworkID, err.Error()))
		return
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
	attachment, err := store.UpsertAppPrivateNetworkAttachment(r.Context(), state.AppPrivateNetworkAttachment{
		AccountID:    acct.ID,
		AppID:        app.ID,
		NetworkID:    req.NetworkID,
		Region:       req.Region,
		CIDRs:        cidrs,
		Status:       api.PrivateNetworkAttachmentStatusPending,
		StatusDetail: privateNetworkPendingDetail,
	})
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.ErrPrivateNetworkInvalid("network_id", req.NetworkID, "the attachment conflicts with another account binding"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not save private network attachment"))
		return
	}
	_ = s.notif.Notify(r.Context(), "app_changed", fmt.Sprintf(
		`{"kind":"private_network_attachment","app_id":"%s","account_id":"%s","network_id":%q,"region":%q,"status":%q}`,
		app.ID, acct.ID, attachment.NetworkID, attachment.Region, attachment.Status))
	s.audit.Emit(r.Context(), "app.private_network_attachment_requested", &acct.ID, map[string]any{
		"app_id": app.ID, "network_id": attachment.NetworkID, "region": attachment.Region,
		"cidrs": prefixesToStrings(attachment.CIDRs), "status": attachment.Status,
	})
	converted := privateNetworkAttachmentResponse(attachment)
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

func privateNetworkAttachmentResponse(in state.AppPrivateNetworkAttachment) api.AppPrivateNetworkAttachment {
	out := api.AppPrivateNetworkAttachment{
		ID:           in.ID,
		NetworkID:    in.NetworkID,
		Region:       in.Region,
		CIDRs:        prefixesToStrings(in.CIDRs),
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

func prefixesToStrings(prefixes []netip.Prefix) []string {
	out := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		out = append(out, prefix.String())
	}
	return out
}
