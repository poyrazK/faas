package mcpmanagement

import (
	"errors"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// idleServerTTL is how long an approved connection keeps its MCP server after
// its last request. Its plans are durable and rebuilding takes about a
// millisecond, so memory follows active connections rather than the grant
// bound (ADR-20261026195952).
const idleServerTTL = 10 * time.Minute

// grantOptions are the per-connection server options consent approved.
func (r *remoteServer) grantOptions(g *oauthGrant, apps map[string]string, environments *EnvironmentConsent) Options {
	return Options{Lifecycle: r.opts.Lifecycle, Telemetry: r.opts.Telemetry, auditLogger: r.opts.AuditLogger, accountID: g.accountID, grantID: g.id, AllowedApps: apps, Environments: environments, planStore: r.opts.StateStore, planNamespace: g.id, Profile: "oauth", APIBase: r.opts.APIBase, Version: r.opts.Version, Timeout: r.opts.Timeout, Token: g.client.Token(), AllowWrites: r.opts.AllowWrites && slices.Contains(g.scopes, scopeWrite), Toolsets: r.opts.Toolsets, Capabilities: g.capabilities, LocalCLI: r.grantCLI(g.client.Token()), uploads: r.uploads}
}

// serverLocked returns an approved connection's MCP server, building it from
// the consented options and durable plans on first use. Caller holds r.mu.
func (r *remoteServer) serverLocked(g *oauthGrant) (*mcp.Server, error) {
	if g.server == nil {
		server, management, err := newManagementServer(g.client, r.grantOptions(g, g.allowedApps, g.environments))
		if err != nil {
			return nil, err
		}
		g.server, g.management = server, management
	}
	return g.server, nil
}

var errStaleAccess = errors.New("access token no longer names this connection")

// holdServer re-checks that the access token still names g and holds g's
// server for one request; release lets it be dropped once idle again.
func (r *remoteServer) holdServer(key string, g *oauthGrant) (*mcp.Server, time.Time, func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, expiry, valid := r.accessLeaseLocked(key)
	if !valid || current != g {
		return nil, time.Time{}, nil, errStaleAccess
	}
	server, err := r.serverLocked(g)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	g.inflight++
	g.lastUsed = time.Now()
	return server, expiry, func() {
		r.mu.Lock()
		g.inflight--
		g.lastUsed = time.Now()
		r.mu.Unlock()
	}, nil
}

// dropIdleServersLocked releases servers whose connection has been idle for
// idleServerTTL. A server serving a request, holding work, or holding a
// secret request (kept only in memory) stays. Caller holds r.mu.
func (r *remoteServer) dropIdleServersLocked(now time.Time) {
	for _, g := range r.grants {
		if g.server != nil && g.inflight == 0 && now.Sub(g.lastUsed) > idleServerTTL && g.management.idle(now) {
			g.server, g.management = nil, nil
		}
	}
}

// idle reports whether dropping this server loses nothing a rebuild cannot
// restore from durable state.
func (m *management) idle(now time.Time) bool {
	if m == nil {
		return true
	}
	m.mu.Lock()
	for _, req := range m.secretRequests {
		if now.Before(req.expiresAt) {
			m.mu.Unlock()
			return false
		}
	}
	m.mu.Unlock()
	if m.grantLifecycle != nil && m.grantLifecycle.busy() {
		return false
	}
	if m.subscriptions != nil {
		m.subscriptions.mu.Lock()
		listening := len(m.subscriptions.groups) > 0
		m.subscriptions.mu.Unlock()
		if listening {
			return false
		}
	}
	return true
}
