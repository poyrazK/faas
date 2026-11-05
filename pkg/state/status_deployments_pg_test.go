package state_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgListDeploymentsWithStatusNarrowsListAllDeployments pins imaged's
// narrowed sweep listing to ListAllDeployments filtered by status: the same
// rows in the same order, and none from a deleted app.
func TestPgListDeploymentsWithStatusNarrowsListAllDeployments(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	seed := func(appStatus string, statuses ...string) []state.Deployment {
		t.Helper()
		_, app := seedPgAccountAndApp(t, s, ctx)
		var deps []state.Deployment
		for _, status := range statuses {
			d := seedPgDeployment(t, s, ctx, app)
			if _, err := pool.Exec(ctx, `update deployments set status = $2, traffic_percent = 0 where id = $1`, d.ID, status); err != nil {
				t.Fatalf("update deployment: %v", err)
			}
			deps = append(deps, d)
		}
		// The app status goes last: a deleted app takes no new deployments.
		if _, err := pool.Exec(ctx, `update apps set status = $2 where id = $1`, app.ID, appStatus); err != nil {
			t.Fatalf("update app: %v", err)
		}
		return deps
	}
	seed("active", "live", "superseded", "snapshotting", "failed")
	seed("active", "live")
	deleted := seed("deleted", "live")

	statuses := []state.DeploymentStatus{state.DeployLive, state.DeploySnapshotting}
	got, err := s.ListDeploymentsWithStatus(ctx, statuses...)
	if err != nil {
		t.Fatalf("ListDeploymentsWithStatus: %v", err)
	}
	all, err := s.ListAllDeployments(ctx)
	if err != nil {
		t.Fatalf("ListAllDeployments: %v", err)
	}
	var want []string
	for _, d := range all {
		if slices.Contains(statuses, d.Status) {
			want = append(want, d.ID)
		}
	}
	ids := make([]string, 0, len(got))
	for _, d := range got {
		ids = append(ids, d.ID)
	}
	if len(want) != 3 || !slices.Equal(ids, want) {
		t.Fatalf("ListDeploymentsWithStatus = %v, want %v (deleted app's %s absent)", ids, want, deleted[0].ID)
	}
	if none, err := s.ListDeploymentsWithStatus(ctx); err != nil || len(none) != 0 {
		t.Fatalf("ListDeploymentsWithStatus() = %d rows, %v; want none", len(none), err)
	}
}

// TestPgListDeploymentsWithStatusForNodeScopesToOwner pins O32's node-scoped
// sweep listing: a node gets its own apps' serving deployments and those of
// apps whose owner is not active, never another active node's.
func TestPgListDeploymentsWithStatusForNodeScopesToOwner(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	mine := resolveDefaultLocal(t, ctx, s)
	newNode := func(prefix string) state.ComputeNode {
		t.Helper()
		n, err := s.CreateComputeNode(ctx, state.ComputeNode{
			Name: prefix + uuid.NewString()[:8], TargetURL: "tcp://127.0.0.1:50051",
			VPCPUs: 1, MemMB: 1024, MaxConcurrency: 10, AdmissionCeilingMB: 512, VCPUBudget: 1, Active: true,
		})
		if err != nil {
			t.Fatalf("CreateComputeNode: %v", err)
		}
		return n
	}
	other, retired := newNode("o32-other-"), newNode("o32-retired-")
	if _, err := pool.Exec(ctx, `update compute_nodes set lifecycle = 'retired' where id = $1`, retired.ID); err != nil {
		t.Fatalf("retire node: %v", err)
	}
	liveOn := func(nodeID string) string {
		t.Helper()
		_, app := seedPgAccountAndApp(t, s, ctx)
		d := seedPgDeployment(t, s, ctx, app)
		if _, err := pool.Exec(ctx, `update deployments set status = 'live', traffic_percent = 0 where id = $1`, d.ID); err != nil {
			t.Fatalf("update deployment: %v", err)
		}
		if _, err := pool.Exec(ctx, `update apps set node_id = $2 where id = $1`, app.ID, nodeID); err != nil {
			t.Fatalf("update app node: %v", err)
		}
		return d.ID
	}
	own, foreign, orphaned := liveOn(mine.ID), liveOn(other.ID), liveOn(retired.ID)

	ids := func(nodeID string) []string {
		t.Helper()
		got, err := s.ListDeploymentsWithStatusForNode(ctx, nodeID, state.DeployLive)
		if err != nil {
			t.Fatalf("ListDeploymentsWithStatusForNode: %v", err)
		}
		out := make([]string, 0, len(got))
		for _, d := range got {
			out = append(out, d.ID)
		}
		return out
	}
	for _, tc := range []struct {
		node          string
		want, notWant []string
	}{
		{mine.ID, []string{own, orphaned}, []string{foreign}},
		{other.ID, []string{foreign, orphaned}, []string{own}},
	} {
		got := ids(tc.node)
		for _, id := range tc.want {
			if !slices.Contains(got, id) {
				t.Fatalf("node %s listing %v misses %s", tc.node, got, id)
			}
		}
		for _, id := range tc.notWant {
			if slices.Contains(got, id) {
				t.Fatalf("node %s listing %v includes another active node's %s", tc.node, got, id)
			}
		}
	}
}
