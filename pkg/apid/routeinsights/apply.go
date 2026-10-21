package routeinsights

// Applying a fix (ADR-20261020161310): the API and the dashboard share this
// path. Rule writes go through the edge-rules API core, so plan quotas,
// validation, fleet convergence and audit apply as they do for a hand-made
// rule.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RuleWriter creates and changes edge rules through the edge-rules API core.
type RuleWriter interface {
	CreateRule(ctx context.Context, acct state.Account, app state.App, req api.CreateEdgeRuleRequest) (api.EdgeRuleResponse, *api.Problem)
	UpdateRule(ctx context.Context, acct state.Account, ruleID string, req api.UpdateEdgeRuleRequest) (api.EdgeRuleResponse, *api.Problem)
}

// FixStore is what applying a fix reads and writes.
type FixStore interface {
	ListStore
	GetRouteInsight(context.Context, sqlc.GetRouteInsightParams) (sqlc.GetRouteInsightRow, error)
	MarkRouteInsightsFixed(context.Context, sqlc.MarkRouteInsightsFixedParams) (int64, error)
}

// Fixer applies route insight fixes.
type Fixer struct {
	Store FixStore
	Rules RuleWriter
	// Purge durably requests a purge of the app's cached responses whose
	// request path matches glob.
	Purge func(ctx context.Context, app state.App, glob string) error
	Log   *slog.Logger
	Now   func() time.Time
}

// Apply applies the fix of one of the app's insights. fixID must match the
// fix the insight would apply now, so a caller applies exactly what it read.
func (f *Fixer) Apply(ctx context.Context, acct state.Account, app state.App, insightID, fixID string) (api.RouteInsightFixResponse, *api.Problem) {
	now := f.now()
	insight, prob := f.load(ctx, acct, app, insightID)
	if prob != nil {
		return api.RouteInsightFixResponse{}, prob
	}
	listed, err := List(ctx, f.Store, acct.ID, app.ID, now)
	if err != nil {
		f.log().Warn("route insight fix: list insights", "app_id", app.ID, "err", err)
		return api.RouteInsightFixResponse{}, api.ErrCapacity("could not read the app's route insights")
	}
	rules, err := f.Store.ListEdgeRulesForApp(ctx, app.ID)
	if err != nil {
		f.log().Warn("route insight fix: list edge rules", "app_id", app.ID, "err", err)
		return api.RouteInsightFixResponse{}, api.ErrCapacity("could not read the app's edge rules")
	}
	plan, reason := PlanFix(insight, activeInsights(listed), rules, now)
	if plan == nil {
		return api.RouteInsightFixResponse{}, api.ErrRouteInsightNoFix(reason)
	}
	if fixID != plan.Fix.ID {
		return api.RouteInsightFixResponse{}, api.ErrRouteInsightFixChanged()
	}
	var rule api.EdgeRuleResponse
	if plan.Create != nil {
		rule, prob = f.Rules.CreateRule(ctx, acct, app, *plan.Create)
	} else {
		rule, prob = f.Rules.UpdateRule(ctx, acct, plan.Fix.RuleID, *plan.Update)
	}
	if prob != nil {
		return api.RouteInsightFixResponse{}, prob
	}
	out := api.RouteInsightFixResponse{Fix: plan.Fix, Rule: rule, Insight: insight}
	if plan.PurgeGlob != "" {
		out.CachePurge = api.RouteInsightPurgeRequested
		err := errors.New("no cache purge path is configured")
		if f.Purge != nil {
			err = f.Purge(ctx, app, plan.PurgeGlob)
		}
		if err != nil {
			f.log().Warn("route insight fix: cache purge", "app_id", app.ID, "glob", plan.PurgeGlob, "err", err)
			out.CachePurge = api.RouteInsightPurgeFailed
		}
	}
	// The rule change is committed; a failure to record the resolution
	// leaves it to the next evaluation rather than failing the request.
	out.Resolved, err = f.Store.MarkRouteInsightsFixed(ctx, sqlc.MarkRouteInsightsFixedParams{
		FixedAt: timestamptz(now), FixAction: pgtype.Text{String: plan.Fix.Action, Valid: true},
		AppID: pgUUID(app.ID), AccountID: pgUUID(acct.ID), Method: insight.Method, Route: insight.Route, Kinds: plan.Kinds,
	})
	if err != nil {
		f.log().Warn("route insight fix: record resolution", "app_id", app.ID, "insight_id", insight.ID, "err", err)
	}
	if updated, prob := f.load(ctx, acct, app, insight.ID); prob == nil {
		out.Insight = updated
	}
	return out, nil
}

func (f *Fixer) load(ctx context.Context, acct state.Account, app state.App, insightID string) (api.RouteInsight, *api.Problem) {
	notFound := api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such route insight")
	if _, err := uuid.Parse(insightID); err != nil {
		return api.RouteInsight{}, notFound
	}
	row, err := f.Store.GetRouteInsight(ctx, sqlc.GetRouteInsightParams{ID: pgUUID(insightID), AppID: pgUUID(app.ID), AccountID: pgUUID(acct.ID)})
	if errors.Is(err, state.ErrNotFound) {
		return api.RouteInsight{}, notFound
	}
	if err != nil {
		f.log().Warn("route insight fix: read insight", "app_id", app.ID, "err", err)
		return api.RouteInsight{}, api.ErrCapacity("could not read the route insight")
	}
	return insightFromRow(sqlc.ListRouteInsightsRow(row)), nil
}

func (f *Fixer) now() time.Time {
	if f.Now != nil {
		return f.Now().UTC()
	}
	return time.Now().UTC()
}

func (f *Fixer) log() *slog.Logger {
	if f.Log != nil {
		return f.Log
	}
	return slog.Default()
}
