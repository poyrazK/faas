package state

// adr: 435/422 — standards compose with ordinary lifecycle and capacity checks.

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func TestMemInstanceApplicationStandardRefusalPreservesExclusiveOwner(t *testing.T) {
	standardRefusalPreservesExclusiveOwner(t, NewMemStore())
}

// A failed managed migration cannot revoke the still-running exclusive owner.
func standardRefusalPreservesExclusiveOwner(t *testing.T, s standardRuntimeCaptureTestStore) {
	t.Helper()
	f := newRuntimeCaptureFixture(t, s, true)
	ins, _, r := nativeBootTestAttempt(t, s, f, StateColdBooting)
	ins, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, r)
	if err != nil {
		t.Fatal(err)
	}
	owners := s.(ExclusiveWorkStore)
	if _, err := owners.UpsertExclusiveWorkPolicy(t.Context(), f.app.AccountID, exclusivework.Policy{Name: "managed-migration", Scope: "account", MemberAppIDs: []string{f.app.ID}, Contention: "queue", LeaseSeconds: 30, MaxAttemptSeconds: 60, MaxAttempts: 2, RetryAfterSeconds: 2}); err != nil {
		t.Fatal(err)
	}
	op, _, err := owners.AdmitExclusiveOperation(t.Context(), ExclusiveAdmission{AccountID: f.app.AccountID, AppID: f.app.ID, PolicyName: "managed-migration", Key: []byte(`"migration"`), Request: []byte(`{"kind":"sync"}`), IdempotencyKey: "managed-migration"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := owners.ClaimExclusiveOperation(t.Context(), f.app.AccountID, op.ID, ExclusiveIncarnation(ins))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(t.Context(), f.dep.ID, "/changed-migration.ext4", "layers/changed-migration.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkInstanceMigrating(t.Context(), ins.ID, ins.NodeID, uuid.NewString()); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("stale migration did not refuse: %v", err)
	}
	actual, err := s.InstanceByID(t.Context(), ins.ID)
	if err != nil || actual.State != ins.State || actual.LeaseToken != ins.LeaseToken || actual.MigrationStartedAt != nil {
		t.Fatalf("refused migration changed runtime: %+v %v", actual, err)
	}
	if current, err := owners.ValidateExclusiveOperation(t.Context(), claim); err != nil || current.State != "running" || !current.QuotaReserved {
		t.Fatalf("refused migration revoked the serving owner: %+v %v", current, err)
	}
}

func TestMemInstanceApplicationStandardBootCapacity(t *testing.T) {
	s := NewMemStore()
	standardNativeBootCapacity(t, s, func(id string) {
		if s.instanceApplicationStandardBoots[id].Receipt != nil {
			t.Fatal("capacity refusal retained a native receipt")
		}
	})
}

func TestMemInstanceApplicationStandardPromotionCapacity(t *testing.T) {
	s := NewMemStore()
	standardNativePromotionCapacity(t, s, func(id string) {
		if s.instanceApplicationStandardPromotions[id].Receipt != nil {
			t.Fatal("capacity refusal retained a promotion receipt")
		}
	})
}

func standardNativeCapacityFixture(t *testing.T, s standardRuntimeCaptureTestStore, desired int) (runtimeCaptureFixture, string) {
	t.Helper()
	f := newRuntimeCaptureFixture(t, s, true)
	node := ComputeNode{Name: DefaultLocalNodeName, TargetURL: "unix:///tmp/native-capacity.sock", VPCPUs: 1, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 4160, VCPUBudget: 16, Lifecycle: NodeLifecycleActive, Active: true}
	first, err := s.UpsertComputeNode(t.Context(), node)
	if err != nil || first.ID != f.nodeID {
		t.Fatalf("configure captured host: %+v %v", first, err)
	}
	node.Name = "native-capacity-" + uuid.NewString()
	peer, err := s.CreateComputeNode(t.Context(), node)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{first.ID, peer.ID} {
		if err := s.HeartbeatComputeNode(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	manifest := f.app.Manifest
	manifest.ExecutionMode = api.ExecutionModeService
	manifest.ServiceReplicas = &ServiceReplicas{Min: 0, Max: 20, Desired: desired}
	ram := 512
	f.app, err = s.UpdateApp(t.Context(), f.app.ID, UpdateAppParams{Manifest: &manifest, RAMMB: &ram})
	if err != nil {
		t.Fatal(err)
	}
	if report, err := s.SetServiceCapacityProtection(t.Context(), true); err != nil || report.State != "protected" {
		t.Fatalf("enable native capacity fixture: %+v %v", report, err)
	}
	return f, peer.ID
}

// ADR-435/422: a native receipt cannot consume protected recovery headroom.
// This simulates the native consumer and does not claim a guest boot or policy ACK.
func standardNativeBootCapacity(t *testing.T, s standardRuntimeCaptureTestStore, assertNoReceipt func(string)) {
	t.Helper()
	f, _ := standardNativeCapacityFixture(t, s, 8)
	ins, err := s.CreateInstanceWithMode(t.Context(), f.app.ID, f.dep.ID, string(StateWaking), 512, f.nodeID, uuid.NewString(), string(InstanceModeService))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, runtimeCaptureTestBinding(t, s, ins))
	if err != nil {
		t.Fatal(err)
	}
	r := runtimeadmission.Receipt{Binding: binding, NativeInputHash: binding.PayloadHash, Netns: "native-capacity", HostIP: "10.100.0.8", LeaseUID: 20008, Paused: true, CompletedAtUnixNano: time.Now().UnixNano()}
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateWarm, r); ServiceCapacityProblem(err) == nil {
		t.Fatalf("native publication bypassed protected headroom: %v", err)
	}
	assertNativeBootUnpublished(t, s, ins)
	assertNoReceipt(binding.Token)
	if _, err := s.CreateComputeNode(t.Context(), ComputeNode{Name: "capacity-recovery-" + uuid.NewString(), TargetURL: "unix:///tmp/native-capacity.sock", VPCPUs: 1, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 4160, VCPUBudget: 16, Lifecycle: NodeLifecycleActive, Active: true}); err != nil {
		t.Fatal(err)
	}
	if actual, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateWarm, r); err != nil || actual.State != string(StateWarm) {
		t.Fatalf("saved native grant did not recover after more capacity: %+v %v", actual, err)
	}
}

