// adr: 732
package vmmdgrpc

import (
	"context"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// AppSpec.quarantine must reach fcvm on both the restore and the cold-boot
// wire, or a fork whose capture is unusable would cold-boot unquarantined.
func TestQuarantineSurvivesBothWakeWires(t *testing.T) {
	app := func(quarantine bool) *vmmdpb.AppSpec {
		return &vmmdpb.AppSpec{BaseKey: "/b", LayerKey: "/l", VcpuCount: 1, MemSizeMib: 256, Quarantine: quarantine}
	}
	for _, quarantine := range []bool{false, true} {
		wake, err := toWakeRequest(context.Background(), &vmmdpb.CreateFromSnapshotRequest{
			Instance: "fork-1", App: app(quarantine),
			Snapshot: &vmmdpb.SnapshotRef{VmstatePath: "/v", VmstateStorageKey: "snap/d/vmstate", FcVersion: "1.7.0", StorageKey: "snap/d/mem"},
		})
		if err != nil {
			t.Fatalf("toWakeRequest: %v", err)
		}
		cold, err := toColdBootRequest(context.Background(), &vmmdpb.CreateColdBootRequest{Instance: "fork-1", App: app(quarantine)})
		if err != nil {
			t.Fatalf("toColdBootRequest: %v", err)
		}
		if wake.Quarantine != quarantine || cold.Quarantine != quarantine {
			t.Fatalf("quarantine=%v: restore wire %v, cold-boot wire %v", quarantine, wake.Quarantine, cold.Quarantine)
		}
	}
}
