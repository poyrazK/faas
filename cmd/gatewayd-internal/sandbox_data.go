package main

import (
	"log/slog"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/sandboxdata"
	"github.com/onebox-faas/faas/pkg/sandboxtoken"
)

// newSandboxDataHandler serves sandbox session data-plane calls
// (ADR-20261027090013 §4). Session tokens are signed with the session
// binding key apid shares, so the calls read no database. Nil when the key or
// the node cache is missing.
func newSandboxDataHandler(deps runDeps, log *slog.Logger) http.Handler {
	if deps.nodeCache == nil || deps.sessions == nil {
		return nil
	}
	signer, err := sandboxtoken.NewSigner(deps.sessions.BindingKey())
	if err != nil {
		log.Warn("gatewayd-internal: sandbox data plane off", "err", err.Error())
		return nil
	}
	mux := http.NewServeMux()
	(&sandboxdata.Handler{Tokens: signer, Nodes: deps.nodeCache.cache, Log: log}).Routes(mux)
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
