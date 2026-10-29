package routeinsights

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/routeawake"
	"github.com/onebox-faas/faas/pkg/routeadvisor"
	"github.com/onebox-faas/faas/pkg/safetext"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// firstRunDelay keeps the first evaluation out of apid's start-up.
const firstRunDelay = 2 * time.Minute

// maxMessageRunes matches the route_insights message CHECK.
const maxMessageRunes = 1000

// insightMethods are the methods route_insights accepts.
var insightMethods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}

// Store is the PgStore surface the evaluator reads and writes.
type Store interface {
	routeawake.Store
	AccountByID(ctx context.Context, id string) (state.Account, error)
	AppByID(ctx context.Context, id string) (state.App, error)
	ListEdgeRulesForApp(ctx context.Context, appID string) ([]state.EdgeRule, error)
	RouteAdviceRouteStats(context.Context, sqlc.RouteAdviceRouteStatsParams) ([]sqlc.RouteAdviceRouteStatsRow, error)
	RouteInsightCandidateApps(context.Context, sqlc.RouteInsightCandidateAppsParams) ([]sqlc.RouteInsightCandidateAppsRow, error)
	ClaimRouteInsightEvaluation(context.Context, sqlc.ClaimRouteInsightEvaluationParams) (int64, error)
	UpsertRouteInsight(context.Context, sqlc.UpsertRouteInsightParams) error
	ResolveStaleRouteInsightsBatch(context.Context, sqlc.ResolveStaleRouteInsightsBatchParams) (int64, error)
}

// Evaluator periodically records each running app's route insights.
type Evaluator struct {
	Store    Store
	Log      *slog.Logger
	Interval time.Duration
	Now      func() time.Time
	// Metrics is optional; nil records nothing.
	Metrics *Metrics
	// Released decides the route insights capability for an app
	// (ADR-20261028110044). An unreleased app is skipped after its claim, so
	// the rotation still moves on; nil releases every app.
	Released func(state.Account, state.App) bool
}

// Stats summarizes one evaluation. Taken counts candidates another
// evaluation claimed first.
type Stats struct {
	Apps     int
	Skipped  int
	Failed   int
	Taken    int
	Observed int
	Resolved int64
}

// Run evaluates every Interval (default RouteInsightInterval) until ctx ends.
func (e *Evaluator) Run(ctx context.Context) error {
	interval := e.Interval
	if interval <= 0 {
		interval = api.RouteInsightInterval
	}
	timer := time.NewTimer(firstRunDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			stats, err := e.RunOnce(ctx)
			if err != nil {
				e.log().Warn("route insights evaluation failed", "err", err)
			} else {
				e.log().Info("route insights evaluated", "apps", stats.Apps, "skipped", stats.Skipped,
					"failed", stats.Failed, "taken", stats.Taken, "observed", stats.Observed, "resolved", stats.Resolved)
			}
			timer.Reset(interval)
		}
	}
}

// RunOnce evaluates every app on a plan with request telemetry that ran in
// the last RouteInsightWindow and is due, least recently evaluated first,
// then resolves insights no evaluation has observed lately. Each app is
// claimed before it is evaluated, so replicas share the work, and an app
// evaluated within half an interval is not due (ADR-20261024154324). One
// app's failure is logged and does not stop the others.
func (e *Evaluator) RunOnce(ctx context.Context) (Stats, error) {
	started := e.now()
	stats, err := e.evaluateDue(ctx, started)
	if err == nil {
		stats.Resolved, err = e.Store.ResolveStaleRouteInsightsBatch(ctx, sqlc.ResolveStaleRouteInsightsBatchParams{
			ResolvedAt: timestamptz(started), StaleBefore: timestamptz(started.Add(-api.RouteInsightResolveAfter)),
			RowLimit: api.RouteInsightResolveBatch,
		})
		if err != nil {
			err = fmt.Errorf("resolve stale insights: %w", err)
		}
	}
	e.Metrics.observeRun(stats, err, e.now())
	return stats, err
}

func (e *Evaluator) evaluateDue(ctx context.Context, started time.Time) (Stats, error) {
	dueBefore := timestamptz(started.Add(-api.RouteInsightInterval / 2))
	plans := telemetryPlans()
	var stats Stats
	for {
		rows, err := e.Store.RouteInsightCandidateApps(ctx, sqlc.RouteInsightCandidateAppsParams{
			SinceAt: timestamptz(started.Add(-api.RouteInsightWindow)), Plans: plans, DueBefore: dueBefore,
			MaxApps: api.RouteInsightBatchApps,
		})
		if err != nil {
			return stats, fmt.Errorf("route insights candidates: %w", err)
		}
		claimed := 0
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return stats, err
			}
			now := e.now()
			n, err := e.Store.ClaimRouteInsightEvaluation(ctx, sqlc.ClaimRouteInsightEvaluationParams{
				AppID: row.AppID, AccountID: row.AccountID, EvaluatedAt: timestamptz(now), DueBefore: dueBefore,
			})
			if err != nil {
				return stats, fmt.Errorf("claim route insights app: %w", err)
			}
			if n == 0 {
				stats.Taken++
				continue
			}
			claimed++
			stats.Apps++
			observed, skipped, err := e.evaluateApp(ctx, uuidString(row.AccountID), uuidString(row.AppID), now)
			stats.Observed += observed
			switch {
			case err != nil:
				stats.Failed++
				e.log().Warn("route insights app evaluation failed", "app_id", uuidString(row.AppID), "err", err)
			case skipped:
				stats.Skipped++
			}
		}
		// A claimed app is no longer due, so the next read moves on; a batch
		// another replica took entirely ends the run rather than rereading it.
		if len(rows) < api.RouteInsightBatchApps || claimed == 0 {
			return stats, nil
		}
	}
}

