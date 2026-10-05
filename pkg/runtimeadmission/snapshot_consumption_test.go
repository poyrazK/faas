package runtimeadmission

// adr: 595
// Native facts here are simulations; catalog and wire validation are real.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

func snapshotConsumedReceiptFixture(t *testing.T) (Receipt, SnapshotRestoreEvidence) {
	t.Helper()
	e, b, _ := snapshotRestoreEvidenceFixture(t)
	r := e.Capture.Parent.Clone()
	r.Binding, r.Method, r.CompletedAtUnixNano = b, vmmdpb.WakeMethod_WAKE_RESTORE, time.Now().UnixNano()
	r.ArtifactConsumption.ConfigHash = SnapshotLoadCommandHash(false)
	r.ArtifactConsumption.ProcessPID, r.ArtifactConsumption.ProcessStart = 52, "202"
	r.SnapshotConsumption = SnapshotConsumption{Version: SnapshotRestoreVersion, CaptureToken: e.CaptureToken,
		EvidenceHash: b.SnapshotEvidenceHash, Memory: e.Capture.Memory, VMState: e.Capture.VMState, PrivateDrive: e.Capture.PrivateDrive, MappedMemoryBytes: e.Capture.Memory.Bytes}
	return r, e
}

func TestSnapshotConsumptionReceiptRequiresCompleteLoadAndCatalog(t *testing.T) {
	r, e := snapshotConsumedReceiptFixture(t)
	if err := r.Check(r.Binding, time.Now()); err != nil {
		t.Fatal("complete receipt refused", err)
	}
	if err := r.SnapshotConsumption.CheckEvidence(r.Binding, r.ArtifactConsumption, false, e, time.Now()); err != nil {
		t.Fatal("catalog refused", err)
	}
	for _, tc := range []struct {
		name string
		edit func(*Receipt)
	}{
		{"missing mapping proof", func(r *Receipt) { r.SnapshotConsumption = SnapshotConsumption{} }},
		{"missing drives", func(r *Receipt) { r.ArtifactConsumption.Drives = nil }},
		{"cold command hash", func(r *Receipt) { r.ArtifactConsumption.ConfigHash = strings.Repeat("a", 64) }},
		{"paused command hash", func(r *Receipt) { r.ArtifactConsumption.ConfigHash = SnapshotLoadCommandHash(true) }},
		{"wrong token", func(r *Receipt) { r.SnapshotConsumption.CaptureToken = uuid.NewString() }},
		{"wrong evidence", func(r *Receipt) { r.SnapshotConsumption.EvidenceHash = strings.Repeat("b", 64) }},
		{"partial memory", func(r *Receipt) { r.SnapshotConsumption.MappedMemoryBytes-- }},
		{"invalid digest", func(r *Receipt) { r.SnapshotConsumption.Memory.Digest = "" }},
		{"different capture keys", func(r *Receipt) { r.SnapshotConsumption.VMState.StorageKey += "other" }},
		{"unknown version", func(r *Receipt) { r.SnapshotConsumption.Version++ }},
		{"wrong private size", func(r *Receipt) { r.SnapshotConsumption.PrivateDrive.Bytes++ }},
		{"cold method with snapshot proof", func(r *Receipt) { r.Method = vmmdpb.WakeMethod_WAKE_COLD_BOOT }},
		{"paused receipt remains unavailable", func(r *Receipt) { r.Paused = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := r.Clone()
			tc.edit(&changed)
			if changed.Check(changed.Binding, time.Now()) == nil {
				t.Fatal("incomplete receipt accepted")
			}
		})
	}
	changed := r.Clone()
	changed.SnapshotConsumption.Memory.Digest = "sha256:" + strings.Repeat("0", 64)
	if err := changed.Check(changed.Binding, time.Now()); err != nil {
		t.Fatal("fixture is not structurally valid", err)
	}
	if changed.SnapshotConsumption.CheckEvidence(changed.Binding, changed.ArtifactConsumption, false, e, time.Now()) == nil {
		t.Fatal("a native assertion replaced the catalog's exact bytes")
	}
	cold := r.Clone()
	cold.Method = vmmdpb.WakeMethod_WAKE_COLD_BOOT
	cold.SnapshotConsumption = SnapshotConsumption{}
	if err := cold.Check(cold.Binding, time.Now()); err != nil {
		t.Fatal("verified cold fallback changed", err)
	}
	// Enabling a serving restore receipt does not silently enable a restored
	// parent as capture authority. That lineage needs its own implementation.
	parent := e.Capture.Clone()
	parent.Parent = r.Clone()
	if parent.Check(time.Now()) == nil {
		t.Fatal("recursive restore parent gained capture authority")
	}
}

func TestSnapshotConsumptionReceiptWireRoundTripAndAbsence(t *testing.T) {
	r, _ := snapshotConsumedReceiptFixture(t)
	p := r.ToProto()
	got, err := ReceiptFromProto(p)
	if err != nil || !got.Equal(r) {
		t.Fatal("complete wire receipt lost facts", err)
	}
	p.SnapshotConsumption.Memory.Digest = "caller-change"
	if !got.Equal(r) {
		t.Fatal("wire caller mutated retained receipt")
	}
	for _, nested := range []string{"proof", "memory", "vmstate", "private"} {
		t.Run(nested, func(t *testing.T) {
			p := r.ToProto()
			target := p.SnapshotConsumption.ProtoReflect()
			switch nested {
			case "memory":
				target = p.SnapshotConsumption.Memory.ProtoReflect()
			case "vmstate":
				target = p.SnapshotConsumption.Vmstate.ProtoReflect()
			case "private":
				target = p.SnapshotConsumption.PrivateDrive.ProtoReflect()
			}
			target.SetUnknown([]byte{0xa0, 0x06, 0x01})
			if _, err := ReceiptFromProto(p); err == nil {
				t.Fatal("unknown proof fields crossed wire boundary")
			}
		})
	}
	cold := consumedReceiptFixture(t)
	raw, err := json.Marshal(cold)
	if err != nil || strings.Contains(string(raw), "snapshot_consumption") || cold.ToProto().SnapshotConsumption != nil {
		t.Fatal("absent proof changed historical encodings", err)
	}
	copy := r.Clone()
	copy.SnapshotConsumption.Memory.Digest = "reader-change"
	if copy.Equal(r) || r.SnapshotConsumption.Memory.Digest == "reader-change" {
		t.Fatal("proof copied incorrectly")
	}
}
