//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgProjectPlatformTenantPolicyLifecycle(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "project-policy@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "customer-platform", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanHobby)
	app := state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "customer-api", WorkloadName: "customer-api", PlatformTenantRequired: true}
	result, err := store.ApplyProjectReconcile(ctx, project, []state.ProjectReconcileMutation{{Op: "create", App: app, SetPlatformTenantRequired: true}}, nil, state.ProjectScanSourceCompose, limits)
	if err != nil || len(result.Added) != 1 || !result.Added[0].PlatformTenantRequired {
		t.Fatalf("create=%+v, %v", result, err)
	}
	app = result.Added[0]
	for _, tc := range []struct {
		name    string
		restore bool
		set     bool
		value   bool
		want    bool
	}{
		{name: "omit during update", want: true},
		{name: "omit during restore", restore: true, want: true},
		{name: "disable during restore", restore: true, set: true},
		{name: "enable during update", set: true, value: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutation := state.ProjectReconcileMutation{Op: "update", App: app, SetPlatformTenantRequired: tc.set}
			// Model a stale planning snapshot when the field is omitted.
			mutation.App.PlatformTenantRequired = tc.value
			if tc.restore {
				if _, err := store.SoftDeleteAppCascade(ctx, app.ID); err != nil {
					t.Fatal(err)
				}
				mutation.Op = "create"
				mutation.App.ID = ""
			}
			if _, err := store.ApplyProjectReconcile(ctx, project, []state.ProjectReconcileMutation{mutation}, nil, state.ProjectScanSourceCompose, limits); err != nil {
				t.Fatal(err)
			}
			got, err := store.AppByID(ctx, app.ID)
			if err != nil || got.PlatformTenantRequired != tc.want || got.Status != state.AppActive {
				t.Fatalf("policy=%v status=%s err=%v", got.PlatformTenantRequired, got.Status, err)
			}
			app = got
		})
	}
}

func TestPgApplyProjectPlanPersistsPlatformTenantPolicy(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "legacy-project-policy@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	project := state.Project{AccountID: account.ID, Slug: "customer-platform", ScanSource: state.ProjectScanSourceCompose}
	_, apps, _, err := store.ApplyProjectPlan(ctx, project, []state.App{{Slug: "customer-api", WorkloadName: "customer-api", WorkloadClass: state.WorkloadClassHTTP, PlatformTenantRequired: true}}, nil, api.MustLimitsFor(api.PlanHobby))
	if err != nil || len(apps) != 1 || !apps[0].PlatformTenantRequired {
		t.Fatalf("legacy project app=%+v err=%v", apps, err)
	}
}
