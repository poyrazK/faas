package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCreateSyntheticCheckAlertRule(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	createApp(t, e, "shop")
	createApp(t, e, "other")
	check := func(slug string) string {
		rec := e.do(t, http.MethodPost, "/v1/apps/"+slug+"/synthetics", api.CreateSyntheticCheckRequest{Name: "health", Path: "/", IntervalSeconds: 300}, nil)
		var out api.SyntheticCheckResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != http.StatusCreated {
			t.Fatalf("create check on %s: %d %s", slug, rec.Code, rec.Body)
		}
		return out.ID
	}
	shopCheck, otherCheck := check("shop"), check("other")

	req := alertRuleReq()
	req.Name, req.Metric, req.Comparison, req.Threshold, req.SyntheticCheckID = "health failing", api.AlertRuleMetricSyntheticConsecutiveFailures, "gte", 2, shopCheck
	if created := mustCreateAlertRule(t, e, "shop", req); created.SyntheticCheckID != shopCheck {
		t.Fatalf("created = %+v", created)
	}
	for _, tt := range []struct{ name, metric, id string }{
		{"missing id", api.AlertRuleMetricSyntheticLatencyP95, ""},
		{"another app's check", api.AlertRuleMetricSyntheticConsecutiveFailures, otherCheck},
		{"id on another metric", "latency_p99_ms", shopCheck},
	} {
		bad := alertRuleReq()
		bad.Name, bad.Metric, bad.SyntheticCheckID = "bad "+tt.name, tt.metric, tt.id
		if rec := e.do(t, http.MethodPost, "/v1/apps/shop/alerts", bad, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", tt.name, rec.Code, rec.Body)
		}
	}
}

func TestSyntheticCheckView(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	c := api.SyntheticCheckResponse{Name: "health", Method: "GET", URL: "https://shop.gregale.dev/healthz", IntervalSeconds: 300, Enabled: true}
	if v := syntheticCheckView(c, nil); v.LastResult != "not run yet" || v.LastClass != "dim" {
		t.Fatalf("no runs: %+v", v)
	}
	ok := syntheticCheckView(c, &api.SyntheticCheckResults{Uptime24hPct: f(99.5), P95LatencyMS24h: 840, Recent: []api.SyntheticCheckRun{{OK: true, StatusCode: 200, LatencyMS: 92}}})
	if ok.LastClass != "ok" || ok.Uptime24h != "99.50%" || ok.P95 != "840 ms" || ok.Every != "5 min" {
		t.Fatalf("ok: %+v", ok)
	}
	bad := syntheticCheckView(c, &api.SyntheticCheckResults{Recent: []api.SyntheticCheckRun{{StatusCode: 503, ErrorClass: "status"}}})
	if bad.LastClass != "bad" || bad.LastResult != "FAIL status · 503" {
		t.Fatalf("bad: %+v", bad)
	}
}
