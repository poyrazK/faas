package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	platformTenantAccessTokenDefaultTTL = 90 * 24 * time.Hour
	platformTenantAccessTokenMaxTTL     = 365 * 24 * time.Hour
	platformTenantStatementPageSize     = 100
	platformTenantStatementMaxOffset    = 1000000
)

func (s *server) platformTenantAccessStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.PlatformTenant, state.PlatformTenantAccessStore, bool) {
	tenants, ok := s.platformTenantStore(w, acct)
	if !ok {
		return state.PlatformTenant{}, nil, false
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, tenants)
	if !ok {
		return state.PlatformTenant{}, nil, false
	}
	store, ok := s.store.(state.PlatformTenantAccessStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant access tokens are unavailable"))
	}
	return tenant, store, ok
}

func platformTenantAccessTokenResponse(token state.PlatformTenantAccessToken) api.PlatformTenantAccessTokenResponse {
	return api.PlatformTenantAccessTokenResponse{ID: token.ID, TenantID: token.TenantID, Name: token.Name,
		Prefix: token.Prefix, Scopes: append([]string(nil), token.Scopes...), CreatedAt: token.CreatedAt,
		ExpiresAt: token.ExpiresAt, LastUsedAt: token.LastUsedAt, RevokedAt: token.RevokedAt}
}

func (s *server) listPlatformTenantAccessTokens(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, store, ok := s.platformTenantAccessStore(w, r, acct)
	if !ok {
		return
	}
	tokens, err := store.ListPlatformTenantAccessTokens(r.Context(), acct.ID, tenant.ID)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such platform tenant")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list platform tenant access tokens"))
		return
	}
	out := api.PlatformTenantAccessTokenListResponse{Tokens: make([]api.PlatformTenantAccessTokenResponse, 0, len(tokens))}
	for _, token := range tokens {
		out.Tokens = append(out.Tokens, platformTenantAccessTokenResponse(token))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createPlatformTenantAccessToken(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, store, ok := s.platformTenantAccessStore(w, r, acct)
	if !ok {
		return
	}
	var req api.CreatePlatformTenantAccessTokenRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	invalidName := len(req.Name) == 0 || len(req.Name) > 64
	for _, char := range req.Name {
		invalidName = invalidName || unicode.IsControl(char)
	}
	if invalidName || len(req.Scopes) == 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid platform tenant token", "name must be 1-64 characters and at least one read scope is required"))
		return
	}
	seen := make(map[string]bool, len(req.Scopes))
	for _, scope := range req.Scopes {
		if (scope != api.ScopePlatformTenantUsageRead && scope != api.ScopePlatformTenantStatementsRead) || seen[scope] {
			api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
				"Invalid platform tenant token scopes", "scopes may contain platform_tenant:usage:read and/or platform_tenant:statements:read"))
			return
		}
		seen[scope] = true
	}
	now := time.Now().UTC()
	expiresAt := now.Add(platformTenantAccessTokenDefaultTTL)
	if req.ExpiresAt != nil {
		expiresAt = req.ExpiresAt.UTC()
	}
	if !expiresAt.After(now) || expiresAt.After(now.Add(platformTenantAccessTokenMaxTTL)) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid expiration", "expires_at must be in the future and no more than 365 days away"))
		return
	}
	plaintext, prefix, hash, err := api.GeneratePlatformTenantAccessToken()
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not generate platform tenant access token"))
		return
	}
	token, err := store.CreatePlatformTenantAccessToken(r.Context(), state.PlatformTenantAccessTokenInput{
		AccountID: acct.ID, TenantID: tenant.ID, Name: req.Name, Prefix: prefix, TokenHash: hash,
		Scopes: req.Scopes, ExpiresAt: expiresAt,
	})
	var quota *state.PlatformTenantAccessTokenQuotaError
	switch {
	case errors.As(err, &quota):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "platform_tenant_token_quota",
			"Platform tenant token quota reached", "revoke an unused token before creating another"))
		return
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Token name already exists", "choose a unique name among active platform tenant tokens"))
		return
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no such platform tenant")
		return
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid platform tenant token", "check the token name, scopes, and expiration"))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not create platform tenant access token"))
		return
	}
	s.audit.Emit(r.Context(), "platform_tenant.access_token_created", &acct.ID,
		map[string]any{"tenant_id": tenant.ID, "token_id": token.ID, "scopes": token.Scopes, "expires_at": token.ExpiresAt})
	out := api.CreatePlatformTenantAccessTokenResponse{PlatformTenantAccessTokenResponse: platformTenantAccessTokenResponse(token), Token: plaintext}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, out)
}

