//go:build !no_pg

package apidserver_test

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// ADR-1494: native escalation is an owner-only, revision-checked browser setting.
func TestNativeApplicationEscalationDashboardPostgres(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "native-escalation-settings")
	st := state.NewPgStore(*e.Pool())
	team, err := st.PutIssueTeam(t.Context(), app.ID, app.AccountID, app.AccountID, "backup", api.PutIssueTeamRequest{Name: "Backup", MemberAccountIDs: []string{app.AccountID}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	pageURL := "/dashboard/apps/" + app.Slug + "/issues/tracker-notifications"
	r := httptest.NewRequest("GET", pageURL, nil)
	e.AddAdminSession(t, r)
	page := httptest.NewRecorder()
	(*e.H()).ServeHTTP(page, r)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Native issue backup-team escalation") {
		t.Fatal(page.Code, page.Body.String())
	}
	var csrf *http.Cookie
	for _, c := range page.Result().Cookies() {
		if c.Name == apidserver.DashboardIssueCookie {
			csrf = c
		}
	}
	if csrf == nil {
		t.Fatal("missing CSRF cookie")
	}
	post := func(revision, delay, backup string, valid bool) *httptest.ResponseRecorder {
		form := url.Values{"settings_kind": {"application_escalation"}, "expected_revision": {revision}, "delay_minutes": {delay}, "team_id": {backup}, "enabled": {"1"}}
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
	if w := post("0", "15", team.ID, false); w.Code != 400 {
		t.Fatal("CSRF", w.Code)
	}
	if w := post("0", "15", team.ID, true); w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "#application-escalation-settings") {
		t.Fatal("save", w.Code, w.Body.String())
	}
	if w := post("0", "15", team.ID, true); w.Code != 409 {
		t.Fatal("stale", w.Code, w.Body.String())
	}
	for _, bad := range []struct{ delay, team string }{{"invalid", team.ID}, {"14", team.ID}, {"10081", team.ID}, {"15", ""}, {"15", "invalid"}} {
		if w := post("1", bad.delay, bad.team, true); w.Code != 400 {
			t.Fatal("invalid settings", w.Code, w.Body.String())
		}
	}
	if w := post("1", "15", team.ID, true); w.Code != 303 {
		t.Fatal("no-op", w.Code, w.Body.String())
	}
	policy, err := st.GetIssueApplicationEscalationPolicy(t.Context(), app.ID, app.AccountID)
	if err != nil || policy.Revision != 1 || !policy.Enabled || policy.TeamID != team.ID || policy.DelayMinutes != 15 {
		t.Fatal(policy, err)
	}
}
