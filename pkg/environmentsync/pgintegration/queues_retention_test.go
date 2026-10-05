package pgintegration_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func approveQueueDefinition(t *testing.T, store queueIntentStore, source state.EnvironmentGitSource, definition api.EnvironmentDefinition, sha string) state.EnvironmentGitSource {
	t.Helper()
	current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
	if err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	current, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(current, desired, strings.Repeat(sha, 40)))
	if err != nil {
		t.Fatal(err)
	}
	return current
}

func approveQueueRetirement(t *testing.T, store queueIntentStore, source state.EnvironmentGitSource, desired environmentsync.DesiredState) state.EnvironmentGitSource {
	t.Helper()
	current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
	if err != nil {
		t.Fatal(err)
	}
	prune := true
	current, err = store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: current.Generation, Prune: &prune})
	if err != nil {
		t.Fatal(err)
	}
	// Compile detaches the map before removal, leaving the caller's approved
	// queue available for the separately reviewed recovery definition.
	copy, err := environmentsync.Compile(desired.Definition)
	if err != nil {
		t.Fatal(err)
	}
	d := copy.Definition
	d.QueuePruningPolicy = "retain"
	w := d.Workloads["api"]
	delete(w.QueueBindings, "orders")
	w.Variables["MODE"] = "retired"
	d.Workloads["api"] = w
	return approveQueueDefinition(t, store, current, d, "b")
}

func assertQueueHeld(t *testing.T, store queueIntentStore, source state.EnvironmentGitSource, app state.App, bindingID, consumerID string) {
	t.Helper()
	row, err := store.QueueBindingHistoryByID(t.Context(), source.AccountID, app.ID, bindingID)
	if err != nil || row.RetiredAt == nil || row.Enabled || row.EnvironmentID != source.EnvironmentID {
		t.Fatalf("original binding is not held: %+v %v", row, err)
	}
	consumer, err := store.TriggerByID(t.Context(), consumerID)
	if err != nil || consumer.Enabled || consumer.QueueBindingID.String() != uuid.MustParse(bindingID).String() {
		t.Fatalf("consumer hold lost identity: %+v %v", consumer, err)
	}
	claimed, err := store.ClaimTriggerRecords(t.Context(), consumerID, 10)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("held consumer claimed work: %+v %v", claimed, err)
	}
}

func TestEnvironmentGitOpsQueueReviewedRetirementAndOriginalRecovery(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, desired, app, original := queueIntentFixture(t, basic, "enforce")
		adoptQueueIntent(t, store, source)
		queueIntentWorker(t, store)
		consumerID := original.Changes[0].TriggerID
		work, err := store.EnqueueInvocation(t.Context(), state.Invocation{AccountID: source.AccountID, AppID: app.ID, Source: state.InvocationQueue, QueueName: "orders-v2", QueueBindingID: original.Binding.ID, DeploymentScope: "production", DueAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		receiptID, err := store.InsertTriggerRecord(t.Context(), consumerID, work.ID, []byte(`{}`), []byte(`{}`), []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		source = approveQueueRetirement(t, store, source, desired)
		queueIntentWorker(t, store)
		assertQueueHeld(t, store, source, app, original.Binding.ID, consumerID)
		assertVariable(t, store, source, app, "production", "MODE", "retired")
		if _, err := store.EnqueueInvocation(t.Context(), state.Invocation{AccountID: source.AccountID, AppID: app.ID, Source: state.InvocationQueue, QueueName: "orders-v2", QueueBindingID: original.Binding.ID, DeploymentScope: "production"}); err == nil {
			t.Fatal("retired binding admitted new work")
		}
		for _, row := range mustQueueHistory(t, store, source, app) {
			if row.ID != original.Binding.ID && (row.RetiredAt != nil || !row.Enabled) {
				t.Fatalf("neighbor was retired: %+v", row)
			}
		}
		runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 1)
		if err != nil || len(runs) != 1 || runs[0].Status != "converged" {
			t.Fatalf("retirement was not verified: %+v %v", runs, err)
		}
		// Restoring the name, and even pinning a neighboring UUID, cannot
		// release the retained delivery lane or commit unrelated intent.
		for i, id := range []string{"", uuid.NewString(), uuid.MustParse(original.Binding.ID).String()} {
			copy, err := environmentsync.Compile(desired.Definition)
			if err != nil {
				t.Fatal(err)
			}
			d := copy.Definition
			w := d.Workloads["api"]
			w.Variables["MODE"] = "recovered"
			if id != "" {
				w.QueueRecoveries = map[string]string{"orders": id}
			}
			d.Workloads["api"] = w
			source = approveQueueDefinition(t, store, source, d, string(rune('c'+i)))
			queueIntentWorker(t, store)
			assertQueueHeld(t, store, source, app, original.Binding.ID, consumerID)
			assertVariable(t, store, source, app, "production", "MODE", "retired")
		}
		preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || !preview.CanApply() {
			t.Fatalf("reviewed original recovery: %+v %v", preview, err)
		}
		found := false
		for _, change := range preview.Changes {
			if change.Path == "queue_bindings/orders" {
				found = change.Action == "adopt" && string(change.Before) == "null"
			}
		}
		if !found {
			t.Fatalf("retirement was not explicit in review: %+v", preview)
		}
		if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Hash); err != nil {
			t.Fatal(err)
		}
		assertQueueHeld(t, store, source, app, original.Binding.ID, consumerID)
		queueIntentWorker(t, store)
		row, err := store.QueueBindingByID(t.Context(), source.AccountID, app.ID, original.Binding.ID)
		if err != nil || !row.Enabled || row.RetiredAt != nil || row.QueueName != "orders-v2" || row.MaxConcurrency != 3 {
			t.Fatalf("original queue not recovered: %+v %v", row, err)
		}
		consumer, err := store.TriggerByID(t.Context(), consumerID)
		if err != nil || !consumer.Enabled || consumer.QueueBindingID.String() != uuid.MustParse(row.ID).String() {
			t.Fatalf("consumer namespace replaced: %+v %v", consumer, err)
		}
		if id, err := store.TriggerRecordIDByItemIdentifier(t.Context(), consumerID, work.ID); err != nil || id != receiptID {
			t.Fatalf("receipt namespace lost: %s %v", id, err)
		}
		if inv, err := store.InvocationByID(t.Context(), work.ID); err != nil || inv.QueueBindingID != row.ID || inv.DeploymentScope != "production" || inv.State != state.InvocationPending {
			t.Fatalf("accepted work was rewritten: %+v %v", inv, err)
		}
		claimed, err := store.ClaimTriggerRecords(t.Context(), consumerID, 10)
		if err != nil || len(claimed) != 1 || claimed[0].ID.String() != receiptID {
			t.Fatalf("original receipt did not resume: %+v %v", claimed, err)
		}
		assertVariable(t, store, source, app, "production", "MODE", "recovered")
	})
}

