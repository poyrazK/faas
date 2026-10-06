package pgintegration_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

type queueIntentStore interface {
	intentTestStore
	state.QueueBindingConsumerStore
	state.QueueBindingHistoryStore
}

func queueIntentFixture(t *testing.T, basic gitOpsTestStore, mode string) (queueIntentStore, state.EnvironmentGitSource, environmentsync.DesiredState, state.App, state.QueueBindingConsumerResult) {
	t.Helper()
	store := basic.(queueIntentStore)
	source, base := seedMode(t, store, mode)
	app, err := store.CreateApp(t.Context(), state.App{AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: "shop-api", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	var production state.QueueBindingConsumerResult
	for _, scope := range []string{"production", "staging", ""} {
		result, err := store.CreateQueueBindingWithConsumer(t.Context(), state.QueueBinding{AccountID: source.AccountID, AppID: app.ID, DeploymentScope: scope, Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		if scope == "production" {
			production = result
		}
	}
	if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, "production", "MODE", "console"); err != nil {
		t.Fatal(err)
	}
	d := base.Definition
	enabled := true
	d.Workloads["api"] = api.EnvironmentWorkload{App: app.Slug, Variables: map[string]string{"MODE": "approved"}, QueueBindings: map[string]api.EnvironmentQueueBinding{
		"orders": {QueueName: "orders-v2", Mode: "push", WorkloadClass: "worker", Enabled: &enabled, MaxConcurrency: 3, RetryPolicy: &api.RetryPolicyDTO{MaxAttempts: 4}},
	}}
	desired, err := environmentsync.Compile(d)
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("a", 40)))
	if err != nil {
		t.Fatal(err)
	}
	return store, source, desired, app, production
}

func adoptQueueIntent(t *testing.T, store queueIntentStore, source state.EnvironmentGitSource) {
	t.Helper()
	plan, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
	if err != nil || !plan.CanApply() {
		t.Fatalf("adoption preview: %+v %v", plan, err)
	}
	found := false
	for _, change := range plan.Changes {
		if change.Path == "queue_bindings/orders" {
			found = change.Action == "adopt"
		}
	}
	if !found {
		t.Fatalf("scoped queue adoption missing: %+v", plan)
	}
	if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, plan.Hash); err != nil {
		t.Fatal(err)
	}
}

func queueIntentWorker(t *testing.T, store queueIntentStore) {
	t.Helper()
	worker := environmentgitops.Worker{Store: store, Backend: environmentgitops.IntentBackend{Store: store}, LeaseDuration: time.Minute, CheckInterval: time.Minute, RetryInterval: time.Second}
	if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
		t.Fatalf("queue worker: %v %v", worked, err)
	}
}

