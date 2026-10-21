package hosting

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/hosting/cisetup"
	"github.com/onebox-faas/faas/pkg/state"
)

type CISetupHost interface{ HostingCISetupEnabled() bool }

// PrepareCISetup only reads intent and renders files. POST carries configuration
// data; it never stores the body or uses generic idempotency replay.
func (s Service) PrepareCISetup(w http.ResponseWriter, r *http.Request, account state.Account) {
	if !s.guard(w, account) {
		return
	}
	h, ok := s.Host.(CISetupHost)
	oidcHost, hasStore := s.Host.(OIDCHost)
	if !ok || !h.HostingCISetupEnabled() || !hasStore || oidcHost.HostingOIDCStore() == nil {
		setupUnavailable(w)
		return
	}
	var req api.HostingCISetupRequest
	if httpkit.DecodeJSONSized(r, &req, api.HostingCISetupRequestMaxBytes) != nil || uuid.Validate(req.TrustID) != nil {
		api.WriteProblem(w, api.ErrValidation("a bounded setup request and trust UUID are required"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.HostingCISetupTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	site, ok := s.site(w, r, account)
	if !ok {
		return
	}
	result, err := s.prepareCISetup(r, account, site, req, oidcHost.HostingOIDCStore())
	if err != nil {
		failure(w, err)
		return
	}
	if !s.guard(w, account) {
		return
	}
	if !h.HostingCISetupEnabled() {
		setupUnavailable(w)
		return
	}
	httpkit.WriteJSON(w, 200, result)
}
func setupUnavailable(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_ci_setup_disabled", "GitHub setup unavailable", "internal frontend deployment setup is disabled or unconfigured"))
}

func (s Service) prepareCISetup(r *http.Request, account state.Account, site api.HostingSite, req api.HostingCISetupRequest, store state.HostingOIDCStore) (api.HostingCISetup, error) {
	trust, err := store.GetHostingOIDCTrust(r.Context(), site.ID, req.TrustID)
	if err != nil {
		return api.HostingCISetup{}, err
	}
	if trust.AccountID != account.ID {
		return api.HostingCISetup{}, state.ErrNotFound
	}
	caps := api.HostingCapabilities{StagedReleasesSupported: true}
	s.extendCapabilities(r, &caps)
	result := cisetup.Prepare(site, trust, caps, req, time.Now().UTC())
	again, err := store.GetHostingOIDCTrust(r.Context(), site.ID, req.TrustID)
	if err != nil {
		return api.HostingCISetup{}, err
	}
	if again != trust {
		return api.HostingCISetup{}, state.ErrConflict
	}
	current, err := s.Host.HostingStore().GetHostingSite(r.Context(), account.ID, site.ID)
	if err != nil {
		return api.HostingCISetup{}, err
	}
	if current.Revision != site.Revision || current.Status != site.Status || current.Suspended != site.Suspended || current.OriginID != site.OriginID {
		return api.HostingCISetup{}, state.ErrConflict
	}
	latest := api.HostingCapabilities{StagedReleasesSupported: true}
	s.extendCapabilities(r, &latest)
	if latest.PreviewReservationsEnabled != caps.PreviewReservationsEnabled || latest.CISetupEnabled != caps.CISetupEnabled || latest.OIDCEnabled != caps.OIDCEnabled || latest.AutomationEnabled != caps.AutomationEnabled || latest.PreviewsEnabled != caps.PreviewsEnabled || latest.HistoryEnabled != caps.HistoryEnabled {
		return api.HostingCISetup{}, state.ErrConflict
	}
	return result, nil
}
