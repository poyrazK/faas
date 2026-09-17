package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type realtimeEndpointRegistrar interface {
	RegisterEndpoint(context.Context, realtime.Endpoint) error
	RemoveEndpoint(context.Context, string) error
}

func realtimeEndpointStore(s *server) (state.ManagedRealtimeEndpointStore, bool) {
	store, ok := s.store.(state.ManagedRealtimeEndpointStore)
	return store, ok
}

func validateRealtimeCallbackURL(raw string) *api.Problem {
	if raw == "" {
		return api.ErrRealtimeInvalid("callback_url is required")
	}
	if len(raw) > api.RealtimeCallbackURLMaxBytes {
		return api.ErrRealtimeInvalid(fmt.Sprintf("callback_url exceeds %d bytes", api.RealtimeCallbackURLMaxBytes))
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return api.ErrRealtimeInvalid("callback_url must be an absolute http:// or https:// URL")
	}
	return nil
}

func normalizeRealtimePath(path, name, fallback string) (string, *api.Problem) {
	if path == "" {
		return fallback, nil
	}
	if len(path) > api.RealtimePathMaxBytes || !strings.HasPrefix(path, "/") || strings.Contains(path, "..") || strings.ContainsAny(path, "?#") {
		return "", api.ErrRealtimeInvalid(fmt.Sprintf("%s must start with /, contain no .. segments, and be at most %d bytes", name, api.RealtimePathMaxBytes))
	}
	return path, nil
}

func validateRealtimeToken(token, field string, required bool) *api.Problem {
	if required && token == "" {
		return api.ErrRealtimeInvalid(field + " is required")
	}
	if len(token) > api.RealtimeCallbackAuthTokenMaxBytes {
		return api.ErrRealtimeInvalid(fmt.Sprintf("%s exceeds %d bytes", field, api.RealtimeCallbackAuthTokenMaxBytes))
	}
	return nil
}

func validateRealtimePolicy(origins []string, maxConnections int, maxMessageBytes, maxConnectionAgeSeconds int64) *api.Problem {
	if err := api.ValidateRealtimeOrigins(origins); err != nil {
		return api.ErrRealtimeInvalid(err.Error())
	}
	if maxConnections < 0 || maxConnections > api.RealtimeMaxConnections {
		return api.ErrRealtimeInvalid(fmt.Sprintf("max_connections must be between 0 and %d", api.RealtimeMaxConnections))
	}
	if maxMessageBytes < 0 || maxMessageBytes > api.RealtimeMessageMaxBytes {
		return api.ErrRealtimeInvalid(fmt.Sprintf("max_message_bytes must be between 0 and %d", api.RealtimeMessageMaxBytes))
	}
	if maxConnectionAgeSeconds < 0 || maxConnectionAgeSeconds > api.RealtimeMaxConnectionAgeSeconds {
		return api.ErrRealtimeInvalid(fmt.Sprintf("max_connection_age_seconds must be between 0 and %d", api.RealtimeMaxConnectionAgeSeconds))
	}
	return nil
}

func sealRealtimeToken(plaintext, label string) ([]byte, *api.Problem) {
	if plaintext == "" {
		return []byte{}, nil
	}
	recipient := setSecretRecipient()
	if recipient == nil {
		return nil, api.ErrCapacity("host age recipient not loaded — refusing to seal realtime credential")
	}
	sealed, err := secretbox.SealBytes(recipient, label, []byte(plaintext), api.RealtimeCallbackAuthTokenMaxBytes)
	if err != nil {
		if prob := api.AsProblem(err); prob != nil {
			return nil, prob
		}
		return nil, api.ErrCapacity("could not seal realtime credential")
	}
	return sealed, nil
}

