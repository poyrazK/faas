// adr: 531
package state_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type environmentQueueConsumerTestStore interface {
	environmentQueueTestStore
	state.ProjectEnvironmentQueueConsumerStore
}

type queueConsumerFixture struct {
	account state.Account
	project state.Project
	app     state.App
	spec    state.ProjectEnvironmentWorkloadSpec
	dep     state.Deployment
	prod    state.QueueBinding
}

func seedQueueConsumers(t *testing.T, store environmentQueueConsumerTestStore) queueConsumerFixture {
	t.Helper()
	ctx := t.Context()
	var f queueConsumerFixture
	var err error
	f.account, err = store.CreateAccount(ctx, "queue-consumer-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f.project, err = store.CreateProject(ctx, state.Project{AccountID: f.account.ID, Slug: "consumer"})
	if err != nil {
		t.Fatal(err)
	}
	f.app, err = store.CreateApp(ctx, state.App{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "worker",
		Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker, RAMMB: 256, MaxConcurrency: 4,
		Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"stage", "other"} {
		if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: scope}); err != nil {
			t.Fatal(err)
		}
	}
	f.prod, err = store.CreateQueueBinding(ctx, state.QueueBinding{AccountID: f.account.ID, AppID: f.app.ID,
		Name: "orders", QueueName: "orders", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 4})
	if err != nil {
		t.Fatal(err)
	}
	f.spec, err = state.ReplaceEnvironmentQueueBindings(ctx, store, f.app, "stage", 0, []state.ProjectEnvironmentQueueDefinition{
		{Name: "retry", QueueName: "retry", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: false, MaxConcurrency: 1},
		{Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 2, RetryPolicyJSON: json.RawMessage(`{"max_attempts":4}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.dep, err = store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "stage", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, f.dep.ID); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestMemEnvironmentQueueConsumersAreCompleteOwnedAndPinned(t *testing.T) {
	testEnvironmentQueueConsumers(t, state.NewMemStore())
}

func testEnvironmentQueueConsumers(t *testing.T, store environmentQueueConsumerTestStore) queueConsumerFixture {
	t.Helper()
	ctx := t.Context()
	f := seedQueueConsumers(t, store)
	if _, err := store.ProjectEnvironmentQueueConsumersForDeployment(ctx, f.account.ID, f.project.ID, f.dep.ID); !errors.Is(err, state.ErrProjectEnvironmentQueuePreparationUnavailable) {
		t.Fatalf("unprepared set read: %v", err)
	}
	first, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID)
	if err != nil || first.State != "prepared" || first.WorkloadSpecID != f.spec.ID || first.SettingsHash != f.spec.Hash ||
		first.BindingCount != 2 || len(first.Consumers) != 2 || first.Consumers[0].Name != "orders" || first.Consumers[1].Enabled ||
		first.Consumers[0].ID == f.prod.ID || first.Consumers[0].ID == first.Consumers[1].ID {
		t.Fatalf("complete owned queue projection: %+v, %v", first, err)
	}
	read, err := store.ProjectEnvironmentQueueConsumersForDeployment(ctx, f.account.ID, f.project.ID, f.dep.ID)
	if err != nil || !reflect.DeepEqual(first, read) {
		t.Fatalf("persisted projection differs: %+v, %v", read, err)
	}
	// Concurrent retries return the same set and consumer identities.
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			got, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID)
			if err != nil || !reflect.DeepEqual(first, got) {
				t.Errorf("concurrent preparation changed identity: %+v, %v", got, err)
			}
		})
	}
	wg.Wait()
	read.Consumers[0].Name = "mutated"
	read.Consumers[0].RetryPolicyJSON[0] = '!'
	if got, err := store.ProjectEnvironmentQueueConsumersForDeployment(ctx, f.account.ID, f.project.ID, f.dep.ID); err != nil || !reflect.DeepEqual(first, got) {
		t.Fatalf("returned values alias storage: %+v, %v", got, err)
	}
	changedBindings := []state.ProjectEnvironmentQueueDefinition{first.Consumers[0].ProjectEnvironmentQueueDefinition}
	changedBindings[0].MaxConcurrency = 3
	changed, err := state.ReplaceEnvironmentQueueBindings(ctx, store, f.app, "stage", f.spec.Revision, changedBindings)
	if err != nil || changed.Hash == f.spec.Hash {
		t.Fatalf("desired edit: %+v, %v", changed, err)
	}
	if got, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID); err != nil || !reflect.DeepEqual(first, got) {
		t.Fatalf("desired head rewrote pinned consumers: %+v, %v", got, err)
	}
	if _, err := state.ReplaceEnvironmentQueueBindings(ctx, store, f.app, "other", 0, f.spec.Settings.QueueBindings.Bindings); err != nil {
		t.Fatal(err)
	}
	otherDep, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "other", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, otherDep.ID); err != nil {
		t.Fatal(err)
	}
	other, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, otherDep.ID)
	if err != nil || other.EnvironmentID == first.EnvironmentID || other.ID == first.ID || len(other.Consumers) != 2 || other.Consumers[0].ID == first.Consumers[0].ID {
		t.Fatalf("sibling inherited physical consumer: %+v, %v", other, err)
	}
	if got, err := store.QueueBindingByID(ctx, f.account.ID, f.app.ID, f.prod.ID); err != nil || got.MaxConcurrency != 4 || !got.Enabled {
		t.Fatalf("preparation modified production: %+v, %v", got, err)
	}
	bindings, err := store.ListQueueBindingsForApp(ctx, f.account.ID, f.app.ID)
	if err != nil || len(bindings) != 1 || bindings[0].ID != f.prod.ID {
		t.Fatalf("stage consumers appeared in production: %+v, %v", bindings, err)
	}
	// A prepared projection cannot be used as dispatch qualification.
	release, err := store.PublishProjectReleaseSet(ctx, f.account.ID, f.project.ID, "stage", 1800, []state.ProjectReleaseMember{{AppID: f.app.ID, DeploymentID: f.dep.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ProjectEnvironmentWorkloadConfigHashes(ctx, f.account.ID, f.project.ID, "stage", release.ID); !errors.Is(err, state.ErrProjectEnvironmentQueueActivationUnavailable) {
		t.Fatalf("preparation was treated as activation: %v", err)
	}
	request := state.Invocation{AppID: f.app.ID, AccountID: f.account.ID, Source: state.InvocationQueue, QueueName: "orders", DueAt: time.Now()}
	request.Headers, _ = json.Marshal(map[string]string{api.RevisionHeader: f.dep.ID})
	if _, err := store.EnqueueInvocation(ctx, request); !errors.Is(err, state.ErrInvocationEnvironmentWorkIsolation) {
		t.Fatalf("prepared set enabled shared queue admission: %v", err)
	}
	env, err := store.ProjectEnvironmentBySlug(ctx, f.account.ID, f.project.ID, "stage")
	if err != nil {
		t.Fatal(err)
	}
	ram := 512
	updated, err := state.UpdateEnvironmentWorkloadSettings(ctx, store, f.app, env, &changed.Revision, state.UpdateAppParams{RAMMB: &ram})
	if err != nil || updated.Settings.QueueBindings == nil || !reflect.DeepEqual(updated.Settings.QueueBindings, changed.Settings.QueueBindings) {
		t.Fatalf("ordinary edit lost the persisted queue collection: %+v, %v", updated, err)
	}
	next, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "stage", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	nextSet, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, next.ID)
	if err != nil || nextSet.SettingsHash != updated.Hash || nextSet.WorkloadSpecID != updated.ID || len(nextSet.Consumers) != 1 ||
		nextSet.Consumers[0].MaxConcurrency != 3 || nextSet.Consumers[0].ID == first.Consumers[0].ID {
		t.Fatalf("new deployment reused old consumer configuration/identity: %+v, %v", nextSet, err)
	}
	return f
}

func TestMemEnvironmentQueueConsumerCleanup(t *testing.T) {
	testEnvironmentQueueConsumerCleanup(t, state.NewMemStore())
}

func testEnvironmentQueueConsumerCleanup(t *testing.T, store environmentQueueConsumerTestStore) queueConsumerFixture {
	t.Helper()
	ctx := t.Context()
	f := seedQueueConsumers(t, store)
	if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "stage"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("deleted live consumer environment: %v", err)
	}
	if err := store.UpdateDeploymentStatus(ctx, f.dep.ID, state.DeploySuperseded, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "stage"); err != nil {
		t.Fatalf("prepared consumer blocked idle deletion: %v", err)
	}
	if _, err := store.ProjectEnvironmentQueueConsumersForDeployment(ctx, f.account.ID, f.project.ID, f.dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted environment retained accessible consumer: %v", err)
	}
	if _, err := store.QueueBindingByID(ctx, f.account.ID, f.app.ID, f.prod.ID); err != nil {
		t.Fatalf("consumer deletion removed production binding: %v", err)
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, f.account.ID, f.project.ID, "other"); err != nil {
		t.Fatalf("consumer deletion removed sibling: %v", err)
	}
	return f
}

func TestMemEnvironmentQueueConsumersEmptyAndUnavailable(t *testing.T) {
	testEnvironmentQueueConsumersEmptyAndUnavailable(t, state.NewMemStore())
}

func testEnvironmentQueueConsumersEmptyAndUnavailable(t *testing.T, store environmentQueueConsumerTestStore) {
	t.Helper()
	ctx := t.Context()
	f := seedQueueConsumers(t, store)
	legacy, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "other", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, legacy.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, legacy.ID); !errors.Is(err, state.ErrProjectEnvironmentQueueCollectionUnavailable) {
		t.Fatalf("legacy stage inherited production: %v", err)
	}
	if _, err := state.ReplaceEnvironmentQueueBindings(ctx, store, f.app, "other", 0, nil); err != nil {
		t.Fatal(err)
	}
	emptyDep, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "other", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, emptyDep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("nonlive preparation: %v", err)
	}
	if err := store.MarkDeploymentLive(ctx, emptyDep.ID); err != nil {
		t.Fatal(err)
	}
	empty, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, emptyDep.ID)
	if err != nil || empty.BindingCount != 0 || empty.Consumers == nil || len(empty.Consumers) != 0 || len(empty.BookHash) != 64 {
		t.Fatalf("explicit empty preparation: %+v, %v", empty, err)
	}
	production, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "production", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, production.ID); err != nil {
		t.Fatal(err)
	}
	for _, fault := range []struct{ name, account, project, dep string }{
		{"production", f.account.ID, f.project.ID, production.ID},
		{"foreign_account", uuid.NewString(), f.project.ID, f.dep.ID},
		{"foreign_project", f.account.ID, uuid.NewString(), f.dep.ID},
		{"unknown_deployment", f.account.ID, f.project.ID, uuid.NewString()},
	} {
		t.Run(fault.name, func(t *testing.T) {
			if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, fault.account, fault.project, fault.dep); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("invalid owner prepared: %v", err)
			}
			if _, err := store.ProjectEnvironmentQueueConsumersForDeployment(ctx, fault.account, fault.project, fault.dep); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("invalid owner read: %v", err)
			}
		})
	}
	for _, fault := range []struct{ account, project, dep string }{
		{"malformed", f.project.ID, f.dep.ID},
		{f.account.ID, "malformed", f.dep.ID},
		{f.account.ID, f.project.ID, uuid.Nil.String()},
	} {
		if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, fault.account, fault.project, fault.dep); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid identifiers prepared: %v", err)
		}
		if _, err := store.ProjectEnvironmentQueueConsumersForDeployment(ctx, fault.account, fault.project, fault.dep); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid identifiers read: %v", err)
		}
	}
}
