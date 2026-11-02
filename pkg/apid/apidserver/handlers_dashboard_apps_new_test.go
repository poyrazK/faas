// handlers_dashboard_apps_new_test.go — Issue #961 / Mega-B PR-3.
//
// Pins the three renderAppNew states (Connect / Degraded / Form)
// and the dispatcher routing for /dashboard/apps/new. The wizard is
// a thin handler; the §11 story lives in sessionGithubLogin (covered
// by handlers_install_github_test.go) and bindAppToRepo (covered by
// the same file). These tests assert the dashboard renders the right
// copy + form fields, NOT the bind-time invariants.
package apidserver_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// appsNewFake extends bindPickerFake (handlers_install_github_test.go)
// with a programmable ListInstallableRepos that returns a fixed repo
// list — the wizard's "Form" state needs at least one repo row to
// assert against. The other methods come along for the ride because
// the interface requires them.
type appsNewFake struct {
	bindPickerFake
	repos   []apidserver.Repo
	listErr error
}

func (f *appsNewFake) ListInstallableRepos(_ context.Context, _ string, _ int64) ([]apidserver.Repo, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.repos, nil
}

// TestRenderAppNew_ConnectGitHubFirstWhenNoSessionLogin pins the
// first-state behavior: a freshly-signed-in customer (no
// env.GithubLogin yet) sees the Connect GitHub CTA only. The
// install/repo/template <select>s would 403 at submit time, so the
// wizard must not show them.
func TestRenderAppNew_ConnectGitHubFirstWhenNoSessionLogin(t *testing.T) {
	srv, cookie := apidserver.NewAuthedDashboardServer(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/apps/new", nil)
	r.AddCookie(cookie)
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Connect GitHub",
		"/dashboard/install/connect",
		"csrf_token",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
	for _, banned := range []string{
		`name="installation_id"`,
		`name="repo_full_name"`,
		`name="template_name"`,
	} {
		if strings.Contains(body, banned) {
			t.Errorf("body should not include %q (Connect-first CTA is the only affordance)\n--- body ---\n%s", banned, body)
		}
	}
}

// TestRenderAppNew_GitHubDegradedDegrades pins the GitHub-unreachable
// path. The stub githubd client always returns errGithubdNotReady;
// in renderAppNew that's a "GitHub is unreachable, retry" banner.
// The form section must NOT render in this state — there are no
// repos to pick from.
func TestRenderAppNew_GitHubDegradedDegrades(t *testing.T) {
	gh := &appsNewFake{repos: nil}
	gh.bindPickerFake = bindPickerFake{verified: true, accountLogin: "alice"}
	gh.listErr = (*apidserver.ErrGithubdNotReady)

	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "alice@example.com", "free")
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if err := store.UpsertGitHubInstall(t.Context(), state.GitHubInstall{
		AccountID: acct.ID, InstallationID: 42, AuditGithubLogin: "alice", SealedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed GitHub install: %v", err)
	}
	mgr, err := session.NewEphemeralManager(apidserver.SessionCookieLifetime)
	if err != nil {
		t.Fatalf("session manager: %v", err)
	}
	rawCookie, err := apidserver.MintDashboardSession(t.Context(), store, mgr, acct.ID)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	stamped := stampCookie(t, mgr, rawCookie, "alice")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := apidserver.NewServerWithDeps(store, log, "gregale.dev", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, gh, mgr, nil, 15*60_000_000_000, "").Handler()

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/apps/new", nil)
	r.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: stamped})
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "GitHub is unreachable") {
		t.Errorf("body missing the degraded banner\n--- body ---\n%s", body)
	}
	for _, banned := range []string{
		`name="installation_id"`,
		`name="repo_full_name"`,
		`name="template_name"`,
	} {
		if strings.Contains(body, banned) {
			t.Errorf("body should not include %q (degraded banner only)\n--- body ---\n%s", banned, body)
		}
	}
}

