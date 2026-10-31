package vmmdgrpc

// adr: 20261030230157 — vmmd's local data socket admits only the gateway's uid
// and serves only the gateway's RPCs.

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

func TestLocalDataCredsAdmitOnlyTheGatewayUID(t *testing.T) {
	for _, tc := range []struct {
		name    string
		peerUID func(net.Conn) (int, error)
		admit   bool
	}{
		{name: "gateway uid", peerUID: func(net.Conn) (int, error) { return 1001, nil }, admit: true},
		{name: "schedd uid", peerUID: func(net.Conn) (int, error) { return 1002, nil }},
		{name: "root", peerUID: func(net.Conn) (int, error) { return 0, nil }},
		{name: "no peer credentials", peerUID: func(net.Conn) (int, error) { return 0, errors.New("not a unix socket") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, client := net.Pipe()
			defer server.Close()
			defer client.Close()
			conn, info, err := localDataCreds{uid: 1001, peerUID: tc.peerUID}.ServerHandshake(server)
			if !tc.admit {
				if err == nil {
					t.Fatal("handshake admitted a peer that is not the gateway")
				}
				return
			}
			if err != nil || conn != server {
				t.Fatalf("handshake = %v, %v; want the connection admitted", conn, err)
			}
			if p, ok := info.(LocalDataPeer); !ok || p.UID != 1001 || p.AuthType() != "peercred" {
				t.Fatalf("AuthInfo = %#v, want LocalDataPeer for uid 1001", info)
			}
		})
	}
}

func TestLocalDataServesOnlyTheGatewayRPCs(t *testing.T) {
	for _, tc := range []struct {
		method string
		want   codes.Code
	}{
		{vmmdpb.Vmmd_ForwardHTTPStream_FullMethodName, codes.OK},
		{vmmdpb.Vmmd_ForwardTCPStream_FullMethodName, codes.OK},
		{vmmdpb.Vmmd_AttachAppTask_FullMethodName, codes.OK},
		{vmmdpb.Vmmd_Destroy_FullMethodName, codes.PermissionDenied},
		{vmmdpb.Vmmd_ExecuteAppTask_FullMethodName, codes.PermissionDenied},
		{vmmdpb.Vmmd_CreateAdmittedRuntime_FullMethodName, codes.PermissionDenied},
		{vmmdpb.Vmmd_CreateFromSnapshot_FullMethodName, codes.PermissionDenied},
		{vmmdpb.Vmmd_PauseAndSnapshot_FullMethodName, codes.PermissionDenied},
	} {
		if got := status.Code(authorizeLocalData(tc.method)); got != tc.want {
			t.Errorf("%s: code %v, want %v", tc.method, got, tc.want)
		}
	}
}

// The unix-socket shortcuts trust a peer without AuthInfo (ADR-015). A local
// data socket peer always carries one, so they never apply to it.
func TestLocalDataPeerIsNotATrustedUnixPeer(t *testing.T) {
	addr := &net.UnixAddr{Net: "unix", Name: "/run/faas/vmmd-data.sock"}
	trusted := peer.NewContext(context.Background(), &peer.Peer{Addr: addr})
	data := peer.NewContext(context.Background(), &peer.Peer{Addr: addr, AuthInfo: LocalDataPeer{UID: 1001}})

	if err := authorizeCaller(trusted, vmmdpb.Vmmd_Destroy_FullMethodName, &vmmdpb.DestroyRequest{}); err != nil {
		t.Fatalf("vmmd.sock peer refused: %v", err)
	}
	if err := authorizeCaller(data, vmmdpb.Vmmd_Destroy_FullMethodName, &vmmdpb.DestroyRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("data socket peer calling Destroy: %v, want Unauthenticated", err)
	}
	if err := authorizeSchedulerPeer(data, "runtime admission"); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("data socket peer asking for runtime admission: %v, want Unauthenticated", err)
	}
}

// vmmdCallPattern matches a method call with arguments, the shape of a vmmd
// client call (ctx first). Calls without arguments, such as a ring buffer's
// Stats(), are other types' methods.
var vmmdCallPattern = regexp.MustCompile(`\.([A-Z][A-Za-z0-9]+)\([A-Za-z_]`)

// TestLocalDataMethodsCoverTheGateway scans gatewayd-internal and pkg/gateway
// for calls named like a vmmd RPC. Each must be on the allowlist, or the
// gateway's own node would get PermissionDenied for it over the local data
// socket; each allowlisted RPC must still be called.
func TestLocalDataMethodsCoverTheGateway(t *testing.T) {
	rpcs := map[string]string{}
	for _, m := range vmmdpb.Vmmd_ServiceDesc.Methods {
		rpcs[m.MethodName] = "/" + vmmdpb.Vmmd_ServiceDesc.ServiceName + "/" + m.MethodName
	}
	for _, s := range vmmdpb.Vmmd_ServiceDesc.Streams {
		rpcs[s.StreamName] = "/" + vmmdpb.Vmmd_ServiceDesc.ServiceName + "/" + s.StreamName
	}
	called := map[string][]string{}
	for _, dir := range []string{"../../cmd/gatewayd-internal", "../gateway"} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range vmmdCallPattern.FindAllSubmatch(body, -1) {
				if full, ok := rpcs[string(m[1])]; ok && !slices.Contains(called[full], path) {
					called[full] = append(called[full], path)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", dir, err)
		}
	}
	for method, files := range called {
		if !localDataMethods[method] {
			t.Errorf("%s is called in %v but the local data socket refuses it: add it to localDataMethods (and ADR-20261030230157), or rename the unrelated method", method, files)
		}
	}
	for method := range localDataMethods {
		if len(called[method]) == 0 {
			t.Errorf("%s is on the local data socket's allowlist but the gateway no longer calls it", method)
		}
	}
}
