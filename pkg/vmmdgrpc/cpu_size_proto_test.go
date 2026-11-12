// adr: 20261109172421

package vmmdgrpc

import (
	"context"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// The instance's CPU weight and uncapped quota reach both wake paths; an
// older schedd's request carries neither.
func TestCPUWeightAndUncappedForwardedToWakeRequests(t *testing.T) {
	app := &vmmdpb.AppSpec{BaseKey: "/b", LayerKey: "/l", CpuWeight: 32, CpuUncapped: true}
	restore, err := toWakeRequest(context.Background(), &vmmdpb.CreateFromSnapshotRequest{Instance: "i-restore", App: app})
	if err != nil {
		t.Fatalf("toWakeRequest: %v", err)
	}
	cold, err := toColdBootRequest(context.Background(), &vmmdpb.CreateColdBootRequest{Instance: "i-cold", App: app})
	if err != nil {
		t.Fatalf("toColdBootRequest: %v", err)
	}
	for name, req := range map[string]struct {
		weight   int
		uncapped bool
	}{"restore": {restore.CPUWeight, restore.CPUUncapped}, "cold": {cold.CPUWeight, cold.CPUUncapped}} {
		if req.weight != 32 || !req.uncapped {
			t.Errorf("%s: weight %d uncapped %v, want 32 and true", name, req.weight, req.uncapped)
		}
	}
	legacy, err := toColdBootRequest(context.Background(), &vmmdpb.CreateColdBootRequest{Instance: "i-old", App: &vmmdpb.AppSpec{BaseKey: "/b", LayerKey: "/l"}})
	if err != nil || legacy.CPUWeight != 0 || legacy.CPUUncapped {
		t.Fatalf("older request: weight %d uncapped %v (err %v), want none", legacy.CPUWeight, legacy.CPUUncapped, err)
	}
}
