// debug_regression_cron.go — ADR-127 PR-B production debugger
// regression detector. Mirrors cmd/apid/dns_poller.go::startDNSPoller
// (first-pass-immediate + ticker + ctx-cancel). The cron ticks
// every 5 minutes; on each pass it walks every app that has shipped
// traffic in the window, finds the prior deployment to use as
// baseline, and upserts a debug_regression_observations row for
// every (deployment, route) where the current p95 exceeds the
// baseline by ≥20% and the affected request count is at least
// debugRegressionMinAffected (default 100 — PR-B chosen value).
//
// Why 5 minutes: same cadence as the gatewayd-internal publisher
// (pkg/gateway/request_telemetry_publisher.go ships every 5s; the
// cron is per-app aggregation, not per-row). The cron isn't on
// the customer hot path — a missed tick just delays regression
// detection by ≤5 minutes, the dashboard's "since=10m" filter
// already absorbs that.
//
// Why apid (not meterd or schedd): CLAUDE.md line 71 — schedd is
// the ONLY writer to `instances`, apid is the sole writer to
// customer-intent tables, vmmd is the sole firecracker/jailer
// owner. debug_regression_observations is a customer-intent
// extension of apps (the regression banner surfaces in the
// customer dashboard), so apid owns the writer.
//
// Dark-launch posture (mirrors the dns_poller doctor branch):
// when the operator sets FAAS_REQUEST_TELEMETRY_ENABLED=false,
// every per-tick call to runRegressionOnce short-circuits and
// bumps debugRegressionSkippedFlagDisabled so a stalled-loop
// alert correlates with an explicit opt-out.

package apidserver

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/debugger"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// debugRegressionTickInterval is the cadence of the regression
// detector. Mirrors dns_poller's verifyInterval (30s) but at a
// slower rate — the regression detector walks every active app's
// recent telemetry, which is more expensive than a DNS probe.
const debugRegressionTickInterval = 5 * time.Minute

// debugRegressionWindow is the lookback for the per-app
// deployment enumeration. Long enough that a 1-req/s app
// accumulates enough rows for a stable p95 (≥100 rows); short
// enough that a 1-month-old deployment isn't still being
// compared to itself.
const debugRegressionWindow = 24 * time.Hour

// debugRegressionMinAffected is the floor on rows exceeding the
// baseline threshold before the regression detector fires. PR-B
// chosen value: a single noisy request can't fire a banner — the
// signal needs ≥100 affected rows in the window.
const debugRegressionMinAffected = 100

// debugRegressionFactor is the per-route p95 inflation factor
// that fires a regression row. PR-B chosen value: 1.20x (20%
// slower than baseline).
const debugRegressionFactor = 1.20

// debugRegressionMaxApps is the per-tick cap on apps walked.
// Hard ceiling so a fleet-wide DebugTelemetryEnabled flip
// doesn't pile up unbounded queries in a single 5m window.
const debugRegressionMaxApps = 1000

// debugRegressionMaxRoutes is the per-app ceiling on baseline
// routes walked. Caps the worst-case per-app round-trips.
const debugRegressionMaxRoutes = 200

// debugRegressionDrilldownLimit is the LIMIT on the per-route
// affected-count drilldown. Caps the worst-case scan cost per
// regressed route.
const debugRegressionDrilldownLimit = 10000

// debugRegressionBaselineWindow is the rolling window used for
// the baseline p95 calculation on the prior deployment. 30
// minutes is enough for ≥100 rows at 1 req/s; short enough that
// the prior deployment's traffic is the only signal in the
// window.
const debugRegressionBaselineWindow = 30 * time.Minute

// startDebugRegressionCron runs the regression detection loop
// until ctx is cancelled. Mirrors startDNSPoller's structure —
// first-pass-immediate + ticker + ctx-cancel. The cron is
// best-effort: per-app errors are logged and skipped so a single
// bad app doesn't abort the whole pass.
func startDebugRegressionCron(ctx context.Context, s *server, log *slog.Logger, getenv func(string) string) {
	if s.store == nil {
		return
	}
	enabled := func() bool {
		return getenv("FAAS_REQUEST_TELEMETRY_ENABLED") != "false"
	}
	go func() {
		t := time.NewTicker(debugRegressionTickInterval)
		defer t.Stop()
		if enabled() {
			s.runRegressionOnce(ctx, log)
		} else {
			s.emitRegressionSkip(log)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if enabled() {
					s.runRegressionOnce(ctx, log)
				} else {
					s.emitRegressionSkip(log)
				}
			}
		}
	}()
}

