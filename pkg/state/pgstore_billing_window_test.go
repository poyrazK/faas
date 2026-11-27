package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// The meter's billing-window listing filters in SQL: live rows, rows whose
// lifecycle-ledger interval reaches into the window (a plain state update
// leaves parked_at NULL, so only the ledger shows a recent park), and rows
// stopped in it. A row whose residency ended a month before stays out.
func TestPg_ListBillingWindowInstances(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, err := s.CreateAccount(ctx, "pg-billing-window-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "pg-billing-window-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + uuid.NewString(), Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	nodeID := resolveDefaultLocal(t, ctx, s)
	create := func() state.Instance {
		t.Helper()
		ins, err := s.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 512, nodeID, "")
		if err != nil {
			t.Fatal(err)
		}
		return ins
	}
	now := time.Now().UTC()
	running := create()
	parkedNow := create()
	if err := s.UpdateInstanceState(ctx, parkedNow.ID, string(state.StateParked)); err != nil {
		t.Fatal(err)
	}
	failedNow := create()
	if err := s.UpdateInstanceStateToTerminal(ctx, failedNow.ID, string(state.StateFailed), now); err != nil {
		t.Fatal(err)
	}
	old := create()
	if err := s.UpdateInstanceState(ctx, old.ID, string(state.StateParked)); err != nil {
		t.Fatal(err)
	}
	monthAgo := now.Add(-30 * 24 * time.Hour)
	if _, err := pool.Exec(ctx, `update instance_billing_intervals set started_at = $2, ended_at = $3 where instance_id = $1`,
		old.ID, monthAgo, monthAgo.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update instances set started_at = $2 where id = $1`, old.ID, monthAgo); err != nil {
		t.Fatal(err)
	}

	all, err := s.ListInstancesForApp(ctx, app.ID)
	if err != nil || len(all) != 4 {
		t.Fatalf("full history = %d, %v; want 4", len(all), err)
	}
	rows, err := s.ListBillingWindowInstances(ctx, now.Add(-24*time.Hour), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, ins := range rows {
		listed[ins.ID] = true
	}
	for name, id := range map[string]string{"running": running.ID, "parked now": parkedNow.ID, "failed now": failedNow.ID} {
		if !listed[id] {
			t.Errorf("%s instance missing from the billing window", name)
		}
	}
	if listed[old.ID] {
		t.Error("instance resident a month ago was listed")
	}
}

// InstanceBillingSeconds counts residency that overlaps [start, end): an open
// interval and one that ended inside the window count, one that ended before
// it or starts at its end does not.
func TestPg_InstanceBillingSecondsOverlap(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, err := s.CreateAccount(ctx, "pg-billing-seconds-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "pg-billing-seconds-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + uuid.NewString(), Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	nodeID := resolveDefaultLocal(t, ctx, s)
	// The seeded node's heartbeat is as old as the template database, so later
	// in a shard it is past api.DeadNodeReconcilerStalenessSeconds and the
	// dead-node clamp (#7777) bills nothing. This test is about window overlap
	// on a live node.
	if _, err := pool.Exec(ctx, `update compute_nodes set last_heartbeat_at = now() where id = $1`, nodeID); err != nil {
		t.Fatal(err)
	}
	end := time.Now().UTC().Truncate(time.Second)
	start := end.Add(-time.Minute)
	resident := func(startedAt time.Time, endedAt *time.Time) string {
		t.Helper()
		ins, err := s.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 512, nodeID, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `update instance_billing_intervals set started_at = $2, ended_at = $3 where instance_id = $1`, ins.ID, startedAt, endedAt); err != nil {
			t.Fatal(err)
		}
		return ins.ID
	}
	at := func(d time.Duration) *time.Time { v := end.Add(d); return &v }
	open := resident(end.Add(-10*time.Minute), nil)
	endedInside := resident(end.Add(-5*time.Minute), at(-30*time.Second))
	endedBefore := resident(end.Add(-5*time.Minute), at(-2*time.Minute))
	startsAtEnd := resident(end, nil)

	got, err := s.InstanceBillingSeconds(ctx, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if got[open] != 60 || got[endedInside] != 30 {
		t.Errorf("open = %d s, ended inside = %d s; want 60 and 30", got[open], got[endedInside])
	}
	if _, ok := got[endedBefore]; ok {
		t.Error("an interval that ended before the window was counted")
	}
	if _, ok := got[startsAtEnd]; ok {
		t.Error("an interval that starts at the window end was counted")
	}
}

// A running row on a node that stopped heartbeating bills only up to the
// node's last heartbeat; the same row on a live node bills the whole minute
// (account usage audit 2026-11-21: dead-node ghosts billed for ~24 h).
func TestPg_InstanceBillingSecondsStopsAtDeadNodeHeartbeat(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, err := s.CreateAccount(ctx, "pg-billing-dead-node-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "pg-billing-dead-node-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + uuid.NewString(), Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	deadNode, err := s.UpsertComputeNode(ctx, state.ComputeNode{Name: "dead-" + uuid.NewString(), TargetURL: "unix:///tmp/dead-" + uuid.NewString() + ".sock", VPCPUs: 4, VCPUBudget: api.VCPUSlots, MemMB: 4096, MaxConcurrency: 8, AdmissionCeilingMB: 4096, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	liveNode := resolveDefaultLocal(t, ctx, s)
	if _, err := pool.Exec(ctx, `update compute_nodes set last_heartbeat_at = now() where id = $1`, liveNode); err != nil {
		t.Fatal(err)
	}
	end := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Minute)
	start := end.Add(-time.Minute)
	lastBeat := start.Add(20 * time.Second)
	if _, err := pool.Exec(ctx, `update compute_nodes set last_heartbeat_at = $2 where id = $1`, deadNode.ID, lastBeat); err != nil {
		t.Fatal(err)
	}
	resident := func(nodeID string) string {
		t.Helper()
		ins, err := s.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 512, nodeID, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `update instance_billing_intervals set started_at = $2, ended_at = null where instance_id = $1`, ins.ID, start.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		return ins.ID
	}
	onDead := resident(deadNode.ID)
	onLive := resident(liveNode)

	got, err := s.InstanceBillingSeconds(ctx, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if got[onDead] != 20 {
		t.Errorf("dead-node instance = %d s, want 20 (up to its node's last heartbeat)", got[onDead])
	}
	if got[onLive] != 60 {
		t.Errorf("live-node instance = %d s, want 60", got[onLive])
	}
	// A minute wholly after the last heartbeat bills nothing.
	later, err := s.InstanceBillingSeconds(ctx, end, end.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := later[onDead]; ok {
		t.Errorf("dead-node instance billed %d s after its node's last heartbeat", v)
	}
}

// A platform snapshot refresh opens no billing interval, like a mirror; an
// ordinary instance in the same state does (ADR-20261106214916).
func TestPg_SnapshotRefreshOpensNoBillingInterval(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, err := s.CreateAccount(ctx, "pg-refresh-billing-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "pg-refresh-billing-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + uuid.NewString(), Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	nodeID := resolveDefaultLocal(t, ctx, s)
	intervals := func(instanceID string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `select count(*) from instance_billing_intervals where instance_id = $1`, instanceID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	refresh, err := s.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateColdBooting), 512, nodeID, uuid.NewString(), string(state.InstanceModeRefresh))
	if err != nil {
		t.Fatal(err)
	}
	normal, err := s.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateColdBooting), 512, nodeID, uuid.NewString(), string(state.InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	if got := intervals(refresh.ID); got != 0 {
		t.Errorf("a cold-booting refresh opened %d billing intervals, want 0", got)
	}
	if got := intervals(normal.ID); got != 1 {
		t.Errorf("a cold-booting normal instance opened %d billing intervals, want 1", got)
	}
	if _, err := pool.Exec(ctx, `update instances set state = 'running' where id = $1`, refresh.ID); err != nil {
		t.Fatal(err)
	}
	if got := intervals(refresh.ID); got != 0 {
		t.Errorf("a running refresh opened %d billing intervals, want 0", got)
	}
}
