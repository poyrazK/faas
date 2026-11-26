package vmmdgrpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"net"
	"testing"

	"google.golang.org/grpc"
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

func TestCallerPolicyArchiveStreamsRequireScheduler(t *testing.T) {
	intercept := CallerPolicyStreamInterceptor()
	for _, method := range []string{
		vmmdpb.Vmmd_StreamDatabaseExport_FullMethodName,
		vmmdpb.Vmmd_StreamDatabaseArchiveImport_FullMethodName,
	} {
		for _, tc := range []struct {
			cn   string
			want codes.Code
		}{
			{"schedd.faas", codes.OK},
			{"pgproxyd.faas", codes.PermissionDenied},
			{"db-2.faas", codes.PermissionDenied},
		} {
			called := false
			stream := &recvStream{ctx: tlsPeer(tc.cn)}
			err := intercept(nil, stream, &grpc.StreamServerInfo{FullMethod: method}, func(any, grpc.ServerStream) error {
				called = true
				return nil
			})
			if status.Code(err) != tc.want || called != (tc.want == codes.OK) {
				t.Errorf("%s as %s: err = %v, handler called = %v", method, tc.cn, err, called)
			}
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
	// Sandbox session calls carry a session token only the gateway checks
	// (ADR-20261027090013), so schedd's certificate is refused them.
	gatewayOnly := map[string]bool{
		vmmdpb.Vmmd_SandboxCall_FullMethodName:             true,
		vmmdpb.Vmmd_PersistSandboxWorkspace_FullMethodName: true,
	}
	for _, m := range methods {
		want := callerSchedd
		if gatewayOnly[m] {
			want = callerGateway
		}
		if vmmdCallerPolicy[m]&want == 0 {
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
		{"schedd captures Run template", "schedd.faas", vmmdpb.Vmmd_CaptureExecutionTemplate_FullMethodName, nil, codes.OK},
		{"gateway cannot capture Run template", "gatewayd.faas", vmmdpb.Vmmd_CaptureExecutionTemplate_FullMethodName, nil, codes.PermissionDenied},
		{"builderd stops its builder", "builderd.faas", vmmdpb.Vmmd_StopInstance_FullMethodName, &vmmdpb.StopInstanceRequest{Instance: "build-abc"}, codes.OK},
		{"builderd stops a tenant", "builderd.faas", vmmdpb.Vmmd_StopInstance_FullMethodName, &vmmdpb.StopInstanceRequest{Instance: "7f6c2a8e"}, codes.PermissionDenied},
		{"builderd pings", "builderd.faas", vmmdpb.Vmmd_Ping_FullMethodName, nil, codes.OK},
		{"imaged mounts a parent", "imaged.faas", vmmdpb.Vmmd_MountOverlayParent_FullMethodName, nil, codes.OK},
		{"imaged destroys", "imaged.faas", vmmdpb.Vmmd_Destroy_FullMethodName, nil, codes.PermissionDenied},
		{"pgproxyd opens a session", "pgproxyd.faas", vmmdpb.Vmmd_DatabaseSession_FullMethodName, nil, codes.OK},
		{"pgproxyd opens a replica-only session", "pgproxyd.faas", vmmdpb.Vmmd_DatabaseReplicaSession_FullMethodName, nil, codes.OK},
		{"gateway cannot open a replica-only session", "gatewayd.faas", vmmdpb.Vmmd_DatabaseReplicaSession_FullMethodName, nil, codes.PermissionDenied},
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

func TestBuilderLogReadsRequireDecodedBuilderIdentity(t *testing.T) {
	for _, tc := range []struct {
		instance string
		want     codes.Code
	}{{"build-01a184ea-be8e-7751-925f-b531d8e501d8", codes.OK}, {"01a184ea-be8e-7751-925f-b531d8e501d8", codes.PermissionDenied}, {"", codes.PermissionDenied}, {"build-", codes.PermissionDenied}} {
		if got := status.Code(authorizeCaller(tlsPeer("builderd.faas"), vmmdpb.Vmmd_Logs_FullMethodName, &vmmdpb.LogsRequest{Instance: tc.instance})); got != tc.want {
			t.Fatal(tc.instance, got, tc.want)
		}
	}
}

type decodedLogStream struct {
	grpc.ServerStream
	ctx      context.Context
	instance string
}

func (s *decodedLogStream) Context() context.Context { return s.ctx }
func (s *decodedLogStream) RecvMsg(value any) error {
	value.(*vmmdpb.LogsRequest).Instance = s.instance
	return nil
}
func TestBuilderStreamingLogsReauthorizeDecodedRequest(t *testing.T) {
	for _, tc := range []struct {
		instance string
		want     codes.Code
	}{{"build-01a184ea-be8e-7751-925f-b531d8e501d8", codes.OK}, {"01a184ea-be8e-7751-925f-b531d8e501d8", codes.PermissionDenied}} {
		stream := &decodedLogStream{ctx: tlsPeer("builderd.faas"), instance: tc.instance}
		err := CallerPolicyStreamInterceptor()(nil, stream, &grpc.StreamServerInfo{FullMethod: vmmdpb.Vmmd_Logs_FullMethodName}, func(_ any, checked grpc.ServerStream) error { return checked.RecvMsg(&vmmdpb.LogsRequest{}) })
		if status.Code(err) != tc.want {
			t.Fatal(tc.instance, err)
		}
	}
}

type recvStream struct {
	grpc.ServerStream
	ctx context.Context
	req *vmmdpb.LogsRequest
}

func (r *recvStream) Context() context.Context { return r.ctx }
func (r *recvStream) RecvMsg(m any) error {
	m.(*vmmdpb.LogsRequest).Instance = r.req.GetInstance()
	return nil
}

// builderd follows its own builder's console for live build steps, and must
// never read a tenant VM's logs (retail-store feedback A3, 2026-11-18).
func TestCallerPolicyBuilderLogsOnlyItsBuilders(t *testing.T) {
	unix := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.UnixAddr{Net: "unix", Name: "/run/faas/vmmd.sock"}})
	interceptor := CallerPolicyStreamInterceptor()
	info := &grpc.StreamServerInfo{FullMethod: vmmdpb.Vmmd_Logs_FullMethodName, IsServerStream: true}
	for _, tc := range []struct {
		name     string
		ctx      context.Context
		instance string
		want     codes.Code
	}{
		{"builderd follows its builder", tlsPeer("builderd.faas"), "build-01a184ea", codes.OK},
		{"builderd follows a deferred cache VM", tlsPeer("builderd.faas"), "build-01a184ea-cache", codes.OK},
		{"builderd follows a tenant", tlsPeer("builderd.faas"), "7f6c2a8e-0b1d-4c55-9a3e-2f1b6d9c4e10", codes.PermissionDenied},
		{"builderd follows a bare prefix", tlsPeer("builderd.faas"), "build-", codes.PermissionDenied},
		{"schedd follows a tenant", tlsPeer("schedd.faas"), "7f6c2a8e", codes.OK},
		{"gateway follows a tenant", tlsPeer("gatewayd-internal.faas"), "7f6c2a8e", codes.OK},
		{"unix socket follows a tenant", unix, "7f6c2a8e", codes.OK},
		{"imaged follows a builder", tlsPeer("imaged.faas"), "build-x", codes.PermissionDenied},
	} {
		stream := &recvStream{ctx: tc.ctx, req: &vmmdpb.LogsRequest{Instance: tc.instance}}
		err := interceptor(nil, stream, info, func(_ any, ss grpc.ServerStream) error {
			return ss.RecvMsg(&vmmdpb.LogsRequest{})
		})
		if got := status.Code(err); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}
