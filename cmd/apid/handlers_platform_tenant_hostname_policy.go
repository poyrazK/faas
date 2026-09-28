package main

import (
	"net/http"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) platformTenantHostnamePolicyStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.PlatformTenant, state.PlatformTenantHostnamePolicyStore, bool) {
	tenantStore, ok := s.platformTenantStore(w, acct)
	if !ok {
		return state.PlatformTenant{}, nil, false
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, tenantStore)
	if !ok {
		return state.PlatformTenant{}, nil, false
	}
	if !s.runtimeBool(runtimeConfigTenantSurfaces, api.TenantSurfacesEnabled()) {
		api.WriteProblem(w, api.ErrTenantSurfacesNotEnabled())
		return state.PlatformTenant{}, nil, false
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || !limits.TenantSurfacesAllowed {
		api.WriteProblem(w, api.ErrTenantSurfacesNotAllowed(acct.Plan))
		return state.PlatformTenant{}, nil, false
	}
	store, ok := s.store.(state.PlatformTenantHostnamePolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant hostname policies are unavailable"))
	}
	return tenant, store, ok
}

func platformTenantHostnamePolicyResponse(policy state.PlatformTenantHostnamePolicy) api.PlatformTenantHostnamePolicyResponse {
	out := api.PlatformTenantHostnamePolicyResponse{
		TenantID: policy.TenantID, AllowedSuffixes: append([]string{}, policy.AllowedSuffixes...),
		MaxHostnames: policy.MaxHostnames,
		Enabled:      len(policy.AllowedSuffixes) > 0 && policy.MaxHostnames > 0,
	}
	if !policy.UpdatedAt.IsZero() {
		updatedAt := policy.UpdatedAt.UTC()
		out.UpdatedAt = &updatedAt
	}
	return out
}

func (s *server) getPlatformTenantHostnamePolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, store, ok := s.platformTenantHostnamePolicyStore(w, r, acct)
	if !ok {
		return
	}
	policy, err := store.GetPlatformTenantHostnamePolicy(r.Context(), acct.ID, tenant.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load platform tenant hostname policy"))
		return
	}
	writeJSON(w, http.StatusOK, platformTenantHostnamePolicyResponse(policy))
}

func (s *server) setPlatformTenantHostnamePolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, store, ok := s.platformTenantHostnamePolicyStore(w, r, acct)
	if !ok {
		return
	}
	var req api.SetPlatformTenantHostnamePolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.AllowedSuffixes == nil || req.MaxHostnames == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid hostname delegation policy", "allowed_suffixes and max_hostnames are required; use an empty suffix list and zero limit to disable delegation"))
		return
	}
	if len(req.AllowedSuffixes) > api.MaxPlatformTenantHostnameSuffixes || *req.MaxHostnames < 0 ||
		*req.MaxHostnames > api.MaxPlatformTenantDelegatedHosts || (len(req.AllowedSuffixes) == 0) != (*req.MaxHostnames == 0) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid hostname delegation policy", "provide at most 32 DNS suffixes and a matching hostname limit from 0 to 100"))
		return
	}
	suffixes := make([]string, 0, len(req.AllowedSuffixes))
	seen := make(map[string]struct{}, len(req.AllowedSuffixes))
	for _, raw := range req.AllowedSuffixes {
		suffix := strings.ToLower(strings.TrimSpace(raw))
		if !state.ValidPlatformTenantHostname(suffix) {
			api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
				"Invalid hostname delegation policy", "allowed_suffixes must contain DNS hostnames without schemes, ports, wildcards, or trailing dots"))
			return
		}
		if _, duplicate := seen[suffix]; duplicate {
			api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
				"Invalid hostname delegation policy", "allowed_suffixes must be unique after normalization"))
			return
		}
		seen[suffix] = struct{}{}
		suffixes = append(suffixes, suffix)
	}
	sort.Strings(suffixes)
	prior, err := store.GetPlatformTenantHostnamePolicy(r.Context(), acct.ID, tenant.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load platform tenant hostname policy"))
		return
	}
	policy, err := store.SetPlatformTenantHostnamePolicy(r.Context(), acct.ID, tenant.ID, suffixes, *req.MaxHostnames)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not update platform tenant hostname policy"))
		return
	}
	if prior.MaxHostnames != policy.MaxHostnames || strings.Join(prior.AllowedSuffixes, "\x00") != strings.Join(policy.AllowedSuffixes, "\x00") {
		s.audit.Emit(r.Context(), "platform_tenant.hostname_policy_updated", &acct.ID, map[string]any{
			"tenant_id": tenant.ID, "allowed_suffixes": policy.AllowedSuffixes, "max_hostnames": policy.MaxHostnames,
		})
	}
	writeJSON(w, http.StatusOK, platformTenantHostnamePolicyResponse(policy))
}
