package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// fakeOccupancyClient serves the other nodes' counts.
type fakeOccupancyClient struct {
	mu     sync.Mutex
	counts map[string]int
	err    error
	calls  int
}

func (f *fakeOccupancyClient) ReadRemoteOccupancy(_ context.Context, _ string, channels []string) (map[string]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[string]int, len(channels))
	for _, channel := range channels {
		out[channel] = f.counts[channel]
	}
	return out, nil
}

func (f *fakeOccupancyClient) set(counts map[string]int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts, f.err = counts, err
}

// occupancyFrames drains the occupancy counts queued for a connection.
func occupancyFrames(t *testing.T, c *connection) []int {
	t.Helper()
	var counts []int
	for {
		select {
		case msg := <-c.outbound:
			c.releaseOutboundBytes(int64(len(msg.Data)))
			var frame resumeServerFrame
			if err := json.Unmarshal(msg.Data, &frame); err != nil {
				t.Fatalf("frame %s: %v", msg.Data, err)
			}
			if frame.Type == "occupancy" && frame.Channel == "lobby" && frame.Subscribers != nil {
				counts = append(counts, *frame.Subscribers)
			}
		default:
			return counts
		}
	}
}

func newOccupancyTestManager(t *testing.T, client ManagedRealtimeOccupancyClient) *Manager {
	t.Helper()
	m := NewManager(Config{OccupancyPreview: true, OccupancyClient: client, occupancyManual: true}, nil)
	t.Cleanup(func() { _ = m.Close() })
	if err := m.RegisterEndpoint(Endpoint{Preview: true, ID: "e", MaxMessageBytes: 1 << 10}); err != nil {
		t.Fatal(err)
	}
	return m
}

// adr: 1324 — a watcher gets the count after subscribing and on each change,
// and never twice for the same count.
func TestOccupancyFramesFollowLocalChanges(t *testing.T) {
	m := newOccupancyTestManager(t, nil)
	watcher, _ := outboundTestConnection(t, m, "e")
	other, _ := outboundTestConnection(t, m, "e")
	ctx := context.Background()
	if err := m.Subscribe(watcher.info.ID, "lobby"); err != nil {
		t.Fatal(err)
	}
	if !m.watchOccupancy("e", "lobby", watcher) {
		t.Fatal("watch refused")
	}
	m.flushOccupancy(ctx)
	if got := occupancyFrames(t, watcher); len(got) != 1 || got[0] != 1 {
		t.Fatalf("first frames = %v, want [1]", got)
	}
	if err := m.Subscribe(other.info.ID, "lobby"); err != nil {
		t.Fatal(err)
	}
	m.flushOccupancy(ctx)
	m.flushOccupancy(ctx)
	if got := occupancyFrames(t, watcher); len(got) != 1 || got[0] != 2 {
		t.Fatalf("after a second subscriber = %v, want [2]", got)
	}
	// A subscriber that comes and goes between flushes changes nothing.
	if err := m.Unsubscribe(other.info.ID, "lobby"); err != nil {
		t.Fatal(err)
	}
	if err := m.Subscribe(other.info.ID, "lobby"); err != nil {
		t.Fatal(err)
	}
	m.flushOccupancy(ctx)
	if got := occupancyFrames(t, watcher); len(got) != 0 {
		t.Fatalf("unchanged count sent again: %v", got)
	}
	m.removeConnection(ctx, other)
	m.flushOccupancy(ctx)
	if got := occupancyFrames(t, watcher); len(got) != 1 || got[0] != 1 {
		t.Fatalf("after the other connection left = %v, want [1]", got)
	}
	m.removeConnection(ctx, watcher)
	m.occupancyLive.mu.Lock()
	watched := len(m.occupancyLive.watchers)
	m.occupancyLive.mu.Unlock()
	if watched != 0 {
		t.Fatalf("watched channels after the watcher left = %d, want 0", watched)
	}
}

