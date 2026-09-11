// pkg/meter/usage_summary.go — customer-facing per-app usage summary
// rollup (commit 4 of the per-app observability PR series).
//
// The customer-facing /v1/apps/{slug}/usage endpoint reads through
// this helper so the wire response shape stays decoupled from the
// internal Usage rows. The handler is responsible for the
// plan-aware overage computation (free falls through the 402 gate;
// Hobby+/Pro/Scale compute overage = max(0, total - included)); this
// helper stays plan-agnostic and just rolls up the raw window.
//
// Source: pkg/state.Store.UsageByHour — the per-(account, app,
// hour) rollup from usage_minutes that the Stripe pusher already
// consults hourly. We filter to one app_id client-side rather than
// adding a new SQL binding — UsageByHour's indexed scan is the
// dominant cost (the per-app filter is a constant-time in-memory
// pass over a few hundred rows max for any sane 30-day window).
//
// ADR-048 retention: usage_minutes keeps minute-grain rows for 30d;
// usage_daily extends coverage to 1y. This helper rides the
// existing UsageByHour reader and therefore inherits the 30d cap
// (the usage_daily reader is a separate follow-up that lands with
// the trail-period extension). Source="usage_minutes" on the wire
// shape signals the upper bound; future usage_daily coverage will
// switch the label without a wire-shape change.

package meter

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// AppWindowSummary is the plan-agnostic rollup for one app over a
// half-open window [since, until). All units mirror the underlying
// Usage columns:
//
//   - MBSeconds + GBHours: billable resource consumption. GBHours is
//     GBHours(MBSeconds) — the same conversion Billing / the
//     Stripe pusher use. Rounded to 6 decimal places so the test
//     surface and the financial-model cells line up without float
//     drift (mirror of MonthlyUsageGB).
//   - Requests / TxBytes: cumulative HTTP activity (informational,
//     not billed — billing is on RAM).
//   - BuilderSeconds: cumulative builder-microVM CPU-time. Not
//     surfaced on the wire yet (reserved for a future
//     /v1/apps/{slug}/builds-budget endpoint) but kept here so
//     the SQL reads stay one round-trip.
//   - ColdBootCount: WAKE_RESTORE→WAKE_COLD_BOOT transitions in
//     the window.
//
// Source labels: "usage_minutes" today (after the 30d retention
// cap), "usage_daily" after the rollup PR lands, "mixed" if a
// follow-up bridges both. The handler stamps the value on the
// wire response so the dashboard SPA can render an "estimated vs.
// exact" badge.
type AppWindowSummary struct {
	MBSeconds      int64
	GBHours        float64
	Requests       int64
	TxBytes        int64
	NetTxBytes     int64
	BuilderSeconds float64 // informational; surfaced as builder_seconds on the wire
	ColdBootCount  int64
}

// UsageSummaryStore is the minimal store surface BuildAppWindowSummary
// needs. Matches the existing UsageByHour contract on pkg/state.Store
// — extracted to a one-method interface so tests can stub without
// standing up the full state.Store (see usage_summary_test.go).
type UsageSummaryStore interface {
	UsageByHour(ctx context.Context, accountID string, start, end time.Time) ([]state.Usage, error)
}

// BuildAppWindowSummary rolls up UsageByHour rows for one
// accountID+appID over the half-open window [since, until).
// Filters to one app_id client-side; sums every additive column.
// The Source label is hard-wired to "usage_minutes" today — the
// trail-period usage_daily reader is a separate follow-up (see
// ADR-048 §5).
//
// Failure modes:
//   - store error → returned verbatim. The handler 5xxs with a
//     generic Problem envelope (the customer-facing read path
//     doesn't leak SQL details).
//   - since > until → returns the zero summary + nil error. The
//     handler clamps this to a 400 anyway via URL parsing, but
//     fail-safe here so a unit test exercising the helper
//     directly doesn't get a panic.
//
// Thread-safety: read-only — the underlying store is the
// production concurrent reader (PgStore) or MemStore (tests).
func BuildAppWindowSummary(
	ctx context.Context,
	store UsageSummaryStore,
	accountID, appID string,
	since, until time.Time,
) (AppWindowSummary, string, error) {
	if since.After(until) || since.Equal(until) {
		// Empty window — zero summary, no SQL round-trip needed.
		return AppWindowSummary{}, "usage_minutes", nil
	}
	rows, err := store.UsageByHour(ctx, accountID, since.UTC(), until.UTC())
	if err != nil {
		return AppWindowSummary{}, "usage_minutes", err
	}
	var sum AppWindowSummary
	for _, u := range rows {
		if u.AppID != appID {
			continue
		}
		sum.MBSeconds += u.MBSeconds
		sum.Requests += u.Requests
		sum.TxBytes += u.TXBytes
		sum.NetTxBytes += u.NetTxBytes
		// cpu_usec is host cgroup CPU-µs consumed; convert to
		// CPU-seconds for the wire shape so the dashboard's
		// builder-seconds figure is comparable to its
		// instance-seconds figure. CPUHours gives CPU-hours, so
		// multiply by 3600.
		sum.BuilderSeconds += CPUHours(u.CPUUsec) * 3600
		sum.ColdBootCount += u.ColdBootCount
	}
	sum.GBHours = float64(int64(GBHours(sum.MBSeconds)*1e6+0.5)) / 1e6
	return sum, "usage_minutes", nil
}
