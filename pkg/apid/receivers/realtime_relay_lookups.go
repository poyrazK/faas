package receivers

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// realtimeRelayLookupTTL is how long a relay reuses its endpoint and node
// lookups. Signals, presence updates and read receipts are relayed many times
// a second; each used to read both rows from Postgres (ADR-1323, amendment
// of 2026-11-03). A disabled endpoint or a deactivated node stops relaying
// within this long; connections themselves close through the endpoint and
// node reconcilers as before.
const realtimeRelayLookupTTL = 5 * time.Second

// realtimeRelayLookupMax bounds each cache; past it the cache starts over.
const realtimeRelayLookupMax = 4096

type realtimeRelayLookups struct {
	mu        sync.Mutex
	now       func() time.Time
	endpoints map[string]relayEndpointLookup
	nodes     map[string]relayNodeLookup
}

type relayEndpointLookup struct {
	enabled bool
	at      time.Time
}

type relayNodeLookup struct {
	node state.ComputeNode
	at   time.Time
}

func newRealtimeRelayLookups() *realtimeRelayLookups {
	return &realtimeRelayLookups{now: time.Now, endpoints: map[string]relayEndpointLookup{}, nodes: map[string]relayNodeLookup{}}
}

// endpointEnabled answers from the cache, else from read. Only answers are
// cached; errors are not.
func (l *realtimeRelayLookups) endpointEnabled(ctx context.Context, id string, read func(context.Context, string) (state.ManagedRealtimeEndpoint, error)) (bool, error) {
	l.mu.Lock()
	if hit, ok := l.endpoints[id]; ok && l.now().Sub(hit.at) < realtimeRelayLookupTTL {
		l.mu.Unlock()
		return hit.enabled, nil
	}
	l.mu.Unlock()
	endpoint, err := read(ctx, id)
	if err != nil {
		return false, err
	}
	l.mu.Lock()
	if len(l.endpoints) >= realtimeRelayLookupMax {
		l.endpoints = map[string]relayEndpointLookup{}
	}
	l.endpoints[id] = relayEndpointLookup{enabled: endpoint.Enabled, at: l.now()}
	l.mu.Unlock()
	return endpoint.Enabled, nil
}

func (l *realtimeRelayLookups) node(ctx context.Context, name string, read func(context.Context, string) (state.ComputeNode, error)) (state.ComputeNode, error) {
	l.mu.Lock()
	if hit, ok := l.nodes[name]; ok && l.now().Sub(hit.at) < realtimeRelayLookupTTL {
		l.mu.Unlock()
		return hit.node, nil
	}
	l.mu.Unlock()
	node, err := read(ctx, name)
	if err != nil {
		return node, err
	}
	l.mu.Lock()
	if len(l.nodes) >= realtimeRelayLookupMax {
		l.nodes = map[string]relayNodeLookup{}
	}
	l.nodes[name] = relayNodeLookup{node: node, at: l.now()}
	l.mu.Unlock()
	return node, nil
}
