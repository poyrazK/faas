// adr: 435
// adr: 1522
// adr: 1525
// adr: 1529
package gateway

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

func (*stubVmmdClient) UpdateAdmittedAppEgressPolicy(context.Context, *vmmdpb.UpdateAdmittedAppEgressPolicyRequest, ...grpc.CallOption) (*vmmdpb.UpdateAdmittedAppEgressPolicyAck, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding stub does not install admitted runtime egress policies")
}

func (*stubVmmdClient) MaterializeVerifiedParentExt4(context.Context, *vmmdpb.MaterializeVerifiedParentExt4Request, ...grpc.CallOption) (*vmmdpb.MaterializeVerifiedParentExt4Response, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding stub does not materialize verified parents")
}

func (*stubVmmdClient) MaterializeRuntimeScan(context.Context, *vmmdpb.MaterializeRuntimeScanRequest, ...grpc.CallOption) (*vmmdpb.MaterializeRuntimeScanResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding stub does not materialize runtime scans")
}

// ADR-1543: forwarding fixtures cannot authenticate or launch managed probes.
func (*stubVmmdClient) ResolveServiceCaller(context.Context, *vmmdpb.ResolveServiceCallerRequest, ...grpc.CallOption) (*vmmdpb.ResolveServiceCallerResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding stub does not authenticate service callers")
}

func (*stubVmmdClient) RestoreManagedServiceProbe(context.Context, *vmmdpb.RestoreAppTaskRequest, ...grpc.CallOption) (*vmmdpb.RestoreAppTaskResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding stub does not launch managed service probes")
}

func (*stubVmmdClient) GetOperatorRestartGeneration(context.Context, *vmmdpb.GetOperatorRestartGenerationRequest, ...grpc.CallOption) (*vmmdpb.GetOperatorRestartGenerationResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding stub does not resolve operator restart generations")
}

func (*stubVmmdClient) ExecuteOperatorRestartDestroy(context.Context, *vmmdpb.ExecuteOperatorRestartDestroyRequest, ...grpc.CallOption) (*vmmdpb.ExecuteOperatorRestartDestroyResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding stub does not execute operator restarts")
}

func (*stubVmmdClient) ExecuteOperatorPark(context.Context, *vmmdpb.ExecuteOperatorParkRequest, ...grpc.CallOption) (*vmmdpb.ExecuteOperatorParkResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding stub does not execute operator parks")
}

func (*stubVmmdClient) RetireMigrationAdoption(context.Context, *vmmdpb.RetireMigrationAdoptionRequest, ...grpc.CallOption) (*vmmdpb.RetireMigrationAdoptionResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding stub does not retire migration adoptions")
}

var _ vmmdpb.VmmdClient = (*stubVmmdClient)(nil)
