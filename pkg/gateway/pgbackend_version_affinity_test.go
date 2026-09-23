package gateway_test

// adr: 084 — traffic-split cohorts must be stable across gateway replicas.

import (
	"context"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
)

func newVersionAffinityBackend(t *testing.T, store *fakeWeightsStore) *gateway.PGBackend {
	t.Helper()
	b := gateway.NewPGBackend(&fakeRouter{byID: map[string]gateway.App{}}, gateway.NewFakeScheduler("node-a"), nil).WithStore(store)
	if err := b.RefreshDeploymentWeights(context.Background(), "app-1"); err != nil {
		t.Fatalf("RefreshDeploymentWeights: %v", err)
	}
	return b
}

func versionKeyForDeployment(t *testing.T, b *gateway.PGBackend, deploymentID string) string {
	t.Helper()
	for i := 0; i < 10_000; i++ {
		key := fmt.Sprintf("user-%d", i)
		if got, ok := b.AffinityDeployment("app-1", key); ok && got == deploymentID {
			return key
		}
	}
	t.Fatalf("could not find a key for %s", deploymentID)
	return ""
}

func TestPGBackendVersionAffinityIsStableAcrossGatewayReplicas(t *testing.T) {
	store := &fakeWeightsStore{rows: map[string][]gateway.DeploymentWeightsRow{
		"app-1": {
			{ID: "dep-candidate", TrafficPercent: 25},
			{ID: "dep-stable", TrafficPercent: 75},
		},
	}}
	first := newVersionAffinityBackend(t, store)
	second := newVersionAffinityBackend(t, store)

	candidate := 0
	for i := 0; i < 1_000; i++ {
		key := fmt.Sprintf("account-%d", i)
		want, ok := first.AffinityDeployment("app-1", key)
		if !ok {
			t.Fatalf("first AffinityDeployment(%q) was unresolved", key)
		}
		for attempt := 0; attempt < 5; attempt++ {
			if got, ok := first.AffinityDeployment("app-1", key); !ok || got != want {
				t.Fatalf("repeat mapping for %q = %q/%v, want %q/true", key, got, ok, want)
			}
		}
		if got, ok := second.AffinityDeployment("app-1", key); !ok || got != want {
			t.Fatalf("second replica mapping for %q = %q/%v, want %q/true", key, got, ok, want)
		}
		if want == "dep-candidate" {
			candidate++
		}
	}
	if candidate < 200 || candidate > 300 {
		t.Fatalf("candidate cohort = %d/1000, want approximately 25%%", candidate)
	}
}

func TestPGBackendVersionAffinityCohortExpandsMonotonically(t *testing.T) {
	store := &fakeWeightsStore{rows: map[string][]gateway.DeploymentWeightsRow{
		"app-1": {
			{ID: "dep-candidate", TrafficPercent: 10},
			{ID: "dep-stable", TrafficPercent: 90},
		},
	}}
	b := newVersionAffinityBackend(t, store)

	before := make(map[string]string, 1_000)
	beforeCandidates := 0
	for i := 0; i < 1_000; i++ {
		key := fmt.Sprintf("user-%d", i)
		deploymentID, _ := b.AffinityDeployment("app-1", key)
		before[key] = deploymentID
		if deploymentID == "dep-candidate" {
			beforeCandidates++
		}
	}

	store.rows["app-1"] = []gateway.DeploymentWeightsRow{
		{ID: "dep-candidate", TrafficPercent: 50},
		{ID: "dep-stable", TrafficPercent: 50},
	}
	if err := b.RefreshDeploymentWeights(context.Background(), "app-1"); err != nil {
		t.Fatalf("expand candidate: %v", err)
	}
	afterCandidates := 0
	for key, oldDeploymentID := range before {
		deploymentID, _ := b.AffinityDeployment("app-1", key)
		if oldDeploymentID == "dep-candidate" && deploymentID != "dep-candidate" {
			t.Fatalf("existing candidate cohort member %q moved back to %q", key, deploymentID)
		}
		if deploymentID == "dep-candidate" {
			afterCandidates++
		}
	}
	if afterCandidates <= beforeCandidates {
		t.Fatalf("candidate cohort did not expand: before=%d after=%d", beforeCandidates, afterCandidates)
	}
}

func TestPGBackendVersionAffinityKeepsSessionInsideSelectedDeployment(t *testing.T) {
	store := &fakeWeightsStore{rows: map[string][]gateway.DeploymentWeightsRow{
		"app-1": {
			{ID: "dep-candidate", TrafficPercent: 50},
			{ID: "dep-stable", TrafficPercent: 50},
		},
	}}
	b := newVersionAffinityBackend(t, store)
	b.RecordTarget("app-1", gateway.Target{AppID: "app-1", DeploymentID: "dep-candidate", InstanceID: "candidate-1", NodeID: "node-a"})
	b.RecordTarget("app-1", gateway.Target{AppID: "app-1", DeploymentID: "dep-candidate", InstanceID: "candidate-2", NodeID: "node-b"})
	b.RecordTarget("app-1", gateway.Target{AppID: "app-1", DeploymentID: "dep-stable", InstanceID: "stable-1", NodeID: "node-c"})
	key := versionKeyForDeployment(t, b, "dep-candidate")

	if got := b.PickForVersionKey("app-1", key, "stable-1"); !got.OK || got.Target.DeploymentID != "dep-candidate" || got.Target.InstanceID == "stable-1" {
		t.Fatalf("cross-deployment session preference escaped cohort: %+v", got)
	}
	if got := b.PickForVersionKey("app-1", key, "candidate-2"); !got.OK || got.Target.InstanceID != "candidate-2" {
		t.Fatalf("same-deployment session preference was not honored: %+v", got)
	}
}

func TestPGBackendVersionAffinitySignalsColdSelectedBucket(t *testing.T) {
	store := &fakeWeightsStore{rows: map[string][]gateway.DeploymentWeightsRow{
		"app-1": {
			{ID: "dep-candidate", TrafficPercent: 50},
			{ID: "dep-stable", TrafficPercent: 50},
		},
	}}
	b := newVersionAffinityBackend(t, store)
	b.RecordTarget("app-1", gateway.Target{AppID: "app-1", DeploymentID: "dep-stable", InstanceID: "stable-1", NodeID: "node-a"})
	key := versionKeyForDeployment(t, b, "dep-candidate")

	got := b.PickForVersionKey("app-1", key, "")
	if !got.OK || got.Picked != "dep-candidate" || got.ColdBucket != "dep-candidate" || got.Target.DeploymentID != "dep-stable" {
		t.Fatalf("cold keyed pick = %+v, want stable fallback plus candidate wake signal", got)
	}
}