func TestEnvironmentGitOpsScopedQueueAdoptionAndReconciliation(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, _, app, original := queueIntentFixture(t, basic, "enforce")
		work, err := store.EnqueueInvocation(t.Context(), state.Invocation{AccountID: source.AccountID, AppID: app.ID, Source: state.InvocationQueue, QueueName: "orders", QueueBindingID: original.Binding.ID, DeploymentScope: "production", DueAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := store.InsertTriggerRecord(t.Context(), original.Changes[0].TriggerID, work.ID, []byte(`{}`), []byte(`{}`), []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		adoptQueueIntent(t, store, source)
		binding, err := store.QueueBindingByID(t.Context(), source.AccountID, app.ID, original.Binding.ID)
		if err != nil || binding.MaxConcurrency != 1 {
			t.Fatalf("adoption changed intent: %+v %v", binding, err)
		}
		cap := 9
		if _, err := store.UpdateQueueBindingWithConsumer(t.Context(), source.AccountID, app.ID, binding.ID, state.UpdateQueueBindingParams{MaxConcurrency: &cap}); !errors.Is(err, state.ErrEnvironmentGitManaged) {
			t.Fatalf("owned mutation: %v", err)
		}
		if _, err := store.DeleteQueueBindingWithConsumer(t.Context(), source.AccountID, app.ID, binding.ID); !errors.Is(err, state.ErrEnvironmentGitManaged) {
			t.Fatalf("owned retirement: %v", err)
		}
		queueIntentWorker(t, store)
		binding, err = store.QueueBindingByID(t.Context(), source.AccountID, app.ID, binding.ID)
		var retry api.RetryPolicyDTO
		decodeErr := json.Unmarshal(binding.RetryPolicyJSON, &retry)
		if err != nil || decodeErr != nil || binding.MaxConcurrency != 3 || binding.QueueName != "orders-v2" || retry.MaxAttempts != 4 {
			t.Fatalf("approved queue not applied: %+v %v", binding, err)
		}
		consumer, err := store.TriggerByID(t.Context(), original.Changes[0].TriggerID)
		if err != nil || consumer.BatchSizeMax != 3 || consumer.MaxAttempts != 4 || consumer.Slug != "orders-v2" {
			t.Fatalf("consumer projection: %+v %v", consumer, err)
		}
		if id, err := store.TriggerRecordIDByItemIdentifier(t.Context(), consumer.ID.String(), work.ID); err != nil || id != receipt {
			t.Fatalf("lost receipt: %s %v", id, err)
		}
		if inv, err := store.InvocationByID(t.Context(), work.ID); err != nil || inv.QueueBindingID != binding.ID || inv.State != state.InvocationPending {
			t.Fatalf("work reinterpreted: %+v %v", inv, err)
		}
		rows, err := store.ListQueueBindingsForApp(t.Context(), source.AccountID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.ID != binding.ID && row.MaxConcurrency != 1 {
				t.Fatalf("neighbor changed: %+v", row)
			}
		}
		current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
		if err != nil || current.AppliedRevisionID != current.ApprovedRevisionID {
			t.Fatalf("queue not verified: %+v %v", current, err)
		}
	})
}

func TestEnvironmentGitOpsQueueReportDriftAndStaleAdoption(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, _, app, original := queueIntentFixture(t, basic, "report")
		preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		cap := 2
		if _, err := store.UpdateQueueBindingWithConsumer(t.Context(), source.AccountID, app.ID, original.Binding.ID, state.UpdateQueueBindingParams{MaxConcurrency: &cap}); err != nil {
			t.Fatal(err)
		}
		if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Hash); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("stale queue review: %v", err)
		}
		adoptQueueIntent(t, store, source)
		cap = 7
		if _, err := store.UpdateQueueBindingWithConsumer(t.Context(), source.AccountID, app.ID, original.Binding.ID, state.UpdateQueueBindingParams{MaxConcurrency: &cap}); err != nil {
			t.Fatal(err)
		}
		queueIntentWorker(t, store)
		binding, err := store.QueueBindingByID(t.Context(), source.AccountID, app.ID, original.Binding.ID)
		if err != nil || binding.MaxConcurrency != 7 {
			t.Fatalf("report changed queue: %+v %v", binding, err)
		}
		runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 1)
		if err != nil || len(runs) != 1 || runs[0].Status != "drifted" {
			t.Fatalf("report result: %+v %v", runs, err)
		}
	})
}

func TestEnvironmentGitOpsQueueCreationRetainsIdentityAndBlocksPruning(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, desired, app, _ := queueIntentFixture(t, basic, "enforce")
		adoptQueueIntent(t, store, source)
		queueIntentWorker(t, store)
		current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
		if err != nil {
			t.Fatal(err)
		}
		definition := desired.Definition
		w := definition.Workloads["api"]
		w.QueueBindings["billing"] = api.EnvironmentQueueBinding{QueueName: "billing", Mode: "push", WorkloadClass: "worker", MaxConcurrency: 2}
		definition.Workloads["api"] = w
		desired, err = environmentsync.Compile(definition)
		if err != nil {
			t.Fatal(err)
		}
		source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(current, desired, strings.Repeat("b", 40)))
		if err != nil {
			t.Fatal(err)
		}
		queueIntentWorker(t, store)
		rows, err := store.ListQueueBindingsForApp(t.Context(), source.AccountID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range rows {
			if row.Name == "billing" {
				found = row.EnvironmentID == source.EnvironmentID && row.MaxConcurrency == 2
			}
		}
		if !found {
			t.Fatalf("new scoped queue missing: %+v", rows)
		}
		current, err = store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
		if err != nil {
			t.Fatal(err)
		}
		prune := true
		source, err = store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: current.Generation, Prune: &prune})
		if err != nil {
			t.Fatal(err)
		}
		definition = desired.Definition
		w = definition.Workloads["api"]
		delete(w.QueueBindings, "orders")
		w.Variables["MODE"] = "must-not-commit"
		definition.Workloads["api"] = w
		desired, err = environmentsync.Compile(definition)
		if err != nil {
			t.Fatal(err)
		}
		source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("c", 40)))
		if err != nil {
			t.Fatal(err)
		}
		queueIntentWorker(t, store)
		assertVariable(t, store, source, app, "production", "MODE", "approved")
		runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 1)
		if err != nil || len(runs) != 1 || runs[0].Status != "blocked" || !strings.Contains(string(runs[0].Plan), "retained work") {
			t.Fatalf("unsafe prune: %+v %v", runs, err)
		}
		rows, err = store.ListQueueBindingsForApp(t.Context(), source.AccountID, app.ID)
		if err != nil || len(rows) != 4 {
			t.Fatalf("pruning removed identity: %+v %v", rows, err)
		}
	})
}

