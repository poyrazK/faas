package toolregistry

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolconnection"
	"github.com/onebox-faas/faas/pkg/toolgrant"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// Connections accepts owner intent only. A monitoring grant never authorizes
// execution; neither agent keys nor scoped OAuth principals may create one.
func Connections(store state.Store, audit GrantAudit) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		w.Header().Set("Cache-Control", "no-store")
		if !connectionOwnerAccess(w, r, acct, store) {
			return
		}
		connections, ok := store.(state.ToolOAuthConnectionStore)
		if !ok {
			connectionRespond(w, nil, errors.New("connection store unavailable"))
			return
		}
		id := r.PathValue("id")
		if id == "" && r.Method == http.MethodGet {
			listConnections(w, r, store, connections, acct)
			return
		}
		if !toolgrant.ValidID(id) {
			connectionRespond(w, nil, state.ErrInvalidArgument)
			return
		}
		if r.Method == http.MethodPut {
			putConnection(w, r, acct, store, connections, id, audit)
			return
		}
		var c state.ToolOAuthConnection
		var err error
		if r.Method == http.MethodDelete {
			c, err = connections.RevokeToolOAuthConnection(r.Context(), acct.ID, id)
			if err == nil && audit != nil {
				audit(r.Context(), "tool_oauth_connection.revoked", acct.ID, map[string]any{"connection_id": id, "app_id": c.Record.AppID})
			}
		} else {
			c, err = connections.GetToolOAuthConnection(r.Context(), acct.ID, id)
		}
		connectionRespond(w, ownerConnectionView(r.Context(), store, c), err)
	}
}

func connectionOwnerAccess(w http.ResponseWriter, r *http.Request, acct state.Account, store state.Store) bool {
	if !accountAccess(w, r, acct, store) {
		return false
	}
	if p, _ := httpkit.PrincipalFrom(r); p.Key != nil && !httpkit.APIKeyCarriesScope(*p.Key, api.ScopeAdmin) {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Owner authority required", "execution OAuth setup requires an unscoped personal administrator"))
		return false
	}
	return true
}

func listConnections(w http.ResponseWriter, r *http.Request, store state.Store, connections state.ToolOAuthConnectionStore, acct state.Account) {
	limit := api.ToolOAuthConnectionPageMax
	var err error
	if r.URL.Query().Has("limit") {
		limit, err = strconv.Atoi(r.URL.Query().Get("limit"))
	}
	if err != nil || limit < 1 || limit > api.ToolOAuthConnectionPageMax {
		connectionRespond(w, nil, state.ErrInvalidArgument)
		return
	}
	rows, err := connections.ListToolOAuthConnections(r.Context(), acct.ID, r.URL.Query().Get("cursor"), limit+1)
	if err != nil {
		connectionRespond(w, nil, err)
		return
	}
	out := api.ToolOAuthConnectionPage{Connections: []api.ToolOAuthConnectionRecord{}}
	if len(rows) > limit {
		rows = rows[:limit]
		out.NextCursor = rows[len(rows)-1].Record.ID
	}
	for _, row := range rows {
		out.Connections = append(out.Connections, ownerConnectionView(r.Context(), store, row))
	}
	connectionRespond(w, out, nil)
}

