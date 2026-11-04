package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// adr: 091 — amendment 2026-11-04 (review round 10): a cors rule written
// through the API keeps its preset, a preset rules use cannot be deleted, a
// rule cannot name a preset its app may not use, and the account export
// carries the presets.
func TestCorsPresetRulesKeepTheirPreset(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "preset-app")
	r := e.Do(t, http.MethodPost, "/v1/cors-presets", api.CreateCorsPresetRequest{Name: "web", AllowOrigins: []string{"https://a.example.com"}, AllowMethods: []string{"GET"}}, nil)
	var preset api.CorsPresetResponse
	if r.Code != http.StatusCreated || json.Unmarshal(r.Body.Bytes(), &preset) != nil {
		t.Fatalf("create preset = %d %s", r.Code, r.Body)
	}
	rule := func(presetID string) *httptest.ResponseRecorder {
		return e.Do(t, http.MethodPost, "/v1/apps/preset-app/edge-rules", api.CreateEdgeRuleRequest{MatchHost: "@app", MatchPath: "/**", Kind: "cors",
			Action: json.RawMessage(`{"cors_preset_id":"` + presetID + `"}`)}, nil)
	}
	created := rule(preset.ID)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), preset.ID) {
		t.Fatalf("rule naming the preset = %d %s, want 201 keeping cors_preset_id", created.Code, created.Body)
	}
	using, err := (*e.Store()).ListEdgeRulesUsingCorsPreset(t.Context(), (*e.Acct()).ID, preset.ID)
	if err != nil || len(using) != 1 {
		t.Fatalf("rules using the preset = %d (%v), want 1", len(using), err)
	}
	if r := rule("00000000-0000-0000-0000-000000000009"); r.Code != http.StatusUnprocessableEntity && r.Code != http.StatusBadRequest {
		t.Fatalf("rule naming a missing preset = %d %s, want a validation refusal", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodDelete, "/v1/cors-presets/"+preset.ID, nil, nil); r.Code != http.StatusConflict || !strings.Contains(r.Body.String(), using[0].ID) {
		t.Fatalf("deleting a preset in use = %d %s, want 409 naming the rule", r.Code, r.Body)
	}
	export := e.Do(t, http.MethodGet, "/v1/account/export?include_secrets=false", nil, nil)
	if export.Code != http.StatusOK || !strings.Contains(export.Body.String(), `"cors_presets"`) || !strings.Contains(export.Body.String(), preset.ID) {
		t.Fatalf("account export = %d, want its cors_presets to carry the preset", export.Code)
	}
}
