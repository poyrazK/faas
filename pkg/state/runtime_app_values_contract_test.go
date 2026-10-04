// adr: 566
package state_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemRuntimeAppValuesScopeGrantsAndLifetime(t *testing.T) {
	testRuntimeAppValuesScopeGrantsAndLifetime(t, state.NewMemStore())
}

func testRuntimeAppValuesScopeGrantsAndLifetime(t *testing.T, store runtimeAppEnvTestStore) {
	f := seedRuntimeAppEnv(t, store)
	ctx := t.Context()
	for _, scope := range []string{"default", "production", "stage", "other"} {
		cipher := append(bytes.Repeat([]byte(scope+"-sealed-"), 30), 0, 255)
		if err := store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, scope, "PRIVATE", cipher); err != nil {
			t.Fatal(err)
		}
		got, err := store.RuntimeAppValuesForDeployment(ctx, f.account.ID, f.app.ID, f.deployments[scope].ID)
		if err != nil || got.Scope != scope || got.AppID != f.app.ID || got.AccountID != f.account.ID || got.DeploymentID != f.deployments[scope].ID ||
			len(got.Values) != 1 || got.Values[0].Value != scope || len(got.Secrets) != 1 || !bytes.Equal(got.Secrets[0].Ciphertext, cipher) ||
			got.Secrets[0].Scope != scope || got.Secrets[0].DeliveryVersion != 1 {
			t.Fatalf("scope %s values: %+v %v", scope, got.RuntimeAppEnvSnapshot, err)
		}
		got.Secrets[0].Ciphertext[0] = '!'
		again, err := store.RuntimeAppValuesForDeployment(ctx, f.account.ID, f.app.ID, f.deployments[scope].ID)
		if err != nil || !bytes.Equal(again.Secrets[0].Ciphertext, cipher) {
			t.Fatalf("caller changed ciphertext: %v", err)
		}
	}
	decl := json.RawMessage(`[{"name":"helper","type":"sidecar","image":"helper:latest","env_secrets":{"SIDE":"secret:SIDE"}}]`)
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "stage", Kind: state.DeploymentKindImage,
		OverrideEnvSecrets: json.RawMessage(`{"PRIVATE":"secret:PRIVATE"}`), Sidecars: decl})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentSecretReloadSignal(ctx, dep.ID, "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentSidecarSecretReloadSignal(ctx, dep.ID, "helper", "SIGUSR1"); err != nil {
		t.Fatal(err)
	}
	got, err := store.RuntimeAppValuesForDeployment(ctx, f.account.ID, f.app.ID, dep.ID)
	if err != nil || got.SecretGrants.ReloadSignal != "SIGHUP" || got.SecretGrants.SidecarReloadSignals["helper"] != "SIGUSR1" ||
		!json.Valid(got.SecretGrants.OverrideEnvSecrets) || !json.Valid(got.SecretGrants.Sidecars) {
		t.Fatalf("grant snapshot: %+v %v", got.SecretGrants, err)
	}
	got.SecretGrants.OverrideEnvSecrets[0] = '!'
	got.SecretGrants.Sidecars[0] = '!'
	got.SecretGrants.SidecarReloadSignals["helper"] = "invalid"
	got, err = store.RuntimeAppValuesForDeployment(ctx, f.account.ID, f.app.ID, dep.ID)
	if err != nil || !json.Valid(got.SecretGrants.OverrideEnvSecrets) || !json.Valid(got.SecretGrants.Sidecars) || got.SecretGrants.SidecarReloadSignals["helper"] != "SIGUSR1" {
		t.Fatalf("caller mutated grants: %v", err)
	}
	if got, err := store.RuntimeAppValuesForDeployment(ctx, uuid.NewString(), f.app.ID, dep.ID); !errors.Is(err, state.ErrNotFound) || len(got.Secrets) != 0 {
		t.Fatalf("foreign account received secrets: %v", err)
	}
	if err := store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "stage"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "PRIVATE", []byte("replacement-private")); err != nil {
		t.Fatal(err)
	}
	if got, err := store.RuntimeAppValuesForDeployment(ctx, f.account.ID, f.app.ID, dep.ID); !errors.Is(err, state.ErrNotFound) || len(got.Secrets) != 0 || len(got.Values) != 0 {
		t.Fatalf("old VM adopted replacement values: %v", err)
	}
}
