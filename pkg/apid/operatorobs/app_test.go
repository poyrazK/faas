package operatorobs

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeAppSnapshots struct {
	deployments []state.Deployment
	snapshots   map[string]state.Snapshot // deploymentID/tier
	err         error
	reads       int
}

func (f *fakeAppSnapshots) LiveDeployments(context.Context, string) ([]state.Deployment, error) {
	return f.deployments, nil
}

func (f *fakeAppSnapshots) LatestSnapshotForTier(_ context.Context, deploymentID, tier string) (state.Snapshot, error) {
	f.reads++
	if f.err != nil {
		return state.Snapshot{}, f.err
	}
	snap, ok := f.snapshots[deploymentID+"/"+tier]
	if !ok {
		return state.Snapshot{}, state.ErrNotFound
	}
	return snap, nil
}

func TestAppSnapshotsReportsEachTierOfEachLiveDeployment(t *testing.T) {
	at := time.Date(2026, 10, 24, 8, 15, 0, 0, time.UTC)
	f := &fakeAppSnapshots{
		deployments: []state.Deployment{{ID: "dep-new"}, {ID: "dep-old"}},
		snapshots: map[string]state.Snapshot{
			"dep-new/init": {ID: "snap-1", FCVersion: "1.7.0", MemBytes: 128 << 20, StoredBytes: 40 << 20, CreatedAt: at},
		},
	}
	got, err := AppSnapshots(context.Background(), f, "app-1")
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, r := range got {
		rows = append(rows, fmt.Sprintf("%s/%s/%v", r.DeploymentID, r.Tier, r.Present))
	}
	if fmt.Sprint(rows) != "[dep-new/warm/false dep-new/init/true dep-old/warm/false dep-old/init/false]" {
		t.Fatalf("rows = %v", rows)
	}
	if init := got[1]; init.ID != "snap-1" || init.FCVersion != "1.7.0" || init.MemBytes != 128<<20 || init.CreatedAt == nil || !init.CreatedAt.Equal(at) {
		t.Fatalf("init row = %+v", init)
	}
}

func TestAppSnapshotsBoundsDeploymentsAndPropagatesErrors(t *testing.T) {
	f := &fakeAppSnapshots{}
	for i := 0; i < api.ObsAppSnapshotDeploymentsMax+3; i++ {
		f.deployments = append(f.deployments, state.Deployment{ID: fmt.Sprintf("dep-%d", i)})
	}
	if _, err := AppSnapshots(context.Background(), f, "app-1"); err != nil || f.reads != api.ObsAppSnapshotDeploymentsMax*len(appSnapshotTiers) {
		t.Fatalf("reads = %d, err = %v", f.reads, err)
	}
	f.err = errors.New("pool exhausted")
	if _, err := AppSnapshots(context.Background(), f, "app-1"); err == nil {
		t.Fatal("a store error must not read as 'no snapshot'")
	}
	none, err := AppSnapshots(context.Background(), &fakeAppSnapshots{}, "app-1")
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("no live deployment: %v %v", none, err)
	}
}
