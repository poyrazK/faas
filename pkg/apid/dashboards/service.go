// Package dashboards is apid's custom dashboards (ADR-20261022173833): an
// account's saved compositions of widgets, each a typed reference to a signal
// another endpoint already serves. It lives outside apidserver per ADR-982;
// apidserver only wires the routes and implements Host.
package dashboards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Host is what dashboards need from apid.
type Host interface {
	// AppForAccount returns the account's app by slug, or false.
	AppForAccount(ctx context.Context, acct state.Account, slug string) (state.App, bool)
	Dashboards() state.DashboardStore
	Sources() Sources
	Logger() *slog.Logger
}

// Service serves the dashboards API. Enabled is the
// FAAS_CUSTOM_DASHBOARDS_ENABLED gate; while it is off every route answers 503.
type Service struct {
	Host    Host
	Enabled bool
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

const widgetTitleMax = 80

// List serves GET /v1/dashboards.
func (s *Service) List(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.gate(w) {
		return
	}
	rows, err := s.Host.Dashboards().ListDashboards(r.Context(), acct.ID)
	if err != nil {
		s.fail(w, r, "list dashboards", err)
		return
	}
	resp := api.DashboardsResponse{Dashboards: make([]api.Dashboard, 0, len(rows))}
	for _, row := range rows {
		resp.Dashboards = append(resp.Dashboards, dto(row))
	}
	httpkit.WriteJSON(w, http.StatusOK, resp)
}

// ListFor returns the account's dashboards for the dashboard page.
func (s *Service) ListFor(ctx context.Context, acct state.Account) ([]api.Dashboard, error) {
	rows, err := s.Host.Dashboards().ListDashboards(ctx, acct.ID)
	if err != nil {
		return nil, err
	}
	out := make([]api.Dashboard, 0, len(rows))
	for _, row := range rows {
		out = append(out, dto(row))
	}
	return out, nil
}

// Create serves POST /v1/dashboards.
func (s *Service) Create(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.gate(w) {
		return
	}
	row, ok := s.decode(w, r, acct)
	if !ok {
		return
	}
	created, err := s.Host.Dashboards().CreateDashboard(r.Context(), row, api.DashboardsPerAccount)
	if s.writeErr(w, r, "create dashboard", err) {
		return
	}
	httpkit.WriteJSON(w, http.StatusCreated, dto(created))
}

// Get serves GET /v1/dashboards/{id}.
func (s *Service) Get(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.gate(w) {
		return
	}
	row, err := s.Host.Dashboards().DashboardByID(r.Context(), acct.ID, r.PathValue("id"))
	if s.writeErr(w, r, "read dashboard", err) {
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, dto(row))
}

// Replace serves PUT /v1/dashboards/{id}.
func (s *Service) Replace(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.gate(w) {
		return
	}
	row, ok := s.decode(w, r, acct)
	if !ok {
		return
	}
	row.ID = r.PathValue("id")
	replaced, err := s.Host.Dashboards().ReplaceDashboard(r.Context(), row)
	if s.writeErr(w, r, "replace dashboard", err) {
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, dto(replaced))
}

// Delete serves DELETE /v1/dashboards/{id}.
func (s *Service) Delete(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.gate(w) {
		return
	}
	if s.writeErr(w, r, "delete dashboard", s.Host.Dashboards().DeleteDashboard(r.Context(), acct.ID, r.PathValue("id"))) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) gate(w http.ResponseWriter) bool {
	if !s.Enabled {
		api.WriteProblem(w, api.ErrDashboardsUnavailable())
	}
	return s.Enabled
}

// decode reads and validates a dashboard request into a stored row.
func (s *Service) decode(w http.ResponseWriter, r *http.Request, acct state.Account) (state.Dashboard, bool) {
	var req api.DashboardRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrDashboardInvalid("body must be {\"name\": ..., \"widgets\": [...]}"))
		return state.Dashboard{}, false
	}
	widgets, prob := s.Validate(r.Context(), acct, req)
	if prob != nil {
		api.WriteProblem(w, prob)
		return state.Dashboard{}, false
	}
	raw, err := json.Marshal(widgets)
	if err != nil {
		s.fail(w, r, "encode dashboard widgets", err)
		return state.Dashboard{}, false
	}
	return state.Dashboard{AccountID: acct.ID, Name: req.Name, Widgets: raw}, true
}

