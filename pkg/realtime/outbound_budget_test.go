package realtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// outboundTestConnection registers the server side of a live WebSocket and
// returns it with the client end. Nothing drains the server's queue, so frames
// queued to it stay counted: a deterministic slow consumer.
func outboundTestConnection(t *testing.T, m *Manager, endpointID string) (*connection, *websocket.Conn) {
	t.Helper()
	upgraded := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		upgraded <- ws
	}))
	t.Cleanup(server.Close)
	client, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	t.Cleanup(func() { _ = client.Close() })
	value, ok := m.endpoints.Load(endpointID)
	if !ok {
		t.Fatalf("endpoint %q is not registered", endpointID)
	}
	state := value.(*endpointState)
	return m.addConnection(state, *state.config.Load(), "", <-upgraded), client
}

// registerAccountEndpoints registers one endpoint per ID, each its own
// account (named after it).
func registerAccountEndpoints(t *testing.T, m *Manager, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := m.RegisterEndpoint(Endpoint{Preview: true, ID: id, AccountID: "acct-" + id, MaxMessageBytes: 1 << 20}); err != nil {
			t.Fatal(err)
		}
	}
}

func queueFrames(t *testing.T, m *Manager, c *connection, frames, size int) {
	t.Helper()
	for range frames {
		if err := m.queueOutbound(context.Background(), c, Message{Data: make([]byte, size)}); err != nil {
			t.Fatalf("queue %d bytes: %v", size, err)
		}
	}
}

func TestNodeOutboundReservation(t *testing.T) {
	m := NewManager(Config{OutboundNodeBytes: 100}, nil)
	defer m.cancel()
	for _, step := range []struct {
		account string
		n       int64
		ok      bool
		want    int64
	}{
		{"a", 40, true, 40},
		{"b", 50, true, 90},
		{"c", 11, false, 90},
		{"c", 10, true, 100},
		{"c", 1, false, 100},
		{"b", -30, true, 70},
		{"b", 30, true, 100},
	} {
		if ok := m.reserveNodeOutbound(m.accountFor(step.account), step.n); ok != step.ok || m.outboundPendingBytes.Load() != step.want {
			t.Fatalf("reserve(%s, %d) = %v with %d pending, want %v with %d", step.account, step.n, ok, m.outboundPendingBytes.Load(), step.ok, step.want)
		}
	}
}

// One account holds at most its share of the node's outbound budget, so its
// slow readers (each just under the shedder's backlog floor) cannot fill the
// budget and starve every other tenant's frames (security audit 2026-10-27).
func TestNodeOutboundAccountShare(t *testing.T) {
	m := NewManager(Config{OutboundNodeBytes: 100}, nil)
	defer m.cancel()
	flood, other := m.accountFor("flood"), m.accountFor("other")
	if !m.reserveNodeOutbound(flood, 50) {
		t.Fatal("an account's frames within its share were refused")
	}
	if m.reserveNodeOutbound(flood, 1) {
		t.Fatal("an account reserved past its share of the node budget")
	}
	if flood.outbound.Load() != 50 || m.outboundPendingBytes.Load() != 50 {
		t.Fatalf("after a refused reservation: account %d, node %d; want 50 and 50", flood.outbound.Load(), m.outboundPendingBytes.Load())
	}
	if !m.reserveNodeOutbound(other, 50) {
		t.Fatal("another account's frames were refused while the flood held only its share")
	}
	if !m.reserveNodeOutbound(flood, -20) || !m.reserveNodeOutbound(flood, 20) {
		t.Fatal("released bytes did not make room in the account's share")
	}
}

// adr: 1323 — a zero budget, the default until the budget is enabled, never
// refuses a frame and never starts the shedder.
func TestNodeOutboundBudgetDisabled(t *testing.T) {
	m := NewManager(Config{}, nil)
	defer m.cancel()
	if !m.reserveNodeOutbound(m.accountFor(""), 1<<40) || m.outboundPendingBytes.Load() != 1<<40 {
		t.Fatalf("disabled budget refused or lost bytes: %d pending", m.outboundPendingBytes.Load())
	}
	if !m.reserveNodeOutbound(m.accountFor(""), -(1<<40)) || m.outboundPendingBytes.Load() != 0 {
		t.Fatalf("release left %d pending", m.outboundPendingBytes.Load())
	}
	select {
	case <-m.outboundShed:
		t.Fatal("disabled budget woke the shedder")
	default:
	}
	if got := m.Stats().OutboundNodeBytesLimit; got != 0 {
		t.Fatalf("limit = %d, want 0 when disabled", got)
	}
}

