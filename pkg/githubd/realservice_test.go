// RealService tests (slice 8, ADR-012). Covers the full
// githubdgrpc.Service surface: bindings, install-state, write-check.
package githubd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

func TestRealService_BindAndLookup(t *testing.T) {
	svc := NewRealService(nil, nil, nil)
	id, err := svc.BindAppRepo("app-1", "acct-1", "octo/api", "main")
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty binding id")
	}
	b, err := svc.GetAppBinding("app-1", "acct-1")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if b.RepoFullName != "octo/api" {
		t.Errorf("repo = %q, want octo/api", b.RepoFullName)
	}
	if b.ProductionBranch != "main" {
		t.Errorf("branch = %q, want main", b.ProductionBranch)
	}
	if b.BindingID != id {
		t.Errorf("binding id mismatch: got %q, want %q", b.BindingID, id)
	}
}

func TestRealService_BindDefaultsToMain(t *testing.T) {
	svc := NewRealService(nil, nil, nil)
	if _, err := svc.BindAppRepo("app-2", "acct-1", "octo/api", ""); err != nil {
		t.Fatal(err)
	}
	b, _ := svc.GetAppBinding("app-2", "acct-1")
	if b.ProductionBranch != "main" {
		t.Errorf("default branch = %q, want main", b.ProductionBranch)
	}
}

func TestRealService_UnbindRemovesBinding(t *testing.T) {
	svc := NewRealService(nil, nil, nil)
	if _, err := svc.BindAppRepo("app-3", "acct-1", "octo/api", "main"); err != nil {
		t.Fatal(err)
	}
	if err := svc.UnbindAppRepo("app-3", "acct-1"); err != nil {
		t.Fatal(err)
	}
	b, _ := svc.GetAppBinding("app-3", "acct-1")
	if b.BindingID != "" {
		t.Errorf("after unbind, binding = %+v, want empty", b)
	}
	// Idempotent: second unbind is a no-op.
	if err := svc.UnbindAppRepo("app-3", "acct-1"); err != nil {
		t.Errorf("second unbind: %v", err)
	}
}

func TestRealService_InstallStateDefaults(t *testing.T) {
	svc := NewRealService(nil, nil, nil)
	state, instID, branch, err := svc.GetInstallState("acct-none")
	if err != nil {
		t.Fatal(err)
	}
	if state != githubdgrpc.InstallStateUnspecified {
		t.Errorf("state = %v, want Unspecified", state)
	}
	if instID != "" || branch != "" {
		t.Errorf("got non-empty install id/branch: %q/%q", instID, branch)
	}
}

func TestRealService_ExchangeOAuthCodePersists(t *testing.T) {
	svc := NewRealService(nil, nil, nil)
	id, err := svc.ExchangeOAuthCode("acct-1", "12345", "main")
	if err != nil {
		t.Fatal(err)
	}
	if id != "12345" {
		t.Errorf("id = %q, want 12345", id)
	}
	state, instID, branch, _ := svc.GetInstallState("acct-1")
	if state != githubdgrpc.InstallStateInstalled {
		t.Errorf("state = %v, want Installed", state)
	}
	if instID != "12345" || branch != "main" {
		t.Errorf("got %q/%q, want 12345/main", instID, branch)
	}
}

func TestRealService_WriteCheckRequiresConfig(t *testing.T) {
	svc := NewRealService(nil, nil, nil)
	err := svc.WriteCheck("octo/api", "abc", githubdgrpc.CheckPhaseQueued, "", "queued")
	if err == nil {
		t.Error("nil Checks writer should error")
	}
}

func TestRealService_ListInstallableReposRequiresAuth(t *testing.T) {
	svc := NewRealService(nil, nil, nil)
	_, err := svc.ListInstallableRepos("acct-1")
	if err == nil {
		t.Error("nil Auth should error")
	}
}

func TestRealService_ExchangeOAuthRejectsEmpty(t *testing.T) {
	svc := NewRealService(nil, nil, nil)
	if _, err := svc.ExchangeOAuthCode("", "1", "main"); err == nil {
		t.Error("empty accountID should error")
	}
	if _, err := svc.ExchangeOAuthCode("acct", "", "main"); err == nil {
		t.Error("empty installationID should error")
	}
}

func TestRealService_CreateDeploymentFromPushIsHTTPPath(t *testing.T) {
	svc := NewRealService(nil, nil, nil)
	_, _, err := svc.CreateDeploymentFromPush("octo/api", "refs/heads/main", "abc", "alice")
	if err == nil {
		t.Error("gRPC CreateDeploymentFromPush should error (webhook path is HTTP)")
	}
}

// _ pins context import so a future refactor that drops the only
// user doesn't drop the import.
var _ = context.Background

// TestRealService_VerifyInstallation_RequiresAuth asserts the
// §11 fail-closed behavior: a RealService built without OAuth
// credentials must refuse VerifyInstallation rather than silently
// returning verified=false (which the dashboard would treat as a
// "forged" callback and could confuse with a transient GitHub
// outage).
func TestRealService_VerifyInstallation_RequiresAuth(t *testing.T) {
	svc := NewRealService(nil, nil, nil)
	verified, _, err := svc.VerifyInstallation(1)
	if err == nil {
		t.Fatal("expected error when Auth is nil, got nil")
	}
	if verified {
		t.Errorf("verified = true, want false when Auth is nil")
	}
}

// TestRealService_VerifyInstallation_ForgedIsNotAnError asserts the
// reviewed contract: a forged installation_id returns
// (false, "", nil) — verified=false with err=nil — so the dashboard
// renders the "forged callback" banner rather than a 5xx page.
// A non-nil err is reserved for transport failures (api.github.com
// unreachable, App JWT rejected).
//
// We exercise this with an httptest.Server that returns 404 for
// every /app/installations/{id} request, mirroring GitHub's
// response to an unknown install_id.
func TestRealService_VerifyInstallation_ForgedIsNotAnError(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/app/installations/") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer fake.Close()

	auth := &AppAuth{AppID: "1", PrivateKey: newTestKey(t), HTTPClient: &singleHostClient{base: fake.Client(), api: fake.URL}}
	svc := NewRealService(auth, nil, nil)
	verified, branch, err := svc.VerifyInstallation(9999999)
	if err != nil {
		t.Fatalf("err = %v, want nil for forged install_id", err)
	}
	if verified {
		t.Errorf("verified = true, want false for forged install_id")
	}
	if branch != "" {
		t.Errorf("branch = %q, want empty for forged install_id", branch)
	}
}

// TestRealService_VerifyInstallation_TransportErrorIsErr asserts the
// inverse: a 5xx from api.github.com (anything not 200/404) comes
// back as a non-nil err so the dashboard can render a "couldn't
// reach GitHub" banner instead of a "forged callback" banner.
func TestRealService_VerifyInstallation_TransportErrorIsErr(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	}))
	defer fake.Close()

	auth := &AppAuth{AppID: "1", PrivateKey: newTestKey(t), HTTPClient: &singleHostClient{base: fake.Client(), api: fake.URL}}
	svc := NewRealService(auth, nil, nil)
	verified, _, err := svc.VerifyInstallation(1)
	if err == nil {
		t.Fatal("err = nil, want non-nil for 5xx response")
	}
	if verified {
		t.Errorf("verified = true, want false when err is non-nil")
	}
}