func realtimeEndpointResponse(e state.ManagedRealtimeEndpoint) api.ManagedRealtimeEndpointResponse {
	authMode := e.AuthMode
	if authMode == "" {
		if len(e.AuthTokenSealed) > 0 {
			authMode = api.RealtimeAuthModeStaticBearer
		} else {
			authMode = api.RealtimeAuthModeNone
		}
	}
	out := api.ManagedRealtimeEndpointResponseFromRow(e.ID, e.AppID, e.AccountID, e.CallbackURL,
		e.ConnectPath, e.MessagePath, e.DisconnectPath, authMode, e.AuthIssuer, e.AuthJWKSURL,
		e.AuthAudience, e.AuthAlgorithms, e.AuthRequiredClaims, e.AllowedOrigins, e.MaxConnections,
		e.MaxMessageBytes, e.MaxConnectionAgeSeconds, e.Enabled, e.CreatedAt, e.UpdatedAt)
	if len(e.AuthTokenSealed) == 0 {
		out.AuthTokenMasked = ""
	}
	return out
}

func cloneRealtimeClaims(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func realtimeAuthMode(row state.ManagedRealtimeEndpoint) string {
	if row.AuthMode != "" {
		return row.AuthMode
	}
	if len(row.AuthTokenSealed) > 0 {
		return api.RealtimeAuthModeStaticBearer
	}
	return api.RealtimeAuthModeNone
}

func valueOrZeroTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

// realtimeAuthPolicyAuditData intentionally contains only policy shape and
// configuration booleans. Sealed credential bytes and trust URLs never cross
// the audit boundary.
func realtimeAuthPolicyAuditData(row state.ManagedRealtimeEndpoint) map[string]any {
	return map[string]any{
		"endpoint_id":           row.ID,
		"app_id":                row.AppID,
		"auth_mode":             realtimeAuthMode(row),
		"auth_token_configured": len(row.AuthTokenSealed) > 0,
		"issuer_configured":     row.AuthIssuer != "",
		"jwks_url_configured":   row.AuthJWKSURL != "",
		"audience_count":        len(row.AuthAudience),
		"algorithm_count":       len(row.AuthAlgorithms),
		"required_claim_count":  len(row.AuthRequiredClaims),
	}
}

func realtimeAuthPolicyChanged(before, after state.ManagedRealtimeEndpoint) bool {
	return realtimeAuthMode(before) != realtimeAuthMode(after) ||
		!bytes.Equal(before.AuthTokenSealed, after.AuthTokenSealed) ||
		before.AuthIssuer != after.AuthIssuer ||
		before.AuthJWKSURL != after.AuthJWKSURL ||
		!slices.Equal(before.AuthAudience, after.AuthAudience) ||
		!slices.Equal(before.AuthAlgorithms, after.AuthAlgorithms) ||
		!maps.Equal(before.AuthRequiredClaims, after.AuthRequiredClaims)
}

// syncManagedRealtimeEndpoint mirrors durable endpoint state onto the
// configured local or leased realtime owner when one is configured. Mutation
// paths call this best-effort; the background reconciler repairs missed fanout.
func (s *server) syncManagedRealtimeEndpoint(ctx context.Context, row state.ManagedRealtimeEndpoint) error {
	if s.realtimeRegistrar == nil {
		return nil
	}
	if !row.Enabled {
		return s.realtimeRegistrar.RemoveEndpoint(ctx, row.ID)
	}
	callbackAuth, err := unsealRealtimeCredential(ctx, row.CallbackAuthTokenSealed)
	if err != nil {
		return err
	}
	authToken, err := unsealRealtimeCredential(ctx, row.AuthTokenSealed)
	if err != nil {
		return err
	}
	authTokenPrevious, err := unsealRealtimeCredential(ctx, row.AuthTokenPreviousSealed)
	if err != nil {
		return err
	}
	authMode := row.AuthMode
	if authMode == "" {
		if len(authToken) > 0 {
			authMode = api.RealtimeAuthModeStaticBearer
		} else {
			authMode = api.RealtimeAuthModeNone
		}
	}
	return s.realtimeRegistrar.RegisterEndpoint(ctx, realtime.Endpoint{
		ID: row.ID, AppID: row.AppID, AccountID: row.AccountID,
		CallbackURL: row.CallbackURL, ConnectPath: row.ConnectPath,
		MessagePath: row.MessagePath, DisconnectPath: row.DisconnectPath,
		CallbackAuthToken: string(callbackAuth), AuthToken: string(authToken),
		AuthTokenPrevious:          string(authTokenPrevious),
		AuthTokenPreviousExpiresAt: valueOrZeroTime(row.AuthTokenPreviousExpiresAt),
		ClientAuth: realtime.AuthPolicy{
			Mode: realtime.AuthMode(authMode), Issuer: row.AuthIssuer, JWKSURL: row.AuthJWKSURL,
			Audience:       append([]string(nil), row.AuthAudience...),
			Algorithms:     append([]string(nil), row.AuthAlgorithms...),
			RequiredClaims: cloneRealtimeClaims(row.AuthRequiredClaims),
		},
		AllowedOrigins:   append([]string(nil), row.AllowedOrigins...),
		MaxConnections:   row.MaxConnections,
		MaxMessageBytes:  row.MaxMessageBytes,
		MaxConnectionAge: time.Duration(row.MaxConnectionAgeSeconds) * time.Second,
	})
}

func unsealRealtimeCredential(ctx context.Context, sealed []byte) ([]byte, error) {
	if len(sealed) == 0 {
		return nil, nil
	}
	identities := hostIdentitiesForUnseal(ctx)
	if len(identities) == 0 {
		return nil, errors.New("realtime credential identity unavailable")
	}
	_, plaintext, err := secretbox.OpenBytesMulti(identities, sealed)
	if err != nil {
		return nil, fmt.Errorf("realtime credential unseal: %w", err)
	}
	return plaintext, nil
}

func (s *server) realtimePlanLimits(acct state.Account) (api.RealtimeLimits, bool) {
	limits, ok := api.RealtimeLimitsFor(acct.Plan)
	if !ok || limits.EndpointsPerApp == 0 {
		return limits, false
	}
	return limits, true
}

func (s *server) listManagedRealtimeEndpoints(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := s.realtimePlanLimits(acct); !ok {
		api.WriteProblem(w, api.ErrPlanRealtimeNotAllowed(acct.Plan))
		return
	}
	store, ok := realtimeEndpointStore(s)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("managed realtime endpoint store unavailable"))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	rows, err := store.ListManagedRealtimeEndpointsForApp(r.Context(), app.ID)
	if err != nil {
		s.log.WarnContext(r.Context(), "list managed realtime endpoints", "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not list managed realtime endpoints"))
		return
	}
	out := make([]api.ManagedRealtimeEndpointResponse, 0, len(rows))
	for _, row := range rows {
		if row.AccountID == acct.ID {
			out = append(out, realtimeEndpointResponse(row))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createManagedRealtimeEndpoint(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateManagedRealtimeEndpointRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid(err.Error()))
		return
	}
	limits, allowed := s.realtimePlanLimits(acct)
	if !allowed {
		api.WriteProblem(w, api.ErrPlanRealtimeNotAllowed(acct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if prob := validateRealtimeCallbackURL(req.CallbackURL); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := resolveAndCheckEgress(r.Context(), req.CallbackURL); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := validateRealtimeToken(req.CallbackAuthToken, "callback_auth_token", true); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if prob := validateRealtimeToken(req.AuthToken, "auth_token", false); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	authMode, err := api.NormalizeRealtimeAuthMode(req.AuthMode, req.AuthToken != "")
	if err != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid(err.Error()))
		return
	}
	if err := api.ValidateRealtimeAuth(authMode, req.AuthToken != "", req.AuthIssuer, req.AuthJWKSURL, req.AuthAudience, req.AuthAlgorithms, req.AuthRequiredClaims); err != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid(err.Error()))
		return
	}
	if prob := validateRealtimePolicy(req.AllowedOrigins, req.MaxConnections, req.MaxMessageBytes, req.MaxConnectionAgeSeconds); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	connectPath, prob := normalizeRealtimePath(req.ConnectPath, "connect_path", api.DefaultRealtimeConnectPath)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	messagePath, prob := normalizeRealtimePath(req.MessagePath, "message_path", api.DefaultRealtimeMessagePath)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	disconnectPath, prob := normalizeRealtimePath(req.DisconnectPath, "disconnect_path", api.DefaultRealtimeDisconnectPath)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	callbackSealed, prob := sealRealtimeToken(req.CallbackAuthToken, "REALTIME_CALLBACK_AUTH")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	authToken := req.AuthToken
	if authMode != api.RealtimeAuthModeStaticBearer {
		authToken = ""
	}
	authSealed, prob := sealRealtimeToken(authToken, "REALTIME_AUTH")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	store, ok := realtimeEndpointStore(s)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("managed realtime endpoint store unavailable"))
		return
	}
	row, err := store.CreateManagedRealtimeEndpointIfUnderQuota(r.Context(), state.ManagedRealtimeEndpoint{
		AppID: app.ID, AccountID: acct.ID, CallbackURL: req.CallbackURL,
		ConnectPath: connectPath, MessagePath: messagePath, DisconnectPath: disconnectPath,
		CallbackAuthTokenSealed: callbackSealed, AuthTokenSealed: authSealed,
		AuthMode: authMode, AuthIssuer: req.AuthIssuer, AuthJWKSURL: req.AuthJWKSURL,
		AuthAudience:       append([]string(nil), req.AuthAudience...),
		AuthAlgorithms:     append([]string(nil), req.AuthAlgorithms...),
		AuthRequiredClaims: cloneRealtimeClaims(req.AuthRequiredClaims),
		AllowedOrigins:     append([]string(nil), req.AllowedOrigins...),
		MaxConnections:     req.MaxConnections, MaxMessageBytes: req.MaxMessageBytes,
		MaxConnectionAgeSeconds: req.MaxConnectionAgeSeconds, Enabled: enabled,
	}, limits.EndpointsPerApp, limits.EndpointsPerAccount)
	if err != nil {
		var quotaErr *state.ManagedRealtimeEndpointQuotaError
		if errors.As(err, &quotaErr) {
			api.WriteProblem(w, api.ErrPlanRealtimeQuota(acct.Plan, string(quotaErr.Scope), quotaErr.Limit, quotaErr.Observed))
			return
		}
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.ErrRealtimeInvalid("managed realtime endpoint already exists"))
			return
		}
		s.log.WarnContext(r.Context(), "create managed realtime endpoint", "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not create managed realtime endpoint"))
		return
	}
	if err := s.syncManagedRealtimeEndpoint(r.Context(), row); err != nil {
		s.log.WarnContext(r.Context(), "sync managed realtime endpoint", "endpoint_id", row.ID, "err", err)
	}
	s.audit.Emit(r.Context(), "realtime.endpoint_created", &acct.ID, map[string]any{"endpoint_id": row.ID, "app_id": app.ID})
	if realtimeAuthMode(row) != api.RealtimeAuthModeNone {
		s.audit.Emit(r.Context(), "realtime.auth_policy_configured", &acct.ID, realtimeAuthPolicyAuditData(row))
	}
	writeJSON(w, http.StatusCreated, realtimeEndpointResponse(row))
}

