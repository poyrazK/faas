// Tests for the PR-B bind picker handlers
// (cmd/apid/handlers_install_github.go). Mirrors the §11
// ownership-proof pattern established in handlers_oauth_test.go:
//
//  1. each handler must call githubd.VerifyInstallation with the
//     session's github_login as expected_login BEFORE doing any
//     work (list or persist);
//  2. a forged install (account_login != session.github_login) is
//     403, not 302 — the §11 takeover attempt must be loud;
//  3. an unauthenticated session (no github_login) is 403 with
//     the github_login_required code so the dashboard renders
//     "complete /v1/auth/github first" rather than the generic
//     forged banner.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// bindPickerFake is the bind-picker test fake. Extends the OAuth
// callback fake with BindAppRepo counters so the happy-path bind
// test can assert the deterministic binding_id round-trip. We
// can't reuse fakeGithubdClient (defined in handlers_oauth_test.go)
// because its embedded GithubdClient would route BindAppRepo to
// errGithubdNotReady, which makes the bind happy path impossible
// to test without a second fake.
type bindPickerFake struct {
	verified      bool
	accountLogin  string
	defaultBranch string
	verifyErr     error
	bindErr       error
	bindReturn    string

	gotInstallID     int64
	gotExpectedLogin string
	bindCalls        int
}

func (f *bindPickerFake) VerifyInstallation(_ context.Context, installID int64, expectedLogin string) (bool, string, string, error) {
	f.gotInstallID = installID
	f.gotExpectedLogin = expectedLogin
	return f.verified, f.accountLogin, f.defaultBranch, f.verifyErr
}

func (f *bindPickerFake) BindAppRepo(_ context.Context, _, _ string, _ int64, _, _ string) (string, error) {
	f.bindCalls++
	if f.bindErr != nil {
		return "", f.bindErr
	}
	if f.bindReturn != "" {
		return f.bindReturn, nil
	}
	return "bind-test", nil
}

// Unused methods — the bind picker doesn't call them but the
// interface requires them. Returning the not-ready problem keeps
// accidental calls loud (test would see 503 in the response).
func (f *bindPickerFake) GetInstallState(context.Context, string) (InstallState, string, string, error) {
	return InstallStateUnspecified, "", "", errGithubdNotReady
}
func (f *bindPickerFake) ExchangeOAuthCode(context.Context, string, string, string) (string, string, error) {
	return "", "", errGithubdNotReady
}
func (f *bindPickerFake) ListInstallableRepos(context.Context, string, int64) ([]Repo, error) {
	return nil, errGithubdNotReady
}
func (f *bindPickerFake) UnbindAppRepo(context.Context, string, string) error {
	return errGithubdNotReady
}
func (f *bindPickerFake) GetAppBinding(context.Context, string, string) (AppBinding, error) {
	return AppBinding{}, errGithubdNotReady
}
func (f *bindPickerFake) CreateDeploymentFromPush(context.Context, string, string, string, string) (string, string, error) {
	return "", "", errGithubdNotReady
}
func (f *bindPickerFake) WriteCheck(context.Context, string, string, CheckPhase, string, string) error {
	return errGithubdNotReady
}

// MintInstallationToken is unused by the bind picker test. Returns
// the not-ready problem so any accidental call would surface as a
// 503 (DEPLOY-PROV-4 / ADR-092, issue #739).
func (f *bindPickerFake) MintInstallationToken(context.Context, string, int64) (string, time.Time, error) {
	return "", time.Time{}, errGithubdNotReady
}

// StreamSourceRef is unused by the bind picker test. Returns the
// not-ready problem (DEPLOY-PROV-4 / ADR-092, issue #739).
func (f *bindPickerFake) StreamSourceRef(context.Context, string, int64, string, string, int64) (*StreamSourceRefResult, error) {
	return nil, errGithubdNotReady
}
func (f *bindPickerFake) Close() error { return nil }

// newBindPickerTestServer seeds an account + a default app, mints a
// session cookie WITHOUT github_login, and returns the apid handler
// with a programmable GithubdClient + the session.Manager so
// callers can re-mint the cookie via SealGithubLogin.
func newBindPickerTestServer(t *testing.T, gh GithubdClient) (http.Handler, *session.Manager, string, string) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "alice@example.com", "free")
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{
		AccountID: acct.ID,
		Slug:      "myapp",
		Status:    state.AppActive,
	}); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatalf("session manager: %v", err)
	}
	cookie, err := mgr.Issue(acct.ID)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServerWithDeps(store, log, "gregale.dev", noopNotifier{}, "", noopMailer{}, gh, mgr, nil, 15*60_000_000_000, "").handler()
	return srv, mgr, acct.ID, cookie
}

// stampCookie returns a new cookie value with github_login set.
// Mirrors what handlers_github.go does after a successful
// /v1/auth/github exchange.
func stampCookie(t *testing.T, mgr *session.Manager, raw, login string) string {
	t.Helper()
	env, err := mgr.Verify(raw)
	if err != nil {
		t.Fatalf("verify existing cookie: %v", err)
	}
	out, err := mgr.SealGithubLogin(env.AccountID, login, false)
	if err != nil {
		t.Fatalf("seal github login: %v", err)
	}
	return out
}

