package main

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) platformTenantSelfCredentialStores(w http.ResponseWriter, r *http.Request, acct state.Account) (state.PlatformTenant, state.PlatformTenantStore, state.PlatformTenantCredentialStore, bool) {
	tenantID, ok := platformTenantSelfID(w, r)
	if !ok {
		return state.PlatformTenant{}, nil, nil, false
	}
	tenantStore, ok := s.platformTenantStore(w, acct)
	if !ok {
		return state.PlatformTenant{}, nil, nil, false
	}
	tenant, err := tenantStore.GetPlatformTenant(r.Context(), acct.ID, tenantID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no such platform tenant")
		return state.PlatformTenant{}, nil, nil, false
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not load platform tenant"))
		return state.PlatformTenant{}, nil, nil, false
	}
	credentialStore, ok := s.store.(state.PlatformTenantCredentialStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant credentials are unavailable"))
		return state.PlatformTenant{}, nil, nil, false
	}
	return tenant, tenantStore, credentialStore, true
}

func (s *server) listPlatformTenantSelfConsumers(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, tenantStore, _, ok := s.platformTenantSelfCredentialStores(w, r, acct)
	if !ok {
		return
	}
	consumers, err := tenantStore.ListPlatformTenantConsumers(r.Context(), acct.ID, tenant.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list tenant consumers"))
		return
	}
	out := api.PlatformTenantSelfConsumersResponse{Consumers: make([]api.PlatformTenantSelfConsumerResponse, 0, len(consumers))}
	for _, consumer := range consumers {
		out.Consumers = append(out.Consumers, api.PlatformTenantSelfConsumerResponse{
			ConsumerID: consumer.ID, ExternalRef: consumer.ExternalRef, Name: consumer.Name, Status: string(consumer.Status),
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *server) listPlatformTenantSelfCredentials(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, _, credentialStore, ok := s.platformTenantSelfCredentialStores(w, r, acct)
	if !ok {
		return
	}
	limit, offset := 100, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid limit", "limit must be 1-100"))
			return
		}
		limit = value
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid offset", "offset must be non-negative"))
			return
		}
		offset = value
	}
	rows, err := credentialStore.ListPlatformTenantCredentials(r.Context(), acct.ID, tenant.ID, limit, offset)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such platform tenant")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list tenant credentials"))
		return
	}
	out := api.PlatformTenantCredentialsResponse{Keys: make([]api.PlatformTenantCredentialMetadata, 0, len(rows))}
	for _, row := range rows {
		out.Keys = append(out.Keys, platformTenantCredentialMetadata(row))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *server) applyPlatformTenantSelfCredentials(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, _, credentialStore, ok := s.platformTenantSelfCredentialStores(w, r, acct)
	if !ok {
		return
	}
	var req api.ApplyPlatformTenantCredentialsRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	in, err := platformTenantCredentialInput(acct, tenant.ID, req)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid credential bundle", err.Error()))
		return
	}
	in.EnforceDelegationPolicy = true
	result, err := credentialStore.ApplyPlatformTenantCredentials(r.Context(), in)
	if err != nil {
		s.platformTenantSelfCredentialError(w, err, acct.Plan)
		return
	}
	if !req.DryRun {
		for _, row := range result.Keys {
			if row.Action == "create" || row.Action == "revoke" {
				s.audit.Emit(r.Context(), "platform_tenant.self_credential_"+row.Action, &acct.ID,
					map[string]any{"tenant_id": tenant.ID, "consumer_id": row.Key.ConsumerID, "key_id": row.Key.ID})
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, platformTenantCredentialResponse(result))
}

func (s *server) platformTenantSelfCredentialError(w http.ResponseWriter, err error, plan api.Plan) {
	var quota *state.PlatformTenantCredentialPolicyQuotaError
	switch {
	case errors.Is(err, state.ErrPlatformTenantCredentialDelegationDisabled):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "platform_tenant_credential_delegation_disabled",
			"Credential self-service is disabled", "ask the platform owner to enable credential delegation for this tenant"))
	case errors.Is(err, state.ErrPlatformTenantCredentialScopeDenied):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "platform_tenant_credential_scope_denied",
			"Credential scope is not delegated", "the platform owner has not allowed one or more requested key scopes"))
	case errors.As(err, &quota):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "platform_tenant_credential_limit_reached",
			"Delegated credential limit reached", "the platform owner sets the maximum active key count per linked consumer"))
	default:
		s.platformTenantCredentialError(w, err, plan)
	}
}
