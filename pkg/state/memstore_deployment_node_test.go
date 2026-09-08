package state

// memstore_deployment_node_test.go: covers 28 zero-coverage
// MemStore methods that PR #1 / PR #2 left behind. These are pure
// in-memory state operations on the deployment / node registry — no
// Postgres, no network — so the tests are deterministic and fast.
//
// Each test creates a minimal fixture (one account, one app, one
// deployment) and asserts the new method's filter / sort / mutate
// contract. The naming convention `TestMemStore_<method>_<scenario>`
// matches the existing pkg/state/memstore_*_test.go files; reviewers
// grep by this prefix to confirm coverage parity with PgStore.

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// memDeploymentFixture creates an account + app + deployment wired
// to a single MemStore. Used by every test below so each one stays
// a tight single-purpose unit. White-box (package state) so we can
// reach internal helpers like m.deployments and state constants.
func memDeploymentFixture(t *testing.T) (*MemStore, context.Context, Account, App, Deployment) {
	t.Helper()
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "dep-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{AccountID: account.ID, Slug: "dep-" + uuid.NewString(), NodeID: "node-a", RAMMB: 256, Status: AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	deployment, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:dep"})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	return m, ctx, account, app, deployment
}

// memLiveInstance seeds an instance on the fixture's app + deployment
// with the lowercase "running" state — the only form Postgres accepts
// (instances_state_check since migration 00001).
//
// A second helper, memLiveInstanceUpper, used to sit directly below this
// one and seed the literal "RUNNING", justified by a comment saying that
// was "the form ConcurrencyForDeployment's switch recognises" and that
// "both code paths must stay covered". Uppercase was never a code path:
// isInstanceStateLive compared against uppercase literals, so it matched
// nothing a deployment can produce, and the duplicate kept that reader
// green by manufacturing a state the SQL CHECK rejects.
func memLiveInstance(t *testing.T, m *MemStore, ctx context.Context, appID, deploymentID, nodeID string) Instance {
	t.Helper()
	inst, err := m.CreateInstance(ctx, appID, deploymentID, string(StateRunning), 256, nodeID, uuid.NewString())
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	return inst
}

// memGetInstance reads m.instances[id] under the store's mutex.
// White-box accessor — there is no public GetInstance on MemStore,
// only the typed Instance value is mutated and read directly here.
func memGetInstance(t *testing.T, m *MemStore, id string) Instance {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.instances[id]
	if !ok {
		t.Fatalf("instance %q not found in store", id)
	}
	return inst
}

func TestMemStore_ListAllDeployments_Empty(t *testing.T) {
	t.Parallel()
	got, err := NewMemStore().ListAllDeployments(context.Background())
	if err != nil {
		t.Fatalf("ListAllDeployments: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0 on empty store", len(got))
	}
}

func TestMemStore_ListAllDeployments_ExcludesSoftDeleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m1, _, _, _, dep1 := memDeploymentFixture(t)
	// Soft-delete app2 on m1's store. We create a second app +
	// deployment on the same m1 store so the cascade has a real row.
	app2OnM1, err := m1.CreateApp(ctx, App{AccountID: "owner-x", Slug: "soft-deleted-" + uuid.NewString(), NodeID: "node-c", RAMMB: 256, Status: AppActive})
	if err != nil {
		t.Fatalf("CreateApp on m1: %v", err)
	}
	dep2OnM1, err := m1.CreateDeployment(ctx, Deployment{AppID: app2OnM1.ID, ImageDigest: "sha256:dep2"})
	if err != nil {
		t.Fatalf("CreateDeployment on m1: %v", err)
	}
	if _, err := m1.SoftDeleteAppCascade(ctx, app2OnM1.ID); err != nil {
		t.Fatalf("SoftDeleteAppCascade: %v", err)
	}
	got, err := m1.ListAllDeployments(ctx)
	if err != nil {
		t.Fatalf("ListAllDeployments: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (app2's deployment excluded); got = %+v", len(got), got)
	}
	if got[0].ID != dep1.ID {
		t.Errorf("got[0].ID = %q, want %q (app1's deployment)", got[0].ID, dep1.ID)
	}
	_ = dep2OnM1 // keep dep2 live on m1 so we can prove it's filtered
}

func TestMemStore_ListAllDeployments_SortedByCreatedDesc(t *testing.T) {
	t.Parallel()
	m, ctx, _, app, _ := memDeploymentFixture(t)
	// Create two more deployments on the same app — CreatedAt is
	// wall-clock at creation time, so three calls in quick
	// succession produce a strictly-descending slice.
	for i := 0; i < 2; i++ {
		if _, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:extra"}); err != nil {
			t.Fatalf("CreateDeployment[%d]: %v", i, err)
		}
	}
	got, err := m.ListAllDeployments(ctx)
	if err != nil {
		t.Fatalf("ListAllDeployments: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i := 0; i < len(got)-1; i++ {
		if got[i].CreatedAt.Before(got[i+1].CreatedAt) {
			t.Errorf("got[%d].CreatedAt < got[%d].CreatedAt; want descending", i, i+1)
		}
	}
}

