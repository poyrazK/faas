package apidserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/durable"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	entitysocket "github.com/onebox-faas/faas/pkg/durableentity/socket"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) sessionGrantAvailable(w http.ResponseWriter) bool {
	if s.durable.SessionGrants == nil || s.durable.Sockets == nil {
		api.WriteProblem(w, api.ErrCapacity("browser session preview is disabled"))
		return false
	}
	return true
}
func (s *server) mintEntitySessionGrant(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.sessionGrantAvailable(w) {
		return
	}
	_, key, ok := middleware.AccountFromContext(r)
	if !ok || key == nil || key.ID == "" || len(key.Hash) == 0 || key.PlatformTenantID != "" || key.AppID != "" {
		durable.WriteGrantProblem(w, entitysocket.ErrGrantDenied)
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if !s.durable.Apps[app.ID] || !app.WebSocketEnabled || !acct.Plan.WebSocketResponseAllowed() || !app.AcceptsRequestInvocations() || !api.MustLimitsFor(acct.Plan).AsyncInvokeAllowed {
		durable.WriteGrantProblem(w, entitysocket.ErrGrantDenied)
		return
	}
	var input struct {
		Namespace        string   `json:"namespace"`
		Key              string   `json:"key"`
		Environment      string   `json:"env"`
		PlatformTenantID string   `json:"platform_tenant_id"`
		SessionID        string   `json:"session_id"`
		Generation       uint64   `json:"generation"`
		Origin           string   `json:"origin"`
		Commands         []string `json:"commands"`
		TTLSeconds       int      `json:"ttl_seconds"`
	}
	if !decodeJSONLimit(w, r, &input, 8192) {
		return
	}
	if input.TTLSeconds < 1 || input.TTLSeconds > 300 {
		durable.WriteGrantProblem(w, entitysocket.ErrGrantDenied)
		return
	}
	id, _, problem := s.durable.Identity(r, acct, app, api.DurableEntityInvokeRequest{Namespace: input.Namespace, Key: input.Key, Environment: input.Environment, PlatformTenantID: input.PlatformTenantID})
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	grant, token, err := s.durable.SessionGrants.Mint(r.Context(), entitysocket.Grant{Entity: id, Environment: input.Environment, PlatformTenantID: input.PlatformTenantID, IssuerID: key.ID, SessionID: input.SessionID, Generation: input.Generation, Origin: input.Origin, Commands: input.Commands}, time.Duration(input.TTLSeconds)*time.Second)
	if err != nil {
		durable.WriteGrantProblem(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"grant_id": grant.ID, "credential": token, "expires_at": grant.ExpiresAt, "connect_path": "/v1/apps/" + app.Slug + "/entities/session-connect", "protocol": entitysocket.BrowserProtocol})
}
func (s *server) revokeEntitySessionGrant(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.sessionGrantAvailable(w) {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if err := s.durable.SessionGrants.Revoke(r.Context(), r.PathValue("grant"), acct.ID, app.ID); err != nil {
		durable.WriteGrantProblem(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Delegation authenticates the persisted issuer afresh without keeping its
// plaintext credential in a grant or requiring it from the browser.
func (s *server) grantPrincipal(r *http.Request, g entitysocket.Grant) (*http.Request, state.Account, error) {
	key, err := s.store.GetAPIKey(r.Context(), g.Entity.AccountID, g.IssuerID)
	if err != nil {
		return nil, state.Account{}, entitysocket.ErrGrantDenied
	}
	acct, fresh, err := s.authMw.Authn.AuthenticateKey(r.Context(), key.Hash)
	if err != nil || acct.ID != g.Entity.AccountID || fresh.ID != g.IssuerID || !acct.Active() {
		return nil, state.Account{}, entitysocket.ErrGrantDenied
	}
	return r.Clone(middleware.WithPrincipal(r.Context(), acct, &fresh, nil)), acct, nil
}
func (s *server) connectEntityBrowserSession(w http.ResponseWriter, r *http.Request) {
	if !s.sessionGrantAvailable(w) {
		return
	}
	if r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" || len(r.Header.Values("Origin")) != 1 || !entitysocket.ValidOrigin(r.Header.Get("Origin")) {
		durable.WriteGrantProblem(w, entitysocket.ErrGrantDenied)
		return
	}
	protocols := websocket.Subprotocols(r)
	if len(protocols) != 2 || protocols[0] != entitysocket.BrowserProtocol || !strings.HasPrefix(protocols[1], entitysocket.CredentialProtocol) {
		durable.WriteGrantProblem(w, entitysocket.ErrGrantDenied)
		return
	}
	token := strings.TrimPrefix(protocols[1], entitysocket.CredentialProtocol)
	grant, err := s.durable.SessionGrants.Authenticate(r.Context(), token, r.Header.Get("Origin"))
	if err != nil {
		durable.WriteGrantProblem(w, err)
		return
	}
	authenticated, acct, err := s.grantPrincipal(r, grant)
	if err != nil {
		durable.WriteGrantProblem(w, err)
		return
	}
	// Use the same scope and application admission at handshake and each frame.
	admit := s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
		if !ok {
			return
		}
		if app.ID != grant.Entity.AppID || !s.durable.Apps[app.ID] || !app.WebSocketEnabled || !acct.Plan.WebSocketResponseAllowed() || !app.AcceptsRequestInvocations() || !api.MustLimitsFor(acct.Plan).AsyncInvokeAllowed {
			durable.WriteGrantProblem(w, entitysocket.ErrGrantDenied)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	check := &entitySocketResponse{header: make(http.Header)}
	admit(check, authenticated, acct)
	if check.status != http.StatusNoContent {
		w.WriteHeader(check.status)
		_, _ = w.Write(check.body.Bytes())
		return
	}
	lease, release, ok := s.reserveEntityTenantConnection(w, r, grant.Entity)
	if !ok {
		return
	}
	defer release()
	identity, _ := json.Marshal(grant.Entity)
	//nolint:contextcheck // Each frame inherits r.Context() through Authenticate and grantPrincipal; the request lives for the WebSocket lifetime.
	s.durable.Sockets.ServeBrowser(w, r, acct.ID, string(identity), grant.Origin, func(frame entitysocket.Frame) entitysocket.Reply {
		current, err := s.durable.SessionGrants.Authenticate(r.Context(), token, r.Header.Get("Origin"))
		if err != nil {
			if errors.Is(err, entitysocket.ErrGrantDenied) {
				return entitysocket.Reply{Status: 403}
			}
			return entitysocket.Reply{Status: 503}
		}
		if !current.Authorize(frame) {
			return entitysocket.Reply{Status: 403}
		}
		sub, account, err := s.grantPrincipal(r, current)
		if err != nil {
			return entitysocket.Reply{Status: 403}
		}
		gate := &entitySocketResponse{header: make(http.Header)}
		admit(gate, sub, account)
		if gate.status != 204 {
			return entitysocket.Reply{Status: gate.status}
		}
		if s.durable.TenantQuotas != nil {
			if err := s.durable.TenantQuotas.CheckConnection(r.Context(), grant.Entity, lease); err != nil {
				if errors.Is(err, entitysocket.ErrGrantDenied) {
					return entitysocket.Reply{Status: 403}
				}
				return entitysocket.Reply{Status: 503}
			}
		}
		body, _ := json.Marshal(api.DurableEntityInvokeRequest{Namespace: current.Entity.Namespace, Key: current.Entity.Key, Environment: current.Environment, PlatformTenantID: current.PlatformTenantID, RequestID: frame.RequestID, Payload: frame.Payload})
		sub = sub.Clone(context.WithValue(sub.Context(), durable.SocketScopeFence{}, grant.Entity))
		sub.Method = http.MethodPost
		sub.Body = io.NopCloser(bytes.NewReader(body))
		sub.ContentLength = int64(len(body))
		sub.Header = make(http.Header)
		sub.Header.Set("Content-Type", "application/json")
		sub.URL.RawQuery = ""
		sub.URL.Path = "/v1/apps/" + r.PathValue("slug") + "/entities/invoke"
		sub.SetPathValue("slug", r.PathValue("slug"))
		reply := &entitySocketResponse{header: make(http.Header)}
		s.durable.Invoke(reply, sub, account)
		if reply.failed {
			return entitysocket.Reply{Status: 503}
		}
		result := entitysocket.Reply{Status: reply.status}
		if result.Status == 0 {
			result.Status = 200
		}
		if result.Status == 200 {
			var committed api.DurableEntityInvokeResponse
			if json.Unmarshal(reply.body.Bytes(), &committed) != nil {
				return entitysocket.Reply{Status: 503}
			}
			result.Result = &committed
		} else if json.Valid(reply.body.Bytes()) {
			result.Problem = reply.body.Bytes()
		}
		return result
	})
}
