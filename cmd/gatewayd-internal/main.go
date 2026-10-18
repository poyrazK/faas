// Command gatewayd-internal — routing + wake + proxy daemon (Tier
// A7 split, ADR-068 / ADR-070).
//
// gatewayd-internal owns the routing + wake + proxy layer. It does
// NOT terminate TLS — that is gatewayd-public's job. It listens on
// a unix socket at /run/faas/gatewayd-internal.sock (the same socket
// shape schedd uses, ADR-015/018) and is reached only by
// gatewayd-public over loopback.
//
// Inbound traffic shape:
//
//	gatewayd-public → unix socket → gatewayd-internal.ServeHTTP
//	                                          → pkg/gateway/handler.go
//	                                              (hostname→app,
//	                                               wake gate,
//	                                               rate limit,
//	                                               forwarder)
//
// The handler, PGBackend, schedd-router, and forwarder code moved
// verbatim from the legacy cmd/gatewayd/ into this package in PR-A
// (tier-a7 finishing cluster). PR-B wired prodRun() so customer
// traffic reaches the real handler chain; PR-C swept the deploy
// pipeline so cd-controlplane installs this binary (and not the
// legacy one) — see the PR-cluster strategy memory.
//
// Listeners:
//
//	/run/faas/gatewayd-internal.sock   unix socket (loopback only)
//
// The control plane (/healthz, /readyz, /metrics) is wired inside
// run.go's runWithDeps on the address passed via FAAS_GATEWAYD_CONFIG
// (control_addr key) — defaults to :9090 in cmd/gatewayd-internal/config.go.
//
// Drain: SIGTERM → /readyz=503 → GatewayDrainGraceSeconds → Shutdown
// (handled inside runWithDeps, ADR-068 internal-first order).
package main

import (
	"os"

	"github.com/onebox-faas/faas/pkg/wire"
)

// main is the daemon entry point. The wire.Daemon harness wires
// SIGTERM, slog, and ctx cancellation around run; run itself lives in
// run.go and owns the PG pool, schedd router, and HTTP servers.
func main() {
	if path := configPathArg(os.Args[1:]); path != "" {
		configPath = path
	}
	wire.HandoffDaemon("gatewayd-internal", run)
}

// configPathArg keeps the systemd ExecStart contract honest. The service
// units pass --config explicitly; accepting it here also makes a manually
// launched daemon behave exactly like the deployed unit. Environment remains
// the fallback for container/e2e callers that do not pass an argument.
func configPathArg(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] != "--config" {
			continue
		}
		if i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
