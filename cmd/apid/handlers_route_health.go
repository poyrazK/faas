package main

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) routeHealthTarget(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.RouteHealthStore, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return app, nil, false
	}
	store, ok := s.store.(state.RouteHealthStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("route health is unavailable"))
	}
	return app, store, ok
}
func (s *server) getRouteHealthGate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.routeHealthTarget(w, r, acct)
	if !ok {
		return
	}
	gate, err := store.GetRouteHealthGate(r.Context(), acct.ID, app.ID)
	if err != nil {
		s.routeHealthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gate)
}
func (s *server) putRouteHealthGate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.SetRouteHealthGateRequest
	if err := decodeJSONSized(r, &req, api.RouteHealthRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid route health configuration"))
		return
	}
	if err := routehealth.Validate(req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	app, store, ok := s.routeHealthTarget(w, r, acct)
	if !ok {
		return
	}
	gate, err := store.SetRouteHealthGate(r.Context(), acct.ID, app.ID, req)
	if err != nil {
		s.routeHealthError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "route_health.updated", &acct.ID, map[string]any{"app_id": app.ID, "mode": gate.Mode, "on_regression": gate.OnRegression, "revision": gate.Revision, "route_count": len(gate.Routes)})
	writeJSON(w, http.StatusOK, gate)
}
func (s *server) getRouteHealthReport(w http.ResponseWriter, r *http.Request, acct state.Account) {
	opts, err := routeCustomerHealthOptions(r)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	id, err := uuid.Parse(r.PathValue("deployment"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("deployment must be a UUID"))
		return
	}
	app, store, ok := s.routeHealthTarget(w, r, acct)
	if !ok {
		return
	}
	report, err := s.readRouteHealthReport(r.Context(), store, acct.ID, app.ID, id.String(), opts)
	if err != nil {
		s.routeHealthError(w, err)
		return
	}
	if candidate, err := s.store.DeploymentByID(r.Context(), id.String()); err == nil && candidate.AppID == app.ID {
		report.ProfileSignal = s.profileCanarySignal(r.Context(), acct, app, candidate)
	}
	writeJSON(w, http.StatusOK, report)
}
func (s *server) routeHealthError(w http.ResponseWriter, err error) {
	var blocked *state.RouteHealthBlockedError
	switch {
	case errors.As(err, &blocked):
		hint := "Read gregale routes health report APP --deployment " + blocked.Decision.DeploymentID + " for current route evidence."
		if blocked.Decision.HistoryID != "" {
			hint += " Saved decision: gregale routes health explain APP --deployment " + blocked.Decision.DeploymentID + " --decision " + blocked.Decision.HistoryID + "."
		}
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeRouteHealthBlocked, "Route health gate blocked", "Traffic was not increased: "+blocked.Decision.Reason+".").WithHint(hint+" Wait for sufficient healthy observations or abort the canary."))
	case errors.Is(err, state.ErrRouteHealthRevision):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Route health configuration changed", "Read the current revision before updating the guard."))
	case errors.Is(err, state.ErrRouteHealthPlan):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodePlanTrafficSplitNotAllowed, "Route health enforcement unavailable", "Enforcement requires traffic splits and request telemetry entitlement."))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid route health configuration"))
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "app or deployment")
	default:
		api.WriteProblem(w, api.ErrCapacity("route health evidence could not be read or updated"))
	}
}
