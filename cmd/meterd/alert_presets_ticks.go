// cmd/meterd/alert_presets_ticks.go — ADR-123 alert-preset
// signal-feeding goroutines (issue #1233).
//
// Two free-function Loop variants sit alongside the existing
// RollupLoop / StorageRollupLoop / RetentionLoop pair in this
// package: CertExpiryRefresherLoop (meterd-owned, per
// CLAUDE.md ownership rule) feeds
// apid_tenant_surface_cert_expiry_seconds{account_id, app_id,
// hostname} (backing the alert preset cert_expiring_14d), and
// AccountSpendAggregatorLoop feeds meterd_account_spend_eur
// {account_id} (backing the alert preset spend_eur_20).
//
// Both goroutines are ctx-cancelled like the others — cmd/meterd
// wires them with the loop's ctx so a daemon shutdown drains
// cleanly. Neither is part of the 5-tick sampler loop because
// they read a different (wider) signal set and run on a much
// slower cadence: cert-expiry is a 1-hour sweep (the renewer
// runs daily; one hour keeps the gauge fresh within a 5 % error
// band even if a renewal bot slips), and account-spend is a
// 5-minute sweep (the underlying account_spend_snapshot row is
// updated by the meterd rollup every 5 min, so refreshing the
// gauge faster than that is wasted work).
//
// The api_up preset uses the same meterd tick interval as
// AccountSpendAggregatorLoop (5 min) but is computed inline in
// the alert evaluator from pkg/appmetrics.Fetch +
// state.WasInvokedSuccessfullySince, not a separate goroutine —
// see pkg/alerts/evaluator.go::observe for the case.
//
// Memory model: no per-account state is held in either goroutine;
// the gauge label set is recreated on every tick (Prometheus
// handles Add-on-existing-label no-op), and the read set is
// streamed from the state store.
package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Default interval constants live in pkg/meter/config.go so the
// Config.Defaults() path and the standalone nil-default path of
// CertExpiryRefresherLoop / AccountSpendAggregatorLoop share one
// source of truth. The cmd/meterd wire uses Config.Defaults()
// values; the standalone path (called from tests with explicit
// params) falls back to those same constants when Interval <= 0.

// CertExpiryRefresherParams is the param bundle for
// CertExpiryRefresherLoop. The Store is required; Log and Ops
// are nil-coerced.
type CertExpiryRefresherParams struct {
	Store    state.Store
	Log      *slog.Logger
	Ops      *wire.OpsMetrics
	Interval time.Duration
	Now      func() time.Time // injectable for tests; nil falls back to time.Now
}

// CertExpiryRefresherLoop walks every row in
// meterd_tenant_surface_cert_expiry_state every interval, computes
// the remaining-seconds-from-now for each, and stamps the
// apid_tenant_surface_cert_expiry_seconds{account_id, app_id,
// hostname} gauge. The metric name keeps the legacy apid_ prefix
// for backward-compat with deployed alert rules; the owning
// table moved to meterd_ in migrations/00366 per the CLAUDE.md
// ownership rule. Stale rows (last_refreshed_at older than
// 2× interval) are reset to 0 so the alert evaluator's
// degraded-source branch (mirroring pkg/alerts/evaluator.go:505)
// can skip them. Returns when ctx is cancelled.
//
// Pattern mirrors pkg/meter/rollup.go::RollupLoop: free-function
// goroutine, ctx-cancelled, nil-coerced log + ops.
func CertExpiryRefresherLoop(ctx context.Context, p CertExpiryRefresherParams) {
	if p.Store == nil {
		return
	}
	if p.Interval <= 0 {
		p.Interval = meter.DefaultCertExpiryRefresherInterval
	}
	if p.Log == nil {
		p.Log = slog.Default()
	}
	nowFn := p.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	if p.Ops == nil {
		p.Ops = wire.NewOpsMetrics("meterd")
	}

	staleCutoff := 2 * p.Interval
	do := func() {
		walkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		n, err := p.Store.RefreshCertExpiryStates(walkCtx)
		result := "ok"
		if err != nil {
			result = "error"
			p.Log.Warn("meterd: cert-expiry refresher walk failed", "err", err)
		} else {
			p.Log.Info("meterd: cert-expiry refresher walk ok", "rows", n)
		}
		p.Ops.ApidTenantSurfaceCertExpiryRefresherWalkCompleteTotal(result)()
		// Stamp the gauge: read back the rows we just refreshed.
		// The read path is best-effort — a Prometheus failure
		// here does NOT flip any health gate; the rows themselves
		// are the source of truth for the alert evaluator.
		rows, listErr := p.Store.ListCertExpiryStateForWalker(walkCtx, staleCutoff)
		if listErr != nil {
			p.Log.Warn("meterd: list cert-expiry state (gauge stamp) failed", "err", listErr)
			return
		}
		now := nowFn()
		gauge := p.Ops.ApidTenantSurfaceCertExpirySeconds()
		if gauge == nil {
			return
		}
		for _, r := range rows {
			if r.LastObservedCertNotAfter == nil {
				gauge.WithLabelValues(r.AccountID, r.AppID, r.Hostname).Set(0)
				continue
			}
			remaining := int64(r.LastObservedCertNotAfter.Sub(now).Seconds())
			if remaining < 0 {
				remaining = 0
			}
			gauge.WithLabelValues(r.AccountID, r.AppID, r.Hostname).Set(float64(remaining))
		}
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

// AccountSpendAggregatorParams is the param bundle for
// AccountSpendAggregatorLoop. The Store is required; Log and
// Ops are nil-coerced.
type AccountSpendAggregatorParams struct {
	Store    state.Store
	Log      *slog.Logger
	Ops      *wire.OpsMetrics
	Interval time.Duration
}

// AccountSpendAggregatorLoop walks every account every interval,
// reads MTDSpendEurCents(accountID), and stamps the
// meterd_account_spend_eur{account_id} gauge. EUR cents are
// converted to whole-EUR floats so the alert preset's
// `threshold = 20` comparison (gauge > 20) reads naturally on the
// operator's dashboard without an in-PromQL /100. Returns when
// ctx is cancelled.
//
// Pattern mirrors CertExpiryRefresherLoop (above) and
// pkg/meter/rollup.go::RollupLoop.
func AccountSpendAggregatorLoop(ctx context.Context, p AccountSpendAggregatorParams) {
	if p.Store == nil {
		return
	}
	if p.Interval <= 0 {
		p.Interval = meter.DefaultAccountSpendAggregatorInterval
	}
	if p.Log == nil {
		p.Log = slog.Default()
	}
	if p.Ops == nil {
		p.Ops = wire.NewOpsMetrics("meterd")
	}
	do := func() {
		walkCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		accounts, err := p.Store.ListAllAccounts(walkCtx)
		if err != nil {
			p.Log.Warn("meterd: list accounts (spend aggregator) failed", "err", err)
			return
		}
		gauge := p.Ops.MeterdAccountSpendEur()
		if gauge == nil {
			return
		}
		for _, a := range accounts {
			cents, spendErr := p.Store.MTDSpendEurCents(walkCtx, a.ID)
			if spendErr != nil {
				p.Log.Warn("meterd: MTD spend read failed", "account_id", a.ID, "err", spendErr)
				continue
			}
			gauge.WithLabelValues(a.ID).Set(float64(cents) / 100.0)
		}
		p.Log.Info("meterd: account-spend aggregator tick ok", "accounts", len(accounts))
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
