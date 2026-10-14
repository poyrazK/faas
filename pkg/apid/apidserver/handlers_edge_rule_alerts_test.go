package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// adr: 1192 — edge-rule activity alerts accept a rule of the same app,
// hourly windows and webhook only; the rule id round-trips and the metric
// family cannot change on update.
func TestEdgeRuleActivityAlerts(t *testing.T) {
	e := apidserver.SetupAlerts(t, api.PlanPro)
	slug := apidserver.MustSeedEdgeRuleApp(t, e, "edge-alerts")
	other := apidserver.MustSeedEdgeRuleApp(t, e, "edge-alerts-other")
	createEdgeRule := func(app string) api.EdgeRuleResponse {
		t.Helper()
		rec := e.Do(t, "POST", "/v1/apps/"+app+"/edge-rules", apidserver.EdgeRuleRouteReq(app), nil)
		var out api.EdgeRuleResponse
		if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("create edge rule: %d %s", rec.Code, rec.Body.String())
		}
		return out
	}
	createAlert := func(req api.CreateAlertRuleRequest) api.AlertRuleResponse {
		t.Helper()
		rec := e.Do(t, "POST", "/v1/apps/"+slug+"/alerts", req, nil)
		var out api.AlertRuleResponse
		if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("create alert: %d %s", rec.Code, rec.Body.String())
		}
		return out
	}
	rule := createEdgeRule(slug)
	foreign := createEdgeRule(other)

	req := apidserver.AlertRuleReq()
	req.Name, req.Metric, req.WindowSpec, req.Threshold, req.EdgeRuleID = "block spike", "edge_rule_hits", "1h", 1000, rule.ID
	created := createAlert(req)
	if created.EdgeRuleID != rule.ID || created.Metric != "edge_rule_hits" {
		t.Fatalf("created = %+v, want edge_rule_id %s", created, rule.ID)
	}

	appWide := apidserver.AlertRuleReq()
	appWide.Name, appWide.Metric, appWide.WindowSpec = "log-mode would act", "edge_rule_log_hits", "24h"
	if got := createAlert(appWide); got.EdgeRuleID != "" {
		t.Fatalf("app-wide alert carries edge_rule_id %q", got.EdgeRuleID)
	}

	rollback := "rollback"
	for name, tc := range map[string]struct {
		mutate func(*api.CreateAlertRuleRequest)
		status int
	}{
		"sub-hour window":    {func(r *api.CreateAlertRuleRequest) { r.WindowSpec = "15m" }, http.StatusBadRequest},
		"beyond retention":   {func(r *api.CreateAlertRuleRequest) { r.WindowSpec = "15d" }, http.StatusBadRequest},
		"rollback action":    {func(r *api.CreateAlertRuleRequest) { r.Action = &rollback }, http.StatusBadRequest},
		"malformed rule id":  {func(r *api.CreateAlertRuleRequest) { r.EdgeRuleID = "not-a-uuid" }, http.StatusBadRequest},
		"other app's rule":   {func(r *api.CreateAlertRuleRequest) { r.EdgeRuleID = foreign.ID }, http.StatusNotFound},
		"rule id on latency": {func(r *api.CreateAlertRuleRequest) { r.Metric, r.WindowSpec = "latency_p99_ms", "5m" }, http.StatusBadRequest},
		"unknown rule id":    {func(r *api.CreateAlertRuleRequest) { r.EdgeRuleID = "33333333-3333-4333-8333-333333333333" }, http.StatusNotFound},
	} {
		bad := req
		bad.Name = "bad " + name
		tc.mutate(&bad)
		if rec := e.Do(t, "POST", "/v1/apps/"+slug+"/alerts", bad, nil); rec.Code != tc.status {
			t.Errorf("%s: status %d, want %d: %s", name, rec.Code, tc.status, rec.Body.String())
		}
	}

	logHits := "edge_rule_log_hits"
	if rec := e.Do(t, "PATCH", "/v1/apps/"+slug+"/alerts/"+created.ID, api.UpdateAlertRuleRequest{Metric: &logHits}, nil); rec.Code != http.StatusOK {
		t.Fatalf("switch within family: %d %s", rec.Code, rec.Body.String())
	}
	latency := "latency_p99_ms"
	rec := e.Do(t, "PATCH", "/v1/apps/"+slug+"/alerts/"+created.ID, api.UpdateAlertRuleRequest{Metric: &latency}, nil)
	apidserver.AssertProblem(t, rec, 400, api.CodeAlertRuleInvalid)
	short := "5m"
	rec = e.Do(t, "PATCH", "/v1/apps/"+slug+"/alerts/"+created.ID, api.UpdateAlertRuleRequest{WindowSpec: &short}, nil)
	apidserver.AssertProblem(t, rec, 400, api.CodeAlertRuleInvalid)
}