// adr: 1324 — the first frame waits for the other nodes' counts, which are
// refreshed on an interval and kept through a failed read.
func TestOccupancyFramesAddRemoteCounts(t *testing.T) {
	client := &fakeOccupancyClient{counts: map[string]int{"lobby": 7}}
	m := newOccupancyTestManager(t, client)
	watcher, _ := outboundTestConnection(t, m, "e")
	ctx := context.Background()
	if err := m.Subscribe(watcher.info.ID, "lobby"); err != nil {
		t.Fatal(err)
	}
	m.watchOccupancy("e", "lobby", watcher)
	m.flushOccupancy(ctx)
	if got := occupancyFrames(t, watcher); len(got) != 0 {
		t.Fatalf("frame before the other nodes' counts = %v", got)
	}
	start := time.Now()
	m.refreshRemoteOccupancy(ctx, start)
	m.flushOccupancy(ctx)
	if got := occupancyFrames(t, watcher); len(got) != 1 || got[0] != 8 {
		t.Fatalf("with remote counts = %v, want [8]", got)
	}
	client.set(map[string]int{"lobby": 9}, nil)
	m.refreshRemoteOccupancy(ctx, start.Add(time.Second))
	if client.calls != 1 {
		t.Fatalf("refreshed before the interval: %d reads", client.calls)
	}
	m.refreshRemoteOccupancy(ctx, start.Add(api.RealtimeOccupancyRemoteRefresh))
	m.flushOccupancy(ctx)
	if got := occupancyFrames(t, watcher); len(got) != 1 || got[0] != 10 {
		t.Fatalf("after the remote count changed = %v, want [10]", got)
	}
	client.set(nil, errors.New("apid unavailable"))
	m.refreshRemoteOccupancy(ctx, start.Add(2*api.RealtimeOccupancyRemoteRefresh))
	m.flushOccupancy(ctx)
	if got := occupancyFrames(t, watcher); len(got) != 0 {
		t.Fatalf("a failed read changed the count: %v", got)
	}
	m.occupancyLive.mu.Lock()
	remote := m.occupancyLive.remote[channelKey{endpointID: "e", channel: "lobby"}]
	m.occupancyLive.mu.Unlock()
	if remote != 9 {
		t.Fatalf("remote count after a failed read = %d, want the last good 9", remote)
	}
}

func TestOccupancyWatchLimit(t *testing.T) {
	m := newOccupancyTestManager(t, nil)
	c, _ := outboundTestConnection(t, m, "e")
	for i := range api.RealtimeOccupancyWatchedMax {
		if !m.watchOccupancy("e", fmt.Sprintf("ch-%d", i), c) {
			t.Fatalf("watch %d refused below the limit", i)
		}
	}
	if m.watchOccupancy("e", "one-more", c) {
		t.Fatal("watch past the node limit accepted")
	}
	if !m.watchOccupancy("e", "ch-0", c) {
		t.Fatal("a second watcher of a watched channel refused")
	}
}

// adr: 1324 — an SSE stream gets counts through a one-slot channel where a
// newer count replaces one the stream has not written yet.
func TestOccupancyCountsReachSSEStreams(t *testing.T) {
	m := newOccupancyTestManager(t, nil)
	ctx := context.Background()
	c, _ := outboundTestConnection(t, m, "e")
	other, _ := outboundTestConnection(t, m, "e")
	if err := m.Subscribe(c.info.ID, "lobby"); err != nil {
		t.Fatal(err)
	}
	stream := newSSEOccupancyWatcher()
	key := channelKey{endpointID: "e", channel: "lobby"}
	m.occupancy.add(key, 1) // the SSE stream's own subscription
	if !m.watchOccupancy("e", "lobby", stream) {
		t.Fatal("watch refused")
	}
	m.flushOccupancy(ctx)
	if got := <-stream.updates; got != 2 {
		t.Fatalf("first count = %d, want 2", got)
	}
	if err := m.Subscribe(other.info.ID, "lobby"); err != nil {
		t.Fatal(err)
	}
	m.flushOccupancy(ctx) // 3, not read by the stream
	if err := m.Unsubscribe(other.info.ID, "lobby"); err != nil {
		t.Fatal(err)
	}
	m.flushOccupancy(ctx) // 2 replaces the unread 3
	select {
	case got := <-stream.updates:
		if got != 2 {
			t.Fatalf("latest count = %d, want 2", got)
		}
	default:
		t.Fatal("no count pending")
	}
	select {
	case got := <-stream.updates:
		t.Fatalf("stale count %d queued behind the latest", got)
	default:
	}
	m.unwatchOccupancy("e", "lobby", stream)
	m.occupancyLive.mu.Lock()
	watched := len(m.occupancyLive.watchers)
	m.occupancyLive.mu.Unlock()
	if watched != 0 {
		t.Fatalf("watched channels after the stream ended = %d, want 0", watched)
	}
}
