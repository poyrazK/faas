package canary

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ClosedTelemetryUntil caps a request telemetry window's exclusive end at the
// last UTC minute whose gateway buckets have shipped.
//
// Gateways ship a server error as its own row at once but fold successful
// requests into a per-minute bucket that ships only after the minute closes
// (ADR-20261101085552). Reading the open minute therefore saw a release's 5xx
// before its 2xx: prod auto-rolled back a release at 6 requests / 6 errors
// while its true error rate was about 3% (2026-11-09). Route health already
// reads only closed minutes behind api.RouteHealthIngestionLag; the release
// guards use the same allowance (ADR-625 amendment 2026-11-10).
func ClosedTelemetryUntil(until, now time.Time) time.Time {
	closed := now.UTC().Add(-api.RouteHealthIngestionLag).Truncate(time.Minute)
	if until.After(closed) {
		return closed
	}
	return until
}
