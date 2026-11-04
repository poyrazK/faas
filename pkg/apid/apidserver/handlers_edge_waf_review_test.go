package apidserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func wafReviewRuleParams(accountID, appID string) state.CreateEdgeRuleParams {
	return state.CreateEdgeRuleParams{
		AccountID: accountID, AppID: appID, MatchHost: "api.example.com", MatchPath: "/*", Priority: 10, Enabled: true,
		Name: "waf", Kind: state.EdgeRuleKindWAF,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindWAF, WAF: &state.EdgeRuleWAFAction{
			Mode: api.EdgeWAFModeObserve, ParanoiaLevel: 1, AnomalyThreshold: api.EdgeWAFDefaultAnomalyThreshold,
			InspectBodyBytes: api.EdgeWAFDefaultInspectBodyBytes, ExcludeRuleIDs: []int{920350}}},
	}
}

// graphqlDetections are three clients' detections of a GraphQL query in the
// URL, the documented false positive CRS 942190.
func graphqlDetections(ruleID, appID string) []state.EdgeRuleEvent {
	var out []state.EdgeRuleEvent
	for i := range 3 {
		out = append(out, state.EdgeRuleEvent{RuleID: ruleID, AppID: appID, OccurredAt: time.Now().UTC().Add(-time.Duration(i) * time.Minute),
			Outcome: state.EdgeRuleEventDetected, Method: "GET", Host: "api.example.com", Path: "/graphql",
			ClientIP: fmt.Sprintf("198.51.100.%d", i+1), RequestID: fmt.Sprintf("req-%d", i), WAFRuleIDs: []int{942190}, WAFScore: 5})
	}
	return out
}

// adr: 831 — amendment 13 over HTTP: the review groups an app's WAF
// detections and assesses them; waf-exclusions adds a CRS rule, keeps the
// rest of the action, and the next review shows the group excluded.
func TestEdgeWAFReviewAndExclusionsAPI(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanPro)
	slug := apidserver.MustSeedEdgeRuleApp(t, e, "wafreview")
	store := *e.Store()
	app, err := store.AppBySlug(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := store.CreateEdgeRule(ctx, wafReviewRuleParams(app.AccountID, app.ID))
	if err != nil {
		t.Fatal(err)
	}
	var events state.EdgeRuleEventStore = store
	if err := events.RecordEdgeRuleEvents(ctx, graphqlDetections(rule.ID, app.ID)); err != nil {
		t.Fatal(err)
	}

	review := func() api.EdgeWAFReviewGroup {
		t.Helper()
		rec := e.Do(t, "GET", "/v1/apps/"+slug+"/edge-rules/waf-review", nil, nil)
		var out api.EdgeWAFReviewResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || len(out.Groups) != 1 || out.Scanned != 3 {
			t.Fatalf("review: %d %s", rec.Code, rec.Body.String())
		}
		return out.Groups[0]
	}
	if g := review(); g.CRSRuleID != 942190 || g.Path != "/graphql" || g.Clients != 3 || g.Assessment != api.EdgeWAFReviewLikelyFalsePositive || g.RuleName != "waf" {
		t.Fatalf("group = %+v", g)
	}
	if rec := e.Do(t, "GET", "/v1/apps/"+slug+"/edge-rules/waf-review?rule=nope", nil, nil); rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed rule filter: %d", rec.Code)
	}

	rec := e.Do(t, "POST", "/v1/edge-rules/"+rule.ID+"/waf-exclusions", api.EdgeWAFExclusionsRequest{Add: []int{942190}}, nil)
	var updated api.EdgeRuleResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &updated) != nil {
		t.Fatalf("exclusions: %d %s", rec.Code, rec.Body.String())
	}
	var stored struct {
		WAF api.EdgeRuleWAFAction `json:"waf"`
	}
	if json.Unmarshal(updated.Action, &stored) != nil || stored.WAF.Mode != api.EdgeWAFModeObserve || !slices.Equal(stored.WAF.ExcludeRuleIDs, []int{920350, 942190}) {
		t.Fatalf("action = %s", updated.Action)
	}
	if g := review(); g.Assessment != api.EdgeWAFReviewExcluded || !g.Excluded {
		t.Fatalf("after the exclusion: %+v", g)
	}
	if rec := e.Do(t, "POST", "/v1/edge-rules/"+rule.ID+"/waf-exclusions", api.EdgeWAFExclusionsRequest{}, nil); rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("empty exclusions request: %d", rec.Code)
	}
}

// adr: 831 — amendment 13 on the dashboard: the WAF review shows only for
// apps with a kind=waf rule, lists each group with an exclude button, and
// the button adds the CRS rule behind the page's CSRF token.
func TestDashboardEdgeWAFReview(t *testing.T) {
	ctx := context.Background()
	h, cookie, store, sessions := apidserver.NewAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(ctx, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "edge-waf", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	get := func() string {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/edge-waf/edge-rules", nil)
		req.AddCookie(cookie)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET = %d\n%s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	if strings.Contains(get(), `id="waf-review"`) {
		t.Fatal("WAF review shown for an app without a kind=waf rule")
	}
	rule, err := store.CreateEdgeRule(ctx, wafReviewRuleParams(acct.ID, app.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordEdgeRuleEvents(ctx, graphqlDetections(rule.ID, app.ID)); err != nil {
		t.Fatal(err)
	}
	body := get()
	for _, want := range []string{`id="waf-review"`, "likely false positive", "942190", "/graphql", "Exclude 942190", "3 detections since"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}

	path := "/dashboard/apps/edge-waf/edge-rules/" + rule.ID + "/waf-exclusions"
	if rec := apidserver.DashboardPOST(t, h, cookie, path, map[string]string{"crs_rule_id": "942190"}); rec.Code == http.StatusSeeOther {
		t.Fatal("exclude without a CSRF token must be refused")
	}
	token, err := middleware.IssueForAuthenticatedNamed(sessions, apidserver.DashboardEdgeRulesAction, acct.ID, apidserver.DashboardEdgeRulesCSRFCookie)
	if err != nil {
		t.Fatal(err)
	}
	rec := apidserver.DashboardPOST(t, h, cookie, path, map[string]string{middleware.FormFieldName: token, "crs_rule_id": "942190"},
		&http.Cookie{Name: apidserver.DashboardEdgeRulesCSRFCookie, Value: token})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "action=waf_excluded") {
		t.Fatalf("exclude = %d %q\n%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	updated, err := store.GetEdgeRuleByID(ctx, rule.ID)
	if err != nil || updated.Action.WAF == nil || !slices.Contains(updated.Action.WAF.ExcludeRuleIDs, 942190) {
		t.Fatalf("rule after exclude = %+v, %v", updated.Action.WAF, err)
	}
}
