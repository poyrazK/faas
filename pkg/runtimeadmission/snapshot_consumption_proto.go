package runtimeadmission

import vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"

func (c SnapshotConsumption) ToProto() *vmmdpb.RuntimeSnapshotConsumption {
	if c.IsZero() {
		return nil
	}
	artifact := func(a CapturedArtifact) *vmmdpb.RuntimeCapturedArtifact {
		return &vmmdpb.RuntimeCapturedArtifact{StorageKey: a.StorageKey, Digest: a.Digest, Bytes: a.Bytes}
	}
	return &vmmdpb.RuntimeSnapshotConsumption{Version: c.Version, CaptureToken: c.CaptureToken, EvidenceHash: c.EvidenceHash,
		Memory: artifact(c.Memory), Vmstate: artifact(c.VMState), PrivateDrive: artifact(c.PrivateDrive), MappedMemoryBytes: c.MappedMemoryBytes}
}

func snapshotConsumptionFromProto(p *vmmdpb.RuntimeSnapshotConsumption) (SnapshotConsumption, error) {
	if p == nil {
		return SnapshotConsumption{}, nil
	}
	if RejectUnknown(p) != nil || p.Memory == nil || p.Vmstate == nil || p.PrivateDrive == nil || p.Version != SnapshotRestoreVersion {
		return SnapshotConsumption{}, ErrInvalid
	}
	artifact := func(a *vmmdpb.RuntimeCapturedArtifact) CapturedArtifact {
		return CapturedArtifact{StorageKey: a.StorageKey, Digest: a.Digest, Bytes: a.Bytes}
	}
	return SnapshotConsumption{Version: p.Version, CaptureToken: p.CaptureToken, EvidenceHash: p.EvidenceHash,
		Memory: artifact(p.Memory), VMState: artifact(p.Vmstate), PrivateDrive: artifact(p.PrivateDrive), MappedMemoryBytes: p.MappedMemoryBytes}, nil
}
