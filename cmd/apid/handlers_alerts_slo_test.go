package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCreateSLOAlertRule(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	createApp(t, e, "shop")
	createApp(t, e, "other")
	slo := func(slug string) string {
		rec := e.do(t, http.MethodPost, "/v1/apps/"+slug+"/slos", api.CreateSLORequest{Name: "up", SLI: "availability", ObjectivePct: 99.9, WindowDays: 30}, nil)
		var out api.SLOResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != http.StatusCreated {
			t.Fatalf("create SLO on %s: %d %s", slug, rec.Code, rec.Body)
		}
		return out.ID
	}
	shopSLO, otherSLO := slo("shop"), slo("other")

	req := alertRuleReq()
	req.Name, req.Metric, req.Comparison, req.Threshold, req.SLOID = "checkout burning", api.AlertRuleMetricSLOBudgetBurn, "gt", 14.4, shopSLO
	created := mustCreateAlertRule(t, e, "shop", req)
	if created.SLOID != shopSLO || created.Metric != api.AlertRuleMetricSLOBudgetBurn {
		t.Fatalf("created = %+v", created)
	}

	tests := []struct {
		name   string
		metric string
		sloID  string
	}{
		{"missing slo_id", api.AlertRuleMetricSLOBudgetRemaining, ""},
		{"another app's SLO", api.AlertRuleMetricSLOBudgetBurn, otherSLO},
		{"unknown SLO", api.AlertRuleMetricSLOBudgetBurn, "00000000-0000-0000-0000-000000000000"},
		{"slo_id on another metric", "latency_p99_ms", shopSLO},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bad := alertRuleReq()
			bad.Name, bad.Metric, bad.SLOID = "bad "+tt.name, tt.metric, tt.sloID
			if rec := e.do(t, http.MethodPost, "/v1/apps/shop/alerts", bad, nil); rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body)
			}
		})
	}
}
