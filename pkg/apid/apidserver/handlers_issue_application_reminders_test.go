//go:build !no_pg

package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-1493: owner-only opt-in settings use existing browser CSRF and revision checks.
func TestNativeApplicationReminderDashboardPostgres(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "native-reminder-settings")
	pageURL := "/dashboard/apps/" + app.Slug + "/issues/tracker-notifications"
	request := httptest.NewRequest("GET", pageURL, nil)
	e.AddAdminSession(t, request)
	page := httptest.NewRecorder()
	(*e.H()).ServeHTTP(page, request)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Native issue reminders") {
		t.Fatal(page.Code, page.Body.String())
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
	post := func(rev, interval, limit string, valid bool) *httptest.ResponseRecorder {
		form := url.Values{"settings_kind": {"application_reminders"}, "expected_revision": {rev}, "interval_minutes": {interval}, "repeat_limit": {limit}, "enabled": {"1"}}
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
	if w := post("0", "5", "2", false); w.Code != 400 {
		t.Fatal("CSRF", w.Code)
	}
	if w := post("0", "5", "2", true); w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "#application-reminder-settings") {
		t.Fatal("save", w.Code, w.Body.String())
	}
	if w := post("0", "5", "2", true); w.Code != 409 {
		t.Fatal("stale", w.Code, w.Body.String())
	}
	for _, entry := range []struct{ interval, limit string }{{"invalid", "2"}, {"4", "2"}, {"1441", "2"}, {"5", "4"}} {
		if w := post("1", entry.interval, entry.limit, true); w.Code != 400 {
			t.Fatal("bounds", w.Code, w.Body.String())
		}
	}
	st := state.NewPgStore(*e.Pool())
	policy, err := st.GetIssueApplicationReminderPolicy(t.Context(), app.ID, app.AccountID)
	if err != nil || !policy.Enabled || policy.IntervalMinutes != 5 || policy.RepeatLimit != 2 || policy.Revision != 1 {
		t.Fatal(policy, err)
	}
	if w := post("1", "5", "2", true); w.Code != 303 {
		t.Fatal("no-op", w.Code, w.Body.String())
	}
	same, err := st.GetIssueApplicationReminderPolicy(t.Context(), app.ID, app.AccountID)
	if err != nil || same.Revision != 1 {
		t.Fatal("no-op revision", same, err)
	}
}
