// adr: 167

package gateway

import (
	"context"
	"errors"
	"testing"
)

type serviceDiscoveryWeightStore struct{ rows []DeploymentWeightsRow }

func (s serviceDiscoveryWeightStore) LiveDeployments(context.Context, string) ([]DeploymentWeightsRow, error) {
	return s.rows, nil
}

func TestServiceProxyExcludesRetainedRevisionWithoutPin(t *testing.T) {
	b := NewPGBackend(nil, nil, nil).WithStore(serviceDiscoveryWeightStore{rows: []DeploymentWeightsRow{
		{ID: "old", TrafficPercent: 0}, {ID: "current", TrafficPercent: 100},
	}})
	b.RecordTarget("app-1", Target{NodeID: "node-a", InstanceID: "old-instance", DeploymentID: "old", Port: 8080})
	b.RecordTarget("app-1", Target{NodeID: "node-a", InstanceID: "current-instance", DeploymentID: "current", Port: 8080})
	snapshot, err := b.ServiceEndpoints(context.Background(), "app-1")
	if err != nil {
		t.Fatal(err)
	}
	ordinary := serviceEndpointsForDeployment(snapshot.Endpoints, "")
	if len(ordinary) != 1 || ordinary[0].DeploymentID != "current" {
		t.Fatalf("ordinary endpoints = %+v", ordinary)
	}
	pinned := serviceEndpointsForDeployment(snapshot.Endpoints, "old")
	if len(pinned) != 1 || pinned[0].InstanceID != "old-instance" {
		t.Fatalf("pinned endpoints = %+v", pinned)
	}
}

func TestPGBackendServiceEndpointsDeterministicAndEffectivePort(t *testing.T) {
	b := NewPGBackend(nil, nil, nil)
	b.RecordTarget("app-1", Target{
		NodeID: "node-b", InstanceID: "instance-2", DeploymentID: "dep-b", Port: 9090,
		Region: "eu-west", CommitSHA: "sha-b", DeploymentTag: "stable",
		DeploymentCreatedAt: "2026-09-22T12:00:00Z", ImageDigest: "sha256:b",
	})
	b.RecordTarget("app-1", Target{NodeID: "node-a", InstanceID: "instance-3", DeploymentID: "dep-a"})
	b.RecordTarget("app-1", Target{NodeID: "node-a", InstanceID: "instance-1", DeploymentID: "dep-a", Port: 8081})

	got, err := b.ServiceEndpoints(context.Background(), "app-1")
	if err != nil {
		t.Fatalf("ServiceEndpoints: %v", err)
	}
	want := []ServiceEndpoint{
		{InstanceID: "instance-1", NodeID: "node-a", DeploymentID: "dep-a", Port: 8081},
		{InstanceID: "instance-3", NodeID: "node-a", DeploymentID: "dep-a", Port: 8080},
		{
			InstanceID: "instance-2", NodeID: "node-b", DeploymentID: "dep-b",
			Region: "eu-west", CommitSHA: "sha-b", DeploymentTag: "stable",
			DeploymentCreatedAt: "2026-09-22T12:00:00Z", ImageDigest: "sha256:b", Port: 9090,
		},
	}
	if len(got.Endpoints) != len(want) {
		t.Fatalf("endpoint count = %d, want %d: %+v", len(got.Endpoints), len(want), got.Endpoints)
	}
	for i := range want {
		if got.Endpoints[i] != want[i] {
			t.Errorf("endpoint[%d] = %+v, want %+v", i, got.Endpoints[i], want[i])
		}
	}

	// The projection is a fresh snapshot and must not expose picker storage.
	got.Endpoints[0].NodeID = "mutated"
	again, err := b.ServiceEndpoints(context.Background(), "app-1")
	if err != nil {
		t.Fatalf("ServiceEndpoints second read: %v", err)
	}
	if again.Endpoints[0].NodeID != "node-a" {
		t.Fatalf("snapshot mutation leaked into picker: %+v", again.Endpoints[0])
	}
}

func TestPGBackendServiceEndpointsDedupeAndEvict(t *testing.T) {
	b := NewPGBackend(nil, nil, nil)
	b.RecordTarget("app-1", Target{NodeID: "node-a", InstanceID: "instance-1", DeploymentID: "dep-a"})
	// A replay cannot create a duplicate endpoint; RecordTarget replaces the
	// target in place and the projection also defends across buckets.
	b.RecordTarget("app-1", Target{NodeID: "node-a", InstanceID: "instance-1", DeploymentID: "dep-a", Port: 9000})
	// A transition can briefly expose one instance in two deployment buckets.
	b.tgtMu.Lock()
	b.recordTargetLocked("app-1", Target{NodeID: "node-a", InstanceID: "instance-1", DeploymentID: "dep-b", Port: 7000})
	b.tgtMu.Unlock()
	b.RecordTarget("app-1", Target{NodeID: "node-b", InstanceID: "instance-2", DeploymentID: "dep-b"})

	before, err := b.ServiceEndpoints(context.Background(), "app-1")
	if err != nil {
		t.Fatalf("ServiceEndpoints before eviction: %v", err)
	}
	if len(before.Endpoints) != 2 || before.Endpoints[0].Port != 9000 {
		t.Fatalf("before eviction = %+v, want two endpoints with replayed port", before.Endpoints)
	}

	b.EvictInstance("app-1", "instance-1")
	after, err := b.ServiceEndpoints(context.Background(), "app-1")
	if err != nil {
		t.Fatalf("ServiceEndpoints after eviction: %v", err)
	}
	if len(after.Endpoints) != 1 || after.Endpoints[0].InstanceID != "instance-2" {
		t.Fatalf("after eviction = %+v, want only instance-2", after.Endpoints)
	}
}

func TestPGBackendServiceEndpointsReconcilesEmptyCache(t *testing.T) {
	b := NewPGBackend(nil, nil, nil).WithLiveTargetLoader(func(context.Context, string) ([]Target, error) {
		return []Target{{NodeID: "node-remote", InstanceID: "instance-remote", DeploymentID: "dep-1", Port: 8088}}, nil
	})
	got, err := b.ServiceEndpoints(context.Background(), "app-1")
	if err != nil {
		t.Fatalf("ServiceEndpoints: %v", err)
	}
	if len(got.Endpoints) != 1 || got.Endpoints[0].NodeID != "node-remote" {
		t.Fatalf("reconciled endpoints = %+v, want remote target", got.Endpoints)
	}
}

func TestPGBackendServiceEndpointsPropagatesReconcileError(t *testing.T) {
	wantErr := errors.New("store unavailable")
	b := NewPGBackend(nil, nil, nil).WithLiveTargetLoader(func(context.Context, string) ([]Target, error) {
		return nil, wantErr
	})
	_, err := b.ServiceEndpoints(context.Background(), "app-1")
	if !errors.Is(err, wantErr) {
		t.Fatalf("ServiceEndpoints error = %v, want %v", err, wantErr)
	}
}