func (s *server) revokePlatformTenantAccessToken(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, store, ok := s.platformTenantAccessStore(w, r, acct)
	if !ok {
		return
	}
	if _, err := uuid.Parse(r.PathValue("token_id")); err != nil {
		s.notFound(w, "no such platform tenant access token")
		return
	}
	token, changed, err := store.RevokePlatformTenantAccessToken(r.Context(), acct.ID, tenant.ID, r.PathValue("token_id"))
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such platform tenant access token")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not revoke platform tenant access token"))
		return
	}
	if changed {
		s.audit.Emit(r.Context(), "platform_tenant.access_token_revoked", &acct.ID,
			map[string]any{"tenant_id": tenant.ID, "token_id": token.ID})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, platformTenantAccessTokenResponse(token))
}

func platformTenantSelfID(w http.ResponseWriter, r *http.Request) (string, bool) {
	principal, ok := principalFrom(r)
	if !ok || principal.Key == nil || principal.Key.PlatformTenantID == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden,
			"Platform tenant token required", "use a token issued for this downstream tenant"))
		return "", false
	}
	return principal.Key.PlatformTenantID, true
}

func (s *server) getPlatformTenantSelfUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantID, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	r.SetPathValue("id", tenantID)
	s.getPlatformTenantUsage(w, r, acct)
}

func (s *server) platformTenantSelfStatementStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.PlatformTenant, state.PlatformTenantStatementStore, bool) {
	tenantID, ok := platformTenantSelfID(w, r)
	if !ok {
		return state.PlatformTenant{}, nil, false
	}
	tenants, ok := s.platformTenantStore(w, acct)
	if !ok {
		return state.PlatformTenant{}, nil, false
	}
	tenant, err := tenants.GetPlatformTenant(r.Context(), acct.ID, tenantID)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such platform tenant")
		return state.PlatformTenant{}, nil, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load platform tenant"))
		return state.PlatformTenant{}, nil, false
	}
	statements, ok := s.store.(state.PlatformTenantStatementStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant statements are unavailable"))
		return state.PlatformTenant{}, nil, false
	}
	return tenant, statements, true
}

func (s *server) listPlatformTenantSelfStatements(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, store, ok := s.platformTenantSelfStatementStore(w, r, acct)
	if !ok {
		return
	}
	start, err1 := time.Parse(time.RFC3339, r.URL.Query().Get("period_start"))
	end, err2 := time.Parse(time.RFC3339, r.URL.Query().Get("period_end"))
	if err1 != nil || err2 != nil || !validateTenantStatementPeriod(start, end) {
		tenantStatementPeriodProblem(w)
		return
	}
	limit := platformTenantStatementPageSize
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > platformTenantStatementPageSize {
			api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
				"Invalid page size", "limit must be between 1 and 100"))
			return
		}
		limit = parsed
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed > platformTenantStatementMaxOffset {
			api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
				"Invalid page offset", "offset must be between 0 and 1000000"))
			return
		}
		offset = parsed
	}
	rows, err := store.ListFinalizedPlatformTenantStatements(r.Context(), acct.ID, tenant.ID, start.UTC(), end.UTC(), limit+1, offset)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list finalized platform tenant statements"))
		return
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	out := api.PlatformTenantSelfStatementListResponse{Statements: make([]api.PlatformTenantStatementSummaryResponse, 0, len(rows))}
	for _, row := range rows {
		out.Statements = append(out.Statements, platformTenantStatementSummaryResponse(row))
	}
	if hasMore {
		next := offset + len(rows)
		out.NextOffset = &next
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func platformTenantStatementSummaryResponse(s state.PlatformTenantStatementSummary) api.PlatformTenantStatementSummaryResponse {
	return api.PlatformTenantStatementSummaryResponse{ID: s.ID, TenantID: s.TenantID,
		PeriodStart: s.PeriodStart, PeriodEnd: s.PeriodEnd, Revision: s.Revision, Status: string(s.Status),
		Currency: s.Currency, BillableUnits: s.BillableUnits, UnpricedUnits: s.UnpricedUnits,
		AmountMillicents: s.AmountMillicents, AsOf: s.AsOf, CreatedAt: s.CreatedAt, FinalizedAt: s.FinalizedAt}
}

func (s *server) getPlatformTenantSelfStatement(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, store, ok := s.platformTenantSelfStatementStore(w, r, acct)
	if !ok {
		return
	}
	if _, err := uuid.Parse(r.PathValue("statement_id")); err != nil {
		s.notFound(w, "no such finalized platform tenant statement")
		return
	}
	statement, err := store.GetPlatformTenantStatement(r.Context(), acct.ID, tenant.ID, r.PathValue("statement_id"))
	if errors.Is(err, state.ErrNotFound) || err == nil && statement.Status != state.APIConsumerUsageStatementFinalized {
		s.notFound(w, "no such finalized platform tenant statement")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load finalized platform tenant statement"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, platformTenantStatementResponse(statement))
}
