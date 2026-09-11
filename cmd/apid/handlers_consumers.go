package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Consumer control-plane handlers (ADR-120). Consumer identities are scoped
// to one app and keys are shown in plaintext only from the create response.

func (s *server) consumerFeatureAllowed(w http.ResponseWriter, acct state.Account) bool {
	if acct.Plan.ConsumerKeysPerApp() > 0 {
		return true
	}
	api.WriteProblem(w, api.ErrConsumerKeysNotAllowed(acct.Plan))
	return false
}

func (s *server) consumerApp(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, bool) {
	return s.loadApp(w, r, acct, r.PathValue("slug"))
}

func consumerResponse(c state.APIConsumer) api.APIConsumerResponse {
	return api.APIConsumerResponse{
		ID: c.ID, AppID: c.AppID, ExternalRef: c.ExternalRef, Name: c.Name,
		Status: string(c.Status), CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		RevokedAt: c.RevokedAt,
	}
}

func consumerKeyResponse(k state.ConsumerKey, plaintext string) api.ConsumerKeyResponse {
	return api.ConsumerKeyResponse{
		ID: k.ID, ConsumerID: k.ConsumerID, Name: k.Name, Prefix: k.Prefix,
		Scopes: append([]string(nil), k.Scopes...), CreatedAt: k.CreatedAt,
		ExpiresAt: k.ExpiresAt, LastUsedAt: k.LastUsedAt, RevokedAt: k.RevokedAt,
		Key: plaintext,
	}
}

func (s *server) listAPIConsumers(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.consumerFeatureAllowed(w, acct) {
		return
	}
	app, ok := s.consumerApp(w, r, acct)
	if !ok {
		return
	}
	rows, err := s.store.ListAPIConsumersForApp(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list API consumers"))
		return
	}
	out := make([]api.APIConsumerResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, consumerResponse(row))
	}
	writeJSON(w, http.StatusOK, api.APIConsumerListResponse{Consumers: out})
}

func (s *server) createAPIConsumer(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.consumerFeatureAllowed(w, acct) {
		return
	}
	app, ok := s.consumerApp(w, r, acct)
	if !ok {
		return
	}
	var req api.CreateAPIConsumerRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	req.ExternalRef = strings.TrimSpace(req.ExternalRef)
	req.Name = strings.TrimSpace(req.Name)
	if req.ExternalRef == "" || len(req.ExternalRef) > 256 || req.Name == "" || len(req.Name) > 128 {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid consumer", "external_ref must be 1-256 characters and name must be 1-128 characters"))
		return
	}
	c, err := s.store.CreateAPIConsumer(r.Context(), acct.ID, app.ID, req.ExternalRef, req.Name)
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
				"Consumer already exists", "external_ref is already registered for this app"))
			return
		}
		api.WriteProblem(w, api.ErrInternal("could not create API consumer"))
		return
	}
	s.audit.Emit(r.Context(), "api_consumer.created", &acct.ID, map[string]any{
		"app_id": app.ID, "consumer_id": c.ID, "external_ref": c.ExternalRef,
	})
	writeJSON(w, http.StatusCreated, consumerResponse(c))
}

func (s *server) getAPIConsumer(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.consumerFeatureAllowed(w, acct) {
		return
	}
	app, ok := s.consumerApp(w, r, acct)
	if !ok {
		return
	}
	c, err := s.store.GetAPIConsumerByID(r.Context(), acct.ID, r.PathValue("consumer_id"))
	if err != nil || c.AppID != app.ID {
		s.notFound(w, "no such consumer")
		return
	}
	writeJSON(w, http.StatusOK, consumerResponse(c))
}

func (s *server) revokeAPIConsumer(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.consumerFeatureAllowed(w, acct) {
		return
	}
	app, ok := s.consumerApp(w, r, acct)
	if !ok {
		return
	}
	c, err := s.store.GetAPIConsumerByID(r.Context(), acct.ID, r.PathValue("consumer_id"))
	if err != nil || c.AppID != app.ID {
		s.notFound(w, "no such consumer")
		return
	}
	c, err = s.store.RevokeAPIConsumer(r.Context(), acct.ID, c.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not revoke API consumer"))
		return
	}
	s.audit.Emit(r.Context(), "api_consumer.revoked", &acct.ID, map[string]any{
		"app_id": app.ID, "consumer_id": c.ID,
	})
	writeJSON(w, http.StatusOK, consumerResponse(c))
}

func (s *server) listConsumerKeys(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.consumerFeatureAllowed(w, acct) {
		return
	}
	app, ok := s.consumerApp(w, r, acct)
	if !ok {
		return
	}
	c, err := s.store.GetAPIConsumerByID(r.Context(), acct.ID, r.PathValue("consumer_id"))
	if err != nil || c.AppID != app.ID {
		s.notFound(w, "no such consumer")
		return
	}
	rows, err := s.store.ListConsumerKeysForApp(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list consumer keys"))
		return
	}
	out := make([]api.ConsumerKeyResponse, 0)
	for _, row := range rows {
		if row.ConsumerID == c.ID {
			out = append(out, consumerKeyResponse(row, ""))
		}
	}
	writeJSON(w, http.StatusOK, api.ConsumerKeyListResponse{Keys: out})
}

