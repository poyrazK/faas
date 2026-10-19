package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func connectionCounts(remote []state.RealtimeAccountConnections) map[string]int64 {
	out := map[string]int64{}
	for _, r := range remote {
		out[r.AccountID] = r.Connections
	}
	return out
}

func TestPgStoreSyncRealtimeNodeConnections(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	a, b, c := uuid.NewString(), uuid.NewString(), uuid.NewString()
	sync := func(node string, counts ...state.RealtimeAccountConnections) map[string]int64 {
		t.Helper()
		remote, err := s.SyncRealtimeNodeConnections(ctx, node, counts)
		if err != nil {
			t.Fatalf("SyncRealtimeNodeConnections(%s): %v", node, err)
		}
		return connectionCounts(remote)
	}

	if got := sync("node-1", state.RealtimeAccountConnections{AccountID: a, Connections: 10}, state.RealtimeAccountConnections{AccountID: b, Connections: 3}); len(got) != 0 {
		t.Fatalf("first node sees remote %v, want none", got)
	}
	got := sync("node-2", state.RealtimeAccountConnections{AccountID: a, Connections: 4})
	if got[a] != 10 || got[b] != 3 || len(got) != 2 {
		t.Fatalf("node-2 remote = %v, want node-1's counts", got)
	}
	// node-1's own counts are never reported back to it.
	got = sync("node-1", state.RealtimeAccountConnections{AccountID: a, Connections: 12}, state.RealtimeAccountConnections{AccountID: c, Connections: 1})
	if got[a] != 4 || len(got) != 1 {
		t.Fatalf("node-1 remote = %v, want only node-2's account a", got)
	}
	// A sync replaces the node's set: b is gone, a and c updated.
	got = sync("node-3")
	if got[a] != 16 || got[c] != 1 || got[b] != 0 {
		t.Fatalf("node-3 remote = %v, want a=12+4, c=1, b removed", got)
	}

	// A node that stopped syncing stops counting.
	if _, err := pool.Exec(ctx, `update realtime_node_connection_syncs set synced_at=now()-interval '1 minute' where node_name='node-2'`); err != nil {
		t.Fatal(err)
	}
	if got = sync("node-3"); got[a] != 12 {
		t.Fatalf("remote with node-2 stale = %v, want a=12", got)
	}
	// After an hour of silence the node and its rows are removed.
	if _, err := pool.Exec(ctx, `update realtime_node_connection_syncs set synced_at=now()-interval '2 hours' where node_name='node-2'`); err != nil {
		t.Fatal(err)
	}
	sync("node-3")
	var rows int
	if err := pool.QueryRow(ctx, `select count(*) from realtime_node_connections where node_name='node-2'`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("silent node rows = %d, %v, want removed", rows, err)
	}

	for name, counts := range map[string][]state.RealtimeAccountConnections{
		"zero count":     {{AccountID: a, Connections: 0}},
		"bad account":    {{AccountID: "x", Connections: 1}},
		"duplicate":      {{AccountID: a, Connections: 1}, {AccountID: a, Connections: 2}},
		"negative count": {{AccountID: b, Connections: -1}},
	} {
		if _, err := s.SyncRealtimeNodeConnections(ctx, "node-1", counts); !errors.Is(err, state.ErrRealtimeConnectionSyncInvalid) {
			t.Fatalf("%s err = %v, want ErrRealtimeConnectionSyncInvalid", name, err)
		}
	}
	if _, err := s.SyncRealtimeNodeConnections(ctx, "", nil); !errors.Is(err, state.ErrRealtimeConnectionSyncInvalid) {
		t.Fatalf("empty node err = %v", err)
	}
}
