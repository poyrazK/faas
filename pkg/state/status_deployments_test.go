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

// TestMemStoreListDeploymentsWithStatusForNode mirrors the Pg contract for
// O32's node-scoped listing.
func TestMemStoreListDeploymentsWithStatusForNode(t *testing.T) {
	t.Parallel()
	m := NewMemStore()
	m.mu.Lock()
	m.computeNodes["mine"] = ComputeNode{ID: "mine", Active: true}
	m.computeNodes["other"] = ComputeNode{ID: "other", Active: true}
	m.computeNodes["retired"] = ComputeNode{ID: "retired", Active: false}
	for _, node := range []string{"mine", "other", "retired"} {
		m.apps[node] = App{ID: node, Status: AppActive, NodeID: node}
		m.deployments[node] = Deployment{ID: node, AppID: node, Status: DeployLive}
	}
	m.mu.Unlock()
	got, err := m.ListDeploymentsWithStatusForNode(context.Background(), "mine", DeployLive)
	if err != nil {
		t.Fatalf("ListDeploymentsWithStatusForNode: %v", err)
	}
	ids := make([]string, 0, len(got))
	for _, d := range got {
		ids = append(ids, d.ID)
	}
	slices.Sort(ids)
	if want := []string{"mine", "retired"}; !slices.Equal(ids, want) {
		t.Fatalf("ListDeploymentsWithStatusForNode = %v, want %v", ids, want)
	}
}
