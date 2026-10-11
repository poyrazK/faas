package main

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) routeMonitorTarget(w http.ResponseWriter, r *http.Request, a state.Account) (state.App, state.RouteMonitorStore, bool) {
	app, ok := s.loadApp(w, r, a, r.PathValue("slug"))
	if !ok {
		return app, nil, false
	}
	store, ok := s.store.(state.RouteMonitorStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("production route monitoring unavailable"))
	}
	return app, store, ok
}
func (s *server) routeMonitorError(w http.ResponseWriter, err error, a state.Account) {
	switch {
	case errors.Is(err, state.ErrRouteInvestigationPlan):
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", a.Plan))
	case errors.Is(err, state.ErrRouteHealthRevision):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Route monitor revision changed", "Read the current monitor revision before updating it."))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid route monitor configuration"))
	default:
		s.routeHealthError(w, err)
	}
}
func (s *server) getRouteMonitor(w http.ResponseWriter, r *http.Request, a state.Account) {
	app, store, ok := s.routeMonitorTarget(w, r, a)
	if !ok {
		return
	}
	c, err := store.GetRouteMonitor(r.Context(), a.ID, app.ID)
	if err != nil {
		s.routeMonitorError(w, err, a)
		return
	}
	writeJSON(w, http.StatusOK, c)
}
func (s *server) putRouteMonitor(w http.ResponseWriter, r *http.Request, a state.Account) {
	var req api.SetRouteMonitorRequest
	if err := decodeJSONSized(r, &req, api.RouteHealthRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid route monitor configuration"))
		return
	}
	if err := routemonitor.Validate(req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	app, store, ok := s.routeMonitorTarget(w, r, a)
	if !ok {
		return
	}
	c, err := store.SetRouteMonitor(r.Context(), a.ID, app.ID, req)
	if err != nil {
		s.routeMonitorError(w, err, a)
		return
	}
	s.audit.Emit(r.Context(), "route_monitor.updated", &a.ID, map[string]any{"app_id": app.ID, "enabled": c.Enabled, "revision": c.Revision, "route_count": len(c.Routes), "customer_group_by": c.CustomerGroupBy})
	writeJSON(w, http.StatusOK, c)
}
func (s *server) postRouteMonitorPreview(w http.ResponseWriter, r *http.Request, a state.Account) {
	var req api.PreviewRouteMonitorRequest
	if err := decodeJSONSized(r, &req, api.RouteHealthRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid route monitor preview"))
		return
	}
	if err := routemonitor.ValidatePreviewRequest(req, 0); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	details, err := routeMonitorCustomerDetails(r)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	app, store, ok := s.routeMonitorTarget(w, r, a)
	if !ok {
		return
	}
	var preview api.RouteMonitorPreview
	if detailStore, ok := s.store.(state.RouteMonitorPreviewDetailsStore); ok {
		preview, err = detailStore.PreviewRouteMonitorWithCustomerDetails(r.Context(), a.ID, app.ID, req, details)
	} else {
		preview, err = store.PreviewRouteMonitor(r.Context(), a.ID, app.ID, req)
		preview.Report = routemonitor.ProjectReport(preview.Report, details)
	}
	if err != nil {
		s.routeMonitorError(w, err, a)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}
func (s *server) getRouteMonitorReport(w http.ResponseWriter, r *http.Request, a state.Account) {
	app, store, ok := s.routeMonitorTarget(w, r, a)
	if !ok {
		return
	}
	details, err := routeMonitorCustomerDetails(r)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	var report api.RouteMonitorReport
	if detailStore, ok := s.store.(state.RouteMonitorCustomerDetailsStore); ok {
		report, err = detailStore.GetRouteMonitorReportWithCustomerDetails(r.Context(), a.ID, app.ID, details)
	} else {
		report, err = store.GetRouteMonitorReport(r.Context(), a.ID, app.ID)
		report = routemonitor.ProjectReport(report, details)
	}
	if err != nil {
		s.routeMonitorError(w, err, a)
		return
	}
	writeJSON(w, http.StatusOK, report)
}
func (s *server) getRouteMonitorIncident(w http.ResponseWriter, r *http.Request, a state.Account) {
	id, err := uuid.Parse(r.PathValue("incident"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("incident must be a UUID"))
		return
	}
	app, store, ok := s.routeMonitorTarget(w, r, a)
	if !ok {
		return
	}
	details, err := routeMonitorCustomerDetails(r)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	var incident api.RouteMonitorIncident
	if detailStore, ok := s.store.(state.RouteMonitorCustomerDetailsStore); ok {
		incident, err = detailStore.GetRouteMonitorIncidentWithCustomerDetails(r.Context(), a.ID, app.ID, id.String(), details)
	} else {
		incident, err = store.GetRouteMonitorIncident(r.Context(), a.ID, app.ID, id.String())
		incident = routemonitor.ProjectIncident(incident, details)
	}
	if err != nil {
		s.routeMonitorError(w, err, a)
		return
	}
	writeJSON(w, http.StatusOK, incident)
}
func routeMonitorCustomerDetails(r *http.Request) (bool, error) {
	q := r.URL.Query()
	details := false
	for key, values := range q {
		if key != "customer_details" || len(values) != 1 {
			return false, errors.New("only one customer_details option is supported")
		}
		value, err := strconv.ParseBool(values[0])
		if err != nil {
			return false, errors.New("customer_details must be a boolean")
		}
		details = value
	}
	return details, nil
}
func routeMonitorPageOptions(r *http.Request) (int, string, error) {
	limit, before := api.RouteMonitorPageSize, ""
	for name, values := range r.URL.Query() {
		if len(values) != 1 || values[0] == "" {
			return 0, "", errors.New("supply one nonempty page filter")
		}
		switch name {
		case "limit":
			v, err := strconv.Atoi(values[0])
			if err != nil || v < 1 || v > api.RouteMonitorMaxPage {
				return 0, "", errors.New("invalid incident page limit")
			}
			limit = v
		case "before":
			id, err := uuid.Parse(values[0])
			if err != nil || id.String() != values[0] {
				return 0, "", errors.New("before must be a canonical UUID")
			}
			before = values[0]
		default:
			return 0, "", errors.New("unknown incident page filter")
		}
	}
	return limit, before, nil
}
func (s *server) listRouteMonitorIncidents(w http.ResponseWriter, r *http.Request, a state.Account) {
	limit, before, err := routeMonitorPageOptions(r)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	app, store, ok := s.routeMonitorTarget(w, r, a)
	if !ok {
		return
	}
	page, err := store.ListRouteMonitorIncidents(r.Context(), a.ID, app.ID, limit, before)
	if err != nil {
		s.routeMonitorError(w, err, a)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
