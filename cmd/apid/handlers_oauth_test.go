package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeGithubdClient is a recording GithubdClient that the OAuth
// callback tests can program with a canned (verified, default_branch)
// response. It also records the (accountID, installationID) tuple so
// we can assert VerifyInstallation was called with the exact
// installation_id the dashboard saw in the callback URL — i.e. the
// handler did not rewrite or fabricate the id before the verify.
type fakeGithubdClient struct {
	GithubdClient

	verified      bool
	defaultBranch string
	verifyErr     error

	gotInstallID int64
}

func (f *fakeGithubdClient) VerifyInstallation(_ context.Context, installID int64) (bool, string, error) {
	f.gotInstallID = installID
	return f.verified, f.defaultBranch, f.verifyErr
}

// newOAuthTestServer seeds an account into MemStore + mints a
// session cookie, then returns the apid handler with a programmable
// GithubdClient so the callback tests can flip verified / branch /
// error without touching a real socket.
func newOAuthTestServer(t *testing.T, gh GithubdClient) (http.Handler, *http.Cookie) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "alice@example.com", "free")
	if err != nil {
		t.Fatalf("seed account: %v", err)
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
	srv := newServerWithDeps(store, log, "example.com", noopNotifier{}, "", noopMailer{}, gh, mgr, nil, 15*60_000_000_000, "")
	return srv.handler(), &http.Cookie{Name: sessionCookie, Value: cookie}
}

// TestOAuthCallback_VerifiedRedirectsToBindPicker is the happy path:
// real install → /dashboard/apps/new?install=N&default_branch=B.
func TestOAuthCallback_VerifiedRedirectsToBindPicker(t *testing.T) {
	const installID = 4242
	gh := &fakeGithubdClient{verified: true, defaultBranch: "main"}
	srv, cookie := newOAuthTestServer(t, gh)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/oauth/callback?installation_id="+strconv.FormatInt(installID, 10)+"&setup_action=install", nil)
	r.AddCookie(cookie)
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302\nbody = %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	// url.Values.Encode sorts keys alphabetically, so default_branch
	// appears before install in the canonical form.
	want := "/dashboard/apps/new?default_branch=main&install=4242"
	if loc != want {
		t.Errorf("redirect = %q, want %q", loc, want)
	}
	if gh.gotInstallID != installID {
		t.Errorf("verify install_id = %d, want %d", gh.gotInstallID, installID)
	}
}

// TestOAuthCallback_ForgedInstallRedirectsToAccountPage asserts the
// §11 fail-closed behavior: a forged installation_id (verified=false)
// must NOT persist anything and must send the user to a banner page,
// not a 5xx page. This is the regression test for review finding #2.
func TestOAuthCallback_ForgedInstallRedirectsToAccountPage(t *testing.T) {
	gh := &fakeGithubdClient{verified: false}
	srv, cookie := newOAuthTestServer(t, gh)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/oauth/callback?installation_id=9999", nil)
	r.AddCookie(cookie)
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "/dashboard/account?github=forged" {
		t.Errorf("redirect = %q, want /dashboard/account?github=forged", loc)
	}
}

// TestOAuthCallback_GithubdTransportErrorReturns502 covers the
// "couldn't reach GitHub" path: a non-nil err from VerifyInstallation
// becomes a 502 problem (not a redirect, not a 500) so the dashboard
// renders a retry banner.
func TestOAuthCallback_GithubdTransportErrorReturns502(t *testing.T) {
	gh := &fakeGithubdClient{verifyErr: errors.New("dial tcp: connection refused")}
	srv, cookie := newOAuthTestServer(t, gh)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/oauth/callback?installation_id=1", nil)
	r.AddCookie(cookie)
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code = %d, want 502\nbody = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("content-type = %q, want application/problem+json", ct)
	}
}

// TestOAuthCallback_RejectsMissingInstallID returns 400 — a forged
// callback without installation_id is malformed, not a githubd error.
func TestOAuthCallback_RejectsMissingInstallID(t *testing.T) {
	gh := &fakeGithubdClient{verified: true}
	srv, cookie := newOAuthTestServer(t, gh)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/oauth/callback", nil)
	r.AddCookie(cookie)
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

// TestOAuthCallback_RejectsNonIntegerInstallID returns 400 when the
// installation_id is unparseable (the GitHub install URL is built
// from a numeric id; anything else is hostile input).
func TestOAuthCallback_RejectsNonIntegerInstallID(t *testing.T) {
	gh := &fakeGithubdClient{verified: true}
	srv, cookie := newOAuthTestServer(t, gh)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/oauth/callback?installation_id=not-a-number", nil)
	r.AddCookie(cookie)
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
	if gh.gotInstallID != 0 {
		t.Errorf("verify should NOT be called for malformed input; got installID=%d", gh.gotInstallID)
	}
}

// TestOAuthCallback_RejectsZeroInstallID asserts the >0 check on
// installationID — "0" and negative numbers are not valid GitHub
// installation IDs and would 404 from api.github.com anyway, but
// catching them client-side is cheaper and avoids a wasted call.
func TestOAuthCallback_RejectsZeroInstallID(t *testing.T) {
	gh := &fakeGithubdClient{verified: true}
	srv, cookie := newOAuthTestServer(t, gh)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/oauth/callback?installation_id=0", nil)
	r.AddCookie(cookie)
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
	if gh.gotInstallID != 0 {
		t.Errorf("verify should NOT be called for install_id=0; got %d", gh.gotInstallID)
	}
}

// TestOAuthCallback_RequiresSessionAuth asserts the /oauth/callback
// route is gated by sessionAuth — an unauthenticated request is
// redirected to /login rather than letting an anonymous attacker
// forge a binding against any account.
func TestOAuthCallback_RequiresSessionAuth(t *testing.T) {
	gh := &fakeGithubdClient{verified: true}
	store := state.NewMemStore()
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatalf("session manager: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServerWithDeps(store, log, "example.com", noopNotifier{}, "", noopMailer{}, gh, mgr, nil, 15*60_000_000_000, "").handler()

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/oauth/callback?installation_id=1", nil)
	// no cookie
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302 (redirect to /login)", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "/login?next=/oauth/callback" {
		t.Errorf("redirect = %q, want /login?next=/oauth/callback", loc)
	}
	if gh.gotInstallID != 0 {
		t.Errorf("verify should NOT be called for unauthed request; got %d", gh.gotInstallID)
	}
}
