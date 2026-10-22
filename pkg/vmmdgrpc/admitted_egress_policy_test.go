package vmmdgrpc_test

import (
	"context"
	"errors"
	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"
)

type admittedPolicyVMM struct {
	*fakeVMM
	apply func(runtimeadmission.Identity, runtimeadmission.EgressPolicy) (runtimeadmission.EgressReceipt, error)
	calls int
}

func (v *admittedPolicyVMM) UpdateAdmittedAppEgressPolicy(_ context.Context, i runtimeadmission.Identity, p runtimeadmission.EgressPolicy) (runtimeadmission.EgressReceipt, error) {
	v.calls++
	return v.apply(i, p)
}

func TestUpdateAdmittedEgressRequiresPeerCapabilityAndExactBackendReceipt(t *testing.T) {
	i := runtimeadmission.Identity{NodeID: uuid.NewString(), Incarnation: uuid.NewString(), ProtocolVersion: runtimeadmission.ArtifactProtocolVersion}
	p := runtimeadmission.EgressPolicy{AppID: uuid.NewString(), Revision: 2, Ports: []int{8443}}
	req, err := runtimeadmission.EgressRequest(i, p)
	if err != nil {
		t.Fatal(err)
	}
	v := &admittedPolicyVMM{fakeVMM: &fakeVMM{}}
	v.apply = func(i runtimeadmission.Identity, p runtimeadmission.EgressPolicy) (runtimeadmission.EgressReceipt, error) {
		hash, _ := p.Hash()
		return runtimeadmission.EgressReceipt{Identity: i, AppID: p.AppID, Revision: p.Revision, PolicyHash: hash}, nil
	}
	s := vmmdgrpc.New(v, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil).WithNodeID(i.NodeID)
	if ack, err := s.UpdateAdmittedAppEgressPolicy(t.Context(), req); status.Code(err) != codes.Unauthenticated || ack != nil || v.calls != 0 {
		t.Fatal("unverified scheduler reached native mutation", err)
	}
	if ack, err := s.UpdateAdmittedAppEgressPolicy(admittedRPCContext(t), req); err != nil || ack.GetPolicyHash() == "" || v.calls != 1 {
		t.Fatal("valid native receipt rejected", err)
	}
	req.Policy.EgressPorts = []uint32{65536}
	if ack, err := s.UpdateAdmittedAppEgressPolicy(admittedRPCContext(t), req); status.Code(err) != codes.InvalidArgument || ack != nil || v.calls != 1 {
		t.Fatal("lossy projection reached native mutation", err)
	}
	req, _ = runtimeadmission.EgressRequest(i, p)
	v.apply = func(runtimeadmission.Identity, runtimeadmission.EgressPolicy) (runtimeadmission.EgressReceipt, error) {
		return runtimeadmission.EgressReceipt{}, errors.New("physical apply failed")
	}
	if ack, err := s.UpdateAdmittedAppEgressPolicy(admittedRPCContext(t), req); err == nil || ack != nil {
		t.Fatal("physical failure manufactured acknowledgment")
	}
	v.apply = func(i runtimeadmission.Identity, p runtimeadmission.EgressPolicy) (runtimeadmission.EgressReceipt, error) {
		p.Ports[0] = 9443
		hash, _ := p.Hash()
		return runtimeadmission.EgressReceipt{Identity: i, AppID: p.AppID, Revision: p.Revision, PolicyHash: hash}, nil
	}
	if ack, err := s.UpdateAdmittedAppEgressPolicy(admittedRPCContext(t), req); status.Code(err) != codes.InvalidArgument || ack != nil {
		t.Fatal("backend changed original policy before validation", err)
	}
	old := vmmdgrpc.New(&fakeVMM{}, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil).WithNodeID(i.NodeID)
	if ack, err := old.UpdateAdmittedAppEgressPolicy(admittedRPCContext(t), req); status.Code(err) != codes.Unimplemented || ack != nil {
		t.Fatal("old backend accepted strict capability", err)
	}
	if ack, err := s.UpdateAdmittedAppEgressPolicy(admittedRPCContext(t), (*vmmdpb.UpdateAdmittedAppEgressPolicyRequest)(nil)); status.Code(err) != codes.InvalidArgument || ack != nil {
		t.Fatal("nil request accepted", err)
	}
}
