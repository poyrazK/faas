package vmmdgrpc

import (
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRegionalRuntimeWireRejectsMalformedMetadataOnEveryRestore(t *testing.T) {
	app := &vmmdpb.AppSpec{BaseKey: "/b", LayerKey: "/l", VcpuCount: 2, MemSizeMib: 256}
	for _, raw := range [][]byte{[]byte("null"), []byte(`{"Permit":{}}`), make([]byte, api.RegionalRuntimeHostRecordMaxBytes+1)} {
		if _, err := toColdBootRequest(t.Context(), &vmmdpb.CreateColdBootRequest{Instance: "instance", App: app, RegionalRuntime: raw}); err == nil {
			t.Fatal("cold boot ignored malformed metadata")
		}
		if _, err := toWakeRequest(t.Context(), &vmmdpb.CreateFromSnapshotRequest{Instance: "instance", App: app, RegionalRuntime: raw}); err == nil {
			t.Fatal("restore ignored malformed metadata")
		}
		if _, err := toMigrationWakeRequest(t.Context(), &vmmdpb.AdoptMigratedInstanceRequest{InstanceId: "instance", AppSpec: app, RegionalRuntime: raw}); err == nil {
			t.Fatal("migration ignored malformed metadata")
		}
		if _, err := appTaskWakeRequestFromProto(t.Context(), &vmmdpb.RestoreAppTaskRequest{Instance: "instance", App: app, DeploymentId: "deployment", RegionalRuntime: raw}); err == nil {
			t.Fatal("task restore ignored malformed metadata")
		}
	}
}
