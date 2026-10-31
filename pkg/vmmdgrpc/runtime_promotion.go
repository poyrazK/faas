package vmmdgrpc

import (
	"context"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

// admissionHolder reports a parent receipt this vmmd holds through a restart
// (fcvm.Manager.AdmissionHeldFrom).
type admissionHolder interface {
	AdmissionHeldFrom(instance string, parent runtimeadmission.Receipt) string
}

type admittedPromotionVMM interface {
	RuntimeAdmissionIdentity() (runtimeadmission.Identity, error)
	PromoteAdmitted(context.Context, runtimeadmission.Promotion) (*fcvm.Instance, runtimeadmission.Receipt, error)
}

func (s *Server) PromoteAdmittedRuntime(ctx context.Context, req *vmmdpb.PromoteAdmittedRuntimeRequest) (_ *vmmdpb.PromoteAdmittedRuntimeResponse, err error) {
	start := time.Now()
	defer func() { s.ops.Observe("PromoteAdmittedRuntime", time.Since(start), err) }()
	vmm, p, err := s.parseAdmittedPromotion(ctx, req)
	if err != nil {
		return nil, err
	}
	inst, r, err := vmm.PromoteAdmitted(withIncomingCorrelation(ctx), p)
	if err != nil {
		return nil, admissionStatus(err)
	}
	if !nativePromotionReceiptMatches(p, inst, r) {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardRuntimeCleanupTimeout)
		defer cancel()
		_ = s.vmm.Destroy(cleanupCtx, p.Binding.InstanceID)
		if s.streamBridges != nil {
			s.streamBridges.forget(cleanupCtx, p.Binding.InstanceID)
		}
		return nil, admissionStatus(runtimeadmission.ErrInvalid)
	}
	return &vmmdpb.PromoteAdmittedRuntimeResponse{Receipt: r.ToProto()}, nil
}

func (s *Server) parseAdmittedPromotion(ctx context.Context, req *vmmdpb.PromoteAdmittedRuntimeRequest) (admittedPromotionVMM, runtimeadmission.Promotion, error) {
	vmm, ok := s.vmm.(admittedPromotionVMM)
	if !ok {
		return nil, runtimeadmission.Promotion{}, admissionStatus(runtimeadmission.ErrUnavailable)
	}
	if err := authorizeRuntimeAdmissionPeer(ctx); err != nil {
		return nil, runtimeadmission.Promotion{}, err
	}
	p, err := runtimeadmission.PromotionFromProto(req)
	if err != nil {
		return nil, runtimeadmission.Promotion{}, admissionStatus(err)
	}
	// A guest this vmmd adopted carries a parent its predecessor issued
	// (ADR-595 extension); the hold also covers the receipt check after.
	if h, ok := vmm.(admissionHolder); ok {
		p = p.Held(h.AdmissionHeldFrom(p.Binding.InstanceID, p.Parent))
	}
	if err := p.Validate(time.Now()); err != nil {
		return nil, runtimeadmission.Promotion{}, admissionStatus(err)
	}
	i, err := vmm.RuntimeAdmissionIdentity()
	if err != nil || i.Validate() != nil || i.NodeID != s.nodeID || i.NodeID != p.Binding.NodeID || i.Incarnation != p.Binding.Incarnation {
		return nil, runtimeadmission.Promotion{}, admissionStatus(runtimeadmission.ErrStale)
	}
	if p.Binding.ProtocolVersion > i.ProtocolVersion || p.Binding.ProtocolVersion == runtimeadmission.ArtifactProtocolVersion && i.SnapshotRestoreVersion != runtimeadmission.SnapshotRestoreVersion {
		return nil, runtimeadmission.Promotion{}, admissionStatus(runtimeadmission.ErrUnavailable)
	}
	return vmm, p, nil
}

func nativePromotionReceiptMatches(p runtimeadmission.Promotion, inst *fcvm.Instance, r runtimeadmission.Receipt) bool {
	return p.CheckReceipt(r, time.Now()) == nil && inst != nil && !inst.Paused && inst.Lease.Instance == p.Binding.InstanceID && inst.AppID == p.Binding.AppID && inst.AccountID == p.Binding.AccountID && inst.DeploymentID == p.Binding.DeploymentID && r.Netns == inst.Net.Netns && r.HostIP == inst.Lease.HostIP.String() && r.LeaseUID == int32(inst.Lease.UID) && r.Method == wakeMethodFrom(inst.Method)
}
