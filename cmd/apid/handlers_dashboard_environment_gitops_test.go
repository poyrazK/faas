package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
)

func gitOpsDashboardPost(t *testing.T, handler http.Handler, session *http.Cookie, get *httptest.ResponseRecorder, action string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	values.Set("csrf_token", dashboardInputToken(get.Body.String()))
	request := httptest.NewRequest(http.MethodPost, "/dashboard/projects/shop/environments/production/gitops/"+action, strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(session)
	if csrf := findDashboardCookie(get.Result().Cookies(), dashboardEnvironmentGitOpsCookie); csrf != nil {
		request.AddCookie(csrf)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request)
	return rec
}

func TestEnvironmentGitOpsDashboardShowsStaleSourceEvidence(t *testing.T) {
	srv, _, _, _, _ := newProjectLifecycleFixture(t)
	now := time.Now().UTC()
	recent, old := now.Add(-time.Minute), now.Add(-api.EnvironmentGitSourceStaleAfter)
	for _, tc := range []struct {
		name              string
		checked, verified *time.Time
		created           time.Time
		suspended         bool
		errorCode         string
		want              string
	}{
		{name: "first check", created: recent, want: "Waiting for the first successful Git check."},
		{name: "first check overdue", created: old, want: "Git checks are stale."},
		{name: "fresh", created: old, checked: &recent, verified: &recent, want: "Git definition verified."},
		{name: "stale checks", created: old, checked: &old, verified: &old, want: "Git checks are stale."},
		{name: "fresh failed check", created: old, checked: &recent, verified: &old, errorCode: "environment_git_source_unavailable", want: "The Git definition could not be verified."},
		{name: "missing current verification", created: old, checked: &recent, want: "Git verification is stale."},
		{name: "suspended", created: old, checked: &old, verified: &old, suspended: true, want: "Git checks are suspended."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := dashboard.EnvironmentGitOpsData{Project: "shop", Environment: "production", Status: &api.EnvironmentGitOpsStatusResponse{
				Source: api.EnvironmentGitSource{CreatedAt: tc.created, SourceCheckedAt: tc.checked, SourceVerifiedAt: tc.verified,
					SourceErrorCode: tc.errorCode, Suspended: tc.suspended},
			}}
			setEnvironmentGitOpsDashboardFreshness(&data, now)
			rec := httptest.NewRecorder()
			if err := dashboard.Render(rec, srv.log, "", dashboard.Page{Title: "GitOps", Body: "environment_gitops", Data: data}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("source evidence missing %q: %s", tc.want, rec.Body.String())
			}
			if tc.name != "fresh" && strings.Contains(rec.Body.String(), "Git definition verified.") {
				t.Fatal("unavailable or old source was shown as freshly verified")
			}
		})
	}
}

func gitOpsDashboardHidden(t *testing.T, page string, name string) string {
	t.Helper()
	match := regexp.MustCompile(`name="` + regexp.QuoteMeta(name) + `" value="([^"]*)"`).FindStringSubmatch(page)
	if len(match) != 2 {
		t.Fatalf("review page missing %s", name)
	}
	return match[1]
}

