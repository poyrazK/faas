package main

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func routeInvestigationOptions(r *http.Request) (api.RouteHealthInvestigationOptions, error) {
	opts := api.RouteHealthInvestigationOptions{}
	for name, values := range r.URL.Query() {
		if len(values) != 1 || values[0] == "" {
			return opts, errors.New("investigation filters require one nonempty value")
		}
		switch name {
		case "method":
			opts.Method = values[0]
		case "path":
			opts.Path = values[0]
		case "signal":
			opts.Signal = values[0]
		case "customer_group_by":
			opts.CustomerGroupBy = values[0]
		case "customer_id":
			opts.CustomerID = values[0]
		case "status_code":
			value, err := strconv.Atoi(values[0])
			if err != nil {
				return opts, errors.New("status_code must be an integer")
			}
			opts.StatusCode = value
		default:
			return opts, errors.New("unknown investigation filter")
		}
	}
	if opts.Method == "" || opts.Path == "" {
		return opts, errors.New("method and path are required")
	}
	return opts, opts.Validate()
}

func (s *server) getRouteHealthInvestigation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	opts, err := routeInvestigationOptions(r)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	id, err := uuid.Parse(r.PathValue("deployment"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("deployment must be a UUID"))
		return
	}
	app, _, ok := s.routeHealthTarget(w, r, acct)
	if !ok {
		return
	}
	if !acct.Plan.DebugTelemetryEnabled() {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	store, ok := s.store.(state.RouteHealthInvestigationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("route investigation is unavailable"))
		return
	}
	report, err := store.GetRouteHealthInvestigation(r.Context(), acct.ID, app.ID, id.String(), opts)
	if errors.Is(err, state.ErrRouteInvestigationPlan) {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	if err != nil {
		s.routeHealthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}