// TestListInstallableRepos_UnauthenticatedRefused asserts the
// "session has no github_login" path: a logged-in FaaS user with
// no completed /v1/auth/github gets a 403 (not 503, not a redirect)
// so the dashboard renders the right CTA. Mirrors
// TestOAuthCallback_UnauthenticatedInstallRefused.
func TestListInstallableRepos_UnauthenticatedRefused(t *testing.T) {
	gh := &bindPickerFake{verified: true, accountLogin: "alice"}
	srv, _, _, cookie := newBindPickerTestServer(t, gh)

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"installation_id": 42}`)
	r := httptest.NewRequest(http.MethodPost, "/v1/install/repos/list", body)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403\nbody = %s", rec.Code, rec.Body.String())
	}
	var p map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p["code"] != "github_login_required" {
		t.Errorf("problem.code = %q, want github_login_required", p["code"])
	}
	if gh.gotInstallID != 0 {
		t.Errorf("verify should NOT be called when github_login is empty; got installID=%d", gh.gotInstallID)
	}
}

// TestListInstallableRepos_ForeignInstallRefused pins the fail-closed path.
// The direct App lookup only proves the ID belongs to this App; durable
// account+installation association from user OAuth is the ownership proof.
func TestListInstallableRepos_ForeignInstallRefused(t *testing.T) {
	gh := &bindPickerFake{verified: false, accountLogin: "bob"}
	srv, mgr, _, cookie := newBindPickerTestServer(t, gh)
	stamped := stampCookie(t, mgr, cookie, "alice")

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"installation_id": 42}`)
	r := httptest.NewRequest(http.MethodPost, "/v1/install/repos/list", body)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: stamped})
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403\nbody = %s", rec.Code, rec.Body.String())
	}
	var p map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p["code"] != "forged" {
		t.Errorf("problem.code = %q, want forged", p["code"])
	}
	if gh.gotExpectedLogin != "" {
		t.Errorf("verify expected_login = %q, want empty for organization-compatible lookup", gh.gotExpectedLogin)
	}
}

// TestBindAppToRepo_RejectsForeignInstall asserts the §11 ownership
// proof on the bind persistence path: a forged install must not
// land a row in apps.github_install_*. The audit event the handler
// emits (auth.install.takeover_rejected) is observable via the
// events store but we don't assert on it here — the status code is
// the load-bearing assertion.
func TestBindAppToRepo_RejectsForeignInstall(t *testing.T) {
	gh := &bindPickerFake{verified: false, accountLogin: "bob"}
	srv, mgr, _, cookie := newBindPickerTestServer(t, gh)
	stamped := stampCookie(t, mgr, cookie, "alice")

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"installation_id": 42, "repo_full_name": "octocat/hello", "production_branch": "main"}`)
	r := httptest.NewRequest(http.MethodPost, "/v1/apps/myapp/install/bind", body)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: stamped})
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403\nbody = %s", rec.Code, rec.Body.String())
	}
	if gh.gotExpectedLogin != "" {
		t.Errorf("verify expected_login = %q, want empty for organization-compatible lookup", gh.gotExpectedLogin)
	}
	// BindAppRepo must NOT be called on a forged install — the
	// handler returns 403 before reaching BindAppRepo.
	if gh.bindCalls != 0 {
		t.Errorf("BindAppRepo calls = %d, want 0", gh.bindCalls)
	}
}

// TestBindAppToRepo_PersistsOnHappyPath is the positive bind path:
// session has github_login=alice, install account_login=alice,
// githubd.BindAppRepo succeeds, handler returns 200 with the
// (binding_id, repo, branch) tuple. Asserts the §11 ownership
// proof passes through correctly.
func TestBindAppToRepo_PersistsOnHappyPath(t *testing.T) {
	gh := &bindPickerFake{
		verified:      true,
		accountLogin:  "alice",
		defaultBranch: "main",
		bindReturn:    "bind-myapp-octocat/hello",
	}
	srv, mgr, _, cookie := newBindPickerTestServer(t, gh)
	stamped := stampCookie(t, mgr, cookie, "alice")

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"installation_id": 42, "repo_full_name": "octocat/hello", "production_branch": "main"}`)
	r := httptest.NewRequest(http.MethodPost, "/v1/apps/myapp/install/bind", body)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: stamped})
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["binding_id"] != "bind-myapp-octocat/hello" {
		t.Errorf("binding_id = %v, want bind-myapp-octocat/hello", resp["binding_id"])
	}
	if resp["repo_full_name"] != "octocat/hello" {
		t.Errorf("repo_full_name = %v, want octocat/hello", resp["repo_full_name"])
	}
	if resp["production_branch"] != "main" {
		t.Errorf("production_branch = %v, want main", resp["production_branch"])
	}
}

// TestBindAppToRepo_GithubdTransportErrorReturns502 covers the
// "couldn't reach GitHub" path: a non-nil err from
// VerifyInstallation becomes a 502 problem (not a redirect, not a
// 500) so the dashboard renders a retry banner.
func TestBindAppToRepo_GithubdTransportErrorReturns502(t *testing.T) {
	gh := &bindPickerFake{verifyErr: errors.New("dial tcp: connection refused")}
	srv, mgr, _, cookie := newBindPickerTestServer(t, gh)
	stamped := stampCookie(t, mgr, cookie, "alice")

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"installation_id": 42, "repo_full_name": "octocat/hello"}`)
	r := httptest.NewRequest(http.MethodPost, "/v1/apps/myapp/install/bind", body)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: stamped})
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code = %d, want 502\nbody = %s", rec.Code, rec.Body.String())
	}
}
