// adr: 429 — verify the exact node acknowledgment of a complete policy.
package sched_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/sched"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type policyWireServer struct {
	vmmdpb.UnimplementedVmmdServer
	update func(*vmmdpb.UpdateAppEgressPolicyRequest) (*vmmdpb.UpdateAppEgressPolicyAck, error)
}

func (s *policyWireServer) UpdateAppEgressPolicy(_ context.Context, req *vmmdpb.UpdateAppEgressPolicyRequest) (*vmmdpb.UpdateAppEgressPolicyAck, error) {
	return s.update(req)
}

func newPolicyWireClient(t *testing.T, server vmmdpb.VmmdServer) *sched.VMMClient {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(srv, server)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	conn, err := grpc.NewClient("passthrough://bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	client := sched.NewVMMClient(conn)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestVMMClientRevisionedEgressAck(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ack     int64
		rpcErr  error
		wantErr bool
	}{
		{"exact", 9, nil, false},
		{"older", 8, nil, true},
		{"newer", 10, nil, true},
		{"missing", 0, nil, true},
		{"physical failure", 0, errors.New("physical failure"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newPolicyWireClient(t, &policyWireServer{update: func(req *vmmdpb.UpdateAppEgressPolicyRequest) (*vmmdpb.UpdateAppEgressPolicyAck, error) {
				if req.GetAppId() != "app" || req.GetRevision() != 9 || !slices.Equal(req.GetEgressAllowlist(), []string{"8.8.8.0/24"}) || !slices.Equal(req.GetEgressPorts(), []uint32{5432, 6379}) {
					t.Errorf("wrong wire tuple: %v", req)
				}
				return &vmmdpb.UpdateAppEgressPolicyAck{Revision: tc.ack}, tc.rpcErr
			}})
			err := client.UpdateAppEgressPolicy(t.Context(), "app", 9, []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24")}, []int{5432, 6379})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v want error=%v", err, tc.wantErr)
			}
		})
	}
}

func TestVMMClientRevisionedEgressOldNodeAndInvalidPorts(t *testing.T) {
	client := newPolicyWireClient(t, &vmmdpb.UnimplementedVmmdServer{})
	if err := client.UpdateAppEgressPolicy(t.Context(), "app", 1, nil, nil); err == nil {
		t.Fatal("old node falsely acknowledged revision")
	}
	calls := 0
	client = newPolicyWireClient(t, &policyWireServer{update: func(req *vmmdpb.UpdateAppEgressPolicyRequest) (*vmmdpb.UpdateAppEgressPolicyAck, error) {
		calls++
		return &vmmdpb.UpdateAppEgressPolicyAck{Revision: req.GetRevision()}, nil
	}})
	for _, port := range []int{0, -1, 65536, 25} {
		if err := client.UpdateAppEgressPolicy(t.Context(), "app", 1, nil, []int{port}); err == nil {
			t.Fatalf("invalid port %d silently dropped", port)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid projection sent %d RPCs", calls)
	}
}
