package sched

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkerScopedBindingNamesAndCapsDoNotCrossEnvironments(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "worker-binding-scopes@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "worker-binding-scopes"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "worker-binding-scopes", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	bindings := map[string]state.QueueBinding{}
	for _, scope := range []string{"production", "staging"} {
		cap := 1
		if scope == "staging" {
			cap = 5
		}
		binding, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, DeploymentScope: scope, Name: "orders", QueueName: "orders", Mode: "push", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: cap})
		if err != nil {
			t.Fatal(err)
		}
		bindings[scope] = binding.Binding
		for range 30 {
			if _, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app.ID, Source: state.InvocationQueue, QueueName: "orders", DeploymentScope: scope, DueAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
		}
	}
	engine := &Engine{store: store}
	check := func(scope string, want int) {
		t.Helper()
		got, err := engine.workerQueueDemandForScope(ctx, app, scope, 10)
		if err != nil || got != want {
			t.Fatalf("scope %s demand=%d want=%d err=%v", scope, got, want, err)
		}
	}
	check("production", 1)
	check("staging", 3)
	disabled := false
	if _, err := store.UpdateQueueBindingWithConsumer(ctx, account.ID, app.ID, bindings["production"].ID, state.UpdateQueueBindingParams{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	check("production", 0)
	check("staging", 3)
	if err := store.DeleteProjectEnvironment(ctx, account.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	check("staging", 0)
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	check("staging", 0)
	replacement, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, DeploymentScope: "staging", Name: "orders", QueueName: "orders", Mode: "push", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if _, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app.ID, Source: state.InvocationQueue, QueueName: "orders", DeploymentScope: "staging", DueAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	check("staging", 2)
	if _, err := store.DeleteQueueBindingWithConsumer(ctx, account.ID, app.ID, bindings["staging"].ID); err != nil {
		t.Fatal(err)
	}
	check("production", 0)
	check("staging", 2)
	if _, err := store.DeleteQueueBindingWithConsumer(ctx, account.ID, app.ID, replacement.Binding.ID); err != nil {
		t.Fatal(err)
	}
	check("staging", 0)
}
