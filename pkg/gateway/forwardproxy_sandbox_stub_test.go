package gateway

// Sandbox session RPCs (ADR-20261027090013) are served by the sandboxdata
// handler, not the forward proxy; the gateway's vmmd fakes refuse them.

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

func (*stubVmmdClient) RunSandbox(context.Context, ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.RunSandboxRequest, vmmdpb.RunSandboxEvent], error) {
	return nil, status.Error(codes.Unimplemented, "sandbox sessions are not used by HTTP gateway tests")
}

func (*stubVmmdClient) SandboxCall(context.Context, ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.SandboxCallRequest, vmmdpb.SandboxCallEvent], error) {
	return nil, status.Error(codes.Unimplemented, "sandbox sessions are not used by HTTP gateway tests")
}

func (*stubVmmdClient) PersistSandboxWorkspace(context.Context, *vmmdpb.PersistSandboxWorkspaceRequest, ...grpc.CallOption) (*vmmdpb.PersistSandboxWorkspaceResponse, error) {
	return nil, status.Error(codes.Unimplemented, "sandbox sessions are not used by HTTP gateway tests")
}

func (*stubVmmdClient) CaptureSandboxSnapshot(context.Context, *vmmdpb.CaptureSandboxSnapshotRequest, ...grpc.CallOption) (*vmmdpb.CaptureAgentSnapshotResponse, error) {
	return nil, status.Error(codes.Unimplemented, "sandbox sessions are not used by HTTP gateway tests")
}
