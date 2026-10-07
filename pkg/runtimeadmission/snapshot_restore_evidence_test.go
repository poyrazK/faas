package runtimeadmission

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/protobuf/proto"
)

func snapshotRestoreEvidenceFixture(t *testing.T) (SnapshotRestoreEvidence, Binding, []ArtifactSource) {
	t.Helper()
	c, b, sources := snapshotRestoreInputFixture(t)
	c.Memory.Bytes = 128 << 20
	parts := strings.Split(c.Memory.StorageKey, "/")
	e := SnapshotRestoreEvidence{Version: SnapshotRestoreVersion, CaptureToken: parts[len(parts)-3], FCVersion: "1.12.1", Capture: c}
	b.SnapshotCaptureToken = e.CaptureToken
	var err error
	b.SnapshotEvidenceHash, err = e.Hash()
	if err != nil {
		t.Fatal(err)
	}
	return e, b, sources
}

func snapshotRestorePayloadFixture(t *testing.T) (*vmmdpb.CreateAdmittedRuntimeRequest, Binding) {
	t.Helper()
	e, b, sources := snapshotRestoreEvidenceFixture(t)
	r := &vmmdpb.CreateAdmittedRuntimeRequest{Binding: b.ToProto(), SnapshotRestore: e.ToProto(), Boot: &vmmdpb.CreateAdmittedRuntimeRequest_Restore{Restore: &vmmdpb.CreateFromSnapshotRequest{Instance: b.InstanceID, AccountId: b.AccountID, App: &vmmdpb.AppSpec{AppId: b.AppID, MemSizeMib: 128}, Snapshot: &vmmdpb.SnapshotRef{DeploymentId: b.DeploymentID, StorageKey: e.Capture.Memory.StorageKey, VmstateStorageKey: e.Capture.VMState.StorageKey, FcVersion: e.FCVersion}}}}
	for _, source := range sources {
		r.ArtifactSources = append(r.ArtifactSources, source.ToProto())
	}
	var err error
	b.PayloadHash, err = HashBootPayload(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Binding = b.ToProto()
	return r, b
}

func TestSnapshotRestorePayloadIncludesCompanionMemory(t *testing.T) {
	req, b := snapshotRestorePayloadFixture(t)
	// This test isolates memory arithmetic; native source membership is checked
	// separately at the admitted adapter, before allocation.
	req.GetRestore().App.Sidecars = []*vmmdpb.SidecarSpec{{Name: "metrics", RamMb: 64}}
	if CheckSnapshotRestorePayload(req, b, time.Now()) == nil {
		t.Fatal("snapshot with only main RAM accepted for companion VM")
	}
	req.SnapshotRestore.Capture.Memory.Bytes = 192 << 20
	e, err := SnapshotRestoreEvidenceFromProto(req.SnapshotRestore)
	if err != nil {
		t.Fatal(err)
	}
	b.SnapshotEvidenceHash, err = e.Hash()
	if err != nil || CheckSnapshotRestorePayload(req, b, time.Now()) != nil {
		t.Fatal("complete companion memory refused", err)
	}
	for _, ram := range []int32{-1, 1 << 30} {
		req.GetRestore().App.Sidecars[0].RamMb = ram
		if CheckSnapshotRestorePayload(req, b, time.Now()) == nil {
			t.Fatal("invalid companion memory accepted")
		}
	}
}

func TestSnapshotRestoreEvidenceRetainsHistoryAndOwnsNestedBytes(t *testing.T) {
	e, b, sources := snapshotRestoreEvidenceFixture(t)
	e.Capture.Parent.Binding.IssuedAtUnixNano -= int64(2 * time.Hour)
	e.Capture.Parent.Binding.ExpiresAtUnixNano -= int64(2 * time.Hour)
	e.Capture.Parent.CompletedAtUnixNano -= int64(2 * time.Hour)
	e.Capture.CapturedAtUnixNano -= int64(2 * time.Hour)
	b.SnapshotEvidenceHash, _ = e.Hash()
	if err := e.Check(b, sources, e.Capture.Memory.StorageKey, e.Capture.VMState.StorageKey, e.FCVersion, e.Capture.Memory.Bytes, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(e.Capture.Parent.Binding.Validate(time.Now()), ErrExpired) {
		t.Fatal("parent authority was renewed")
	}
	p := e.ToProto()
	got, err := SnapshotRestoreEvidenceFromProto(p)
	if err != nil || !got.Capture.Equal(e.Capture) {
		t.Fatal("wire round trip", err)
	}
	p.Capture.Parent.ArtifactConsumption.Drives[0].DriveId = "caller-edit"
	copy := got.Clone()
	copy.Capture.Parent.ArtifactConsumption.Drives[0].DriveID = "reader-edit"
	if !got.Capture.Equal(e.Capture) {
		t.Fatal("nested evidence aliased")
	}
	if round, err := BindingFromProto(b.ToProto()); err != nil || round != b {
		t.Fatal("coupled binding lost on wire", err)
	}
}

func TestSnapshotRestorePayloadRejectsUnboundOrDifferentEvidence(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*vmmdpb.CreateAdmittedRuntimeRequest, *Binding)
	}{
		{"no envelope", func(r *vmmdpb.CreateAdmittedRuntimeRequest, _ *Binding) { r.SnapshotRestore = nil }},
		{"no binding", func(_ *vmmdpb.CreateAdmittedRuntimeRequest, b *Binding) {
			b.SnapshotCaptureToken = ""
			b.SnapshotEvidenceHash = ""
		}},
		{"only token", func(_ *vmmdpb.CreateAdmittedRuntimeRequest, b *Binding) { b.SnapshotEvidenceHash = "" }},
		{"only hash", func(_ *vmmdpb.CreateAdmittedRuntimeRequest, b *Binding) { b.SnapshotCaptureToken = "" }},
		{"different token", func(_ *vmmdpb.CreateAdmittedRuntimeRequest, b *Binding) { b.SnapshotCaptureToken = uuid.NewString() }},
		{"different hash", func(_ *vmmdpb.CreateAdmittedRuntimeRequest, b *Binding) {
			b.SnapshotEvidenceHash = strings.Repeat("0", 64)
		}},
		{"legacy protocol", func(_ *vmmdpb.CreateAdmittedRuntimeRequest, b *Binding) {
			b.ProtocolVersion = ProtocolVersion
			b.ArtifactSourcesHash = ""
		}},
		{"cold variant", func(r *vmmdpb.CreateAdmittedRuntimeRequest, _ *Binding) {
			r.Boot = &vmmdpb.CreateAdmittedRuntimeRequest_ColdBoot{ColdBoot: &vmmdpb.CreateColdBootRequest{}}
		}},
		{"RAM", func(r *vmmdpb.CreateAdmittedRuntimeRequest, _ *Binding) { r.GetRestore().App.MemSizeMib++ }},
		{"memory locator", func(r *vmmdpb.CreateAdmittedRuntimeRequest, _ *Binding) {
			r.GetRestore().Snapshot.StorageKey += "-other"
		}},
		{"vmstate locator", func(r *vmmdpb.CreateAdmittedRuntimeRequest, _ *Binding) {
			r.GetRestore().Snapshot.VmstateStorageKey += "-other"
		}},
		{"Firecracker", func(r *vmmdpb.CreateAdmittedRuntimeRequest, _ *Binding) {
			r.GetRestore().Snapshot.FcVersion += "-other"
		}},
		{"networkless", func(r *vmmdpb.CreateAdmittedRuntimeRequest, _ *Binding) { r.GetRestore().Snapshot.Networkless = true }},
		{"source set", func(r *vmmdpb.CreateAdmittedRuntimeRequest, _ *Binding) { r.ArtifactSources = r.ArtifactSources[1:] }},
		{"capture digest", func(r *vmmdpb.CreateAdmittedRuntimeRequest, _ *Binding) {
			r.SnapshotRestore.Capture.Memory.Digest = "sha256:" + strings.Repeat("0", 64)
		}},
		{"namespace token", func(r *vmmdpb.CreateAdmittedRuntimeRequest, _ *Binding) {
			r.SnapshotRestore.CaptureToken = uuid.NewString()
		}},
		{"unknown nested", func(r *vmmdpb.CreateAdmittedRuntimeRequest, _ *Binding) {
			r.SnapshotRestore.Capture.Parent.Binding.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, b := snapshotRestorePayloadFixture(t)
			tc.edit(r, &b)
			if CheckSnapshotRestorePayload(r, b, time.Now()) == nil {
				t.Fatal("changed envelope accepted")
			}
		})
	}
	r, b := snapshotRestorePayloadFixture(t)
	if err := CheckSnapshotRestorePayload(r, b, time.Now()); err != nil {
		t.Fatal("valid envelope refused", err)
	}
	r.SnapshotRestore = nil
	b.SnapshotCaptureToken = ""
	b.SnapshotEvidenceHash = ""
	if err := CheckSnapshotRestorePayload(r, b, time.Now()); err != nil {
		t.Fatal("legacy absence changed", err)
	}
}

