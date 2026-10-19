//go:build !no_pg

// adr: 1662
package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestIssueCompoundAlertsHTTPAndDashboardPostgres(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "compound-alerts")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	endpoint := "/v1/apps/" + app.Slug + "/issue-compound-alert-policy"
	policy := apidserver.IssueDecode[api.IssueCompoundAlertPolicy](t, e.Do(t, http.MethodGet, endpoint, nil, nil), 200)
	if policy.Revision != 0 || policy.Rules == nil {
		t.Fatal(policy)
	}
	if w := e.Do(t, http.MethodGet, endpoint, nil, map[string]string{"Authorization": "Bearer " + token.Token}); w.Code != 401 {
		t.Fatal("ingest token managed policy", w.Code)
	}
	rule := api.IssueCompoundAlertRule{Key: "payments", Enabled: true, Environment: "application", MinimumEvents: 20, MinimumCustomers: 3, WindowSeconds: 600, CooldownSeconds: 1800}
	zero := int64(0)
	in := api.UpdateIssueCompoundAlertPolicyRequest{ExpectedRevision: &zero, Rules: []api.IssueCompoundAlertRule{rule}}
	policy = apidserver.IssueDecode[api.IssueCompoundAlertPolicy](t, e.Do(t, http.MethodPut, endpoint, in, nil), 200)
	if policy.Revision != 1 || policy.Rules[0].MinimumCustomers != 3 {
		t.Fatal(policy)
	}
	if w := e.Do(t, http.MethodPut, endpoint, in, nil); w.Code != 409 || !strings.Contains(w.Body.String(), "issue_compound_alert_revision_conflict") {
		t.Fatal("stale write", w.Code, w.Body.String())
	}
	if w := e.Do(t, http.MethodPut, endpoint, api.UpdateIssueCompoundAlertPolicyRequest{ExpectedRevision: &policy.Revision}, nil); w.Code != 400 {
		t.Fatal("null rules", w.Code)
	}
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "CompoundPreview", Message: "customer condition"}
	issue := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/issue-events", event, map[string]string{"Authorization": "Bearer " + token.Token}), 202)
	preview := apidserver.IssueDecode[api.IssueCompoundAlertPreview](t, e.Do(t, http.MethodPost, endpoint+"/preview", api.PreviewIssueCompoundAlertRequest{IssueID: issue.IssueID, Rule: rule}, nil), 200)
	if preview.Allowed || preview.EventsMatched || preview.CustomersMatched || preview.ObservedEvents != 1 || preview.PolicyRevision != 1 {
		t.Fatal(preview)
	}
	if w := e.Do(t, http.MethodPost, endpoint+"/preview", api.PreviewIssueCompoundAlertRequest{IssueID: uuid.NewString(), Rule: rule}, nil); w.Code != 404 {
		t.Fatal("foreign issue", w.Code)
	}
	pageURL := "/dashboard/apps/" + app.Slug + "/issues/compound-alerts"
	r := httptest.NewRequest(http.MethodGet, pageURL, nil)
	e.AddAdminSession(t, r)
	page := httptest.NewRecorder()
	(*e.H()).ServeHTTP(page, r)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Preview unsaved rule") {
		t.Fatal(page.Code, page.Body.String())
	}
	var csrf *http.Cookie
	for _, c := range page.Result().Cookies() {
		if c.Name == "faas_csrf_issue_action" {
			csrf = c
		}
	}
	if csrf == nil {
		t.Fatal("no CSRF")
	}
	form := url.Values{"expected_revision": {"1"}, "key": {"payments"}, "enabled": {"on"}, "environment": {"staging"}, "minimum_events": {"20"}, "minimum_customers": {"3"}, "window_seconds": {"600"}, "cooldown_seconds": {"1800"}, "issue_id": {issue.IssueID}, "action": {"preview"}}
	post := func(withCSRF bool) *httptest.ResponseRecorder {
		t.Helper()
		if withCSRF {
			form.Set("csrf_token", csrf.Value)
		}
		r := httptest.NewRequest(http.MethodPost, pageURL, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		e.AddAdminSession(t, r)
		if withCSRF {
			r.AddCookie(csrf)
			r.Header.Set("X-CSRF-Token", csrf.Value)
		}
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		return w
	}
	if w := post(false); w.Code != 400 {
		t.Fatal("missing CSRF", w.Code)
	}
	if w := post(true); w.Code != 200 || !strings.Contains(w.Body.String(), "environment_excluded") {
		t.Fatal(w.Code, w.Body.String())
	}
	after := apidserver.IssueDecode[api.IssueCompoundAlertPolicy](t, e.Do(t, http.MethodGet, endpoint, nil, nil), 200)
	if after.Revision != 1 || after.Rules[0].Environment != "application" {
		t.Fatal("preview saved draft", after)
	}
	form.Set("action", "save")
	if w := post(true); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := post(true); w.Code != 409 {
		t.Fatal("stale dashboard write", w.Code)
	}
	if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE accounts SET plan='free' WHERE id=$1", app.AccountID); err != nil {
		t.Fatal(err)
	}
	if w := e.Do(t, http.MethodGet, endpoint, nil, nil); w.Code != 402 {
		t.Fatal("plan gate", w.Code)
	}
}
