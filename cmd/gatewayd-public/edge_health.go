package main

// Edge health for the Cloudflare Load Balancer (ADR-1152 §2).
//
// A cell's edges sit behind one Cloudflare Load Balancer pool, and its
// monitor decides which edges receive traffic. /readyz is the wrong signal
// for that: it includes a Postgres ping, so a database outage would take
// every edge out of the pool at once, exactly when the edges keep serving
// warm traffic from their last-known routes (ADR-1152 §5). /edgez answers
// only what the load balancer needs to know: this edge is not draining and
// can reach a compute gateway. Caddy exposes it to the monitor at one
// reserved path on the origin hostname (public_edge role).

import (
	"net/http"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/gateway"
)

type edgeHealth struct {
	internal *gateway.ReadySignal
	draining atomic.Bool
}

// ready reports whether this edge should receive traffic.
func (e *edgeHealth) ready() (bool, string) {
	if e.draining.Load() {
		return false, "draining"
	}
	if e.internal == nil {
		return false, "compute gateway check not wired"
	}
	return e.internal.Report()
}

// startDraining takes the edge out of the pool at once, before in-flight
// requests finish, so the load balancer stops sending new ones.
func (e *edgeHealth) startDraining() { e.draining.Store(true) }

func (e *edgeHealth) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if ok, reason := e.ready(); !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not-ready: " + reason))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	}
}
