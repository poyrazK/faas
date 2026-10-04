// adr: 581
package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemRuntimeAppSecretObservationFence(t *testing.T) {
	testRuntimeAppSecretObservationFence(t, state.NewMemStore())
}

func runtimeSecretNodeForTest(t *testing.T, store state.Store) string {
	t.Helper()
	node, err := store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "runtime-secret-fence", TargetURL: "unix:///tmp/runtime-secret-fence.sock", VPCPUs: 8,
		MemMB: 4096, MaxConcurrency: 16, AdmissionCeilingMB: 4096, VCPUBudget: 8, Lifecycle: state.NodeLifecycleActive, LastHeartbeatAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return node.ID
}

func runtimeSecretFenceForTest(t *testing.T, store runtimeAppEnvTestStore, f runtimeAppEnvFixture, dep state.Deployment) state.RuntimeAppSecretFence {
	t.Helper()
	snapshot, err := store.RuntimeAppValuesForDeployment(t.Context(), f.account.ID, f.app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := state.NewRuntimeAppSecretFence(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return fence
}

func testRuntimeAppSecretObservationFence(t *testing.T, store runtimeAppEnvTestStore) {
	ctx := t.Context()
	f := seedRuntimeAppEnv(t, store)
	dep := f.deployments["stage"]
	nodeID := runtimeSecretNodeForTest(t, store)
	instance, err := store.CreateInstance(ctx, f.app.ID, dep.ID, string(state.StateRunning), 256, nodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"default", "production", "stage", "other"} {
		if err := store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, scope, "TOKEN", []byte(scope+"-sealed")); err != nil {
			t.Fatal(err)
		}
	}
	result := state.AppSecretRuntimeReloadResult{AccountID: f.account.ID, AppID: f.app.ID, InstanceID: instance.ID,
		Fence: runtimeSecretFenceForTest(t, store, f, dep), Revision: strings.Repeat("a", 64),
		Projection: state.SecretReloadProjectionUpdated, Signal: state.SecretReloadSignalSent,
		Candidates: []state.AppSecretDeliveryCandidate{{Scope: "stage", Key: "TOKEN", Version: 1}}}
	ack := state.AppSecretRuntimeReloadAckResult{AccountID: f.account.ID, AppID: f.app.ID, InstanceID: instance.ID,
		Fence: result.Fence, Revision: result.Revision, Status: state.SecretApplicationReloadAckApplied, Candidates: result.Candidates}
	assertRejected := func(report state.AppSecretRuntimeReloadResult, acknowledgement state.AppSecretRuntimeReloadAckResult) {
		t.Helper()
		if count, err := store.RecordAppSecretRuntimeReload(ctx, report); count != 0 || !errors.Is(err, state.ErrConflict) {
			t.Fatalf("unowned/stale projection accepted: count=%d err=%v", count, err)
		}
		if count, err := store.RecordAppSecretRuntimeReloadAck(ctx, acknowledgement); count != 0 || !errors.Is(err, state.ErrConflict) {
			t.Fatalf("unowned/stale acknowledgement accepted: count=%d err=%v", count, err)
		}
	}
	for _, repeat := range []int{0, 1} {
		if count, err := store.RecordAppSecretRuntimeReload(ctx, result); count != 1 || err != nil {
			t.Fatalf("owned projection %d: count=%d err=%v", repeat, count, err)
		}
		if count, err := store.RecordAppSecretRuntimeReloadAck(ctx, ack); count != 1 || err != nil {
			t.Fatalf("owned acknowledgement %d: count=%d err=%v", repeat, count, err)
		}
	}
	legacy, legacyAck := result, ack
	legacy.Fence, legacyAck.Fence = state.RuntimeAppSecretFence{}, state.RuntimeAppSecretFence{}
	assertRejected(legacy, legacyAck)
	legacy.Candidates, legacyAck.Candidates = nil, nil
	assertRejected(legacy, legacyAck)
	otherInstance, err := store.CreateInstance(ctx, f.app.ID, f.deployments["other"].ID, string(state.StateRunning), 256, nodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	wrong, wrongAck := result, ack
	wrong.InstanceID, wrongAck.InstanceID = otherInstance.ID, otherInstance.ID
	assertRejected(wrong, wrongAck)
	if err := store.SetDeploymentSecretReloadSignal(ctx, dep.ID, "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	assertRejected(result, ack)
	result.Fence = runtimeSecretFenceForTest(t, store, f, dep)
	ack.Fence = result.Fence
	if err := store.DeleteAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN"); err != nil {
		t.Fatal(err)
	}
	// Copying back the same envelope can reuse delivery version one, but must
	// not reuse the row's creation lifetime or its old observation fence.
	if err := store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN", []byte("stage-sealed")); err != nil {
		t.Fatal(err)
	}
	assertRejected(result, ack)
	current, err := store.GetAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN")
	if err != nil || current.DeliveryVersion != 1 || current.LastRuntimeReloadVersion != 0 {
		t.Fatalf("stale reports modified recreated envelope: version=%+v err=%v", current, err)
	}
	result.Fence = runtimeSecretFenceForTest(t, store, f, dep)
	ack.Fence = result.Fence
	if count, err := store.RecordAppSecretRuntimeReload(ctx, result); count != 1 || err != nil {
		t.Fatalf("new envelope report: count=%d err=%v", count, err)
	}
	stopped, err := store.CreateInstance(ctx, f.app.ID, dep.ID, string(state.StateStopped), 256, nodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	wrong, wrongAck = result, ack
	wrong.InstanceID, wrongAck.InstanceID = stopped.ID, stopped.ID
	assertRejected(wrong, wrongAck)
	if err := store.MarkDeploymentSuperseded(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "stage"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN", []byte("replacement-stage-sealed")); err != nil {
		t.Fatal(err)
	}
	assertRejected(result, ack)
	result.Candidates, ack.Candidates = nil, nil
	assertRejected(result, ack)
	// The original deployment's retained owner also denies the legacy path
	// after the environment/spec/pin cascades have removed the live binding.
	assertRejected(legacy, legacyAck)
	for _, scope := range []string{"default", "production", "stage", "other"} {
		row, err := store.GetAppSecretInScope(ctx, f.account.ID, f.app.ID, scope, "TOKEN")
		if err != nil || row.LastRuntimeReloadVersion != 0 {
			t.Fatalf("stale stage report changed %s observation: %+v %v", scope, row, err)
		}
	}
}

func TestMemRuntimeAppSecretEmptyRevocationFence(t *testing.T) {
	testRuntimeAppSecretEmptyRevocationFence(t, state.NewMemStore())
}

func testRuntimeAppSecretEmptyRevocationFence(t *testing.T, store runtimeAppEnvTestStore) {
	ctx := t.Context()
	f := seedRuntimeAppEnv(t, store)
	dep := f.deployments["stage"]
	instance, err := store.CreateInstance(ctx, f.app.ID, dep.ID, string(state.StateRunning), 256, runtimeSecretNodeForTest(t, store), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentSecretReloadSignal(ctx, dep.ID, "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppSecretInScope(ctx, f.account.ID, f.app.ID, "stage", "TOKEN", []byte("original-sealed")); err != nil {
		t.Fatal(err)
	}
	before := runtimeSecretFenceForTest(t, store, f, dep)
	revocation, err := store.DeleteAppSecretInScopeWithRevocation(ctx, f.account.ID, f.app.ID, "stage", "TOKEN")
	if err != nil || len(revocation.Targets) != 1 {
		t.Fatalf("revocation: %+v %v", revocation, err)
	}
	ack := state.AppSecretRuntimeReloadAckResult{AccountID: f.account.ID, AppID: f.app.ID, InstanceID: instance.ID,
		Fence: before, Revision: strings.Repeat("b", 64), Status: state.SecretApplicationReloadAckApplied}
	if count, err := store.RecordAppSecretRuntimeReloadAck(ctx, ack); count != 0 || !errors.Is(err, state.ErrConflict) {
		t.Fatalf("empty ack reused pre-revocation fence: %d %v", count, err)
	}
	ack.Fence = runtimeSecretFenceForTest(t, store, f, dep)
	if count, err := store.RecordAppSecretRuntimeReloadAck(ctx, ack); count != 1 || err != nil {
		t.Fatalf("current empty acknowledgement: %d %v", count, err)
	}
	status, err := store.GetAppSecretRevocation(ctx, f.account.ID, f.app.ID, revocation.ID)
	if err != nil || status.Targets[0].Status != "applied" {
		t.Fatalf("owned empty acknowledgement did not apply revocation: %+v %v", status, err)
	}
}
