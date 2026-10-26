package state_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestMem_ManualTrafficMarksSplitMembersExplicit keeps MemStore's flags in
// step with PgStore's (Hunt 14 H14-71): split members are explicit, the row
// that ends at 100 and a row lowered to 0 are not, and an untouched dark
// release keeps its flag.
func TestMem_ManualTrafficMarksSplitMembersExplicit(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	acct, err := m.CreateAccount(ctx, "manual-traffic@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "manual-traffic", Type: state.AppTypeApp, RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	create := func() string {
		t.Helper()
		d, err := m.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	v4, v5, dark := create(), create(), create()
	m.SetDeploymentTrafficForTest(v4, 0, false)
	m.SetDeploymentTrafficForTest(v5, 100, false)
	m.SetDeploymentTrafficForTest(dark, 0, true)

	if _, err := m.UpdateDeploymentTraffic(ctx, v4, 50); err != nil {
		t.Fatalf("split: %v", err)
	}
	for id, want := range map[string]bool{v4: true, v5: true, dark: true} {
		if d, _ := m.DeploymentByID(ctx, id); d.TrafficPercentExplicit != want {
			t.Errorf("after split %s explicit=%v, want %v", id, d.TrafficPercentExplicit, want)
		}
	}
	if _, err := m.UpdateDeploymentTraffic(ctx, v5, 100); err != nil {
		t.Fatalf("promote: %v", err)
	}
	for id, want := range map[string]bool{v4: false, v5: false, dark: true} {
		if d, _ := m.DeploymentByID(ctx, id); d.TrafficPercentExplicit != want {
			t.Errorf("after promote %s explicit=%v, want %v", id, d.TrafficPercentExplicit, want)
		}
	}
}
