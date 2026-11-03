package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 1323 (amendment of 2026-11-03) — an endpoint whose callback hangs
// holds at most its share of the replay workers; other endpoints' retries
// keep being delivered, and a quarter of the workers stay free for them.
func TestCallbackReplayKeepsWorkersForOtherEndpoints(t *testing.T) {
	queue := newTestCallbackOutbox(t, CallbackOutboxConfig{ReplayWorkers: 8})
	enqueue := func(endpoint string, n int) {
		for i := range n {
			event := testCallbackEvent()
			event.ID = fmt.Sprintf("evt_%s_%d", endpoint, i)
			event.EndpointID, event.AccountID = endpoint, "account-"+endpoint
			event.ConnectionID = fmt.Sprintf("%s-conn-%d", endpoint, i)
			event.At = time.Date(2026, 11, 3, 0, 0, i, 0, time.UTC)
			if endpoint == "slow" {
				event.At = event.At.Add(-time.Hour) // the oldest, so claimed first
			}
			if claimed, err := queue.EnqueueAndClaim(event); err != nil || !claimed {
				t.Fatalf("enqueue %s = (%v, %v)", event.ID, claimed, err)
			}
			queue.Release(event.ID)
		}
	}
	enqueue("slow", 20)
	enqueue("fast", 10)

	var slowNow, slowPeak atomic.Int32
	var fast atomic.Int32
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = queue.Run(ctx, func(ctx context.Context, event Event) error {
			if event.EndpointID == "slow" {
				n := slowNow.Add(1)
				for {
					peak := slowPeak.Load()
					if n <= peak || slowPeak.CompareAndSwap(peak, n) {
						break
					}
				}
				defer slowNow.Add(-1)
				select {
				case <-release:
				case <-ctx.Done():
				}
				return context.DeadlineExceeded
			}
			fast.Add(1)
			return nil
		})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for fast.Load() < 10 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	got, peak := fast.Load(), slowPeak.Load()
	close(release)
	cancel()
	wg.Wait()
	if got != 10 {
		t.Fatalf("fast endpoint delivered %d of 10 while the slow one hung", got)
	}
	// Past its share only while nothing else was ready, and never the
	// quarter of the workers kept for later events.
	if want := int32(8 - 8/4); peak > want {
		t.Fatalf("slow endpoint held %d replay workers, want at most %d", peak, want)
	}
}

// adr: 1323 (amendment of 2026-11-03) — a restarting node closes sockets
// with 1012 and bounds new connections, so reconnects arrive spread out.
func TestHandshakesAreBounded(t *testing.T) {
	m := NewManager(Config{}, nil)
	defer m.Close()
	if m.handshakes.Limit() != rate.Limit(api.RealtimeNodeHandshakesPerSecond) || m.handshakes.Burst() != api.RealtimeNodeHandshakeBurst {
		t.Fatalf("handshake rate = %v burst %d", m.handshakes.Limit(), m.handshakes.Burst())
	}
	// A limiter that does not refill during the test keeps it independent
	// of how fast the runner is.
	m.handshakes = rate.NewLimiter(rate.Every(time.Hour), 3)
	for i := range 3 {
		if !m.allowHandshake() {
			t.Fatalf("handshake %d within the burst was refused", i)
		}
	}
	if m.allowHandshake() || m.handshakesThrottled.Load() != 1 {
		t.Fatalf("a handshake past the burst: throttled = %d", m.handshakesThrottled.Load())
	}
}

type renewingFleet struct {
	relayCountingFleet
	renews, upserts atomic.Int32
	renewed         bool
	err             error
}

func (f *renewingFleet) RenewPresence(context.Context, string, string, string) (bool, error) {
	f.renews.Add(1)
	return f.renewed, f.err
}

func (f *renewingFleet) UpsertPresence(context.Context, string, string, string, string, string, json.RawMessage) (string, []PresenceMember, error) {
	f.upserts.Add(1)
	return "member", nil, nil
}

// adr: 1323 (amendment of 2026-11-03) — presence renewals extend the lease
// without an upsert; a lease apid no longer holds, or an apid without
// renewals, falls back to the upsert.
func TestPresenceRenewalSkipsTheUpsert(t *testing.T) {
	for _, tc := range []struct {
		name            string
		renewed         bool
		err             error
		upserts         int32
		renewsAfterward int32
	}{
		{name: "renewed", renewed: true, upserts: 0, renewsAfterward: 2},
		{name: "lease gone", renewed: false, upserts: 1, renewsAfterward: 2},
		{name: "older apid", err: ErrPresenceRenewUnsupported, upserts: 1, renewsAfterward: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fleet := &renewingFleet{renewed: tc.renewed, err: tc.err}
			m := NewManager(Config{FleetClient: fleet}, nil)
			defer m.Close()
			c := &connection{info: ConnectionInfo{EndpointID: "ep", ID: "conn-1"}}
			key := channelKey{endpointID: "ep", channel: "room"}
			m.mu.Lock()
			m.presenceMembers[key] = map[string]*presenceMember{"conn-1": {memberID: "member", announced: true, state: json.RawMessage(`{}`)}}
			m.mu.Unlock()
			m.renewFleetPresence(context.Background(), c, "room")
			if fleet.upserts.Load() != tc.upserts {
				t.Fatalf("upserts = %d, want %d", fleet.upserts.Load(), tc.upserts)
			}
			m.renewFleetPresence(context.Background(), c, "room")
			if fleet.renews.Load() != tc.renewsAfterward {
				t.Fatalf("renewals after two renewals = %d, want %d", fleet.renews.Load(), tc.renewsAfterward)
			}
		})
	}
}