func TestNodeOutboundBudgetShedsLargestBacklog(t *testing.T) {
	const kib = 1 << 10
	m := NewManager(Config{OutboundQueueBytes: 512 * kib, OutboundNodeBytes: 1 << 20}, nil)
	defer func() { _ = m.Close() }()
	// One account each, so no backlog passes an account's share (512 KiB).
	registerAccountEndpoints(t, m, "big", "mid", "small")
	big, bigClient := outboundTestConnection(t, m, "big")
	mid, _ := outboundTestConnection(t, m, "mid")
	small, _ := outboundTestConnection(t, m, "small")
	queueFrames(t, m, big, 8, 64*kib) // 512 KiB, its whole queue
	queueFrames(t, m, mid, 4, 64*kib) // 256 KiB: the node now sits at 75%
	queueFrames(t, m, small, 1, 16*kib)

	// Above 75% the shedder closes the largest backlog first and stops once
	// queued bytes are at or below 50% of the budget.
	deadline := time.Now().Add(5 * time.Second)
	for m.outboundShedDisconnects.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := m.Stats().OutboundShedDisconnects; got != 1 {
		t.Fatalf("shed disconnects = %d, want 1", got)
	}
	if got, want := m.outboundPendingBytes.Load(), int64(272*kib); got != want {
		t.Fatalf("pending bytes after shedding = %d, want %d", got, want)
	}
	for name, c := range map[string]*connection{"mid": mid, "small": small} {
		c.outboundMu.Lock()
		closed := c.outboundClosed
		c.outboundMu.Unlock()
		if closed {
			t.Fatalf("%s connection was shed; only the largest backlog should be", name)
		}
	}
	if err := bigClient.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, _, err := bigClient.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != websocket.CloseTryAgainLater || closeErr.Text != outboundNodeBudgetReason {
		t.Fatalf("shed client read = %v, want close %d %q", err, websocket.CloseTryAgainLater, outboundNodeBudgetReason)
	}
	// The freed budget admits new frames again.
	queueFrames(t, m, mid, 1, 64*kib)
}

func TestNodeOutboundBudgetRefusesFramesItCannotShed(t *testing.T) {
	const kib = 1 << 10
	m := NewManager(Config{OutboundQueueBytes: 1 << 20, OutboundNodeBytes: 256 * kib}, nil)
	defer m.cancel()
	// One account each, so no account passes its share (128 KiB).
	registerAccountEndpoints(t, m, "e0", "e1", "e2", "e3", "e4", "e5")
	// Every backlog stays below 1/16 of the per-connection limit (64 KiB), so
	// none is a slow consumer worth disconnecting.
	var conns []*connection
	for i := range 5 {
		c := addSyntheticConnection(t, m, fmt.Sprintf("e%d", i))
		queueFrames(t, m, c, 3, 16*kib)
		conns = append(conns, c)
	}
	last := addSyntheticConnection(t, m, "e5")
	queueFrames(t, m, last, 1, 16*kib) // exactly the budget
	err := m.queueOutbound(context.Background(), last, Message{Data: make([]byte, 16*kib)})
	if !errors.Is(err, ErrOutboundQueueFull) {
		t.Fatalf("frame past the node budget = %v, want ErrOutboundQueueFull", err)
	}
	m.shedOutbound()
	stats := m.Stats()
	if stats.OutboundNodeBudgetDrops != 1 || stats.DroppedMessages != 1 || stats.OutboundShedDisconnects != 0 {
		t.Fatalf("stats = budget drops %d, dropped %d, shed %d; want 1, 1, 0",
			stats.OutboundNodeBudgetDrops, stats.DroppedMessages, stats.OutboundShedDisconnects)
	}
	if stats.OutboundPendingBytes != 256*kib || stats.OutboundNodeBytesLimit != 256*kib {
		t.Fatalf("pending %d of %d, want the full budget", stats.OutboundPendingBytes, stats.OutboundNodeBytesLimit)
	}
	// A delivered frame releases its bytes and makes room.
	msg := <-conns[0].outbound
	conns[0].releaseOutboundBytes(int64(len(msg.Data)))
	queueFrames(t, m, last, 1, 16*kib)
}

func TestNodeOutboundBudgetBoundsCoalescedSignals(t *testing.T) {
	// One account reserves at most half the budget: 100 bytes of 200.
	m := NewManager(Config{OutboundNodeBytes: 200}, nil)
	defer m.cancel()
	if err := m.RegisterEndpoint(Endpoint{Preview: true, ID: "e", MaxMessageBytes: 1 << 20}); err != nil {
		t.Fatal(err)
	}
	c := addSyntheticConnection(t, m, "e")
	key := pendingSignalKey{channel: "room", member: "m", name: "typing"}
	signal := func(size int, at time.Time) error {
		return m.queueOutbound(context.Background(), c, Message{Data: make([]byte, size), signalKey: key, signalUpdated: at})
	}
	start := time.Now()
	if err := signal(40, start); err != nil {
		t.Fatal(err)
	}
	// A newer value replaces the queued one; only the growth is reserved.
	if err := signal(100, start.Add(time.Second)); err != nil || m.outboundPendingBytes.Load() != 100 {
		t.Fatalf("grown signal = %v with %d pending, want nil with 100", err, m.outboundPendingBytes.Load())
	}
	if err := signal(101, start.Add(2*time.Second)); !errors.Is(err, ErrOutboundQueueFull) || m.outboundPendingBytes.Load() != 100 {
		t.Fatalf("signal past the account's share = %v with %d pending", err, m.outboundPendingBytes.Load())
	}
	if err := signal(10, start.Add(3*time.Second)); err != nil || m.outboundPendingBytes.Load() != 10 {
		t.Fatalf("shrunk signal = %v with %d pending, want nil with 10", err, m.outboundPendingBytes.Load())
	}
}
