package state

import (
	"context"
	"slices"
	"testing"
	"time"
)

// TestMemStoreListDeploymentsWithStatus mirrors the Pg contract: only the
// requested statuses, only apps that are not deleted, newest first.
func TestMemStoreListDeploymentsWithStatus(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	base := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	m.mu.Lock()
	m.apps["active"] = App{ID: "active", Status: AppActive}
	m.apps["deleted"] = App{ID: "deleted", Status: AppDeleted}
	for i, d := range []Deployment{
		{ID: "live-old", AppID: "active", Status: DeployLive},
		{ID: "superseded", AppID: "active", Status: DeploySuperseded},
		{ID: "snapshotting", AppID: "active", Status: DeploySnapshotting},
		{ID: "live-new", AppID: "active", Status: DeployLive},
		{ID: "deleted-live", AppID: "deleted", Status: DeployLive},
	} {
		d.CreatedAt = base.Add(time.Duration(i) * time.Minute)
		m.deployments[d.ID] = d
	}
	m.mu.Unlock()

	got, err := m.ListDeploymentsWithStatus(context.Background(), DeployLive, DeploySnapshotting)
	if err != nil {
		t.Fatalf("ListDeploymentsWithStatus: %v", err)
	}
	ids := make([]string, 0, len(got))
	for _, d := range got {
		ids = append(ids, d.ID)
	}
	if want := []string{"live-new", "snapshotting", "live-old"}; !slices.Equal(ids, want) {
		t.Fatalf("ListDeploymentsWithStatus = %v, want %v", ids, want)
	}
}
