// adr: 431
package gateway

// ADR-431: the gateway forwarding fake implements the expanded generated
// client without granting privileged admission authority or inventing native
// receipts. A mistaken call to these capabilities must refuse explicitly.

import (
	"context"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (*stubVmmdClient) RuntimeAdmissionIdentity(context.Context, *vmmdpb.RuntimeAdmissionIdentityRequest, ...grpc.CallOption) (*vmmdpb.RuntimeAdmissionIdentityResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding stub does not grant runtime admission")
}

func (*stubVmmdClient) CreateAdmittedRuntime(context.Context, *vmmdpb.CreateAdmittedRuntimeRequest, ...grpc.CallOption) (*vmmdpb.CreateAdmittedRuntimeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding stub does not create admitted runtimes")
}

func (*stubVmmdClient) PromoteAdmittedRuntime(context.Context, *vmmdpb.PromoteAdmittedRuntimeRequest, ...grpc.CallOption) (*vmmdpb.PromoteAdmittedRuntimeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding stub does not promote admitted runtimes")
}

func (*stubVmmdClient) CaptureAdmittedRuntime(context.Context, *vmmdpb.CaptureAdmittedRuntimeRequest, ...grpc.CallOption) (*vmmdpb.CaptureAdmittedRuntimeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding stub does not capture admitted runtimes")
}

func (*stubVmmdClient) UpdateAppEgressPolicy(context.Context, *vmmdpb.UpdateAppEgressPolicyRequest, ...grpc.CallOption) (*vmmdpb.UpdateAppEgressPolicyAck, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding stub does not install runtime egress policies")
}

func (*stubVmmdClient) MaterializeVerifiedParentExt4(context.Context, *vmmdpb.MaterializeVerifiedParentExt4Request, ...grpc.CallOption) (*vmmdpb.MaterializeVerifiedParentExt4Response, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding stub does not materialize verified parents")
}

var _ vmmdpb.VmmdClient = (*stubVmmdClient)(nil)
