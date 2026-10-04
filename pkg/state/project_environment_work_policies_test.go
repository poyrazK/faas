// adr: 581
package state_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

type environmentWorkPolicyTestStore interface {
	state.Store
	state.ProjectEnvironmentWorkPolicyStore
	state.DeploymentWorkloadSpecReader
	state.AppWorkPolicyStore
	state.ProjectReleaseSetStore
	state.ProjectEnvironmentWorkloadQualificationStore
}

func TestMemEnvironmentWorkPoliciesAreIsolatedAndPinned(t *testing.T) {
	testEnvironmentWorkPoliciesAreIsolatedAndPinned(t, state.NewMemStore())
}

func testEnvironmentWorkPoliciesAreIsolatedAndPinned(t *testing.T, store environmentWorkPolicyTestStore) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "policy-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "policy-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "policy-app-" + uuid.NewString()[:8],
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2, Manifest: state.AppManifest{RevisionPinTTLSeconds: 1800, Env: map[string]string{"MODE": "preserved"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"stage", "other"} {
		if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: slug}); err != nil {
			t.Fatal(err)
		}
	}
	policy := workpolicy.Policy{Name: "orders", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingKeepLatest, Debounce: time.Second}
	if _, err := store.UpsertAppWorkPolicy(ctx, account.ID, app.ID, policy); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.EnvironmentWorkPolicies(ctx, store, app, "stage"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing stage collection inherited production: %v", err)
	}
	first, err := state.ReplaceEnvironmentWorkPolicies(ctx, store, app, "stage", 0, nil)
	if err != nil || first.Settings.WorkPolicies == nil || first.Settings.WorkPolicies.Policies == nil || first.Revision != 1 || first.Settings.WorkPolicies.Revision != 1 {
		t.Fatalf("explicit empty = %+v, %v", first, err)
	}
	emptyDeployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "stage", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	policy.Debounce = 2 * time.Second
	if _, err := store.UpsertAppWorkPolicy(ctx, account.ID, app.ID, policy); err != nil {
		t.Fatal(err)
	}
	if records, _, err := state.EnvironmentWorkPolicies(ctx, store, app, "stage"); err != nil || len(records) != 0 {
		t.Fatalf("empty stage changed after production edit: %+v, %v", records, err)
	}
	if _, err := state.WorkPolicyForDeployment(ctx, store, app, emptyDeployment.ID, policy.Name); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("empty deployment inherited production: %v", err)
	}
	policy.Debounce = 3 * time.Second
	record, second, err := state.UpsertEnvironmentWorkPolicy(ctx, store, app, "stage", &first.Revision, policy)
	if err != nil || record.Revision != 2 || second.Settings.Manifest.Env["MODE"] != "preserved" {
		t.Fatalf("stage upsert = %+v, %+v, %v", record, second, err)
	}
	noop, unchanged, err := state.UpsertEnvironmentWorkPolicy(ctx, store, app, "stage", &second.Revision, policy)
	if err != nil || noop.Revision != record.Revision || unchanged.ID != second.ID || unchanged.Hash != second.Hash {
		t.Fatalf("no-op created a config revision: %+v, %+v, %v", noop, unchanged, err)
	}
	if _, _, err := state.UpsertEnvironmentWorkPolicy(ctx, store, app, "stage", &first.Revision, policy); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale no-op accepted: %v", err)
	}
	pinned, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "stage", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	otherPolicy := workpolicy.Policy{Name: "audit", MaxRunningPerKey: 1}
	_, third, err := state.UpsertEnvironmentWorkPolicy(ctx, store, app, "stage", nil, otherPolicy)
	if err != nil || third.Settings.WorkPolicies.Revision != 3 || third.Settings.WorkPolicies.Policies[1].Revision != 2 {
		t.Fatalf("unrelated policy revision changed: %+v, %v", third, err)
	}
	policy.Debounce = 4 * time.Second
	updated, fourth, err := state.UpsertEnvironmentWorkPolicy(ctx, store, app, "stage", nil, policy)
	if err != nil || updated.Revision != 4 || fourth.Settings.WorkPolicies.Policies[0].Revision != 3 {
		t.Fatalf("policy edit clock = %+v, %+v, %v", updated, fourth, err)
	}
	if old, err := state.WorkPolicyForDeployment(ctx, store, app, pinned.ID, policy.Name); err != nil || old.Revision != 2 || old.Policy.Debounce != 3*time.Second {
		t.Fatalf("desired edit changed deployed policies: %+v, %v", old, err)
	}
	// Generic app edits retain the extra collection and make a defensive copy.
	stage, err := store.ProjectEnvironmentBySlug(ctx, account.ID, project.ID, "stage")
	if err != nil {
		t.Fatal(err)
	}
	ram := 512
	withRAM, err := state.UpdateEnvironmentWorkloadSettings(ctx, store, app, stage, &fourth.Revision, state.UpdateAppParams{RAMMB: &ram})
	if err != nil || withRAM.Settings.WorkPolicies.Revision != 4 || withRAM.Settings.RAMMB != 512 {
		t.Fatalf("app edit lost policy collection: %+v, %v", withRAM, err)
	}
	withRAM.Settings.WorkPolicies.Policies[0].Name = "caller-mutated"
	if records, _, err := state.EnvironmentWorkPolicies(ctx, store, app, "stage"); err != nil || len(records) != 2 || records[0].Policy.Name != "audit" {
		t.Fatalf("mutable returned collection: %+v, %v", records, err)
	}
	if _, err := state.DeleteEnvironmentWorkPolicy(ctx, store, app, "stage", "orders", nil); err != nil {
		t.Fatal(err)
	}
	deleted, err := state.DeleteEnvironmentWorkPolicy(ctx, store, app, "stage", "audit", nil)
	if err != nil || deleted.Settings.WorkPolicies.Policies == nil || len(deleted.Settings.WorkPolicies.Policies) != 0 || deleted.Settings.WorkPolicies.Revision != 6 {
		t.Fatalf("last deletion lost collection clock: %+v, %v", deleted, err)
	}
	recreated, recreatedSpec, err := state.UpsertEnvironmentWorkPolicy(ctx, store, app, "stage", nil, policy)
	if err != nil || recreated.Revision != 7 {
		t.Fatalf("recreated policy reused old revision: %+v, %v", recreated, err)
	}
	if _, _, err := state.UpsertEnvironmentWorkPolicy(ctx, store, app, "other", nil, otherPolicy); err != nil {
		t.Fatal(err)
	}
	if records, _, err := state.EnvironmentWorkPolicies(ctx, store, app, "other"); err != nil || len(records) != 1 || records[0].Policy.Name != "audit" || records[0].Revision != 1 {
		t.Fatalf("sibling stage changed: %+v, %v", records, err)
	}
	production, err := store.AppWorkPolicyByName(ctx, app.ID, policy.Name)
	if err != nil || production.Policy.Debounce != 2*time.Second || production.Revision != 2 {
		t.Fatalf("stage edit changed production: %+v, %v", production, err)
	}
	newDeployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "stage", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if configured, err := state.WorkPolicyForDeployment(ctx, store, app, newDeployment.ID, policy.Name); err != nil || configured.Revision != 7 {
		t.Fatalf("new deployment did not pin desired collection: %+v, %v", configured, err)
	}
	if err := store.MarkDeploymentLive(ctx, newDeployment.ID); err != nil {
		t.Fatalf("mark policy deployment live: %v", err)
	}
	release, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "stage", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: newDeployment.ID}})
	if err != nil {
		t.Fatalf("publish policy release: %v", err)
	}
	if _, _, err := store.ProjectEnvironmentWorkloadConfigHashes(ctx, account.ID, project.ID, "stage", release.ID); !errors.Is(err, state.ErrProjectEnvironmentWorkPolicyActivationUnavailable) {
		t.Fatalf("inactive policy collection qualified: %v", err)
	}
	if _, err := store.UpdateProjectEnvironmentProtection(ctx, account.ID, project.ID, "stage", true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.UpsertEnvironmentWorkPolicy(ctx, store, app, "stage", nil, policy); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("protected no-op accepted: %v", err)
	}
	if _, err := state.DeleteEnvironmentWorkPolicy(ctx, store, app, "stage", "orders", nil); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("protected deletion accepted: %v", err)
	}
	if current, err := store.ProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "stage", app.ID); err != nil || current.ID != recreatedSpec.ID {
		t.Fatalf("rejected edit moved desired head: %+v, %v", current, err)
	}
	for _, environment := range []string{"production", "default", "", "missing"} {
		if _, _, err := state.UpsertEnvironmentWorkPolicy(ctx, store, app, environment, nil, policy); err == nil {
			t.Errorf("invalid environment %q accepted", environment)
		}
	}
	foreign := app
	foreign.AccountID = uuid.NewString()
	if _, _, err := state.EnvironmentWorkPolicies(ctx, store, foreign, "stage"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign account read = %v", err)
	}
	if _, _, err := state.UpsertEnvironmentWorkPolicy(ctx, store, foreign, "other", nil, policy); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign account edit = %v", err)
	}
}

