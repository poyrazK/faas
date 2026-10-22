package apidserver

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/routeinsights"
	"github.com/onebox-faas/faas/pkg/state"
)

// getRouteInsights serves GET /v1/apps/{slug}/routes/insights (ADR-1198): the
// findings the route insights evaluator recorded for the app, each active one
// with the fix applying it would make now (ADR-20261020161310). It never
// writes.
func (s *server) getRouteInsights(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !api.MustLimitsFor(acct.Plan).DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("analytics", acct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(routeinsights.ListStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("route insights"))
		return
	}
	insights, err := routeinsights.List(r.Context(), store, acct.ID, app.ID, time.Now().UTC())
	if err != nil {
		s.log.Warn("route insights read failed", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("route insights"))
		return
	}
	writeJSON(w, http.StatusOK, api.RouteInsightsResponse{Slug: app.Slug, Insights: insights})
}

// fixRouteInsight serves POST /v1/apps/{slug}/routes/insights/{id}/fix
// (ADR-20261020161310): it applies the insight's fix when fix_id still names
// the fix the insight would apply now.
func (s *server) fixRouteInsight(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !api.MustLimitsFor(acct.Plan).DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("analytics", acct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.RouteInsightFixRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if strings.TrimSpace(req.FixID) == "" {
		api.WriteProblem(w, api.ErrValidation("fix_id is required; read it from GET /v1/apps/{slug}/routes/insights"))
		return
	}
	fixer, ok := s.routeInsightFixer()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("route insights"))
		return
	}
	out, prob := fixer.Apply(r.Context(), acct, app, r.PathValue("id"), req.FixID)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// routeInsightFixer applies fixes through the edge-rules and cache-purge
// cores, so they behave exactly like the API calls they stand for.
func (s *server) routeInsightFixer() (*routeinsights.Fixer, bool) {
	store, ok := s.store.(routeinsights.FixStore)
	if !ok {
		return nil, false
	}
	return &routeinsights.Fixer{
		Store: store, Rules: &s.edgeRules, Log: s.log,
		Purge: func(ctx context.Context, app state.App, glob string) error {
			if prob := s.requestCachePurge(ctx, app, glob, ""); prob != nil {
				return errors.New(prob.Detail)
			}
			return nil
		},
	}, true
}
