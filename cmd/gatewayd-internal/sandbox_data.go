package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/sandboxdata"
	"github.com/onebox-faas/faas/pkg/gateway/sandboxpreview"
	"github.com/onebox-faas/faas/pkg/sandboxtoken"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// newSandboxDataHandler serves sandbox session data-plane calls
// (ADR-20261027090013 §4), verifying session tokens with apid's public keys.
// Nil without a verifier or node cache. Until the first keys arrive every
// token is refused like an absent route.
func newSandboxDataHandler(deps runDeps, tokens *sandboxtoken.Verifier, log *slog.Logger) http.Handler {
	if deps.nodeCache == nil || tokens == nil {
		return nil
	}
	mux := http.NewServeMux()
	(&sandboxdata.Handler{Tokens: tokens, Nodes: deps.nodeCache.cache, Log: log}).Routes(mux)
	return mux
}

// sandboxDataCarveOut routes sandbox data-plane paths to their handler ahead
// of the apid loopback proxy.
type sandboxDataCarveOut struct {
	sandbox http.Handler
	next    http.Handler
}

func (c sandboxDataCarveOut) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if c.sandbox != nil && api.IsSandboxDataPath(r.URL.Path) {
		c.sandbox.ServeHTTP(w, r)
		return
	}
	c.next.ServeHTTP(w, r)
}

// newSandboxPreviewHandler serves sandbox preview hosts (ADR-20261027090013
// §11). Nil without a preview domain, a database, a verifier or a node
// cache: previews stay off and their hosts route like any other.
func newSandboxPreviewHandler(deps runDeps, log *slog.Logger) *sandboxpreview.Handler {
	if deps.sandboxPreviewDomain == "" || deps.pool == nil || deps.sandboxTokens == nil || deps.nodeCache == nil {
		return nil
	}
	log.Info("gatewayd-internal: sandbox previews on", "domain", deps.sandboxPreviewDomain)
	return &sandboxpreview.Handler{Domain: deps.sandboxPreviewDomain, Routes: previewRoutes{store: agentstore.New(deps.pool)},
		Tokens: deps.sandboxTokens, Nodes: deps.nodeCache.cache, Log: log}
}

// previewRoutes reads preview routes from the agents store; the handler
// caches them.
type previewRoutes struct{ store *agentstore.Store }

func (p previewRoutes) PreviewRoute(ctx context.Context, sandboxID string) (sandboxpreview.Route, error) {
	r, err := p.store.PreviewRoute(ctx, sandboxID)
	if errors.Is(err, agentstore.ErrNotFound) {
		return sandboxpreview.Route{}, sandboxpreview.ErrNoRoute
	}
	if err != nil {
		return sandboxpreview.Route{}, err
	}
	return sandboxpreview.Route{AccountID: r.AccountID, Plan: api.Plan(r.Plan), Live: !agentstore.SandboxTerminal(r.State),
		NodeID: r.NodeID, InstanceID: r.InstanceID, Ports: r.Ports}, nil
}

// sandboxPreviewCarveOut routes preview hosts to their handler ahead of
// everything else on the node.
type sandboxPreviewCarveOut struct {
	preview *sandboxpreview.Handler
	next    http.Handler
}

func (c sandboxPreviewCarveOut) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if c.preview.Match(r.Host) {
		c.preview.ServeHTTP(w, r)
		return
	}
	c.next.ServeHTTP(w, r)
}
