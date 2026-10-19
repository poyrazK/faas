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
