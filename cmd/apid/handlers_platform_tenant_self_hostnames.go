package main

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) createPlatformTenantSelfHostname(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantID, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	if !s.runtimeBool(runtimeConfigTenantSurfaces, api.TenantSurfacesEnabled()) {
		api.WriteProblem(w, api.ErrTenantSurfacesNotEnabled())
		return
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || !limits.TenantSurfacesAllowed {
		api.WriteProblem(w, api.ErrTenantSurfacesNotAllowed(acct.Plan))
		return
	}
	store, ok := s.store.(state.PlatformTenantDelegatedHostnameStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant hostname self-service is unavailable"))
		return
	}
	var req api.CreatePlatformTenantSelfHostnameRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	req.SurfaceID = strings.TrimSpace(req.SurfaceID)
	req.Hostname = strings.ToLower(strings.TrimSpace(req.Hostname))
	if _, err := uuid.Parse(req.SurfaceID); err != nil || !state.ValidPlatformTenantHostname(req.Hostname) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid hostname request", "surface_id must identify one of your linked surfaces and hostname must be a DNS name without a scheme or port"))
		return
	}
	result, err := store.CreatePlatformTenantDelegatedHostname(r.Context(), acct.ID, tenantID, req.SurfaceID,
		req.Hostname, randomToken(16), limits)
	switch {
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no such linked tenant surface")
		return
	case errors.Is(err, state.ErrPlatformTenantSuspended):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden,
			"Platform tenant suspended", "hostname changes are unavailable while this tenant is suspended"))
		return
	case errors.Is(err, state.ErrPlatformTenantHostnameDelegationDisabled):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "platform_tenant_hostname_delegation_disabled",
			"Hostname self-service disabled", "ask the platform owner to configure hostname delegation"))
		return
	case errors.Is(err, state.ErrPlatformTenantHostnameSuffixNotAllowed):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "platform_tenant_hostname_not_allowed",
			"Hostname not allowed", "this hostname is outside the DNS suffixes delegated to your tenant"))
		return
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.ErrTenantHostnameAlreadyClaimed(req.Hostname))
		return
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid hostname request", "check the hostname, surface, and tenant-surface plan limits"))
		return
	case err != nil:
		var delegatedQuota *state.PlatformTenantDelegatedHostnameQuotaError
		if errors.As(err, &delegatedQuota) {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "platform_tenant_hostname_quota",
				"Delegated hostname limit reached", "ask the platform owner to increase this tenant's hostname limit"))
			return
		}
		var surfaceQuota *state.TenantHostnameQuotaError
		if errors.As(err, &surfaceQuota) {
			api.WriteProblem(w, api.ErrTenantHostnameQuota(acct.Plan, surfaceQuota.SurfaceID, surfaceQuota.Limit, surfaceQuota.Observed))
			return
		}
		api.WriteProblem(w, tenantBindingWriteProblem(err, api.ErrCapacity("could not add delegated tenant hostname")))
		return
	}
	if result.Action == "created" {
		s.audit.Emit(r.Context(), "platform_tenant.self_hostname_added", &acct.ID, map[string]any{
			"tenant_id": tenantID, "surface_id": result.Hostname.SurfaceID, "hostname": result.Hostname.Hostname,
		})
	}
	out := api.PlatformTenantSelfHostnameResponse{SurfaceID: result.Hostname.SurfaceID, Hostname: result.Hostname.Hostname,
		Action: result.Action, Verified: result.Hostname.Verified(), TXTRecord: "_faas-verify." + result.Hostname.Hostname}
	if !result.Hostname.Verified() {
		out.ChallengeToken = result.Hostname.ChallengeToken
	}
	if !result.Hostname.VerifiedAt.IsZero() {
		out.VerifiedAt = result.Hostname.VerifiedAt.UTC().Format(time.RFC3339)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, out)
}
