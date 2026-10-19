package vmmdgrpc

import (
	"context"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type admittedSnapshotVMM interface {
	RuntimeAdmissionIdentity() (runtimeadmission.Identity, error)
	CaptureAdmitted(context.Context, runtimeadmission.SnapshotGrant) (fcvm.SnapshotInfo, runtimeadmission.SnapshotAcknowledgment, error)
}

func (s *Server) CaptureAdmittedRuntime(ctx context.Context, req *vmmdpb.CaptureAdmittedRuntimeRequest) (_ *vmmdpb.CaptureAdmittedRuntimeResponse, err error) {
	start := time.Now()
	defer func() { s.ops.Observe("CaptureAdmittedRuntime", time.Since(start), err) }()
	vmm, ok := s.vmm.(admittedSnapshotVMM)
	if !ok {
		return nil, admissionStatus(runtimeadmission.ErrUnavailable)
	}
	if err := authorizeRuntimeAdmissionPeer(ctx); err != nil {
		return nil, err
	}
	if req == nil || runtimeadmission.RejectUnknown(req) != nil {
		return nil, admissionStatus(runtimeadmission.ErrInvalid)
	}
	g, err := runtimeadmission.SnapshotGrantFromProto(req.Grant)
	if err != nil {
		return nil, admissionStatus(err)
	}
	i, err := vmm.RuntimeAdmissionIdentity()
	if err != nil {
		return nil, admissionStatus(err)
	}
	if i.Validate() != nil || s.nodeID == "" || i.NodeID != s.nodeID || g.Parent.Binding.NodeID != i.NodeID || g.Parent.Binding.Incarnation != i.Incarnation || i.ProtocolVersion < runtimeadmission.ArtifactProtocolVersion || g.FCVersion != s.fcVer {
		return nil, admissionStatus(runtimeadmission.ErrStale)
	}
	ctx = withIncomingCorrelation(ctx)
	info, ack, err := vmm.CaptureAdmitted(ctx, g)
	if err != nil {
		return nil, admissionStatus(err)
	}
	p := &vmmdpb.CaptureAdmittedRuntimeResponse{Snapshot: &vmmdpb.SnapshotResponse{MemBytes: info.MemBytes, VmstateBytes: info.VMStateBytes, StoredBytes: info.StoredBytes, BeforeCheckpointCompleted: g.BeforeCheckpoint, Capture: info.Capture.ToProto()}, Acknowledgment: ack.ToProto()}
	if _, err := runtimeadmission.CheckAdmittedSnapshotResponse(p, g); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardRuntimeCleanupTimeout)
		defer cancel()
		_ = s.vmm.Destroy(cleanupCtx, g.Parent.Binding.InstanceID)
		return nil, admissionStatus(err)
	}
	if g.Mode == "park" && s.streamBridges != nil {
		s.streamBridges.forget(context.WithoutCancel(ctx), g.Parent.Binding.InstanceID)
	}
	return p, nil
}
