package mcpmanagement

import (
	"bytes"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type storedGrant struct {
	ID                    string
	ClientID              string
	AccountID             string
	Scopes                []string
	Key                   string
	Expires               time.Time
	CredentialExpires     time.Time
	AccessExpires         time.Time
	AccessHash            string
	RefreshHash           string
	Active                bool
	AllowedApps           map[string]string
	AvailableApps         []api.AppResponse
	Environments          *EnvironmentConsent
	AvailableEnvironments []EnvironmentGrant
}

type oauthSnapshot struct {
	Format      string
	APIBase     string
	ResourceURL string
	Clients     map[string]*oauthClient
	Requests    map[string]*authorizationRequest
	Codes       map[string]*authorizationCode
	Grants      map[string]storedGrant
	UsedRefresh map[string]string
}

func (r *remoteServer) snapshotLocked() oauthSnapshot {
	snapshot := oauthSnapshot{Format: stateFormat, APIBase: r.opts.APIBase, ResourceURL: r.opts.ResourceURL, Clients: r.clients, Requests: r.requests, Codes: r.codes, Grants: map[string]storedGrant{}, UsedRefresh: map[string]string{}}
	for id, g := range r.grants {
		snapshot.Grants[id] = storedGrant{ID: id, ClientID: g.clientID, AccountID: g.accountID, Scopes: g.scopes, Key: g.client.Token(), Expires: g.expires, CredentialExpires: g.credentialExpires, AccessExpires: g.accessExpires, AccessHash: g.accessHash, RefreshHash: g.refreshHash, Active: g.server != nil, AllowedApps: g.allowedApps, AvailableApps: g.availableApps, Environments: g.environments, AvailableEnvironments: g.availableEnvironments}
	}
	for hash, grant := range r.usedRefresh {
		snapshot.UsedRefresh[hash] = grant.id
	}
	return snapshot
}

func (r *remoteServer) restoreOAuth() error {
	if r.opts.StateStore == nil {
		return nil
	}
	var snapshot oauthSnapshot
	if err := r.opts.StateStore.read("oauth", &snapshot); err != nil {
		return err
	}
	if snapshot.Format == "" {
		return nil
	}
	if snapshot.Format != stateFormat || snapshot.APIBase != r.opts.APIBase || snapshot.ResourceURL != r.opts.ResourceURL {
		return errors.New("stored OAuth endpoint binding mismatch")
	}
	if len(snapshot.Clients) > maxOAuthEntries || len(snapshot.Requests) > maxOAuthEntries || len(snapshot.Codes) > maxOAuthEntries || len(snapshot.Grants) > maxOAuthEntries || len(snapshot.UsedRefresh) > maxRefreshHistory {
		return errors.New("stored OAuth capacity is invalid")
	}
	for id, client := range snapshot.Clients {
		if client.ID != id {
			return errors.New("stored client identity mismatch")
		}
		allowed := true
		for _, redirect := range client.Redirects {
			allowed = allowed && r.redirectAllowed(redirect)
		}
		if allowed && time.Now().Before(client.Expires) {
			r.clients[id] = client
		}
	}
	for id, record := range snapshot.Grants {
		if _, allowed := r.requestedScopes(strings.Join(record.Scopes, " ")); !allowed {
			continue
		}
		if record.ID != id || record.Key == "" || record.AccountID == "" {
			return errors.New("stored grant identity mismatch")
		}
		// Operator policy narrowing invalidates existing write grants rather
		// than silently resurrecting a consent or widening another grant.
		if r.clients[record.ClientID] == nil || !time.Now().Before(record.Expires) || (slices.Contains(record.Scopes, scopeWrite) && !r.opts.AllowWrites) {
			continue
		}
		client := r.apiClient(record.Key)
		g := &oauthGrant{id: id, clientID: record.ClientID, accountID: record.AccountID, scopes: record.Scopes, client: client, expires: record.Expires, credentialExpires: record.CredentialExpires, accessExpires: record.AccessExpires, accessHash: record.AccessHash, refreshHash: record.RefreshHash, allowedApps: record.AllowedApps, availableApps: record.AvailableApps}
		g.environments, g.availableEnvironments = record.Environments, record.AvailableEnvironments
		// Journals written before environment consent had default release access;
		// restoring them must never grant named environments implicitly.
		if g.environments == nil {
			g.environments = &EnvironmentConsent{DefaultWrite: slices.Contains(g.scopes, scopeWrite), Named: map[string]EnvironmentGrant{}}
		}
		if record.Active {
			var err error
			g.server, g.management, err = newManagementServer(client, Options{Lifecycle: r.opts.Lifecycle, Telemetry: r.opts.Telemetry, auditLogger: r.opts.AuditLogger, accountID: g.accountID, grantID: g.id, Profile: "oauth", APIBase: r.opts.APIBase, Version: r.opts.Version, Timeout: r.opts.Timeout, AllowWrites: r.opts.AllowWrites && slices.Contains(g.scopes, scopeWrite), AllowedApps: g.allowedApps, Environments: g.environments, planStore: r.opts.StateStore, planNamespace: id, Toolsets: r.opts.Toolsets})
			if err != nil {
				return err
			}
		}
		r.grants[id] = g
		if g.accessHash != "" {
			r.access[g.accessHash] = g
		}
		if g.refreshHash != "" {
			r.refresh[g.refreshHash] = g
		}
	}
	for id, flow := range snapshot.Requests {
		if _, allowed := r.requestedScopes(strings.Join(flow.Scopes, " ")); !allowed {
			continue
		}
		if time.Now().Before(flow.Expires) && r.clients[flow.ClientID] != nil && (flow.GrantID == "" || r.grants[flow.GrantID] != nil) {
			r.requests[id] = flow
		}
	}
	for hash, code := range snapshot.Codes {
		if time.Now().Before(code.Expires) && r.grants[code.GrantID] != nil {
			r.codes[hash] = code
		}
	}
	for hash, id := range snapshot.UsedRefresh {
		if g := r.grants[id]; g != nil {
			r.usedRefresh[hash] = g
		}
	}
	r.pruneLocked()
	return r.opts.StateStore.update("oauth", r.snapshotLocked())
}

// OAuth replies are withheld until the resulting state is fsync'd. A failed
// commit disables all further access; no token/consent success can escape.
// MCP replies are bounded JSON. Logs remain bounded tool output, not streaming.
func (r *remoteServer) durableHandler(next http.Handler) http.Handler {
	if r.opts.StateStore == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !r.opts.StateStore.available() {
			oauthError(w, 503, "temporarily_unavailable", "Authorization storage unavailable")
			return
		}
		reply := &bufferedResponse{header: make(http.Header)}
		next.ServeHTTP(reply, req)
		r.mu.Lock()
		err := r.opts.StateStore.update("oauth", r.snapshotLocked())
		r.mu.Unlock()
		if err != nil {
			setPrivateHeaders(w)
			oauthError(w, 503, "temporarily_unavailable", "Authorization storage could not be committed")
			return
		}
		if reply.failed {
			setPrivateHeaders(w)
			oauthError(w, 503, "temporarily_unavailable", "Management response exceeded its bound; retry writes with the same plan")
			return
		}
		for key, values := range reply.header {
			w.Header()[key] = values
		}
		status := reply.status
		if status == 0 {
			status = 200
		}
		w.WriteHeader(status)
		_, _ = w.Write(reply.body.Bytes())
	})
}

type bufferedResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
	failed bool
}

func (b *bufferedResponse) Header() http.Header { return b.header }
func (b *bufferedResponse) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}
func (b *bufferedResponse) Write(data []byte) (int, error) {
	if b.status == 0 {
		b.status = 200
	}
	if b.body.Len()+len(data) > 4*maxResultBytes+MaxFrameBytes {
		b.failed = true
		return 0, errors.New("MCP HTTP response too large")
	}
	return b.body.Write(data)
}