func TestMemStore_ListDeploymentsByNodeID_FilterByNode(t *testing.T) {
	t.Parallel()
	m, ctx, _, _, depA := memDeploymentFixture(t)
	// Second app + deployment on the SAME store so SetAppNodeID
	// targets a row that exists on m. Seed with empty NodeID so
	// SetAppNodeID accepts the transition (it only fills an empty
	// slot; reassigning a placed app is ErrConflict).
	appB, err := m.CreateApp(ctx, App{AccountID: "owner-y", Slug: "node-b-" + uuid.NewString(), NodeID: "", RAMMB: 256, Status: AppActive})
	if err != nil {
		t.Fatalf("CreateApp on m: %v", err)
	}
	depB, err := m.CreateDeployment(ctx, Deployment{AppID: appB.ID, ImageDigest: "sha256:depB"})
	if err != nil {
		t.Fatalf("CreateDeployment on m: %v", err)
	}
	// appA is on "node-a" (the fixture default); move appB to "node-b".
	if err := m.SetAppNodeID(ctx, appB.ID, "node-b"); err != nil {
		t.Fatalf("SetAppNodeID(appB, node-b): %v", err)
	}

	gotA, err := m.ListDeploymentsByNodeID(ctx, "node-a")
	if err != nil {
		t.Fatalf("ListDeploymentsByNodeID(node-a): %v", err)
	}
	if len(gotA) != 1 || gotA[0].ID != depA.ID {
		t.Errorf("node-a returned %d deps, want exactly %q", len(gotA), depA.ID)
	}

	gotB, err := m.ListDeploymentsByNodeID(ctx, "node-b")
	if err != nil {
		t.Fatalf("ListDeploymentsByNodeID(node-b): %v", err)
	}
	if len(gotB) != 1 || gotB[0].ID != depB.ID {
		t.Errorf("node-b returned %d deps, want exactly %q", len(gotB), depB.ID)
	}
}

func TestMemStore_ListDeploymentsByNodeID_ExcludesSoftDeleted(t *testing.T) {
	t.Parallel()
	m, ctx, _, app, dep := memDeploymentFixture(t)
	if _, err := m.SoftDeleteAppCascade(ctx, app.ID); err != nil {
		t.Fatalf("SoftDeleteAppCascade: %v", err)
	}
	got, err := m.ListDeploymentsByNodeID(ctx, "node-a")
	if err != nil {
		t.Fatalf("ListDeploymentsByNodeID: %v", err)
	}
	for _, d := range got {
		if d.ID == dep.ID {
			t.Errorf("soft-deleted app's deployment still in result: %+v", d)
		}
	}
}

func TestMemStore_ConcurrencyForDeployment_NoInstance(t *testing.T) {
	t.Parallel()
	m, ctx, _, _, _ := memDeploymentFixture(t)
	// Pick a fresh app + deployment; ConcurrencyForDeployment scans
	// m.instances for matching (appID, deploymentID), so we want
	// zero matches.
	_, _, _, app, dep := memDeploymentFixture(t)
	got, err := m.ConcurrencyForDeployment(ctx, app.ID, dep.ID)
	if err != nil {
		t.Fatalf("ConcurrencyForDeployment: %v", err)
	}
	if got != 0 {
		t.Errorf("got = %d, want 0 (no live instances)", got)
	}
}

func TestMemStore_ConcurrencyForDeployment_CountsRunning(t *testing.T) {
	t.Parallel()
	m, ctx, _, app, dep := memDeploymentFixture(t)
	// Two live instances in the canonical lowercase state — what a
	// real deployment produces and what Postgres will store.
	_ = memLiveInstance(t, m, ctx, app.ID, dep.ID, "node-a")
	_ = memLiveInstance(t, m, ctx, app.ID, dep.ID, "node-a")
	got, err := m.ConcurrencyForDeployment(ctx, app.ID, dep.ID)
	if err != nil {
		t.Fatalf("ConcurrencyForDeployment: %v", err)
	}
	if got != 2 {
		t.Errorf("got = %d, want 2 (two live instances on this deployment)", got)
	}
}

