package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261023113721 — rule responses name the gated features the account
// has not been released, so a rule that went inert says so; releasing the
// flag clears it.
func TestEdgeRuleResponsesNameUnreleasedFeatures(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	slug := apidserver.MustSeedEdgeRuleApp(t, e, "unreleased")
	releaseEdgeFeatures(e)
	rec := e.Do(t, http.MethodPost, "/v1/apps/"+slug+"/edge-rules", api.CreateEdgeRuleRequest{
		MatchHost: "api.example.com", MatchPath: "/login", Kind: "challenge", Action: json.RawMessage(`{}`)}, nil)
	var created api.EdgeRuleResponse
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &created) != nil || len(created.UnreleasedFeatures) != 0 {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	for _, flag := range platformflags.EdgeFeatures {
		(*e.S()).SeedPlatformFlag(flag, platformflags.Definition{})
	}
	get := func(path string) []byte {
		t.Helper()
		rec := e.Do(t, http.MethodGet, path, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, rec.Code, rec.Body)
		}
		return rec.Body.Bytes()
	}
	var one api.EdgeRuleResponse
	if err := json.Unmarshal(get("/v1/edge-rules/"+created.ID), &one); err != nil || !slices.Equal(one.UnreleasedFeatures, []string{api.EdgeFeatureChallenge}) {
		t.Fatalf("get: unreleased = %v, %v", one.UnreleasedFeatures, err)
	}
	var list []api.EdgeRuleResponse
	if err := json.Unmarshal(get("/v1/apps/"+slug+"/edge-rules"), &list); err != nil || len(list) != 1 || !slices.Equal(list[0].UnreleasedFeatures, []string{api.EdgeFeatureChallenge}) {
		t.Fatalf("list: %+v, %v", list, err)
	}
	releaseEdgeFeatures(e)
	if body := get("/v1/edge-rules/" + created.ID); strings.Contains(string(body), "unreleased_features") {
		t.Fatalf("released rule still reports unreleased features: %s", body)
	}
}

// adr: 20261023113721 — with the flags off, the dashboard marks an inert
// challenge rule, says challenge recipes are not yet available, and leaves
// the penalty field out of recipe forms.
func TestDashboardEdgeRulesUnreleasedFeatures(t *testing.T) {
	h, cookie, store, _ := apidserver.NewAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountPlan(t.Context(), acct.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "inert-app", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateEdgeRule(t.Context(), state.CreateEdgeRuleParams{
		AccountID: acct.ID, AppID: app.ID, MatchHost: api.EdgeRuleAppHosts, MatchPath: "/login", Priority: 10, Enabled: true,
		Kind: state.EdgeRuleKindChallenge, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindChallenge, Challenge: &state.EdgeRuleChallengeAction{Difficulty: 16, PassTTLSeconds: 3600}},
	}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/inert-app/edge-rules", nil)
	req.AddCookie(cookie)
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("page status = %d", rec.Code)
	}
	for _, want := range []string{"not enforced: challenge rules not yet available", "Challenge rules are not yet available to this account."} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	for _, unwanted := range []string{`name="recipe" value="challenge-path"`, `name="penalty_minutes"`} {
		if strings.Contains(body, unwanted) {
			t.Errorf("page offers %s while unreleased", unwanted)
		}
	}
}
