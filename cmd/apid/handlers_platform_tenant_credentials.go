package main

import (
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) platformTenantCredentialStore(w http.ResponseWriter, acct state.Account) (state.PlatformTenantCredentialStore, bool) {
	if !s.consumerFeatureAllowed(w, acct) {
		return nil, false
	}
	store, ok := s.store.(state.PlatformTenantCredentialStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant credentials are unavailable"))
	}
	return store, ok
}

func (s *server) applyPlatformTenantCredentials(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.platformTenantCredentialStore(w, acct)
	if !ok {
		return
	}
	tenantID := r.PathValue("id")
	if _, err := uuid.Parse(tenantID); err != nil {
		s.notFound(w, "no such platform tenant")
		return
	}
	var req api.ApplyPlatformTenantCredentialsRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	in, err := platformTenantCredentialInput(acct, tenantID, req)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid credential bundle", err.Error()))
		return
	}
	result, err := store.ApplyPlatformTenantCredentials(r.Context(), in)
	if err != nil {
		s.platformTenantCredentialError(w, err, acct.Plan)
		return
	}
	if !req.DryRun {
		for _, row := range result.Keys {
			if row.Action == "create" || row.Action == "revoke" {
				s.audit.Emit(r.Context(), "platform_tenant.credential_"+row.Action, &acct.ID,
					map[string]any{"tenant_id": tenantID, "consumer_id": row.Key.ConsumerID, "key_id": row.Key.ID})
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, platformTenantCredentialResponse(result))
}

func platformTenantCredentialInput(acct state.Account, tenantID string, req api.ApplyPlatformTenantCredentialsRequest) (state.ApplyPlatformTenantCredentialsParams, error) {
	in := state.ApplyPlatformTenantCredentialsParams{AccountID: acct.ID, TenantID: tenantID, DryRun: req.DryRun,
		AppLimit: acct.Plan.ConsumerKeysPerApp(), AccountLimit: acct.Plan.ConsumerKeysPerAccount(),
		RevokeKeyIDs: req.RevokeKeyIDs}
	for _, wanted := range req.Keys {
		hash, err := hex.DecodeString(wanted.Hash)
		if err != nil {
			return in, err
		}
		in.Keys = append(in.Keys, state.PlatformTenantCredentialIntent{ConsumerID: wanted.ConsumerID,
			Name: wanted.Name, Prefix: wanted.Prefix, Hash: hash, Scopes: wanted.Scopes, ExpiresAt: wanted.ExpiresAt})
	}
	return in, nil
}

func (s *server) platformTenantCredentialError(w http.ResponseWriter, err error, plan api.Plan) {
	var quota *state.PlatformTenantCredentialQuotaError
	switch {
	case errors.As(err, &quota):
		api.WriteProblem(w, api.ErrConsumerKeyQuota(plan, quota.Scope, quota.Limit, quota.Observed))
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no such tenant, consumer, or key")
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Credential conflict", "the consumer is inactive, the key name or prefix is occupied, or an existing key differs"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid credential bundle", "key metadata, hashes, scopes, and revocations must be valid and unique"))
	default:
		api.WriteProblem(w, api.ErrInternal("could not apply platform tenant credentials"))
	}
}

func platformTenantCredentialResponse(result state.ApplyPlatformTenantCredentialsResult) api.ApplyPlatformTenantCredentialsResponse {
	out := api.ApplyPlatformTenantCredentialsResponse{TenantID: result.TenantID, DryRun: result.DryRun,
		Keys: make([]api.PlatformTenantCredentialResult, 0, len(result.Keys))}
	for _, row := range result.Keys {
		out.Keys = append(out.Keys, api.PlatformTenantCredentialResult{
			PlatformTenantCredentialMetadata: platformTenantCredentialMetadata(row.Key), Action: row.Action})
	}
	return out
}

func (s *server) listPlatformTenantCredentials(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.platformTenantCredentialStore(w, acct)
	if !ok {
		return
	}
	tenantID := r.PathValue("id")
	if _, err := uuid.Parse(tenantID); err != nil {
		s.notFound(w, "no such platform tenant")
		return
	}
	limit, offset := 100, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			api.WriteProblem(w, api.NewProblem(400, api.CodeValidation, "Invalid limit", "limit must be 1-100"))
			return
		}
		limit = value
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			api.WriteProblem(w, api.NewProblem(400, api.CodeValidation, "Invalid offset", "offset must be non-negative"))
			return
		}
		offset = value
	}
	rows, err := store.ListPlatformTenantCredentials(r.Context(), acct.ID, tenantID, limit, offset)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such platform tenant")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list platform tenant credentials"))
		return
	}
	out := api.PlatformTenantCredentialsResponse{Keys: make([]api.PlatformTenantCredentialMetadata, 0, len(rows))}
	for _, row := range rows {
		out.Keys = append(out.Keys, platformTenantCredentialMetadata(row))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func platformTenantCredentialMetadata(key state.ConsumerKey) api.PlatformTenantCredentialMetadata {
	out := api.PlatformTenantCredentialMetadata{ID: key.ID, ConsumerID: key.ConsumerID,
		Name: key.Name, Prefix: key.Prefix, Scopes: append([]string(nil), key.Scopes...),
		ExpiresAt: key.ExpiresAt, LastUsedAt: key.LastUsedAt, RevokedAt: key.RevokedAt}
	if !key.CreatedAt.IsZero() {
		out.CreatedAt = &key.CreatedAt
	}
	return out
}
