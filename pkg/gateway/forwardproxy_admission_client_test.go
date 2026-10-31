// adr: 435
// adr: 1522
// adr: 1525
// adr: 1529
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

func (*fakeVmmdClient) UpdateAdmittedAppEgressPolicy(context.Context, *vmmdpb.UpdateAdmittedAppEgressPolicyRequest, ...grpc.CallOption) (*vmmdpb.UpdateAdmittedAppEgressPolicyAck, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding fake does not install admitted runtime egress policies")
}

func (*fakeVmmdClient) MaterializeVerifiedParentExt4(context.Context, *vmmdpb.MaterializeVerifiedParentExt4Request, ...grpc.CallOption) (*vmmdpb.MaterializeVerifiedParentExt4Response, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding fake does not materialize verified parents")
}

func (*fakeVmmdClient) MaterializeRuntimeScan(context.Context, *vmmdpb.MaterializeRuntimeScanRequest, ...grpc.CallOption) (*vmmdpb.MaterializeRuntimeScanResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding fake does not materialize runtime scans")
}

// ADR-1642: forwarding fixtures cannot authenticate or launch managed probes.
func (*fakeVmmdClient) ResolveServiceCaller(context.Context, *vmmdpb.ResolveServiceCallerRequest, ...grpc.CallOption) (*vmmdpb.ResolveServiceCallerResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding fake does not authenticate service callers")
}

// ADR-1138: forwarding fixtures name no in-flight request trace.
func (*fakeVmmdClient) InflightRequestTrace(context.Context, *vmmdpb.InflightRequestTraceRequest, ...grpc.CallOption) (*vmmdpb.InflightRequestTraceResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding fake does not track request traces")
}

func (*fakeVmmdClient) RestoreManagedServiceProbe(context.Context, *vmmdpb.RestoreAppTaskRequest, ...grpc.CallOption) (*vmmdpb.RestoreAppTaskResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding fake does not launch managed service probes")
}

func (*fakeVmmdClient) GetOperatorRestartGeneration(context.Context, *vmmdpb.GetOperatorRestartGenerationRequest, ...grpc.CallOption) (*vmmdpb.GetOperatorRestartGenerationResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding fake does not resolve operator restart generations")
}

func (*fakeVmmdClient) ExecuteOperatorRestartDestroy(context.Context, *vmmdpb.ExecuteOperatorRestartDestroyRequest, ...grpc.CallOption) (*vmmdpb.ExecuteOperatorRestartDestroyResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding fake does not execute operator restarts")
}

func (*fakeVmmdClient) ExecuteOperatorPark(context.Context, *vmmdpb.ExecuteOperatorParkRequest, ...grpc.CallOption) (*vmmdpb.ExecuteOperatorParkResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding fake does not execute operator parks")
}

func (*fakeVmmdClient) RetireMigrationAdoption(context.Context, *vmmdpb.RetireMigrationAdoptionRequest, ...grpc.CallOption) (*vmmdpb.RetireMigrationAdoptionResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding fake does not retire migration adoptions")
}

func (*fakeVmmdClient) AdoptionReport(context.Context, *vmmdpb.AdoptionReportRequest, ...grpc.CallOption) (*vmmdpb.AdoptionReportResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding fake does not report vmmd restart adoption")
}

func (*fakeVmmdClient) AdoptionInventory(context.Context, *vmmdpb.AdoptionInventoryRequest, ...grpc.CallOption) (*vmmdpb.AdoptionInventoryResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding fake does not list adoption inventory")
}

var _ vmmdpb.VmmdClient = (*fakeVmmdClient)(nil)
