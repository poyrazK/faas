package realtime

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// durableTestHistory adds a durable cursor store to the resume test history
// and counts its writes.
type durableTestHistory struct {
	*testResumeHistory
	mu       sync.Mutex
	cursor   int64
	advances int
}

func (h *durableTestHistory) LoadDurableCursor(_ context.Context, _, _, _, _ string, initial int64) (int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cursor == 0 {
		h.cursor = initial
	}
	return h.cursor, nil
}

func (h *durableTestHistory) AdvanceDurableCursor(_ context.Context, _, _, _, _ string, sequence int64) (int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.advances++
	h.cursor = max(h.cursor, sequence)
	return h.cursor, nil
}

func (h *durableTestHistory) ResetDurableCursor(_ context.Context, _, _, _, _ string, sequence int64) (int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cursor = sequence
	return sequence, nil
}

// adr: 156 — an ack that repeats the stored cursor is answered without a
// control-plane write and spends the command budget, so a client cannot loop
// cursor writes.
func TestRepeatedDurableAckWritesOnceAndIsBudgeted(t *testing.T) {
	inner := &testResumeHistory{}
	for i, data := range []string{"one", "two"} {
		inner.appendMessage(int64(i+1), data)
	}
	history := &durableTestHistory{testResumeHistory: inner}
	m := NewManager(Config{JWTAuthorizer: &recordingJWTAuthorizer{}, ResumePreview: true, HistoryReader: history, ResumePollInterval: 15 * time.Millisecond}, &testChannelHooks{allowed: true})
	if err := m.RegisterEndpoint(Endpoint{Preview: true, ID: "resume-endpoint", AppID: "app-1", AccountID: "acct-1", CallbackURL: "https://app.example",
		AllowedOrigins: []string{"https://app.example"},
		ClientAuth:     AuthPolicy{Mode: AuthModeOIDCJWT, Issuer: "https://issuer.example", JWKSURL: "https://issuer.example/.well-known/jwks.json", Algorithms: []string{"RS256"}},
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	t.Cleanup(func() { server.Close(); _ = m.Close() })
	client := dialResumeTest(t, server)
	sendResumeTestFrame(t, client, resumeClientFrame{Type: "subscribe", Channel: "updates", Subscription: "feed"})
	for {
		if frame := readResumeTestFrame(t, client); frame.Type == "message" && frame.Sequence == 2 {
			break
		}
	}
	sendResumeTestFrame(t, client, resumeClientFrame{Type: "ack", Channel: "updates", Sequence: 2})
	if frame := readResumeTestFrame(t, client); frame.Type != "acknowledged" || frame.Sequence != 2 {
		t.Fatalf("ack = %+v", frame)
	}
	limited := false
	for range 60 {
		sendResumeTestFrame(t, client, resumeClientFrame{Type: "ack", Channel: "updates", Sequence: 2})
		frame := readResumeTestFrame(t, client)
		if frame.Code == "command_rate_limited" {
			limited = true
			break
		}
		if frame.Type != "acknowledged" {
			t.Fatalf("repeated ack = %+v", frame)
		}
	}
	history.mu.Lock()
	advances := history.advances
	history.mu.Unlock()
	if advances != 1 || !limited {
		t.Fatalf("cursor writes = %d, rate limited = %v; want one write and a refusal past the budget", advances, limited)
	}
}

type countingAccessReader struct {
	mu     sync.Mutex
	reads  map[string]int
	err    error
	access state.ManagedRealtimeAccess
	ticket state.RealtimeSubscriptionTicket
	gate   chan struct{}
}

func (r *countingAccessReader) count(key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reads[key]
}

func (r *countingAccessReader) ReadRealtimeAccess(_ context.Context, ep, principal string) (state.ManagedRealtimeAccess, error) {
	if r.gate != nil {
		<-r.gate
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reads == nil {
		r.reads = map[string]int{}
	}
	r.reads[ep+"/"+principal]++
	return r.access, r.err
}

func (r *countingAccessReader) ReadSubscriptionTicket(_ context.Context, ep, token string) (state.RealtimeSubscriptionTicket, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reads == nil {
		r.reads = map[string]int{}
	}
	r.reads[ep+"/ticket"]++
	if r.err != nil {
		return state.RealtimeSubscriptionTicket{}, r.err
	}
	if r.ticket.Principal == "" {
		return state.RealtimeSubscriptionTicket{}, state.ErrNotFound
	}
	return r.ticket, nil
}

var testTicket = ticketToken(0)

// ticketToken is a well-formed subscription ticket numbered i.
func ticketToken(i uint32) string {
	raw := make([]byte, 32)
	binary.BigEndian.PutUint32(raw, i)
	return "grt_" + base64.RawURLEncoding.EncodeToString(raw)
}

// adr: 1113 — a node reads a principal's access once while the feed is
// healthy, again after the feed names the principal, and serves its last
// answer through an outage only until realtimeAccessStaleMax.
func TestAccessCacheFollowsTheChangeFeed(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	reader := &countingAccessReader{ticket: state.RealtimeSubscriptionTicket{Principal: "alice", ExpiresAt: now.Add(time.Hour)}}
	m := NewManager(Config{AccessReader: reader, TicketReader: reader}, nil)
	defer m.Close()
	m.accessCache.now = func() time.Time { return now }
	ctx := context.Background()
	m.accessCache.applyFeed(state.ManagedRealtimeAccessChanges{Epoch: "e1", Reset: true})

	for range 5 {
		if _, err := m.cachedAccess(ctx, "ep", "alice"); err != nil {
			t.Fatal(err)
		}
		if _, err := m.cachedTicket(ctx, "ep", testTicket, false); err != nil {
			t.Fatal(err)
		}
	}
	if reader.count("ep/alice") != 1 || reader.count("ep/ticket") != 1 {
		t.Fatalf("reads with a healthy feed = %d access, %d ticket; want one each", reader.count("ep/alice"), reader.count("ep/ticket"))
	}

	now = now.Add(500 * time.Millisecond)
	m.accessCache.applyFeed(state.ManagedRealtimeAccessChanges{Epoch: "e1", Seq: 1, Changes: []state.ManagedRealtimeAccessChange{{EndpointID: "ep", Principal: "alice"}}})
	_, _ = m.cachedAccess(ctx, "ep", "alice")
	_, _ = m.cachedTicket(ctx, "ep", testTicket, false)
	if reader.count("ep/alice") != 2 || reader.count("ep/ticket") != 2 {
		t.Fatalf("a change to alice left her cached: %d access, %d ticket reads", reader.count("ep/alice"), reader.count("ep/ticket"))
	}

	// The feed goes quiet: entries last a second, as every read did before.
	now = now.Add(realtimeAccessFeedHealthyFor + time.Second)
	_, _ = m.cachedAccess(ctx, "ep", "alice")
	if reader.count("ep/alice") != 3 {
		t.Fatalf("an unhealthy feed kept serving the cache: %d reads", reader.count("ep/alice"))
	}

	// The control plane fails: the last answer serves until it is too stale.
	reader.mu.Lock()
	reader.err = errors.New("apid unavailable")
	reader.mu.Unlock()
	now = now.Add(2 * time.Second)
	if _, err := m.cachedAccess(ctx, "ep", "alice"); err != nil {
		t.Fatalf("a failed read did not serve the last answer: %v", err)
	}
	now = now.Add(realtimeAccessStaleMax)
	if _, err := m.cachedAccess(ctx, "ep", "alice"); err == nil {
		t.Fatal("an answer older than realtimeAccessStaleMax was served")
	}
}

// A feed reset marks entries stale but keeps them as the outage answer.
func TestAccessCacheResetKeepsOutageAnswers(t *testing.T) {
	reader := &countingAccessReader{}
	m := NewManager(Config{AccessReader: reader}, nil)
	defer m.Close()
	now := time.Unix(1_800_000_000, 0)
	m.accessCache.now = func() time.Time { return now }
	ctx := context.Background()
	m.accessCache.applyFeed(state.ManagedRealtimeAccessChanges{Epoch: "e1", Reset: true})
	_, _ = m.cachedAccess(ctx, "ep", "alice")
	now = now.Add(time.Second)
	m.accessCache.applyFeed(state.ManagedRealtimeAccessChanges{Epoch: "e2", Reset: true})
	reader.mu.Lock()
	reader.err = errors.New("apid unavailable")
	reader.mu.Unlock()
	if _, err := m.cachedAccess(ctx, "ep", "alice"); err != nil || reader.count("ep/alice") != 2 {
		t.Fatalf("after a reset: err %v, reads %d; want a fresh read attempt answered from the stale entry", err, reader.count("ep/alice"))
	}
}

// Concurrent checks of one principal share one control-plane read.
func TestAccessCacheCoalescesConcurrentReads(t *testing.T) {
	reader := &countingAccessReader{gate: make(chan struct{})}
	m := NewManager(Config{AccessReader: reader}, nil)
	defer m.Close()
	var wg sync.WaitGroup
	var failures atomic.Int32
	for range 20 {
		wg.Go(func() {
			if _, err := m.cachedAccess(context.Background(), "ep", "alice"); err != nil {
				failures.Add(1)
			}
		})
	}
	time.Sleep(50 * time.Millisecond)
	close(reader.gate)
	wg.Wait()
	if reader.count("ep/alice") != 1 || failures.Load() != 0 {
		t.Fatalf("reads = %d, failures = %d; want one shared read", reader.count("ep/alice"), failures.Load())
	}
}

// adr: 1117 — a new connection's ticket is refused without a read when it is
// malformed, and past the node's ticket read budget with errTicketCheckBusy.
func TestTicketAdmissionReadsAreBounded(t *testing.T) {
	reader := &countingAccessReader{}
	m := NewManager(Config{TicketReader: reader}, nil)
	defer m.Close()
	ctx := context.Background()
	if _, err := m.cachedTicket(ctx, "ep", "grt_not-a-ticket", true); !errors.Is(err, state.ErrNotFound) || reader.count("ep/ticket") != 0 {
		t.Fatalf("malformed ticket = %v after %d reads", err, reader.count("ep/ticket"))
	}
	busy := false
	for i := range uint32(2_000) {
		if _, err := m.cachedTicket(ctx, "ep", ticketToken(i+1), true); errors.Is(err, errTicketCheckBusy) {
			busy = true
			break
		}
	}
	if !busy {
		t.Fatal("unknown tickets were read without bound")
	}
}

// adr: 156 — readers of one channel position share a read until the channel
// is published to on this node.
func TestHistoryReadsAreSharedPerGeneration(t *testing.T) {
	var h historyReads
	var reads atomic.Int32
	read := func(context.Context) (state.ManagedRealtimeChannelHistory, error) {
		reads.Add(1)
		time.Sleep(20 * time.Millisecond)
		return state.ManagedRealtimeChannelHistory{LatestSequence: 3}, nil
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() { _, _ = h.read(context.Background(), "ep", "room", 3, read) })
	}
	wg.Wait()
	if _, err := h.read(context.Background(), "ep", "room", 3, read); err != nil || reads.Load() != 1 {
		t.Fatalf("reads = %d (%v), want one shared read", reads.Load(), err)
	}
	h.published(channelKey{endpointID: "ep", channel: "room"})
	_, _ = h.read(context.Background(), "ep", "room", 3, read)
	if reads.Load() != 2 {
		t.Fatalf("a read after a publish was served from before it: %d reads", reads.Load())
	}
}

// adr: 156 — a failed history read is retried instead of closing the
// connection.
func TestHistoryReadRetriesThroughAnOutage(t *testing.T) {
	c := &connection{done: make(chan struct{})}
	var calls int
	page, err := retryHistoryRead(context.Background(), c, func(context.Context) (state.ManagedRealtimeChannelHistory, error) {
		calls++
		if calls == 1 {
			return state.ManagedRealtimeChannelHistory{}, errors.New("apid restarting")
		}
		return state.ManagedRealtimeChannelHistory{LatestSequence: 7}, nil
	})
	if err != nil || page.LatestSequence != 7 || calls != 2 {
		t.Fatalf("retry = (%+v, %v) after %d calls", page, err, calls)
	}
}

// adr: 1323 — a plain connection's messages wait for the message rate, and a
// connection that would wait too long is closed rather than spooled.
func TestClientMessagesArePaced(t *testing.T) {
	m := NewManager(Config{}, nil)
	defer m.Close()
	c := &connection{done: make(chan struct{})}
	for i := range api.RealtimeClientMessageBurst {
		if !m.paceClientMessage(context.Background(), c) {
			t.Fatalf("message %d within the burst was refused", i)
		}
	}
	started := time.Now()
	if !m.paceClientMessage(context.Background(), c) {
		t.Fatal("a message past the burst was refused instead of paced")
	}
	if waited := time.Since(started); waited < 20*time.Millisecond {
		t.Fatalf("a message past the burst waited %s", waited)
	}
	if m.clientMessagesPaced.Load() != 1 {
		t.Fatalf("paced = %d", m.clientMessagesPaced.Load())
	}
}
