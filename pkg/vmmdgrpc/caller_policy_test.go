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
		want   codes.Code
	}{
		{"schedd exec", tlsPeer("schedd.faas"), vmmdpb.Vmmd_ExecuteAppTask_FullMethodName, codes.OK},
		{"gateway exec", tlsPeer("gatewayd-internal.faas"), vmmdpb.Vmmd_ExecuteAppTask_FullMethodName, codes.PermissionDenied},
		{"gateway destroy", tlsPeer("gatewayd-internal.faas"), vmmdpb.Vmmd_Destroy_FullMethodName, codes.PermissionDenied},
		{"gateway fork exec", tlsPeer("gatewayd-internal.faas"), vmmdpb.Vmmd_ExecForkCommand_FullMethodName, codes.PermissionDenied},
		{"gateway attach", tlsPeer("gatewayd-internal.faas"), vmmdpb.Vmmd_AttachAppTask_FullMethodName, codes.OK},
		{"unix socket exec", unix, vmmdpb.Vmmd_ExecuteExecution_FullMethodName, codes.OK},
		{"no peer exec", context.Background(), vmmdpb.Vmmd_ExecuteExecution_FullMethodName, codes.Unauthenticated},
	} {
		if got := status.Code(authorizeCaller(tc.ctx, tc.method)); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}
