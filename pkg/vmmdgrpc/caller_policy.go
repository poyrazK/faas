package vmmdgrpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/wire"
)

// schedulerOnlyMethods run commands inside tenant VMs or destroy them. Only
// schedd calls them (pkg/sched). vmmd authorized every caller by transport
// alone, so any daemon certificate from the fleet CA, including the
// internet-facing gatewayd-internal of another node, could execute commands
// in this node's tenant VMs (bug hunt 2026-10-27 H17). A certificate caller
// must now be schedd. Unix-socket callers keep the filesystem boundary:
// every daemon runs as one uid, so peer credentials cannot tell them apart
// until daemons get distinct uids.
var schedulerOnlyMethods = map[string]string{
	vmmdpb.Vmmd_ExecuteAppTask_FullMethodName:   "app task execution",
	vmmdpb.Vmmd_ExecForkCommand_FullMethodName:  "fork command execution",
	vmmdpb.Vmmd_ExecuteExecution_FullMethodName: "execution",
	vmmdpb.Vmmd_Destroy_FullMethodName:          "instance destruction",
}

// CallerPolicyUnaryInterceptor enforces schedulerOnlyMethods on unary RPCs.
func CallerPolicyUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := authorizeCaller(ctx, info.FullMethod); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// CallerPolicyStreamInterceptor enforces schedulerOnlyMethods on streams.
func CallerPolicyStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := authorizeCaller(ss.Context(), info.FullMethod); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

func authorizeCaller(ctx context.Context, method string) error {
	what, restricted := schedulerOnlyMethods[method]
	if !restricted {
		return nil
	}
	if p, ok := peer.FromContext(ctx); ok && p != nil && p.AuthInfo == nil && p.Addr != nil && p.Addr.Network() == "unix" {
		return nil
	}
	cn, err := wire.PeerCN(ctx)
	if err != nil {
		return status.Error(codes.Unauthenticated, what+" requires verified daemon credentials")
	}
	if cn != "schedd.faas" {
		return status.Error(codes.PermissionDenied, "only schedd may call "+what)
	}
	return nil
}
