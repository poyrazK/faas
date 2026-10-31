package apidserver

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// routePrioritiesRequestMaxBytes bounds the PUT body (20 rules).
const routePrioritiesRequestMaxBytes = 16 << 10

// getRoutePriorities serves GET /v1/apps/{slug}/route-priorities (ADR-978):
// the rules gateways apply when the app's warm capacity is saturated.
func (s *server) getRoutePriorities(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !requireRouteControlsPlan(w, acct) {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	s.writeRoutePriorities(w, r, acct, app, true)
}

// putRoutePriorities replaces the app's saved rules. apid is the only writer.
func (s *server) putRoutePriorities(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !requireRouteControlsPlan(w, acct) {
		return
	}
	store, ok := s.store.(state.RoutePriorityStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("route priorities"))
		return
	}
	var req api.SetRoutePrioritiesRequest
	if err := decodeJSONSized(r, &req, routePrioritiesRequestMaxBytes); err != nil || req.Routes == nil {
		api.WriteProblem(w, api.ErrValidation("body must be {\"routes\": [...]}; use [] for no priorities"))
		return
	}
	if err := api.ValidateRoutePriorities(req.Routes); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if _, err := store.SetRoutePriorities(r.Context(), acct.ID, app.ID, req.Routes); errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such app")
		return
	} else if err != nil {
		api.WriteProblem(w, api.ErrCapacity("route priorities"))
		return
	}
	s.audit.Emit(r.Context(), "route_priorities.updated", &acct.ID, map[string]any{"app_id": app.ID, "route_count": len(req.Routes)})
	s.writeRoutePriorities(w, r, acct, app, false)
}

// deleteRoutePriorities removes saved rules, restoring the route-health default.
func (s *server) deleteRoutePriorities(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !requireRouteControlsPlan(w, acct) {
		return
	}
	store, ok := s.store.(state.RoutePriorityStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("route priorities"))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if err := store.DeleteRoutePriorities(r.Context(), acct.ID, app.ID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("route priorities"))
		return
	}
	s.audit.Emit(r.Context(), "route_priorities.reset", &acct.ID, map[string]any{"app_id": app.ID})
	s.writeRoutePriorities(w, r, acct, app, false)
}

// writeRoutePriorities answers with the effective rules; GET also includes
// the queue evidence (ADR-1152).
func (s *server) writeRoutePriorities(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, withQueue bool) {
	source, setting, err := state.EffectiveRoutePriorities(r.Context(), s.store, acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("route priorities"))
		return
	}
	out := api.RoutePrioritiesResponse{Slug: app.Slug, Source: source, Routes: setting.Routes}
	if !setting.UpdatedAt.IsZero() {
		out.UpdatedAt = setting.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	if withQueue {
		queue := s.routeEvidence().PriorityQueue(r.Context(), app)
		out.Queue = &queue
	}
	writeJSON(w, http.StatusOK, out)
}

// requireRouteControlsPlan refuses route priorities, throttle suggestions and
// route lifecycle review on the Free plan, which the catalog leaves them off
// (ADR-20261026143210); route removal checks stay readable for promotions.
func requireRouteControlsPlan(w http.ResponseWriter, acct state.Account) bool {
	if acct.Plan.IsPaid() {
		return true
	}
	api.WriteProblem(w, api.ErrPlanFeatureGated("route_controls", acct.Plan))
	return false
}
