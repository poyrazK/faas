package runtimeadmission

// adr: 595 These receipts use simulated acknowledgments, not KVM acceptance.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

func resumedReceiptFixture(t *testing.T) (Promotion, Receipt, time.Time) {
	t.Helper()
	p, e, now := snapshotResumeEvidenceFixture(t)
	r := p.Parent.Clone()
	r.Binding, r.Paused, r.CompletedAtUnixNano, r.SnapshotResumeEvidence = p.Binding, false, e.CompletedAtUnixNano, e
	return p, r, now
}

func TestSnapshotResumeReceiptPreservesExpiredLoadAndFreshPromotion(t *testing.T) {
	p, r, now := resumedReceiptFixture(t)
	if p.CheckReceipt(r, now) != nil || CheckSnapshotParent(r) != nil || r.ArtifactConsumption.ConfigHash != SnapshotLoadCommandHash(true) {
		t.Fatal("complete resumed receipt lost load lineage")
	}
	if !errors.Is(p.Parent.Binding.Validate(now), ErrExpired) {
		t.Fatal("fixture did not expire the original load")
	}
	for _, edit := range []func(*Receipt){
		func(r *Receipt) { r.SnapshotResumeEvidence = SnapshotResumeEvidence{} },
		func(r *Receipt) { r.Paused = true },
		func(r *Receipt) { r.ArtifactConsumption.ConfigHash = SnapshotLoadCommandHash(false) },
		func(r *Receipt) { r.ArtifactConsumption.ProcessStart += "1" },
		func(r *Receipt) { r.ArtifactConsumption.Drives[0].DriveID += "1" },
		func(r *Receipt) { r.SnapshotConsumption.MappedMemoryBytes-- },
		func(r *Receipt) { r.CompletedAtUnixNano++ },
		func(r *Receipt) { r.SnapshotResumeEvidence.ParentBinding.Token = uuid.NewString() },
		func(r *Receipt) { r.SnapshotResumeEvidence.ParentCompletedAtUnixNano++ },
		func(r *Receipt) { r.SnapshotResumeEvidence.ParentReceiptHash = strings.Repeat("0", 64) },
	} {
		changed := r.Clone()
		edit(&changed)
		if changed.Check(changed.Binding, now) == nil || p.CheckReceipt(changed, now) == nil {
			t.Fatal("substituted lineage accepted")
		}
	}
	clone := p.Parent.Clone()
	clone.Binding, clone.Paused, clone.CompletedAtUnixNano = p.Binding, false, r.CompletedAtUnixNano
	clone.ArtifactConsumption.ConfigHash = SnapshotLoadCommandHash(false)
	if clone.Check(p.Binding, now) != nil || p.CheckReceipt(clone, now) == nil {
		t.Fatal("a different serving load was accepted as promotion")
	}
	if !errors.Is(r.Check(r.Binding, time.Unix(0, r.Binding.ExpiresAtUnixNano)), ErrExpired) || CheckSnapshotParent(r) != nil {
		t.Fatal("historical serving identity renewed fresh authority or expired with it")
	}
}

func TestSnapshotResumeReceiptWireOwnsCompleteProofAndOmitsAbsentProof(t *testing.T) {
	p, r, now := resumedReceiptFixture(t)
	raw, err := proto.Marshal(r.ToProto())
	if err != nil {
		t.Fatal(err)
	}
	wire := r.ToProto()
	proto.Reset(wire)
	if err := proto.Unmarshal(raw, wire); err != nil {
		t.Fatal(err)
	}
	got, err := ReceiptFromProto(wire)
	if err != nil || !got.Equal(r) || p.CheckReceipt(got, now) != nil {
		t.Fatal("wire lost resumed receipt", err)
	}
	wire.SnapshotResumeEvidence.ParentBinding.Token = uuid.NewString()
	wire.ArtifactConsumption.Drives[0].DriveId = "caller edit"
	if !got.Equal(r) {
		t.Fatal("wire mutation changed owned receipt")
	}
	wire = r.ToProto()
	wire.SnapshotResumeEvidence.ParentBinding.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
	if _, err := ReceiptFromProto(wire); err == nil {
		t.Fatal("unknown parent lineage crossed receipt boundary")
	}
	parentJSON, err := json.Marshal(p.Parent)
	if err != nil || strings.Contains(string(parentJSON), "snapshot_resume_evidence") || p.Parent.ToProto().SnapshotResumeEvidence != nil {
		t.Fatal("absent proof changed old receipt encoding", err)
	}
	jsonRaw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var copy Receipt
	if err := json.Unmarshal(jsonRaw, &copy); err != nil || !copy.Equal(r) || copy.Check(copy.Binding, now) != nil {
		t.Fatal("JSON lost complete resume lineage", err)
	}
}
