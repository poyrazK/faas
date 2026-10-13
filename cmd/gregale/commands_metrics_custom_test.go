package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func customSeriesServer(t *testing.T, payload api.CustomMetricSeriesResponse) (*string, *string) {
	t.Helper()
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	return &gotPath, &gotQuery
}

func TestCmdMetrics_CustomRendersHistory(t *testing.T) {
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	gotPath, gotQuery := customSeriesServer(t, api.CustomMetricSeriesResponse{
		Name: "orders_pending", Range: "24h", Step: "15m", Source: "prometheus",
		Points: []api.CustomMetricSeriesPoint{{At: at, Value: 10}, {At: at.Add(15 * time.Minute), Value: 40}, {At: at.Add(30 * time.Minute), Value: 25}},
	})
	var stdout strings.Builder
	old := osStdout
	osStdout = &stdout
	t.Cleanup(func() { osStdout = old })

	if code := cmdMetrics([]string{"shop", "--custom", "orders_pending"}); code != 0 {
		t.Fatalf("metrics --custom = %d, want 0", code)
	}
	if *gotPath != "/v1/apps/shop/custom-metrics/orders_pending/series" || *gotQuery != "" {
		t.Fatalf("request = %s?%s, want the series path with the server's default range", *gotPath, *gotQuery)
	}
	out := stdout.String()
	for _, want := range []string{"shop / orders_pending — last 24h (step 15m)", "▁█▄", "Latest: 25", "min 10 · max 40 · 3 samples"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, out)
		}
	}
}

func TestCmdMetrics_CustomPassesExplicitRangeAndRejectsAccount(t *testing.T) {
	_, gotQuery := customSeriesServer(t, api.CustomMetricSeriesResponse{Source: "prometheus"})
	var stdout strings.Builder
	old := osStdout
	osStdout = &stdout
	t.Cleanup(func() { osStdout = old })

	if code := cmdMetrics([]string{"shop", "--custom", "orders_pending", "--range", "7d"}); code != 0 {
		t.Fatalf("explicit range = %d", code)
	}
	if *gotQuery != "range=7d" {
		t.Fatalf("query = %q, want range=7d", *gotQuery)
	}
	if !strings.Contains(stdout.String(), "(no values pushed in this window)") {
		t.Fatalf("empty history should say so:\n%s", stdout.String())
	}
	_, _, restore := swapIO(t)
	defer restore()
	if code := cmdMetrics([]string{"--account", "--custom", "orders_pending"}); code != 1 {
		t.Fatalf("--custom with --account = %d, want 1", code)
	}
}
