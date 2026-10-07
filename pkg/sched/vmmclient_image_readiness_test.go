// adr:643
package sched_test

import (
	"context"
	"net"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/sched"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type imageCheckNode struct {
	vmmdpb.UnimplementedVmmdServer
	supported, verified           bool
	advertisedMonitor, monitoring bool
	boots, destroys               int
}

func (n *imageCheckNode) Ping(context.Context, *vmmdpb.PingRequest) (*vmmdpb.PingResponse, error) {
	return &vmmdpb.PingResponse{SupportsImageHealthcheck: n.supported, SupportsImageHealthcheckMonitoring: n.advertisedMonitor}, nil
}
func (n *imageCheckNode) CreateColdBoot(_ context.Context, r *vmmdpb.CreateColdBootRequest) (*vmmdpb.WakeResponse, error) {
	n.boots++
	return &vmmdpb.WakeResponse{Instance: r.GetInstance(), SupportsImageHealthcheck: n.supported, ImageHealthcheckVerified: n.verified, SupportsImageHealthcheckMonitoring: n.monitoring}, nil
}
func (n *imageCheckNode) CreateFromSnapshot(_ context.Context, r *vmmdpb.CreateFromSnapshotRequest) (*vmmdpb.WakeResponse, error) {
	n.boots++
	return &vmmdpb.WakeResponse{Instance: r.GetInstance(), SupportsImageHealthcheck: n.supported, ImageHealthcheckVerified: n.verified, SupportsImageHealthcheckMonitoring: n.monitoring}, nil
}
func (n *imageCheckNode) ResumeWarmInstance(_ context.Context, r *vmmdpb.ResumeWarmInstanceRequest) (*vmmdpb.ResumeWarmInstanceResponse, error) {
	n.boots++
	return &vmmdpb.ResumeWarmInstanceResponse{Instance: r.GetInstance(), ImageHealthcheckVerified: n.verified, SupportsImageHealthcheckMonitoring: n.monitoring}, nil
}
func (n *imageCheckNode) AdoptMigratedInstance(_ context.Context, _ *vmmdpb.AdoptMigratedInstanceRequest) (*vmmdpb.AdoptMigratedInstanceResponse, error) {
	n.boots++
	return &vmmdpb.AdoptMigratedInstanceResponse{ImageHealthcheckVerified: n.verified, SupportsImageHealthcheckMonitoring: n.monitoring}, nil
}
func (n *imageCheckNode) Destroy(context.Context, *vmmdpb.DestroyRequest) (*vmmdpb.DestroyResponse, error) {
	n.destroys++
	return &vmmdpb.DestroyResponse{}, nil
}

func TestImageHealthcheckClientRefusesUnverifiedBoots(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		supported, verified           bool
		advertisedMonitor, monitoring bool
		wantOK                        bool
		boots, destroys               int
	}{
		{name: "legacy daemon"},
		{name: "readiness-only daemon", supported: true, verified: true},
		{name: "changed serving handler", supported: true, advertisedMonitor: true, monitoring: true, boots: 4, destroys: 4},
		{name: "missing monitoring acknowledgement", supported: true, verified: true, advertisedMonitor: true, boots: 4, destroys: 4},
		{name: "verified", supported: true, verified: true, advertisedMonitor: true, monitoring: true, wantOK: true, boots: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := &imageCheckNode{supported: tc.supported, verified: tc.verified, advertisedMonitor: tc.advertisedMonitor, monitoring: tc.monitoring}
			server := grpc.NewServer()
			vmmdpb.RegisterVmmdServer(server, node)
			listener := bufconn.Listen(1024 * 1024)
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(server.Stop)
			conn, err := grpc.NewClient("passthrough:///image-healthcheck", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close(); _ = listener.Close() })
			client := sched.NewVMMClient(conn)
			app := sched.AppSpec{ImageHealthcheckRequired: true}
			for _, call := range []func() error{
				func() error { _, err := client.CreateColdBoot(t.Context(), "image", app); return err },
				func() error {
					_, err := client.CreateFromSnapshot(t.Context(), "image", app, sched.SnapshotRef{})
					return err
				},
				func() error { return client.ResumeWarmInstanceWithImageHealthcheck(t.Context(), "image") },
				func() error {
					_, err := client.AdoptMigratedInstance(t.Context(), "old", "image", app, "mem", "state", "lease")
					return err
				},
			} {
				if err := call(); (err == nil) != tc.wantOK {
					t.Fatalf("verified=%v error=%v", tc.verified, err)
				}
			}
			if node.boots != tc.boots || node.destroys != tc.destroys {
				t.Fatalf("boots/cleanup=%d/%d want=%d/%d", node.boots, node.destroys, tc.boots, tc.destroys)
			}
		})
	}
}