// Validate checks a dashboard definition and returns its widgets with
// defaults applied and log queries in canonical form.
func (s *Service) Validate(ctx context.Context, acct state.Account, req api.DashboardRequest) ([]api.DashboardWidget, *api.Problem) {
	if !state.ValidDashboardName(req.Name) {
		return nil, api.ErrDashboardInvalid("name must be 1..63 lowercase letters, digits and dashes, starting with a letter or digit")
	}
	if len(req.Widgets) > api.DashboardWidgetsMax {
		return nil, api.ErrDashboardInvalid(fmt.Sprintf("a dashboard holds at most %d widgets; this one has %d", api.DashboardWidgetsMax, len(req.Widgets)))
	}
	out := make([]api.DashboardWidget, 0, len(req.Widgets))
	for i, widget := range req.Widgets {
		normalized, err := s.validateWidget(ctx, acct, widget)
		if err != nil {
			return nil, api.ErrDashboardInvalid(fmt.Sprintf("widget %d: %v", i+1, err))
		}
		out = append(out, normalized)
	}
	return out, nil
}

func (s *Service) validateWidget(ctx context.Context, acct state.Account, w api.DashboardWidget) (api.DashboardWidget, error) {
	w.Title = strings.TrimSpace(w.Title)
	if n := utf8.RuneCountInString(w.Title); n < 1 || n > widgetTitleMax {
		return w, fmt.Errorf("title must be 1..%d characters", widgetTitleMax)
	}
	if _, ok := s.Host.AppForAccount(ctx, acct, w.App); !ok {
		return w, fmt.Errorf("the account has no app %q", w.App)
	}
	if w.Kind == api.DashboardWidgetSLO {
		if _, err := uuid.Parse(w.SLOID); err != nil {
			return w, errors.New("slo widgets need slo_id, the SLO's id")
		}
		w.Metric, w.Query, w.Window = "", "", ""
		return w, nil
	}
	if w.Window == "" {
		w.Window = api.DashboardDefaultWindow
	}
	if !slices.Contains(api.DashboardWindows, w.Window) {
		return w, fmt.Errorf("window must be one of %s", strings.Join(api.DashboardWindows, ", "))
	}
	w.SLOID = ""
	switch w.Kind {
	case api.DashboardWidgetAppMetric:
		if !slices.Contains(api.DashboardAppMetrics, w.Metric) {
			return w, fmt.Errorf("metric must be one of %s", strings.Join(api.DashboardAppMetrics, ", "))
		}
		w.Query = ""
	case api.DashboardWidgetCustomMetric:
		if api.ValidateCustomMetricName(w.Metric) != nil {
			return w, errors.New("metric must be a pushed custom metric name ([a-z][a-z0-9_]{0,62})")
		}
		w.Query = ""
	case api.DashboardWidgetLogCount:
		canonical, filter, err := state.CanonicalLogQuery(w.Query)
		if err != nil {
			return w, fmt.Errorf("query: %w", err)
		}
		if filter.Query != "" && (w.Window == "7d") {
			return w, fmt.Errorf("a text filter (q) counts at most %s; use a shorter window", api.LogSearchTextMaxWindow)
		}
		w.Query, w.Metric = canonical, ""
	default:
		return w, fmt.Errorf("kind must be one of %s, %s, %s, %s", api.DashboardWidgetAppMetric, api.DashboardWidgetLogCount, api.DashboardWidgetCustomMetric, api.DashboardWidgetSLO)
	}
	return w, nil
}

// writeErr maps store errors to problems and reports whether it wrote one.
func (s *Service) writeErr(w http.ResponseWriter, r *http.Request, op string, err error) bool {
	var quota *state.DashboardQuotaError
	switch {
	case err == nil:
		return false
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.ErrDashboardNotFound())
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "dashboard_name_taken", "Dashboard name taken",
			"the account already has a dashboard with that name"))
	case errors.As(err, &quota):
		api.WriteProblem(w, api.ErrDashboardQuota(quota.Limit, quota.Observed))
	default:
		s.fail(w, r, op, err)
	}
	return true
}

func (s *Service) fail(w http.ResponseWriter, r *http.Request, op string, err error) {
	s.Host.Logger().WarnContext(r.Context(), "dashboards: "+op, "err", err)
	api.WriteProblem(w, api.ErrCapacity("could not "+op))
}

// dto renders a stored dashboard; widgets were validated on the way in.
func dto(row state.Dashboard) api.Dashboard {
	out := api.Dashboard{ID: row.ID, Name: row.Name, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	_ = json.Unmarshal(row.Widgets, &out.Widgets)
	if out.Widgets == nil {
		out.Widgets = []api.DashboardWidget{}
	}
	return out
}
