package sched

// adr: 177

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostport"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAdmitInstanceLeasesDeclaredPortsAndReleasesOnStop(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "hostports@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID:      acct.ID,
		Slug:           "host-port-app",
		RAMMB:          128,
		MaxConcurrency: 2,
		Manifest: state.AppManifest{Ports: []api.WorkloadPort{
			{Name: "http", Port: 8080, Protocol: api.WorkloadPortTCP},
			{Name: "dns", Port: 53, Protocol: api.WorkloadPortUDP},
		}},
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:hostports", Status: state.DeployLive})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	result, err := e.AdmitInstanceForDeployment(ctx, app.ID, dep.ID, "", TriggerServiceReplica)
	if err != nil {
		t.Fatalf("AdmitInstanceForDeployment: %v", err)
	}
	instance, err := store.InstanceByID(ctx, result.InstanceID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	leases, err := store.ListHostPortLeases(ctx, instance.NodeID, result.InstanceID)
	if err != nil {
		t.Fatalf("ListHostPortLeases: %v", err)
	}
	if len(leases) != 2 {
		t.Fatalf("leases=%+v, want two declared listeners", leases)
	}
	for _, lease := range leases {
		if lease.HostPort < hostport.DefaultStartPort || lease.HostPort > hostport.DefaultEndPort {
			t.Fatalf("lease outside registry range: %+v", lease)
		}
	}
	e.transition(ctx, result.InstanceID, app.ID, state.StateStopped)
	leases, err = store.ListHostPortLeases(ctx, "", result.InstanceID)
	if err != nil {
		t.Fatalf("ListHostPortLeases after stop: %v", err)
	}
	if len(leases) != 0 {
		t.Fatalf("stop leaked host-port leases: %+v", leases)
	}
}