func (s *server) getManagedRealtimeEndpoint(w http.ResponseWriter, r *http.Request, acct state.Account) {
	row, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, realtimeEndpointResponse(row))
}

func (s *server) updateManagedRealtimeEndpoint(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.UpdateManagedRealtimeEndpointRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid(err.Error()))
		return
	}
	if _, ok := s.realtimePlanLimits(acct); !ok {
		api.WriteProblem(w, api.ErrPlanRealtimeNotAllowed(acct.Plan))
		return
	}
	store, ok := realtimeEndpointStore(s)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("managed realtime endpoint store unavailable"))
		return
	}
	existing, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	params := state.UpdateManagedRealtimeEndpointParams{}
	authMode := existing.AuthMode
	if authMode == "" {
		if len(existing.AuthTokenSealed) > 0 {
			authMode = api.RealtimeAuthModeStaticBearer
		} else {
			authMode = api.RealtimeAuthModeNone
		}
	}
	authIssuer := existing.AuthIssuer
	authJWKSURL := existing.AuthJWKSURL
	authAudience := append([]string(nil), existing.AuthAudience...)
	authAlgorithms := append([]string(nil), existing.AuthAlgorithms...)
	authRequiredClaims := cloneRealtimeClaims(existing.AuthRequiredClaims)
	authTokenConfigured := len(existing.AuthTokenSealed) > 0
	if req.CallbackURL != nil {
		if prob := validateRealtimeCallbackURL(*req.CallbackURL); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		if prob := resolveAndCheckEgress(r.Context(), *req.CallbackURL); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		params.CallbackURL = req.CallbackURL
	}
	if req.ConnectPath != nil {
		path, prob := normalizeRealtimePath(*req.ConnectPath, "connect_path", api.DefaultRealtimeConnectPath)
		if prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		params.ConnectPath = &path
	}
	if req.MessagePath != nil {
		path, prob := normalizeRealtimePath(*req.MessagePath, "message_path", api.DefaultRealtimeMessagePath)
		if prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		params.MessagePath = &path
	}
	if req.DisconnectPath != nil {
		path, prob := normalizeRealtimePath(*req.DisconnectPath, "disconnect_path", api.DefaultRealtimeDisconnectPath)
		if prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		params.DisconnectPath = &path
	}
	if req.CallbackAuthToken != nil {
		if prob := validateRealtimeToken(*req.CallbackAuthToken, "callback_auth_token", true); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		sealed, prob := sealRealtimeToken(*req.CallbackAuthToken, "REALTIME_CALLBACK_AUTH")
		if prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		params.CallbackAuthTokenSealed = &sealed
	}
	if req.AuthToken != nil {
		if prob := validateRealtimeToken(*req.AuthToken, "auth_token", false); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		sealed, prob := sealRealtimeToken(*req.AuthToken, "REALTIME_AUTH")
		if prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		params.AuthTokenSealed = &sealed
		authTokenConfigured = *req.AuthToken != ""
		empty := []byte{}
		params.AuthTokenPreviousSealed = &empty
		params.ClearAuthTokenPreviousExpiresAt = true
	}
	if req.AuthMode != nil {
		mode, err := api.NormalizeRealtimeAuthMode(*req.AuthMode, authTokenConfigured)
		if err != nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid(err.Error()))
			return
		}
		authMode = mode
	}
	if req.AuthIssuer != nil {
		authIssuer = *req.AuthIssuer
	}
	if req.AuthJWKSURL != nil {
		authJWKSURL = *req.AuthJWKSURL
	}
	if req.AuthAudience != nil {
		authAudience = append([]string(nil), (*req.AuthAudience)...)
	}
	if req.AuthAlgorithms != nil {
		authAlgorithms = append([]string(nil), (*req.AuthAlgorithms)...)
	}
	if req.AuthRequiredClaims != nil {
		authRequiredClaims = cloneRealtimeClaims(*req.AuthRequiredClaims)
	}
	// Switching away from static bearer clears the old sealed token, and
	// switching away from JWT clears its public trust metadata. This avoids
	// stale credentials surviving an apparently unrelated mode change.
	if req.AuthMode != nil && authMode != api.RealtimeAuthModeStaticBearer && req.AuthToken == nil {
		empty := []byte{}
		params.AuthTokenSealed = &empty
		params.AuthTokenPreviousSealed = &empty
		params.ClearAuthTokenPreviousExpiresAt = true
		authTokenConfigured = false
	}
	if req.AuthMode != nil && authMode != api.RealtimeAuthModeOIDCJWT {
		authIssuer, authJWKSURL = "", ""
		authAudience, authAlgorithms, authRequiredClaims = nil, nil, nil
	}
	if req.AuthMode != nil && authMode == api.RealtimeAuthModeStaticBearer {
		authIssuer, authJWKSURL = "", ""
		authAudience, authAlgorithms, authRequiredClaims = nil, nil, nil
	}
	if err := api.ValidateRealtimeAuth(authMode, authTokenConfigured, authIssuer, authJWKSURL, authAudience, authAlgorithms, authRequiredClaims); err != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid(err.Error()))
		return
	}
	params.AuthMode = &authMode
	params.AuthIssuer = &authIssuer
	params.AuthJWKSURL = &authJWKSURL
	params.AuthAudience = &authAudience
	params.AuthAlgorithms = &authAlgorithms
	params.AuthRequiredClaims = &authRequiredClaims
	if req.AllowedOrigins != nil {
		if prob := validateRealtimePolicy(*req.AllowedOrigins, 0, 0, 0); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		origins := append([]string(nil), (*req.AllowedOrigins)...)
		params.AllowedOrigins = &origins
	}
	if req.MaxConnections != nil {
		if prob := validateRealtimePolicy(nil, *req.MaxConnections, 0, 0); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		params.MaxConnections = req.MaxConnections
	}
	if req.MaxMessageBytes != nil {
		if prob := validateRealtimePolicy(nil, 0, *req.MaxMessageBytes, 0); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		params.MaxMessageBytes = req.MaxMessageBytes
	}
	if req.MaxConnectionAgeSeconds != nil {
		if prob := validateRealtimePolicy(nil, 0, 0, *req.MaxConnectionAgeSeconds); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		params.MaxConnectionAgeSeconds = req.MaxConnectionAgeSeconds
	}
	params.Enabled = req.Enabled
	row, err := store.UpdateManagedRealtimeEndpoint(r.Context(), existing.ID, params)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "managed realtime endpoint not found")
			return
		}
		s.log.WarnContext(r.Context(), "update managed realtime endpoint", "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not update managed realtime endpoint"))
		return
	}
	if err := s.syncManagedRealtimeEndpoint(r.Context(), row); err != nil {
		s.log.WarnContext(r.Context(), "sync managed realtime endpoint", "endpoint_id", row.ID, "err", err)
	}
	s.audit.Emit(r.Context(), "realtime.endpoint_updated", &acct.ID, map[string]any{"endpoint_id": row.ID, "app_id": row.AppID})
	if realtimeAuthPolicyChanged(existing, row) {
		data := realtimeAuthPolicyAuditData(row)
		data["previous_auth_mode"] = realtimeAuthMode(existing)
		data["auth_token_changed"] = !bytes.Equal(existing.AuthTokenSealed, row.AuthTokenSealed)
		s.audit.Emit(r.Context(), "realtime.auth_policy_updated", &acct.ID, data)
	}
	writeJSON(w, http.StatusOK, realtimeEndpointResponse(row))
}

