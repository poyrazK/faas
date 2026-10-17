package sched_test

// adr:1543 — managed probes require the original live caller and reviewed release.

import (
	"context"
	"net"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type serviceCallerNode struct {
	*legacyAliasNode
	supported bool
}

func (s *serviceCallerNode) RestoreManagedServiceProbe(ctx context.Context, req *vmmdpb.RestoreAppTaskRequest) (*vmmdpb.RestoreAppTaskResponse, error) {
	if !s.supported {
		return nil, status.Error(codes.Unimplemented, "old node")
	}
	return s.RestoreAppTask(ctx, req)
}

func TestVMMClientManagedServiceProbeRequiresLiveIdentityBeforeBoot(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "supported"}[supported], func(t *testing.T) {
			node := &serviceCallerNode{legacyAliasNode: &legacyAliasNode{}, supported: supported}
			server := grpc.NewServer()
			vmmdpb.RegisterVmmdServer(server, node)
			listener := bufconn.Listen(1024 * 1024)
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(server.Stop)
			conn, err := grpc.NewClient("passthrough:///service-caller", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close(); _ = listener.Close() })
			client := sched.NewVMMClient(conn)
			_, err = client.RestoreAppTask(t.Context(), sched.AppTaskRestoreSpec{Instance: "task", DeploymentID: "deployment", App: sched.AppSpec{Plan: api.PlanPro}, RequireServiceCallerIdentity: true})
			if (err == nil) != supported || node.boots != map[bool]int{false: 0, true: 1}[supported] {
				t.Fatalf("restore = %v, boot calls = %d", err, node.boots)
			}
		})
	}
}