// runRegressionOnce is the per-tick regression detection pass.
// Walks every app that has shipped traffic in the window, finds
// the prior deployment to use as baseline, and upserts a row for
// every (deployment, route) regression detected. Per-app errors
// are logged + skipped; the loop never aborts on a bad app.
func (s *server) runRegressionOnce(ctx context.Context, log *slog.Logger) {
	// Capture the pass-start timestamp at cron entry so the
	// oldest-pass gauge measures wall-clock now() at emit time,
	// not a frozen value from earlier in the pass.
	lastPassAt := time.Now()
	appIDs, err := s.store.ListAppsWithRecentTelemetry(ctx, pgtype.Interval{
		Microseconds: int64(debugRegressionWindow / time.Microsecond),
		Valid:        true,
	})
	if err != nil {
		log.Warn("regression_cron: list apps failed", "err", err)
		s.emitRegressionOldestPassGauge(ctx, log, lastPassAt)
		return
	}
	if len(appIDs) > debugRegressionMaxApps {
		log.Warn("regression_cron: app cap hit",
			"observed", len(appIDs),
			"cap", debugRegressionMaxApps)
		appIDs = appIDs[:debugRegressionMaxApps]
	}
	for _, appID := range appIDs {
		appCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := s.runRegressionForApp(appCtx, log, appID); err != nil {
			log.Warn("regression_cron: per-app pass failed",
				"app", appID.String(), "err", err)
		}
		cancel()
	}
	resolved, err := s.store.ResolveStaleRegressionObservations(ctx, pgtype.Interval{
		Microseconds: int64(debugRegressionWindow / time.Microsecond),
		Valid:        true,
	})
	if err != nil {
		log.Warn("regression_cron: resolve stale observations failed", "err", err)
	} else {
		for _, row := range resolved {
			s.notifyDebugRegressionChanged(ctx, uuidFromPg(row.AppID), debugRegressionObservationToItem(row))
		}
	}
	// Always refresh the gauge — even if the per-app pass failed,
	// the gauge still reflects the wall-clock staleness of the
	// cron loop so a stalled-loop alert fires when nothing else
	// does.
	s.emitRegressionOldestPassGauge(ctx, log, lastPassAt)
}