func TestEnvironmentGitOpsDashboardReviewAdoptionAndHistory(t *testing.T) {
	srv, store, account, project, app := newProjectLifecycleFixture(t)
	if _, err := store.UpdateProjectBinding(t.Context(), account.ID, project.ID, "example/shop", "main", 42); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "production", "MODE", "console"); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	srv.githubd = &environmentGitOpsClient{repositories: []Repo{{ID: 123, FullName: "example/shop"}}, archive: environmentGitOpsArchive(t), resolvedSHA: sha}
	handler := srv.handler()
	cookie := &http.Cookie{Name: sessionCookie, Value: issueDashboardTestCookie(t, store, srv.sessions, account.ID)}
	target := "/dashboard/projects/shop/environments/production/gitops"
	get := dashboardGet(handler, target, cookie)
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), "Connect in report mode") {
		t.Fatalf("unbound page: %d %s", get.Code, get.Body.String())
	}
	post := gitOpsDashboardPost(t, handler, cookie, get, "bind", url.Values{"manifest_path": {"environments/production.yaml"}})
	if post.Code != http.StatusSeeOther {
		t.Fatalf("bind: %d %s", post.Code, post.Body.String())
	}
	get = dashboardGet(handler, target, cookie)
	post = gitOpsDashboardPost(t, handler, cookie, get, "review", url.Values{"commit_sha": {sha}})
	if post.Code != http.StatusOK || !strings.Contains(post.Body.String(), "Approve this definition") || !strings.Contains(post.Body.String(), "MODE") {
		t.Fatalf("review: %d %s", post.Code, post.Body.String())
	}
	review := post
	post = gitOpsDashboardPost(t, handler, cookie, review, "approve", url.Values{
		"commit_sha": {sha}, "definition_digest": {gitOpsDashboardHidden(t, review.Body.String(), "definition_digest")},
		"expected_generation": {gitOpsDashboardHidden(t, review.Body.String(), "expected_generation")},
	})
	if post.Code != http.StatusSeeOther {
		t.Fatalf("approve: %d %s", post.Code, post.Body.String())
	}
	get = dashboardGet(handler, target, cookie)
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), "Adopt the reviewed fields") {
		t.Fatalf("adoption page: %d %s", get.Code, get.Body.String())
	}
	post = gitOpsDashboardPost(t, handler, cookie, get, "adopt", url.Values{"plan_hash": {gitOpsDashboardHidden(t, get.Body.String(), "plan_hash")}})
	if post.Code != http.StatusSeeOther {
		t.Fatalf("adopt: %d %s", post.Code, post.Body.String())
	}
	source, err := store.EnvironmentGitSource(t.Context(), account.ID, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	get = dashboardGet(handler, target, cookie)
	post = gitOpsDashboardPost(t, handler, cookie, get, "controls", url.Values{"expected_generation": {strconv.FormatInt(source.Generation, 10)}, "mode": {"enforce"}})
	if post.Code != http.StatusConflict || !strings.Contains(post.Body.String(), "environment_git_enforcement_unavailable") {
		t.Fatalf("controls: %d %s", post.Code, post.Body.String())
	}
	enableEnvironmentGitOpsTestExecutor(t, store, account.ID, project.ID)
	worker := environmentgitops.Worker{Store: store, Backend: environmentgitops.IntentBackend{Store: store}, LeaseDuration: time.Minute, CheckInterval: time.Minute, RetryInterval: time.Second}
	if _, err := worker.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	get = dashboardGet(handler, target, cookie)
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), "converged") || !strings.Contains(get.Body.String(), sha) {
		t.Fatalf("history page: %d %s", get.Code, get.Body.String())
	}
	for _, suspended := range []bool{true, false} {
		source, err = store.EnvironmentGitSource(t.Context(), account.ID, project.ID, "production")
		if err != nil {
			t.Fatal(err)
		}
		values := url.Values{"expected_generation": {strconv.FormatInt(source.Generation, 10)}}
		if suspended {
			values.Set("suspended", "true")
		}
		post = gitOpsDashboardPost(t, handler, cookie, get, "controls", values)
		if post.Code != http.StatusSeeOther {
			t.Fatalf("suspended=%v controls: %d %s", suspended, post.Code, post.Body.String())
		}
		get = dashboardGet(handler, target, cookie)
		if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), "converged") || !strings.Contains(get.Body.String(), `name="suspended"`) {
			t.Fatalf("suspended=%v status and controls unavailable: %d %s", suspended, get.Code, get.Body.String())
		}
		if strings.Contains(get.Body.String(), "Git checks are suspended.") != suspended {
			t.Fatalf("suspended=%v status does not match source", suspended)
		}
	}
}

func TestEnvironmentGitOpsDashboardRejectsMissingCSRFAndAnonymousAccess(t *testing.T) {
	srv, store, account, _, _ := newProjectLifecycleFixture(t)
	handler := srv.handler()
	target := "/dashboard/projects/shop/environments/production/gitops"
	if rec := dashboardGet(handler, target); rec.Code != http.StatusFound {
		t.Fatalf("anonymous page: %d", rec.Code)
	}
	request := httptest.NewRequest(http.MethodPost, target+"/bind", strings.NewReader("manifest_path=environments/production.yaml"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: issueDashboardTestCookie(t, store, srv.sessions, account.ID)})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "CSRF") {
		t.Fatalf("missing CSRF: %d %s", rec.Code, rec.Body.String())
	}
}
