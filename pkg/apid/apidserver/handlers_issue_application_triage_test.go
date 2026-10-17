//go:build !no_pg

package apidserver_test

import (
	"fmt"
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

// ADR-1492: native issue response lifecycle, authorization and delivery fencing.
func TestApplicationIssueResponseDashboardPostgres(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "native-response")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	body := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "ResponseError", Message: "private-title-marker", SourceKind: "exception"}
	response := e.Do(t, "POST", "/v1/apps/"+app.Slug+"/issue-events", body, map[string]string{"Authorization": "Bearer " + token.Token})
	if response.Code != 202 {
		t.Fatalf("ingest %d %s", response.Code, response.Body.String())
	}
	st := state.NewPgStore(*e.Pool())
	items, err := st.ListIssues(t.Context(), app.ID, state.IssueListFilter{}, state.IssueCursor{})
	if err != nil || len(items.Items) != 1 {
		t.Fatal(items, err)
	}
	issue := items.Items[0].ID
	pageURL := "/dashboard/apps/" + app.Slug + "/issues/" + issue + "/triage"
	get := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", pageURL, nil)
		e.AddAdminSession(t, r)
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		return w
	}
	page := get()
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Acknowledge") || strings.Contains(page.Body.String(), "private-title-marker") {
		t.Fatalf("GET %d %s", page.Code, page.Body.String())
	}
	var csrf *http.Cookie
	for _, c := range page.Result().Cookies() {
		if c.Name == apidserver.DashboardIssueCookie {
			csrf = c
		}
	}
	if csrf == nil {
		t.Fatal("no CSRF cookie")
	}
	post := func(form url.Values, valid bool) *httptest.ResponseRecorder {
		if valid {
			form.Set("csrf_token", csrf.Value)
		}
		r := httptest.NewRequest("POST", pageURL, strings.NewReader(form.Encode()))
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
	if w := post(url.Values{"action": {"acknowledge"}, "expected_revision": {"0"}}, true); w.Code != 303 || w.Header().Get("Location") != pageURL {
		t.Fatalf("ack %d %s", w.Code, w.Body.String())
	}
	if w := post(url.Values{"action": {"snooze"}, "expected_revision": {"0"}, "snoozed_until": {time.Now().UTC().Add(time.Hour).Format(time.RFC3339)}}, true); w.Code != 409 {
		t.Fatal("stale form", w.Code, w.Body.String())
	}
	v, err := st.GetIssueApplicationTriage(t.Context(), app.ID, app.AccountID, app.AccountID, issue)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"claim", "bad-action"} {
		if w := post(url.Values{"action": {action}, "expected_revision": {fmt.Sprint(v.Revision)}}, true); w.Code != 400 {
			t.Fatal("invalid", w.Code)
		}
	}
	form := url.Values{"action": {"snooze"}, "expected_revision": {fmt.Sprint(v.Revision)}, "snoozed_until": {time.Now().UTC().Add(time.Hour).Format("2006-01-02T15:04")}, "reason": {"<script>investigating</script>"}}
	if w := post(form, true); w.Code != 303 {
		t.Fatal("snooze", w.Code, w.Body.String())
	}
	if p := get(); p.Code != 200 || strings.Contains(p.Body.String(), "<script>investigating</script>") || !strings.Contains(p.Body.String(), "&lt;script&gt;investigating") {
		t.Fatal("unsafe history", p.Code, p.Body.String())
	}
	v, err = st.GetIssueApplicationTriage(t.Context(), app.ID, app.AccountID, app.AccountID, issue)
	if err != nil || !v.Snoozed || len(v.History) != 2 {
		t.Fatal(v, err)
	}
	if w := post(url.Values{"action": {"unsnooze"}, "expected_revision": {fmt.Sprint(v.Revision)}}, true); w.Code != 303 {
		t.Fatal("clear", w.Code, w.Body.String())
	}
}
