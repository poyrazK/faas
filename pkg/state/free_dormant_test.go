// adr: 20261105120000
package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type freeDormantTestStore interface {
	state.DormantFreeAppLister
	CreateAccount(ctx context.Context, email string, plan api.Plan) (state.Account, error)
	CreateApp(ctx context.Context, app state.App) (state.App, error)
	CreateDeployment(ctx context.Context, d state.Deployment) (state.Deployment, error)
	CreateSnapshot(ctx context.Context, s state.Snapshot) (state.Snapshot, error)
	CreateInstance(ctx context.Context, appID, deploymentID, st string, ramMB int, nodeID, wakeID string) (state.Instance, error)
}

func TestMemListDormantFreeSnapshotApps(t *testing.T) {
	testListDormantFreeSnapshotApps(t, state.NewMemStore(), "node-1")
}

func TestPgListDormantFreeSnapshotApps(t *testing.T) {
	s, ctx := pgStore(t)
	testListDormantFreeSnapshotApps(t, s, resolveDefaultLocal(t, ctx, s))
}

// The Free-tier disk reaper selects only Free apps with a snapshot and no
// live instance or activity since the cutoff.
func testListDormantFreeSnapshotApps(t *testing.T, s freeDormantTestStore, nodeID string) {
	t.Helper()
	ctx := context.Background()
	before := time.Now().UTC().Add(-time.Minute)
	app := func(plan api.Plan, snapshot bool, instanceState string) string {
		t.Helper()
		acct, err := s.CreateAccount(ctx, "dormant-"+uuid.NewString()[:8]+"@example.com", plan)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		a, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "dormant-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
		if err != nil {
			t.Fatalf("CreateApp: %v", err)
		}
		dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: a.ID, Kind: state.DeploymentKindImage})
		if err != nil {
			t.Fatalf("CreateDeployment: %v", err)
		}
		if snapshot {
			if _, err := s.CreateSnapshot(ctx, state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10", StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "dormant")}); err != nil {
				t.Fatalf("CreateSnapshot: %v", err)
			}
		}
		if instanceState != "" {
			if _, err := s.CreateInstance(ctx, a.ID, dep.ID, instanceState, 256, nodeID, ""); err != nil {
				t.Fatalf("CreateInstance: %v", err)
			}
		}
		return a.ID
	}
	dormant := app(api.PlanFree, true, "")
	noSnapshot := app(api.PlanFree, false, "")
	running := app(api.PlanFree, true, string(state.StateRunning))
	paid := app(api.PlanHobby, true, "")

	// Everything was created before a cutoff an hour from now.
	got, err := s.ListDormantFreeSnapshotApps(ctx, time.Now().UTC().Add(time.Hour), 1000)
	if err != nil {
		t.Fatalf("ListDormantFreeSnapshotApps: %v", err)
	}
	found := map[string]bool{}
	for _, id := range got {
		found[id] = true
	}
	if !found[dormant] {
		t.Fatalf("dormant Free app %s missing from %v", dormant, got)
	}
	for name, id := range map[string]string{"no snapshot": noSnapshot, "running": running, "paid": paid} {
		if found[id] {
			t.Fatalf("%s app %s selected: %v", name, id, got)
		}
	}
	// With the cutoff before the apps were created and deployed, none is
	// dormant.
	recent, err := s.ListDormantFreeSnapshotApps(ctx, before, 1000)
	if err != nil {
		t.Fatalf("ListDormantFreeSnapshotApps(before): %v", err)
	}
	for _, id := range recent {
		if id == dormant {
			t.Fatalf("app created after the cutoff selected: %v", recent)
		}
	}
	if limited, err := s.ListDormantFreeSnapshotApps(ctx, time.Now().UTC().Add(time.Hour), 0); err != nil || len(limited) != 0 {
		t.Fatalf("limit 0 = %v, %v; want none", limited, err)
	}
}
