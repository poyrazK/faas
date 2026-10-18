// adr: 1151 — the vmmd client carries freeze and thaw over the wire and lifts
// vmmd's frozen-tier refusals to their stable codes.
package sched_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/sched"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type frozenNode struct {
	vmmdpb.UnimplementedVmmdServer
	freeze *vmmdpb.FreezeInstanceRequest
	thaw   *vmmdpb.ThawInstanceRequest
	busy   bool
}

func (n *frozenNode) FreezeInstance(_ context.Context, r *vmmdpb.FreezeInstanceRequest) (*vmmdpb.FreezeInstanceResponse, error) {
	n.freeze = r
	if n.busy {
		return nil, grpcerr.ToStatus(api.NewProblem(409, api.CodeInstanceBusy, "Instance busy", "serving a request"))
	}
	return &vmmdpb.FreezeInstanceResponse{Instance: r.GetInstance(), PauseMicros: 400, ReclaimMicros: 9000,
		ResidentBytesBefore: 200 << 20, ResidentBytesAfter: 60 << 20}, nil
}

func (n *frozenNode) ThawInstance(_ context.Context, r *vmmdpb.ThawInstanceRequest) (*vmmdpb.ThawInstanceResponse, error) {
	n.thaw = r
	return &vmmdpb.ThawInstanceResponse{Instance: r.GetInstance(), ImageHealthcheckVerified: r.GetImageHealthcheckRequired(), AfterRestoreRan: r.GetRunAfterRestore()}, nil
}

func frozenClient(t *testing.T, node *frozenNode) *sched.VMMClient {
	t.Helper()
	server := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(server, node)
	listener := bufconn.Listen(1024 * 1024)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///frozen", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(); _ = listener.Close() })
	return sched.NewVMMClient(conn)
}

func TestVMMClientFreezeAndThaw(t *testing.T) {
	node := &frozenNode{}
	client := frozenClient(t, node)
	rep, err := client.FreezeInstance(t.Context(), "inst", true)
	if err != nil {
		t.Fatal(err)
	}
	want := sched.FreezeReport{Pause: 400 * time.Microsecond, Reclaim: 9 * time.Millisecond,
		ResidentBytesBefore: 200 << 20, ResidentBytesAfter: 60 << 20}
	if rep != want || node.freeze.GetInstance() != "inst" || !node.freeze.GetReclaim() {
		t.Fatalf("FreezeInstance = %+v (request %v), want %+v", rep, node.freeze, want)
	}
	result, err := client.ThawInstance(t.Context(), "inst", sched.ThawRequest{ImageHealthcheckRequired: true, RunAfterRestore: true})
	if err != nil || !result.HealthcheckVerified || !result.AfterRestoreRan || node.thaw.GetInstance() != "inst" ||
		!node.thaw.GetImageHealthcheckRequired() || !node.thaw.GetRunAfterRestore() {
		t.Fatalf("ThawInstance = %+v, %v (request %v)", result, err, node.thaw)
	}
}

func TestVMMClientFreezeLiftsBusyRefusal(t *testing.T) {
	client := frozenClient(t, &frozenNode{busy: true})
	_, err := client.FreezeInstance(t.Context(), "inst", false)
	var prob *api.Problem
	if !errors.As(err, &prob) || prob.Code != api.CodeInstanceBusy {
		t.Fatalf("FreezeInstance on a busy instance = %v, want %s", err, api.CodeInstanceBusy)
	}
}