// telemetryPlans lists the plans whose apps have request telemetry; apps on
// other plans are not evaluated.
func telemetryPlans() []string {
	var out []string
	for _, plan := range api.Plans {
		if limits, ok := api.LimitsFor(plan); ok && limits.DebugTelemetryEnabled {
			out = append(out, string(plan))
		}
	}
	return out
}

// evaluateApp records one app's findings. An app whose plan lost request
// telemetry since it was read is skipped; its insights resolve once stale.
func (e *Evaluator) evaluateApp(ctx context.Context, accountID, appID string, now time.Time) (int, bool, error) {
	acct, err := e.Store.AccountByID(ctx, accountID)
	if err != nil {
		return 0, false, fmt.Errorf("account: %w", err)
	}
	app, err := e.Store.AppByID(ctx, appID)
	if err != nil {
		return 0, false, fmt.Errorf("app: %w", err)
	}
	if app.AccountID != acct.ID {
		return 0, true, nil
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || !limits.DebugTelemetryEnabled {
		return 0, true, nil
	}
	if e.Released != nil && !e.Released(acct, app) {
		return 0, true, nil
	}
	findings, err := e.findings(ctx, acct, app, limits, now)
	if err != nil {
		return 0, false, err
	}
	observed := 0
	for _, f := range findings {
		if err := e.upsert(ctx, acct.ID, app.ID, f, now); err != nil {
			return observed, false, err
		}
		observed++
	}
	return observed, false, nil
}

func (e *Evaluator) findings(ctx context.Context, acct state.Account, app state.App, limits api.Limits, now time.Time) ([]Finding, error) {
	since := now.Add(-api.RouteInsightWindow)
	data, err := routeawake.Load(ctx, e.Store, acct.ID, app.ID, since, now)
	if err != nil {
		return nil, err
	}
	out := KeptAwake(routeawake.Analyze(routeawake.Inputs{
		App: app.Slug, From: since, Until: now,
		IdleTimeoutS: limits.EffectiveIdleTimeoutS(string(app.Type), app.IdleTimeoutS), TailTimeoutS: limits.WaitUntilTimeoutS(), RAMMB: app.RAMMB,
		Awake: data.Awake, Routes: data.Routes, Sources: data.Sources,
	}))
	stats, err := e.Store.RouteAdviceRouteStats(ctx, sqlc.RouteAdviceRouteStatsParams{
		AppID: pgUUID(app.ID), AccountID: pgUUID(acct.ID), SinceAt: timestamptz(since), UntilAt: timestamptz(now),
		RouteLimit: api.RouteAdviceMaxRoutes, CacheMaxAgeSeconds: int32(api.ResponseCacheDefaultMaxAgeSeconds),
	})
	if err != nil {
		return nil, fmt.Errorf("route advice stats: %w", err)
	}
	rules, err := e.Store.ListEdgeRulesForApp(ctx, app.ID)
	if err != nil {
		return nil, fmt.Errorf("edge rules: %w", err)
	}
	existing := make([]routeadvisor.ExistingRule, 0, len(rules))
	for _, rule := range rules {
		e := routeadvisor.ExistingRule{Kind: string(rule.Kind), Path: rule.MatchPath, Methods: rule.MatchMethods, Enabled: rule.Enabled, Priority: rule.Priority}
		if rule.Action.Cache != nil {
			e.MaxAgeSeconds = rule.Action.Cache.MaxAgeSeconds
		}
		existing = append(existing, e)
	}
	advised := routeadvisor.RoutesFromStats(stats)
	out = append(out, Cacheable(app.Slug, advised, existing)...)
	return append(out, CacheRules(advised, existing)...), nil
}

func (e *Evaluator) upsert(ctx context.Context, accountID, appID string, f Finding, now time.Time) error {
	if !slices.Contains(insightMethods, f.Method) || len(f.Route) == 0 || f.Route[0] != '/' || len(f.Route) > 512 {
		return nil
	}
	evidence, err := json.Marshal(f.Evidence)
	if err != nil {
		return fmt.Errorf("insight evidence: %w", err)
	}
	message := safetext.TruncateRunes(f.Message, maxMessageRunes)
	if err := e.Store.UpsertRouteInsight(ctx, sqlc.UpsertRouteInsightParams{
		AccountID: pgUUID(accountID), AppID: pgUUID(appID), Kind: f.Kind, Method: f.Method, Route: f.Route,
		Message: message, Evidence: evidence, ObservedAt: timestamptz(now),
		// A fixed insight's window still holds pre-fix evidence; it may
		// reopen only once a full window has passed (ADR-20261020161310).
		RefixBefore: timestamptz(now.Add(-api.RouteInsightWindow)),
	}); err != nil {
		return fmt.Errorf("upsert %s insight for %s %s: %w", f.Kind, f.Method, f.Route, err)
	}
	return nil
}

func (e *Evaluator) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

func (e *Evaluator) log() *slog.Logger {
	if e.Log != nil {
		return e.Log
	}
	return slog.Default()
}

func timestamptz(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func pgUUID(id string) pgtype.UUID {
	parsed, err := uuid.Parse(id)
	return pgtype.UUID{Bytes: parsed, Valid: err == nil}
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
