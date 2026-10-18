package runtimeadmission

import (
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

func admittedSnapshotResponseFixture(t *testing.T) (SnapshotGrant, *vmmdpb.CaptureAdmittedRuntimeResponse) {
	t.Helper()
	g, a := snapshotGrantFixture(t)
	return g, &vmmdpb.CaptureAdmittedRuntimeResponse{Snapshot: &vmmdpb.SnapshotResponse{MemBytes: a.Capture.Memory.Bytes, VmstateBytes: a.Capture.VMState.Bytes, StoredBytes: 4096, Capture: a.Capture.ToProto()}, Acknowledgment: a.ToProto()}
}

func TestSnapshotGrantProtoRoundTripOwnsNestedHistory(t *testing.T) {
	g, p := admittedSnapshotResponseFixture(t)
	parsed, err := SnapshotGrantFromProto(p.Acknowledgment.Grant)
	if err != nil || !parsed.Equal(g) {
		t.Fatal("grant lost authority or lineage in RPC conversion", err)
	}
	a, err := CheckAdmittedSnapshotResponse(p, g)
	if err != nil || a.Check(g, time.Now()) != nil {
		t.Fatal("acknowledgment lost its exact issued grant", err)
	}
	p.Acknowledgment.Grant.Parent.ArtifactConsumption.Drives[0].Source.StorageKey = "caller-mutated"
	p.Snapshot.Capture.Parent.ArtifactConsumption.Drives[0].Source.StorageKey = "caller-mutated"
	if !parsed.Equal(g) || !a.Grant.Equal(g) || !a.Capture.Parent.Equal(g.Parent) {
		t.Fatal("wire caller mutated returned nested evidence")
	}
}

func TestAdmittedSnapshotResponseRejectsMissingMismatchedAndUnknownProof(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*vmmdpb.CaptureAdmittedRuntimeResponse)
	}{
		{"missing acknowledgment", func(p *vmmdpb.CaptureAdmittedRuntimeResponse) { p.Acknowledgment = nil }},
		{"missing snapshot", func(p *vmmdpb.CaptureAdmittedRuntimeResponse) { p.Snapshot = nil }},
		{"missing capture", func(p *vmmdpb.CaptureAdmittedRuntimeResponse) { p.Snapshot.Capture = nil }},
		{"wrong count", func(p *vmmdpb.CaptureAdmittedRuntimeResponse) { p.Snapshot.MemBytes++ }},
		{"negative storage", func(p *vmmdpb.CaptureAdmittedRuntimeResponse) { p.Snapshot.StoredBytes = -1 }},
		{"callback", func(p *vmmdpb.CaptureAdmittedRuntimeResponse) { p.Snapshot.BeforeCheckpointCompleted = true }},
		{"different measurement", func(p *vmmdpb.CaptureAdmittedRuntimeResponse) { p.Snapshot.Capture.CapturedAtUnixNano-- }},
		{"different grant", func(p *vmmdpb.CaptureAdmittedRuntimeResponse) { p.Acknowledgment.Grant.BeforeCheckpoint = true }},
		{"unknown response", func(p *vmmdpb.CaptureAdmittedRuntimeResponse) { p.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01}) }},
		{"unknown grant", func(p *vmmdpb.CaptureAdmittedRuntimeResponse) {
			p.Acknowledgment.Grant.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
		}},
		{"unknown drive", func(p *vmmdpb.CaptureAdmittedRuntimeResponse) {
			p.Acknowledgment.Grant.Parent.ArtifactConsumption.Drives[0].Source.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
		}},
		{"unknown measurement", func(p *vmmdpb.CaptureAdmittedRuntimeResponse) {
			p.Acknowledgment.Capture.Memory.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, p := admittedSnapshotResponseFixture(t)
			tc.edit(p)
			if _, err := CheckAdmittedSnapshotResponse(p, g); err == nil {
				t.Fatal("malformed capture proof accepted")
			}
		})
	}
}
