package apidserver_test

// adr: 20261030214500 — deleting an SLO deletes the alert rules built on it;
// each such rule is audited as alert_rule.deleted, naming the SLO.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestDeleteSLOAuditsTheRulesItRemoves(t *testing.T) {
	e := apidserver.SetupAlerts(t, api.PlanPro)
	apidserver.CreateApp(t, e, "shop")
	rec := e.Do(t, http.MethodPost, "/v1/apps/shop/slos", api.CreateSLORequest{Name: "up", SLI: "availability", ObjectivePct: 99.9, WindowDays: 30}, nil)
	var slo api.SLOResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &slo); err != nil || rec.Code != http.StatusCreated {
		t.Fatalf("create SLO: %d %s", rec.Code, rec.Body)
	}
	req := apidserver.AlertRuleReq()
	req.Name, req.Metric, req.Comparison, req.Threshold, req.SLOID = "checkout burning", api.AlertRuleMetricSLOBudgetBurn, "gt", 14.4, slo.ID
	rule := apidserver.MustCreateAlertRule(t, e, "shop", req)

	if rec := e.Do(t, http.MethodDelete, "/v1/apps/shop/slos/"+slo.ID, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete SLO = %d %s", rec.Code, rec.Body)
	}
	if rec := e.Do(t, http.MethodGet, "/v1/apps/shop/alerts/"+rule.ID, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("rule after its SLO was deleted = %d %s; want 404", rec.Code, rec.Body)
	}
	rec = e.Do(t, http.MethodGet, "/v1/audit-events?kind_prefix=alert_rule.deleted", nil, nil)
	var list api.ListAuditEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("list audit events: %d %s", rec.Code, rec.Body)
	}
	for _, ev := range list.Events {
		data := string(ev.Data)
		if strings.Contains(data, rule.ID) && strings.Contains(data, `"cause":"slo"`) && strings.Contains(data, slo.ID) {
			return
		}
	}
	t.Fatalf("no alert_rule.deleted event names rule %s and SLO %s: %+v", rule.ID, slo.ID, list.Events)
}