// TestRenderAppNew_PreFillsRepo pins the ?repo=<owner>/<name> deep
// link. The CLI's `gregale connect repo <owner>/<name>` opens this
// URL with the repo query param; the wizard must echo it so the
// form's <option selected> lands on the right row.
func TestRenderAppNew_PreFillsRepo(t *testing.T) {
	gh := &appsNewFake{repos: []apidserver.Repo{
		{FullName: "poyrazK/gregale", DefaultBranch: "main"},
		{FullName: "poyrazK/other", DefaultBranch: "main"},
	}}
	gh.bindPickerFake = bindPickerFake{verified: true, accountLogin: "alice"}

	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "alice@example.com", "free")
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if err := store.UpsertGitHubInstall(t.Context(), state.GitHubInstall{
		AccountID: acct.ID, InstallationID: 42, AuditGithubLogin: "alice", SealedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed GitHub install: %v", err)
	}
	mgr, err := session.NewEphemeralManager(apidserver.SessionCookieLifetime)
	if err != nil {
		t.Fatalf("session manager: %v", err)
	}
	rawCookie, err := apidserver.MintDashboardSession(t.Context(), store, mgr, acct.ID)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	stamped := stampCookie(t, mgr, rawCookie, "alice")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := apidserver.NewServerWithDeps(store, log, "gregale.dev", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, gh, mgr, nil, 15*60_000_000_000, "").Handler()

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/apps/new?repo=poyrazK/gregale", nil)
	r.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: stamped})
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// <h1>{{.Title}}</h1> renders "New app"; the wizard's
	// distinctive copy is "Choose GitHub installation" inside
	// the form section. Both must be present so a regression in
	// the dispatch routing (a future refactor that drops the
	// literal-equal case) fails this test.
	if !strings.Contains(body, "New app") {
		t.Errorf("body missing wizard heading\n--- body ---\n%s", body)
	}
	if !strings.Contains(body, "Choose GitHub installation") {
		t.Errorf("body missing form section heading — did the Form path fall through to the Connect CTA?\n--- body ---\n%s", body)
	}
	// Both repos must be in the dropdown; the prefilled repo must
	// carry the `selected` attribute. The Go html/template emits
	// `selected` with no leading space when the {{if eq}} condition
	// is true (no other attributes intervene), so the test matches
	// the bare attribute rather than `value="..." selected`.
	if !strings.Contains(body, "poyrazK/gregale") {
		t.Errorf("body missing the prefilled repo\n--- body ---\n%s", body)
	}
	if !strings.Contains(body, "selected>poyrazK/gregale") {
		t.Errorf("body missing `selected` on prefilled repo\n--- body ---\n%s", body)
	}
}

