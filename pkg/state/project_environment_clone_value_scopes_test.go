// adr: 583
package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneValueScopeStore interface {
	state.Store
	CloneProjectEnvironment(context.Context, state.ProjectEnvironmentClone, api.Limits) (state.ProjectEnvironment, state.ProjectEnvironmentCloneResult, error)
	state.ProjectEnvironmentCloneValuesStore
}

func TestMemCloneCapturesEffectiveProductionValueScopes(t *testing.T) {
	testCloneEffectiveProductionValueScopes(t, state.NewMemStore())
}

func testCloneEffectiveProductionValueScopes(t *testing.T, store cloneValueScopeStore) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "clone-value-scopes@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "clone-value-scopes"})
	if err != nil {
		t.Fatal(err)
	}
	var apps []state.App
	expectedScopes := map[string]string{}
	for _, tc := range []struct{ slug, serving, values string }{
		{"legacy", "default", "default"}, {"named", "production", "production"},
		{"undeployed-named", "", "production"}, {"undeployed-legacy", "", "default"},
	} {
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID,
			Slug: tc.slug, WorkloadName: tc.slug, RAMMB: 128, CPUMillicores: 250, Status: state.AppActive})
		if err != nil {
			t.Fatalf("create %s workload: %v", tc.slug, err)
		}
		apps = append(apps, app)
		if tc.serving != "" {
			deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: tc.serving,
				Kind: state.DeploymentKindImage, ImageDigest: "sha256:scopes", Status: state.DeployPending, TrafficPercent: 100})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
				t.Fatal(err)
			}
			ignored := "production"
			if tc.serving == "production" {
				ignored = "default"
			}
			if err := store.UpsertAppEnvInScope(ctx, account.ID, app.ID, ignored, "IGNORED", "not-serving"); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.UpsertAppEnvInScope(ctx, account.ID, app.ID, tc.values, "MODE", tc.values); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAppSecretWithClassInScope(ctx, account.ID, app.ID, tc.values,
			"TOKEN", "age1-test", "1111111111111111", state.SecretClassEphemeral, []byte("sealed-"+tc.values)); err != nil {
			t.Fatal(err)
		}
		scope, err := state.ProjectEnvironmentCloneValueScope(ctx, store, account.ID, app.ID, "production")
		if err != nil || scope != tc.values {
			t.Fatalf("%s source scope = %q, %v; want %q", app.Slug, scope, err, tc.values)
		}
		expectedScopes[app.ID] = scope
	}
	preview, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID,
		Slug: "legacy-pr-42", PreviewOfSlug: apps[0].Slug, PreviewPrNumber: 42, RAMMB: 128, CPUMillicores: 250, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(ctx, account.ID, preview.ID, "production", "PREVIEW", "preview-only"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.CaptureProjectEnvironmentCloneValues(ctx, account.ID, project.ID, "production")
	if err != nil || len(snapshot.Hash) != 64 || len(snapshot.ValueScopes) != len(expectedScopes) {
		t.Fatalf("capture source values: %+v, %v", snapshot, err)
	}
	clone := state.ProjectEnvironmentClone{AccountID: account.ID, ProjectID: project.ID,
		SourceSlug: "production", TargetSlug: "staging", ExpectedSourceValueScopes: expectedScopes, ExpectedSourceValuesHash: snapshot.Hash}
	_, result, err := store.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(api.PlanPro))
	if err != nil || result.VariablesCopied != 4 || result.SecretsCopied != 4 || result.WorkloadsCopied != 4 {
		t.Fatalf("clone lost effective values or copied previews: %+v, %v", result, err)
	}
	for _, app := range apps {
		values, err := store.ListAppEnvInScope(ctx, account.ID, app.ID, "staging")
		if err != nil || len(values) != 1 || values[0].Key != "MODE" || values[0].Value != expectedScopes[app.ID] {
			t.Fatalf("%s cloned values = %+v, %v", app.Slug, values, err)
		}
		secrets, err := store.ListAppSecretsInScope(ctx, account.ID, app.ID, "staging")
		if err != nil || len(secrets) != 1 || string(secrets[0].Ciphertext) != "sealed-"+expectedScopes[app.ID] || secrets[0].SecretClass != state.SecretClassEphemeral {
			t.Fatalf("%s cloned secrets = %+v, %v", app.Slug, secrets, err)
		}
	}
	if values, err := store.ListAppEnvInScope(ctx, account.ID, preview.ID, "staging"); err != nil || len(values) != 0 {
		t.Fatalf("preview entered project clone: %+v, %v", values, err)
	}
	// Target writes and changes outside the serving namespaces are irrelevant.
	if err := store.UpsertAppEnvInScope(ctx, account.ID, apps[1].ID, "default", "UNSELECTED", "changed"); err != nil {
		t.Fatal(err)
	}
	again, err := store.CaptureProjectEnvironmentCloneValues(ctx, account.ID, project.ID, "production")
	if err != nil || again.Hash != snapshot.Hash {
		t.Fatalf("target or unselected values invalidated capture: %+v, %v", again, err)
	}
	for _, mutation := range []struct {
		name string
		edit func() error
	}{
		{"variable-edit", func() error {
			return store.UpsertAppEnvInScope(ctx, account.ID, apps[0].ID, "default", "MODE", "changed")
		}},
		{"secret-rotation", func() error {
			return store.UpsertAppSecretWithClassInScope(ctx, account.ID, apps[1].ID, "production", "TOKEN", "age1-test", "2222222222222222", state.SecretClassPersistent, []byte("sealed-rotated"))
		}},
		{"variable-delete", func() error { return store.DeleteAppEnvInScope(ctx, account.ID, apps[2].ID, "production", "MODE") }},
	} {
		captured, err := store.CaptureProjectEnvironmentCloneValues(ctx, account.ID, project.ID, "production")
		if err != nil {
			t.Fatal(err)
		}
		if err := mutation.edit(); err != nil {
			t.Fatal(err)
		}
		clone.ExpectedSourceValuesHash, clone.TargetSlug = captured.Hash, mutation.name
		if _, _, err := store.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(api.PlanPro)); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("%s was not fenced: %v", mutation.name, err)
		}
		if _, err := store.ProjectEnvironmentBySlug(ctx, account.ID, project.ID, clone.TargetSlug); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("rejected %s clone left a target: %v", mutation.name, err)
		}
	}
	// Cutover after provider preparation invalidates the captured scope roster.
	cutover, err := store.CreateDeployment(ctx, state.Deployment{AppID: apps[0].ID, Scope: "production",
		Kind: state.DeploymentKindImage, ImageDigest: "sha256:cutover", Status: state.DeployPending, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, cutover.ID); err != nil {
		t.Fatal(err)
	}
	clone.TargetSlug = "race-blocked"
	if _, _, err := store.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(api.PlanPro)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("source scope cutover was not fenced: %v", err)
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, account.ID, project.ID, clone.TargetSlug); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rejected clone left target: %v", err)
	}
}
