package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

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
	out := api.ManagedRealtimeEndpointResponseFromRow(e.ID, e.AppID, e.AccountID, e.CallbackURL,
		e.ConnectPath, e.MessagePath, e.DisconnectPath, e.Enabled, e.CreatedAt, e.UpdatedAt)
	if len(e.AuthTokenSealed) == 0 {
		out.AuthTokenMasked = ""
	}
	return out
}

// syncManagedRealtimeEndpoint mirrors durable endpoint state onto the local
// realtime owner when one is configured. Cross-node deployments leave the
// registrar unset until the dispatch/lease control plane is enabled.
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
	return s.realtimeRegistrar.RegisterEndpoint(ctx, realtime.Endpoint{
		ID: row.ID, AppID: row.AppID, AccountID: row.AccountID,
		CallbackURL: row.CallbackURL, ConnectPath: row.ConnectPath,
		MessagePath: row.MessagePath, DisconnectPath: row.DisconnectPath,
		CallbackAuthToken: string(callbackAuth), AuthToken: string(authToken),
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
	authSealed, prob := sealRealtimeToken(req.AuthToken, "REALTIME_AUTH")
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
		CallbackAuthTokenSealed: callbackSealed, AuthTokenSealed: authSealed, Enabled: enabled,
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
	writeJSON(w, http.StatusOK, realtimeEndpointResponse(row))
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
