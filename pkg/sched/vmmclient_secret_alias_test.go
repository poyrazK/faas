// adr: 459 — environment intent and runtime ownership contracts.
package sched_test

import (
	"context"
	"net"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sched"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestVMMClientSecretAliasRoundTrip(t *testing.T) {
	var received fcvm.WakeRequest
	vm := &fakeVMM{wakeFn: func(ctx context.Context, request fcvm.WakeRequest) (*fcvm.Instance, error) {
		received = request
		return (&fakeVMM{}).Wake(ctx, request)
	}}
	client := newClient(t, vm)
	app := sched.AppSpec{BaseKey: "base", LayerKey: "layer", Plan: api.PlanPro, AccountID: "account", SealedEnv: []fcvm.SealedEnvEntry{{Key: "DATABASE_URL", SourceKey: "DATABASE_B", Ciphertext: []byte("sealed-bytes")}}}
	if _, err := client.CreateColdBoot(t.Context(), "alias-wire", app); err != nil {
		t.Fatal(err)
	}
	if len(received.SealedEnvEntries) != 1 || received.SealedEnvEntries[0].Key != "DATABASE_URL" || received.SealedEnvEntries[0].SourceKey != "DATABASE_B" {
		t.Fatalf("wire lost alias binding: %+v", received.SealedEnvEntries)
	}
}

type legacyAliasNode struct {
	vmmdpb.UnimplementedVmmdServer
	boots     int
	advertise bool
	destroyed []string
}

func (s *legacyAliasNode) Ping(context.Context, *vmmdpb.PingRequest) (*vmmdpb.PingResponse, error) {
	return &vmmdpb.PingResponse{FcVersion: "1.10.0", SupportsSecretAliases: s.advertise}, nil
}
func (s *legacyAliasNode) CreateColdBoot(context.Context, *vmmdpb.CreateColdBootRequest) (*vmmdpb.WakeResponse, error) {
	s.boots++
	return &vmmdpb.WakeResponse{}, nil
}
func (s *legacyAliasNode) CreateFromSnapshot(context.Context, *vmmdpb.CreateFromSnapshotRequest) (*vmmdpb.WakeResponse, error) {
	s.boots++
	return &vmmdpb.WakeResponse{}, nil
}
func (s *legacyAliasNode) AdoptMigratedInstance(context.Context, *vmmdpb.AdoptMigratedInstanceRequest) (*vmmdpb.AdoptMigratedInstanceResponse, error) {
	s.boots++
	return &vmmdpb.AdoptMigratedInstanceResponse{}, nil
}
func (s *legacyAliasNode) RestoreAppTask(context.Context, *vmmdpb.RestoreAppTaskRequest) (*vmmdpb.RestoreAppTaskResponse, error) {
	s.boots++
	return &vmmdpb.RestoreAppTaskResponse{}, nil
}

func TestVMMClientSecretAliasRefusesLegacyNodesBeforeBoot(t *testing.T) {
	node := &legacyAliasNode{}
	server := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(server, node)
	listener := bufconn.Listen(1024 * 1024)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///legacy-alias", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(); _ = listener.Close() })
	client := sched.NewVMMClient(conn)
	app := sched.AppSpec{Plan: api.PlanPro, SealedEnv: []fcvm.SealedEnvEntry{{Key: "DATABASE_URL", SourceKey: "DATABASE_B", Ciphertext: []byte("sealed")}}}
	for _, call := range []struct {
		name   string
		invoke func() error
	}{
		{"cold", func() error { _, err := client.CreateColdBoot(t.Context(), "alias", app); return err }},
		{"restore", func() error {
			_, err := client.CreateFromSnapshot(t.Context(), "alias", app, sched.SnapshotRef{})
			return err
		}},
		{"migration", func() error {
			_, err := client.AdoptMigratedInstance(t.Context(), "old", "alias", app, "mem", "state", "lease")
			return err
		}},
		{"app task", func() error {
			_, err := client.RestoreAppTask(t.Context(), sched.AppTaskRestoreSpec{Instance: "alias", App: app})
			return err
		}},
	} {
		t.Run(call.name, func(t *testing.T) {
			if err := call.invoke(); err == nil {
				t.Fatal("legacy node accepted alias delivery")
			}
		})
	}
	if node.boots != 0 {
		t.Fatalf("incompatible node received %d boot RPCs", node.boots)
	}
}

func (s *legacyAliasNode) Destroy(_ context.Context, request *vmmdpb.DestroyRequest) (*vmmdpb.DestroyResponse, error) {
	s.destroyed = append(s.destroyed, request.GetInstance())
	return &vmmdpb.DestroyResponse{}, nil
}

func TestVMMClientSecretAliasRequiresServingHandlerAcknowledgement(t *testing.T) {
	node := &legacyAliasNode{advertise: true}
	server := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(server, node)
	listener := bufconn.Listen(1024 * 1024)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///changed-alias-node", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(); _ = listener.Close() })
	client := sched.NewVMMClient(conn)
	app := sched.AppSpec{Plan: api.PlanPro, SealedEnv: []fcvm.SealedEnvEntry{{Key: "DATABASE_URL", SourceKey: "DATABASE_B", Ciphertext: []byte("sealed")}}}
	for _, call := range []struct {
		name   string
		invoke func() error
	}{
		{"cold", func() error { _, err := client.CreateColdBoot(t.Context(), "alias", app); return err }},
		{"restore", func() error {
			_, err := client.CreateFromSnapshot(t.Context(), "alias", app, sched.SnapshotRef{})
			return err
		}},
		{"migration", func() error {
			_, err := client.AdoptMigratedInstance(t.Context(), "old", "alias", app, "mem", "state", "lease")
			return err
		}},
		{"app task", func() error {
			_, err := client.RestoreAppTask(t.Context(), sched.AppTaskRestoreSpec{Instance: "alias", App: app})
			return err
		}},
	} {
		t.Run(call.name, func(t *testing.T) {
			before := len(node.destroyed)
			if err := call.invoke(); err == nil {
				t.Fatal("unacknowledged alias delivery was published")
			}
			if len(node.destroyed) != before+1 || node.destroyed[before] != "alias" {
				t.Fatal("incompatible boot was not torn down")
			}
		})
	}
	if node.boots != 4 {
		t.Fatalf("serving handler calls: %d", node.boots)
	}
}