// TestRenderAppNew_DispatchRouteIsMounted pins that the
// /dashboard/apps/new route actually reaches renderAppNew. The
// dashboard dispatcher has many cases; a regression that drops the
// literal-equal branch (favoring the prefix-match) would send
// /apps/new through renderAppDetail and 404 on the missing slug.
func TestRenderAppNew_DispatchRouteIsMounted(t *testing.T) {
	srv, cookie := apidserver.NewAuthedDashboardServer(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/apps/new?repo=poyrazK/gregale", nil)
	r.AddCookie(cookie)
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// <h1>{{.Title}}</h1> renders "New app"; the Connect-first CTA
	// is the wizard's signature copy in the no-github-login state.
	// Either of these appearing proves the wizard rendered (vs the
	// AppDetail page rendering a different shape).
	if !strings.Contains(body, "New app") {
		t.Errorf("body missing wizard heading — the /apps/new route is hitting the wrong handler\n--- body ---\n%s", body)
	}
	if !strings.Contains(body, "Connect GitHub") {
		t.Errorf("body missing the Connect-first CTA — the dispatcher sent /apps/new elsewhere\n--- body ---\n%s", body)
	}
}

func newGitHubWizardPostServer(t *testing.T, gh apidserver.GithubdClient) (http.Handler, *state.MemStore, *session.Manager, state.Account, string) {
	return newGitHubWizardPostServerFor(t, gh, true)
}

func newGitHubWizardPostServerFor(t *testing.T, gh apidserver.GithubdClient, verified bool) (http.Handler, *state.MemStore, *session.Manager, state.Account, string) {
	return newGitHubWizardPostServerAs(t, gh, verified, "alice")
}

// newGitHubWizardPostServerAs seeds the account's connection to installation
// 42. An empty githubLogin is a Google or password session.
func newGitHubWizardPostServerAs(t *testing.T, gh apidserver.GithubdClient, verified bool, githubLogin string) (http.Handler, *state.MemStore, *session.Manager, state.Account, string) {
	t.Helper()
	store := state.NewMemStore()
	res, err := store.CreateAccountWithPersonalOrg(t.Context(), state.CreateAccountWithPersonalOrgParams{
		Email: "alice@example.com", Plan: "free", RequireEmailVerification: !verified,
	})
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	acct := res.Account
	mgr, err := session.NewEphemeralManager(apidserver.SessionCookieLifetime)
	if err != nil {
		t.Fatalf("session manager: %v", err)
	}
	rawCookie, err := apidserver.MintDashboardSession(t.Context(), store, mgr, acct.ID)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	if err := store.UpsertGitHubInstall(t.Context(), state.GitHubInstall{
		AccountID: acct.ID, InstallationID: 42, AuditGithubLogin: "alice", SealedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed GitHub install: %v", err)
	}
	cookie := rawCookie
	if githubLogin != "" {
		cookie = stampCookie(t, mgr, rawCookie, githubLogin)
	}
	return apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, gh, mgr, nil, 15*60_000_000_000, "").Handler(), store, mgr, acct, cookie
}

func postGitHubWizard(t *testing.T, h http.Handler, mgr *session.Manager, acct state.Account, sessionValue string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	csrf, err := middleware.IssueForAuthenticatedNamed(mgr, apidserver.GithubWizardCreateAction, acct.ID, apidserver.GithubWizardCreateCSRFCookie)
	if err != nil {
		t.Fatalf("issue wizard csrf: %v", err)
	}
	form.Set("csrf_token", csrf)
	req := httptest.NewRequest(http.MethodPost, "/dashboard/apps/new", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: sessionValue})
	req.AddCookie(&http.Cookie{Name: apidserver.GithubWizardCreateCSRFCookie, Value: csrf})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// A Google or password session has no GitHub login after Connect GitHub
// (ADR-1345); the wizard used to answer 403 github_login_required and loop
// on "Connect GitHub" (ADR-20261101160300).
func TestCreateAppFromGitHubWizard_PasswordSessionCreatesAndBinds(t *testing.T) {
	gh := &appsNewFake{repos: []apidserver.Repo{{FullName: "octocat/hello", DefaultBranch: "trunk"}}}
	gh.bindPickerFake = bindPickerFake{verified: true, accountLogin: "alice", bindReturn: "bind-created"}
	h, _, mgr, acct, sessionValue := newGitHubWizardPostServerAs(t, gh, true, "")

	rec := postGitHubWizard(t, h, mgr, acct, sessionValue, url.Values{
		"installation_id": {"42"}, "repo_full_name": {"octocat/hello"}, "slug": {"password-app"},
	})
	if got := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || got != "/dashboard/apps/password-app?github=connected" {
		t.Fatalf("code = %d Location = %q, want the connected app\nbody = %s", rec.Code, got, rec.Body.String())
	}
	if gh.bindCalls != 1 || gh.gotBranch != "trunk" {
		t.Fatalf("BindAppRepo calls = %d branch = %q, want one bind of the repository's default branch", gh.bindCalls, gh.gotBranch)
	}
}

// The app page's "Bind a repository" opens the wizard for an existing app.
func TestCreateAppFromGitHubWizard_BindsExistingApp(t *testing.T) {
	gh := &appsNewFake{repos: []apidserver.Repo{{FullName: "octocat/hello", DefaultBranch: "main"}}}
	gh.bindPickerFake = bindPickerFake{verified: true, accountLogin: "alice", bindReturn: "bind-existing"}
	h, store, mgr, acct, sessionValue := newGitHubWizardPostServerAs(t, gh, true, "")
	existing, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "existing-app", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	apps, _ := store.ListApps(t.Context(), acct.ID)

	rec := postGitHubWizard(t, h, mgr, acct, sessionValue, url.Values{
		"installation_id": {"42"}, "repo_full_name": {"octocat/hello"}, "existing_app": {"existing-app"},
	})
	if got := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || got != "/dashboard/apps/existing-app?github=connected" {
		t.Fatalf("code = %d Location = %q\nbody = %s", rec.Code, got, rec.Body.String())
	}
	if gh.bindCalls != 1 || gh.gotAppID != existing.ID {
		t.Fatalf("BindAppRepo calls = %d app = %q, want the existing app %q", gh.bindCalls, gh.gotAppID, existing.ID)
	}
	if after, _ := store.ListApps(t.Context(), acct.ID); len(after) != len(apps) {
		t.Fatalf("apps = %d, want %d: binding an existing app creates none", len(after), len(apps))
	}
}

// The installation must be one the account connected; knowing its ID is
// not enough.
func TestCreateAppFromGitHubWizard_RejectsUnconnectedInstallation(t *testing.T) {
	gh := &appsNewFake{repos: []apidserver.Repo{{FullName: "octocat/hello", DefaultBranch: "main"}}}
	gh.bindPickerFake = bindPickerFake{verified: true, accountLogin: "alice"}
	h, store, mgr, acct, sessionValue := newGitHubWizardPostServerAs(t, gh, true, "")

	rec := postGitHubWizard(t, h, mgr, acct, sessionValue, url.Values{
		"installation_id": {"99"}, "repo_full_name": {"octocat/hello"}, "slug": {"foreign-app"},
	})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "not+connected") {
		t.Fatalf("code = %d Location = %q, want the wizard with an error", rec.Code, rec.Header().Get("Location"))
	}
	if _, err := store.AppBySlug(t.Context(), "foreign-app"); err == nil || gh.bindCalls != 0 || gh.gotInstallID != 0 {
		t.Fatalf("app created or GitHub contacted for an installation the account did not connect")
	}
}

