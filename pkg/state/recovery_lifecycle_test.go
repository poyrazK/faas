package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreNodeListDrainableHonorsLiveInstances(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()

	idle, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:      "drainable-idle",
		Lifecycle: state.NodeLifecycleActive,
		Active:    true,
	})
	if err != nil {
		t.Fatalf("create idle node: %v", err)
	}
	busy, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:      "drainable-busy",
		Lifecycle: state.NodeLifecycleActive,
		Active:    true,
	})
	if err != nil {
		t.Fatalf("create busy node: %v", err)
	}

	for i, instanceState := range []state.State{
		state.StateWaking,
		state.StateColdBooting,
		state.StateRunning,
		state.StateSnapshotting,
		state.StateMigrating,
	} {
		if _, err := store.CreateInstance(ctx, "app-drain", "dep-drain", string(instanceState), 128, busy.ID, "wake-drain"); err != nil {
			t.Fatalf("create live instance %d (%s): %v", i, instanceState, err)
		}
	}
	if _, err := store.CreateInstance(ctx, "app-drain", "dep-parked", string(state.StateParked), 128, busy.ID, "wake-parked"); err != nil {
		t.Fatalf("create parked instance: %v", err)
	}

	nodes, err := store.NodeListDrainable(ctx)
	if err != nil {
		t.Fatalf("NodeListDrainable: %v", err)
	}
	foundIdle, foundBusy := false, false
	for _, node := range nodes {
		foundIdle = foundIdle || node.ID == idle.ID
		foundBusy = foundBusy || node.ID == busy.ID
	}
	if !foundIdle || foundBusy {
		t.Fatalf("drainable nodes = %+v, want idle node %q and no busy node %q", nodes, idle.ID, busy.ID)
	}
}

func TestMemStoreUpdateInstanceStateIfStampsParkedAt(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	instance, err := store.CreateInstance(ctx, "app-recovery", "dep-recovery", string(state.StateRunning), 128, "node-recovery", "wake-recovery")
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}

	if err := store.UpdateInstanceStateIf(ctx, instance.ID, string(state.StateRunning), string(state.StateParked)); err != nil {
		t.Fatalf("UpdateInstanceStateIf: %v", err)
	}
	parked, err := store.InstanceByID(ctx, instance.ID)
	if err != nil {
		t.Fatalf("reload parked instance: %v", err)
	}
	if parked.State != string(state.StateParked) || parked.ParkedAt.IsZero() {
		t.Fatalf("parked instance = %+v, want parked state and timestamp", parked)
	}
	migrated, err := store.MigrationInstanceByID(ctx, instance.ID)
	if err != nil || migrated.ID != instance.ID {
		t.Fatalf("MigrationInstanceByID = %+v, %v; want instance %q", migrated, err, instance.ID)
	}
	if _, err := store.MigrationInstanceByID(ctx, "missing-instance"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing MigrationInstanceByID error = %v, want ErrNotFound", err)
	}
	parkedAt := parked.ParkedAt

	if err := store.UpdateInstanceStateIf(ctx, instance.ID, string(state.StateRunning), string(state.StateFailed)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale UpdateInstanceStateIf error = %v, want ErrConflict", err)
	}
	unchanged, err := store.InstanceByID(ctx, instance.ID)
	if err != nil {
		t.Fatalf("reload unchanged instance: %v", err)
	}
	if unchanged.State != string(state.StateParked) || !unchanged.ParkedAt.Equal(parkedAt) {
		t.Fatalf("stale update changed instance: %+v", unchanged)
	}
}

func TestMemStoreListInstancesOnNodeIDUsesPhysicalPlacement(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "physical-placement@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID,
		Slug:      "physical-placement",
		NodeID:    "owner-node",
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, "dep-physical", string(state.StateRunning), 128, "physical-node", "wake-physical")
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}

	if got, err := store.ListInstancesByNodeID(ctx, "physical-node"); err != nil {
		t.Fatalf("ownership query: %v", err)
	} else if len(got) != 0 {
		t.Fatalf("ownership query returned %d instances, want 0", len(got))
	}
	got, err := store.ListInstancesOnNodeID(ctx, "physical-node")
	if err != nil {
		t.Fatalf("physical query: %v", err)
	}
	if len(got) != 1 || got[0].ID != instance.ID {
		t.Fatalf("physical query = %+v, want instance %q", got, instance.ID)
	}
}