func standardNativePromotionCapacity(t *testing.T, s standardRuntimeCaptureTestStore, assertNoReceipt func(string)) {
	t.Helper()
	f, peerID := standardNativeCapacityFixture(t, s, 7)
	ins, err := s.CreateInstanceWithMode(t.Context(), f.app.ID, f.dep.ID, string(StateWaking), 512, f.nodeID, uuid.NewString(), string(InstanceModeService))
	if err != nil {
		t.Fatal(err)
	}
	publishRuntimeCaptureTestReceipt(t, s, ins, StateWarm, "native-capacity", "10.100.0.8", 20008)
	warm, err := s.InstanceByID(t.Context(), ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.GetInstanceApplicationStandardWarmParent(t.Context(), ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.IssueInstanceApplicationStandardPromotion(t.Context(), promotionTestGrant(t, parent))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetComputeNodeActive(t.Context(), f.nodeID, false); err != nil {
		t.Fatal(err)
	}
	r := parent
	r.Binding, r.Paused, r.CompletedAtUnixNano = p.Binding, false, time.Now().UnixNano()
	if _, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r); ServiceCapacityProblem(err) == nil {
		t.Fatalf("native promotion bypassed host eligibility: %v", err)
	}
	actual, err := s.InstanceByID(t.Context(), ins.ID)
	if err != nil || actual.State != string(StateWarm) || actual.Netns != warm.Netns || actual.StartedAt != warm.StartedAt {
		t.Fatalf("refused promotion changed paused runtime: %+v %v", actual, err)
	}
	assertNoReceipt(p.Binding.Token)
	if err := s.SetComputeNodeActive(t.Context(), f.nodeID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetComputeNodeActive(t.Context(), peerID, false); err != nil {
		t.Fatal(err)
	}
	if actual, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r); err != nil || actual.State != string(StateRunning) {
		t.Fatalf("declared native recovery on eligible survivor refused: %+v %v", actual, err)
	}
}
