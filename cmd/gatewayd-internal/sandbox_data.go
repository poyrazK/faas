package main

import (
	"log/slog"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/sandboxdata"
	"github.com/onebox-faas/faas/pkg/sandboxtoken"
)

// newSandboxDataHandler serves sandbox session data-plane calls
// (ADR-20261027090013 §4), verifying session tokens with tokens. Nil when
// there is no verifier or node cache: the gateway holds no session key
// (ADR-20261102191500), so until the session-token key reaches gateways the
// data plane stays off and its paths fall through to apid, which owns none
// of them.
func newSandboxDataHandler(deps runDeps, tokens *sandboxtoken.Signer, log *slog.Logger) http.Handler {
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
