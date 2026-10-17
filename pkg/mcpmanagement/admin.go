package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/safetext"
)

// ConnectionSummary is an explicit, bounded projection of a bridge grant.
// Credentials, token hashes, pending browser codes and native keys are excluded.
type ConnectionSummary struct {
	RequestReadApps               []string  `json:"request_read_apps"`
	RequestReadAppCount           int       `json:"request_read_app_count"`
	RequestReadAppsTruncated      bool      `json:"request_read_apps_truncated"`
	GrantID                       string    `json:"grant_id"`
	ClientID                      string    `json:"client_id"`
	AccountID                     string    `json:"account_id"`
	Scopes                        []string  `json:"scopes"`
	ExpiresAt                     time.Time `json:"expires_at"`
	CredentialExpiresAt           time.Time `json:"credential_expires_at"`
	AppAccess                     string    `json:"app_access"`
	Apps                          []string  `json:"apps"`
	AppCount                      int       `json:"selected_app_count"`
	AppsTruncated                 bool      `json:"apps_truncated"`
	Environments                  []string  `json:"environments"`
	EnvironmentCount              int       `json:"named_environment_count"`
	EnvironmentsTruncated         bool      `json:"environments_truncated"`
	DefaultWrite                  bool      `json:"default_write"`
	WritableEnvironments          []string  `json:"writable_environments"`
	WritableEnvironmentsTruncated bool      `json:"writable_environments_truncated"`
	WritableEnvironmentCount      int       `json:"writable_environment_count"`
}

// AdminHandler is for an owner-protected local socket, never the public MCP mux.
func (s *RemoteService) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /connections", s.remote.adminConnections)
	mux.HandleFunc("DELETE /connections/{grant}", s.remote.adminRevoke)
	return s.remote.opts.Lifecycle.wrapHTTP(mux)
}

func (r *remoteServer) adminConnections(w http.ResponseWriter, req *http.Request) {
	limit, offset, err := adminPage(req)
	if err != nil {
		http.Error(w, "Invalid connection page", http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.opts.StateStore.available() {
		http.Error(w, "Authorization storage unavailable", http.StatusServiceUnavailable)
		return
	}
	ids := []string{}
	for id, g := range r.grants {
		if g.server != nil && !g.revoked && time.Now().Before(g.expires) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	start := min(offset, len(ids))
	end := start + min(limit, len(ids)-start)
	items := []ConnectionSummary{}
	for _, id := range ids[start:end] {
		items = append(items, connectionSummary(r.grants[id]))
	}
	out := map[string]any{"items": items, "total": len(ids)}
	if end < len(ids) {
		out["next_offset"] = end
	}
	writeAdminJSON(w, out)
}
func adminPage(req *http.Request) (int, int, error) {
	limit, offset := defaultPageSize, 0
	query := req.URL.Query()
	for key := range query {
		if key != "limit" && key != "offset" {
			return 0, 0, invalid("invalid_argument", "unknown page argument")
		}
		if len(query[key]) != 1 {
			return 0, 0, invalid("invalid_argument", "duplicate page argument")
		}
	}
	var err error
	if query.Has("limit") {
		limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil {
			return 0, 0, err
		}
	}
	if query.Has("offset") {
		offset, err = strconv.Atoi(query.Get("offset"))
		if err != nil {
			return 0, 0, err
		}
	}
	if limit < 1 || limit > maxPageSize || offset < 0 {
		return 0, 0, invalid("invalid_argument", "invalid page bound")
	}
	return limit, offset, nil
}
func connectionSummary(g *oauthGrant) ConnectionSummary {
	out := ConnectionSummary{GrantID: g.id, ClientID: g.clientID, AccountID: safetext.Truncate(safetext.Clean(g.accountID), 128), Scopes: append([]string(nil), g.scopes...), ExpiresAt: g.expires, CredentialExpiresAt: g.credentialExpires, AppAccess: "all", Apps: []string{}, Environments: []string{}, WritableEnvironments: []string{}}
	if g.allowedApps != nil {
		out.AppAccess = "selected"
		for slug := range g.allowedApps {
			out.Apps = append(out.Apps, slug)
		}
		sort.Strings(out.Apps)
		out.AppCount = len(out.Apps)
		out.AppsTruncated = len(out.Apps) > defaultPageSize
		out.Apps = out.Apps[:min(len(out.Apps), defaultPageSize)]
	}
	if g.environments != nil {
		out.RequestReadApps = []string{}
		for slug := range g.environments.RequestReadApps {
			out.RequestReadApps = append(out.RequestReadApps, slug)
		}
		sort.Strings(out.RequestReadApps)
		out.RequestReadAppCount = len(out.RequestReadApps)
		out.RequestReadAppsTruncated = len(out.RequestReadApps) > defaultPageSize
		out.RequestReadApps = out.RequestReadApps[:min(len(out.RequestReadApps), defaultPageSize)]
		out.DefaultWrite = g.environments.DefaultWrite
		for key, e := range g.environments.Named {
			out.Environments = append(out.Environments, key)
			if e.Write {
				out.WritableEnvironmentCount++
				out.WritableEnvironments = append(out.WritableEnvironments, key)
			}
		}
		sort.Strings(out.Environments)
		sort.Strings(out.WritableEnvironments)
		out.WritableEnvironmentsTruncated = len(out.WritableEnvironments) > defaultPageSize
		out.WritableEnvironments = out.WritableEnvironments[:min(len(out.WritableEnvironments), defaultPageSize)]
		out.EnvironmentCount = len(out.Environments)
		out.EnvironmentsTruncated = len(out.Environments) > defaultPageSize
		out.Environments = out.Environments[:min(len(out.Environments), defaultPageSize)]
	}
	return out
}
func (r *remoteServer) adminRevoke(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("grant")
	if validateSegment(id, "grant_id") != nil || req.URL.RawQuery != "" {
		http.Error(w, "Invalid grant ID", http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.opts.StateStore.available() {
		http.Error(w, "Authorization storage unavailable", http.StatusServiceUnavailable)
		return
	}
	g := r.grants[id]
	if g == nil || g.server == nil || g.revoked {
		http.Error(w, "Active grant not found", http.StatusNotFound)
		return
	}
	r.revokeLocked(g)
	if err := r.opts.StateStore.update("oauth", r.snapshotLocked()); err != nil {
		http.Error(w, "Revocation could not be committed; bridge storage is unavailable", http.StatusServiceUnavailable)
		return
	}
	writeAdminJSON(w, map[string]any{"grant_id": id, "revoked": true, "durable": r.opts.StateStore != nil})
}
func writeAdminJSON(w http.ResponseWriter, value any) {
	setPrivateHeaders(w)
	data, err := json.Marshal(value)
	if err != nil || len(data) > MaxFrameBytes {
		http.Error(w, "Connection metadata exceeds its bound; request a smaller page", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
