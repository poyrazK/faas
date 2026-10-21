package state_test

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgListActiveInstancesOnNodeIDSkipsHistory pins meterd's fleet-stats read:
// only the node's waking, cold-booting and running instances come back, newest
// first, and neither history nor another node's rows do.
func TestPgListActiveInstancesOnNodeIDSkipsHistory(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	node := resolveDefaultLocal(t, ctx, s)
	other, err := s.CreateComputeNode(ctx, state.ComputeNode{
		Name: "active-on-node-" + uuid.NewString()[:8], TargetURL: "tcp://127.0.0.1:50051",
		VPCPUs: 1, MemMB: 1024, MaxConcurrency: 10, AdmissionCeilingMB: 512, VCPUBudget: 1, Active: true,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	_, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	base := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	insert := func(nodeID, st string, minute int) string {
		t.Helper()
		id := uuid.NewString()
		if _, err := pool.Exec(ctx, `
			insert into instances (id, app_id, deployment_id, node_id, state, ram_mb, started_at)
			values ($1, $2, $3, $4, $5, 256, $6)`,
			id, app.ID, dep.ID, nodeID, st, base.Add(time.Duration(minute)*time.Minute)); err != nil {
			t.Fatalf("insert %s instance: %v", st, err)
		}
		return id
	}
	running := insert(node, "running", 1)
	waking := insert(node, "waking", 3)
	booting := insert(node, "cold_booting", 2)
	for i, st := range []string{"parked", "stopped", "failed", "draining", "frozen"} {
		insert(node, st, 10+i)
	}
	insert(other.ID, "running", 20)

	got, err := s.ListActiveInstancesOnNodeID(ctx, node)
	if err != nil {
		t.Fatalf("ListActiveInstancesOnNodeID: %v", err)
	}
	ids := make([]string, 0, len(got))
	for _, ins := range got {
		ids = append(ids, ins.ID)
	}
	if want := []string{waking, booting, running}; !slices.Equal(ids, want) {
		t.Fatalf("active instances = %v, want %v (newest first, no history, no other node)", ids, want)
	}
}