func putConnection(w http.ResponseWriter, r *http.Request, acct state.Account, store state.Store, connections state.ToolOAuthConnectionStore, id string, audit GrantAudit) {
	var in api.ToolOAuthConnectionIntent
	if httpkit.DecodeJSONSized(r, &in, api.ToolOAuthConnectionIntentMaxBytes) != nil || toolconnection.ValidateIntent(in) != nil {
		connectionRespond(w, nil, state.ErrInvalidArgument)
		return
	}
	if old, err := connections.GetToolOAuthConnection(r.Context(), acct.ID, id); err == nil {
		if !toolconnection.SameIntent(old.Record.Intent, in) {
			connectionRespond(w, nil, state.ErrConflict)
			return
		}
		connectionRespond(w, ownerConnectionView(r.Context(), store, old), nil)
		return
	} else if !errors.Is(err, state.ErrNotFound) {
		connectionRespond(w, nil, err)
		return
	}
	app, err := store.AppBySlug(r.Context(), in.App)
	if err != nil || !personalApp(r, acct, store, app) {
		connectionRespond(w, nil, state.ErrNotFound)
		return
	}
	out := api.ToolOAuthConnectionRecord{ID: id, AccountID: acct.ID, AppID: app.ID, ProjectID: app.ProjectID, Intent: in, CreatedAt: time.Now().UTC(), State: "refresh_required"}
	out.SourceSHA256 = toolconnection.GuidedSourceDigest()
	if !in.Guided {
		refresh, e := connectionSecret(r.Context(), store, out, in.RefreshTokenSecretRef, "")
		if e != nil {
			connectionRespond(w, nil, e)
			return
		}
		out.SourceSHA256 = toolpublication.Digest(refresh.Ciphertext)
	}
	if in.ClientSecretRef != "" {
		client, e := connectionSecret(r.Context(), store, out, in.ClientSecretRef, "")
		if e != nil {
			connectionRespond(w, nil, e)
			return
		}
		out.ClientSourceSHA256 = toolpublication.Digest(client.Ciphertext)
	}
	out.BindingSHA256 = toolconnection.Digest(out)
	saved, err := connections.PutToolOAuthConnection(r.Context(), out)
	if err == nil && audit != nil {
		audit(r.Context(), "tool_oauth_connection.created", acct.ID, map[string]any{"connection_id": id, "app_id": app.ID})
	}
	connectionRespond(w, ownerConnectionView(r.Context(), store, saved), err)
}

func connectionSecret(ctx context.Context, store state.Store, c api.ToolOAuthConnectionRecord, ref, digest string) (*state.AppSecret, error) {
	if !toolgrant.ValidCredentialRef(ref) {
		return nil, state.ErrNotFound
	}
	key := strings.TrimPrefix(ref, api.SecretRefPrefix)
	s, err := store.GetAppSecretInScope(ctx, c.AccountID, c.AppID, c.Intent.Scope, key)
	if err != nil || s == nil || s.AccountID != c.AccountID || s.AppID != c.AppID || s.Scope != c.Intent.Scope || s.Key != key || s.ManagedPostgresBindingID != "" || s.ManagedObjectStorageCredentialID != "" || s.ManagedCredentialRef != "" || digest != "" && toolpublication.Digest(s.Ciphertext) != digest {
		return nil, state.ErrNotFound
	}
	return s, nil
}

func connectionAuthority(ctx context.Context, store state.Store, c state.ToolOAuthConnection) error {
	r := c.Record
	if toolconnection.ValidateRecord(r) != nil || r.RevokedAt != nil {
		return state.ErrNotFound
	}
	app, err := store.AppByID(ctx, r.AppID)
	if err != nil || app.AccountID != r.AccountID || app.Slug != r.Intent.App || app.ProjectID != r.ProjectID {
		return state.ErrNotFound
	}
	if app.OrgID != "" {
		org, e := store.OrgByPersonalAccount(ctx, r.AccountID)
		if e != nil || org.ID != app.OrgID {
			return state.ErrNotFound
		}
	}
	if !r.Intent.Guided {
		if _, err = connectionSecret(ctx, store, r, r.Intent.RefreshTokenSecretRef, r.SourceSHA256); err != nil {
			return err
		}
	}
	if r.Intent.ClientSecretRef != "" {
		_, err = connectionSecret(ctx, store, r, r.Intent.ClientSecretRef, r.ClientSourceSHA256)
	}
	return err
}

func connectionForGrant(ctx context.Context, store state.Store, g api.ToolGrantRecord) (state.ToolOAuthConnection, error) {
	connections, ok := store.(state.ToolOAuthConnectionStore)
	if !ok || g.Credential == nil || g.Credential.ConnectionID == "" {
		return state.ToolOAuthConnection{}, state.ErrNotFound
	}
	c, err := connections.GetToolOAuthConnection(ctx, g.AccountID, g.Credential.ConnectionID)
	if err != nil {
		return c, err
	}
	if c.Record.AppID != g.AppID || c.Record.ProjectID != g.ProjectID || c.Record.BindingSHA256 != g.Credential.SourceSHA256 || c.Record.Intent.Scope != g.Credential.Scope || !toolconnection.Covers(c.Record.Intent.Scopes, g.Credential.RequiredScopes) || c.Record.Intent.Guided && c.Record.Generation == 0 {
		return c, state.ErrNotFound
	}
	return c, connectionAuthority(ctx, store, c)
}

