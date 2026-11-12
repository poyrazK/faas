package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAlertPresetCatalogHidesOperatorOnlyRows(t *testing.T) {
	e := apidserver.Setup(t, api.PlanScale)
	(*e.Store()).SeedAlertPresetForTest(state.AlertPreset{Name: "api_down", DisplayName: "API down", EnabledInCatalog: true, MinimumPlan: "hobby"})
	(*e.Store()).SeedAlertPresetForTest(state.AlertPreset{Name: "canary_stuck_step", DisplayName: "Canary stuck", EnabledInCatalog: true, MinimumPlan: "scale"})

	rec := e.Do(t, http.MethodGet, "/v1/alert-presets", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got []api.AlertPresetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "api_down" {
		t.Fatalf("customer catalog = %+v, want only api_down", got)
	}
}

func TestAlertPresetOperatorOnlyNameCannotBeEnabled(t *testing.T) {
	e := apidserver.Setup(t, api.PlanScale)
	(*e.Store()).SeedAlertPresetForTest(state.AlertPreset{Name: "canary_stuck_step", EnabledInCatalog: true, MinimumPlan: "scale"})

	_, prob := (*e.S()).LoadAndGateAlertPreset(context.Background(), (*e.Acct()), "canary_stuck_step")
	if prob == nil || prob.Status != http.StatusNotFound {
		t.Fatalf("loadAndGateAlertPreset operator row = %#v, want 404", prob)
	}
}

// adr: 20261109153000 — the operator signals the catalog hides cannot be
// reached by naming them on a rule either: such a rule would stay degraded
// for good and take a slot of the plan's rule limit.
func TestAlertRuleCannotUseAnOperatorOnlyMetric(t *testing.T) {
	e := apidserver.SetupAlerts(t, api.PlanScale)
	apidserver.CreateApp(t, e, "shop")
	req := apidserver.AlertRuleReq()
	req.Metric, req.Comparison, req.Threshold = "canary_fleet_in_flight_high", "gt", 50
	rec := e.Do(t, http.MethodPost, "/v1/apps/shop/alerts", req, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create = %d %s; want 400", rec.Code, rec.Body)
	}
}
