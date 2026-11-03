package realtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type relayCountingFleet struct{}

func (relayCountingFleet) UpsertPresence(context.Context, string, string, string, string, string, json.RawMessage) (string, []PresenceMember, error) {
	return "", nil, nil
}
func (relayCountingFleet) DeletePresence(context.Context, string, string, string) ([]PresenceMember, time.Time, error) {
	return nil, time.Time{}, nil
}
func (relayCountingFleet) ReadPresenceSnapshot(context.Context, string, string) ([]PresenceMember, error) {
	return nil, nil
}
func (relayCountingFleet) RelayEphemeral(context.Context, string, string, EphemeralFrame) error {
	return nil
}

// adr: 1323 (amendment of 2026-11-03) — signals, presence updates and read
// receipts each cost a control-plane relay, so an endpoint and the node have
// relay budgets; past either, the frame is refused.
func TestEphemeralRelaysAreBudgeted(t *testing.T) {
	m := NewManager(Config{FleetClient: relayCountingFleet{}}, nil)
	defer m.Close()
	busy := &connection{state: &endpointState{}}
	for i := range api.RealtimeEndpointEphemeralRelayBurst {
		if !m.allowEphemeralRelay(busy) {
			t.Fatalf("relay %d within the endpoint burst was refused", i)
		}
	}
	if m.allowEphemeralRelay(busy) {
		t.Fatal("a relay past the endpoint burst was allowed")
	}
	// Other endpoints share what is left of the node's burst.
	left := api.RealtimeNodeEphemeralRelayBurst - api.RealtimeEndpointEphemeralRelayBurst
	allowed := 0
	for allowed < left+api.RealtimeEndpointEphemeralRelayBurst {
		other := &connection{state: &endpointState{}}
		if !m.allowEphemeralRelay(other) {
			break
		}
		allowed++
	}
	if allowed < left-5 || allowed > left+25 {
		t.Fatalf("other endpoints relayed %d, want about the node's remaining %d", allowed, left)
	}
	if m.ephemeralRelaysThrottled.Load() != 2 {
		t.Fatalf("throttled = %d, want 2", m.ephemeralRelaysThrottled.Load())
	}

	// A node with no fleet relays nothing and spends nothing.
	local := NewManager(Config{}, nil)
	defer local.Close()
	for range api.RealtimeNodeEphemeralRelayBurst + 1 {
		if !local.allowEphemeralRelay(busy) {
			t.Fatal("a node without a fleet client refused a local frame")
		}
	}
}

// adr: 1323 (amendment of 2026-11-03) — live subscribe and renew frames are
// paced per connection.
func TestLiveFramesAreBudgeted(t *testing.T) {
	m := NewManager(Config{}, nil)
	defer m.Close()
	c := &connection{}
	for i := range api.RealtimeLiveFrameBurst {
		if !m.allowLiveFrame(c) {
			t.Fatalf("live frame %d within the burst was refused", i)
		}
	}
	if m.allowLiveFrame(c) || m.liveFramesThrottled.Load() != 1 {
		t.Fatalf("a live frame past the burst: throttled = %d", m.liveFramesThrottled.Load())
	}
}

// adr: 1323 (amendment of 2026-11-03) — inbox read receipts find their
// reader's connections through an index, not a scan of the node.
func TestInboxReadersAreIndexed(t *testing.T) {
	m := NewManager(Config{}, nil)
	defer m.Close()
	alice := &connection{readerKey: readerKeyFor("ep", "alice")}
	alice2 := &connection{readerKey: readerKeyFor("ep", "alice")}
	bob := &connection{readerKey: readerKeyFor("ep", "bob")}
	other := &connection{readerKey: readerKeyFor("ep-2", "alice")}
	m.mu.Lock()
	for _, c := range []*connection{alice, alice2, bob, other} {
		m.indexReaderLocked(c)
	}
	m.mu.Unlock()
	if got := m.readerConns[readerKeyFor("ep", "alice")]; len(got) != 2 {
		t.Fatalf("alice's connections = %d, want 2", len(got))
	}
	m.mu.Lock()
	m.unindexReaderLocked(alice)
	m.unindexReaderLocked(alice2)
	m.mu.Unlock()
	if _, ok := m.readerConns[readerKeyFor("ep", "alice")]; ok {
		t.Fatal("a reader with no connections stayed indexed")
	}
	if len(m.readerConns) != 2 {
		t.Fatalf("index = %d readers, want bob and the other endpoint's alice", len(m.readerConns))
	}
}
