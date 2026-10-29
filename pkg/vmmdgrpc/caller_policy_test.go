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
