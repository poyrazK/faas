package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAlertPresetCatalogHidesOperatorOnlyRows(t *testing.T) {
	e := setup(t, api.PlanScale)
	e.store.SeedAlertPresetForTest(state.AlertPreset{Name: "api_down", DisplayName: "API down", EnabledInCatalog: true, MinimumPlan: "hobby"})
	e.store.SeedAlertPresetForTest(state.AlertPreset{Name: "canary_stuck_step", DisplayName: "Canary stuck", EnabledInCatalog: true, MinimumPlan: "scale"})

	rec := e.do(t, http.MethodGet, "/v1/alert-presets", nil, nil)
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
	e := setup(t, api.PlanScale)
	e.store.SeedAlertPresetForTest(state.AlertPreset{Name: "canary_stuck_step", EnabledInCatalog: true, MinimumPlan: "scale"})

	_, prob := e.s.loadAndGateAlertPreset(context.Background(), e.acct, "canary_stuck_step")
	if prob == nil || prob.Status != http.StatusNotFound {
		t.Fatalf("loadAndGateAlertPreset operator row = %#v, want 404", prob)
	}
}