// runRegressionForApp runs the per-app detector. Returns an error
// for the per-app envelope so the caller logs + skips without
// aborting the cron.
//
// Detection shape:
//  1. ListDeploymentsForCompare(LIMIT 2) → current + prior.
//  2. If only one deployment exists, skip — there's no baseline.
//  3. Baseline p95 per route on the prior deployment
//     (RequestTelemetryBaselineP95ByRoute over a 30-min window
//     ending at the boundary between the two deployments).
//  4. Current p95 per route on the latest deployment (same 30-min
//     window).
//  5. For each route where current.p95 > baseline.p95 * 1.20:
//     a. count affected rows (current deployment's rows in window
//     where latency_ms > baseline.p95 * 1.20).
//     b. If affected >= debugRegressionMinAffected: upsert
//     regression row with regression_factor = current.p95 /
//     baseline.p95.
func (s *server) runRegressionForApp(ctx context.Context, log *slog.Logger, appID pgtype.UUID) error {
	deps, err := s.store.ListDeploymentsForCompare(ctx, sqlc.ListDeploymentsForCompareParams{
		AppID: appID,
		Column2: pgtype.Interval{
			Microseconds: int64(debugRegressionWindow / time.Microsecond),
			Valid:        true,
		},
		Limit: 2,
	})
	if err != nil {
		return fmt.Errorf("list deployments: %w", err)
	}
	if len(deps) < 2 {
		// No prior deployment → can't compute baseline. PR-B
		// decided this is an "no signal" outcome, not an error.
		return nil
	}
	cur := deps[0]
	prev := deps[1]

	// Fixed 30-min baseline window ending at the deployment
	// boundary. The boundary itself is approximated as
	// now() - baselineWindow (the cron runs at fixed cadence; the
	// gap between baseline and current windows is small relative
	// to the 5m cron tick).
	now := time.Now()
	curStart := now.Add(-debugRegressionBaselineWindow)
	curEnd := now
	prevStart := curStart.Add(-debugRegressionBaselineWindow)
	prevEnd := curStart

	// Compute baseline p95 per route on the prior deployment.
	baseline, err := s.store.RequestTelemetryBaselineP95ByRoute(ctx, sqlc.RequestTelemetryBaselineP95ByRouteParams{
		AppID:        appID,
		DeploymentID: prev.DeploymentID,
		ReceivedAt:   pgtype.Timestamptz{Time: prevStart, Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: prevEnd, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("baseline p95: %w", err)
	}
	baselineByRoute := make(map[string]int32, len(baseline))
	for _, row := range baseline {
		baselineByRoute[row.Route] = row.P95Ms
	}
	if len(baselineByRoute) > debugRegressionMaxRoutes {
		// Cap the map so a 10k-route app doesn't blow up the
		// per-app pass. Sort the keys for determinism — Go's map
		// iteration order is randomized, so without sorting the
		// chosen cap-of-200 routes would change every tick and the
		// regression banner would flake on/off across passes.
		sortedRoutes := make([]string, 0, len(baselineByRoute))
		for k := range baselineByRoute {
			sortedRoutes = append(sortedRoutes, k)
		}
		sort.Strings(sortedRoutes)
		kept := make(map[string]int32, debugRegressionMaxRoutes)
		for _, k := range sortedRoutes[:debugRegressionMaxRoutes] {
			kept[k] = baselineByRoute[k]
		}
		baselineByRoute = kept
	}

	// Compute current p95 per route on the current deployment.
	current, err := s.store.RequestTelemetryBaselineP95ByRoute(ctx, sqlc.RequestTelemetryBaselineP95ByRouteParams{
		AppID:        appID,
		DeploymentID: cur.DeploymentID,
		ReceivedAt:   pgtype.Timestamptz{Time: curStart, Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: curEnd, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("current p95: %w", err)
	}
	currentP95ByRoute := make(map[string]int32, len(current))
	for _, row := range current {
		currentP95ByRoute[row.Route] = row.P95Ms
	}
	// Like the p95 reads, a failed error-rate read skips the app for this
	// pass: recording latency alone would rewrite an observation's kind and
	// zero its stored error rates.
	baselineErrors, err := s.regressionErrorsByRoute(ctx, appID, prev.DeploymentID, prevStart, prevEnd)
	if err != nil {
		return fmt.Errorf("baseline error rates: %w", err)
	}
	currentErrors, err := s.regressionErrorsByRoute(ctx, appID, cur.DeploymentID, curStart, curEnd)
	if err != nil {
		return fmt.Errorf("current error rates: %w", err)
	}

	pass := &debugRegressionPass{
		server: s, log: log, appID: appID, cur: cur, prev: prev,
		curStart: curStart, curEnd: curEnd, prevStart: prevStart,
	}
	for _, route := range debugRegressionCandidateRoutes(baselineByRoute, currentP95ByRoute, baselineErrors, currentErrors) {
		pass.evaluateRoute(ctx, route, baselineByRoute[route], currentP95ByRoute[route], baselineErrors[route], currentErrors[route])
	}
	return nil
}

// debugRegressionPass carries one app's detector pass. The latency
// drilldown and the retained spans are read at most once per pass, and only
// when a route needs them.
type debugRegressionPass struct {
	server              *server
	log                 *slog.Logger
	appID               pgtype.UUID
	cur, prev           sqlc.ListDeploymentsForCompareRow
	curStart, curEnd    time.Time
	prevStart           time.Time
	drilldown           []sqlc.RequestTelemetryByDeploymentRow
	drilldownLoaded     bool
	dependencyRows      []sqlc.ListRequestTelemetryDependencySpansRow
	dependencyRowsReady bool
}

// evaluateRoute decides whether one route regressed on latency, on errors or
// on both, and records a single observation for it.
func (p *debugRegressionPass) evaluateRoute(ctx context.Context, route string, baseP95, curP95 int32, baseErrors, curErrors debugger.RouteErrors) {
	latency, affected := false, int32(0)
	if baseP95 > 0 {
		threshold := int32(float64(baseP95) * debugRegressionFactor)
		if curP95 > threshold {
			rows, ok := p.latencyDrilldown(ctx)
			if !ok {
				// Without the drilldown the latency signal is unknown; skip
				// the route rather than record errors alone over it.
				return
			}
			affected = countAffectedRequests(rows, route, threshold)
			latency = affected >= debugRegressionMinAffected
		}
	}
	errorsRegressed := debugger.ErrorRateRegressed(baseErrors, curErrors)
	kind := debugger.RegressionKind(latency, errorsRegressed)
	if kind == "" {
		return
	}
	if !latency {
		// An error-only observation reports its failed requests as affected.
		affected = saturateInt32(curErrors.Errors)
	}
	params, err := debugRegressionParams(p.appID, p.cur.DeploymentID, route, kind, baseP95, curP95, affected, baseErrors, curErrors)
	if err != nil {
		p.log.Warn("regression_cron: numeric scan failed", "app", p.appID.String(), "route", route, "err", err)
		return
	}
	// Column8 is the suspected_dependency jsonb (nil keeps the stored one).
	// An error-only regression names a failing dependency before a slow one.
	params.Column8 = encodeSuspectedDependency(suspectedDependencyFor(
		buildDebugDependencyComparisonBetween(p.dependencySpans(ctx), uuidFromPg(p.cur.DeploymentID), uuidFromPg(p.prev.DeploymentID), route, 0), !latency))
	p.server.recordRegressionObservation(ctx, p.log, params)
}

// debugRegressionParams builds the observation row for a regressed route.
// regression_factor is the latency inflation and is CHECKed ≥ 1.0, so an
// error-only regression without a slower p95 stores 1.00.
func debugRegressionParams(appID, deploymentID pgtype.UUID, route, kind string, baseP95, curP95, affected int32, baseErrors, curErrors debugger.RouteErrors) (sqlc.UpsertRegressionObservationParams, error) {
	factor := 1.0
	if baseP95 > 0 && curP95 > 0 {
		factor = float64(curP95) / float64(baseP95)
	}
	if factor < 1 {
		factor = 1
	}
	params := sqlc.UpsertRegressionObservationParams{
		AppID: appID, DeploymentID: deploymentID, Route: route,
		P95Ms: curP95, P95BaseMs: baseP95, AffectedCount: affected,
		Kind:       kind,
		ErrorCount: saturateInt32(curErrors.Errors),
	}
	var err error
	if params.RegressionFactor, err = numericFromFloat2(factor); err != nil {
		return params, err
	}
	if params.ErrorRatePct, err = numericFromFloat2(curErrors.RatePct()); err != nil {
		return params, err
	}
	params.ErrorRateBasePct, err = numericFromFloat2(baseErrors.RatePct())
	return params, err
}

func (p *debugRegressionPass) latencyDrilldown(ctx context.Context) ([]sqlc.RequestTelemetryByDeploymentRow, bool) {
	if p.drilldownLoaded {
		return p.drilldown, p.drilldown != nil
	}
	p.drilldownLoaded = true
	// Affected requests: the current deployment's rows in the window whose
	// latency exceeds a route's threshold, capped at 10k rows; the caller
	// filters by route.
	rows, err := p.server.store.RequestTelemetryByDeployment(ctx, sqlc.RequestTelemetryByDeploymentParams{
		AppID:        p.appID,
		DeploymentID: p.cur.DeploymentID,
		ReceivedAt:   pgtype.Timestamptz{Time: p.curStart, Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: p.curEnd, Valid: true},
		Limit:        debugRegressionDrilldownLimit,
	})
	if err != nil {
		p.log.Warn("regression_cron: drilldown failed",
			"app", p.appID.String(), "deployment", p.cur.DeploymentID.String(), "err", err)
		return nil, false
	}
	if rows == nil {
		rows = []sqlc.RequestTelemetryByDeploymentRow{}
	}
	p.drilldown = rows
	return rows, true
}

func (p *debugRegressionPass) dependencySpans(ctx context.Context) []sqlc.ListRequestTelemetryDependencySpansRow {
	if !p.dependencyRowsReady {
		p.dependencyRows = p.server.regressionDependencyRows(ctx, p.log, p.appID, p.prevStart, p.curEnd)
		p.dependencyRowsReady = true
	}
	return p.dependencyRows
}

// recordRegressionObservation upserts one observation, re-reads it so the
// event reflects a preserved acknowledgement or dismissal, and publishes it.
func (s *server) recordRegressionObservation(ctx context.Context, log *slog.Logger, params sqlc.UpsertRegressionObservationParams) {
	if err := s.store.UpsertRegressionObservation(ctx, params); err != nil {
		log.Warn("regression_cron: upsert failed",
			"app", params.AppID.String(), "deployment", params.DeploymentID.String(), "route", params.Route, "err", err)
		return
	}
	row, err := s.store.GetRegressionObservation(ctx, sqlc.GetRegressionObservationParams{
		AppID: params.AppID, DeploymentID: params.DeploymentID, Route: params.Route,
	})
	if err != nil {
		log.Warn("regression_cron: read after upsert failed",
			"app", params.AppID.String(), "deployment", params.DeploymentID.String(), "route", params.Route, "err", err)
		return
	}
	s.notifyDebugRegressionChanged(ctx, uuidFromPg(params.AppID), debugRegressionObservationToItem(row))
	// ADR-127 Debugger UX v1: regression persisted to
	// debug_regression_observations — bump
	// apid_debug_regression_detected_total so the
	// FaasDebugRegressionDetected page-tier alert can fire.
	// A persistent regression re-fires every 5m (the cron
	// upserts the same PRIMARY KEY); PagerDuty dedupes the
	// sustained-fire alert into one ongoing incident.
	if c := s.ops.DebugRegressionDetected(); c != nil {
		c.Inc()
	}
}

// regressionErrorsByRoute reads per-route request and 5xx counts for one
// deployment window.
func (s *server) regressionErrorsByRoute(ctx context.Context, appID, deploymentID pgtype.UUID, from, until time.Time) (map[string]debugger.RouteErrors, error) {
	rows, err := s.store.RequestTelemetryErrorsByRoute(ctx, sqlc.RequestTelemetryErrorsByRouteParams{
		AppID:        appID,
		DeploymentID: deploymentID,
		ReceivedAt:   pgtype.Timestamptz{Time: from, Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: until, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]debugger.RouteErrors, len(rows))
	for _, row := range rows {
		out[row.Route] = debugger.RouteErrors{Requests: row.Requests, Errors: row.ErrorRequests}
	}
	return out, nil
}

// debugRegressionCandidateRoutes returns the current routes that have a
// baseline for at least one signal, sorted and capped at
// debugRegressionMaxRoutes so the walked set is stable across passes.
func debugRegressionCandidateRoutes(baselineP95, currentP95 map[string]int32, baselineErrors, currentErrors map[string]debugger.RouteErrors) []string {
	seen := make(map[string]bool, len(currentP95)+len(currentErrors))
	routes := make([]string, 0, len(currentP95)+len(currentErrors))
	add := func(route string) {
		if seen[route] {
			return
		}
		_, hasP95 := baselineP95[route]
		_, hasErrors := baselineErrors[route]
		if hasP95 || hasErrors {
			seen[route] = true
			routes = append(routes, route)
		}
	}
	for route := range currentP95 {
		add(route)
	}
	for route := range currentErrors {
		add(route)
	}
	sort.Strings(routes)
	if len(routes) > debugRegressionMaxRoutes {
		routes = routes[:debugRegressionMaxRoutes]
	}
	return routes
}

func numericFromFloat2(value float64) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	err := n.Scan(fmt.Sprintf("%.2f", value))
	return n, err
}

func saturateInt32(value int64) int32 {
	const maxInt32 = int64(1<<31 - 1)
	switch {
	case value > maxInt32:
		return int32(maxInt32)
	case value < 0:
		return 0
	default:
		return int32(value)
	}
}

// debugRegressionDependencyMaxRows bounds the span read made when an app has
// at least one regressed route in a detector pass.
const debugRegressionDependencyMaxRows = 1000

// regressionDependencyRows loads retained spans for the detector's baseline
// and current windows. Naming a suspect is enrichment: on any failure the
// regression is still recorded, keeping its previously stored suspect.
func (s *server) regressionDependencyRows(ctx context.Context, log *slog.Logger, appID pgtype.UUID, from, until time.Time) []sqlc.ListRequestTelemetryDependencySpansRow {
	app, err := s.store.AppByID(ctx, uuidFromPg(appID))
	if err != nil {
		log.Warn("regression_cron: suspected dependency app lookup failed", "app", appID.String(), "err", err)
		return nil
	}
	rows, err := s.store.ListRequestTelemetryDependencySpans(ctx, sqlc.ListRequestTelemetryDependencySpansParams{
		AppID:        appID,
		AccountID:    stringToPgUUID(app.AccountID),
		ReceivedAt:   pgtype.Timestamptz{Time: from, Valid: true},
		ReceivedAt_2: pgtype.Timestamptz{Time: until, Valid: true},
		Limit:        debugRegressionDependencyMaxRows,
	})
	if err != nil {
		log.Warn("regression_cron: suspected dependency spans unavailable", "app", appID.String(), "err", err)
		return nil
	}
	return rows
}

// countAffectedRequests returns the number of requests represented by
// drilldown rows for route whose latency exceeds threshold. The gateway
// publisher collapses identical requests into rows with a Count weight, so
// counting rows would under-report the impact of a regression. The result is
// saturated at the int32 range used by debug_regression_observations.
func countAffectedRequests(rows []sqlc.RequestTelemetryByDeploymentRow, route string, threshold int32) int32 {
	const maxAffectedCount = int64(1<<31 - 1)
	var affected int64
	for _, row := range rows {
		// Drilldown is per-deployment, not per-route — the indexed query
		// stays reusable for the compare endpoint and we filter here so
		// the persisted badge attributes the count to the route.
		if row.Route != route || row.LatencyMs <= threshold {
			continue
		}
		count := int64(row.Count)
		if count < 1 {
			// Legacy rows predate the CHECK constraint; preserve the old
			// one-row semantics for those records.
			count = 1
		}
		if affected > maxAffectedCount-count {
			return int32(maxAffectedCount)
		}
		affected += count
	}
	return int32(affected)
}

// emitRegressionOldestPassGauge (ADR-127 PR-B) refreshes the
// apid_debug_regression_oldest_pass_seconds gauge after the cron
// pass completes. The metric measures wall-clock seconds since
// the most recent cron pass — not "since last detected
// regression", which made the value climb unboundedly when the
// cron was healthy but silent. The doctor loop's
// emitDoctorOldestObservationGauge uses the data-staleness
// semantic; the regression cron uses the pass-staleness semantic
// because regression detection is a positive observation that
// can legitimately go quiet for hours (a stable production app)
// — pass-staleness is the operator's "is the loop alive" signal,
// data-staleness is misleadingly noisy.
//
// The caller passes lastPassAt (captured at cron entry); we
// translate to seconds at emit time so the gauge reflects
// wall-clock now, not a stale captured moment. Negative deltas
// are clamped to 0 (clock-skew defense).
func (s *server) emitRegressionOldestPassGauge(ctx context.Context, log *slog.Logger, lastPassAt time.Time) {
	if s.ops == nil {
		return
	}
	gauge := s.ops.DebugRegressionOldestPassSeconds()
	if gauge == nil {
		return
	}
	if lastPassAt.IsZero() {
		gauge.Set(0)
		return
	}
	age := time.Since(lastPassAt).Seconds()
	if age < 0 {
		age = 0
	}
	gauge.Set(age)
}

// emitRegressionSkip (ADR-127 PR-B) bumps the
// apid_debug_regression_skipped_flag_disabled_total counter so an
// operator can correlate a stalled-loop alert with an explicit
// FAAS_REQUEST_TELEMETRY_ENABLED=false opt-out. Called once per
// tick when the kill-switch is off. Best-effort — nil-safe.
func (s *server) emitRegressionSkip(log *slog.Logger) {
	if s.ops == nil {
		return
	}
	if c := s.ops.DebugRegressionSkippedFlagDisabled(); c != nil {
		c.Inc()
	}
	log.Debug("regression pass skipped — FAAS_REQUEST_TELEMETRY_ENABLED off")
}