// With a connection, a password session sees the installation and repository
// pickers, not the Connect GitHub button.
func TestRenderAppNew_PasswordSessionWithConnectionShowsPicker(t *testing.T) {
	gh := &appsNewFake{repos: []apidserver.Repo{{FullName: "octocat/hello", DefaultBranch: "main"}}}
	gh.bindPickerFake = bindPickerFake{verified: true, accountLogin: "alice"}
	h, _, _, _, sessionValue := newGitHubWizardPostServerAs(t, gh, true, "")
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/apps/new?app=existing-app", nil)
	r.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: sessionValue})
	h.ServeHTTP(rec, r)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `name="repo_full_name"`) || strings.Contains(body, `action="/dashboard/install/connect"`) {
		t.Fatalf("code = %d, want the repository picker\n--- body ---\n%s", rec.Code, body)
	}
	if !strings.Contains(body, `name="existing_app" value="existing-app"`) {
		t.Fatalf("body does not bind the existing app\n--- body ---\n%s", body)
	}
}

func TestCreateAppFromGitHubWizard_CreatesAndBinds(t *testing.T) {
	gh := &appsNewFake{repos: []apidserver.Repo{{FullName: "octocat/hello", DefaultBranch: "main"}}}
	gh.bindPickerFake = bindPickerFake{verified: true, accountLogin: "alice", defaultBranch: "main", bindReturn: "bind-created"}
	h, store, mgr, acct, sessionValue := newGitHubWizardPostServer(t, gh)

	csrf, err := middleware.IssueForAuthenticatedNamed(mgr, apidserver.GithubWizardCreateAction, acct.ID, apidserver.GithubWizardCreateCSRFCookie)
	if err != nil {
		t.Fatalf("issue wizard csrf: %v", err)
	}
	form := url.Values{
		"csrf_token":        {csrf},
		"installation_id":   {"42"},
		"repo_full_name":    {"octocat/hello"},
		"production_branch": {"main"},
		"slug":              {"created-app"},
	}
	req := httptest.NewRequest(http.MethodPost, "/dashboard/apps/new", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: sessionValue})
	req.AddCookie(&http.Cookie{Name: apidserver.GithubWizardCreateCSRFCookie, Value: csrf})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303\nbody = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/dashboard/apps/created-app?github=connected" {
		t.Fatalf("Location = %q, want connected app detail", got)
	}
	if _, err := store.AppBySlug(t.Context(), "created-app"); err != nil {
		t.Fatalf("created app lookup: %v", err)
	}
	if gh.bindCalls != 1 {
		t.Fatalf("BindAppRepo calls = %d, want 1", gh.bindCalls)
	}
	if gh.gotInstallID != 42 || gh.gotExpectedLogin != "" {
		t.Fatalf("VerifyInstallation = (%d, %q), want (42, empty expected login)", gh.gotInstallID, gh.gotExpectedLogin)
	}
}

