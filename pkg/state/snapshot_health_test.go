package state_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// The snapshot health reads return the same rows on both stores
// (ADR-20261122135018): the inventory largest footprint first with its app,
// and the live deployments with no usable snapshot, those without a rootfs
// first.
func TestSnapshotHealthStores(t *testing.T) {
	for _, backend := range []string{"mem", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			var s interface {
				state.SnapshotHealthReader
				CreateAccount(context.Context, string, api.Plan) (state.Account, error)
				CreateApp(context.Context, state.App) (state.App, error)
				CreateDeployment(context.Context, state.Deployment) (state.Deployment, error)
				CreateSnapshot(context.Context, state.Snapshot) (state.Snapshot, error)
				MarkSnapshotStale(context.Context, string) error
			} = state.NewMemStore()
			// The Postgres store creates a deployment pending with no layer;
			// the test puts it live with its rootfs the way imaged and the
			// activation would. The memory store keeps the fields as given.
			publish := func(state.Deployment) {}
			if backend == "postgres" {
				pool := pgtest.OpenMigrated(t)
				s = state.NewPgStore(pool)
				publish = func(d state.Deployment) {
					t.Helper()
					if _, err := pool.Exec(ctx, `UPDATE deployments SET status = 'live', rootfs_key = $2, rootfs_bytes = $3 WHERE id = $1`,
						d.ID, d.RootfsKey, d.RootfsBytes); err != nil {
						t.Fatal(err)
					}
				}
			}
			acct, err := s.CreateAccount(ctx, "snapshot-health@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			const mb = 1 << 20
			deploy := func(slug, rootfsKey string, layer int64) state.Deployment {
				t.Helper()
				a, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Status: state.AppActive})
				if err != nil {
					t.Fatal(err)
				}
				d, err := s.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: a.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive,
					ImageDigest: "sha256:" + fmt.Sprintf("%064d", 1), RootfsKey: rootfsKey, RootfsBytes: layer})
				if err != nil {
					t.Fatal(err)
				}
				d.RootfsKey, d.RootfsBytes = rootfsKey, layer
				publish(d)
				return d
			}
			snapshot := func(d state.Deployment, stored, mem, disk int64) state.Snapshot {
				t.Helper()
				sn, err := s.CreateSnapshot(ctx, state.Snapshot{DeploymentID: d.ID, FCVersion: "1.12.0", StoredBytes: stored, MemBytes: mem,
					DiskBytes: disk, Tier: state.SnapshotTierInit, StorageKey: "snap/" + d.ID})
				if err != nil {
					t.Fatal(err)
				}
				return sn
			}
			big := deploy("health-big", "layer/big", 30*mb)
			snapshot(big, 150*mb, 512*mb, 0)
			legacy := deploy("health-legacy", "layer/legacy", 0)
			snapshot(legacy, 0, 40*mb, 20*mb)
			stale := deploy("health-stale", "layer/stale", 0)
			if err := s.MarkSnapshotStale(ctx, snapshot(stale, 10*mb, 10*mb, 0).ID); err != nil {
				t.Fatal(err)
			}
			broken := deploy("health-broken", "", 0)

			inv, err := s.OperatorSnapshotInventory(ctx, 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(inv) != 3 || inv[0].AppSlug != "health-big" || inv[0].SnapshotBytes != 150*mb || inv[0].LayerBytes != 30*mb || inv[0].AccountID != acct.ID {
				t.Fatalf("inventory = %+v", inv)
			}
			if inv[1].AppSlug != "health-legacy" || inv[1].SnapshotBytes != 60*mb || inv[2].AppSlug != "health-stale" || !inv[2].Stale {
				t.Fatalf("inventory order = %+v", inv)
			}
			if inv, err := s.OperatorSnapshotInventory(ctx, 1); err != nil || len(inv) != 1 {
				t.Fatalf("limited inventory = %+v err=%v", inv, err)
			}
			deps, err := s.OperatorLiveDeploymentsWithoutSnapshot(ctx, 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(deps) != 2 || deps[0].DeploymentID != broken.ID || deps[0].ColdBootable || deps[1].DeploymentID != stale.ID || !deps[1].ColdBootable {
				t.Fatalf("unsnapshotted = %+v", deps)
			}
			for _, call := range []func() error{
				func() error { _, err := s.OperatorSnapshotInventory(ctx, 0); return err },
				func() error { _, err := s.OperatorLiveDeploymentsWithoutSnapshot(ctx, 0); return err },
			} {
				if call() == nil {
					t.Fatal("limit 0: want error")
				}
			}
		})
	}
}
