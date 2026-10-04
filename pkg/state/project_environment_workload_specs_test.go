// adr: 566
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkloadAppFieldPoliciesCoverEveryAppField(t *testing.T) {
	policies := state.WorkloadAppFieldPolicies()
	appType := reflect.TypeOf(state.App{})
	settingsType := reflect.TypeOf(state.ProjectEnvironmentWorkloadSettings{})
	if len(policies) != appType.NumField() {
		t.Fatalf("%d policies for %d app fields", len(policies), appType.NumField())
	}
	for i := 0; i < appType.NumField(); i++ {
		field := appType.Field(i)
		policy, ok := policies[field.Name]
		if !ok {
			t.Errorf("App.%s needs an environment ownership and clone policy", field.Name)
			continue
		}
		setting, present := settingsType.FieldByName(field.Name)
		switch policy {
		case state.WorkloadFieldCopy, state.WorkloadFieldRemap:
			if !present || setting.Type != field.Type {
				t.Errorf("App.%s configuration missing from workload settings", field.Name)
			}
		case state.WorkloadFieldIdentity, state.WorkloadFieldOperational:
			if present {
				t.Errorf("App.%s must remain outside cloned configuration", field.Name)
			}
		default:
			t.Errorf("App.%s has unknown policy %q", field.Name, policy)
		}
	}
	for i := 0; i < settingsType.NumField(); i++ {
		field := settingsType.Field(i)
		if _, belongsToApp := appType.FieldByName(field.Name); !belongsToApp {
			if (field.Name != "WorkPolicies" || field.Type != reflect.TypeOf((*state.ProjectEnvironmentWorkPolicySettings)(nil))) &&
				(field.Name != "QueueBindings" || field.Type != reflect.TypeOf((*state.ProjectEnvironmentQueueSettings)(nil))) {
				t.Errorf("Settings.%s needs an explicit environment configuration ownership decision", field.Name)
			}
		}
	}
}

type workloadSpecTestStore interface {
	state.Store
	state.ProjectEnvironmentWorkloadSpecStore
	state.DeploymentWorkloadSpecReader
	CloneProjectEnvironment(context.Context, state.ProjectEnvironmentClone, api.Limits) (state.ProjectEnvironment, state.ProjectEnvironmentCloneResult, error)
	RollbackProjectEnvironmentClone(context.Context, string, string, string) error
}

func TestMemEnvironmentWorkloadSettingsAreIsolatedAndPinned(t *testing.T) {
	testEnvironmentWorkloadSettings(t, state.NewMemStore())
}

