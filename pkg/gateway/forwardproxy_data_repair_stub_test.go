// adr: 20261016
package gateway

import (
	"context"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Forwarding fixtures implement the client contract without granting repair
// authority or inventing guest retirement receipts.
func (*stubVmmdClient) DataRepairRehearsalIdentity(context.Context, *vmmdpb.DataRepairRehearsalIdentityRequest, ...grpc.CallOption) (*vmmdpb.DataRepairRehearsalIdentityResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding does not identify repair runtimes")
}

func (*stubVmmdClient) RunDataRepairRehearsal(context.Context, ...grpc.CallOption) (grpc.ClientStreamingClient[vmmdpb.DataRepairRehearsalChunk, vmmdpb.DataRepairRehearsalResult], error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding does not run repair rehearsals")
}

func (*stubVmmdClient) RetireDataRepairRehearsal(context.Context, *vmmdpb.RetireDataRepairRehearsalRequest, ...grpc.CallOption) (*vmmdpb.RetireDataRepairRehearsalResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding does not retire repair runtimes")
}
