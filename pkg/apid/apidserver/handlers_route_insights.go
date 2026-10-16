package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/routeinsights"
	"github.com/onebox-faas/faas/pkg/state"
)

// getRouteInsights serves GET /v1/apps/{slug}/routes/insights (ADR-1198): the
// findings the route insights evaluator recorded for the app. It never writes.
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
