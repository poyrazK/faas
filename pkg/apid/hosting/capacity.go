package hosting

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	artifacts "github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state"
)

type PreviewReservationsHost interface {
	HostingPreviewReservationsEnabled() bool
	HostingPreviewCapacityReader() state.HostingPreviewCapacityReader
}

func (s Service) previewReservationsEnabled() bool {
	h, ok := s.Host.(PreviewReservationsHost)
	a, automation := s.Host.(AutomationHost)
	p, previews := s.Host.(PreviewHost)
	return ok && h.HostingPreviewReservationsEnabled() && h.HostingPreviewCapacityReader() != nil && automation && a.HostingAutomationEnabled() && a.HostingAutomationStore() != nil && previews && p.HostingPreviewsEnabled() && p.HostingPreviewStore() != nil && s.Host.HostingDurableRoutingEnabled()
}
func reservationsUnavailable(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_preview_reservations_disabled", "Preview reservations unavailable", "internal preview reservations or their automation, preview and durable routing prerequisites are disabled or unconfigured"))
}
func (s Service) allowReservedIntent(w http.ResponseWriter, r *http.Request, a state.Account, site, id string, req api.HostingCIRunRequest, store state.HostingAutomationStore) bool {
	if s.previewReservationsEnabled() {
		return true
	}
	// Hot disable stops new holds, but cannot change a previously admitted intent.
	old, err := store.GetHostingCIRun(r.Context(), a.ID, site, id)
	fingerprint, _ := artifacts.CIFingerprint(req)
	if err == nil && old.PreviewReserved && old.Fingerprint == fingerprint {
		return true
	}
	reservationsUnavailable(w)
	return false
}
func (s Service) PreviewCapacity(w http.ResponseWriter, r *http.Request, a state.Account) {
	if !s.guard(w, a) {
		return
	}
	if !s.previewReservationsEnabled() {
		reservationsUnavailable(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.HostingPreviewCapacityTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	site, ok := s.site(w, r, a)
	if !ok {
		return
	}
	h := s.Host.(PreviewReservationsHost)
	result, err := h.HostingPreviewCapacityReader().GetHostingPreviewCapacity(ctx, a.ID, site.ID)
	if err != nil {
		failure(w, err)
		return
	}
	if result.SiteID != site.ID {
		failure(w, state.ErrConflict)
		return
	}
	if !s.guard(w, a) {
		return
	}
	if !s.previewReservationsEnabled() {
		reservationsUnavailable(w)
		return
	}
	httpkit.WriteJSON(w, 200, result)
}
