//go:build !no_pg

package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestIssueNotificationSettingsAllowCompanyAdmin(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	store := *e.Store()
	creator, err := store.CreateAccount(t.Context(), "issue-company-creator@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(t.Context(), state.Org{Slug: "issue-settings-company", Name: "Issue Settings", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.AddOrgMember(t.Context(), org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err = store.AddOrgMember(t.Context(), org.ID, (*e.Acct()).ID, state.OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "issue-company-app"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/dashboard/apps/" + app.Slug + "/issues/tracker-notifications"
	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		e.AddAdminSession(t, req)
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, req)
		return rec
	}
	page := get()
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Notification settings") {
		t.Fatalf("admin page: %d %s", page.Code, page.Body.String())
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
	form := url.Values{"csrf_token": {csrf.Value}, "expected_revision": {"0"}, "failure_threshold": {"3"}, "enabled": {"1"}}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrf)
	e.AddAdminSession(t, req)
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("admin save: %d %s", rec.Code, rec.Body.String())
	}
	st := state.NewPgStore(*e.Pool())
	policy, err := st.GetIssueTrackerNotificationPolicy(t.Context(), app.ID, creator.ID)
	if err != nil || !policy.Enabled || policy.FailureThreshold != 3 {
		t.Fatalf("saved policy: %+v %v", policy, err)
	}
	// A demotion after authorization but before the write must also fence the
	// pending transaction; checking only the role at page load is not enough.
	form.Set("expected_revision", "1")
	form.Set("failure_threshold", "4")
	body := &offboardingPausedBody{reader: strings.NewReader(form.Encode()), started: make(chan struct{}), resume: make(chan struct{})}
	paused := httptest.NewRequest(http.MethodPost, path, body)
	paused.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	paused.AddCookie(csrf)
	e.AddAdminSession(t, paused)
	pausedResult := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { (*e.H()).ServeHTTP(pausedResult, paused); close(done) }()
	defer body.release()
	select {
	case <-body.started:
	case <-done:
		t.Fatalf("request ended before revocation: %d %s", pausedResult.Code, pausedResult.Body.String())
	case <-time.After(5 * time.Second):
		t.Fatal("request did not reach settings body")
	}
	if err = store.UpdateOrgMemberRole(t.Context(), org.ID, (*e.Acct()).ID, state.OrgRoleViewer); err != nil {
		t.Fatal(err)
	}
	body.release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("revoked request did not finish")
	}
	if pausedResult.Code == http.StatusSeeOther {
		t.Fatal("demoted admin saved notification settings")
	}
	policy, err = st.GetIssueTrackerNotificationPolicy(t.Context(), app.ID, creator.ID)
	if err != nil || policy.FailureThreshold != 3 {
		t.Fatalf("revoked request changed policy: %+v %v", policy, err)
	}
	if page = get(); page.Code != http.StatusNotFound {
		t.Fatalf("viewer without notifications: %d %s", page.Code, page.Body.String())
	}
}
