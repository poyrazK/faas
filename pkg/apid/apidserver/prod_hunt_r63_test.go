package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// Prod hunt r63: `error_rate_pct gt 500` and `latency_p95_ms lt -1` were
// saved and could never fire. Create and update refuse such thresholds.
func TestAlertRuleRefusesThresholdsThatNeverFire(t *testing.T) {
	e := apidserver.SetupAlerts(t, api.PlanPro)
	apidserver.CreateApp(t, e, "shop")
	for _, tc := range []struct {
		metric, comparison string
		threshold          float64
	}{
		{"error_rate_pct", "gt", 500},
		{"latency_p95_ms", "lt", -1},
	} {
		req := apidserver.AlertRuleReq()
		req.Name, req.Metric, req.Comparison, req.Threshold = "never "+tc.metric, tc.metric, tc.comparison, tc.threshold
		rec := e.Do(t, http.MethodPost, "/v1/apps/shop/alerts", req, nil)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "never fires") {
			t.Fatalf("%s %s %g: %d %s, want 400 never fires", tc.metric, tc.comparison, tc.threshold, rec.Code, rec.Body)
		}
	}
	req := apidserver.AlertRuleReq()
	req.Name, req.Metric, req.Comparison, req.Threshold = "reachable", "error_rate_pct", "gt", 5
	rec := e.Do(t, http.MethodPost, "/v1/apps/shop/alerts", req, nil)
	var rule api.AlertRuleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &rule); err != nil || rec.Code != http.StatusCreated {
		t.Fatalf("reachable rule: %d %s", rec.Code, rec.Body)
	}
	bad := 150.0
	rec = e.Do(t, http.MethodPatch, "/v1/apps/shop/alerts/"+rule.ID, api.UpdateAlertRuleRequest{Threshold: &bad}, nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "never fires") {
		t.Fatalf("update to gt 150: %d %s, want 400 never fires", rec.Code, rec.Body)
	}
}
