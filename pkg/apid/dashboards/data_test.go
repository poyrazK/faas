package dashboards

// adr: 20261022173833 — reading a dashboard resolves each widget through
// its source; one failing widget carries an error and the rest still read.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeSources struct {
	ledger   *state.MemStore
	metrics  api.AppMetricsResponse
	series   api.CustomMetricSeriesResponse
	slo      *api.SLOStatus
	sloErr   error
	withheld map[string]bool // widget kinds whose source is not released
}

func (f *fakeSources) Released(_ state.Account, _ state.App, kind string) error {
	if f.withheld[kind] {
		return errors.New(kind + " is not available to this account")
	}
	return nil
}

func (f *fakeSources) AppMetrics(context.Context, state.Account, state.App, string) (api.AppMetricsResponse, error) {
	return f.metrics, nil
}
func (f *fakeSources) LogEvents() state.LogEventStore { return f.ledger }
func (f *fakeSources) CustomMetricSeries(context.Context, state.App, string, string) api.CustomMetricSeriesResponse {
	return f.series
}
func (f *fakeSources) SLOStatus(context.Context, state.Account, state.App, string) (*api.SLOStatus, error) {
	return f.slo, f.sloErr
}

func TestDashboardDataResolvesEveryWidget(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	svc, acct := newService(true)
	svc.Now = func() time.Time { return now }
	acct.Plan = api.PlanPro
	host := svc.Host.(fakeHost)
	shop := host.apps["shop"]
	host.sources.metrics = api.AppMetricsResponse{Source: "prometheus", LatencyP95MS: 182.5, RequestCount: 900}
	host.sources.series = api.CustomMetricSeriesResponse{Source: "prometheus", Kind: "gauge", Points: []api.CustomMetricSeriesPoint{
		{At: now.Add(-2 * time.Minute), Value: 4}, {At: now.Add(-time.Minute), Value: 7},
	}}
	host.sources.sloErr = errors.New("no such SLO")
	for i, e := range []state.LogEvent{
		{Source: state.LogEventSourceHTTP, Status: 502, Level: "error", Route: "/checkout", Message: "POST /checkout returned 502", Occurrences: 3},
		{Source: state.LogEventSourceHTTP, Status: 200, Level: "info", Route: "/checkout", Message: "POST /checkout returned 200"},
	} {
		e.OccurredAt, e.AccountID, e.AppID, e.SourceEventID = now.Add(-time.Duration(i+1)*time.Minute), acct.ID, shop.ID, "e"+string(rune('a'+i))
		if _, err := host.store.InsertLogEvent(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	w := call(t, svc.Create, acct, http.MethodPost, "", `{"name":"checkout","widgets":[
		{"title":"p95","kind":"app_metric","app":"shop","metric":"latency_p95_ms"},
		{"title":"5xx","kind":"log_count","app":"shop","query":"status=5xx"},
		{"title":"queue","kind":"custom_metric","app":"shop","metric":"orders_pending","window":"5m"},
		{"title":"slo","kind":"slo","app":"shop","slo_id":"6f0c1d4e-7a52-4c37-9a63-0f6a7b6c1a10"}]}`)
	var created api.Dashboard
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &created) != nil {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}

	w = call(t, svc.Data, acct, http.MethodGet, created.ID, "")
	var data api.DashboardData
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &data) != nil || len(data.Widgets) != 4 {
		t.Fatalf("data = %d %s", w.Code, w.Body.String())
	}
	p95, logs, queue, slo := data.Widgets[0], data.Widgets[1], data.Widgets[2], data.Widgets[3]
	if p95.Value == nil || *p95.Value != 182.5 || p95.Unit != "ms" {
		t.Errorf("p95 = %+v", p95)
	}
	if logs.Value == nil || *logs.Value != 3 || logs.Detail != "error 3" {
		t.Errorf("log count = %+v, want the 502's three occurrences", logs)
	}
	if queue.Value == nil || *queue.Value != 7 || len(queue.Series) != 2 {
		t.Errorf("custom metric = %+v, want the latest point and the series", queue)
	}
	if slo.Value != nil || slo.Error != "no such SLO" {
		t.Errorf("slo = %+v, want its error and no value", slo)
	}
	if !data.AsOf.Equal(now) || data.Name != "checkout" {
		t.Errorf("data header = %+v", data)
	}
}

// adr: 20261028101615 — a widget whose source is not released to the account
// reads as an error, and the other widgets still read.
func TestDashboardDataWithholdsUnreleasedSources(t *testing.T) {
	svc, acct := newService(true)
	acct.Plan = api.PlanPro
	host := svc.Host.(fakeHost)
	host.sources.metrics = api.AppMetricsResponse{Source: "prometheus", RequestCount: 900}
	host.sources.withheld = map[string]bool{api.DashboardWidgetLogCount: true}
	w := call(t, svc.Create, acct, http.MethodPost, "", `{"name":"mixed","widgets":[
		{"title":"requests","kind":"app_metric","app":"shop","metric":"request_count"},
		{"title":"5xx","kind":"log_count","app":"shop","query":"status=5xx"}]}`)
	var created api.Dashboard
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &created) != nil {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	w = call(t, svc.Data, acct, http.MethodGet, created.ID, "")
	var data api.DashboardData
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &data) != nil || len(data.Widgets) != 2 {
		t.Fatalf("data = %d %s", w.Code, w.Body.String())
	}
	if requests := data.Widgets[0]; requests.Value == nil || *requests.Value != 900 {
		t.Errorf("app metric = %+v, want its value", requests)
	}
	if logs := data.Widgets[1]; logs.Value != nil || logs.Error != "log_count is not available to this account" {
		t.Errorf("log count = %+v, want the source's refusal and no value", logs)
	}
}