func (s *server) rotateManagedRealtimeAuth(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.RotateManagedRealtimeAuthRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid(err.Error()))
		return
	}
	row, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	if realtimeAuthMode(row) != api.RealtimeAuthModeStaticBearer || len(row.AuthTokenSealed) == 0 {
		api.WriteProblem(w, api.ErrRealtimeInvalid("auth token rotation requires static_bearer authentication"))
		return
	}
	if prob := validateRealtimeToken(req.NewAuthToken, "new_auth_token", true); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	grace := api.RealtimeAuthRotationDefaultGraceSeconds
	if req.GracePeriodSeconds != nil {
		grace = *req.GracePeriodSeconds
	}
	if grace < 0 || grace > api.RealtimeAuthRotationMaxGraceSeconds {
		api.WriteProblem(w, api.ErrRealtimeInvalid(fmt.Sprintf("grace_period_seconds must be between 0 and %d", api.RealtimeAuthRotationMaxGraceSeconds)))
		return
	}
	sealed, prob := sealRealtimeToken(req.NewAuthToken, "REALTIME_AUTH")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	deadline := time.Now().UTC().Add(time.Duration(grace) * time.Second)
	previous := append([]byte(nil), row.AuthTokenSealed...)
	previousExpiry := &deadline
	params := state.UpdateManagedRealtimeEndpointParams{
		AuthTokenSealed:            &sealed,
		AuthTokenPreviousSealed:    &previous,
		AuthTokenPreviousExpiresAt: previousExpiry,
	}
	authMode := api.RealtimeAuthModeStaticBearer
	params.AuthMode = &authMode
	store, ok := realtimeEndpointStore(s)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("managed realtime endpoint store unavailable"))
		return
	}
	updatedRow, err := store.UpdateManagedRealtimeEndpoint(r.Context(), row.ID, params)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "managed realtime endpoint not found")
			return
		}
		s.log.WarnContext(r.Context(), "rotate managed realtime auth token", "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not rotate managed realtime auth token"))
		return
	}
	if err := s.syncManagedRealtimeEndpoint(r.Context(), updatedRow); err != nil {
		s.log.WarnContext(r.Context(), "sync rotated managed realtime endpoint", "endpoint_id", updatedRow.ID, "err", err)
	}
	data := realtimeAuthPolicyAuditData(updatedRow)
	data["grace_period_seconds"] = grace
	data["previous_token_expires_at"] = deadline.Format(time.RFC3339)
	s.audit.Emit(r.Context(), "realtime.auth_token_rotated", &acct.ID, data)
	expiresAt := deadline.Format(time.RFC3339)
	writeJSON(w, http.StatusOK, api.RotateManagedRealtimeAuthResponse{
		EndpointID: updatedRow.ID, AuthMode: realtimeAuthMode(updatedRow),
		PreviousTokenExpiresAt: &expiresAt,
	})
}

