package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

// releaseEdgeFeatures releases every edge feature platform flag
// (ADR-20261023113721) to every account of a test server.
func releaseEdgeFeatures(e apidserver.TestEnv) {
	for _, flag := range platformflags.EdgeFeatures {
		(*e.S()).SeedPlatformFlag(flag, platformflags.Definition{Enabled: true, Default: true})
	}
}

// adr: 20261023113721 — challenge rules, WAF warn and block, and penalties
// cannot be configured until their platform flag includes the account;
// withdrawing a flag never strands a rule that already enforces one.
func TestEdgeFeatureReleaseGate(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	slug := apidserver.MustSeedEdgeRuleApp(t, e, "edge-release")
	create := func(req api.CreateEdgeRuleRequest) *httptest.ResponseRecorder {
		return e.Do(t, http.MethodPost, "/v1/apps/"+slug+"/edge-rules", req, nil)
	}
	patch := func(id string, req api.UpdateEdgeRuleRequest) *httptest.ResponseRecorder {
		return e.Do(t, http.MethodPatch, "/v1/edge-rules/"+id, req, nil)
	}
	decode := func(rec *httptest.ResponseRecorder, want int) api.EdgeRuleResponse {
		t.Helper()
		var out api.EdgeRuleResponse
		if rec.Code != want || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("status = %d, want %d; body = %s", rec.Code, want, rec.Body)
		}
		return out
	}
	unreleased := func(rec *httptest.ResponseRecorder, feature string) {
		t.Helper()
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"code":"`+api.CodeEdgeFeatureUnavailable+`"`) ||
			!strings.Contains(rec.Body.String(), feature) {
			t.Fatalf("want 422 %s naming %q; got %d %s", api.CodeEdgeFeatureUnavailable, feature, rec.Code, rec.Body)
		}
	}
	challenge := api.CreateEdgeRuleRequest{MatchHost: "api.example.com", MatchPath: "/login", Kind: "challenge", Action: json.RawMessage(`{}`)}
	penalized := api.CreateEdgeRuleRequest{MatchHost: "api.example.com", MatchPath: "/", Kind: "ip",
		Action: json.RawMessage(`{"deny":["203.0.113.0/24"]}`), Penalty: &api.EdgeRulePenalty{Seconds: 600}}

	unreleased(create(challenge), "Challenge rules")
	unreleased(create(wafEdgeRuleRequest(`{"mode":"block"}`)), "WAF warn and block modes")
	unreleased(create(wafEdgeRuleRequest(`{"mode":"warn"}`)), "WAF warn and block modes")
	unreleased(create(penalized), "Edge rule penalties")
	// Observe-mode WAF and rules without a penalty are not gated, but
	// switching a rule onto a gated feature is.
	observe := decode(create(wafEdgeRuleRequest(`{"mode":"observe"}`)), http.StatusCreated)
	block := json.RawMessage(`{"mode":"block"}`)
	unreleased(patch(observe.ID, api.UpdateEdgeRuleRequest{Action: &block}), "WAF warn and block modes")
	plain := penalized
	plain.Penalty = nil
	ip := decode(create(plain), http.StatusCreated)
	unreleased(patch(ip.ID, api.UpdateEdgeRuleRequest{Penalty: &api.EdgeRulePenalty{Seconds: 600}}), "Edge rule penalties")
	// A disabled rule may take a penalty; enabling it is the switch.
	off, on := false, true
	decode(patch(ip.ID, api.UpdateEdgeRuleRequest{Enabled: &off}), http.StatusOK)
	decode(patch(ip.ID, api.UpdateEdgeRuleRequest{Penalty: &api.EdgeRulePenalty{Seconds: 600}}), http.StatusOK)
	unreleased(patch(ip.ID, api.UpdateEdgeRuleRequest{Enabled: &on}), "Edge rule penalties")

	releaseEdgeFeatures(e)
	ch := decode(create(challenge), http.StatusCreated)
	pen := decode(create(penalized), http.StatusCreated)
	decode(patch(observe.ID, api.UpdateEdgeRuleRequest{Action: &block}), http.StatusOK)

	for _, flag := range platformflags.EdgeFeatures {
		(*e.S()).SeedPlatformFlag(flag, platformflags.Definition{})
	}
	name := "login challenge"
	decode(patch(ch.ID, api.UpdateEdgeRuleRequest{Name: &name}), http.StatusOK)
	decode(patch(ch.ID, api.UpdateEdgeRuleRequest{Enabled: &off}), http.StatusOK)
	unreleased(patch(ch.ID, api.UpdateEdgeRuleRequest{Enabled: &on}), "Challenge rules")
	decode(patch(pen.ID, api.UpdateEdgeRuleRequest{Penalty: &api.EdgeRulePenalty{Seconds: 900}}), http.StatusOK)
	decode(patch(pen.ID, api.UpdateEdgeRuleRequest{ClearPenalty: true}), http.StatusOK)
	unreleased(patch(pen.ID, api.UpdateEdgeRuleRequest{Penalty: &api.EdgeRulePenalty{Seconds: 600}}), "Edge rule penalties")
	observeAction := json.RawMessage(`{"mode":"observe"}`)
	decode(patch(observe.ID, api.UpdateEdgeRuleRequest{Action: &observeAction}), http.StatusOK)
	unreleased(patch(observe.ID, api.UpdateEdgeRuleRequest{Action: &block}), "WAF warn and block modes")
	if rec := e.Do(t, http.MethodDelete, "/v1/edge-rules/"+ch.ID, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete challenge rule: %d %s", rec.Code, rec.Body)
	}
}
