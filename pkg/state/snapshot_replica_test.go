package state

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestMemStoreSnapshotReplicaLifecycle(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	region := DefaultLocalityLabel
	second := ComputeNode{
		ID: "node-2", Name: "compute-2", TargetURL: "unix:///run/faas/compute-2.sock",
		AdmissionCeilingMB: 4096, VCPUBudget: 16, Active: true,
		Region: &region,
	}
	if _, err := m.CreateComputeNode(ctx, second); err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	snap, err := m.CreateSnapshot(ctx, Snapshot{
		ID: "snap-1", DeploymentID: "dep-1", FCVersion: "fc-1", StorageKey: SnapMemKey("dep-1"),
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	local, err := m.ComputeNodeByName(ctx, DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("ComputeNodeByName: %v", err)
	}
	if err := m.RecordSnapshotOrigin(ctx, snap.ID, local.ID); err != nil {
		t.Fatalf("RecordSnapshotOrigin: %v", err)
	}
	added, err := m.EnqueueSnapshotReplicasForNode(ctx, second.ID)
	if err != nil {
		t.Fatalf("EnqueueSnapshotReplicasForNode: %v", err)
	}
	if added != 1 {
		t.Fatalf("enqueued = %d, want 1", added)
	}
	job, err := m.ClaimSnapshotReplica(ctx, second.ID)
	if err != nil {
		t.Fatalf("ClaimSnapshotReplica: %v", err)
	}
	if job.SnapshotID != snap.ID || job.VMStateStorageKey != SnapVMStateKey("dep-1") {
		t.Fatalf("job = %+v", job)
	}
	if got, want := job.LayerStorageKeys, []string{"layers/dep-1.ext4"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("job layer keys = %v, want %v", got, want)
	}
	if err := m.MarkSnapshotReplicaReady(ctx, snap.ID, second.ID); err != nil {
		t.Fatalf("MarkSnapshotReplicaReady: %v", err)
	}
	ready, err := m.ReadySnapshotReplicaNodes(ctx, snap.ID)
	if err != nil {
		t.Fatalf("ReadySnapshotReplicaNodes: %v", err)
	}
	if len(ready) != 1 || ready[0] != second.ID {
		t.Fatalf("ready nodes = %v, want [%s]", ready, second.ID)
	}
	if _, err := m.ClaimSnapshotReplica(ctx, second.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second claim err = %v, want ErrNotFound", err)
	}
}

func TestMemStoreSnapshotReplicaPermanentFailureStopsRetry(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	region := DefaultLocalityLabel
	second := ComputeNode{
		ID: "node-permanent", Name: "compute-permanent", TargetURL: "unix:///run/faas/compute-permanent.sock",
		AdmissionCeilingMB: 4096, VCPUBudget: 16, Active: true,
		Region: &region,
	}
	if _, err := m.CreateComputeNode(ctx, second); err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	m.deployments["dep-permanent"] = Deployment{
		ID: "dep-permanent", RootfsKey: "apps/example/dep-permanent.ext4",
	}
	if _, err := m.SetDeploymentSidecarLayer(ctx, DeploymentSidecarLayer{
		DeploymentID: "dep-permanent", SidecarName: "z-sidecar", StorageKey: "apps/example/dep-permanent-z.ext4",
	}); err != nil {
		t.Fatalf("SetDeploymentSidecarLayer: %v", err)
	}
	if _, err := m.SetDeploymentSidecarLayer(ctx, DeploymentSidecarLayer{
		DeploymentID: "dep-permanent", SidecarName: "a-sidecar", StorageKey: "apps/example/dep-permanent-a.ext4",
	}); err != nil {
		t.Fatalf("SetDeploymentSidecarLayer: %v", err)
	}
	snap, err := m.CreateSnapshot(ctx, Snapshot{
		ID: "snap-permanent", DeploymentID: "dep-permanent", FCVersion: "fc-1", StorageKey: SnapMemKey("dep-permanent"),
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	local, err := m.ComputeNodeByName(ctx, DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("ComputeNodeByName: %v", err)
	}
	if err := m.RecordSnapshotOrigin(ctx, snap.ID, local.ID); err != nil {
		t.Fatalf("RecordSnapshotOrigin: %v", err)
	}
	if _, err := m.EnqueueSnapshotReplicasForNode(ctx, second.ID); err != nil {
		t.Fatalf("EnqueueSnapshotReplicasForNode: %v", err)
	}
	job, err := m.ClaimSnapshotReplica(ctx, second.ID)
	if err != nil {
		t.Fatalf("ClaimSnapshotReplica: %v", err)
	}
	wantLayers := []string{
		"apps/example/dep-permanent.ext4",
		"apps/example/dep-permanent-a.ext4",
		"apps/example/dep-permanent-z.ext4",
	}
	if !reflect.DeepEqual(job.LayerStorageKeys, wantLayers) {
		t.Fatalf("job layer keys = %v, want %v", job.LayerStorageKeys, wantLayers)
	}
	if err := m.MarkSnapshotReplicaFailed(ctx, job.SnapshotID, second.ID, errors.New("registry unavailable")); err != nil {
		t.Fatalf("MarkSnapshotReplicaFailed transient: %v", err)
	}
	if err := m.MarkSnapshotReplicaFailed(ctx, job.SnapshotID, second.ID,
		PermanentSnapshotReplicaError(errors.New("immutable layer missing"))); err != nil {
		t.Fatalf("MarkSnapshotReplicaFailed permanent: %v", err)
	}
	if _, err := m.ClaimSnapshotReplica(ctx, second.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim after permanent failure = %v, want ErrNotFound", err)
	}
}

func TestSnapshotReplicaRetryDelayIsCapped(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 0, want: 5 * time.Second},
		{attempt: 1, want: 5 * time.Second},
		{attempt: 2, want: 10 * time.Second},
		{attempt: 7, want: 5 * time.Minute},
		{attempt: 100, want: 5 * time.Minute},
	}
	for _, tt := range tests {
		if got := snapshotReplicaRetryDelay(tt.attempt); got != tt.want {
			t.Errorf("snapshotReplicaRetryDelay(%d) = %s, want %s", tt.attempt, got, tt.want)
		}
	}
}
