package financialtest

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 566 — review reproduction for retained deleted-app residency.
func TestFinancialPostgresDeletedAppResidency(t *testing.T) {
	store, pool, ctx := financialPostgres(t)
	account := financialAccountWithContext(t, ctx, store)
	start, end := financialPeriod()
	minute := start.Add(time.Hour)
	node := financialLocalNode(t, ctx, store)
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "deleted-before-sample", Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256, MinInstances: 1, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, dep.ID, "running", 256, node, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateInstanceStateToTerminal(ctx, instance.ID, "stopped", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update instance_billing_intervals set started_at=$2, ended_at=$3 where instance_id=$1`, instance.ID, minute.Add(10*time.Second), minute.Add(50*time.Second)); err != nil {
		t.Fatal(err)
	}
	seconds, err := store.InstanceBillingSeconds(ctx, minute, minute.Add(time.Minute))
	if err != nil || seconds[instance.ID] != 40 {
		t.Fatalf("residency fixture: %v %v", seconds, err)
	}
	if err := store.DeleteApp(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	sampler := meter.NewSampler(store, nil, func() time.Time { return minute.Add(90 * time.Second) })
	for range 2 {
		if _, err := sampler.SampleAndRoll(ctx); err != nil {
			t.Fatal(err)
		}
	}
	head, err := store.FinancialEvidenceHead(ctx, account.ID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	rows := financialRowsWithContext(t, ctx, store, account.ID, head)
	want := int64(api.BillableRAMMB(256)) * 40
	if len(rows) != 1 || rows[0].Evidence.Quantity != want {
		t.Fatalf("lost closed-minute app usage: got %v; want %d MB-seconds for retained deleted app", rows, want)
	}
	members, err := store.ListDeletedAppsInBillingWindow(ctx, minute, minute.Add(time.Minute))
	if err != nil || len(members) != 1 || members[0].ID != app.ID || members[0].Status != state.AppDeleted {
		t.Fatalf("retained billing membership: %+v, %v", members, err)
	}
	later := meter.NewSampler(store, nil, func() time.Time { return minute.Add(150 * time.Second) })
	if rows, err := later.SampleAndRoll(ctx); err != nil || len(rows) != 0 {
		t.Fatalf("deleted app acquired new floor usage: %+v, %v", rows, err)
	}
	members, err = store.ListDeletedAppsInBillingWindow(ctx, minute.Add(time.Minute), minute.Add(2*time.Minute))
	if err != nil || len(members) != 0 {
		t.Fatalf("expired residency retained billing membership: %+v, %v", members, err)
	}
}
