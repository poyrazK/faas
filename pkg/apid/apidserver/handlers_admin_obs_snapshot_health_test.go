package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// The snapshot health report sizes the fleet's snapshots, lists the largest
// and recently stale ones, and names live deployments with no usable snapshot
// (ADR-20261122135018).
func TestObsSnapshotHealthReport(t *testing.T) {
	e := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	ctx := context.Background()
	store := *e.Store()
	const mb = 1 << 20
	deploy := func(slug, rootfsKey string, layerMB int64) state.Deployment {
		t.Helper()
		a, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: slug, Type: state.AppTypeApp, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		d, err := store.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: a.ID, Status: state.DeployLive,
			RootfsKey: rootfsKey, RootfsBytes: layerMB * mb})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	snapshot := func(d state.Deployment, storedMB int64) state.Snapshot {
		t.Helper()
		sn, err := store.CreateSnapshot(ctx, state.Snapshot{DeploymentID: d.ID, FCVersion: "1.12.0", StoredBytes: storedMB * mb,
			MemBytes: 512 * mb, Tier: state.SnapshotTierInit, StorageKey: "snap/" + d.ID})
		if err != nil {
			t.Fatal(err)
		}
		return sn
	}
	shop := deploy("snap-shop", "layer/shop", 20)
	snapshot(shop, 180)
	blog := deploy("snap-blog", "layer/blog", 10)
	snapshot(blog, 70)
	stale := deploy("snap-stale", "layer/stale", 0)
	if err := store.MarkSnapshotStale(ctx, snapshot(stale, 90).ID); err != nil {
		t.Fatal(err)
	}
	broken := deploy("snap-broken", "", 0)

	rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/snapshot-health", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var r api.ObsSnapshotHealthReport
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Totals.Live != 2 || r.Totals.Stale != 1 || r.Totals.AvgBytes != 140*mb || r.Totals.OverWarn != 1 || r.TargetMB != 130 {
		t.Fatalf("totals = %+v", r.Totals)
	}
	if len(r.Largest) != 2 || r.Largest[0].AppSlug != "snap-shop" || r.Largest[0].FootprintBytes != 200*mb {
		t.Fatalf("largest = %+v", r.Largest)
	}
	if len(r.RecentlyStale) != 1 || r.RecentlyStale[0].AppSlug != "snap-stale" {
		t.Fatalf("recently stale = %+v", r.RecentlyStale)
	}
	if r.Totals.Unsnapshotted != 2 || r.Totals.NotColdBootable != 1 || r.Unsnapshotted[0].DeploymentID != broken.ID || r.Unsnapshotted[0].ColdBootable {
		t.Fatalf("unsnapshotted = %+v (totals %+v)", r.Unsnapshotted, r.Totals)
	}
	if r.Unsnapshotted[1].DeploymentID != stale.ID || !r.Unsnapshotted[1].ColdBootable {
		t.Fatalf("stale-only deployment = %+v", r.Unsnapshotted[1])
	}
}