func TestMemStoreLifecycleRecoverySurface(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()

	createNode := func(name string, lifecycle state.NodeLifecycle) state.ComputeNode {
		t.Helper()
		node, err := store.CreateComputeNode(ctx, state.ComputeNode{
			Name:      name,
			Lifecycle: lifecycle,
			Active:    lifecycle == state.NodeLifecycleActive || lifecycle == state.NodeLifecycleRecovering,
		})
		if err != nil {
			t.Fatalf("create %s node: %v", lifecycle, err)
		}
		return node
	}

	active := createNode("lifecycle-active", state.NodeLifecycleActive)
	draining := createNode("lifecycle-draining", state.NodeLifecycleDraining)
	unavailable := createNode("lifecycle-unavailable", state.NodeLifecycleUnavailable)
	recovering := createNode("lifecycle-recovering", state.NodeLifecycleRecovering)

	if got, err := store.NodeGet(ctx, active.ID); err != nil || got.ID != active.ID {
		t.Fatalf("NodeGet = %+v, %v", got, err)
	}
	if _, err := store.NodeGet(ctx, "missing-node"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("NodeGet missing = %v, want ErrNotFound", err)
	}
	if got, err := store.NodeGetByName(ctx, draining.Name); err != nil || got.ID != draining.ID {
		t.Fatalf("NodeGetByName = %+v, %v", got, err)
	}
	if _, err := store.NodeGetByName(ctx, "missing-node"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("NodeGetByName missing = %v, want ErrNotFound", err)
	}

	all, err := store.NodeList(ctx, "")
	if err != nil || len(all) < 5 {
		t.Fatalf("NodeList(all) = %d, %v; want seeded and test nodes", len(all), err)
	}
	filtered, err := store.NodeList(ctx, state.NodeLifecycleUnavailable)
	if err != nil || len(filtered) != 1 || filtered[0].ID != unavailable.ID {
		t.Fatalf("NodeList(unavailable) = %+v, %v", filtered, err)
	}
	recoverable, err := store.NodeListRecoverable(ctx)
	if err != nil {
		t.Fatalf("NodeListRecoverable: %v", err)
	}
	if len(recoverable) != 2 || recoverable[0].ID != recovering.ID || recoverable[1].ID != unavailable.ID {
		t.Fatalf("NodeListRecoverable = %+v, want recovering then unavailable", recoverable)
	}

	if err := store.NodeSetLifecycle(ctx, "missing-node", state.NodeLifecycleActive, state.NodeLifecycleDraining); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("NodeSetLifecycle missing = %v, want ErrNotFound", err)
	}
	if err := store.NodeSetLifecycle(ctx, draining.ID, state.NodeLifecycleActive, state.NodeLifecycleUnavailable); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("NodeSetLifecycle stale = %v, want ErrConflict", err)
	}
	if err := store.NodeSetLifecycle(ctx, active.ID, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
		t.Fatalf("NodeSetLifecycle active->draining: %v", err)
	}
	got, err := store.NodeGet(ctx, active.ID)
	if err != nil || got.Active || got.DrainInitiatedAt == nil {
		t.Fatalf("active->draining = %+v, %v; want inactive and timestamp", got, err)
	}
	if err := store.NodeSetLifecycle(ctx, active.ID, state.NodeLifecycleDraining, state.NodeLifecycleActive); err != nil {
		t.Fatalf("NodeSetLifecycle draining->active: %v", err)
	}
	got, err = store.NodeGet(ctx, active.ID)
	if err != nil || !got.Active || got.DrainCompletedAt == nil {
		t.Fatalf("draining->active = %+v, %v; want active and completion timestamp", got, err)
	}
	if err := store.NodeSetLifecycle(ctx, unavailable.ID, state.NodeLifecycleUnavailable, state.NodeLifecycleRecovering); err != nil {
		t.Fatalf("NodeSetLifecycle unavailable->recovering: %v", err)
	}
	got, err = store.NodeGet(ctx, unavailable.ID)
	if err != nil || !got.Active || got.RecoveryInitiatedAt == nil {
		t.Fatalf("unavailable->recovering = %+v, %v; want admitting and timestamp", got, err)
	}

	if err := store.NodeMarkDrainCompleted(ctx, "missing-node", time.Now()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("NodeMarkDrainCompleted missing = %v, want ErrNotFound", err)
	}
	if err := store.NodeMarkDrainCompleted(ctx, active.ID, time.Now()); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("NodeMarkDrainCompleted non-draining = %v, want ErrConflict", err)
	}
	if err := store.NodeSetLifecycle(ctx, active.ID, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
		t.Fatalf("prepare drain completion: %v", err)
	}
	completedAt := time.Date(2026, 9, 4, 22, 0, 0, 0, time.UTC)
	if err := store.NodeMarkDrainCompleted(ctx, active.ID, completedAt); err != nil {
		t.Fatalf("NodeMarkDrainCompleted: %v", err)
	}
	got, err = store.NodeGet(ctx, active.ID)
	if err != nil || got.Lifecycle != state.NodeLifecycleActive || got.DrainCompletedAt == nil || !got.DrainCompletedAt.Equal(completedAt) {
		t.Fatalf("completed drain = %+v, %v", got, err)
	}

	if err := store.NodeMarkRecovered(ctx, "missing-node"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("NodeMarkRecovered missing = %v, want ErrNotFound", err)
	}
	if err := store.NodeMarkRecovered(ctx, active.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("NodeMarkRecovered non-recovering = %v, want ErrConflict", err)
	}
	if err := store.NodeMarkRecovered(ctx, recovering.ID); err != nil {
		t.Fatalf("NodeMarkRecovered: %v", err)
	}
	got, err = store.NodeGet(ctx, recovering.ID)
	if err != nil || got.Lifecycle != state.NodeLifecycleActive || got.LastRecoveryOutcome == nil || *got.LastRecoveryOutcome != "succeeded" {
		t.Fatalf("recovered node = %+v, %v", got, err)
	}

	for _, instanceState := range []state.State{
		state.StateRunning,
		state.StateColdBooting,
		state.StateWaking,
		state.StateSnapshotting,
		state.StateMigrating,
		state.StateStopped,
	} {
		if _, err := store.CreateInstance(ctx, "recovery-app", "recovery-deployment", string(instanceState), 128, "recovery-node", "wake-"+string(instanceState)); err != nil {
			t.Fatalf("create recovery instance (%s): %v", instanceState, err)
		}
	}
	if _, err := store.CreateInstance(ctx, "recovery-app", "recovery-deployment", string(state.StateRunning), 128, "other-node", "wake-other"); err != nil {
		t.Fatalf("create other-node instance: %v", err)
	}
	instances, err := store.InstanceListByNodeForRecovery(ctx, "recovery-node")
	if err != nil {
		t.Fatalf("InstanceListByNodeForRecovery: %v", err)
	}
	if len(instances) != 5 {
		t.Fatalf("recovery instances = %d, want 5", len(instances))
	}
	for _, instance := range instances {
		if instance.State == string(state.StateStopped) {
			t.Fatalf("stopped instance included in recovery set: %+v", instance)
		}
	}

	if err := store.DeploymentRecordSnapshotMiss(ctx, "deployment", time.Now()); err != nil {
		t.Fatalf("DeploymentRecordSnapshotMiss: %v", err)
	}
	if err := store.DeploymentClearSnapshotBackoff(ctx, "deployment"); err != nil {
		t.Fatalf("DeploymentClearSnapshotBackoff: %v", err)
	}
	if deployment, active, err := store.DeploymentSnapshotBackoffActive(ctx, "deployment"); err != nil || active || deployment.ID != "" {
		t.Fatalf("DeploymentSnapshotBackoffActive = %+v, %t, %v; want empty, false, nil", deployment, active, err)
	}
}

