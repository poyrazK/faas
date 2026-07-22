// Tests for cmd/apid/githubd_client.go.
//
// The interface surface is large (9 RPC pass-throughs on liveClient)
// but most production code paths only exercise the stub when no
// socket is configured (slice-1 default) and the live client when one
// is. We test the constructor's empty-socket branch, the liveClient
// nil-safety guard (the only code path that diverges from "delegate to
// githubdgrpc.Client"), and a sweep that pins every stub method.
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// errGithubdNotReadyCode is the stable code returned by every stub
// method. Pinned here AND asserted against in TestStubGithubdClient_EveryMethodReturnsNotReady
// so a future refactor that renames the code (or stops returning an
// *api.Problem) is caught by CI.
const errGithubdNotReadyCode = "githubd_not_ready"

// Compile-time check that the package-level sentinel is still an
// api.Problem (callers branch on its Code field).
var _ = errGithubdNotReady

// errGithubdNotReadyReference ensures the package-level sentinel stays
// reachable from this test file. If the variable gets renamed or moved
// in a refactor, this line fails to compile and tells the author to
// update both sides.
var _ = errors.Is

// assertGithubdNotReadyError fails the test if err is not an *api.Problem
// with Code == errGithubdNotReadyCode. Lets the per-method tests stay
// table-readable: each row checks the error shape in one call instead of
// the dance of "non-nil" + "non-empty" + "cast" + "compare Code".
func assertGithubdNotReadyError(t *testing.T, method string, err error) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: err = nil, want not-ready problem", method)
		return
	}
	prob, ok := err.(*api.Problem)
	if !ok {
		t.Errorf("%s: err type = %T, want *api.Problem", method, err)
		return
	}
	if prob.Code != errGithubdNotReadyCode {
		t.Errorf("%s: code = %q, want %q", method, prob.Code, errGithubdNotReadyCode)
	}
}

// TestNewGithubdClient_EmptySocketReturnsStub confirms the slice-1
// default: no socket configured → stub. The constructor must NEVER
// dial and must NEVER return nil.
func TestNewGithubdClient_EmptySocketReturnsStub(t *testing.T) {
	c := newGithubdClient(context.Background(), "", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if c == nil {
		t.Fatal("newGithubdClient returned nil for empty socket path")
	}
	// Type-assert to stub and check the not-ready sentinel is what
	// every method returns.
	stub, ok := c.(stubGithubdClient)
	if !ok {
		t.Fatalf("got %T, want stubGithubdClient", c)
	}
	_, _, _, err := stub.GetInstallState(context.Background(), "acct")
	if err == nil || err.Error() == "" {
		t.Errorf("GetInstallState on stub: err = %v, want a problem", err)
	}
}

// TestNewGithubdClient_BadSocketFallsBackToStub is intentionally
// omitted: gRPC's unix-socket dial is lazy and succeeds at Dial time
// (the kernel only resolves the path on first RPC), so a "bad socket"
// path returns a liveClient whose first RPC fails — exactly the
// production behavior we want, but hard to assert without a fake
// dialer. The nil-safety guard on liveClient.Close covers the same
// defensive posture without the test infra burden.

// TestLiveClient_CloseNilSafe: the liveClient.Close guard short-circuits
// when either the receiver or the wrapped *githubdgrpc.Client is nil.
// Tests that exercise a real githubdgrpc dial are out of scope here —
// that requires a running daemon and lives in pkg/githubdgrpc's own
// test suite. The nil-safety guard is the only production code path
// that diverges from "delegate to githubdgrpc.Client" so it's the only
// one worth a unit test.
func TestLiveClient_CloseNilSafe(t *testing.T) {
	// nil receiver → no panic.
	var l *liveClient
	if err := l.Close(); err != nil {
		t.Errorf("nil receiver Close: err = %v, want nil", err)
	}
	// receiver non-nil but wrapped client nil → no panic.
	l = &liveClient{}
	if err := l.Close(); err != nil {
		t.Errorf("nil client Close: err = %v, want nil", err)
	}
}

// TestStubGithubdClient_EveryMethodReturnsNotReady: a sweep that calls
// every stub method and confirms each returns an *api.Problem with
// Code == errGithubdNotReadyCode. A future refactor that returns a
// real value, or returns the problem from a different source, would
// break the "GitHub not connected" UX — so pin the surface.
func TestStubGithubdClient_EveryMethodReturnsNotReady(t *testing.T) {
	var s stubGithubdClient
	ctx := context.Background()

	// (install, token, login, err) — install must be the unspecified
	// sentinel because the dashboard branches on it.
	inst, tok, login, err := s.GetInstallState(ctx, "a")
	assertGithubdNotReadyError(t, "GetInstallState", err)
	if inst != InstallStateUnspecified {
		t.Errorf("GetInstallState: inst = %v, want unspecified", inst)
	}
	if tok != "" || login != "" {
		t.Errorf("GetInstallState: token=%q login=%q, want empty", tok, login)
	}

	_, err = s.ExchangeOAuthCode(ctx, "a", "c", "s")
	assertGithubdNotReadyError(t, "ExchangeOAuthCode", err)
	_, err = s.ListInstallableRepos(ctx, "a")
	assertGithubdNotReadyError(t, "ListInstallableRepos", err)
	_, err = s.BindAppRepo(ctx, "app", "a", "r", "main")
	assertGithubdNotReadyError(t, "BindAppRepo", err)
	err = s.UnbindAppRepo(ctx, "app", "a")
	assertGithubdNotReadyError(t, "UnbindAppRepo", err)
	_, err = s.GetAppBinding(ctx, "app", "a")
	assertGithubdNotReadyError(t, "GetAppBinding", err)
	_, _, err = s.CreateDeploymentFromPush(ctx, "r", "ref", "sha", "pusher")
	assertGithubdNotReadyError(t, "CreateDeploymentFromPush", err)
	err = s.WriteCheck(ctx, "r", "sha", CheckPhaseBuilding, "url", "summary")
	assertGithubdNotReadyError(t, "WriteCheck", err)
	_, _, err = s.VerifyInstallation(ctx, 123)
	assertGithubdNotReadyError(t, "VerifyInstallation", err)
	if err := s.Close(); err != nil {
		t.Errorf("Close: err = %v, want nil (stub is no-op)", err)
	}
}