func (s *server) finalizeManagedRealtimeAuth(w http.ResponseWriter, r *http.Request, acct state.Account) {
	row, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	if realtimeAuthMode(row) != api.RealtimeAuthModeStaticBearer || len(row.AuthTokenSealed) == 0 {
		api.WriteProblem(w, api.ErrRealtimeInvalid("auth token rotation requires static_bearer authentication"))
		return
	}
	wasPending := len(row.AuthTokenPreviousSealed) > 0
	empty := []byte{}
	params := state.UpdateManagedRealtimeEndpointParams{
		AuthTokenPreviousSealed:         &empty,
		ClearAuthTokenPreviousExpiresAt: true,
	}
	store, ok := realtimeEndpointStore(s)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("managed realtime endpoint store unavailable"))
		return
	}
	updatedRow, err := store.UpdateManagedRealtimeEndpoint(r.Context(), row.ID, params)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "managed realtime endpoint not found")
			return
		}
		s.log.WarnContext(r.Context(), "finalize managed realtime auth token rotation", "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not finalize managed realtime auth token rotation"))
		return
	}
	if err := s.syncManagedRealtimeEndpoint(r.Context(), updatedRow); err != nil {
		s.log.WarnContext(r.Context(), "sync finalized managed realtime endpoint", "endpoint_id", updatedRow.ID, "err", err)
	}
	if wasPending {
		s.audit.Emit(r.Context(), "realtime.auth_token_rotation_finalized", &acct.ID, realtimeAuthPolicyAuditData(updatedRow))
	}
	writeJSON(w, http.StatusOK, api.FinalizeManagedRealtimeAuthResponse{
		EndpointID: updatedRow.ID, AuthMode: realtimeAuthMode(updatedRow),
	})
}

