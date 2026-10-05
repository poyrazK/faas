// adr: 585
package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type environmentQueueTestStore interface {
	state.Store
	state.ProjectEnvironmentWorkPolicyStore
	state.DeploymentWorkloadSpecReader
	state.ProjectEnvironmentWorkloadQualificationStore
	state.ProjectReleaseSetStore
}

func TestMemEnvironmentQueueSettingsAreIsolatedAndPinned(t *testing.T) {
	testEnvironmentQueueSettings(t, state.NewMemStore())
}

func testEnvironmentQueueSettings(t *testing.T, store environmentQueueTestStore) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "queue-stage-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "queues"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "queue-worker", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker, RAMMB: 256, MaxConcurrency: 4, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600, Env: map[string]string{"MODE": "retained"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"stage", "other"} {
		if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: slug}); err != nil {
			t.Fatal(err)
		}
	}
	prod, err := store.CreateQueueBinding(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "orders", QueueName: "orders", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 4, RetryPolicyJSON: []byte(`{"max_attempts":3}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.EnvironmentQueueBindings(ctx, store, app, "stage"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing stage inherited production: %v", err)
	}
	empty, err := state.ReplaceEnvironmentQueueBindings(ctx, store, app, "stage", 0, nil)
	if err != nil || empty.Settings.QueueBindings == nil || empty.Settings.QueueBindings.Bindings == nil || empty.Settings.QueueBindings.Revision != 1 {
		t.Fatalf("complete empty queues: %+v, %v", empty, err)
	}
	d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "stage", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if bindings, err := state.QueueBindingsForDeployment(ctx, store, app, d.ID); err != nil || len(bindings) != 0 {
		t.Fatalf("empty pinned queues inherited production: %+v, %v", bindings, err)
	}
	binding := state.ProjectEnvironmentQueueDefinition{Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: false, MaxConcurrency: 2, RetryPolicyJSON: []byte(`{"max_seconds":20,"base_seconds":2,"max_attempts":4}`)}
	configured, err := state.UpsertEnvironmentQueueBinding(ctx, store, app, "stage", &empty.Revision, binding)
	if err != nil || configured.Settings.QueueBindings.Revision != 2 || configured.Settings.Manifest.Env["MODE"] != "retained" {
		t.Fatalf("stage queue configuration: %+v, %v", configured, err)
	}
	binding.RetryPolicyJSON = []byte(`{"max_attempts":4,"base_seconds":2,"max_seconds":20}`)
	noop, err := state.UpsertEnvironmentQueueBinding(ctx, store, app, "stage", &configured.Revision, binding)
	if err != nil || noop.ID != configured.ID || noop.Hash != configured.Hash {
		t.Fatalf("canonical no-op changed configuration: %+v, %v", noop, err)
	}
	if _, err := state.UpsertEnvironmentQueueBinding(ctx, store, app, "stage", &empty.Revision, binding); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale edit accepted: %v", err)
	}
	pinned, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "stage", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	binding.MaxConcurrency = 3
	changed, err := state.UpsertEnvironmentQueueBinding(ctx, store, app, "stage", &configured.Revision, binding)
	if err != nil {
		t.Fatal(err)
	}
	if bindings, err := state.QueueBindingsForDeployment(ctx, store, app, pinned.ID); err != nil || len(bindings) != 1 || bindings[0].MaxConcurrency != 2 || bindings[0].Enabled {
		t.Fatalf("desired edit changed pinned queues: %+v, %v", bindings, err)
	}
	if actual, err := store.QueueBindingByID(ctx, account.ID, app.ID, prod.ID); err != nil || actual.MaxConcurrency != 4 || !actual.Enabled {
		t.Fatalf("stage modified production binding: %+v, %v", actual, err)
	}
	if _, _, err := state.EnvironmentQueueBindings(ctx, store, app, "other"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stage altered sibling: %v", err)
	}
	rows, spec, err := state.EnvironmentQueueBindings(ctx, store, app, "stage")
	if err != nil {
		t.Fatal(err)
	}
	rows[0].Name = "caller-mutated"
	rows[0].RetryPolicyJSON[0] = '!'
	spec.Settings.QueueBindings.Bindings[0].Name = "also-mutated"
	if rows, _, err := state.EnvironmentQueueBindings(ctx, store, app, "stage"); err != nil || rows[0].Name != "orders" {
		t.Fatalf("returned values alias storage: %+v, %v", rows, err)
	}
	if _, err := store.UpdateProjectEnvironmentProtection(ctx, account.ID, project.ID, "stage", true); err != nil {
		t.Fatal(err)
	}
	if _, err := state.UpsertEnvironmentQueueBinding(ctx, store, app, "stage", &changed.Revision, binding); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("protected no-op accepted: %v", err)
	}
	if _, err := store.UpdateProjectEnvironmentProtection(ctx, account.ID, project.ID, "stage", false); err != nil {
		t.Fatal(err)
	}
	deleted, err := state.DeleteEnvironmentQueueBinding(ctx, store, app, "stage", "orders", &changed.Revision)
	if err != nil || deleted.Settings.QueueBindings.Bindings == nil || len(deleted.Settings.QueueBindings.Bindings) != 0 || deleted.Settings.QueueBindings.Revision != 4 {
		t.Fatalf("deleted collection did not retain clock/empty: %+v, %v", deleted, err)
	}
	if _, err := state.ReplaceEnvironmentQueueBindings(ctx, store, app, "production", 0, nil); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("stage API edited production: %v", err)
	}
	if err := store.MarkDeploymentLive(ctx, pinned.ID); err != nil {
		t.Fatal(err)
	}
	release, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "stage", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: pinned.ID}})
	if err != nil {
		t.Fatalf("worker stage release: %v", err)
	}
	if _, _, err := store.ProjectEnvironmentWorkloadConfigHashes(ctx, account.ID, project.ID, "stage", release.ID); !errors.Is(err, state.ErrProjectEnvironmentQueueActivationUnavailable) {
		t.Fatalf("queue definitions qualified without consumer proof: %v", err)
	}
}
