package state

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func TestMemInstanceApplicationStandardNativeBoot(t *testing.T) {
	standardNativeBootLifecycle(t, NewMemStore())
}
func TestMemInstanceApplicationStandardNativeRestart(t *testing.T) {
	standardNativeBootRestart(t, NewMemStore())
}
func TestMemInstanceApplicationStandardNativeStolenState(t *testing.T) {
	standardNativeBootStolenState(t, NewMemStore())
}

func nativeBootTestAttempt(t *testing.T, s standardRuntimeCaptureTestStore, f runtimeCaptureFixture, status State) (Instance, runtimeadmission.Binding, runtimeadmission.Receipt) {
	t.Helper()
	ins, err := s.CreateInstance(t.Context(), f.app.ID, f.dep.ID, string(status), 128, f.nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	binding := runtimeCaptureTestBinding(t, s, ins)
	binding, err = s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, binding)
	if err != nil {
		t.Fatalf("issue simulated native grant: instance=%+v: %v", ins, err)
	}
	receipt := runtimeadmission.Receipt{Binding: binding, NativeInputHash: strings.Repeat("b", 64), Netns: "native-test", HostIP: "10.100.0.8", LeaseUID: 20008, CompletedAtUnixNano: time.Now().UnixNano()}
	return ins, binding, receipt
}

func standardNativeBootLifecycle(t *testing.T, s standardRuntimeCaptureTestStore) {
	t.Helper()
	f := newRuntimeCaptureFixture(t, s, true)
	ins, binding, receipt := nativeBootTestAttempt(t, s, f, StateColdBooting)
	if !runtimeadmission.ValidHash(binding.CapturedInputHash) || binding.EgressRevision <= 1 {
		t.Fatalf("capture omitted native identity or real policy revision: %+v", binding)
	}
	again, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, binding)
	if err != nil || again != binding {
		t.Fatalf("grant retry changed authority: %+v %v", again, err)
	}
	second := binding
	second.Token = uuid.NewString()
	if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, second); !errors.Is(err, ErrConflict) {
		t.Fatalf("one instance acquired multiple initial boot grants: %v", err)
	}
	altered := binding
	altered.PayloadHash = strings.Repeat("c", 64)
	if _, err := s.IssueInstanceApplicationStandardBoot(t.Context(), ins.State, altered); !errors.Is(err, ErrConflict) {
		t.Fatalf("token changed payload: %v", err)
	}
	for _, mutate := range []func(*runtimeadmission.Receipt){
		func(r *runtimeadmission.Receipt) { r.Binding.Incarnation = uuid.NewString() },
		func(r *runtimeadmission.Receipt) { r.Binding.EgressRevision++ },
		func(r *runtimeadmission.Receipt) { r.Binding.CapturedInputHash = strings.Repeat("c", 64) },
		func(r *runtimeadmission.Receipt) { r.CompletedAtUnixNano = r.Binding.ExpiresAtUnixNano },
		func(r *runtimeadmission.Receipt) { r.NativeInputHash = "" },
	} {
		bad := receipt
		mutate(&bad)
		if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, bad); err == nil {
			t.Fatal("different/invalid receipt published")
		}
		assertNativeBootUnpublished(t, s, ins)
	}
	actual, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, receipt)
	if err != nil || actual.State != string(StateRunning) || actual.Netns != receipt.Netns || actual.HostIP != receipt.HostIP || actual.GuestUID != int(receipt.LeaseUID) || actual.NodeID != ins.NodeID || actual.WakeID != ins.WakeID {
		t.Fatalf("atomic publication: %+v %v", actual, err)
	}
	if err := s.SetInstanceRuntime(t.Context(), ins.ID, "different", receipt.HostIP, int(receipt.LeaseUID)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("runtime tuple changed without native receipt: %v", err)
	}
	if err := s.UpdateInstanceState(t.Context(), ins.ID, string(StateWarm)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("paused state changed without native receipt: %v", err)
	}
	if err := s.UpdateInstanceState(t.Context(), ins.ID, string(StateColdBooting)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("published grant reentered boot state: %v", err)
	}
	enrollment, err := s.GetApplicationStandardEnrollment(t.Context(), f.app.OrgID, f.app.ID)
	if err != nil || enrollment.ObservedRevision != 0 {
		t.Fatalf("native boot fabricated policy observation: %+v %v", enrollment, err)
	}
	if err := s.UpdateInstanceStateToTerminal(t.Context(), ins.ID, string(StateStopped), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteInstance(t.Context(), ins.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetInstanceApplicationStandardAdmission(t.Context(), ins.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("history retained after owner erasure: %v", err)
	}
	// Paused publication requires the matching paused receipt.
	warm, _, r := nativeBootTestAttempt(t, s, f, StateWaking)
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), warm.State, StateWarm, r); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("unpaused receipt published warm: %v", err)
	}
	r.Paused = true
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), warm.State, StateWarm, r); err != nil {
		t.Fatal(err)
	}
}

func standardNativeBootRestart(t *testing.T, s standardRuntimeCaptureTestStore) {
	t.Helper()
	f := newRuntimeCaptureFixture(t, s, true)
	ins, _, receipt := nativeBootTestAttempt(t, s, f, StateColdBooting)
	if err := s.RegisterComputeNodeRuntimeIdentity(t.Context(), runtimeadmission.Identity{ProtocolVersion: runtimeadmission.ProtocolVersion, NodeID: f.nodeID, Incarnation: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, receipt); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("old process receipt published: %v", err)
	}
	assertNativeBootUnpublished(t, s, ins)
}

func standardNativeBootStolenState(t *testing.T, s standardRuntimeCaptureTestStore) {
	t.Helper()
	f := newRuntimeCaptureFixture(t, s, true)
	ins, _, receipt := nativeBootTestAttempt(t, s, f, StateColdBooting)
	if err := s.UpdateInstanceStateToTerminal(t.Context(), ins.ID, string(StateFailed), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateRunning, receipt); !errors.Is(err, ErrConflict) {
		t.Fatalf("stolen state published: %v", err)
	}
	actual, err := s.InstanceByID(t.Context(), ins.ID)
	if err != nil || actual.State != string(StateFailed) || actual.Netns != "" || actual.HostIP != "" {
		t.Fatalf("stolen state left partial tuple: %+v %v", actual, err)
	}
}

func assertNativeBootUnpublished(t *testing.T, s standardRuntimeCaptureTestStore, ins Instance) {
	t.Helper()
	actual, err := s.InstanceByID(t.Context(), ins.ID)
	if err != nil || actual.State != ins.State || actual.Netns != "" || actual.HostIP != "" || actual.GuestUID != 0 {
		t.Fatalf("refused receipt changed runtime: %+v %v", actual, err)
	}
}
