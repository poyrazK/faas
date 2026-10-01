package state

import (
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"testing"
	"time"
)

func promotionTestGrant(t *testing.T, parent runtimeadmission.Receipt) runtimeadmission.Promotion {
	t.Helper()
	p := runtimeadmission.Promotion{Binding: parent.Binding, Parent: parent}
	now := time.Now()
	p.Binding.Token, p.Binding.IssuedAtUnixNano, p.Binding.ExpiresAtUnixNano = uuid.NewString(), now.UnixNano(), now.Add(time.Minute).UnixNano()
	p.Binding.PayloadHash, _ = runtimeadmission.HashPromotionPayload(p.ToProto())
	return p
}

func promotionTestParent(t *testing.T, s standardRuntimeCaptureTestStore) (runtimeCaptureFixture, Instance, runtimeadmission.Receipt) {
	t.Helper()
	f := newRuntimeCaptureFixture(t, s, true)
	ins, _, r := nativeBootTestAttempt(t, s, f, StateWaking)
	r.Paused = true
	warm, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, StateWarm, r)
	if err != nil {
		t.Fatal(err)
	}
	return f, warm, r
}

func TestMemInstanceApplicationStandardPromotion(t *testing.T) {
	standardPromotionLifecycle(t, NewMemStore())
}
func TestMemInstanceApplicationStandardPromotionRestart(t *testing.T) {
	standardPromotionRestart(t, NewMemStore())
}

func TestMemInstanceApplicationStandardPausedHistoryCannotRecreateResidency(t *testing.T) {
	standardPausedHistoryCannotRecreateResidency(t, NewMemStore())
}

func TestMemInstanceApplicationStandardPromotionInputChanged(t *testing.T) {
	standardPromotionInputChanged(t, NewMemStore())
}

func standardPromotionInputChanged(t *testing.T, s standardRuntimeCaptureTestStore) {
	t.Helper()
	f, warm, parent := promotionTestParent(t, s)
	p, err := s.IssueInstanceApplicationStandardPromotion(t.Context(), promotionTestGrant(t, parent))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(t.Context(), f.dep.ID, "/changed-promotion.ext4", "layers/changed-promotion.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	r := parent
	r.Binding, r.Paused, r.CompletedAtUnixNano = p.Binding, false, time.Now().UnixNano()
	if _, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("changed runtime inputs promoted: %v", err)
	}
	actual, _ := s.InstanceByID(t.Context(), warm.ID)
	if actual.State != string(StateWarm) || actual.Netns != warm.Netns {
		t.Fatalf("stale publication changed warm lease: %+v", actual)
	}
}

func standardPausedHistoryCannotRecreateResidency(t *testing.T, s standardRuntimeCaptureTestStore) {
	t.Helper()
	_, warm, _ := promotionTestParent(t, s)
	if err := s.UpdateInstanceState(t.Context(), warm.ID, string(StateParked)); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateInstanceState(t.Context(), warm.ID, string(StateWarm)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("historical receipt recreated residency: %v", err)
	}
}

func standardPromotionLifecycle(t *testing.T, s standardRuntimeCaptureTestStore) {
	t.Helper()
	f, warm, parent := promotionTestParent(t, s)
	loaded, err := s.GetInstanceApplicationStandardWarmParent(t.Context(), warm.ID)
	if err != nil || loaded != parent {
		t.Fatalf("paused lease: %+v %v", loaded, err)
	}
	if _, err := s.PublishInstanceRuntime(t.Context(), warm.ID, warm.State, warm.Netns, warm.HostIP, warm.GuestUID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("historical receipt authorized resume: %v", err)
	}
	p, err := s.IssueInstanceApplicationStandardPromotion(t.Context(), promotionTestGrant(t, parent))
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.IssueInstanceApplicationStandardPromotion(t.Context(), p)
	if err != nil || again != p {
		t.Fatalf("saved grant retry changed: %+v %v", again, err)
	}
	if _, err := s.IssueInstanceApplicationStandardPromotion(t.Context(), promotionTestGrant(t, parent)); !errors.Is(err, ErrConflict) {
		t.Fatalf("second promotion grant: %v", err)
	}
	r := parent
	r.Binding, r.Paused, r.CompletedAtUnixNano = p.Binding, false, time.Now().UnixNano()
	bad := r
	bad.LeaseUID++
	if _, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), bad); err == nil {
		t.Fatal("different lease published")
	}
	actual, err := s.InstanceByID(t.Context(), warm.ID)
	if err != nil || actual.State != string(StateWarm) || actual.Netns != warm.Netns {
		t.Fatalf("refusal changed warm lease: %+v %v", actual, err)
	}
	actual, err = s.PublishInstanceApplicationStandardPromotion(t.Context(), r)
	if err != nil || actual.State != string(StateRunning) || actual.Netns != warm.Netns || actual.GuestUID != warm.GuestUID {
		t.Fatalf("promotion publication: %+v %v", actual, err)
	}
	againIns, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r)
	if err != nil || againIns.StartedAt != actual.StartedAt {
		t.Fatalf("publication retry changed serving instance: %+v %v", againIns, err)
	}
	bad = r
	bad.CompletedAtUnixNano++
	if _, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), bad); !errors.Is(err, ErrConflict) {
		t.Fatalf("different publication retry: %v", err)
	}
	if err := s.UpdateInstanceState(t.Context(), warm.ID, string(StateWarm)); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("promotion returned to paused parent: %v", err)
	}
	enrollment, _ := s.GetApplicationStandardEnrollment(t.Context(), f.app.OrgID, f.app.ID)
	if enrollment.ObservedRevision != 0 {
		t.Fatal("resume fabricated consumer observation")
	}
	if err := s.UpdateInstanceStateToTerminal(t.Context(), warm.ID, string(StateStopped), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteInstance(t.Context(), warm.ID); err != nil {
		t.Fatal(err)
	}
}

func standardPromotionRestart(t *testing.T, s standardRuntimeCaptureTestStore) {
	t.Helper()
	f, warm, parent := promotionTestParent(t, s)
	p, err := s.IssueInstanceApplicationStandardPromotion(t.Context(), promotionTestGrant(t, parent))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterComputeNodeRuntimeIdentity(t.Context(), runtimeadmission.Identity{ProtocolVersion: runtimeadmission.ProtocolVersion, NodeID: f.nodeID, Incarnation: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	r := parent
	r.Binding, r.Paused, r.CompletedAtUnixNano = p.Binding, false, time.Now().UnixNano()
	if _, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("old process promoted: %v", err)
	}
	if _, err := s.GetInstanceApplicationStandardWarmParent(t.Context(), warm.ID); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("old resident lease still eligible: %v", err)
	}
}
