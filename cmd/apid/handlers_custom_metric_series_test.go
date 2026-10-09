package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func customHistoryEnv(t *testing.T) testEnv {
	t.Helper()
	e := setup(t, api.PlanPro)
	e.s.customMetricHistoryEnabled = true
	return e
}

func TestGetCustomMetricSeries_DisabledAndInvalid(t *testing.T) {
	e := setup(t, api.PlanPro)
	createApp(t, e, "shop")
	assertProblem(t, e.do(t, http.MethodGet, "/v1/apps/shop/custom-metrics/orders/series", nil, nil),
		http.StatusServiceUnavailable, "custom_metric_history_unavailable")

	e.s.customMetricHistoryEnabled = true
	rec := e.do(t, http.MethodGet, "/v1/apps/shop/custom-metrics/orders/series?range=90d", nil, nil)
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
}

func TestGetCustomMetricSeries_ReturnsRecordedHistory(t *testing.T) {
	e := customHistoryEnv(t)
	app := createApp(t, e, "shop")
	var gotQuery string
	installPromFixture(t, &e, func(q string) string {
		gotQuery = q
		return `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1760000000,"12"],[1760000900,"30"]]}]}}`
	})

	rec := e.do(t, http.MethodGet, "/v1/apps/shop/custom-metrics/orders_pending/series?range=6h", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.CustomMetricSeriesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Source != "prometheus" || out.Range != "6h" || out.Step != "5m" || len(out.Points) != 2 || out.Points[1].Value != 30 {
		t.Fatalf("series = %+v", out)
	}
	want := fmt.Sprintf(`max(gregale_app_custom_metric{app=%q,name="orders_pending"})`, app.ID)
	if gotQuery != want {
		t.Fatalf("query = %s, want %s", gotQuery, want)
	}
}

func TestCustomMetricsDashboard_ListsMetricsWithHistory(t *testing.T) {
	e := customHistoryEnv(t)
	app := createApp(t, e, "shop")
	if err := e.store.PutCustomMetric(context.Background(), app.ID, "orders_pending", 42, time.Now(), api.MaxCustomMetricsPerApp); err != nil {
		t.Fatal(err)
	}
	installPromFixture(t, &e, func(string) string {
		return `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1760000000,"10"],[1760000900,"42"]]}]}}`
	})

	r := httptest.NewRequest(http.MethodGet, "/dashboard/apps/shop/custom-metrics", nil)
	r.SetPathValue("slug", "shop")
	r = r.WithContext(WithAccount(r.Context(), e.acct))
	rec := httptest.NewRecorder()
	e.s.renderCustomMetricsDashboard(rec, r)
	body := rec.Body.String()
	for _, want := range []string{"<code>orders_pending</code>", "<svg", ">42<"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}
