package runtimeadmission

import vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"

func (e SnapshotResumeEvidence) ToProto() *vmmdpb.RuntimeSnapshotResumeEvidence {
	if e.IsZero() {
		return nil
	}
	return &vmmdpb.RuntimeSnapshotResumeEvidence{Version: e.Version, Binding: e.Binding.ToProto(), ParentReceiptHash: e.ParentReceiptHash,
		ResumeCommandHash: e.ResumeCommandHash, ResumeHookPayloadHash: e.ResumeHookPayloadHash,
		CommandCompletedAtUnixNano: e.CommandCompletedAtUnixNano, HostTimeUnixNano: e.HostTimeUnixNano,
		HookCompletedAtUnixNano: e.HookCompletedAtUnixNano, CompletedAtUnixNano: e.CompletedAtUnixNano}
}

func SnapshotResumeEvidenceFromProto(p *vmmdpb.RuntimeSnapshotResumeEvidence) (SnapshotResumeEvidence, error) {
	if p == nil || RejectUnknown(p) != nil || p.Version != SnapshotResumeEvidenceVersion {
		return SnapshotResumeEvidence{}, ErrInvalid
	}
	b, err := BindingFromProto(p.Binding)
	if err != nil {
		return SnapshotResumeEvidence{}, err
	}
	return SnapshotResumeEvidence{Version: p.Version, Binding: b, ParentReceiptHash: p.ParentReceiptHash,
		ResumeCommandHash: p.ResumeCommandHash, ResumeHookPayloadHash: p.ResumeHookPayloadHash,
		CommandCompletedAtUnixNano: p.CommandCompletedAtUnixNano, HostTimeUnixNano: p.HostTimeUnixNano,
		HookCompletedAtUnixNano: p.HookCompletedAtUnixNano, CompletedAtUnixNano: p.CompletedAtUnixNano}, nil
}
