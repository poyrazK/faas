package state_test

// adr: 435/422 — standards compose with ordinary lifecycle and capacity checks.

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemInstanceApplicationStandardRefusalPreservesExclusiveOwner(t *testing.T) {
	standardRefusalPreservesExclusiveOwner(t, state.NewMemStore())
}

// A failed managed migration cannot revoke the still-running exclusive owner.
func standardRefusalPreservesExclusiveOwner(t *testing.T, s state.StandardRuntimeCaptureTestStore) {
	t.Helper()
	f := state.NewRuntimeCaptureFixture(t, s, true)
	ins, _, r := state.NativeBootTestAttempt(t, s, f, state.StateColdBooting)
	ins, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, state.StateRunning, r)
	if err != nil {
		t.Fatal(err)
	}
	owners := s.(state.ExclusiveWorkStore)
	if _, err := owners.UpsertExclusiveWorkPolicy(t.Context(), (*f.App()).AccountID, exclusivework.Policy{Name: "managed-migration", Scope: "account", MemberAppIDs: []string{(*f.App()).ID}, Contention: "queue", LeaseSeconds: 30, MaxAttemptSeconds: 60, MaxAttempts: 2, RetryAfterSeconds: 2}); err != nil {
		t.Fatal(err)
	}
	op, _, err := owners.AdmitExclusiveOperation(t.Context(), state.ExclusiveAdmission{AccountID: (*f.App()).AccountID, AppID: (*f.App()).ID, PolicyName: "managed-migration", Key: []byte(`"migration"`), Request: []byte(`{"kind":"sync"}`), IdempotencyKey: "managed-migration"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := owners.ClaimExclusiveOperation(t.Context(), (*f.App()).AccountID, op.ID, state.ExclusiveIncarnation(ins))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(t.Context(), (*f.Dep()).ID, "/changed-migration.ext4", "layers/changed-migration.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkInstanceMigrating(t.Context(), ins.ID, ins.NodeID, uuid.NewString()); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
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
	s := state.NewMemStore()
	standardNativeBootCapacity(t, s, func(id string) {
		if (*s.InstanceApplicationStandardBoots())[id].Receipt != nil {
			t.Fatal("capacity refusal retained a native receipt")
		}
	})
}

func TestMemInstanceApplicationStandardPromotionCapacity(t *testing.T) {
	s := state.NewMemStore()
	standardNativePromotionCapacity(t, s, func(id string) {
		if (*s.InstanceApplicationStandardPromotions())[id].Receipt != nil {
			t.Fatal("capacity refusal retained a promotion receipt")
		}
	})
}

func standardNativeCapacityFixture(t *testing.T, s state.StandardRuntimeCaptureTestStore, desired int) (state.RuntimeCaptureFixture, string) {
	t.Helper()
	f := state.NewRuntimeCaptureFixture(t, s, true)
	node := state.ComputeNode{Name: state.DefaultLocalNodeName, TargetURL: "unix:///tmp/native-capacity.sock", VPCPUs: 1, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 4160, VCPUBudget: 16, Lifecycle: state.NodeLifecycleActive, Active: true}
	first, err := s.UpsertComputeNode(t.Context(), node)
	if err != nil || first.ID != (*f.NodeID()) {
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
	manifest := (*f.App()).Manifest
	manifest.ExecutionMode = api.ExecutionModeService
	manifest.ServiceReplicas = &state.ServiceReplicas{Min: 0, Max: 20, Desired: desired}
	ram := 512
	(*f.App()), err = s.UpdateApp(t.Context(), (*f.App()).ID, state.UpdateAppParams{Manifest: &manifest, RAMMB: &ram})
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
func standardNativeBootCapacity(t *testing.T, s state.StandardRuntimeCaptureTestStore, assertNoReceipt func(string)) {
	t.Helper()
	f, _ := standardNativeCapacityFixture(t, s, 8)
	ins, err := s.CreateInstanceWithMode(t.Context(), (*f.App()).ID, (*f.Dep()).ID, string(state.StateWaking), 512, (*f.NodeID()), uuid.NewString(), string(state.InstanceModeService))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, state.RuntimeCaptureTestBinding(t, s, ins))
	if err != nil {
		t.Fatal(err)
	}
	r := runtimeadmission.Receipt{Binding: binding, NativeInputHash: binding.PayloadHash, Netns: "native-capacity", HostIP: "10.100.0.8", LeaseUID: 20008, Paused: true, CompletedAtUnixNano: time.Now().UnixNano()}
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, state.StateWarm, r); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("native publication bypassed protected headroom: %v", err)
	}
	state.AssertNativeBootUnpublished(t, s, ins)
	assertNoReceipt(binding.Token)
	if _, err := s.CreateComputeNode(t.Context(), state.ComputeNode{Name: "capacity-recovery-" + uuid.NewString(), TargetURL: "unix:///tmp/native-capacity.sock", VPCPUs: 1, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 4160, VCPUBudget: 16, Lifecycle: state.NodeLifecycleActive, Active: true}); err != nil {
		t.Fatal(err)
	}
	if actual, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, state.StateWarm, r); err != nil || actual.State != string(state.StateWarm) {
		t.Fatalf("saved native grant did not recover after more capacity: %+v %v", actual, err)
	}
}

func standardNativePromotionCapacity(t *testing.T, s state.StandardRuntimeCaptureTestStore, assertNoReceipt func(string)) {
	t.Helper()
	f, peerID := standardNativeCapacityFixture(t, s, 7)
	ins, err := s.CreateInstanceWithMode(t.Context(), (*f.App()).ID, (*f.Dep()).ID, string(state.StateWaking), 512, (*f.NodeID()), uuid.NewString(), string(state.InstanceModeService))
	if err != nil {
		t.Fatal(err)
	}
	state.PublishRuntimeCaptureTestReceipt(t, s, ins, state.StateWarm, "native-capacity", "10.100.0.8", 20008)
	warm, err := s.InstanceByID(t.Context(), ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.GetInstanceApplicationStandardWarmParent(t.Context(), ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.IssueInstanceApplicationStandardPromotion(t.Context(), state.PromotionTestGrant(t, parent))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetComputeNodeActive(t.Context(), (*f.NodeID()), false); err != nil {
		t.Fatal(err)
	}
	r := parent
	r.Binding, r.Paused, r.CompletedAtUnixNano = p.Binding, false, time.Now().UnixNano()
	if _, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("native promotion bypassed host eligibility: %v", err)
	}
	actual, err := s.InstanceByID(t.Context(), ins.ID)
	if err != nil || actual.State != string(state.StateWarm) || actual.Netns != warm.Netns || actual.StartedAt != warm.StartedAt {
		t.Fatalf("refused promotion changed paused runtime: %+v %v", actual, err)
	}
	assertNoReceipt(p.Binding.Token)
	if err := s.SetComputeNodeActive(t.Context(), (*f.NodeID()), true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetComputeNodeActive(t.Context(), peerID, false); err != nil {
		t.Fatal(err)
	}
	if actual, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r); err != nil || actual.State != string(state.StateRunning) {
		t.Fatalf("declared native recovery on eligible survivor refused: %+v %v", actual, err)
	}
}
