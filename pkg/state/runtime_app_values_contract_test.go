// adr: 590
package state_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemRuntimeAppValuesScopeGrantsAndLifetime(t *testing.T) {
	testRuntimeAppValuesScopeGrantsAndLifetime(t, state.NewMemStore())
}

func TestMemRuntimeAppValuesPreviewSkipsProjectEnvironment(t *testing.T) {
	testRuntimeAppValuesPreviewSkipsProjectEnvironment(t, state.NewMemStore())
}

// A PR preview keeps its parent's project, and its builds land in the default
// scope, which maps to production. Every wake handed the preview's unreviewed
// code the production project environment's configuration and secrets.
func testRuntimeAppValuesPreviewSkipsProjectEnvironment(t *testing.T, store runtimeAppEnvTestStore) {
	f := seedRuntimeAppEnv(t, store)
	ctx := t.Context()
	secrets, ok := any(store).(state.ProjectEnvironmentSecretStore)
	if !ok {
		t.Fatalf("%T does not keep project environment secrets", store)
	}
	values, hash, err := api.NormalizeProjectEnvironmentConfig(json.RawMessage(`{"SHARED_MODE":"production"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{AccountID: f.account.ID,
		ProjectID: f.project.ID, EnvironmentSlug: "production", ConfigHash: hash, Values: values}); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.SetProjectEnvironmentSecret(ctx, state.ProjectEnvironmentSecret{AccountID: f.account.ID, ProjectID: f.project.ID,
		EnvironmentSlug: "production", Key: "STRIPE_KEY", Ciphertext: []byte("production-sealed"), Kid: "age1previewtestkid", ValueHash: "hash-1"}, 10); err != nil {
		t.Fatal(err)
	}
	parent, err := store.RuntimeAppValuesForDeployment(ctx, f.account.ID, f.app.ID, f.deployments["default"].ID)
	if err != nil || len(parent.Inherited) != 1 || len(parent.InheritedSecrets) != 1 || parent.InheritedSecrets[0].Key != "STRIPE_KEY" {
		t.Fatalf("parent inheritance = %+v / %+v, %v; want the production config and secret", parent.Inherited, parent.InheritedSecrets, err)
	}
	preview, err := store.CreateApp(ctx, state.App{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "pr-7-" + f.app.Slug,
		Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker, WorkloadName: "preview", RAMMB: 256, MaxConcurrency: 1,
		PreviewOfSlug: f.app.Slug, PreviewPrNumber: 7, PreviewPrState: state.PreviewPrStateOpen})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: preview.ID, Kind: state.DeploymentKindPreview})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.RuntimeAppValuesForDeployment(ctx, f.account.ID, preview.ID, dep.ID)
	if err != nil || len(got.Inherited) != 0 || len(got.InheritedSecrets) != 0 {
		t.Fatalf("preview inherited %+v / %+v (%v); want neither production config nor secrets", got.Inherited, got.InheritedSecrets, err)
	}
	env, err := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, preview.ID, dep.ID)
	if err != nil || len(env.Inherited) != 0 {
		t.Fatalf("preview env inherited %+v (%v); want no production config", env.Inherited, err)
	}

	// adr: 20261028100000 — the production app's preview scope is what its PR
	// previews receive; a value the preview sets itself still wins.
	for key, value := range map[string]string{"MODE": "review", "LOG_LEVEL": "debug"} {
		if err := store.UpsertAppEnvInScope(ctx, f.account.ID, f.app.ID, state.PreviewValuesScope, key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.UpsertAppEnvInScope(ctx, f.account.ID, preview.ID, "default", "LOG_LEVEL", "trace"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, f.account.ID, f.app.ID, state.PreviewValuesScope,
		"STRIPE_KEY", "age1previewtestkid", "hash-2", []byte("preview-sealed")); err != nil {
		t.Fatal(err)
	}
	got, err = store.RuntimeAppValuesForDeployment(ctx, f.account.ID, preview.ID, dep.ID)
	if err != nil || len(got.Inherited) != 1 || got.Inherited[0].Key != "MODE" || got.Inherited[0].Value != "review" ||
		got.Inherited[0].AppID != preview.ID || len(got.InheritedSecrets) != 1 || got.InheritedSecrets[0].Key != "STRIPE_KEY" ||
		string(got.InheritedSecrets[0].Ciphertext) != "preview-sealed" {
		t.Fatalf("preview inherited %+v / %+v (%v); want the production app's preview values below its own", got.Inherited, got.InheritedSecrets, err)
	}
	env, err = store.RuntimeAppEnvForDeployment(ctx, f.account.ID, preview.ID, dep.ID)
	if err != nil || len(env.Inherited) != 1 || env.Inherited[0].Key != "MODE" {
		t.Fatalf("preview env inherited %+v (%v); want the preview-scope MODE", env.Inherited, err)
	}
	if _, err := state.NewRuntimeAppConfigFence(got); err != nil {
		t.Fatalf("runtime config fence over preview values: %v", err)
	}
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
