package apidserver

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/dashboards"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

// dashboardsHost adapts *server to dashboards.Host (ADR-20261022173833),
// reading fields at call time so test store swaps take effect.
type dashboardsHost struct{ s *server }

var _ dashboards.Host = dashboardsHost{}

// AppForAccount resolves an app the account owns; deleted apps and other
// accounts' apps read as absent.
func (h dashboardsHost) AppForAccount(ctx context.Context, acct state.Account, slug string) (state.App, bool) {
	if slug == "" {
		return state.App{}, false
	}
	app, err := h.s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		return state.App{}, false
	}
	return app, true
}

func (h dashboardsHost) Dashboards() state.DashboardStore { return h.s.store }

func (h dashboardsHost) Sources() dashboards.Sources { return dashboardSources(h) }

func (h dashboardsHost) Logger() *slog.Logger { return h.s.log }

// dashboardSources reads widget signals through the code behind the per-app
// endpoints, with the same plan gates.
type dashboardSources struct{ s *server }

func (d dashboardSources) AppMetrics(ctx context.Context, acct state.Account, app state.App, window string) (api.AppMetricsResponse, error) {
	if !acct.Plan.PerAppMetricsAllowed() {
		return api.AppMetricsResponse{}, errors.New("per-app metrics need the Hobby plan or above")
	}
	resp, src := appmetrics.Fetch(ctx, d.s.promqlClient, d.s.log, app.ID, window)
	resp.AppID, resp.Range, resp.Source = app.ID, window, src
	return resp, nil
}

func (d dashboardSources) LogEvents() state.LogEventStore { return d.s.store }

func (d dashboardSources) CustomMetricSeries(ctx context.Context, app state.App, name, rng string) api.CustomMetricSeriesResponse {
	return d.s.customMetricSeries(ctx, app.ID, name, d.s.customMetricKind(ctx, app.ID, name), rng, time.Now().UTC())
}

func (d dashboardSources) SLOStatus(ctx context.Context, acct state.Account, app state.App, sloID string) (*api.SLOStatus, error) {
	if !acct.Plan.PerAppMetricsAllowed() {
		return nil, errors.New("SLOs need the Hobby plan or above")
	}
	slos, ok := d.s.store.(state.SLOStore)
	if !ok {
		return nil, errors.New("SLOs unavailable")
	}
	def, err := slos.GetSLO(ctx, app.ID, sloID)
	if err != nil {
		return nil, errors.New("no such SLO")
	}
	return d.s.sloStatus(ctx, def, time.Now()), nil
}