func TestMemStoreRecentInvocationObservations(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "observations@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: "observed-app", AccountID: account.ID, Slug: "observed-app"})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	otherApp, err := store.CreateApp(ctx, state.App{ID: "other-observed-app", AccountID: account.ID, Slug: "other-observed-app"})
	if err != nil {
		t.Fatalf("create other app: %v", err)
	}
	singleApp, err := store.CreateApp(ctx, state.App{ID: "single-latency-app", AccountID: account.ID, Slug: "single-latency-app"})
	if err != nil {
		t.Fatalf("create single-latency app: %v", err)
	}

	base := time.Date(2026, 9, 4, 22, 0, 0, 0, time.UTC)
	add := func(id, appID string, invocationState state.InvocationState, createdAt time.Time, completedAt *time.Time) {
		t.Helper()
		if _, err := store.EnqueueInvocation(ctx, state.Invocation{
			ID: id, AppID: appID, AccountID: account.ID, State: invocationState,
			CreatedAt: createdAt, DueAt: createdAt, CompletedAt: completedAt,
		}); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
	}
	completedAt := func(createdAt time.Time, latency time.Duration) *time.Time {
		at := createdAt.Add(latency)
		return &at
	}

	add("observed-success-1", app.ID, state.InvocationCompleted, base.Add(time.Minute), completedAt(base.Add(time.Minute), 100*time.Millisecond))
	add("observed-failed", app.ID, state.InvocationFailed, base.Add(2*time.Minute), nil)
	add("observed-dead-letter", app.ID, state.InvocationDeadLetter, base.Add(3*time.Minute), nil)
	add("observed-pending", app.ID, state.InvocationPending, base.Add(4*time.Minute), nil)
	add("observed-old", app.ID, state.InvocationCompleted, base.Add(-time.Minute), completedAt(base.Add(-time.Minute), 50*time.Millisecond))
	add("other-app", otherApp.ID, state.InvocationCompleted, base.Add(5*time.Minute), completedAt(base.Add(5*time.Minute), time.Second))

	recent, err := store.RecentInvocations(ctx, app.ID, base, 2)
	if err != nil {
		t.Fatalf("RecentInvocations(limit=2): %v", err)
	}
	if len(recent) != 2 || recent[0].ID != "observed-pending" || recent[1].ID != "observed-dead-letter" {
		t.Fatalf("RecentInvocations(limit=2) = %+v, want newest two target rows", recent)
	}
	allRecent, err := store.RecentInvocations(ctx, app.ID, base, 0)
	if err != nil || len(allRecent) != 4 {
		t.Fatalf("RecentInvocations(unbounded) = %d, %v; want 4", len(allRecent), err)
	}

	rate, err := store.RecentErrorRate(ctx, app.ID, base)
	if err != nil {
		t.Fatalf("RecentErrorRate: %v", err)
	}
	if rate != 0.5 {
		t.Fatalf("RecentErrorRate = %v, want 0.5", rate)
	}
	if rate, err := store.RecentErrorRate(ctx, app.ID, base.Add(10*time.Minute)); err != nil || rate != 0 {
		t.Fatalf("RecentErrorRate(empty) = %v, %v; want 0", rate, err)
	}

	add("latency-success-2", app.ID, state.InvocationCompleted, base.Add(6*time.Minute), completedAt(base.Add(6*time.Minute), 200*time.Millisecond))
	add("latency-success-3", app.ID, state.InvocationCompleted, base.Add(7*time.Minute), completedAt(base.Add(7*time.Minute), 300*time.Millisecond))
	add("latency-pending", app.ID, state.InvocationPending, base.Add(8*time.Minute), completedAt(base.Add(8*time.Minute), time.Second))
	add("latency-no-completion", app.ID, state.InvocationCompleted, base.Add(9*time.Minute), nil)
	negativeCompleted := base.Add(10 * time.Minute)
	add("latency-negative", app.ID, state.InvocationCompleted, base.Add(11*time.Minute), &negativeCompleted)
	oldCompleted := base.Add(-time.Second)
	add("latency-old", app.ID, state.InvocationCompleted, base.Add(-2*time.Minute), &oldCompleted)

	p95, err := store.RecentP95LatencyMs(ctx, app.ID, base)
	if err != nil {
		t.Fatalf("RecentP95LatencyMs: %v", err)
	}
	if p95 != 290 {
		t.Fatalf("RecentP95LatencyMs = %v, want 290", p95)
	}
	if p95, err := store.RecentP95LatencyMs(ctx, app.ID, base.Add(20*time.Minute)); err != nil || p95 != 0 {
		t.Fatalf("RecentP95LatencyMs(empty) = %v, %v; want 0", p95, err)
	}
	oneCreated := base.Add(30 * time.Minute)
	add("single-latency", singleApp.ID, state.InvocationCompleted, oneCreated, completedAt(oneCreated, 425*time.Millisecond))
	if p95, err := store.RecentP95LatencyMs(ctx, singleApp.ID, base); err != nil || p95 != 425 {
		t.Fatalf("RecentP95LatencyMs(single) = %v, %v; want 425", p95, err)
	}
}
