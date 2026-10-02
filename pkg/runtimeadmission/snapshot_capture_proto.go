package runtimeadmission

import (
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

func (c SnapshotCapture) ToProto() *vmmdpb.RuntimeSnapshotCapture {
	if c.IsZero() {
		return nil
	}
	artifact := func(a CapturedArtifact) *vmmdpb.RuntimeCapturedArtifact {
		return &vmmdpb.RuntimeCapturedArtifact{StorageKey: a.StorageKey, Digest: a.Digest, Bytes: a.Bytes}
	}
	return &vmmdpb.RuntimeSnapshotCapture{Version: c.Version, Parent: c.Parent.ToProto(), Memory: artifact(c.Memory), Vmstate: artifact(c.VMState), PrivateDrive: artifact(c.PrivateDrive), CapturedAtUnixNano: c.CapturedAtUnixNano}
}

func SnapshotCaptureFromProto(p *vmmdpb.RuntimeSnapshotCapture) (SnapshotCapture, error) {
	if p == nil {
		return SnapshotCapture{}, nil
	}
	if RejectUnknown(p) != nil || p.Memory == nil || p.Vmstate == nil || p.PrivateDrive == nil {
		return SnapshotCapture{}, ErrInvalid
	}
	parent, err := ReceiptFromProto(p.Parent)
	if err != nil {
		return SnapshotCapture{}, err
	}
	artifact := func(a *vmmdpb.RuntimeCapturedArtifact) CapturedArtifact {
		return CapturedArtifact{StorageKey: a.StorageKey, Digest: a.Digest, Bytes: a.Bytes}
	}
	c := SnapshotCapture{Version: p.Version, Parent: parent, Memory: artifact(p.Memory), VMState: artifact(p.Vmstate), PrivateDrive: artifact(p.PrivateDrive), CapturedAtUnixNano: p.CapturedAtUnixNano}
	if err := c.Check(time.Now()); err != nil {
		return SnapshotCapture{}, err
	}
	return c, nil
}

func CheckSnapshotResponse(p *vmmdpb.SnapshotResponse, instance, memory, vmstate string) (SnapshotCapture, error) {
	if p == nil {
		return SnapshotCapture{}, ErrInvalid
	}
	if p.Capture == nil {
		return SnapshotCapture{}, nil
	}
	if RejectUnknown(p) != nil {
		return SnapshotCapture{}, ErrInvalid
	}
	capture, err := SnapshotCaptureFromProto(p.Capture)
	if err != nil || capture.Parent.Binding.InstanceID != instance || capture.Memory.StorageKey != memory || capture.VMState.StorageKey != vmstate || capture.Memory.Bytes != p.MemBytes || capture.VMState.Bytes != p.VmstateBytes || p.StoredBytes < 0 {
		return SnapshotCapture{}, ErrInvalid
	}
	return capture, nil
}
