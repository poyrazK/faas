package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routerequirements"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) automaticRouteCheckTarget(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.AutomaticRouteCheckStore, bool) {
	if !enforceOpenAPIPlan(acct.Plan) {
		api.WriteProblem(w, api.ErrPlanOpenAPIDocsNotAllowed(acct.Plan))
		return state.App{}, nil, false
	}
	if err := state.ValidateCheckRouteRequirements(api.CheckRouteRequirementsRequest{DeploymentID: r.PathValue("deployment")}); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return state.App{}, nil, false
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return app, nil, false
	}
	queue, ok := s.store.(state.AutomaticRouteCheckStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("automatic route checks are unavailable"))
	}
	return app, queue, ok
}

func (s *server) getAutomaticRouteCheck(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, queue, ok := s.automaticRouteCheckTarget(w, r, acct)
	if !ok {
		return
	}
	fingerprint := func(snapshot state.RoutePolicySnapshot) string {
		return routerequirements.ConfigurationFingerprint(s.routeRequirementsContext(snapshot), string(snapshot.Account.Plan))
	}
	result, err := queue.GetAutomaticRouteCheck(r.Context(), acct.ID, app.ID, r.PathValue("deployment"), fingerprint)
	if err != nil {
		s.automaticRouteCheckError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) refreshAutomaticRouteCheck(w http.ResponseWriter, r *http.Request, acct state.Account) {
	// This action has no request options; reject accidental unsupported input.
	if r.ContentLength != 0 {
		api.WriteProblem(w, api.ErrValidation("route check refresh accepts an empty body"))
		return
	}
	app, queue, ok := s.automaticRouteCheckTarget(w, r, acct)
	if !ok {
		return
	}
	if err := queue.QueueAutomaticRouteCheck(r.Context(), acct.ID, app.ID, r.PathValue("deployment")); err != nil {
		s.automaticRouteCheckError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"app_id": app.ID, "deployment_id": r.PathValue("deployment"), "status": "queued"})
}

func (s *server) automaticRouteCheckError(w http.ResponseWriter, err error) {
	if errors.Is(err, state.ErrAutomaticRouteCheckPlan) {
		api.WriteProblem(w, api.NewProblem(http.StatusPaymentRequired, api.CodePlanOpenAPIDocsNotAllowed, "Stored route checks unavailable", "The current plan must include captured endpoint discovery to read stored route evidence."))
		return
	}
	s.routeRequirementsError(w, err)
}
