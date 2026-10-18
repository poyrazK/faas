// adr: 20261016
package gateway_test

import (
	"context"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Forwarding fixtures refuse repair-only authority.
func (*fakeVmmdClient) DataRepairRehearsalIdentity(context.Context, *vmmdpb.DataRepairRehearsalIdentityRequest, ...grpc.CallOption) (*vmmdpb.DataRepairRehearsalIdentityResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding does not identify repair runtimes")
}

func (*fakeVmmdClient) RunDataRepairRehearsal(context.Context, ...grpc.CallOption) (grpc.ClientStreamingClient[vmmdpb.DataRepairRehearsalChunk, vmmdpb.DataRepairRehearsalResult], error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding does not run repair rehearsals")
}

func (*fakeVmmdClient) RetireDataRepairRehearsal(context.Context, *vmmdpb.RetireDataRepairRehearsalRequest, ...grpc.CallOption) (*vmmdpb.RetireDataRepairRehearsalResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding does not retire repair runtimes")
}
