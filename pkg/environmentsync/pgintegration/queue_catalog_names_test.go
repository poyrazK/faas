// adr: 493 — reviewed queue adoption preserves existing catalog identities.
package pgintegration_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentGitOpsQueueCatalogNamesPreserveAdoptionAndAcceptedWork(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store := basic.(queueIntentStore)
		source, base := seedMode(t, store, "enforce")
		app, err := store.CreateApp(t.Context(), state.App{AccountID: source.AccountID, ProjectID: source.ProjectID,
			Slug: "shop-api", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
		if err != nil {
			t.Fatal(err)
		}
		names := []string{"q", "tag-orders", strings.Repeat("q", 63)}
		originals := make(map[string]state.QueueBindingConsumerResult)
		wanted := make(map[string]api.EnvironmentQueueBinding)
		for _, name := range names {
			original, err := store.CreateQueueBindingWithConsumer(t.Context(), state.QueueBinding{AccountID: source.AccountID,
				AppID: app.ID, DeploymentScope: "production", Name: name, QueueName: name, Mode: "push",
				WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
			if err != nil {
				t.Fatal(err)
			}
			originals[name] = original
			destination := name
			if name == "q" {
				destination = strings.Repeat("z", 63)
			}
			wanted[name] = api.EnvironmentQueueBinding{QueueName: destination, Mode: "push", WorkloadClass: "worker", MaxConcurrency: 2}
		}
		first := originals["q"]
		work, err := store.EnqueueInvocation(t.Context(), state.Invocation{AccountID: source.AccountID, AppID: app.ID,
			Source: state.InvocationQueue, QueueName: "q", QueueBindingID: first.Binding.ID,
			DeploymentScope: "production", DueAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := store.InsertTriggerRecord(t.Context(), first.Changes[0].TriggerID, work.ID, []byte(`{}`), []byte(`{}`), []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		definition := base.Definition
		definition.Workloads["api"] = api.EnvironmentWorkload{App: app.Slug, QueueBindings: wanted}
		desired, err := environmentsync.Compile(definition)
		if err != nil {
			t.Fatal(err)
		}
		source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("a", 40)))
		if err != nil {
			t.Fatal(err)
		}
		plan, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || !plan.CanApply() {
			t.Fatalf("catalog adoption preview: %+v %v", plan, err)
		}
		if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, plan.Hash); err != nil {
			t.Fatal(err)
		}
		for _, original := range originals {
			binding, err := store.QueueBindingByID(t.Context(), source.AccountID, app.ID, original.Binding.ID)
			if err != nil || binding.QueueName != original.Binding.QueueName || binding.MaxConcurrency != 1 {
				t.Fatalf("adoption changed catalog values: %+v %v", binding, err)
			}
		}
		queueIntentWorker(t, store)
		for name, original := range originals {
			binding, err := store.QueueBindingByID(t.Context(), source.AccountID, app.ID, original.Binding.ID)
			if err != nil || binding.Name != name || binding.QueueName != wanted[name].QueueName || binding.MaxConcurrency != 2 {
				t.Fatalf("catalog intent did not retain identity: %+v %v", binding, err)
			}
			consumer, err := store.TriggerByID(t.Context(), original.Changes[0].TriggerID)
			if err != nil || consumer.Slug != wanted[name].QueueName || consumer.BatchSizeMax != 2 {
				t.Fatalf("catalog consumer identity changed: %+v %v", consumer, err)
			}
		}
		retained, err := store.InvocationByID(t.Context(), work.ID)
		if err != nil || retained.QueueBindingID != first.Binding.ID || retained.QueueName != "q" || retained.State != state.InvocationPending {
			t.Fatalf("accepted work changed with the queue label: %+v %v", retained, err)
		}
		id, err := store.TriggerRecordIDByItemIdentifier(t.Context(), first.Changes[0].TriggerID, work.ID)
		if err != nil || id != receipt {
			t.Fatalf("accepted work lost its receipt: %s %v", id, err)
		}
	})
}
