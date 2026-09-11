package main

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	defaultDeployTokenLifetime = 90 * 24 * time.Hour
	maxDeployTokenLifetime     = 365 * 24 * time.Hour
)

func (s *server) deployTokenStore(w http.ResponseWriter) (state.DeployTokenStore, bool) {
	store, ok := any(s.store).(state.DeployTokenStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("deploy tokens are unavailable"))
	}
	return store, ok
}

func parseDeployTokenExpiry(raw string, now time.Time) (time.Time, *api.Problem) {
	if strings.TrimSpace(raw) == "" {
		return now.Add(defaultDeployTokenLifetime), nil
	}
	expiresAt, err := time.Parse(time.RFC3339, raw)
	if err != nil || !expiresAt.After(now) {
		return time.Time{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid deploy token expiry", "expires_at must be a future RFC3339 timestamp")
	}
	if expiresAt.After(now.Add(maxDeployTokenLifetime)) {
		return time.Time{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid deploy token expiry", "expires_at cannot be more than 365 days in the future")
	}
	return expiresAt.UTC(), nil
}

func deployTokenResponse(t state.DeployToken) api.DeployTokenResponse {
	resp := api.DeployTokenResponse{
		ID: t.ID, AppID: t.AppID, Prefix: api.DeployTokenPrefix,
		Label: t.Label, Scopes: t.Scopes, Status: t.Status,
		CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339),
	}
	if t.ExpiresAt != nil {
		resp.ExpiresAt = t.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if t.LastUsedAt != nil {
		resp.LastUsedAt = t.LastUsedAt.UTC().Format(time.RFC3339)
	}
	if t.RevokedAt != nil {
		resp.RevokedAt = t.RevokedAt.UTC().Format(time.RFC3339)
	}
	if t.RotatedFromID != nil {
		resp.RotatedFromID = *t.RotatedFromID
	}
	return resp
}

func (s *server) listDeployTokens(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.deployTokenStore(w)
	if !ok {
		return
	}
	tokens, err := store.ListDeployTokensForApp(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list deploy tokens"))
		return
	}
	out := make([]api.DeployTokenResponse, 0, len(tokens))
	for _, token := range tokens {
		out = append(out, deployTokenResponse(token))
	}
	writeJSON(w, http.StatusOK, api.ListDeployTokensResponse{Tokens: out})
}

func (s *server) createDeployToken(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.deployTokenStore(w)
	if !ok {
		return
	}
	var req api.CreateDeployTokenRequest
	if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid deploy token request", "request body must be valid JSON"))
		return
	}
	if len(req.Label) > 100 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid deploy token label", "label must be at most 100 characters"))
		return
	}
	expiresAt, problem := parseDeployTokenExpiry(req.ExpiresAt, time.Now().UTC())
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	plaintext, hash, err := api.GenerateDeployToken()
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not generate deploy token"))
		return
	}
	token, err := store.CreateDeployToken(r.Context(), acct.ID, app.ID, hash, req.Label,
		[]string{api.ScopeDeployWrite}, expiresAt)
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.ErrCapacity("could not allocate deploy token"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not create deploy token"))
		return
	}
	resp := deployTokenResponse(token)
	resp.Plaintext = plaintext
	s.audit.Emit(r.Context(), "deploy_token.created", &acct.ID, map[string]any{
		"token_id": token.ID, "app_id": app.ID, "scopes": token.Scopes,
		"expires_at": token.ExpiresAt.UTC().Format(time.RFC3339),
	})
	writeJSON(w, http.StatusCreated, resp)
}

func (s *server) revokeDeployToken(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.deployTokenStore(w)
	if !ok {
		return
	}
	token, err := store.RevokeDeployToken(r.Context(), acct.ID, app.ID, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such deploy token")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not revoke deploy token"))
		return
	}
	s.audit.Emit(r.Context(), "deploy_token.revoked", &acct.ID, map[string]any{
		"token_id": token.ID, "app_id": app.ID,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) rotateDeployToken(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.deployTokenStore(w)
	if !ok {
		return
	}
	var req api.RotateDeployTokenRequest
	if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid deploy token request", "request body must be valid JSON"))
		return
	}
	if len(req.Label) > 100 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid deploy token label", "label must be at most 100 characters"))
		return
	}
	expiresAt, problem := parseDeployTokenExpiry(req.ExpiresAt, time.Now().UTC())
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	plaintext, hash, err := api.GenerateDeployToken()
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not generate deploy token"))
		return
	}
	newToken, oldToken, err := store.RotateDeployToken(r.Context(), acct.ID, app.ID,
		r.PathValue("id"), hash, req.Label, expiresAt, 0)
	if err != nil {
		switch {
		case errors.Is(err, state.ErrNotFound):
			s.notFound(w, "no such deploy token")
		case errors.Is(err, state.ErrAPIKeyRevoked):
			s.notFound(w, "no such deploy token")
		case errors.Is(err, state.ErrAPIKeyExpired):
			api.WriteProblem(w, api.ErrAPIKeyExpired())
		default:
			api.WriteProblem(w, api.ErrCapacity("could not rotate deploy token"))
		}
		return
	}
	resp := api.RotateDeployTokenResponse{
		Token: deployTokenResponse(newToken), TokenPlaintext: plaintext,
		OldTokenID: oldToken.ID,
	}
	if oldToken.ExpiresAt != nil {
		resp.OldTokenExpires = oldToken.ExpiresAt.UTC().Format(time.RFC3339)
	}
	s.audit.Emit(r.Context(), "deploy_token.rotated", &acct.ID, map[string]any{
		"token_id": newToken.ID, "old_token_id": oldToken.ID, "app_id": app.ID,
	})
	writeJSON(w, http.StatusCreated, resp)
}
