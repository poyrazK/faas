// Package agentconnections lists and revokes the agents connected to an
// account (ADR-20261030133644): hosted MCP sign-ins and agent keys. A
// connected agent is the API key it uses, so revoking one revokes the key.
package agentconnections

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Deps are the apid services the handlers use.
type Deps struct {
	Store state.Store
	Log   *slog.Logger
	// Prefixes returns the display prefix of each key by ID.
	Prefixes func(context.Context, []state.APIKey) map[string]string
	// Revoke is apid's shared key revocation: it invalidates authentication
	// caches and audits key.revoked.
	Revoke func(r *http.Request, acct state.Account, keyID string) error
	// Audit records an audit event for the account.
	Audit func(ctx context.Context, kind string, accountID string, data map[string]any)
}

// Kind reports how a key connects an agent, or "" for any other key. An
// mcp-login key with the admin scope is an ordinary key some customer
// labelled that way, as for TouchKeyLastUsed.
func Kind(k state.APIKey) string {
	switch {
	case k.Label == api.MCPLoginKeyLabel && !slices.Contains(k.Scopes, api.ScopeAdmin):
		return api.AgentConnectionHostedMCP
	case slices.Contains(k.Scopes, api.ScopeAgent):
		return api.AgentConnectionAgentKey
	}
	return ""
}

func usable(k state.APIKey, now time.Time) bool {
	return (k.Status == "" || k.Status == "active" || k.Status == "grace") && (k.ExpiresAt == nil || k.ExpiresAt.After(now))
}

// connected returns the account's usable agent keys, newest first.
func connected(ctx context.Context, store state.Store, accountID string) ([]state.APIKey, error) {
	keys, err := store.ListAPIKeys(ctx, accountID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := keys[:0:0]
	for _, k := range keys {
		if Kind(k) != "" && usable(k, now) {
			out = append(out, k)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// List handles GET /v1/agent-connections.
func List(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		keys, err := connected(r.Context(), d.Store, acct.ID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not list agent connections"))
			return
		}
		clients := map[string]state.AgentClient{}
		if store, ok := d.Store.(state.AgentClientStore); ok && len(keys) > 0 {
			if clients, err = store.APIKeyAgentClients(r.Context(), acct.ID); err != nil {
				d.Log.WarnContext(r.Context(), "read agent client names", slog.String("err", err.Error()))
				clients = map[string]state.AgentClient{}
			}
		}
		prefixes := d.Prefixes(r.Context(), keys)
		out := api.AgentConnectionsResponse{Items: make([]api.AgentConnectionResponse, 0, len(keys))}
		for _, k := range keys {
			out.Items = append(out.Items, response(k, clients[k.ID], prefixes[k.ID]))
		}
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}

func response(k state.APIKey, client state.AgentClient, prefix string) api.AgentConnectionResponse {
	out := api.AgentConnectionResponse{ID: k.ID, Kind: Kind(k), ClientName: client.Name, ClientSite: client.Site, Label: k.Label, Prefix: prefix, Scopes: k.Scopes, CreatedAt: k.CreatedAt.UTC().Format(time.RFC3339)}
	if k.LastUsedAt != nil {
		out.LastUsedAt = k.LastUsedAt.UTC().Format(time.RFC3339)
	}
	if k.ExpiresAt != nil {
		out.ExpiresAt = k.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return out
}

// Revoke handles DELETE /v1/agent-connections/{id}. Revoking an agent's key
// again succeeds; a key that is not one of the account's agents is 404.
func Revoke(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		k, err := d.Store.GetAPIKey(r.Context(), acct.ID, r.PathValue("id"))
		if errors.Is(err, state.ErrNotFound) || (err == nil && Kind(k) == "") {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Agent connection not found", "no connected agent with this id belongs to this account"))
			return
		}
		if err == nil {
			err = d.Revoke(r, acct, k.ID)
		}
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not revoke agent connection"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// RevokeAll handles POST /v1/agent-connections/revoke_all.
func RevokeAll(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		keys, err := connected(r.Context(), d.Store, acct.ID)
		revoked := 0
		for i := 0; err == nil && i < len(keys); i++ {
			if err = d.Revoke(r, acct, keys[i].ID); err == nil {
				revoked++
			}
		}
		if revoked > 0 {
			d.Audit(r.Context(), "agent_connections.revoke_all", acct.ID, map[string]any{"revoked_count": revoked})
		}
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not revoke every agent connection"))
			return
		}
		httpkit.WriteJSON(w, http.StatusOK, api.AgentConnectionsRevokeAllResponse{Revoked: revoked})
	}
}

// hostName is a lower-case DNS host name.
var hostName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

// Name handles PUT /v1/agent-connections/self: the hosted MCP server names
// the client of the key it calls with. The first name wins.
func Name(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		p, ok := httpkit.PrincipalFrom(r)
		if !ok || p.Key == nil || Kind(*p.Key) != api.AgentConnectionHostedMCP {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Hosted MCP key required", "only a hosted MCP connection's key can name its client"))
			return
		}
		var req api.AgentClientRequest
		if err := httpkit.DecodeJSON(r, &req); err != nil || !validName(req.ClientName) || len(req.ClientSite) > 253 || (req.ClientSite != "" && !hostName.MatchString(req.ClientSite)) {
			api.WriteProblem(w, api.ErrValidation("client_name must be 1 to 80 printable characters and client_site a host name"))
			return
		}
		store, ok := d.Store.(state.AgentClientStore)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("agent client names unavailable"))
			return
		}
		if err := store.SetAPIKeyAgentClient(r.Context(), acct.ID, p.Key.ID, state.AgentClient{Name: req.ClientName, Site: req.ClientSite}); err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not name agent client"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// Disconnect handles DELETE /v1/agent-connections/self: the hosted MCP
// server retires the key of a connection that ended (denied, revoked by the
// client or an operator, or expired).
func Disconnect(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		p, ok := httpkit.PrincipalFrom(r)
		if !ok || p.Key == nil || Kind(*p.Key) != api.AgentConnectionHostedMCP {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Hosted MCP key required", "only a hosted MCP connection's key can disconnect itself"))
			return
		}
		if err := d.Revoke(r, acct, p.Key.ID); err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not disconnect agent"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// validName refuses control characters: names are printed in terminals.
func validName(name string) bool {
	n := utf8.RuneCountInString(name)
	return utf8.ValidString(name) && n >= 1 && n <= api.AgentClientNameMax && strings.TrimSpace(name) == name && !strings.ContainsFunc(name, unicode.IsControl)
}
