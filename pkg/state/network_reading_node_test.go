// adr: 20261122090000
package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// nodeNetworkStore is the slice of a store the live-migration egress case
// uses.
type nodeNetworkStore interface {
	state.NodeNetworkObserver
	AppendUsage(ctx context.Context, accountID, appID, instanceID string, minute time.Time, mbSeconds, requests, cpuUsec, txBytes, netTxBytes, netRxBytes int64, coldBootCount int32, tailSeconds int64) error
}

// A live migration: the source node's final reading is metered once, the
// destination's counter starts from zero even when it is above the source's,
// and the source's departed reading is ignored after the switch.
func runNodeNetworkObservationAcrossLiveMigration(t *testing.T, ctx context.Context, store nodeNetworkStore, accountID, appID, instanceID string) {
	t.Helper()
	minute := time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)
	steps := []struct {
		name     string
		readings []state.NetworkReading
		values   []int64
		want     int64
	}{
		{"source first reading", []state.NetworkReading{{NodeID: "node-a"}}, []int64{1_000}, 1_000},
		{"source grows", []state.NetworkReading{{NodeID: "node-a"}}, []int64{1_400}, 400},
		{"migration: source final then destination", []state.NetworkReading{{NodeID: "node-a", Departed: true}, {NodeID: "node-b"}}, []int64{1_500, 2_000}, 100 + 2_000},
		{"stale source final ignored", []state.NetworkReading{{NodeID: "node-a", Departed: true}, {NodeID: "node-b"}}, []int64{1_500, 2_300}, 300},
		{"legacy reading without node", []state.NetworkReading{{}}, []int64{2_350}, 50},
	}
	for i, step := range steps {
		at := minute.Add(time.Duration(i) * time.Minute)
		if err := store.AppendUsage(ctx, accountID, appID, instanceID, at, 1, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		var got int64
		for j, reading := range step.readings {
			tx, _, _, err := store.AppendNodeNetworkUsageObservation(ctx, accountID, appID, instanceID, at, reading, step.values[j], true, 0, false, nil)
			if err != nil {
				t.Fatalf("%s: %v", step.name, err)
			}
			got += tx
		}
		if got != step.want {
			t.Errorf("%s: egress delta = %d, want %d", step.name, got, step.want)
		}
	}
}

func TestNodeNetworkObservationAcrossLiveMigration(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "migrate-egress@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	runNodeNetworkObservationAcrossLiveMigration(t, ctx, store, acct.ID, uuid.NewString(), uuid.NewString())
}

func TestPg_NodeNetworkObservationAcrossLiveMigration(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	acct, err := s.CreateAccount(ctx, "pg-migrate-egress-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "pg-migrate-egress-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + uuid.NewString(), Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	// meter_network_checkpoints references instances.
	ins, err := s.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 512, resolveDefaultLocal(t, ctx, s), "")
	if err != nil {
		t.Fatal(err)
	}
	runNodeNetworkObservationAcrossLiveMigration(t, ctx, s, acct.ID, app.ID, ins.ID)
}
