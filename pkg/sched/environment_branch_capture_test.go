package sched_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261031193635 slice 5a: a capture boot never receives a managed
// binding secret. Each becomes a placeholder in the plain env; customer
// secrets stay sealed.
func TestCredentialFreeCaptureEnvReplacesManagedSecrets(t *testing.T) {
	rows := []state.AppSecret{
		{Key: "DATABASE_URL", ManagedPostgresBindingID: "binding-1"},
		{Key: "GREGALE_S3_ASSETS_ENDPOINT", ManagedObjectStorageCredentialID: "credential-1"},
		{Key: "GREGALE_S3_ASSETS_SECRET_ACCESS_KEY", ManagedObjectStorageCredentialID: "credential-1"},
		{Key: "STRIPE_KEY"},
	}
	entries := []fcvm.SealedEnvEntry{
		{Key: "DATABASE_URL", Ciphertext: []byte("sealed-db")},
		{Key: "READ_DB", SourceKey: "DATABASE_URL", Ciphertext: []byte("sealed-db")}, // an alias
		{Key: "GREGALE_S3_ASSETS_ENDPOINT", Ciphertext: []byte("sealed-endpoint")},
		{Key: "GREGALE_S3_ASSETS_SECRET_ACCESS_KEY", Ciphertext: []byte("sealed-key")},
		{Key: "STRIPE_KEY", Ciphertext: []byte("sealed-stripe")},
	}
	candidates := []state.AppSecretDeliveryCandidate{{Scope: "production", Key: "DATABASE_URL"}, {Scope: "production", Key: "STRIPE_KEY"}}
	apiEnv := []fcvm.APIEnvEntry{{Key: "DATABASE_URL", Value: "postgres://shadowed@prod/db"}, {Key: "LOG_LEVEL", Value: "info"}}

	sealed, delivered, env := sched.XCredentialFreeCaptureEnv(entries, candidates, apiEnv, rows)
	if len(sealed) != 1 || sealed[0].Key != "STRIPE_KEY" {
		t.Fatalf("sealed env = %+v, want only the customer secret", sealed)
	}
	if len(delivered) != 1 || delivered[0].Key != "STRIPE_KEY" {
		t.Fatalf("delivery candidates = %+v", delivered)
	}
	plain := map[string]string{}
	for _, entry := range env {
		if _, dup := plain[entry.Key]; dup {
			t.Fatalf("plain env repeats %s: %+v", entry.Key, env)
		}
		plain[entry.Key] = entry.Value
	}
	want := map[string]string{
		"DATABASE_URL":                        "postgres://placeholder:placeholder@credential-free.invalid/placeholder",
		"READ_DB":                             "postgres://placeholder:placeholder@credential-free.invalid/placeholder",
		"GREGALE_S3_ASSETS_ENDPOINT":          "https://credential-free.invalid/placeholder",
		"GREGALE_S3_ASSETS_SECRET_ACCESS_KEY": state.CredentialFreePlaceholder,
		"LOG_LEVEL":                           "info",
	}
	for key, value := range want {
		if plain[key] != value {
			t.Errorf("%s = %q, want %q", key, plain[key], value)
		}
	}
	for key, value := range plain {
		if strings.Contains(value, "prod") || strings.Contains(value, "shadowed") {
			t.Errorf("%s still carries a production value: %q", key, value)
		}
	}
	if len(plain) != len(want) {
		t.Fatalf("plain env = %v", plain)
	}
}

func TestCredentialFreeCaptureRefusesSidecarManagedSecrets(t *testing.T) {
	rows := []state.AppSecret{{Key: "DATABASE_URL", ManagedPostgresBindingID: "binding-1"}, {Key: "STRIPE_KEY"}}
	clean := []fcvm.WorkloadSpec{{SealedSecrets: []fcvm.SealedEnvEntry{{Key: "STRIPE_KEY"}}, GrantedEnvNames: []string{"STRIPE_KEY"}}}
	if sched.XSidecarsReceiveManagedSecrets(clean, rows) {
		t.Fatal("a customer secret refused the capture")
	}
	for _, sidecar := range []fcvm.WorkloadSpec{
		{SealedSecrets: []fcvm.SealedEnvEntry{{Key: "DB", SourceKey: "DATABASE_URL"}}},
		{GrantedEnvNames: []string{"DATABASE_URL"}},
	} {
		if !sched.XSidecarsReceiveManagedSecrets([]fcvm.WorkloadSpec{sidecar}, rows) {
			t.Fatalf("sidecar %+v receives a managed secret", sidecar)
		}
	}
}

// A cold-booting branch of an app that opts into branch restores queues its
// lineage parent's capture; an app that does not, or a production deployment,
// queues nothing.
func TestBranchColdBootRequestsTheParentsCapture(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	app, dep := seedEnvironmentBranch(t, store, api.ExecutionModeRequest, false)
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	parent, err := store.EnvironmentBranchLineageParent(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}

	engine.XRequestEnvironmentBranchCapture(ctx, app, dep)
	if _, err := store.EnvironmentBranchCapture(ctx, parent); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("an app without the opt-in queued a capture: %v", err)
	}
	app.Manifest.AfterRestore = &api.AfterRestoreHook{Path: "/internal/after-restore", Identity: api.AfterRestoreIdentityReload}
	parentDeployment, err := store.DeploymentByID(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	engine.XRequestEnvironmentBranchCapture(ctx, app, parentDeployment)
	if _, err := store.EnvironmentBranchCapture(ctx, parent); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("a production cold boot queued a capture: %v", err)
	}
	engine.XRequestEnvironmentBranchCapture(ctx, app, dep)
	capture, err := store.EnvironmentBranchCapture(ctx, parent)
	if err != nil || capture.State != state.EnvironmentBranchCaptureRequested {
		t.Fatalf("capture = %+v, %v", capture, err)
	}
}
