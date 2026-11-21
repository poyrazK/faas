package gateway

import (
	"context"
	"net/http"
	"sync/atomic"
)

type capacityRefusalKey struct{}

// withCapacityRefusalMark plants the flag markCapacityRefused sets. ServeHTTP
// installs it first, before the wake path can refuse the request.
func withCapacityRefusalMark(ctx context.Context) context.Context {
	return context.WithValue(ctx, capacityRefusalKey{}, new(atomic.Bool))
}

// markCapacityRefused records that the gateway refused the request for lack of
// app capacity: the plan's concurrency was reached, the drop overflow mode
// shed it, the concurrency queue was full or the node refused it. Observe
// counts it in gateway_capacity_refusals_total, which customer SLOs, error
// rates and outage detection read as failed requests (Observe audit
// 2026-11-21, findings 8 and 15; ADR-20261121190000). A request without the
// flag is ignored.
func markCapacityRefused(r *http.Request) {
	if r == nil {
		return
	}
	if flag, ok := r.Context().Value(capacityRefusalKey{}).(*atomic.Bool); ok && flag != nil {
		flag.Store(true)
	}
}

// capacityRefusedFrom reports markCapacityRefused for r.
func capacityRefusedFrom(r *http.Request) bool {
	if r == nil {
		return false
	}
	flag, ok := r.Context().Value(capacityRefusalKey{}).(*atomic.Bool)
	return ok && flag != nil && flag.Load()
}
