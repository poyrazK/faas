package gateway

import (
	"context"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (*stubVmmdClient) CreateRehearsalColdBoot(context.Context, *vmmdpb.CreateColdBootRequest, ...grpc.CallOption) (*vmmdpb.WakeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding does not create rehearsal runtimes")
}

func (*stubVmmdClient) CreateRehearsalFromSnapshot(context.Context, *vmmdpb.CreateFromSnapshotRequest, ...grpc.CallOption) (*vmmdpb.WakeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding does not create rehearsal runtimes")
}

func (*stubVmmdClient) RehearseSuspendResume(context.Context, *vmmdpb.RehearseSuspendResumeRequest, ...grpc.CallOption) (*vmmdpb.RehearseSuspendResumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "gateway forwarding does not control rehearsal lifecycles")
}
