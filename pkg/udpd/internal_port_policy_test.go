package udpd_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/udpd"
)

func TestInternalPortWithdrawsPublicUDP(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "review-udp@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	manifest := state.AppManifest{Ports: []api.WorkloadPort{{Name: "service", Port: 5353, Protocol: api.WorkloadPortUDP}}}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "review-udp", Status: state.AppActive, RAMMB: 256, Manifest: manifest})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := store.CreateUDPListener(ctx, state.UDPListener{AppID: app.ID, AccountID: account.ID, ListenerName: "service", GuestPort: 5353, PublicPort: 40100, Protocol: "udp", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateDeployment(ctx, state.Deployment{ID: "dep-review", AppID: app.ID, Status: state.DeployLive, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateInstance(ctx, app.ID, "dep-review", string(state.StateRunning), 256, "node-review", "")
	if err != nil {
		t.Fatal(err)
	}
	manifest.Ports[0].Internal = true
	if _, err = store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	if err = api.ValidateWorkloadPorts(manifest.Ports); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListEnabledUDPListeners(ctx)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &udpd.StoreTargetResolver{Store: store}
	route := udpd.Route{ListenerID: listener.ID, PublicPort: 40100, AppID: app.ID, AccountID: account.ID, ListenerName: "service", GuestPort: 5353}
	if len(rows) != 0 {
		t.Fatalf("internal port still in public inventory: %v", rows)
	}
	target, err := resolver.ResolveTarget(ctx, route)
	if err == nil {
		t.Fatalf("port is internal but public resolver still admits target=%+v; public supervisor inventory contains %d listeners", target, len(rows))
	}
}
