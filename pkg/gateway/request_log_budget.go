package gateway

import (
	"log/slog"
	"sync/atomic"

	"golang.org/x/time/rate"

	"github.com/onebox-faas/faas/pkg/api"
)

// requestLogBudget bounds the gateway_request lines a gatewayd-internal
// writes at Info: a cold request or any answer but a 2xx. Without it a flood
// of 404s for names that route nowhere, or an app answering errors at volume,
// writes one line per request. Building those lines took about 12% of
// gatewayd-internal's CPU under a 404 flood on 2026-11-09, and past journald's
// per-unit rate limit journald dropped every line the daemon wrote, warnings
// included. A line past the budget is never built: the budget counts it, and
// the next line written carries suppressed_before.
type requestLogBudget struct {
	limiter    *rate.Limiter
	suppressed atomic.Uint64
}

func newRequestLogBudget() *requestLogBudget {
	return &requestLogBudget{
		limiter: rate.NewLimiter(rate.Limit(api.GatewayRequestLogLinesPerSecond), api.GatewayRequestLogBurst),
	}
}

// admit reports whether a request line at level may be written, spending the
// budget for an Info line. A Debug line, written only when debug logging is
// on, is not budgeted. A nil budget admits every line.
func (b *requestLogBudget) admit(level slog.Level, m *Metrics) bool {
	if b == nil || level < slog.LevelInfo || b.limiter.Allow() {
		return true
	}
	b.suppressed.Add(1)
	m.ObserveRequestLogSuppressed()
	return false
}

// takeSuppressed returns the lines suppressed since the last call and resets
// the count, so the next written line can carry it.
func (b *requestLogBudget) takeSuppressed() uint64 {
	if b == nil {
		return 0
	}
	return b.suppressed.Swap(0)
}
