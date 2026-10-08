// adr: 570
package vmmdgrpc

import (
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestTargetLifetimeServingNodeReachesColdAndRestore(t *testing.T) {
	for _, restore := range []bool{false, true} {
		vmm := &migrationHandlerVMM{}
		server := New(vmm, wire.NewOpsMetrics("vmmd-lifetime"), "1.10.0", nil).WithNodeID("serving-node")
		ctx := wire.WithContext(t.Context(), wire.CorrelationFields{AppID: "app", WakeID: "wake", NodeID: "caller-node"})
		app := &vmmdpb.AppSpec{AppId: "app", BaseKey: "base", LayerKey: "layer", VcpuCount: 1, MemSizeMib: 128}
		var err error
		if restore {
			_, err = server.CreateFromSnapshot(ctx, &vmmdpb.CreateFromSnapshotRequest{Instance: "instance", App: app})
		} else {
			_, err = server.CreateColdBoot(ctx, &vmmdpb.CreateColdBootRequest{Instance: "instance", App: app})
		}
		if err != nil {
			t.Fatal(err)
		}
		if vmm.wakeReq.NodeID != "serving-node" || vmm.wakeCorrelation.WakeID != "wake" {
			t.Fatalf("restore=%t node=%q correlation=%+v", restore, vmm.wakeReq.NodeID, vmm.wakeCorrelation)
		}
	}
}
