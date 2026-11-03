package receivers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1323 (amendment of 2026-11-03) — ephemeral relays reuse their endpoint
// and node lookups for a few seconds instead of reading Postgres per frame;
// failed reads are not cached.
func TestRealtimeRelayLookupsAreCached(t *testing.T) {
	lookups := newRealtimeRelayLookups()
	now := time.Unix(1_000, 0)
	lookups.now = func() time.Time { return now }
	endpointReads, nodeReads := 0, 0
	fail := true
	readEndpoint := func(context.Context, string) (state.ManagedRealtimeEndpoint, error) {
		endpointReads++
		if fail {
			return state.ManagedRealtimeEndpoint{}, errors.New("down")
		}
		return state.ManagedRealtimeEndpoint{Enabled: true}, nil
	}
	readNode := func(_ context.Context, name string) (state.ComputeNode, error) {
		nodeReads++
		return state.ComputeNode{ID: "id-" + name, Active: true}, nil
	}
	ctx := context.Background()
	if _, err := lookups.endpointEnabled(ctx, "ep", readEndpoint); err == nil {
		t.Fatal("a failed read answered")
	}
	fail = false
	for range 3 {
		if enabled, err := lookups.endpointEnabled(ctx, "ep", readEndpoint); err != nil || !enabled {
			t.Fatalf("endpoint = (%v, %v)", enabled, err)
		}
		if node, err := lookups.node(ctx, "node-a", readNode); err != nil || node.ID != "id-node-a" {
			t.Fatalf("node = (%+v, %v)", node, err)
		}
	}
	if endpointReads != 2 || nodeReads != 1 {
		t.Fatalf("reads = endpoint %d, node %d; want 2 (one failed) and 1", endpointReads, nodeReads)
	}
	now = now.Add(realtimeRelayLookupTTL)
	_, _ = lookups.endpointEnabled(ctx, "ep", readEndpoint)
	_, _ = lookups.node(ctx, "node-a", readNode)
	if endpointReads != 3 || nodeReads != 2 {
		t.Fatalf("after the TTL reads = endpoint %d, node %d", endpointReads, nodeReads)
	}
}
