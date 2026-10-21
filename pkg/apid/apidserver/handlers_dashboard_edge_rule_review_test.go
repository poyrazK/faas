package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261020105001 — the edge-rules page reviews rules from their hourly counts: a
// log-mode rule observed for days is ready to enforce with its matches since
// its last change, a fresh one is still collecting, a rule silent for a week
// is quiet; active enforced and disabled rules are left out. Every rule
// gets a 7-day sparkline, and enforcing a ready rule takes it off the list.
func TestDashboardEdgeRuleReview(t *testing.T) {
	h, cookie, store, sessions := apidserver.NewAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "review-app", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	now := time.Now().UTC()
	rule := func(name, mode string, enabled bool, changed time.Duration) string {
		t.Helper()
		r, err := store.CreateEdgeRule(t.Context(), state.CreateEdgeRuleParams{
			AccountID: acct.ID, AppID: app.ID, MatchHost: api.EdgeRuleAppHosts, MatchPath: "/" + strings.ToLower(strings.Fields(name)[0]) + "/**",
			Enabled: enabled, Kind: state.EdgeRuleKindIP, Name: name, Mode: mode,
		})
		if err != nil {
			t.Fatalf("CreateEdgeRule(%s): %v", name, err)
		}
		store.SetEdgeRuleUpdatedAtForTest(r.ID, now.Add(-changed))
		return r.ID
	}
	ready := rule("Ready rule", state.EdgeRuleModeLog, true, 72*time.Hour)
	rule("Fresh rule", state.EdgeRuleModeLog, true, 2*time.Hour)
	rule("Dead rule", state.EdgeRuleModeEnforce, true, 10*24*time.Hour)
	busy := rule("Busy rule", state.EdgeRuleModeEnforce, true, 10*24*time.Hour)
	rule("Off rule", state.EdgeRuleModeLog, false, 72*time.Hour)
	if err := store.RecordEdgeRuleHits(t.Context(), []state.EdgeRuleHit{
		{RuleID: ready, AppID: app.ID, Bucket: now.Add(-time.Hour), Outcome: state.EdgeRuleHitLogged, Hits: 10},
		{RuleID: ready, AppID: app.ID, Bucket: now.Add(-30 * time.Hour), Outcome: state.EdgeRuleHitLogged, Hits: 30},
		{RuleID: ready, AppID: app.ID, Bucket: now.Add(-100 * time.Hour), Outcome: state.EdgeRuleHitLogged, Hits: 500},
		{RuleID: busy, AppID: app.ID, Bucket: now, Outcome: state.EdgeRuleHitMatched, Hits: 5},
	}); err != nil {
		t.Fatalf("RecordEdgeRuleHits: %v", err)
	}
	page := func() (string, string) {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/review-app/edge-rules", nil)
		req.AddCookie(cookie)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("page status = %d", rec.Code)
		}
		body := rec.Body.String()
		start := strings.Index(body, `id="rule-review"`)
		if start < 0 {
			return body, ""
		}
		end := strings.Index(body[start:], "<h2>Rules</h2>")
		if end < 0 {
			t.Fatal("rule review not followed by the rules table")
		}
		return body, body[start : start+end]
	}

	body, review := page()
	for _, want := range []string{
		"Ready rule", "Matched <strong>40</strong> requests in log mode", "/edge-rules/" + ready + "/mode", "Start enforcing",
		"Fresh rule", "In log mode for 2 h", "Dead rule", "No matches in 7 days",
	} {
		if !strings.Contains(review, want) {
			t.Errorf("review missing %q:\n%s", want, review)
		}
	}
	if strings.Contains(review, "Busy rule") || strings.Contains(review, "Off rule") {
		t.Errorf("review lists an active enforced or disabled rule:\n%s", review)
	}
	if !(strings.Index(review, "Ready rule") < strings.Index(review, "Fresh rule") && strings.Index(review, "Fresh rule") < strings.Index(review, "Dead rule")) {
		t.Errorf("review order is not ready, collecting, quiet:\n%s", review)
	}
	if n := strings.Count(body, `<svg class="sparkline"`); n != 5 {
		t.Errorf("sparklines = %d, want one per rule", n)
	}
	for _, want := range []string{`aria-label="540 matches in the last 7 days"`, `aria-label="5 matches in the last 7 days"`, "10 logged"} {
		if !strings.Contains(body, want) {
			t.Errorf("rules table missing %q", want)
		}
	}

	token, err := middleware.IssueForAuthenticatedNamed(sessions, apidserver.DashboardEdgeRulesAction, acct.ID, apidserver.DashboardEdgeRulesCSRFCookie)
	if err != nil {
		t.Fatalf("IssueForAuthenticatedNamed: %v", err)
	}
	form := map[string]string{middleware.FormFieldName: token, "mode": "enforce"}
	rec := apidserver.DashboardPOST(t, h, cookie, "/dashboard/apps/review-app/edge-rules/"+ready+"/mode", form, &http.Cookie{Name: apidserver.DashboardEdgeRulesCSRFCookie, Value: token})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("start enforcing: %d %s", rec.Code, rec.Body.String())
	}
	if _, review := page(); strings.Contains(review, "Ready rule") {
		t.Fatalf("enforced rule still in the review:\n%s", review)
	}
}
