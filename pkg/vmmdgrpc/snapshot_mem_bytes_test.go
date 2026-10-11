// adr: 069

package vmmdgrpc

import (
	"context"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// vmmd restores a companion snapshot only when its recorded memory length
// equals the requested guest memory (companionSnapshotMemoryMatches). The
// wire SnapshotRef carried no length, so production-us rc.252 cold-booted
// every companion wake and marked each snapshot stale.
func TestWakeRequestCarriesSnapshotMemBytes(t *testing.T) {
	request, err := toWakeRequest(context.Background(), &vmmdpb.CreateFromSnapshotRequest{
		Instance: "inst-1",
		App:      &vmmdpb.AppSpec{BaseKey: "/b"},
		Snapshot: &vmmdpb.SnapshotRef{StorageKey: "snap/mem", VmstateStorageKey: "snap/vmstate", FcVersion: "1.7.0", MemBytes: 1140850688},
	})
	if err != nil {
		t.Fatalf("toWakeRequest: %v", err)
	}
	if request.Snapshot == nil || request.Snapshot.MemBytes != 1140850688 {
		t.Fatalf("wake snapshot = %+v; want MemBytes 1140850688", request.Snapshot)
	}
}
