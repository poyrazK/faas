package main

import (
	"net/http"
	"sort"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) platformTenantCredentialPolicyStore(w http.ResponseWriter, acct state.Account) (state.PlatformTenantCredentialPolicyStore, bool) {
	if !s.consumerFeatureAllowed(w, acct) {
		return nil, false
	}
	store, ok := s.store.(state.PlatformTenantCredentialPolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant credential policies are unavailable"))
	}
	return store, ok
}

func platformTenantCredentialPolicyResponse(policy state.PlatformTenantCredentialPolicy) api.PlatformTenantCredentialPolicyResponse {
	out := api.PlatformTenantCredentialPolicyResponse{TenantID: policy.TenantID,
		Enabled:       len(policy.AllowedScopes) > 0 && policy.MaxKeysPerConsumer > 0,
		AllowedScopes: append([]string{}, policy.AllowedScopes...), MaxKeysPerConsumer: policy.MaxKeysPerConsumer}
	if !policy.UpdatedAt.IsZero() {
		updatedAt := policy.UpdatedAt.UTC()
		out.UpdatedAt = &updatedAt
	}
	return out
}

func (s *server) getPlatformTenantCredentialPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.platformTenantCredentialPolicyStore(w, acct)
	if !ok {
		return
	}
	tenantID := r.PathValue("id")
	if _, err := uuid.Parse(tenantID); err != nil {
		s.notFound(w, "no such platform tenant")
		return
	}
	policy, err := store.GetPlatformTenantCredentialPolicy(r.Context(), acct.ID, tenantID)
	if err != nil {
		s.platformTenantCredentialError(w, err, acct.Plan)
		return
	}
	writeJSON(w, http.StatusOK, platformTenantCredentialPolicyResponse(policy))
}

func (s *server) setPlatformTenantCredentialPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.platformTenantCredentialPolicyStore(w, acct)
	if !ok {
		return
	}
	tenantID := r.PathValue("id")
	if _, err := uuid.Parse(tenantID); err != nil {
		s.notFound(w, "no such platform tenant")
		return
	}
	var req api.SetPlatformTenantCredentialPolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.AllowedScopes == nil || req.MaxKeysPerConsumer == nil || len(req.AllowedScopes) > api.MaxPlatformTenantCredentialScopes ||
		*req.MaxKeysPerConsumer < 0 || *req.MaxKeysPerConsumer > api.MaxPlatformTenantKeysPerConsumer ||
		(len(req.AllowedScopes) == 0) != (*req.MaxKeysPerConsumer == 0) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid credential delegation policy", "allowed_scopes and max_keys_per_consumer are required; use an empty scope list and zero limit to disable delegation"))
		return
	}
	scopes := make([]string, 0, len(req.AllowedScopes))
	seen := make(map[string]struct{}, len(req.AllowedScopes))
	for _, scope := range req.AllowedScopes {
		if scope != "read" && scope != "write" && scope != "admin" {
			api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
				"Invalid credential delegation policy", "allowed_scopes may contain only read, write, and admin"))
			return
		}
		if _, duplicate := seen[scope]; duplicate {
			api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
				"Invalid credential delegation policy", "allowed_scopes must not contain duplicates"))
			return
		}
		seen[scope] = struct{}{}
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	prior, err := store.GetPlatformTenantCredentialPolicy(r.Context(), acct.ID, tenantID)
	if err != nil {
		s.platformTenantCredentialError(w, err, acct.Plan)
		return
	}
	policy, err := store.SetPlatformTenantCredentialPolicy(r.Context(), acct.ID, tenantID, scopes, *req.MaxKeysPerConsumer)
	if err != nil {
		s.platformTenantCredentialError(w, err, acct.Plan)
		return
	}
	if prior.MaxKeysPerConsumer != policy.MaxKeysPerConsumer || len(prior.AllowedScopes) != len(policy.AllowedScopes) {
		s.audit.Emit(r.Context(), "platform_tenant.credential_policy_updated", &acct.ID, map[string]any{
			"tenant_id": tenantID, "allowed_scopes": policy.AllowedScopes, "max_keys_per_consumer": policy.MaxKeysPerConsumer,
		})
	} else {
		changed := false
		for i := range prior.AllowedScopes {
			if prior.AllowedScopes[i] != policy.AllowedScopes[i] {
				changed = true
				break
			}
		}
		if changed {
			s.audit.Emit(r.Context(), "platform_tenant.credential_policy_updated", &acct.ID, map[string]any{
				"tenant_id": tenantID, "allowed_scopes": policy.AllowedScopes, "max_keys_per_consumer": policy.MaxKeysPerConsumer,
			})
		}
	}
	writeJSON(w, http.StatusOK, platformTenantCredentialPolicyResponse(policy))
}
