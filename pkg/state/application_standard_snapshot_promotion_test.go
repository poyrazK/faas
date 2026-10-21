package state

// adr: 595 Actual store transactions with explicitly simulated native facts.

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func standardPausedRestoreFixture(t *testing.T, s standardSnapshotPublicationTestStore) (standardRestoreFixture, runtimeadmission.Receipt) {
	t.Helper()
	f := standardRestoreTestFixture(t, s)
	b, err := s.IssueInstanceApplicationStandardBoot(t.Context(), f.Target.State, f.Binding)
	if err != nil {
		t.Fatal(err)
	}
	parent := standardConsumedRestoreReceipt(b, f)
	parent.Paused, parent.ArtifactConsumption.ConfigHash = true, runtimeadmission.SnapshotLoadCommandHash(true)
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), f.Target.State, StateWarm, parent); err != nil {
		t.Fatal("publish paused measured load", err)
	}
	return f, parent
}

func standardMeasuredPromotionReceipt(t *testing.T, p runtimeadmission.Promotion) runtimeadmission.Receipt {
	t.Helper()
	hash, err := runtimeadmission.HashSnapshotResumeParent(p.Parent)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now().UnixNano()
	r := p.Parent.Clone()
	r.Binding, r.Paused, r.CompletedAtUnixNano = p.Binding, false, clock
	r.SnapshotResumeEvidence = runtimeadmission.SnapshotResumeEvidence{Version: runtimeadmission.SnapshotResumeEvidenceVersion, Binding: p.Binding,
		ParentBinding: p.Parent.Binding, ParentCompletedAtUnixNano: p.Parent.CompletedAtUnixNano, ParentReceiptHash: hash,
		ResumeCommandHash: runtimeadmission.SnapshotResumeCommandHash(), ResumeHookPayloadHash: strings.Repeat("a", 64),
		CommandCompletedAtUnixNano: clock, HostTimeUnixNano: clock, HookCompletedAtUnixNano: clock, CompletedAtUnixNano: clock}
	if err := p.CheckReceipt(r, time.Now()); err != nil {
		t.Fatal("simulated promotion receipt is invalid", err)
	}
	return r
}

func standardMeasuredSnapshotPromotion(t *testing.T, s standardSnapshotPublicationTestStore) {
	t.Helper()
	f, parent := standardPausedRestoreFixture(t, s)
	p, err := s.IssueInstanceApplicationStandardPromotion(t.Context(), promotionTestGrant(t, parent))
	if err != nil {
		t.Fatal("issue fresh measured promotion", err)
	}
	r := standardMeasuredPromotionReceipt(t, p)
	for _, edit := range []func(*runtimeadmission.Receipt){
		func(r *runtimeadmission.Receipt) {
			r.SnapshotResumeEvidence = runtimeadmission.SnapshotResumeEvidence{}
		},
		func(r *runtimeadmission.Receipt) { r.SnapshotResumeEvidence.ParentBinding.Token = uuid.NewString() },
		func(r *runtimeadmission.Receipt) {
			r.ArtifactConsumption.ConfigHash = runtimeadmission.SnapshotLoadCommandHash(false)
		},
		func(r *runtimeadmission.Receipt) { r.ArtifactConsumption.ProcessStart += "1" },
		func(r *runtimeadmission.Receipt) { r.SnapshotConsumption.MappedMemoryBytes-- },
	} {
		bad := r.Clone()
		edit(&bad)
		if _, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), bad); err == nil {
			t.Fatal("substituted measured promotion published")
		}
		actual, err := s.InstanceByID(t.Context(), f.Target.ID)
		if err != nil || actual.State != string(StateWarm) {
			t.Fatal("refusal changed paused residency", err)
		}
	}
	actual, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r)
	if err != nil || actual.State != string(StateRunning) || actual.Netns != parent.Netns {
		t.Fatal("complete measured promotion refused", err)
	}
	again, err := s.PublishInstanceApplicationStandardPromotion(t.Context(), r)
	if err != nil || !again.StartedAt.Equal(actual.StartedAt) {
		t.Fatal("retry changed committed publication", err)
	}
	got, err := s.(InstanceApplicationStandardRuntimeReceiptStore).GetInstanceApplicationStandardRuntimeReceipt(t.Context(), actual.ID)
	if err != nil || !got.Equal(r) {
		t.Fatal("reader selected boot instead of actual promotion", err)
	}
	got.SnapshotResumeEvidence.ParentBinding.Token = uuid.NewString()
	got.ArtifactConsumption.Drives[0].DriveID = "reader edit"
	got, err = s.(InstanceApplicationStandardRuntimeReceiptStore).GetInstanceApplicationStandardRuntimeReceipt(t.Context(), actual.ID)
	if err != nil || !got.Equal(r) {
		t.Fatal("reader changed stored proof", err)
	}
	// A fresh capture must carry the measured serving receipt, including resume.
	token := uuid.NewString()
	prefix := strings.TrimSuffix(SnapshotCaptureMemKey(actual.DeploymentID, "warm", token), "mem")
	g, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), actual.State, ApplicationStandardSnapshotCaptureRequest{Token: token, InstanceID: actual.ID,
		MemoryKey: prefix + "mem", VMStateKey: prefix + "vmstate", PrivateDriveKey: prefix + "drive", FCVersion: f.Grant.FCVersion, Mode: "warm", SourceStartedAtUnixNano: actual.StartedAt.UnixNano()})
	if err != nil || !g.Parent.Equal(r) {
		t.Fatal("capture lost actual resumed serving lineage", err)
	}
	a := standardSnapshotAck(g)
	a.Capture.Memory.Bytes = int64(actual.RAMMB) << 20
	if err := s.PublishApplicationStandardSnapshotCapture(t.Context(), a); err != nil {
		t.Fatal("publish capture of resumed parent", err)
	}
	if !standardSnapshotGet(t, s, g).Acknowledgment.Capture.Parent.Equal(r) || !standardSnapshotGet(t, s, f.Grant).Acknowledgment.Capture.Equal(f.Evidence.Capture) {
		t.Fatal("new capture rewrote historical lineage")
	}
}

func TestMemStandardMeasuredSnapshotPromotion(t *testing.T) {
	standardMeasuredSnapshotPromotion(t, NewMemStore())
}
