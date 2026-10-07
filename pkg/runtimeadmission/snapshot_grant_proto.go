package runtimeadmission

import (
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

func (g SnapshotGrant) ToProto() *vmmdpb.RuntimeSnapshotGrant {
	return &vmmdpb.RuntimeSnapshotGrant{Version: g.Version, Token: g.Token, Parent: g.Parent.ToProto(), MemoryKey: g.MemoryKey, VmstateKey: g.VMStateKey, PrivateDriveKey: g.PrivateDriveKey, FcVersion: g.FCVersion, Mode: g.Mode, BeforeCheckpoint: g.BeforeCheckpoint, SourceStartedAtUnixNano: g.SourceStartedAtUnixNano, IssuedAtUnixNano: g.IssuedAtUnixNano, ExpiresAtUnixNano: g.ExpiresAtUnixNano}
}

func SnapshotGrantFromProto(p *vmmdpb.RuntimeSnapshotGrant) (SnapshotGrant, error) {
	if p == nil || RejectUnknown(p) != nil {
		return SnapshotGrant{}, ErrInvalid
	}
	parent, err := ReceiptFromProto(p.Parent)
	if err != nil {
		return SnapshotGrant{}, err
	}
	g := SnapshotGrant{Version: p.Version, Token: p.Token, Parent: parent, MemoryKey: p.MemoryKey, VMStateKey: p.VmstateKey, PrivateDriveKey: p.PrivateDriveKey, FCVersion: p.FcVersion, Mode: p.Mode, BeforeCheckpoint: p.BeforeCheckpoint, SourceStartedAtUnixNano: p.SourceStartedAtUnixNano, IssuedAtUnixNano: p.IssuedAtUnixNano, ExpiresAtUnixNano: p.ExpiresAtUnixNano}
	if err := g.Validate(time.Now()); err != nil {
		return SnapshotGrant{}, err
	}
	return g, nil
}

func (a SnapshotAcknowledgment) ToProto() *vmmdpb.RuntimeSnapshotAcknowledgment {
	return &vmmdpb.RuntimeSnapshotAcknowledgment{Grant: a.Grant.ToProto(), Capture: a.Capture.ToProto(), CompletedAtUnixNano: a.CompletedAtUnixNano}
}

func SnapshotAcknowledgmentFromProto(p *vmmdpb.RuntimeSnapshotAcknowledgment, expected SnapshotGrant) (SnapshotAcknowledgment, error) {
	if p == nil || p.Capture == nil || RejectUnknown(p) != nil {
		return SnapshotAcknowledgment{}, ErrInvalid
	}
	g, err := SnapshotGrantFromProto(p.Grant)
	if err != nil {
		return SnapshotAcknowledgment{}, err
	}
	c, err := SnapshotCaptureFromProto(p.Capture)
	if err != nil {
		return SnapshotAcknowledgment{}, err
	}
	a := SnapshotAcknowledgment{Grant: g, Capture: c, CompletedAtUnixNano: p.CompletedAtUnixNano}
	if err := a.Check(expected, time.Now()); err != nil {
		return SnapshotAcknowledgment{}, err
	}
	return a, nil
}

func CheckAdmittedSnapshotResponse(p *vmmdpb.CaptureAdmittedRuntimeResponse, g SnapshotGrant) (SnapshotAcknowledgment, error) {
	if p == nil || p.Snapshot == nil || RejectUnknown(p) != nil {
		return SnapshotAcknowledgment{}, ErrInvalid
	}
	a, err := SnapshotAcknowledgmentFromProto(p.Acknowledgment, g)
	if err != nil {
		return SnapshotAcknowledgment{}, err
	}
	c, err := CheckSnapshotResponse(p.Snapshot, g.Parent.Binding.InstanceID, g.MemoryKey, g.VMStateKey)
	if err != nil || !c.Equal(a.Capture) || p.Snapshot.BeforeCheckpointCompleted != g.BeforeCheckpoint {
		return SnapshotAcknowledgment{}, ErrInvalid
	}
	return a, nil
}
