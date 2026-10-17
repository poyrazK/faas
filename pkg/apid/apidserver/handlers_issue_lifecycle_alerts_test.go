//go:build !no_pg

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

func TestIssueLifecycleAlertsHTTPAndDashboardPostgres(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "lifecycle-alerts")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	endpoint := "/v1/apps/" + app.Slug + "/issue-lifecycle-alert-policy"
	policy := apidserver.IssueDecode[api.IssueLifecycleAlertPolicy](t, e.Do(t, http.MethodGet, endpoint, nil, nil), 200)
	if policy.Revision != 0 || policy.Environments == nil || policy.CooldownSeconds != 0 {
		t.Fatal(policy)
	}
	unauth := httptest.NewRecorder()
	(*e.H()).ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, endpoint, nil))
	if unauth.Code != 401 {
		t.Fatal("unauthenticated policy read", unauth.Code)
	}
	if w := e.Do(t, http.MethodGet, endpoint, nil, map[string]string{"Authorization": "Bearer " + token.Token}); w.Code != 401 {
		t.Fatal("ingest credential could read policy", w.Code)
	}
	if w := e.Do(t, http.MethodPut, endpoint, api.UpdateIssueLifecycleAlertPolicyRequest{Environments: []string{}}, nil); w.Code != 400 {
		t.Fatal("missing revision", w.Code, w.Body.String())
	}
	zero := int64(0)
	in := api.UpdateIssueLifecycleAlertPolicyRequest{ExpectedRevision: &zero, Environments: []string{"application"}, CooldownSeconds: 60}
	policy = apidserver.IssueDecode[api.IssueLifecycleAlertPolicy](t, e.Do(t, http.MethodPut, endpoint, in, nil), 200)
	if policy.Revision != 1 {
		t.Fatal(policy)
	}
	if w := e.Do(t, http.MethodPut, endpoint, in, nil); w.Code != 409 {
		t.Fatal("stale write", w.Code, w.Body.String())
	}
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "PreviewError", Message: "lifecycle preview"}
	issue := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/issue-events", event, map[string]string{"Authorization": "Bearer " + token.Token}), 202)
	previewIn := api.PreviewIssueLifecycleAlertRequest{IssueID: issue.IssueID, Event: "regressed", Environments: []string{"staging"}, CooldownSeconds: 60}
	preview := apidserver.IssueDecode[api.IssueLifecycleAlertPreview](t, e.Do(t, http.MethodPost, endpoint+"/preview", previewIn, nil), 200)
	if preview.Allowed || preview.Reason != "environment_excluded" || preview.PolicyRevision != 1 {
		t.Fatal(preview)
	}
	previewIn.Event = "resolved"
	if w := e.Do(t, http.MethodPost, endpoint+"/preview", previewIn, nil); w.Code != 400 {
		t.Fatal("unsupported preview event", w.Code)
	}
	previewIn.Event = "regressed"
	previewIn.IssueID = uuid.NewString()
	if w := e.Do(t, http.MethodPost, endpoint+"/preview", previewIn, nil); w.Code != 404 {
		t.Fatal("foreign/missing issue preview", w.Code)
	}
	pageURL := "/dashboard/apps/" + app.Slug + "/issues/lifecycle-alerts"
	r := httptest.NewRequest(http.MethodGet, pageURL, nil)
	e.AddAdminSession(t, r)
	page := httptest.NewRecorder()
	(*e.H()).ServeHTTP(page, r)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Preview policy") {
		t.Fatal(page.Code, page.Body.String())
	}
	var csrf *http.Cookie
	for _, c := range page.Result().Cookies() {
		if c.Name == "faas_csrf_issue_action" {
			csrf = c
		}
	}
	if csrf == nil {
		t.Fatal("no CSRF cookie")
	}
	form := url.Values{"expected_revision": {"1"}, "environments": {"staging"}, "cooldown_seconds": {"120"}, "issue_id": {issue.IssueID}, "event": {"regressed"}, "action": {"preview"}}
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
		t.Fatal("missing CSRF accepted", w.Code)
	}
	if w := post(true); w.Code != 200 || !strings.Contains(w.Body.String(), "Unsaved draft") || !strings.Contains(w.Body.String(), `value="120"`) || !strings.Contains(w.Body.String(), "excludes the issue") {
		t.Fatal(w.Code, w.Body.String())
	}
	after := apidserver.IssueDecode[api.IssueLifecycleAlertPolicy](t, e.Do(t, http.MethodGet, endpoint, nil, nil), 200)
	if after.Revision != 1 || after.Environments[0] != "application" || after.CooldownSeconds != 60 {
		t.Fatal("preview changed policy", after)
	}
	form.Set("action", "save")
	if w := post(true); w.Code != 303 {
		t.Fatal("dashboard save", w.Code, w.Body.String())
	}
	if w := post(true); w.Code != 409 {
		t.Fatal("dashboard stale save", w.Code, w.Body.String())
	}
	if _, err := (*e.Pool()).Exec(t.Context(), "UPDATE accounts SET plan='free' WHERE id=$1", app.AccountID); err != nil {
		t.Fatal(err)
	}
	if w := e.Do(t, http.MethodGet, endpoint, nil, nil); w.Code != 402 {
		t.Fatal("plan gate", w.Code)
	}
}