func TestCreateAppFromGitHubWizard_RejectsMissingCSRF(t *testing.T) {
	gh := &appsNewFake{repos: []apidserver.Repo{{FullName: "octocat/hello", DefaultBranch: "main"}}}
	gh.bindPickerFake = bindPickerFake{verified: true, accountLogin: "alice"}
	h, store, _, _, sessionValue := newGitHubWizardPostServer(t, gh)
	form := url.Values{
		"installation_id":   {"42"},
		"repo_full_name":    {"octocat/hello"},
		"production_branch": {"main"},
		"slug":              {"created-app"},
	}
	req := httptest.NewRequest(http.MethodPost, "/dashboard/apps/new", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: sessionValue})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303\nbody = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Location"), "form+expired") {
		t.Fatalf("Location = %q, want expired-form error", rec.Header().Get("Location"))
	}
	if _, err := store.AppBySlug(t.Context(), "created-app"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("created app lookup error = %v, want not found", err)
	}
	if gh.bindCalls != 0 {
		t.Fatalf("BindAppRepo calls = %d, want 0", gh.bindCalls)
	}
}

// TestCreateAppFromGitHubWizard_RequiresVerifiedEmail — POST /v1/apps
// requires a verified email; the dashboard wizard created the app (and
// bound the repo) without one.
func TestCreateAppFromGitHubWizard_RequiresVerifiedEmail(t *testing.T) {
	gh := &appsNewFake{repos: []apidserver.Repo{{FullName: "octocat/hello", DefaultBranch: "main"}}}
	gh.bindPickerFake = bindPickerFake{verified: true, accountLogin: "alice", defaultBranch: "main", bindReturn: "bind-created"}
	h, store, mgr, acct, sessionValue := newGitHubWizardPostServerFor(t, gh, false)

	csrf, err := middleware.IssueForAuthenticatedNamed(mgr, apidserver.GithubWizardCreateAction, acct.ID, apidserver.GithubWizardCreateCSRFCookie)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"csrf_token": {csrf}, "installation_id": {"42"}, "repo_full_name": {"octocat/hello"},
		"production_branch": {"main"}, "slug": {"unverified-app"},
	}
	req := httptest.NewRequest(http.MethodPost, "/dashboard/apps/new", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: sessionValue})
	req.AddCookie(&http.Cookie{Name: apidserver.GithubWizardCreateCSRFCookie, Value: csrf})
	h.ServeHTTP(httptest.NewRecorder(), req)
	if _, err := store.AppBySlug(t.Context(), "unverified-app"); err == nil {
		t.Fatal("the wizard created an app for an unverified account")
	}
	if gh.bindCalls != 0 {
		t.Fatalf("BindAppRepo calls = %d, want 0", gh.bindCalls)
	}
}