func TestSnapshotRestorePayloadHashCoversCaptureAndParentEvidence(t *testing.T) {
	r, b := snapshotRestorePayloadFixture(t)
	for _, edit := range []func(*vmmdpb.RuntimeSnapshotRestoreEvidence){
		func(e *vmmdpb.RuntimeSnapshotRestoreEvidence) { e.Version++ },
		func(e *vmmdpb.RuntimeSnapshotRestoreEvidence) { e.CaptureToken = uuid.NewString() },
		func(e *vmmdpb.RuntimeSnapshotRestoreEvidence) { e.FcVersion += "-other" },
		func(e *vmmdpb.RuntimeSnapshotRestoreEvidence) { e.Capture.CapturedAtUnixNano++ },
		func(e *vmmdpb.RuntimeSnapshotRestoreEvidence) {
			e.Capture.Parent.NativeInputHash = strings.Repeat("0", 64)
		},
		func(e *vmmdpb.RuntimeSnapshotRestoreEvidence) {
			e.Capture.Parent.Binding.CapturedInputHash = strings.Repeat("0", 64)
		},
		func(e *vmmdpb.RuntimeSnapshotRestoreEvidence) {
			e.Capture.Parent.ArtifactConsumption.Drives[0].ProducerDigest = "sha256:" + strings.Repeat("0", 64)
		},
		func(e *vmmdpb.RuntimeSnapshotRestoreEvidence) { e.Capture.Memory.StorageKey += "-other" },
		func(e *vmmdpb.RuntimeSnapshotRestoreEvidence) {
			e.Capture.Memory.Digest = "sha256:" + strings.Repeat("0", 64)
		},
		func(e *vmmdpb.RuntimeSnapshotRestoreEvidence) { e.Capture.Memory.Bytes++ },
		func(e *vmmdpb.RuntimeSnapshotRestoreEvidence) { e.Capture.Vmstate.StorageKey += "-other" },
		func(e *vmmdpb.RuntimeSnapshotRestoreEvidence) {
			e.Capture.Vmstate.Digest = "sha256:" + strings.Repeat("0", 64)
		},
		func(e *vmmdpb.RuntimeSnapshotRestoreEvidence) { e.Capture.Vmstate.Bytes++ },
		func(e *vmmdpb.RuntimeSnapshotRestoreEvidence) { e.Capture.PrivateDrive.StorageKey += "-other" },
		func(e *vmmdpb.RuntimeSnapshotRestoreEvidence) {
			e.Capture.PrivateDrive.Digest = "sha256:" + strings.Repeat("0", 64)
		},
		func(e *vmmdpb.RuntimeSnapshotRestoreEvidence) { e.Capture.PrivateDrive.Bytes++ },
	} {
		changed := proto.Clone(r).(*vmmdpb.CreateAdmittedRuntimeRequest)
		edit(changed.SnapshotRestore)
		hash, err := HashBootPayload(changed)
		if err != nil || hash == b.PayloadHash {
			t.Fatal("envelope field outside payload hash", err)
		}
	}
	r.SnapshotRestore.Capture.Memory.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
	if _, err := HashBootPayload(r); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown evidence outside wire fence", err)
	}
}

func TestSnapshotRestoreIdentityRequiresVersionedMeasuredProtocol(t *testing.T) {
	i := Identity{ProtocolVersion: ArtifactProtocolVersion, NodeID: uuid.NewString(), Incarnation: uuid.NewString()}
	if i.Validate() != nil {
		t.Fatal("old protocol-2 identity became unavailable")
	}
	i.SnapshotRestoreVersion = SnapshotRestoreVersion
	if i.Validate() != nil {
		t.Fatal("versioned capability refused")
	}
	i.ProtocolVersion = ProtocolVersion
	if !errors.Is(i.Validate(), ErrUnavailable) {
		t.Fatal("legacy protocol invented snapshot capability")
	}
	i.ProtocolVersion = ArtifactProtocolVersion
	i.SnapshotRestoreVersion++
	if !errors.Is(i.Validate(), ErrUnavailable) {
		t.Fatal("unknown snapshot capability accepted")
	}
}
