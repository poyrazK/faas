// RealService tests (slice 8, ADR-012). Covers the full
// githubdgrpc.Service surface: bindings, install-state, write-check.
package githubd

import (
	"context"
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
