// adr: 1151 — schedd's VMMClient against vmmd's real gRPC handlers: a freeze
// reports its timings, a busy guest's refusal reaches schedd as
// instance_busy (schedd then keeps it running), and thaw errors surface.
package vmmdgrpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sched"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// schedClient serves vmm through the real vmmd handlers and returns the
// client schedd uses for it.
func schedClient(t *testing.T, vmm *freezeStubVMM) *sched.VMMClient {
	t.Helper()
	server := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(server, New(vmm, nil, "1.17.0", nil))
	listener := bufconn.Listen(1024 * 1024)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///vmmd",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(); _ = listener.Close() })
	return sched.NewVMMClient(conn)
}

func TestFreezeContractSchedToVMMD(t *testing.T) {
	vmm := &freezeStubVMM{}
	client := schedClient(t, vmm)
	rep, err := client.FreezeInstance(t.Context(), "vm-a", false)
	if err != nil {
		t.Fatalf("FreezeInstance: %v", err)
	}
	if rep.Pause != 400*time.Microsecond || rep.ResidentBytesAfter != 60<<20 || len(vmm.freezes) != 1 || vmm.freezes[0].Reclaim {
		t.Fatalf("report %+v, freezes %+v; want the vmm's report and no reclaim", rep, vmm.freezes)
	}

	// The refusal schedd's FreezeIdle keys on: the guest keeps running.
	vmm.freezeErr = fmt.Errorf("freeze vm-a: %w", fcvm.ErrInstanceBusy)
	_, err = client.FreezeInstance(t.Context(), "vm-a", false)
	var prob *api.Problem
	if !errors.As(err, &prob) || prob.Code != api.CodeInstanceBusy {
		t.Fatalf("busy freeze reached schedd as %v; want problem code %s", err, api.CodeInstanceBusy)
	}

	result, err := client.ThawInstance(t.Context(), "vm-a", sched.ThawRequest{RunAfterRestore: true})
	if err != nil || len(vmm.thaws) != 1 || !vmm.thawOpts[0].RunResumeHook || !result.AfterRestoreRan {
		t.Fatalf("ThawInstance: %+v, %v, thaws %v %+v; want the resume hook run and confirmed", result, err, vmm.thaws, vmm.thawOpts)
	}
	vmm.thawErr = errors.New("thaw vm-a: instance is not live")
	if _, err := client.ThawInstance(t.Context(), "vm-a", sched.ThawRequest{}); err == nil {
		t.Fatal("a failed thaw reached schedd as success")
	}
}