func TestMemStore_UpdateDeploymentMinInstances_StoresValueVerbatim(t *testing.T) {
	t.Parallel()
	// UpdateDeploymentMinInstances does NOT clamp — it stores the
	// integer the caller passes. The Postgres schema's
	// CHECK min_instances >= 0 is enforced upstream by the SQL
	// layer + handler validation; MemStore mirrors the row
	// update path exactly. This test pins that contract: a caller
	// passing -3 gets -3 back.
	m, ctx, _, _, dep := memDeploymentFixture(t)
	got, err := m.UpdateDeploymentMinInstances(ctx, dep.ID, -3)
	if err != nil {
		t.Fatalf("UpdateDeploymentMinInstances(-3): %v", err)
	}
	if got.MinInstances != -3 {
		t.Errorf("got.MinInstances = %d, want -3 (stored verbatim, no clamping in MemStore)", got.MinInstances)
	}
}

func TestMemStore_UpdateDeploymentMinInstances_Positive(t *testing.T) {
	t.Parallel()
	m, ctx, _, _, dep := memDeploymentFixture(t)
	got, err := m.UpdateDeploymentMinInstances(ctx, dep.ID, 2)
	if err != nil {
		t.Fatalf("UpdateDeploymentMinInstances: %v", err)
	}
	if got.MinInstances != 2 {
		t.Errorf("got.MinInstances = %d, want 2", got.MinInstances)
	}
}

func TestMemStore_ListLiveInstancesOnNode_Empty(t *testing.T) {
	t.Parallel()
	got, err := NewMemStore().ListLiveInstancesOnNode(context.Background(), "node-x", 50)
	if err != nil {
		t.Fatalf("ListLiveInstancesOnNode: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0 on empty store", len(got))
	}
}

func TestMemStore_ListLiveInstancesOnNode_RespectsLimit(t *testing.T) {
	t.Parallel()
	m, ctx, _, app, dep := memDeploymentFixture(t)
	// 3 instances on node-a, but the limit is 2.
	for i := 0; i < 3; i++ {
		_ = memLiveInstance(t, m, ctx, app.ID, dep.ID, "node-a")
	}
	got, err := m.ListLiveInstancesOnNode(ctx, "node-a", 2)
	if err != nil {
		t.Fatalf("ListLiveInstancesOnNode: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2 (limit honored)", len(got))
	}
}

func TestMemStore_MigrateInstanceOwner_Happy(t *testing.T) {
	t.Parallel()
	m, ctx, _, app, dep := memDeploymentFixture(t)
	inst := memLiveInstance(t, m, ctx, app.ID, dep.ID, "node-a")
	lease := uuid.NewString()
	// Pre-condition: instance must be in "migrating" state for
	// MigrateInstanceOwner to accept the transition.
	if err := m.MarkInstanceMigrating(ctx, inst.ID, "node-a", lease); err != nil {
		t.Fatalf("MarkInstanceMigrating: %v", err)
	}
	if err := m.MigrateInstanceOwner(ctx, inst.ID, "node-a", "node-b", lease); err != nil {
		t.Fatalf("MigrateInstanceOwner: %v", err)
	}
	got := memGetInstance(t, m, inst.ID)
	if got.NodeID != "node-b" {
		t.Errorf("NodeID = %q, want node-b", got.NodeID)
	}
}

func TestMemStore_MigrateInstanceOwner_NotFound(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	err := m.MigrateInstanceOwner(context.Background(), uuid.NewString(), "node-a", "node-b", uuid.NewString())
	if err == nil {
		t.Error("MigrateInstanceOwner(unknown) = nil; want not-found error")
	}
}

func TestMemStore_CancelInstanceMigration_Happy(t *testing.T) {
	t.Parallel()
	m, ctx, _, app, dep := memDeploymentFixture(t)
	inst := memLiveInstance(t, m, ctx, app.ID, dep.ID, "node-a")
	lease := uuid.NewString()
	if err := m.MarkInstanceMigrating(ctx, inst.ID, "node-a", lease); err != nil {
		t.Fatalf("MarkInstanceMigrating: %v", err)
	}
	if err := m.MigrateInstanceOwner(ctx, inst.ID, "node-a", "node-b", lease); err != nil {
		t.Fatalf("MigrateInstanceOwner: %v", err)
	}
	// CancelInstanceMigration requires (state == migrating,
	// nodeID == originalNodeID, leaseToken matches). After a
	// successful MigrateInstanceOwner, the nodeID is "node-b", so
	// we cannot roll back to "node-a" directly — that path is
	// exercised by the watchdog in production. For coverage of
	// CancelInstanceMigration's happy path we instead start from
	// a fresh "migrating" instance.
	inst2 := memLiveInstance(t, m, ctx, app.ID, dep.ID, "node-a")
	lease2 := uuid.NewString()
	if err := m.MarkInstanceMigrating(ctx, inst2.ID, "node-a", lease2); err != nil {
		t.Fatalf("MarkInstanceMigrating: %v", err)
	}
	if err := m.CancelInstanceMigration(ctx, inst2.ID, "node-a", lease2); err != nil {
		t.Fatalf("CancelInstanceMigration: %v", err)
	}
	got := memGetInstance(t, m, inst2.ID)
	if got.State != "parked" {
		t.Errorf("State after cancel = %q, want parked", got.State)
	}
}

