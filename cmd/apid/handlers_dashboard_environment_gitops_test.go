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
	if post.Code != http.StatusSeeOther {
		t.Fatalf("controls: %d %s", post.Code, post.Body.String())
	}
	worker := environmentgitops.Worker{Store: store, Backend: environmentgitops.IntentBackend{Store: store}, LeaseDuration: time.Minute, CheckInterval: time.Minute, RetryInterval: time.Second}
	if _, err := worker.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	get = dashboardGet(handler, target, cookie)
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), "converged") || !strings.Contains(get.Body.String(), sha) {
		t.Fatalf("history page: %d %s", get.Code, get.Body.String())
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