func TestEnvironmentGitOpsQueueOverrideRestoresApprovedIntent(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, _, app, original := queueIntentFixture(t, basic, "enforce")
		adoptQueueIntent(t, store, source)
		queueIntentWorker(t, store)
		override := state.EnvironmentGitOpsOverrideRequest{Resource: "workload/api", Path: "queue_bindings/orders", Reason: "temporary incident capacity", ExpiresAt: time.Now().Add(time.Hour)}
		if err := store.SetEnvironmentGitOpsOverride(t.Context(), source.AccountID, source.ID, override); err != nil {
			t.Fatal(err)
		}
		cap := 8
		if _, err := store.UpdateQueueBindingWithConsumer(t.Context(), source.AccountID, app.ID, original.Binding.ID, state.UpdateQueueBindingParams{MaxConcurrency: &cap}); err != nil {
			t.Fatal(err)
		}
		queueIntentWorker(t, store)
		current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
		if err != nil {
			t.Fatal(err)
		}
		runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 1)
		if err != nil || len(runs) != 1 || runs[0].Status == "converged" {
			t.Fatalf("override falsely converged: %+v %v", runs, err)
		}
		if current.AppliedRevisionID != current.ApprovedRevisionID {
			t.Fatal("prior approved application evidence was lost")
		}
		if err := store.RemoveEnvironmentGitOpsOverride(t.Context(), source.AccountID, source.ID, override.Resource, override.Path); err != nil {
			t.Fatal(err)
		}
		queueIntentWorker(t, store)
		binding, err := store.QueueBindingByID(t.Context(), source.AccountID, app.ID, original.Binding.ID)
		if err != nil || binding.MaxConcurrency != 3 {
			t.Fatalf("override not restored: %+v %v", binding, err)
		}
	})
}

func TestEnvironmentGitOpsQueueQuotaFailureRollsBackWholeIntent(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, desired, app, _ := queueIntentFixture(t, basic, "enforce")
		adoptQueueIntent(t, store, source)
		queueIntentWorker(t, store)
		limits := api.MustLimitsFor(api.PlanPro)
		for i := 3; i < limits.TriggerLimitPerApp-1; i++ {
			name := fmt.Sprintf("filler-%d", i)
			if _, err := store.CreateQueueBindingWithConsumer(t.Context(), state.QueueBinding{AccountID: source.AccountID, AppID: app.ID, DeploymentScope: "production", Name: name, QueueName: name, Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1}); err != nil {
				t.Fatal(err)
			}
		}
		current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
		if err != nil {
			t.Fatal(err)
		}
		definition := desired.Definition
		w := definition.Workloads["api"]
		for _, name := range []string{"billing", "shipping"} {
			w.QueueBindings[name] = api.EnvironmentQueueBinding{QueueName: name, Mode: "push", WorkloadClass: "worker"}
		}
		w.Variables["MODE"] = "must-not-commit"
		definition.Workloads["api"] = w
		desired, err = environmentsync.Compile(definition)
		if err != nil {
			t.Fatal(err)
		}
		source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(current, desired, strings.Repeat("b", 40)))
		if err != nil {
			t.Fatal(err)
		}
		queueIntentWorker(t, store)
		assertVariable(t, store, source, app, "production", "MODE", "approved")
		rows, err := store.ListQueueBindingsForApp(t.Context(), source.AccountID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.Name == "billing" || row.Name == "shipping" {
				t.Fatalf("partial binding survived failed transaction: %+v", row)
			}
		}
		triggers, err := store.ListTriggersForApp(t.Context(), app.ID)
		if err != nil || len(triggers) != limits.TriggerLimitPerApp-1 {
			t.Fatalf("partial consumer survived: %d %v", len(triggers), err)
		}
		runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 1)
		if err != nil || len(runs) != 1 || runs[0].Status != "partial" {
			t.Fatalf("quota result: %+v %v", runs, err)
		}
	})
}

func TestEnvironmentGitOpsRetiredQueueCannotBeImplicitlyRecovered(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, _, app, original := queueIntentFixture(t, basic, "report")
		adoptQueueIntent(t, store, source)
		if _, err := store.DeleteQueueBindingWithConsumer(t.Context(), source.AccountID, app.ID, original.Binding.ID); err != nil {
			t.Fatal(err)
		}
		current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: current.Generation, Mode: "enforce"}); err != nil {
			t.Fatal(err)
		}
		queueIntentWorker(t, store)
		binding, err := store.QueueBindingHistoryByID(t.Context(), source.AccountID, app.ID, original.Binding.ID)
		if err != nil || binding.RetiredAt == nil || binding.Enabled {
			t.Fatalf("retirement released: %+v %v", binding, err)
		}
		runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 1)
		if err != nil || len(runs) != 1 || runs[0].Status != "blocked" || !strings.Contains(string(runs[0].Plan), "reviewed recovery") {
			t.Fatalf("retirement result: %+v %v", runs, err)
		}
	})
}
