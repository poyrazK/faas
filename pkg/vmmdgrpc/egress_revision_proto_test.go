package vmmdgrpc

import (
	"context"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// The egress revision reaches both wake paths, so vmmd can prefer a request's
// newer policy over its cached one (see fcvm.wakeEgressNewerThanCache).
func TestEgressRevisionForwardedToWakeRequests(t *testing.T) {
	app := &vmmdpb.AppSpec{BaseKey: "/b", LayerKey: "/l", EgressAllowlist: []string{"198.18.0.254/32"}, EgressPorts: []uint32{5432}, EgressRevision: 7}
	restore, err := toWakeRequest(context.Background(), &vmmdpb.CreateFromSnapshotRequest{Instance: "i-restore", App: app})
	if err != nil {
		t.Fatalf("toWakeRequest: %v", err)
	}
	cold, err := toColdBootRequest(context.Background(), &vmmdpb.CreateColdBootRequest{Instance: "i-cold", App: app})
	if err != nil {
		t.Fatalf("toColdBootRequest: %v", err)
	}
	if restore.EgressRevision != 7 || cold.EgressRevision != 7 {
		t.Fatalf("egress revision restore=%d cold=%d, want 7", restore.EgressRevision, cold.EgressRevision)
	}
}
