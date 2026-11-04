package mcpmanagement

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// A headless agent opens the hosted server with an API key that carries the
// agent scope, sent as its bearer token (ADR-20261029085359). The key's scopes
// pick its tools, as consent does for an OAuth grant; it reaches every app in
// its account, as a local connection does. Every request re-verifies the key
// with apid, so a revoked or expired key stops at its next request. A key
// connection keeps nothing durable: its plans live in memory, and an idle one
// is dropped like an idle grant's server.
type keyConnection struct {
	// id names the connection's uploads and audit lines; it is derived from
	// the key's hash, never the key.
	id         string
	accountID  string
	scopes     []string
	client     *api.Client
	server     *mcp.Server
	management *management
	inflight   int
	lastUsed   time.Time
}

var errKeyConnectionCapacity = errors.New("agent key connection capacity reached")

// agentKeyBearer reports whether a bearer token has the shape of a Gregale API
// key rather than one of this bridge's OAuth tokens.
func agentKeyBearer(token string) bool {
	return strings.HasPrefix(token, "fp_") && !strings.HasPrefix(token, "gmcp_")
}

// agentKeyScopesUsable reports whether a key may open the server: an agent key,
// never an admin one, that can inspect.
func agentKeyScopesUsable(scopes []string) bool {
	return slices.Contains(scopes, api.ScopeAgent) && !slices.Contains(scopes, api.ScopeAdmin) && slices.Contains(scopes, api.ScopeAppsRead)
}

// serveAgentKey authenticates a request by its agent key and serves it with
// the key's connection.
func (r *remoteServer) serveAgentKey(w http.ResponseWriter, req *http.Request, next http.Handler, token string) {
	client := r.apiClient(token)
	verifyCtx, stopVerify := context.WithTimeout(req.Context(), r.opts.Timeout)
	account, err := client.Whoami(verifyCtx)
	stopVerify()
	if err != nil || account.ID == "" {
		r.challenge(w)
		return
	}
	if !agentKeyScopesUsable(account.KeyScopes) {
		w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="agent apps:read"`)
		oauthError(w, http.StatusForbidden, "insufficient_scope", "This API key is not an agent key. Create one for your agent with `gregale key create <name> --scopes agent,deploy:write` (add secrets:request, storage:manage or postgres:manage,postgres:read as needed), or connect with OAuth.")
		return
	}
	server, release, err := r.holdKeyConnection(tokenHash(token), account.ID, account.KeyScopes, client)
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "Agent connection capacity reached; retry shortly")
		return
	}
	defer release()
	next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), grantServerKey{}, server)))
}

// holdKeyConnection returns the key's connection server, building it on first
// use, and holds it for one request.
func (r *remoteServer) holdKeyConnection(hash, accountID string, scopes []string, client *api.Client) (*mcp.Server, func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.keyConns[hash]
	if c != nil && (c.accountID != accountID || !slices.Equal(c.scopes, scopes)) {
		// A request already holding the old server keeps its own reference.
		c = nil
	}
	if c == nil {
		if len(r.keyConns) >= r.capacity() {
			r.dropIdleKeyConnectionsLocked(time.Now(), 0)
		}
		if len(r.keyConns) >= r.capacity() {
			return nil, nil, errKeyConnectionCapacity
		}
		c = &keyConnection{id: "gmcp_key_" + hash[:24], accountID: accountID, scopes: slices.Clone(scopes), client: client}
		r.keyConns[hash] = c
	}
	if c.server == nil {
		server, management, err := newManagementServer(c.client, r.keyOptions(c))
		if err != nil {
			delete(r.keyConns, hash)
			return nil, nil, err
		}
		c.server, c.management = server, management
		if r.opts.AuditLogger != nil {
			r.opts.AuditLogger.Info("management MCP authorization", "event", "mcp.agent_key.connected", "grant_id", c.id, "account_id", c.accountID)
		}
	}
	c.inflight++
	c.lastUsed = time.Now()
	return c.server, func() {
		r.mu.Lock()
		c.inflight--
		c.lastUsed = time.Now()
		r.mu.Unlock()
	}, nil
}

// keyOptions are a key connection's server options: the key's scopes stand in
// for consent, and nothing is persisted.
func (r *remoteServer) keyOptions(c *keyConnection) Options {
	var capabilities []string
	for _, scope := range []string{api.ScopeSecretsRequest, api.ScopeStorageManage, api.ScopeManagedPostgresManage} {
		if slices.Contains(c.scopes, scope) {
			capabilities = append(capabilities, scope)
		}
	}
	return Options{Lifecycle: r.opts.Lifecycle, Telemetry: r.opts.Telemetry, auditLogger: r.opts.AuditLogger, accountID: c.accountID, grantID: c.id, Profile: "agent-key", APIBase: r.opts.APIBase, Version: r.opts.Version, Timeout: r.opts.Timeout, Token: c.client.Token(), AllowWrites: r.opts.AllowWrites && slices.Contains(c.scopes, api.ScopeDeployWrite), Toolsets: r.opts.Toolsets, Capabilities: capabilities, LocalCLI: r.grantCLI(c.client.Token()), uploads: r.uploads, agentKey: true}
}

// dropIdleKeyConnectionsLocked forgets key connections unused for idle with
// nothing in flight and nothing a rebuild would lose. Caller holds r.mu.
func (r *remoteServer) dropIdleKeyConnectionsLocked(now time.Time, idle time.Duration) {
	for hash, c := range r.keyConns {
		if c.inflight == 0 && now.Sub(c.lastUsed) >= idle && c.management.idle(now) {
			delete(r.keyConns, hash)
		}
	}
}
