package vmmdgrpc

import (
	"context"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"time"
)

type admittedEgressVMM interface {
	UpdateAdmittedAppEgressPolicy(context.Context, runtimeadmission.Identity, runtimeadmission.EgressPolicy) (runtimeadmission.EgressReceipt, error)
}

func (s *Server) UpdateAdmittedAppEgressPolicy(ctx context.Context, req *vmmdpb.UpdateAdmittedAppEgressPolicyRequest) (_ *vmmdpb.UpdateAdmittedAppEgressPolicyAck, err error) {
	start := time.Now()
	defer func() { s.ops.Observe("UpdateAdmittedAppEgressPolicy", time.Since(start), err) }()
	if err := authorizeRuntimeAdmissionPeer(ctx); err != nil {
		return nil, err
	}
	vmm, ok := s.vmm.(admittedEgressVMM)
	if !ok {
		return nil, admissionStatus(runtimeadmission.ErrUnavailable)
	}
	identity, policy, err := runtimeadmission.EgressFromProto(req)
	if err != nil {
		return nil, admissionStatus(err)
	}
	if s.nodeID == "" || s.nodeID != identity.NodeID {
		return nil, admissionStatus(runtimeadmission.ErrStale)
	}
	receipt, err := vmm.UpdateAdmittedAppEgressPolicy(ctx, identity, policy.Clone())
	if err != nil {
		return nil, admissionStatus(err)
	}
	if err := receipt.Check(identity, policy); err != nil {
		return nil, admissionStatus(err)
	}
	return receipt.ToProto(), nil
}
