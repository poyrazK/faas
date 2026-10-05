//go:build !no_pg

package state_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
)

func TestPgWorkerAccountReservationRace(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	fx := conformance.Seed(t, store)
	app, err := store.CreateApp(ctx, state.App{AccountID: fx.Account.ID,
		Slug: "other-worker", RAMMB: 128, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging",
		Kind: state.DeploymentKindImage, ImageDigest: "sha256:other-worker"})
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "other-worker-node",
		TargetURL: "unix:///tmp/worker-reservation.sock", VPCPUs: 2, MemMB: 4096,
		MaxConcurrency: 20, AdmissionCeilingMB: 4096, VCPUBudget: 2, Lifecycle: state.NodeLifecycleActive})
	if err != nil {
		t.Fatal(err)
	}
	const attempts = 16
	start := make(chan struct{})
	var done sync.WaitGroup
	errs := make([]error, attempts)
	for i := range attempts {
		done.Add(1)
		go func() {
			defer done.Done()
			<-start
			selectedApp, selectedDep, selectedNode := fx.App, fx.Deployment, fx.Node
			if i%2 == 1 {
				selectedApp, selectedDep, selectedNode = app, dep, node
			}
			_, errs[i] = store.CreateInstanceWithMode(ctx, selectedApp.ID, selectedDep.ID,
				string(state.StateColdBooting), 128, selectedNode.ID, uuid.NewString(), string(state.InstanceModeWorker))
		}()
	}
	close(start)
	done.Wait()
	admitted := 0
	for i, err := range errs {
		if err == nil {
			admitted++
		} else if !errors.Is(err, state.ErrAccountWorkerCapacity) {
			t.Fatalf("worker admission %d: %v", i, err)
		}
	}
	limit := api.MustLimitsFor(fx.Account.Plan).WorkerReplicasMax
	rows, err := store.ListInstancesForAccount(ctx, fx.Account.ID)
	if err != nil || admitted != limit || len(rows) != limit {
		t.Fatalf("cross-node/account race admitted=%d persisted=%d want=%d err=%v", admitted, len(rows), limit, err)
	}
	// A failed insert releases both locks and must not consume the reopened
	// account slot. This uses a real SQL constraint failure after the check.
	if err := store.DeleteInstance(ctx, rows[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateWaking),
		128, node.ID, "invalid-wake-uuid", string(state.InstanceModeWorker)); err == nil {
		t.Fatal("invalid insert unexpectedly published a worker")
	}
	if _, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateWaking),
		128, node.ID, uuid.NewString(), string(state.InstanceModeWorker)); err != nil {
		t.Fatalf("failed insert consumed account slot or retained a lock: %v", err)
	}
	rows, err = store.ListInstancesForAccount(ctx, fx.Account.ID)
	if err != nil || len(rows) != limit {
		t.Fatalf("rollback recovery rows=%d want=%d err=%v", len(rows), limit, err)
	}
}