type forgedEnvironmentPolicyReader struct {
	state.ProjectEnvironmentWorkloadSpecReader
	spec state.ProjectEnvironmentWorkloadSpec
}

func (r forgedEnvironmentPolicyReader) ProjectEnvironmentWorkloadSpec(context.Context, string, string, string, string) (state.ProjectEnvironmentWorkloadSpec, error) {
	return r.spec, nil
}

func (r forgedEnvironmentPolicyReader) ProjectEnvironmentWorkloadSpecForDeployment(context.Context, string, string, string) (state.ProjectEnvironmentWorkloadSpec, error) {
	return r.spec, nil
}

func TestEnvironmentWorkPolicyReadersAuthenticateIdentityAndHash(t *testing.T) {
	app := state.App{ID: uuid.NewString(), AccountID: uuid.NewString(), ProjectID: uuid.NewString()}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	settings.WorkPolicies = &state.ProjectEnvironmentWorkPolicySettings{Revision: 1, Policies: []state.ProjectEnvironmentCloneWorkPolicy{{Name: "orders", Revision: 1, MaxRunningPerKey: 1, PendingUpdates: "all"}}}
	hash, err := state.WorkloadSettingsHash(settings)
	if err != nil {
		t.Fatal(err)
	}
	spec := state.ProjectEnvironmentWorkloadSpec{ID: uuid.NewString(), AccountID: app.AccountID, ProjectID: app.ProjectID, AppID: app.ID,
		EnvironmentID: uuid.NewString(), EnvironmentSlug: "stage", Revision: 1, Hash: hash, Settings: settings}
	for _, field := range []string{"AccountID", "ProjectID", "AppID", "Hash", "ID", "EnvironmentID"} {
		t.Run(field, func(t *testing.T) {
			bad := spec
			reflect.ValueOf(&bad).Elem().FieldByName(field).SetString("")
			reader := forgedEnvironmentPolicyReader{spec: bad}
			if _, _, err := state.EnvironmentWorkPolicies(context.Background(), reader, app, "stage"); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("head identity/hash bypass = %v", err)
			}
			if _, err := state.WorkPolicyForDeployment(context.Background(), reader, app, uuid.NewString(), "orders"); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("deployment identity/hash bypass = %v", err)
			}
		})
	}
}
