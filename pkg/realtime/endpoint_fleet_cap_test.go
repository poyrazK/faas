package realtime

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeEndpointSyncer struct {
	fakeConnectionSyncer
	mu        sync.Mutex
	endpoints []state.RealtimeEndpointConnections
	sentEP    [][]state.RealtimeEndpointConnections
}

func (f *fakeEndpointSyncer) SyncRealtimeConnectionsWithEndpoints(ctx context.Context, counts []state.RealtimeAccountConnections, endpoints []state.RealtimeEndpointConnections) ([]state.RealtimeAccountConnections, []state.RealtimeEndpointConnections, error) {
	accounts, err := f.SyncRealtimeConnections(ctx, counts)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sentEP = append(f.sentEP, endpoints)
	return accounts, f.endpoints, err
}

func (f *fakeEndpointSyncer) setEndpoints(endpoints []state.RealtimeEndpointConnections) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.endpoints = endpoints
}

// A customer-set max_connections holds across nodes: connections the other
// nodes report count against it. Before, each node allowed the full cap, so
// two nodes admitted twice the endpoint's max_connections.
func TestEndpointMaxConnectionsCountsOtherNodes(t *testing.T) {
	syncer := &fakeEndpointSyncer{}
	// The syncer is attached after the Manager starts, so its background
	// sync loop never runs and the test drives every sync itself.
	node := newFairUseNode(t, Config{})
	node.m.cfg.ConnectionSync = syncer
	register := func(limit int) string {
		id := uuid.NewString()
		if err := node.m.RegisterEndpoint(Endpoint{Preview: true, ID: id, AppID: "app", AccountID: uuid.NewString(), MaxConnections: limit,
			Authorize: func(context.Context, *http.Request) (string, error) { return "", nil }}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	capped := register(2)
	uncapped := register(0)
	// Another node holds one connection to each endpoint.
	syncer.setEndpoints([]state.RealtimeEndpointConnections{{EndpointID: capped, Connections: 1}, {EndpointID: uncapped, Connections: 1}})
	node.m.syncConnections(context.Background())

	syncer.mu.Lock()
	sent := syncer.sentEP[len(syncer.sentEP)-1]
	syncer.mu.Unlock()
	if len(sent) != 1 || sent[0].EndpointID != capped || sent[0].Connections != 0 {
		t.Fatalf("reported endpoints = %+v, want only the capped endpoint with 0", sent)
	}
	if _, status, _ := node.dial(t, capped); status != http.StatusSwitchingProtocols {
		t.Fatalf("first local connection refused with %d", status)
	}
	_, status, problem := node.dial(t, capped)
	if status != http.StatusTooManyRequests || problem.Limit == nil || *problem.Limit != 2 || problem.Observed == nil || *problem.Observed != 2 {
		t.Fatalf("second local connection = %d %+v, want 429 with limit 2 observed 2 (one here, one elsewhere)", status, problem)
	}
	// An endpoint without its own cap is bounded per node only.
	for range 2 {
		if _, status, _ := node.dial(t, uncapped); status != http.StatusSwitchingProtocols {
			t.Fatalf("uncapped endpoint refused with %d", status)
		}
	}
	// When the other node's connection goes, the next sync frees the slot.
	syncer.setEndpoints(nil)
	node.m.syncConnections(context.Background())
	if _, status, _ := node.dial(t, capped); status != http.StatusSwitchingProtocols {
		t.Fatalf("connection after the remote one closed refused with %d", status)
	}
}
