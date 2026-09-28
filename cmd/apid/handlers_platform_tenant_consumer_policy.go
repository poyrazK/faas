package main

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) platformTenantConsumerPolicyStore(w http.ResponseWriter, acct state.Account) (state.PlatformTenantConsumerProvisioningPolicyStore, bool) {
	if !s.consumerFeatureAllowed(w, acct) {
		return nil, false
	}
	store, ok := s.store.(state.PlatformTenantConsumerProvisioningPolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant consumer policies are unavailable"))
	}
	return store, ok
}

func platformTenantConsumerPolicyResponse(policy state.PlatformTenantConsumerProvisioningPolicy) api.PlatformTenantConsumerProvisioningPolicyResponse {
	out := api.PlatformTenantConsumerProvisioningPolicyResponse{TenantID: policy.TenantID,
		Enabled: policy.Enabled, MaxConsumers: policy.MaxConsumers}
	if !policy.UpdatedAt.IsZero() {
		updatedAt := policy.UpdatedAt.UTC()
		out.UpdatedAt = &updatedAt
	}
	return out
}

func (s *server) getPlatformTenantConsumerProvisioningPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.platformTenantConsumerPolicyStore(w, acct)
	if !ok {
		return
	}
	tenantID := r.PathValue("id")
	if _, err := uuid.Parse(tenantID); err != nil {
		s.notFound(w, "no such platform tenant")
		return
	}
	policy, err := store.GetPlatformTenantConsumerProvisioningPolicy(r.Context(), acct.ID, tenantID)
	if err != nil {
		s.platformTenantCredentialError(w, err, acct.Plan)
		return
	}
	writeJSON(w, http.StatusOK, platformTenantConsumerPolicyResponse(policy))
}

func (s *server) setPlatformTenantConsumerProvisioningPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.platformTenantConsumerPolicyStore(w, acct)
	if !ok {
		return
	}
	tenantID := r.PathValue("id")
	if _, err := uuid.Parse(tenantID); err != nil {
		s.notFound(w, "no such platform tenant")
		return
	}
	var req api.SetPlatformTenantConsumerProvisioningPolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.Enabled == nil || req.MaxConsumers == nil ||
		(*req.Enabled && (*req.MaxConsumers < 1 || *req.MaxConsumers > api.MaxPlatformTenantSelfServiceConsumers)) ||
		(!*req.Enabled && *req.MaxConsumers != 0) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid consumer provisioning policy", "enabled and max_consumers are required; enabled policies need a 1-100000 cap, disabled policies require a zero cap"))
		return
	}
	prior, err := store.GetPlatformTenantConsumerProvisioningPolicy(r.Context(), acct.ID, tenantID)
	if err != nil {
		s.platformTenantCredentialError(w, err, acct.Plan)
		return
	}
	policy, err := store.SetPlatformTenantConsumerProvisioningPolicy(r.Context(), acct.ID, tenantID, *req.Enabled, *req.MaxConsumers)
	if err != nil {
		s.platformTenantCredentialError(w, err, acct.Plan)
		return
	}
	if prior.Enabled != policy.Enabled || prior.MaxConsumers != policy.MaxConsumers {
		s.audit.Emit(r.Context(), "platform_tenant.consumer_provisioning_policy_updated", &acct.ID, map[string]any{
			"tenant_id": tenantID, "enabled": policy.Enabled, "max_consumers": policy.MaxConsumers,
		})
	}
	writeJSON(w, http.StatusOK, platformTenantConsumerPolicyResponse(policy))
}
