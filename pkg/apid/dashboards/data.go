package dashboards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Sources reads the signals widgets reference, through the same code as the
// per-app endpoints (ADR-20261022173833).
type Sources interface {
	// AppMetrics is GET /v1/apps/{slug}/metrics for window, including its
	// plan gate; an error is the widget's reason.
	AppMetrics(ctx context.Context, acct state.Account, app state.App, window string) (api.AppMetricsResponse, error)
	LogEvents() state.LogEventStore
	CustomMetricSeries(ctx context.Context, app state.App, name, rng string) api.CustomMetricSeriesResponse
	SLOStatus(ctx context.Context, acct state.Account, app state.App, sloID string) (*api.SLOStatus, error)
	// Released refuses a widget whose source is not released to the
	// account: its operator switch or capability flag is off
	// (ADR-20261028101615), so a dashboard never shows what the source's
	// own routes hide. The error is the widget's reason.
	Released(acct state.Account, app state.App, kind string) error
}

const (
	dataConcurrency   = 4
	dataWidgetTimeout = 8 * time.Second
)

var windowDurations = map[string]time.Duration{
	"5m": 5 * time.Minute, "1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour,
}

// Data serves GET /v1/dashboards/{id}/data.
func (s *Service) Data(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.gate(w) {
		return
	}
	out, err := s.Read(r.Context(), acct, r.PathValue("id"))
	if s.writeErr(w, r, "read dashboard", err) {
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// Read resolves one of the account's dashboards: every widget for its
// window, with bounded concurrency and a per-widget deadline. The API and
// the dashboard page share it.
func (s *Service) Read(ctx context.Context, acct state.Account, id string) (api.DashboardData, error) {
	row, err := s.dashboard(ctx, acct, id)
	if err != nil {
		return api.DashboardData{}, err
	}
	var widgets []api.DashboardWidget
	_ = json.Unmarshal(row.Widgets, &widgets)
	now := s.now()
	out := api.DashboardData{ID: row.ID, Name: row.Name, AsOf: now, Widgets: make([]api.DashboardWidgetValue, len(widgets))}
	slots := make(chan struct{}, dataConcurrency)
	var wg sync.WaitGroup
	for i, widget := range widgets {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer func() { <-slots; wg.Done() }()
			widgetCtx, cancel := context.WithTimeout(ctx, dataWidgetTimeout)
			defer cancel()
			out.Widgets[i] = s.resolve(widgetCtx, acct, widget, now)
		}()
	}
	wg.Wait()
	return out, nil
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// resolve reads one widget; a failure becomes the widget's Error.
func (s *Service) resolve(ctx context.Context, acct state.Account, w api.DashboardWidget, now time.Time) api.DashboardWidgetValue {
	v := api.DashboardWidgetValue{DashboardWidget: w}
	app, ok := s.Host.AppForAccount(ctx, acct, w.App)
	if !ok {
		v.Error = "app unavailable"
		return v
	}
	src := s.Host.Sources()
	if err := src.Released(acct, app, w.Kind); err != nil {
		v.Error = err.Error()
		return v
	}
	var err error
	switch w.Kind {
	case api.DashboardWidgetAppMetric:
		err = resolveAppMetric(ctx, src, acct, app, &v)
	case api.DashboardWidgetLogCount:
		err = resolveLogCount(ctx, src, acct, app, now, &v)
	case api.DashboardWidgetCustomMetric:
		err = resolveCustomMetric(ctx, src, app, &v)
	case api.DashboardWidgetSLO:
		err = resolveSLO(ctx, src, acct, app, &v)
	default:
		err = fmt.Errorf("unknown widget kind %q", w.Kind)
	}
	if err != nil {
		v.Value, v.Series, v.Error = nil, nil, err.Error()
	}
	return v
}

func resolveAppMetric(ctx context.Context, src Sources, acct state.Account, app state.App, v *api.DashboardWidgetValue) error {
	m, err := src.AppMetrics(ctx, acct, app, v.Window)
	if err != nil {
		return err
	}
	if strings.HasPrefix(m.Source, "degraded") {
		return fmt.Errorf("metrics unavailable (%s)", m.Source)
	}
	values := map[string]struct {
		value float64
		unit  string
	}{
		"request_count":  {float64(m.RequestCount), "requests"},
		"error_rate_pct": {m.ErrorRatePct, "%"},
		"latency_p50_ms": {m.LatencyP50MS, "ms"},
		"latency_p95_ms": {m.LatencyP95MS, "ms"},
		"latency_p99_ms": {m.LatencyP99MS, "ms"},
		"cold_start_pct": {m.ColdStartPct, "%"},
	}
	got, ok := values[v.Metric]
	if !ok {
		return fmt.Errorf("unknown metric %q", v.Metric)
	}
	v.Value, v.Unit = &got.value, got.unit
	return nil
}

func resolveLogCount(ctx context.Context, src Sources, acct state.Account, app state.App, now time.Time, v *api.DashboardWidgetValue) error {
	_, filter, err := state.CanonicalLogQuery(v.Query)
	if err != nil {
		return err
	}
	since := now.Add(-windowDurations[v.Window])
	if keep := retention(acct.Plan); now.Sub(since) > keep {
		since = now.Add(-keep)
	}
	filter.AccountID, filter.AppID, filter.Since, filter.Until = acct.ID, app.ID, since, now
	filter.Timeout = api.LogSearchStatementTimeout
	facets, err := src.LogEvents().FacetLogEvents(ctx, filter, 1)
	if errors.Is(err, state.ErrLogSearchTimeout) {
		return errors.New("the log count timed out; narrow the window or the filters")
	}
	if errors.Is(err, state.ErrLogReadBusy) {
		return errors.New("too many log reads at once; refresh in a few seconds")
	}
	if err != nil {
		return errors.New("logs unavailable")
	}
	total := float64(facets.Total)
	v.Value, v.Unit = &total, "events"
	parts := make([]string, 0, len(facets.Levels))
	for _, level := range facets.Levels {
		parts = append(parts, fmt.Sprintf("%s %d", level.Value, level.Count))
	}
	v.Detail = strings.Join(parts, ", ")
	return nil
}

func resolveCustomMetric(ctx context.Context, src Sources, app state.App, v *api.DashboardWidgetValue) error {
	rng := v.Window
	if rng == "5m" {
		rng = "1h" // the shortest series range; the value is still the latest point
	}
	series := src.CustomMetricSeries(ctx, app, v.Metric, rng)
	if strings.HasPrefix(series.Source, "degraded") {
		return fmt.Errorf("metric unavailable (%s)", series.Source)
	}
	if len(series.Points) == 0 {
		return fmt.Errorf("no values pushed in the window")
	}
	last := series.Points[len(series.Points)-1].Value
	v.Value, v.Unit = &last, series.Kind
	if series.Kind == state.CustomMetricKindCounter {
		v.Unit = "per second"
	}
	v.Series = make([]api.DashboardPoint, 0, len(series.Points))
	for _, p := range series.Points {
		v.Series = append(v.Series, api.DashboardPoint(p))
	}
	return nil
}

func resolveSLO(ctx context.Context, src Sources, acct state.Account, app state.App, v *api.DashboardWidgetValue) error {
	status, err := src.SLOStatus(ctx, acct, app, v.SLOID)
	if err != nil {
		return err
	}
	if status == nil || status.BudgetRemainingPct == nil {
		return fmt.Errorf("no SLO data yet")
	}
	v.Value, v.Unit = status.BudgetRemainingPct, "% budget left"
	if status.AttainmentPct != nil {
		v.Detail = fmt.Sprintf("attainment %.3f%% over %d of %d hours", *status.AttainmentPct, status.HoursRecorded, status.HoursExpected)
	}
	return nil
}

// retention is how far back the plan's log ledger reaches (ADR-211).
func retention(plan api.Plan) time.Duration {
	days := 1
	if limits, ok := api.LimitsFor(plan); ok && limits.LogArchiveRetentionDaysMax > 0 {
		days = limits.LogArchiveRetentionDaysMax
	}
	return time.Duration(days) * 24 * time.Hour
}
