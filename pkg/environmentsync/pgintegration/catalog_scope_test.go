// adr: 532 — accepted catalog names work through intent, queue and runtime receipts.
package pgintegration_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentGitOpsCatalogScopesPreserveIntentAndAcceptedWork(t *testing.T) {
	for _, scope := range []string{"a", "1", "ab", "12", "1a", "a-1", strings.Repeat("a", 33)} {
		t.Run(scope, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				testCatalogScope(t, basic, scope)
			})
		})
	}
}

func testCatalogScope(t *testing.T, basic gitOpsTestStore, scope string) {
	t.Helper()
	store := basic.(queueIntentStore)
	ctx := t.Context()
	source, base := seedModePolicyScope(t, basic, "enforce", "manual", scope)
	app, err := store.CreateApp(ctx, state.App{AccountID: source.AccountID, ProjectID: source.ProjectID,
		Slug: "shop-api", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker,
		RAMMB: 512, MaxConcurrency: 1, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := basic.(state.ProjectDeployBranchesStore).ReplaceProjectDeployBranches(ctx, source.AccountID, source.ProjectID, map[string]string{"catalog": scope}); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{scope, state.DefaultEnvScope} {
		if err := store.UpsertAppEnvInScope(ctx, source.AccountID, app.ID, target, "MODE", "original-"+target); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAppSecretInScope(ctx, source.AccountID, app.ID, target, "DATABASE", []byte("sealed-"+target)); err != nil {
			t.Fatal(err)
		}
	}
	refs := basic.(state.AppEnvironmentSecretReferenceStore)
	if err := refs.PutAppEnvironmentSecretReference(ctx, source.AccountID, app.ID, scope, "DATABASE_URL", "secret:DATABASE"); err != nil {
		t.Fatal(err)
	}
	if err := refs.DeleteAppEnvironmentSecretReference(ctx, source.AccountID, app.ID, scope, "REMOVED"); err != nil {
		t.Fatal(err)
	}
	original, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: source.AccountID, AppID: app.ID,
		DeploymentScope: scope, Name: "orders", QueueName: "orders", Mode: "push", Enabled: true,
		WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	work, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: source.AccountID, AppID: app.ID,
		Source: state.InvocationQueue, QueueName: "orders", QueueBindingID: original.Binding.ID, DeploymentScope: scope, DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := store.InsertTriggerRecord(ctx, original.Changes[0].TriggerID, work.ID, []byte(`{}`), []byte(`{}`), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	definition := base.Definition
	definition.Workloads["api"] = api.EnvironmentWorkload{App: app.Slug, Variables: map[string]string{"MODE": "approved"},
		SecretRefs: map[string]string{"DATABASE_URL": "secret:DATABASE"}, QueueBindings: map[string]api.EnvironmentQueueBinding{
			"orders": {QueueName: "orders-v2", Mode: "push", WorkloadClass: "worker", MaxConcurrency: 2},
		}}
	desired, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(ctx, approval(source, desired, strings.Repeat("a", 40)))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.PreviewEnvironmentGitOpsAdoption(ctx, source.AccountID, source.ID)
	if err != nil || !plan.CanApply() {
		t.Fatalf("catalog adoption: %+v %v", plan, err)
	}
	if err := store.AdoptEnvironmentGitOps(ctx, source.AccountID, source.ID, plan.Hash); err != nil {
		t.Fatal(err)
	}
	variables, err := store.ListAppEnvInScope(ctx, source.AccountID, app.ID, scope)
	if err != nil || len(variables) != 1 || variables[0].Value != "original-"+scope {
		t.Fatalf("adoption changed values: %+v %v", variables, err)
	}
	if err := store.UpsertAppEnvInScope(ctx, source.AccountID, app.ID, scope, "MODE", "console"); !errors.Is(err, state.ErrEnvironmentGitManaged) {
		t.Fatalf("short scope bypassed ownership: %v", err)
	}
	queueIntentWorker(t, store)
	variables, err = store.ListAppEnvInScope(ctx, source.AccountID, app.ID, scope)
	if err != nil || len(variables) != 1 || variables[0].Value != "approved" {
		t.Fatalf("approved intent was not applied: %+v %v", variables, err)
	}
	neighbor, err := store.ListAppEnvInScope(ctx, source.AccountID, app.ID, state.DefaultEnvScope)
	if err != nil || len(neighbor) != 1 || neighbor[0].Value != "original-default" {
		t.Fatalf("default scope changed: %+v %v", neighbor, err)
	}
	current, err := store.QueueBindingByID(ctx, source.AccountID, app.ID, original.Binding.ID)
	if err != nil || current.QueueName != "orders-v2" || current.EnvironmentID != source.EnvironmentID || current.DeploymentScope != scope {
		t.Fatalf("queue identity changed: %+v %v", current, err)
	}
	accepted, err := store.InvocationByID(ctx, work.ID)
	if err != nil || accepted.QueueBindingID != original.Binding.ID || accepted.DeploymentScope != scope || accepted.QueueName != "orders" {
		t.Fatalf("accepted work changed: %+v %v", accepted, err)
	}
	if id, err := store.TriggerRecordIDByItemIdentifier(ctx, original.Changes[0].TriggerID, work.ID); err != nil || id != receipt {
		t.Fatalf("accepted receipt changed: %q %v", id, err)
	}
	secrets, err := store.ListAppSecretsInScope(ctx, source.AccountID, app.ID, scope)
	if err != nil || len(secrets) != 1 || !bytes.Equal(secrets[0].Ciphertext, []byte("sealed-"+scope)) {
		t.Fatalf("sealed source changed: %+v %v", secrets, err)
	}
	inputs := state.RuntimeConfigInputs{Scope: scope, Boundary: time.Now().UTC(), Variables: map[string]string{"MODE": "approved"},
		SecretRefs: map[string]string{"DATABASE_URL": "secret:DATABASE"}, SecretVersions: map[string]int64{scope + "/DATABASE": secrets[0].DeliveryVersion}}
	runtime := basic.(state.RuntimeConfigReceiptStore)
	if fresh, err := runtime.RuntimeConfigInputsFresh(ctx, app.ID, inputs); err != nil || !fresh {
		t.Fatalf("catalog runtime inputs rejected: %v %v", fresh, err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope,
		Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("b", 64), Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	instance := runtimeInstance(t, store, app, deployment, state.StateWaking)
	if err := store.UpdateInstanceState(ctx, instance.ID, string(state.StateRunning)); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RecordInstanceRuntimeConfigReceipt(ctx, instance.ID, instance.WakeID, inputs); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.PublishSnapshotIfRuntimeFresh(ctx, state.Snapshot{
		DeploymentID: deployment.ID, FCVersion: "1.13.0", StorageKey: state.SnapMemKey(deployment.ID)}, instance.ID, instance.StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	captured, exists, err := runtime.SnapshotRuntimeConfigReceipt(ctx, snapshot.ID)
	if err != nil || !exists || captured.Scope != scope || captured.SecretRefs["DATABASE_URL"] != "secret:DATABASE" {
		t.Fatalf("snapshot lost scoped receipt: %+v %v %v", captured, exists, err)
	}
}
