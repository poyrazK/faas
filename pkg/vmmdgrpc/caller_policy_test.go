package vmmdgrpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"net"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

func tlsPeer(cn string) context.Context {
	leaf := &x509.Certificate{Subject: pkix.Name{CommonName: cn}}
	info := credentials.TLSInfo{State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{leaf}}}}
	return peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{}, AuthInfo: info})
}

// Any fleet-CA daemon certificate could execute commands in tenant VMs
// (bug hunt 2026-10-27 H17). Those RPCs now require schedd's certificate.
func TestCallerPolicy(t *testing.T) {
	unix := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.UnixAddr{Net: "unix", Name: "/run/faas/vmmd.sock"}})
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		method string
		req    any
		want   codes.Code
	}{
		{"schedd exec", tlsPeer("schedd.faas"), vmmdpb.Vmmd_ExecuteAppTask_FullMethodName, nil, codes.OK},
		{"gateway exec", tlsPeer("gatewayd-internal.faas"), vmmdpb.Vmmd_ExecuteAppTask_FullMethodName, nil, codes.PermissionDenied},
		{"gateway destroy", tlsPeer("gatewayd-internal.faas"), vmmdpb.Vmmd_Destroy_FullMethodName, nil, codes.PermissionDenied},
		{"gateway fork exec", tlsPeer("gatewayd-internal.faas"), vmmdpb.Vmmd_ExecForkCommand_FullMethodName, nil, codes.PermissionDenied},
		{"gateway attach", tlsPeer("gatewayd-internal.faas"), vmmdpb.Vmmd_AttachAppTask_FullMethodName, nil, codes.OK},
		// ADR-20261027090013: the gateway checked the session token.
		{"gateway sandbox call", tlsPeer("gatewayd-internal.faas"), vmmdpb.Vmmd_SandboxCall_FullMethodName, nil, codes.OK},
		{"schedd sandbox call", tlsPeer("schedd.faas"), vmmdpb.Vmmd_SandboxCall_FullMethodName, nil, codes.PermissionDenied},
		{"builderd sandbox call", tlsPeer("builderd.faas"), vmmdpb.Vmmd_SandboxCall_FullMethodName, nil, codes.PermissionDenied},
		{"unix socket sandbox call", unix, vmmdpb.Vmmd_SandboxCall_FullMethodName, nil, codes.OK},
		{"no peer sandbox call", context.Background(), vmmdpb.Vmmd_SandboxCall_FullMethodName, nil, codes.Unauthenticated},
		{"unix socket exec", unix, vmmdpb.Vmmd_ExecuteExecution_FullMethodName, nil, codes.OK},
		{"no peer exec", context.Background(), vmmdpb.Vmmd_ExecuteExecution_FullMethodName, nil, codes.Unauthenticated},
		// builderd tears down the builder VMs it boots (rc.301 failed every build).
		{"builderd destroys its builder", tlsPeer("builderd.faas"), vmmdpb.Vmmd_Destroy_FullMethodName, &vmmdpb.DestroyRequest{Instance: "build-01a184ea-be8e-7751-925f-b531d8e501d8"}, codes.OK},
		{"builderd destroys a tenant", tlsPeer("builderd.faas"), vmmdpb.Vmmd_Destroy_FullMethodName, &vmmdpb.DestroyRequest{Instance: "7f6c2a8e-0b1d-4c55-9a3e-2f1b6d9c4e10"}, codes.PermissionDenied},
		{"builderd destroys a bare prefix", tlsPeer("builderd.faas"), vmmdpb.Vmmd_Destroy_FullMethodName, &vmmdpb.DestroyRequest{Instance: "build-"}, codes.PermissionDenied},
		{"builderd retires a rehearsal worker", tlsPeer("builderd.faas"), vmmdpb.Vmmd_Destroy_FullMethodName, &vmmdpb.DestroyRequest{Instance: "build-x", RehearsalWorkerFaultId: "f"}, codes.PermissionDenied},
		{"builderd destroys without a request", tlsPeer("builderd.faas"), vmmdpb.Vmmd_Destroy_FullMethodName, nil, codes.PermissionDenied},
		{"builderd exec", tlsPeer("builderd.faas"), vmmdpb.Vmmd_ExecuteAppTask_FullMethodName, nil, codes.PermissionDenied},
		{"gateway destroys a builder", tlsPeer("gatewayd-internal.faas"), vmmdpb.Vmmd_Destroy_FullMethodName, &vmmdpb.DestroyRequest{Instance: "build-x"}, codes.PermissionDenied},
	} {
		if got := status.Code(authorizeCaller(tc.ctx, tc.method, tc.req)); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

// Every vmmd RPC names its certificate callers, so a new one is refused to
// all but unix-socket callers until it gets an entry (bug hunt H95).
func TestCallerPolicyCoversEveryMethod(t *testing.T) {
	desc := vmmdpb.Vmmd_ServiceDesc
	var methods []string
	for _, m := range desc.Methods {
		methods = append(methods, "/"+desc.ServiceName+"/"+m.MethodName)
	}
	for _, s := range desc.Streams {
		methods = append(methods, "/"+desc.ServiceName+"/"+s.StreamName)
	}
	for _, m := range methods {
		if vmmdCallerPolicy[m]&callerSchedd == 0 {
			t.Errorf("%s has no caller policy, or schedd may not call it", m)
		}
	}
	if len(vmmdCallerPolicy) != len(methods) {
		t.Errorf("policy has %d entries for %d methods: remove entries for deleted RPCs", len(vmmdCallerPolicy), len(methods))
	}
}

// A stolen gateway certificate reaches only the gateway's own RPCs, not the
// restore-then-exec path, instance lifecycle, egress policy or snapshots
// (bug hunt H95).
func TestCallerPolicyAllowList(t *testing.T) {
	for _, tc := range []struct {
		name, cn, method string
		req              any
		want             codes.Code
	}{
		{"gateway forwards", "gatewayd.faas", vmmdpb.Vmmd_ForwardHTTPStream_FullMethodName, nil, codes.OK},
		{"gateway resolves a service caller", "gatewayd.faas", vmmdpb.Vmmd_ResolveServiceCaller_FullMethodName, nil, codes.OK},
		{"gateway restores an app task", "gatewayd.faas", vmmdpb.Vmmd_RestoreAppTask_FullMethodName, nil, codes.PermissionDenied},
		{"gateway streams an app task", "gatewayd.faas", vmmdpb.Vmmd_ExecuteAppTaskStream_FullMethodName, nil, codes.PermissionDenied},
		{"gateway stops an instance", "gatewayd.faas", vmmdpb.Vmmd_StopInstance_FullMethodName, &vmmdpb.StopInstanceRequest{Instance: "7f6c2a8e"}, codes.PermissionDenied},
		{"gateway parks", "gatewayd.faas", vmmdpb.Vmmd_PauseAndSnapshot_FullMethodName, nil, codes.PermissionDenied},
		{"gateway opens egress", "gatewayd.faas", vmmdpb.Vmmd_UpdateEgressAllowlist_FullMethodName, nil, codes.PermissionDenied},
		{"schedd parks", "schedd.faas", vmmdpb.Vmmd_PauseAndSnapshot_FullMethodName, nil, codes.OK},
		{"builderd stops its builder", "builderd.faas", vmmdpb.Vmmd_StopInstance_FullMethodName, &vmmdpb.StopInstanceRequest{Instance: "build-abc"}, codes.OK},
		{"builderd stops a tenant", "builderd.faas", vmmdpb.Vmmd_StopInstance_FullMethodName, &vmmdpb.StopInstanceRequest{Instance: "7f6c2a8e"}, codes.PermissionDenied},
		{"builderd pings", "builderd.faas", vmmdpb.Vmmd_Ping_FullMethodName, nil, codes.OK},
		{"imaged mounts a parent", "imaged.faas", vmmdpb.Vmmd_MountOverlayParent_FullMethodName, nil, codes.OK},
		{"imaged destroys", "imaged.faas", vmmdpb.Vmmd_Destroy_FullMethodName, nil, codes.PermissionDenied},
		{"pgproxyd opens a session", "pgproxyd.faas", vmmdpb.Vmmd_DatabaseSession_FullMethodName, nil, codes.OK},
		{"pgproxyd controls a database", "pgproxyd.faas", vmmdpb.Vmmd_DatabaseControl_FullMethodName, nil, codes.PermissionDenied},
		{"database peer replicates", "vmmd.faas", vmmdpb.Vmmd_DatabaseReplication_FullMethodName, nil, codes.OK},
		{"unknown daemon pings", "meterd.faas", vmmdpb.Vmmd_Ping_FullMethodName, nil, codes.PermissionDenied},
		{"unknown method", "schedd.faas", "/onebox.faas.vmmd.v1.Vmmd/NotARealMethod", nil, codes.PermissionDenied},
	} {
		if got := status.Code(authorizeCaller(tlsPeer(tc.cn), tc.method, tc.req)); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}
