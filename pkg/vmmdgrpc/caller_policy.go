package vmmdgrpc

import (
	"context"
	"strings"

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
// until daemons get distinct uids. builderd keeps Destroy for its own builder
// VMs; see builderMayCall.
var schedulerOnlyMethods = map[string]string{
	vmmdpb.Vmmd_ExecuteAppTask_FullMethodName:   "app task execution",
	vmmdpb.Vmmd_ExecForkCommand_FullMethodName:  "fork command execution",
	vmmdpb.Vmmd_ExecuteExecution_FullMethodName: "execution",
	vmmdpb.Vmmd_Destroy_FullMethodName:          "instance destruction",
}

// CallerPolicyUnaryInterceptor enforces schedulerOnlyMethods on unary RPCs.
func CallerPolicyUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := authorizeCaller(ctx, info.FullMethod, req); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// CallerPolicyStreamInterceptor enforces schedulerOnlyMethods on streams.
func CallerPolicyStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := authorizeCaller(ss.Context(), info.FullMethod, nil); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

func authorizeCaller(ctx context.Context, method string, req any) error {
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
	if cn == "schedd.faas" || (cn == "builderd.faas" && builderMayCall(method, req)) {
		return nil
	}
	return status.Error(codes.PermissionDenied, "only schedd may call "+what)
}

// builderInstancePrefix names the builder VMs builderd boots over TCP and must
// tear down itself (pkg/builderd/vm_metal.go). Rejecting those destroys failed
// every build on rc.301 and leaked the builder's cgroup, netns and jail.
const builderInstancePrefix = "build-"

// builderMayCall lets builderd's certificate destroy only builder instances.
// Tenant instance IDs are UUIDs minted by schedd, so they never carry the
// prefix, and builderd never retires rehearsal workers.
func builderMayCall(method string, req any) bool {
	if method != vmmdpb.Vmmd_Destroy_FullMethodName {
		return false
	}
	d, ok := req.(*vmmdpb.DestroyRequest)
	if !ok || d.GetRehearsalColdPreparationId() != "" || d.GetRehearsalWorkerFaultId() != "" {
		return false
	}
	id, found := strings.CutPrefix(d.GetInstance(), builderInstancePrefix)
	return found && id != ""
}