func testEnvironmentWorkloadSettings(t *testing.T, store workloadSpecTestStore) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "workload-spec-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "spec-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID, ProjectID: project.ID, Slug: "spec-app-" + uuid.NewString()[:8],
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 4, Status: state.AppActive,
		WorkloadClass: state.WorkloadClassHTTP, StartCommand: "serve original",
		Manifest:        state.AppManifest{Env: map[string]string{"MODE": "original"}},
		RetryPolicyJSON: json.RawMessage(`{"max_attempts":2,"backoff_multiplier":1e0}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	settings.PlatformTenantRequired = true
	first, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID, 0, settings)
	if err != nil || first.Revision != 1 {
		t.Fatalf("create workload spec = %+v, %v", first, err)
	}
	first.Settings.Manifest.Env["MODE"] = "caller-mutated"
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	settings.RAMMB, settings.StartCommand = 512, "serve staging"
	settings.PlatformTenantRequired = false
	settings.Manifest.Env["MODE"] = "staging"
	second, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID, 1, settings)
	if err != nil || second.Revision != 2 || second.ID == first.ID || second.Hash == first.Hash {
		t.Fatalf("edit workload spec = %+v, %v", second, err)
	}
	legacyApp, err := state.AppForDeployment(ctx, store, legacy)
	if err != nil || legacyApp.RAMMB != 256 || legacyApp.StartCommand != "serve original" || legacyApp.PlatformTenantRequired {
		t.Fatalf("desired settings changed legacy deployment: %+v, %v", legacyApp, err)
	}
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID, 1, settings); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale edit = %v, want conflict", err)
	}
	pinned, err := state.AppForDeployment(ctx, store, dep)
	if err != nil || pinned.RAMMB != 256 || pinned.StartCommand != "serve original" || pinned.Manifest.Env["MODE"] != "original" || !pinned.PlatformTenantRequired {
		t.Fatalf("tested deployment changed after stage edit: %+v, %v", pinned, err)
	}
	current, err := state.ResolveAppForEnvironment(ctx, store, app, "staging")
	if err != nil || current.RAMMB != 512 || current.Manifest.Env["MODE"] != "staging" || current.PlatformTenantRequired {
		t.Fatalf("current stage configuration = %+v, %v", current, err)
	}
	production, err := store.AppByID(ctx, app.ID)
	if err != nil || production.RAMMB != 256 || production.Manifest.Env["MODE"] != "original" || production.PlatformTenantRequired {
		t.Fatalf("stage edit changed production: %+v, %v", production, err)
	}
	newRAM := 1024
	requireTenant := true
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{RAMMB: &newRAM, SetPlatformTenantRequired: true, PlatformTenantRequired: &requireTenant}); err != nil {
		t.Fatal(err)
	}
	pinned, err = state.AppForDeployment(ctx, store, dep)
	if err != nil || pinned.RAMMB != 256 {
		t.Fatalf("production edit changed pinned stage: RAM=%d, %v", pinned.RAMMB, err)
	}
	if _, err := store.ProjectEnvironmentWorkloadSpecByID(ctx, uuid.NewString(), project.ID, first.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account history = %v", err)
	}
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, uuid.NewString(), "staging", app.ID, 0, settings); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-project write = %v", err)
	}
	latest, err := store.ProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID)
	if err != nil || latest.Hash != second.Hash {
		t.Fatalf("failed edits changed head: %+v, %v", latest, err)
	}
	if _, err := store.PutProjectEnvironmentRoutePolicy(ctx, state.ProjectEnvironmentRoutePolicy{
		AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "production",
		OnlyAllowDeclaredRoutes: true, DeclaredRoutes: []state.DeclaredRoute{{Path: "/production", Methods: []string{"GET"}}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"staging", "production"} {
		target := "copy-" + source
		_, _, err := store.CloneProjectEnvironment(ctx, state.ProjectEnvironmentClone{
			AccountID: account.ID, ProjectID: project.ID, SourceSlug: source, TargetSlug: target,
		}, api.MustLimitsFor(account.Plan))
		if err != nil {
			t.Fatal(err)
		}
		copied, err := store.ProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, target, app.ID)
		wantRAM := 512
		if source == "production" {
			wantRAM = 1024
		}
		if err != nil || copied.Revision != 1 || copied.Settings.RAMMB != wantRAM || copied.Settings.PlatformTenantRequired != (source == "production") {
			t.Fatalf("clone %s configuration = %+v, %v", source, copied, err)
		}
		if source == "production" && (!copied.Settings.OnlyAllowDeclaredRoutes || len(copied.Settings.DeclaredRoutes) != 1 || copied.Settings.DeclaredRoutes[0].Path != "/production") {
			t.Fatalf("legacy environment route override was not materialized: %+v", copied.Settings)
		}
		changed := copied.Settings
		changed.RAMMB = 128
		if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, target, app.ID, copied.Revision, changed); err != nil {
			t.Fatal(err)
		}
		original, err := store.ProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID)
		if err != nil || original.Hash != second.Hash {
			t.Fatalf("copy edit changed source: %+v, %v", original, err)
		}
		if err := store.RollbackProjectEnvironmentClone(ctx, account.ID, project.ID, target); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ProjectEnvironmentWorkloadSpecByID(ctx, account.ID, project.ID, copied.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("rolled back copy retained settings: %v", err)
		}
	}
	if _, err := store.PutProjectEnvironmentRoutePolicy(ctx, state.ProjectEnvironmentRoutePolicy{
		AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "staging",
		OnlyAllowDeclaredRoutes: true, DeclaredRoutes: []state.DeclaredRoute{{Path: "/staging", Methods: []string{"GET"}}},
	}); err != nil {
		t.Fatal(err)
	}
	head, err := store.ProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID)
	if err != nil || head.Revision != second.Revision+1 || !head.Settings.OnlyAllowDeclaredRoutes || head.Settings.RAMMB != 512 {
		t.Fatalf("route edit did not advance desired workload settings atomically: %+v, %v", head, err)
	}
	pinned, err = state.AppForDeployment(ctx, store, dep)
	if err != nil || pinned.OnlyAllowDeclaredRoutes {
		t.Fatalf("route edit changed previously tested deployment: %+v, %v", pinned, err)
	}
	if _, err := store.UpdateProjectEnvironmentProtection(ctx, account.ID, project.ID, "staging", true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutUnprotectedProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, environment.ID, app.ID, second.Revision, settings); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("edit after protection race = %v, want conflict", err)
	}
	if _, err := store.UpdateProjectEnvironmentProtection(ctx, account.ID, project.ID, "staging", false); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProjectEnvironment(ctx, account.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProjectEnvironmentWorkloadSpecByID(ctx, account.ID, project.ID, first.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted stage history = %v", err)
	}
}
