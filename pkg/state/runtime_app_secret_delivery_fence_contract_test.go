// adr: 585
package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemRuntimeAppSecretBootDeliveryFence(t *testing.T) {
	testRuntimeAppSecretBootDeliveryFence(t, state.NewMemStore())
}

func testRuntimeAppSecretBootDeliveryFence(t *testing.T, store runtimeAppEnvTestStore) {
	ctx := t.Context()
	f := seedRuntimeAppEnv(t, store)
	dep := f.deployments["stage"]
	nodeID := runtimeSecretNodeForTest(t, store)
	instance, err := store.CreateInstance(ctx, f.app.ID, dep.ID, string(state.StateColdBooting), 256, nodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"default", "production", "stage", "other"} {
		if err := store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, scope, "TOKEN", []byte(scope+"-sealed")); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "SIDECAR_TOKEN", []byte("sidecar-sealed")); err != nil {
		t.Fatal(err)
	}
	result := state.AppSecretDeliveryResult{AccountID: f.account.ID, AppID: f.app.ID, InstanceID: instance.ID, WakeID: instance.WakeID,
		Fence: runtimeSecretFenceForTest(t, store, f, dep), Status: state.SecretDeliveryDelivered,
		Candidates: []state.AppSecretDeliveryCandidate{{Scope: "stage", Key: "TOKEN", Version: 1}, {Scope: "stage", Key: "SIDECAR_TOKEN", Version: 1}}}
	assertRejected := func(report state.AppSecretDeliveryResult, expected error) {
		t.Helper()
		if count, err := store.RecordAppSecretDelivery(ctx, report); count != 0 || !errors.Is(err, expected) {
			t.Fatalf("stale boot accepted: count=%d err=%v want %v", count, err, expected)
		}
	}
	wrong := result
	wrong.Fence = state.RuntimeAppSecretFence{}
	assertRejected(wrong, state.ErrInvalidArgument)
	wrong = result
	wrong.WakeID = uuid.NewString()
	assertRejected(wrong, state.ErrConflict)
	wrong = result
	wrong.Candidates = []state.AppSecretDeliveryCandidate{result.Candidates[0], {Scope: "stage", Key: "SIDECAR_TOKEN", Version: 2}}
	assertRejected(wrong, state.ErrConflict)
	row, err := store.GetAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN")
	if err != nil || row.DeliveredVersion != 0 || row.LastDeliveryAttemptAt != nil {
		t.Fatalf("partial delivery escaped rejected batch: %+v %v", row, err)
	}
	failed := result
	failed.Status, failed.ErrorCode = state.SecretDeliveryFailed, "runtime_start_failed"
	if count, err := store.RecordAppSecretDelivery(ctx, failed); count != 2 || err != nil {
		t.Fatalf("owned boot failure: count=%d err=%v", count, err)
	}
	for repeat := 0; repeat < 2; repeat++ {
		if count, err := store.RecordAppSecretDelivery(ctx, result); count != 2 || err != nil {
			t.Fatalf("owned boot success %d: count=%d err=%v", repeat, count, err)
		}
	}
	if count, err := store.RecordAppSecretDelivery(ctx, failed); count != 0 || err != nil {
		t.Fatalf("late failure downgraded delivered secrets: count=%d err=%v", count, err)
	}
	if err := store.DeleteAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN", []byte("stage-sealed")); err != nil {
		t.Fatal(err)
	}
	assertRejected(result, state.ErrConflict)
	assertRejected(failed, state.ErrConflict)
	row, err = store.GetAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN")
	if err != nil || row.DeliveryVersion != 1 || row.DeliveredVersion != 0 || row.LastDeliveryAttemptAt != nil {
		t.Fatalf("recreated envelope adopted stale boot: %+v %v", row, err)
	}
	result.Fence = runtimeSecretFenceForTest(t, store, f, dep)
	if err := store.SetDeploymentSecretReloadSignal(ctx, dep.ID, "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	assertRejected(result, state.ErrConflict)
	result.Fence = runtimeSecretFenceForTest(t, store, f, dep)
	if count, err := store.RecordAppSecretDelivery(ctx, result); count != 2 || err != nil {
		t.Fatalf("current boot after grant change: count=%d err=%v", count, err)
	}
	if err := store.ResealAppSecretWithKidAndValueHashInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN", "replacement-key", "1111111111111111", []byte("stage-resealed")); err != nil {
		t.Fatal(err)
	}
	// Resealing preserves delivery_version, but the selected envelope differs.
	assertRejected(result, state.ErrConflict)
	result.Fence = runtimeSecretFenceForTest(t, store, f, dep)
	if count, err := store.RecordAppSecretDelivery(ctx, result); count != 2 || err != nil {
		t.Fatalf("current resealed boot: count=%d err=%v", count, err)
	}
	if err := store.UpdateInstanceState(ctx, instance.ID, string(state.StateStopped)); err != nil {
		t.Fatal(err)
	}
	assertRejected(result, state.ErrConflict)
	failed = result
	failed.Status, failed.ErrorCode = state.SecretDeliveryFailed, "runtime_start_failed"
	assertRejected(failed, state.ErrConflict)
	if err := store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN", []byte("stage-rotated")); err != nil {
		t.Fatal(err)
	}
	failedInstance, err := store.CreateInstance(ctx, f.app.ID, dep.ID, string(state.StateFailed), 256, nodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	failed.Fence = runtimeSecretFenceForTest(t, store, f, dep)
	failed.InstanceID, failed.WakeID = failedInstance.ID, failedInstance.WakeID
	failed.Candidates = []state.AppSecretDeliveryCandidate{{Scope: "stage", Key: "TOKEN", Version: 2}}
	if count, err := store.RecordAppSecretDelivery(ctx, failed); count != 1 || err != nil {
		t.Fatalf("current failed instance lost its failure summary: count=%d err=%v", count, err)
	}
	wrong = failed
	wrong.Status, wrong.ErrorCode = state.SecretDeliveryDelivered, ""
	assertRejected(wrong, state.ErrConflict)
	if err := store.MarkDeploymentSuperseded(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "stage"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN", []byte("replacement-sealed")); err != nil {
		t.Fatal(err)
	}
	assertRejected(result, state.ErrConflict)
	assertRejected(failed, state.ErrConflict)
	failed.Candidates = nil
	assertRejected(failed, state.ErrConflict)
	for _, scope := range []string{"default", "production", "stage", "other"} {
		row, err := store.GetAppSecretInScope(ctx, f.account.ID, f.app.ID, scope, "TOKEN")
		if err != nil || row.DeliveredVersion != 0 || row.LastDeliveryAttemptAt != nil {
			t.Fatalf("boot crossed stage lifetime/scope into %s: %+v %v", scope, row, err)
		}
	}
}
