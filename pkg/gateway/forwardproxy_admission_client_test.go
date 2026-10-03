// adr: 435
package gateway_test

// ADR-435: the gateway forwarding fake implements the expanded generated
// client without granting privileged admission authority or inventing native
// receipts. A mistaken call to these capabilities must refuse explicitly.

import (
	"context"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (*fakeVmmdClient) RuntimeAdmissionIdentity(context.Context, *vmmdpb.RuntimeAdmissionIdentityRequest, ...grpc.CallOption) (*vmmdpb.RuntimeAdmissionIdentityResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding stub does not grant runtime admission")
}

func (*fakeVmmdClient) CreateAdmittedRuntime(context.Context, *vmmdpb.CreateAdmittedRuntimeRequest, ...grpc.CallOption) (*vmmdpb.CreateAdmittedRuntimeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding stub does not create admitted runtimes")
}

func (*fakeVmmdClient) PromoteAdmittedRuntime(context.Context, *vmmdpb.PromoteAdmittedRuntimeRequest, ...grpc.CallOption) (*vmmdpb.PromoteAdmittedRuntimeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding stub does not promote admitted runtimes")
}

func (*fakeVmmdClient) UpdateAppEgressPolicy(context.Context, *vmmdpb.UpdateAppEgressPolicyRequest, ...grpc.CallOption) (*vmmdpb.UpdateAppEgressPolicyAck, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding stub does not install runtime egress policies")
}

func (*fakeVmmdClient) MaterializeVerifiedParentExt4(context.Context, *vmmdpb.MaterializeVerifiedParentExt4Request, ...grpc.CallOption) (*vmmdpb.MaterializeVerifiedParentExt4Response, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding fake does not materialize verified parents")
}

var _ vmmdpb.VmmdClient = (*fakeVmmdClient)(nil)