func ownerConnectionView(ctx context.Context, store state.Store, c state.ToolOAuthConnection) api.ToolOAuthConnectionRecord {
	out := toolconnection.View(c.Record, c.LeaseUntil, time.Now())
	if out.State == "authorization_pending" {
		out.RecoveryAction = "connect"
		if connections, ok := store.(state.ToolOAuthConnectionStore); ok {
			if a, err := connections.GetToolOAuthAuthorization(ctx, out.AccountID, out.ID); err == nil && !time.Now().Before(a.ExpiresAt) {
				out.State = "reconnect_required"
				out.RecoveryAction = "reconnect"
			}
		}
	}
	if out.ID != "" && out.RevokedAt == nil && connectionAuthority(ctx, store, c) != nil {
		out.State = "reconnect_required"
		out.RecoveryAction = "review_connection"
	}
	return out
}

func connectionRespond(w http.ResponseWriter, out any, err error) {
	var limit *state.ToolOAuthConnectionLimitError
	switch {
	case errors.As(err, &limit):
		api.WriteProblem(w, api.NewProblem(429, "tool_oauth_connection_limit", "Connection capacity reached", "retained execution OAuth connections are bounded per app and account").WithLimit(limit.Limit, limit.Observed).WithDocs("https://gregale.dev/docs/mcp#managed-oauth-for-tool-execution"))
	case errors.Is(err, state.ErrToolOAuthBusy):
		api.WriteProblem(w, api.NewProblem(409, "tool_connection_refreshing", "Connection renewal in progress", "retain the invocation identity and inspect connection status before trying again"))
	case errors.Is(err, state.ErrToolOAuthReconnect):
		api.WriteProblem(w, api.NewProblem(503, "tool_connection_reconnect_required", "Connection approval required", "ask the owner to reconnect and review a new execution connection"))
	default:
		grantRespond(w, out, err)
	}
}

func DelegatedConnection(store state.Store) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		w.Header().Set("Cache-Control", "no-store")
		key, ok := registryKey(w, r, acct, store)
		if !ok {
			return
		}
		id := r.PathValue("grant")
		if !toolgrant.ValidID(id) {
			connectionRespond(w, nil, state.ErrInvalidArgument)
			return
		}
		grants, ok := store.(state.ToolGrantStore)
		if !ok {
			connectionRespond(w, nil, errors.New("grant store unavailable"))
			return
		}
		g, err := grants.GetToolGrant(r.Context(), acct.ID, id)
		if err != nil || !toolgrant.Active(g, time.Now()) || !g.Intent.AllowInvocation || g.Intent.KeyID != key.ID || g.Credential == nil || g.Credential.ConnectionID == "" {
			connectionRespond(w, nil, state.ErrNotFound)
			return
		}
		if err := delegatedConnectionGrant(r, acct, store, key, g); err != nil {
			connectionRespond(w, nil, err)
			return
		}
		c, err := connectionForGrant(r.Context(), store, g)
		if err != nil {
			connectionRespond(w, nil, err)
			return
		}
		view := ownerConnectionView(r.Context(), store, c)
		connectionRespond(w, api.ToolOAuthConnectionStatus{GrantID: id, App: g.App, AppID: g.AppID, Version: g.Intent.Version, Name: g.Intent.Name, State: view.State, ExpiresAt: view.ExpiresAt, RecoveryAction: view.RecoveryAction}, nil)
	}
}

func delegatedConnectionGrant(r *http.Request, acct state.Account, store state.Store, key state.APIKey, g api.ToolGrantRecord) error {
	publications, ok := store.(state.ToolPublicationStore)
	if !ok {
		return state.ErrNotFound
	}
	p, err := publications.GetToolPublication(r.Context(), acct.ID, g.Intent.Version)
	if err != nil {
		return err
	}
	_, allowed, err := accessibleGrant(r, acct, store, key, state.ToolGrantEntry{Grant: g, Publication: p})
	if err != nil {
		return err
	}
	if !allowed {
		return state.ErrNotFound
	}
	return nil
}
