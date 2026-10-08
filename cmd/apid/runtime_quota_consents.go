package main

// adr: 645
import (
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) registerRuntimeConsentRoutes(mux *http.ServeMux) {
	admin := func(h accountHandler) http.HandlerFunc {
		principal := s.requireRegionalAccountKey(s.requireDirectoryAccount(s.requireVerifiedEmail(h)))
		return s.authLimited(s.requireMFA(s.requireScope(api.ScopeAdmin)(principal)))
	}
	mux.HandleFunc("POST /internal/v1/ingress-status", s.regionalIngressStatus)
	path := "/v1/cells/{cell_id}/apps/{app_id}/runtime-consents/{consent_id}"
	mux.HandleFunc("PUT "+path, admin(s.requireCellDeploymentApp(s.putRuntimeConsent)))
	mux.HandleFunc("GET "+path, admin(s.getRuntimeConsent))
	mux.HandleFunc("DELETE "+path, admin(s.revokeRuntimeConsent))
	mux.HandleFunc("PUT /v1/cells/{cell_id}/apps/{app_id}/runtime-quotas/{allocation_id}/delegated", s.acceptDelegatedCellRuntimeQuota)
}

func (s *server) runtimeConsentStore(w http.ResponseWriter, r *http.Request) (state.RegionalRuntimeConsentStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.store.(state.RegionalRuntimeConsentStore)
	if !ok || s.regionalQuotaAPIBase == "" {
		api.WriteProblem(w, api.ErrCapacity("regional runtime consent is unavailable"))
		return nil, false
	}
	if r.PathValue("cell_id") != s.controlPlaneCell.ID || !globaldirectory.ValidID(r.PathValue("app_id")) || !globaldirectory.ValidID(r.PathValue("consent_id")) {
		s.notFound(w, "no such regional runtime consent")
		return nil, false
	}
	return store, true
}

func (s *server) putRuntimeConsent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.runtimeConsentStore(w, r)
	if !ok {
		return
	}
	raw, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, api.RegionalRuntimeConsentMaxBytes))
	c, err := runtimequota.DecodeConsent(raw)
	app, _ := r.Context().Value(cellDeploymentAppContextKey{}).(state.App)
	b := c.Binding
	if readErr != nil || err != nil || c.ID != r.PathValue("consent_id") || b.CellID != s.controlPlaneCell.ID || b.RegionalAppID != app.ID || b.RegionalAppSlug != app.Slug || b.RegionalAccountID != quotaLocalUUID(acct.ID) || b.Region != app.ComputeRegion || b.Region != s.controlPlaneCell.Region || b.APIBase != s.regionalQuotaAPIBase || !s.regionalQuotaTrust.HasIssuer(c.IssuerID, runtimequota.Directory) {
		api.WriteProblem(w, api.ErrValidation("send original personal runtime consent for this trusted directory and regional app"))
		return
	}
	consent, err := store.PutRegionalRuntimeConsent(r.Context(), c)
	s.writeRuntimeConsent(w, r, acct, consent, err)
}

func (s *server) getRuntimeConsent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.runtimeConsentStore(w, r)
	if !ok {
		return
	}
	c, err := store.RegionalRuntimeConsent(r.Context(), acct.ID, r.PathValue("cell_id"), r.PathValue("app_id"), r.PathValue("consent_id"))
	s.writeRuntimeConsent(w, r, acct, c, err)
}

func (s *server) revokeRuntimeConsent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.runtimeConsentStore(w, r)
	if !ok {
		return
	}
	c, err := store.RevokeRegionalRuntimeConsent(r.Context(), acct.ID, r.PathValue("cell_id"), r.PathValue("app_id"), r.PathValue("consent_id"))
	s.writeRuntimeConsent(w, r, acct, c, err)
}

func (s *server) writeRuntimeConsent(w http.ResponseWriter, r *http.Request, acct state.Account, c state.RegionalRuntimeConsent, err error) {
	var limit *state.RegionalRuntimeConsentLimitError
	switch {
	case err == nil:
		if r.Method != http.MethodGet {
			s.audit.Emit(r.Context(), "regional_runtime_consent.changed", &acct.ID, map[string]any{"consent_id": c.Consent.ID, "app_id": c.Consent.Binding.RegionalAppID, "revoked": c.RevokedAt != nil})
		}
		writeJSON(w, http.StatusOK, c)
	case errors.As(err, &limit):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Runtime consent limit reached", "Revoke an unused delivery consent or wait for expiry.").WithLimit(limit.Limit, limit.Observed))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("runtime consent identity or expiry is invalid"))
	default:
		s.runtimeQuotaTransportError(w, r, acct, err)
	}
}

func (s *server) acceptDelegatedCellRuntimeQuota(w http.ResponseWriter, r *http.Request) {
	d, ok := s.readQuotaDelivery(w, r)
	if !ok {
		return
	}
	if d.ConsentID == "" || r.Header.Get("Authorization") != "" {
		api.WriteProblem(w, api.ErrValidation("delegated delivery requires its original runtime consent UUID and signed directory authority"))
		return
	}
	authority, ok := s.cellQuotaAuthority(w, d.Binding)
	if !ok {
		return
	}
	grant, err := authority.Directories.VerifyGrant(d.Authority, d.Binding)
	if err != nil || grant.AllocationID != r.PathValue("allocation_id") {
		api.WriteProblem(w, api.ErrValidation("delegated quota authority differs from request path"))
		return
	}
	receipt, err := authority.AcceptDelegatedGrant(r.Context(), d.Authority, d.ConsentID)
	s.writeQuotaDeliveryReceipt(w, r, receipt, err, false)
}
