package receivers

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/state"
)

type fakeConnectionStore struct {
	node   string
	counts []state.RealtimeAccountConnections
	remote []state.RealtimeAccountConnections
}

func (f *fakeConnectionStore) SyncRealtimeNodeConnections(_ context.Context, node string, counts []state.RealtimeAccountConnections) ([]state.RealtimeAccountConnections, error) {
	if err := state.ValidateRealtimeConnectionSync(node, counts); err != nil {
		return nil, err
	}
	f.node, f.counts = node, counts
	return f.remote, nil
}

func TestSyncConnectionsRPC(t *testing.T) {
	a, b := uuid.NewString(), uuid.NewString()
	store := &fakeConnectionStore{remote: []state.RealtimeAccountConnections{{AccountID: b, Connections: 7}}}
	client := newUsageTestClient(t, &realtimeFleetReceiver{nodes: fakeUsageNodes{}, connections: store}, "node-a")
	remote, err := client.SyncRealtimeConnections(t.Context(), []state.RealtimeAccountConnections{{AccountID: a, Connections: 3}})
	if err != nil {
		t.Fatalf("SyncRealtimeConnections: %v", err)
	}
	if store.node != "node-a" || len(store.counts) != 1 || store.counts[0].Connections != 3 {
		t.Fatalf("stored %s %+v, want node-a's count", store.node, store.counts)
	}
	if len(remote) != 1 || remote[0].AccountID != b || remote[0].Connections != 7 {
		t.Fatalf("remote = %+v", remote)
	}
	if _, err := client.SyncRealtimeConnections(t.Context(), nil); err != nil {
		t.Fatalf("an empty node must still sync: %v", err)
	}
	if _, err := client.SyncRealtimeConnections(t.Context(), []state.RealtimeAccountConnections{{AccountID: a, Connections: 0}}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("zero count err = %v, want InvalidArgument", err)
	}
	unknown := newUsageTestClient(t, &realtimeFleetReceiver{nodes: fakeUsageNodes{}, connections: store}, "node-x")
	if _, err := unknown.SyncRealtimeConnections(t.Context(), nil); status.Code(err) != codes.NotFound {
		t.Fatalf("unregistered node err = %v, want NotFound", err)
	}
	disabled := newUsageTestClient(t, &realtimeFleetReceiver{nodes: fakeUsageNodes{}}, "node-a")
	if _, err := disabled.SyncRealtimeConnections(t.Context(), nil); status.Code(err) != codes.Unavailable {
		t.Fatalf("no store err = %v, want Unavailable", err)
	}
}
