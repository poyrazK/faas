// adr: 120
//
// ADR-120 defines the gatewayd-internal consumer-key middleware and its
// last_used_at bookkeeping. These tests pin the debouncer that fronts that
// write: the debounce itself, its memory bound, and the fact that eviction
// costs only a redundant observational write and never changes an
// authorization outcome.

package gateway

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// touchCountingStore records TouchConsumerKeyLastUsed calls. Only the touch
// path is exercised, so the lookup methods are unreachable here.
type touchCountingStore struct {
	mu      sync.Mutex
	touched map[string]int
	done    chan struct{}
	want    int
	seen    int
}

func newTouchCountingStore(want int) *touchCountingStore {
	return &touchCountingStore{touched: map[string]int{}, done: make(chan struct{}), want: want}
}

func (s *touchCountingStore) ConsumerKeyByAppAndPrefix(context.Context, string, string, string) (ConsumerAuthKey, error) {
	return ConsumerAuthKey{}, ErrConsumerAuthNotFound
}

func (s *touchCountingStore) GetAPIConsumerByID(context.Context, string, string) (ConsumerAuthConsumer, error) {
	return ConsumerAuthConsumer{}, ErrConsumerAuthNotFound
}

func (s *touchCountingStore) TouchConsumerKeyLastUsed(_ context.Context, keyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touched[keyID]++
	s.seen++
	if s.seen == s.want {
		close(s.done)
	}
	return nil
}

func (s *touchCountingStore) count(keyID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.touched[keyID]
}

// waitForTouches blocks until the expected number of async touches landed.
// The touch write is fire-and-forget in a goroutine, so the test cannot read
// the counter synchronously.
func (s *touchCountingStore) waitForTouches(t *testing.T) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		s.mu.Lock()
		seen := s.seen
		s.mu.Unlock()
		t.Fatalf("timed out waiting for touches: saw %d, want %d", seen, s.want)
	}
}

// TestConsumerKeyToucher_DebouncesRepeats pins the behaviour the cache exists
// for: a repeat inside the window must not produce a second write.
func TestConsumerKeyToucher_DebouncesRepeats(t *testing.T) {
	store := newTouchCountingStore(1)
	toucher := newConsumerKeyToucher()

	for i := 0; i < 5; i++ {
		toucher.touch(store, "key-1")
	}
	store.waitForTouches(t)

	if got := store.count("key-1"); got != 1 {
		t.Fatalf("TouchConsumerKeyLastUsed called %d times, want 1 — the debounce is not working", got)
	}
}

// TestConsumerKeyToucher_IsBounded is the regression pin.
//
// The debouncer was a bare map[string]time.Time with no eviction: one entry
// per distinct consumer key that ever authenticated, never removed, on a
// process designed to run for weeks. Keys that were later revoked, or whose
// app was deleted, stayed resident forever.
func TestConsumerKeyToucher_IsBounded(t *testing.T) {
	store := newTouchCountingStore(consumerKeyTouchCacheEntries * 2)
	toucher := newConsumerKeyToucher()

	for i := 0; i < consumerKeyTouchCacheEntries*2; i++ {
		toucher.touch(store, fmt.Sprintf("key-%d", i))
	}
	store.waitForTouches(t)

	if got := toucher.last.Len(); got > consumerKeyTouchCacheEntries {
		t.Fatalf("debounce cache holds %d entries after %d distinct keys; cap is %d",
			got, consumerKeyTouchCacheEntries*2, consumerKeyTouchCacheEntries)
	}
}

// TestConsumerKeyToucher_EvictionOnlyCostsAnExtraWrite documents the tradeoff:
// a key pushed out by cap pressure is touched again on its next request. That
// is a redundant observational write, never an authorization difference.
func TestConsumerKeyToucher_EvictionOnlyCostsAnExtraWrite(t *testing.T) {
	store := newTouchCountingStore(consumerKeyTouchCacheEntries + 2)
	toucher := newConsumerKeyToucher()

	toucher.touch(store, "victim")
	// Fill past the cap so "victim" is evicted as the least-recently-used.
	for i := 0; i < consumerKeyTouchCacheEntries; i++ {
		toucher.touch(store, fmt.Sprintf("filler-%d", i))
	}
	toucher.touch(store, "victim")
	store.waitForTouches(t)

	if got := store.count("victim"); got != 2 {
		t.Fatalf("evicted key touched %d times, want 2 (once before eviction, once after)", got)
	}
}

// TestConsumerKeyToucher_NilSafe keeps the existing guard contract: the
// toucher is reachable from handler paths that may not have wired a store.
func TestConsumerKeyToucher_NilSafe(t *testing.T) {
	var nilToucher *consumerKeyToucher
	nilToucher.touch(newTouchCountingStore(0), "key-1") // must not panic

	toucher := newConsumerKeyToucher()
	toucher.touch(nil, "key-1")
	toucher.touch(newTouchCountingStore(0), "")
	if toucher.last.Len() != 0 {
		t.Fatalf("no-op touches populated the cache: %d entries", toucher.last.Len())
	}
}
