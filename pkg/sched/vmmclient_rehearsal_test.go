// adr: 20261017201005
package sched_test

import (
	"context"
	"net"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type rehearsalWireNode struct {
	vmmdpb.UnimplementedVmmdServer
	legacyBoots, containedBoots, destroyed int
	ack                                    string
}

func (n *rehearsalWireNode) CreateColdBoot(context.Context, *vmmdpb.CreateColdBootRequest) (*vmmdpb.WakeResponse, error) {
	n.legacyBoots++
	return &vmmdpb.WakeResponse{}, nil
}
func (n *rehearsalWireNode) CreateFromSnapshot(context.Context, *vmmdpb.CreateFromSnapshotRequest) (*vmmdpb.WakeResponse, error) {
	n.legacyBoots++
	return &vmmdpb.WakeResponse{}, nil
}
func (n *rehearsalWireNode) CreateRehearsalColdBoot(_ context.Context, r *vmmdpb.CreateColdBootRequest) (*vmmdpb.WakeResponse, error) {
	n.containedBoots++
	return &vmmdpb.WakeResponse{Instance: r.Instance, Netns: "fc-rehearsal", RehearsalRunId: n.ack}, nil
}
func (n *rehearsalWireNode) CreateRehearsalFromSnapshot(_ context.Context, r *vmmdpb.CreateFromSnapshotRequest) (*vmmdpb.WakeResponse, error) {
	n.containedBoots++
	return &vmmdpb.WakeResponse{Instance: r.Instance, Netns: "fc-rehearsal", RehearsalRunId: n.ack}, nil
}
func (n *rehearsalWireNode) Destroy(context.Context, *vmmdpb.DestroyRequest) (*vmmdpb.DestroyResponse, error) {
	n.destroyed++
	return &vmmdpb.DestroyResponse{}, nil
}

func rehearsalWireClient(t *testing.T, node vmmdpb.VmmdServer) *sched.VMMClient {
	t.Helper()
	s := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(s, node)
	l := bufconn.Listen(1024 * 1024)
	go func() { _ = s.Serve(l) }()
	t.Cleanup(func() { s.Stop(); _ = l.Close() })
	c, err := grpc.NewClient("passthrough:///rehearsal", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return l.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return sched.NewVMMClient(c)
}

func TestRehearsalNetworkClientRequiresDistinctMethodAndExactAck(t *testing.T) {
	const run = "abababababababababababababababab"
	app := sched.AppSpec{Plan: api.PlanPro, RehearsalRunID: run}
	for _, ack := range []string{"", run} {
		t.Run("ack="+ack, func(t *testing.T) {
			n := &rehearsalWireNode{ack: ack}
			c := rehearsalWireClient(t, n)
			_, coldErr := c.CreateColdBoot(t.Context(), "cold", app)
			_, restoreErr := c.CreateFromSnapshot(t.Context(), "restore", app, sched.SnapshotRef{})
			if (coldErr == nil) != (ack == run) || (restoreErr == nil) != (ack == run) {
				t.Fatalf("acknowledgement: %v %v", coldErr, restoreErr)
			}
			if n.legacyBoots != 0 || n.containedBoots != 2 || ack == "" && n.destroyed != 2 {
				t.Fatalf("boot routing/cleanup: %+v", n)
			}
		})
	}
	t.Run("old runtime", func(t *testing.T) {
		n := &legacyAliasNode{}
		c := rehearsalWireClient(t, n)
		if _, err := c.CreateColdBoot(t.Context(), "cold", app); err == nil {
			t.Fatal("legacy cold boot accepted")
		}
		if _, err := c.CreateFromSnapshot(t.Context(), "restore", app, sched.SnapshotRef{}); err == nil {
			t.Fatal("legacy restore accepted")
		}
		if n.boots != 0 {
			t.Fatal("old runtime started unrestricted code")
		}
	})
}
