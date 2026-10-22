package main_test

// adr: 20261022173833 — `gregale dashboards` creates from a widgets file,
// lists, shows and deletes.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdDashboardsCreateListGetRm(t *testing.T) {
	var calls []string
	var created api.DashboardRequest
	dash := api.Dashboard{ID: "d-1", Name: "checkout", CreatedAt: time.Now(), UpdatedAt: time.Now(), Widgets: []api.DashboardWidget{
		{Title: "p95", Kind: api.DashboardWidgetAppMetric, App: "shop", Metric: "latency_p95_ms", Window: "1h"},
		{Title: "5xx", Kind: api.DashboardWidgetLogCount, App: "shop", Query: "status=5xx", Window: "6h"},
	}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &created)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(dash)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/v1/dashboards":
			_ = json.NewEncoder(w).Encode(api.DashboardsResponse{Dashboards: []api.Dashboard{dash}})
		default:
			_ = json.NewEncoder(w).Encode(dash)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	var stdout bytes.Buffer
	old := *main.OsStdout
	*main.OsStdout = &stdout
	t.Cleanup(func() { *main.OsStdout = old })

	file := filepath.Join(t.TempDir(), "widgets.json")
	if err := os.WriteFile(file, []byte(`[{"title":"p95","kind":"app_metric","app":"shop","metric":"latency_p95_ms"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := main.CmdDashboards([]string{"create", "checkout", "--file", file}); code != 0 {
		t.Fatalf("create = %d", code)
	}
	if created.Name != "checkout" || len(created.Widgets) != 1 || created.Widgets[0].Metric != "latency_p95_ms" {
		t.Fatalf("create body = %+v", created)
	}
	for _, args := range [][]string{{"list"}, {"get", "d-1"}, {"rm", "d-1"}} {
		if code := main.CmdDashboards(args); code != 0 {
			t.Fatalf("%v = %d", args, code)
		}
	}
	want := "POST /v1/dashboards|GET /v1/dashboards|GET /v1/dashboards/d-1|DELETE /v1/dashboards/d-1"
	if got := strings.Join(calls, "|"); got != want {
		t.Fatalf("calls = %s, want %s", got, want)
	}
	out := stdout.String()
	for _, s := range []string{"created dashboard checkout (d-1) with 2 widgets", "d-1", "log_count shop/status=5xx over 6h", "deleted dashboard d-1"} {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q\nfull: %s", s, out)
		}
	}
	if code := main.CmdDashboards([]string{"create", "checkout"}); code == 0 {
		t.Fatal("create without --file succeeded")
	}
}

func TestCmdDashboardsShowRendersValuesAndErrors(t *testing.T) {
	p95 := 182.5
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/dashboards/d-1/data" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(api.DashboardData{ID: "d-1", Name: "checkout", AsOf: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC), Widgets: []api.DashboardWidgetValue{
			{DashboardWidget: api.DashboardWidget{Title: "Checkout p95", Kind: api.DashboardWidgetAppMetric, App: "shop", Window: "1h"}, Value: &p95, Unit: "ms"},
			{DashboardWidget: api.DashboardWidget{Title: "Queue", Kind: api.DashboardWidgetCustomMetric, App: "shop", Window: "1h"}, Error: "no values pushed in the window"},
		}})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	var stdout bytes.Buffer
	old := *main.OsStdout
	*main.OsStdout = &stdout
	t.Cleanup(func() { *main.OsStdout = old })
	if code := main.CmdDashboards([]string{"show", "d-1"}); code != 0 {
		t.Fatalf("show = %d", code)
	}
	out := stdout.String()
	for _, s := range []string{"checkout as of 2026-10-22 12:00:00 UTC", "Checkout p95", "182.5 ms", "unavailable: no values pushed in the window"} {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q\nfull: %s", s, out)
		}
	}
}
