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

type fakeEndpointConnectionStore struct {
	fakeConnectionStore
	endpoints       []state.RealtimeEndpointConnections
	remoteEndpoints []state.RealtimeEndpointConnections
}

func (f *fakeEndpointConnectionStore) SyncRealtimeNodeEndpointConnections(_ context.Context, node string, counts []state.RealtimeEndpointConnections) ([]state.RealtimeEndpointConnections, error) {
	if err := state.ValidateRealtimeEndpointConnectionSync(node, counts); err != nil {
		return nil, err
	}
	f.endpoints = counts
	return f.remoteEndpoints, nil
}

// Capped endpoints' counts ride the account sync. A refused endpoint set
// (as from an apid that predates the field) leaves the account sync working.
func TestSyncConnectionsRPCCarriesEndpoints(t *testing.T) {
	a, ep := uuid.NewString(), uuid.NewString()
	store := &fakeEndpointConnectionStore{remoteEndpoints: []state.RealtimeEndpointConnections{{EndpointID: ep, Connections: 4}}}
	store.remote = []state.RealtimeAccountConnections{{AccountID: a, Connections: 9}}
	receiver := &realtimeFleetReceiver{nodes: fakeUsageNodes{}, connections: store, endpointConnections: store}
	client := newUsageTestClient(t, receiver, "node-a")
	accounts, endpoints, err := client.SyncRealtimeConnectionsWithEndpoints(t.Context(), nil, []state.RealtimeEndpointConnections{{EndpointID: ep, Connections: 0}})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(store.endpoints) != 1 || store.endpoints[0].EndpointID != ep {
		t.Fatalf("stored endpoints = %+v", store.endpoints)
	}
	if len(accounts) != 1 || len(endpoints) != 1 || endpoints[0].Connections != 4 {
		t.Fatalf("remote = %+v %+v", accounts, endpoints)
	}
	refused := newUsageTestClient(t, receiver, "node-a")
	accounts, endpoints, err = refused.SyncRealtimeConnectionsWithEndpoints(t.Context(), nil, []state.RealtimeEndpointConnections{{EndpointID: "not-a-uuid"}})
	if err != nil || len(accounts) != 1 || endpoints != nil {
		t.Fatalf("refused endpoints: accounts %+v endpoints %+v err %v; want the account sync alone", accounts, endpoints, err)
	}
}
