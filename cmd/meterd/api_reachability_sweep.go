// cmd/meterd/api_reachability_sweep.go — ADR-123 alert-preset
// signal-feeding goroutine for the `api_down` preset (issue #1233,
// PR-B). Mirrors the existing CertExpiryRefresherLoop /
// AccountSpendAggregatorLoop pattern in alert_presets_ticks.go
// (same package, free-function goroutine, ctx-cancelled, nil-coerced
// log + ops).
//
// The api_up alert preset has historically been computed inline by
// pkg/alerts/evaluator.go via state.WasInvokedSuccessfullySince on a
// per-rule basis. PR-B adds the operator-visible Prometheus mirror
// meterd_api_reachable{account_id, app_id} = 1.0 if the app served
// a successful invocation in the last 5 minutes, 0.0 otherwise. The
// evaluator keeps reading its inline path; the gauge is purely the
// "is the signal actually flowing" observability mirror — without
// it, an alert rule that stops firing on a real outage looks
// identical to one whose metric series is silently absent.
//
// Walk pattern:
//  1. ListAllAccounts → for each account, ListApps(accountID)
//  2. For each (accountID, app.ID), call
//     state.WasInvokedSuccessfullySince(ctx, accountID, appID, now-5min)
//  3. Stamp gauge.WithLabelValues(accountID, appID).Set(1.0|0.0)
//
// Cardinality: bounded by per-plan app count (Hobby=5, Pro=25,
// Scale=100). A 1000-account Scale fleet produces ~100k series
// worst case, well under Prometheus's per-target series ceiling.
//
// Memory model: no per-pair state is held across ticks; the gauge
// label set is recreated on every sweep (Prometheus handles
// Add-on-existing-label as a no-op), and a new app that surfaces
// between ticks starts stamping from its next sweep.
package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// apiReachabilityStore is the slice of state.Store the sweep reads.
type apiReachabilityStore interface {
	ListAllAccounts(ctx context.Context) ([]state.Account, error)
	ListApps(ctx context.Context, accountID string) ([]state.App, error)
	WasInvokedSuccessfullySince(ctx context.Context, accountID, appID string, since time.Time) (bool, error)
}

// apiReachabilityStepTimeout bounds each store read in the sweep. The walk
// used to share one 60 s deadline, so a single stall while meterd's small
// pool was busy failed every remaining app at once with an already-cancelled
// context (61 apps at 07:02:52 UTC on production-us, hunt #10, H10-133) and
// left their gauges stale.
const apiReachabilityStepTimeout = 10 * time.Second

// APIReachabilitySweepParams is the param bundle for
// APIReachabilitySweepLoop. Store is required; Log and Ops are
// nil-coerced to the package-level defaults.
//
// ReachWindow is the look-back window for the
// WasInvokedSuccessfullySince EXISTS scan. Zero falls back to the
// gauge's natural reachability window (5 min, the same window the
// gauge encodes). Caller-supplied windows smaller than Interval
// risk the gauge flipping 1.0 → 0.0 within the same sweep; caller-
// supplied windows larger than Interval risk the gauge lagging
// actual reachability. The default is the documented "alert preset
// api_down" contract.
type APIReachabilitySweepParams struct {
	Store       state.Store
	Log         *slog.Logger
	Ops         *wire.OpsMetrics
	Interval    time.Duration
	ReachWindow time.Duration
	Now         func() time.Time // injectable for tests; nil falls back to time.Now
}

// APIReachabilitySweepLoop walks every (account_id, app_id) pair
// every Interval, calls
// state.WasInvokedSuccessfullySince(ctx, accountID, appID, now-ReachWindow)
// for each, and stamps meterd_api_reachable{account_id, app_id} to
// 1.0 (recent successful invocation) or 0.0 (none in the window).
// The first tick runs immediately so a freshly-restarted meterd
// stamps the gauge without waiting Interval.
//
// Pair walk is best-effort: a transient ListAllAccounts or ListApps
// error logs and skips the rest of the walk — the gauge keeps its
// previous values until the next tick. A persistent failure shows
// up as a stale "1.0" gauge that the alert evaluator's
// degraded-source branch (mirroring pkg/alerts/evaluator.go:505)
// catches.
//
// Returns when ctx is cancelled.
func APIReachabilitySweepLoop(ctx context.Context, p APIReachabilitySweepParams) {
	if p.Store == nil {
		return
	}
	if p.Interval <= 0 {
		p.Interval = meter.DefaultAPIReachabilitySweepInterval
	}
	if p.ReachWindow <= 0 {
		p.ReachWindow = 5 * time.Minute
	}
	if p.Log == nil {
		p.Log = slog.Default()
	}
	if p.Ops == nil {
		p.Ops = wire.NewOpsMetrics("meterd")
	}
	nowFn := p.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	do := func() {
		gauge := p.Ops.MeterdAPIReachable()
		if gauge == nil {
			return
		}
		since := nowFn().Add(-p.ReachWindow)
		stamped, failed := sweepAPIReachability(ctx, p.Store, gauge, since, apiReachabilityStepTimeout, p.Log)
		p.Log.Info("meterd: api reachability sweep tick ok", "stamped", stamped, "failed", failed)
	}
	do()
	t := time.NewTicker(p.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			do()
		}
	}
}

// sweepAPIReachability stamps the gauge for every (account, app) pair. Each
// store read gets its own deadline, so one slow read fails only its own app.
func sweepAPIReachability(ctx context.Context, store apiReachabilityStore, gauge *prometheus.GaugeVec, since time.Time, step time.Duration, log *slog.Logger) (stamped, failed int) {
	listCtx, cancel := context.WithTimeout(ctx, step)
	accounts, err := store.ListAllAccounts(listCtx)
	cancel()
	if err != nil {
		log.Warn("meterd: list accounts (api reachability sweep) failed", "err", err)
		return 0, 0
	}
	for _, a := range accounts {
		if ctx.Err() != nil {
			return stamped, failed
		}
		appsCtx, cancel := context.WithTimeout(ctx, step)
		apps, listErr := store.ListApps(appsCtx, a.ID)
		cancel()
		if listErr != nil {
			log.Warn("meterd: list apps (api reachability sweep) failed", "account_id", a.ID, "err", listErr)
			continue
		}
		for _, app := range apps {
			if ctx.Err() != nil {
				return stamped, failed
			}
			readCtx, cancel := context.WithTimeout(ctx, step)
			ok, invErr := store.WasInvokedSuccessfullySince(readCtx, a.ID, app.ID, since)
			cancel()
			if invErr != nil {
				log.Warn("meterd: was-invoked-successfully-since failed", "account_id", a.ID, "app_id", app.ID, "err", invErr)
				failed++
				continue
			}
			value := 0.0
			if ok {
				value = 1.0
			}
			gauge.WithLabelValues(a.ID, app.ID).Set(value)
			stamped++
		}
	}
	return stamped, failed
}
