package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routerequirements"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) canaryRouteGateTarget(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.CanaryRouteGateStore, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return app, nil, false
	}
	store, ok := s.store.(state.CanaryRouteGateStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("canary route gates are unavailable"))
	}
	return app, store, ok
}

func (s *server) getCanaryRouteGate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.canaryRouteGateTarget(w, r, acct)
	if !ok {
		return
	}
	gate, err := store.GetCanaryRouteGate(r.Context(), acct.ID, app.ID)
	if err != nil {
		s.canaryRouteGateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gate)
}

func (s *server) putCanaryRouteGate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var request api.SetCanaryRouteGateRequest
	if err := decodeJSONSized(r, &request, api.RoutePolicyRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid canary route gate request"))
		return
	}
	if err := state.ValidateCanaryRouteGate(request); err != nil {
		api.WriteProblem(w, api.ErrValidation("mode must be report or enforce; explicitly supply expected_revision (0 initially)"))
		return
	}
	app, store, ok := s.canaryRouteGateTarget(w, r, acct)
	if !ok {
		return
	}
	gate, err := store.SetCanaryRouteGate(r.Context(), acct.ID, app.ID, request)
	if err != nil {
		s.canaryRouteGateError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "route_gate.updated", &acct.ID, map[string]any{"app_id": app.ID, "mode": gate.Mode, "revision": gate.Revision})
	writeJSON(w, http.StatusOK, gate)
}

func (s *server) routeGateFingerprint(snapshot state.RoutePolicySnapshot) string {
	return routerequirements.ConfigurationFingerprint(s.routeRequirementsContext(snapshot), string(snapshot.Account.Plan))
}

func (s *server) canaryRouteGateError(w http.ResponseWriter, err error) {
	var blocked *state.RouteGateBlockedError
	switch {
	case errors.As(err, &blocked):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeRouteGateBlocked, "Canary route gate blocked", "Traffic was not increased: "+strings.Join(blocked.Decision.Reasons, ", ")+".").WithHint("Read routes results for this deployment; resolve findings, refresh and wait, then retry canary advance. Use abort to return traffic to the stable deployment."))
	case errors.Is(err, state.ErrRouteGateRevision):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Route gate changed", "Read the current gate revision before changing its mode."))
	case errors.Is(err, state.ErrRouteGateRequirements):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Route requirements missing", "Save version 2 route requirements before enabling enforcement."))
	case errors.Is(err, state.ErrRouteGatePlan):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodePlanTrafficSplitNotAllowed, "Route enforcement unavailable", "Canary route enforcement requires a plan with traffic splits and captured endpoint discovery."))
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "app or route gate")
	default:
		api.WriteProblem(w, api.ErrCapacity("canary route gate could not be read or updated"))
	}
}
