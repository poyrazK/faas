package e2etest

import (
	"fmt"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// The portable transport must preserve the booted guest shape. Otherwise
// imaged rejects its captures and restore tests silently exercise cold boots.
func TestFakeVMMDCaptureAndStatsPreserveBootMemory(t *testing.T) {
	for _, ram := range []int32{256, 512, 1024} {
		for _, restore := range []bool{false, true} {
			t.Run(fmt.Sprintf("ram-%d/restore-%t", ram, restore), func(t *testing.T) {
				server := &FakeVMMD{}
				app := &vmmdpb.AppSpec{MemSizeMib: ram}
				var err error
				if restore {
					_, err = server.CreateFromSnapshot(t.Context(), &vmmdpb.CreateFromSnapshotRequest{Instance: "guest", App: app})
				} else {
					_, err = server.CreateColdBoot(t.Context(), &vmmdpb.CreateColdBootRequest{Instance: "guest", App: app})
				}
				if err != nil {
					t.Fatal(err)
				}
				snapshot, err := server.PauseAndSnapshot(t.Context(), &vmmdpb.PauseAndSnapshotRequest{Instance: "guest"})
				want := int64(ram) << 20
				if err != nil || snapshot.GetMemBytes() != want || snapshot.GetStoredBytes() != want+snapshot.GetVmstateBytes() {
					t.Fatalf("capture changed guest memory: %+v %v", snapshot, err)
				}
				stats, err := server.Stats(t.Context(), &vmmdpb.StatsRequest{})
				if err != nil || len(stats.GetInstances()) != 1 || stats.GetTotalResidentBytes().GetValue() != want || stats.GetInstances()[0].GetResidentBytes().GetValue() != want {
					t.Fatalf("stats changed guest memory: %+v %v", stats, err)
				}
			})
		}
	}
}