func (s *server) countConsumerKeys(ctx context.Context, acctID, appID string) (int, error) {
	rows, err := s.store.ListConsumerKeysForApp(ctx, acctID, appID)
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

func (s *server) countAccountConsumerKeys(ctx context.Context, acctID string) (int, error) {
	apps, err := s.store.ListApps(ctx, acctID)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, app := range apps {
		rows, err := s.store.ListConsumerKeysForApp(ctx, acctID, app.ID)
		if err != nil {
			return 0, err
		}
		count += len(rows)
	}
	return count, nil
}

func (s *server) createConsumerKey(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.consumerFeatureAllowed(w, acct) {
		return
	}
	app, ok := s.consumerApp(w, r, acct)
	if !ok {
		return
	}
	c, err := s.store.GetAPIConsumerByID(r.Context(), acct.ID, r.PathValue("consumer_id"))
	if err != nil || c.AppID != app.ID {
		s.notFound(w, "no such consumer")
		return
	}
	if !c.Active() {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Consumer is revoked", "keys cannot be issued for a revoked consumer"))
		return
	}
	var req api.CreateConsumerKeyRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if strings.TrimSpace(req.Name) == "" || len(req.Name) > 64 || len(req.Scopes) == 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid consumer key", "name must be 1-64 characters and scopes must not be empty"))
		return
	}
	seen := make(map[string]struct{}, len(req.Scopes))
	for i, scope := range req.Scopes {
		req.Scopes[i] = strings.TrimSpace(scope)
		if req.Scopes[i] != "read" && req.Scopes[i] != "write" && req.Scopes[i] != "admin" {
			api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
				"Invalid consumer key scope", "scopes must contain only read, write, or admin"))
			return
		}
		if _, ok := seen[req.Scopes[i]]; ok {
			api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
				"Invalid consumer key scope", "scopes must not contain duplicates"))
			return
		}
		seen[req.Scopes[i]] = struct{}{}
	}
	if req.ExpiresAt != nil && !req.ExpiresAt.After(time.Now()) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid expiry", "expires_at must be in the future"))
		return
	}
	appCount, err := s.countConsumerKeys(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not count consumer keys"))
		return
	}
	if appCount >= acct.Plan.ConsumerKeysPerApp() {
		api.WriteProblem(w, api.ErrConsumerKeyQuota(acct.Plan, api.PlanQuotaScopeApp, acct.Plan.ConsumerKeysPerApp(), appCount))
		return
	}
	accountCount, err := s.countAccountConsumerKeys(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not count account consumer keys"))
		return
	}
	if accountCount >= acct.Plan.ConsumerKeysPerAccount() {
		api.WriteProblem(w, api.ErrConsumerKeyQuota(acct.Plan, api.PlanQuotaScopeAccount, acct.Plan.ConsumerKeysPerAccount(), accountCount))
		return
	}
	plaintext, prefix, hash, err := api.GenerateConsumerKey()
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not generate consumer key"))
		return
	}
	k, err := s.store.CreateConsumerKeyForConsumer(r.Context(), acct.ID, c.ID, strings.TrimSpace(req.Name), prefix, hash, req.Scopes, req.ExpiresAt)
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
				"Consumer key already exists", "a key with this name or generated prefix already exists for the app"))
			return
		}
		api.WriteProblem(w, api.ErrInternal("could not create consumer key"))
		return
	}
	s.audit.Emit(r.Context(), "consumer_key.created", &acct.ID, map[string]any{
		"app_id": app.ID, "consumer_id": c.ID, "key_id": k.ID, "name": k.Name,
		"scopes": k.Scopes,
	})
	writeJSON(w, http.StatusCreated, consumerKeyResponse(k, plaintext))
}

func (s *server) revokeConsumerKey(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.consumerFeatureAllowed(w, acct) {
		return
	}
	app, ok := s.consumerApp(w, r, acct)
	if !ok {
		return
	}
	c, err := s.store.GetAPIConsumerByID(r.Context(), acct.ID, r.PathValue("consumer_id"))
	if err != nil || c.AppID != app.ID {
		s.notFound(w, "no such consumer")
		return
	}
	k, err := s.store.GetConsumerKeyByID(r.Context(), acct.ID, r.PathValue("key_id"))
	if err != nil || k.AppID != app.ID || k.ConsumerID != c.ID {
		s.notFound(w, "no such consumer key")
		return
	}
	k, err = s.store.RevokeConsumerKey(r.Context(), acct.ID, k.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not revoke consumer key"))
		return
	}
	s.audit.Emit(r.Context(), "consumer_key.revoked", &acct.ID, map[string]any{
		"app_id": app.ID, "consumer_id": c.ID, "key_id": k.ID,
	})
	writeJSON(w, http.StatusOK, consumerKeyResponse(k, ""))
}
