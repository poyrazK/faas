package tcpd_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tcpd"
)

func TestInternalPortWithdrawsPublicTCP(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "review-tcp@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	manifest := state.AppManifest{Ports: []api.WorkloadPort{{Name: "service", Port: 9000, Protocol: api.WorkloadPortTCP}}}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "review-tcp", Status: state.AppActive, RAMMB: 256, Manifest: manifest})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := store.CreateTCPListener(ctx, state.TCPListener{AppID: app.ID, AccountID: account.ID, ListenerName: "service", GuestPort: 9000, PublicPort: 40100, Protocol: "tcp", Enabled: true})
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
	rows, err := store.ListEnabledTCPListeners(ctx)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &tcpd.StoreTargetResolver{Instances: store}
	route := tcpd.Route{ListenerID: listener.ID, PublicPort: 40100, AppID: app.ID, AccountID: account.ID, ListenerName: "service", GuestPort: 9000, Protocol: "tcp"}
	if len(rows) != 0 {
		t.Fatalf("internal port still in public inventory: %v", rows)
	}
	target, err := resolver.ResolveTarget(ctx, route)
	if err == nil {
		t.Fatalf("port is internal but public resolver still admits target=%+v; public supervisor inventory contains %d listeners", target, len(rows))
	}
}
