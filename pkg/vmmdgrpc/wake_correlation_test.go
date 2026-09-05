package vmmdgrpc_test

import (
	"context"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Exercise the real gRPC boundary without OTel/interceptors: the metadata
// envelope must reach the context that Manager and restore timing emitters read.
func TestWakeCorrelationReachesVMM(t *testing.T) {
	for _, restore := range []bool{false, true} {
		name := "cold_boot"
		if restore {
			name = "restore"
		}
		t.Run(name, func(t *testing.T) {
			want := wire.CorrelationFields{
				RequestID: "request-1", WakeID: "wake-1", AppID: "app-1",
				DeploymentID: "deployment-1", InstanceID: "instance-1",
				TraceID: "trace-1", SpanID: "span-1", InvocationID: "invocation-1",
				Trigger: "gateway", QueuedCount: 3, ConcurrencyAtAdmit: 2,
			}
			seen := make(chan wire.CorrelationFields, 1)
			fake := &fakeVMM{wakeFn: func(ctx context.Context, req fcvm.WakeRequest) (*fcvm.Instance, error) {
				fields, _ := wire.FromContext(ctx)
				seen <- fields
				return &fcvm.Instance{Lease: fcvm.Lease{Instance: req.Instance}, Method: fcvm.WakeRestore}, nil
			}}
			cli, _ := newServer(t, fake)
			ctx := wire.WithCorrelationOutgoing(t.Context(), want)
			app := &vmmdpb.AppSpec{BaseKey: "/base", LayerKey: "/layer", VcpuCount: 1, MemSizeMib: 128}
			var err error
			if restore {
				_, err = cli.CreateFromSnapshot(ctx, &vmmdpb.CreateFromSnapshotRequest{Instance: "instance-1", App: app})
			} else {
				_, err = cli.CreateColdBoot(ctx, &vmmdpb.CreateColdBootRequest{Instance: "instance-1", App: app})
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := <-seen; got != want {
				t.Fatalf("VMM correlation = %+v, want %+v", got, want)
			}
		})
	}
}
