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
	"github.com/onebox-faas/faas/pkg/state"
)

func TestNativeApplicationResponseInboxDashboardPostgres(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "response-inbox")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	response := e.Do(t, "POST", "/v1/apps/"+app.Slug+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "InboxError", Message: "private-inbox-evidence", SourceKind: "exception"}, map[string]string{"Authorization": "Bearer " + token.Token})
	if response.Code != 202 {
		t.Fatal(response.Code, response.Body.String())
	}
	st := state.NewPgStore(*e.Pool())
	issues, err := st.ListIssues(t.Context(), app.ID, state.IssueListFilter{}, state.IssueCursor{})
	if err != nil || len(issues.Items) != 1 {
		t.Fatal(issues, err)
	}
	id := issues.Items[0].ID
	pageURL := "/dashboard/apps/" + app.Slug + "/issues/responses"
	get := func(query string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", pageURL+query, nil)
		e.AddAdminSession(t, r)
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		return w
	}
	page := get("")
	if page.Code != 200 || !strings.Contains(page.Body.String(), id) || !strings.Contains(page.Body.String(), "Acknowledge") || strings.Contains(page.Body.String(), "private-inbox-evidence") {
		t.Fatal(page.Code, page.Body.String())
	}
	var csrf *http.Cookie
	for _, c := range page.Result().Cookies() {
		if c.Name == apidserver.DashboardIssueCookie {
			csrf = c
		}
	}
	if csrf == nil {
		t.Fatal("missing CSRF")
	}
	for _, query := range []string{"?status=unknown", "?team=foreign", "?after=bad"} {
		if w := get(query); w.Code != 400 {
			t.Fatal("invalid filter", w.Code, w.Body.String())
		}
	}
	post := func(form url.Values, valid bool) *httptest.ResponseRecorder {
		form.Set("return_to", "responses")
		if valid {
			form.Set("csrf_token", csrf.Value)
		}
		r := httptest.NewRequest("POST", "/dashboard/apps/"+app.Slug+"/issues/"+id+"/triage", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(csrf)
		e.AddAdminSession(t, r)
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		return w
	}
	if w := post(url.Values{"action": {"acknowledge"}, "expected_revision": {"0"}}, false); w.Code != 400 {
		t.Fatal("CSRF", w.Code)
	}
	if w := post(url.Values{"action": {"acknowledge"}, "expected_revision": {"0"}, "status": {"unknown"}}, true); w.Code != 400 {
		t.Fatal("invalid redirect filter", w.Code)
	}
	until := time.Now().UTC().Add(time.Hour).Format("2006-01-02T15:04")
	if w := post(url.Values{"action": {"snooze"}, "expected_revision": {"0"}, "snoozed_until": {until}, "status": {"snoozed"}}, true); w.Code != 303 || w.Header().Get("Location") != pageURL+"?status=snoozed" {
		t.Fatal("snooze", w.Code, w.Body.String(), w.Header())
	}
	if w := get("?status=snoozed"); w.Code != 200 || !strings.Contains(w.Body.String(), id) || !strings.Contains(w.Body.String(), "Clear snooze") {
		t.Fatal("snoozed inbox", w.Code, w.Body.String())
	}
	if w := get(""); w.Code != 200 || strings.Contains(w.Body.String(), id) {
		t.Fatal("awake filter", w.Code, w.Body.String())
	}
	if w := post(url.Values{"action": {"unsnooze"}, "expected_revision": {"0"}}, true); w.Code != 409 {
		t.Fatal("stale", w.Code, w.Body.String())
	}
	if w := post(url.Values{"action": {"unsnooze"}, "expected_revision": {"1"}}, true); w.Code != 303 {
		t.Fatal("unsnooze", w.Code, w.Body.String())
	}
	if w := post(url.Values{"action": {"acknowledge"}, "expected_revision": {"2"}, "status": {"all"}}, true); w.Code != 303 {
		t.Fatal("acknowledge", w.Code, w.Body.String())
	}
	if w := get(""); w.Code != 200 || strings.Contains(w.Body.String(), id) {
		t.Fatal("acknowledged still awake", w.Code, w.Body.String())
	}
	if w := get("?status=all"); w.Code != 200 || !strings.Contains(w.Body.String(), id) {
		t.Fatal("all responses", w.Code, w.Body.String())
	}
	triage, err := st.GetIssueApplicationTriage(t.Context(), app.ID, app.AccountID, app.AccountID, id)
	if err != nil || triage.AcknowledgedAt == nil || triage.Revision != 3 {
		t.Fatal(triage, err)
	}
	// The existing narrow response page remains navigable after the new inbox link.
	r := httptest.NewRequest("GET", "/dashboard/apps/"+app.Slug+"/issues/"+id+"/triage", nil)
	e.AddAdminSession(t, r)
	w := httptest.NewRecorder()
	(*e.H()).ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), pageURL) {
		t.Fatal("response navigation", w.Code, w.Body.String())
	}
}