func (s *server) deleteManagedRealtimeEndpoint(w http.ResponseWriter, r *http.Request, acct state.Account) {
	row, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	store, ok := realtimeEndpointStore(s)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("managed realtime endpoint store unavailable"))
		return
	}
	if err := store.DeleteManagedRealtimeEndpoint(r.Context(), row.ID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "managed realtime endpoint not found")
			return
		}
		s.log.WarnContext(r.Context(), "delete managed realtime endpoint", "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not delete managed realtime endpoint"))
		return
	}
	if s.realtimeRegistrar != nil {
		if err := s.realtimeRegistrar.RemoveEndpoint(r.Context(), row.ID); err != nil {
			s.log.WarnContext(r.Context(), "remove managed realtime endpoint from owner", "endpoint_id", row.ID, "err", err)
		}
	}
	s.audit.Emit(r.Context(), "realtime.endpoint_deleted", &acct.ID, map[string]any{"endpoint_id": row.ID, "app_id": row.AppID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) loadManagedRealtimeEndpoint(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ManagedRealtimeEndpoint, state.App, bool) {
	if _, ok := s.realtimePlanLimits(acct); !ok {
		api.WriteProblem(w, api.ErrPlanRealtimeNotAllowed(acct.Plan))
		return state.ManagedRealtimeEndpoint{}, state.App{}, false
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return state.ManagedRealtimeEndpoint{}, state.App{}, false
	}
	store, ok := realtimeEndpointStore(s)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("managed realtime endpoint store unavailable"))
		return state.ManagedRealtimeEndpoint{}, state.App{}, false
	}
	row, err := store.ManagedRealtimeEndpointByID(r.Context(), r.PathValue("id"))
	if err != nil || row.AppID != app.ID || row.AccountID != acct.ID {
		s.notFound(w, "managed realtime endpoint not found")
		return state.ManagedRealtimeEndpoint{}, state.App{}, false
	}
	return row, app, true
}