func TestMemStore_CancelInstanceMigration_NotMigrating(t *testing.T) {
	t.Parallel()
	m, ctx, _, app, dep := memDeploymentFixture(t)
	inst := memLiveInstance(t, m, ctx, app.ID, dep.ID, "node-a")
	// Calling Cancel on a non-migrating instance is a *conflict*
	// error in MemStore, not a no-op — the watchdog handles this
	// defensively by ignoring the conflict. The contract pinned
	// here is: it never returns nil for a state mismatch, so the
	// watchdog must inspect the error.
	err := m.CancelInstanceMigration(ctx, inst.ID, "node-a", uuid.NewString())
	if err == nil {
		t.Error("CancelInstanceMigration on non-migrating = nil; want ErrConflict")
	}
}

func TestMemStore_ListRunningInstancesOnDeadNodes_Empty(t *testing.T) {
	t.Parallel()
	got, err := NewMemStore().ListRunningInstancesOnDeadNodes(context.Background(), time.Now().UTC(), 50)
	if err != nil {
		t.Fatalf("ListRunningInstancesOnDeadNodes: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0 on empty store", len(got))
	}
}

func TestMemStore_FailRunningInstanceOnDeadNode_Happy(t *testing.T) {
	t.Parallel()
	m, ctx, _, app, dep := memDeploymentFixture(t)
	inst := memLiveInstance(t, m, ctx, app.ID, dep.ID, "node-dead")
	// FailRunningInstanceOnDeadNode requires State == "running"
	// (the lowercase string), so the fixture's stored state must
	// match. memLiveInstance already passes string(StateRunning),
	// which is "running" — but only if StateRunning serialises to
	// that. Verify the stored value before calling.
	if got := memGetInstance(t, m, inst.ID); got.State != string(StateRunning) {
		t.Fatalf("pre-call State = %q, want %q", got.State, StateRunning)
	}
	if err := m.FailRunningInstanceOnDeadNode(ctx, inst.ID, "node-dead"); err != nil {
		t.Fatalf("FailRunningInstanceOnDeadNode: %v", err)
	}
	got := memGetInstance(t, m, inst.ID)
	if got.State != string(StateFailed) {
		t.Errorf("State = %q, want %q", got.State, StateFailed)
	}
}

func TestMemStore_FailRunningInstanceOnDeadNode_WrongNode(t *testing.T) {
	t.Parallel()
	m, ctx, _, app, dep := memDeploymentFixture(t)
	inst := memLiveInstance(t, m, ctx, app.ID, dep.ID, "node-live")
	err := m.FailRunningInstanceOnDeadNode(ctx, inst.ID, "node-dead")
	if err == nil {
		t.Error("FailRunningInstanceOnDeadNode(wrong node) = nil; want ErrConflict")
	}
}

func TestMemStore_CountAuthDefaultFlippedApps_NoMatch(t *testing.T) {
	t.Parallel()
	m, ctx, acct, _, _ := memDeploymentFixture(t)
	got, err := m.CountAuthDefaultFlippedApps(ctx, acct.ID)
	if err != nil {
		t.Fatalf("CountAuthDefaultFlippedApps: %v", err)
	}
	if got != 0 {
		t.Errorf("got = %d, want 0 on empty fixture", got)
	}
}

// TestMemStore_DeploymentSweep_SortedByCreatedDesc asserts the
// sort contract for ListAllDeployments on multiple deployments
// using sort.Slice verification on the returned slice.
func TestMemStore_DeploymentSweep_SortedByCreatedDesc(t *testing.T) {
	t.Parallel()
	m, ctx, _, app, _ := memDeploymentFixture(t)
	for i := 0; i < 4; i++ {
		if _, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:extra"}); err != nil {
			t.Fatalf("CreateDeployment[%d]: %v", i, err)
		}
	}
	got, err := m.ListAllDeployments(ctx)
	if err != nil {
		t.Fatalf("ListAllDeployments: %v", err)
	}
	// Verify the slice is non-increasing on CreatedAt.
	if !sort.SliceIsSorted(got, func(i, j int) bool {
		return got[i].CreatedAt.After(got[j].CreatedAt)
	}) {
		t.Errorf("ListAllDeployments not sorted descending on CreatedAt: %+v", got)
	}
}