func mustQueueHistory(t *testing.T, store queueIntentStore, source state.EnvironmentGitSource, app state.App) []state.QueueBinding {
	t.Helper()
	rows, err := store.ListQueueBindingHistoryForApp(t.Context(), source.AccountID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestEnvironmentGitOpsQueueRetirementReportAndOverridePreserveHold(t *testing.T) {
	for _, mode := range []string{"report", "enforce"} {
		t.Run(mode, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				store, source, desired, app, original := queueIntentFixture(t, basic, mode)
				adoptQueueIntent(t, store, source)
				queueIntentWorker(t, store)
				if mode == "enforce" {
					if err := store.SetEnvironmentGitOpsOverride(t.Context(), source.AccountID, source.ID, state.EnvironmentGitOpsOverrideRequest{Resource: "workload/api", Path: "queue_bindings/orders", Reason: "finish incident deliveries", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
						t.Fatal(err)
					}
				}
				source = approveQueueRetirement(t, store, source, desired)
				queueIntentWorker(t, store)
				if row, err := store.QueueBindingByID(t.Context(), source.AccountID, app.ID, original.Binding.ID); err != nil || row.RetiredAt != nil || !row.Enabled {
					t.Fatalf("report/override retired a queue: %+v %v", row, err)
				}
				runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 1)
				if err != nil || len(runs) != 1 || runs[0].Status == "converged" {
					t.Fatalf("report/override falsely converged: %+v %v", runs, err)
				}
				if mode == "enforce" {
					if err := store.RemoveEnvironmentGitOpsOverride(t.Context(), source.AccountID, source.ID, "workload/api", "queue_bindings/orders"); err != nil {
						t.Fatal(err)
					}
					queueIntentWorker(t, store)
					assertQueueHeld(t, store, source, app, original.Binding.ID, original.Changes[0].TriggerID)
				}
			})
		})
	}
}

func TestEnvironmentGitOpsQueueRecoveryQuotaRollsBackAllIntent(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, desired, app, original := queueIntentFixture(t, basic, "enforce")
		adoptQueueIntent(t, store, source)
		queueIntentWorker(t, store)
		source = approveQueueRetirement(t, store, source, desired)
		queueIntentWorker(t, store)
		// The retained consumer spends no active trigger slot. Filling the
		// released capacity must cause recovery to remain wholly retired.
		limits := api.MustLimitsFor(api.PlanPro)
		for i := 2; i < limits.TriggerLimitPerApp; i++ {
			name := fmt.Sprintf("filler-%d", i)
			if _, err := store.CreateQueueBindingWithConsumer(t.Context(), state.QueueBinding{AccountID: source.AccountID, AppID: app.ID, DeploymentScope: "production", Name: name, QueueName: name, Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1}); err != nil {
				t.Fatal(err)
			}
		}
		copy, err := environmentsync.Compile(desired.Definition)
		if err != nil {
			t.Fatal(err)
		}
		d := copy.Definition
		w := d.Workloads["api"]
		w.QueueRecoveries = map[string]string{"orders": uuid.MustParse(original.Binding.ID).String()}
		w.Variables["MODE"] = "must-not-commit"
		d.Workloads["api"] = w
		source = approveQueueDefinition(t, store, source, d, "c")
		adoptQueueIntent(t, store, source)
		queueIntentWorker(t, store)
		assertQueueHeld(t, store, source, app, original.Binding.ID, original.Changes[0].TriggerID)
		assertVariable(t, store, source, app, "production", "MODE", "retired")
		runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 1)
		if err != nil || len(runs) != 1 || runs[0].Status != "partial" {
			t.Fatalf("quota recovery not rejected atomically: %+v %v", runs, err)
		}
		if _, err := store.UpdateQueueBindingWithConsumer(t.Context(), source.AccountID, app.ID, original.Binding.ID, state.UpdateQueueBindingParams{}); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("ordinary update released hold: %v", err)
		}
		// Names and UUIDs remain a durable reservation after the failed run.
		if _, err := store.CreateQueueBindingWithConsumer(t.Context(), original.Binding); err == nil {
			t.Fatal("ordinary recreation replaced a retained identity")
		}
	})
}
