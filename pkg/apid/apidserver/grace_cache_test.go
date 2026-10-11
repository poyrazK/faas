package apidserver

// IAM-5 grace-window cache (issue #189) tests.
//
// The cache is the seam that keeps the rotateKey handler off
// a hot PG path. The tests cover the three contract pieces:
//   1. miss → store → set → hit (the read-through pattern)
//   2. Invalidate drops the entry (admin PATCH closure)
//   3. TTL expiry forces a re-fetch (stale read ≤ 60 s)

import (
	"context"
	"sync"
	"testing"
	"time"
)

// stubGraceStore is a hand-rolled state.Store subset that
// counts GetAccountKeyGraceWindow calls and returns whatever
// the test set. We can't use the real MemStore for this test
// because it has a separate mutation surface; the stub keeps
// the assertion shape (one fetch per cache-miss, zero on
// cache-hit) tight.
type stubGraceStore struct {
	mu      sync.Mutex
	values  map[string]*int
	getHits map[string]int
}

func newStubGraceStore() *stubGraceStore {
	return &stubGraceStore{
		values:  map[string]*int{},
		getHits: map[string]int{},
	}
}

func (s *stubGraceStore) GetAccountKeyGraceWindow(_ context.Context, accountID string) (*int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getHits[accountID]++
	v, ok := s.values[accountID]
	if !ok {
		// Mirror the real pgstore behaviour: missing row → (nil,
		// nil) so the rotation handler falls back to the plan
		// default. The legacy "not found" path is reserved for
		// account-not-found; a missing key_grace_window_days
		// column is the no-override contract.
		return nil, nil
	}
	return v, nil
}

func (s *stubGraceStore) setOverride(accountID string, days *int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if days == nil {
		delete(s.values, accountID)
	} else {
		d := *days
		s.values[accountID] = &d
	}
}

func (s *stubGraceStore) getHitsFor(accountID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getHits[accountID]
}

// TestGraceWindowCache_MissThenHit covers the read-through
// pattern. First call: 1 store hit + cache populated. Second
// call: 0 store hits (cache served it).
func TestGraceWindowCache_MissThenHit(t *testing.T) {
	store := newStubGraceStore()
	store.setOverride("acct-1", intPtr(7))
	cache := newGraceWindowCache()
	ctx := context.Background()

	// First call — miss, store hit, cache populated.
	got, err := cache.resolveGraceWindow(ctx, store, "acct-1")
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if got == nil || *got != 7 {
		t.Errorf("first: got %v, want 7", got)
	}
	if h := store.getHitsFor("acct-1"); h != 1 {
		t.Errorf("first call store hits: got %d, want 1", h)
	}

	// Second call — cache hit, store untouched.
	got, err = cache.resolveGraceWindow(ctx, store, "acct-1")
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if got == nil || *got != 7 {
		t.Errorf("second: got %v, want 7", got)
	}
	if h := store.getHitsFor("acct-1"); h != 1 {
		t.Errorf("second call store hits: got %d, want still 1", h)
	}
}

// TestGraceWindowCache_InvalidateDropsEntry covers the
// admin-PATCH closure. The PATCH handler calls Invalidate so
// the next resolve sees the new override.
func TestGraceWindowCache_InvalidateDropsEntry(t *testing.T) {
	store := newStubGraceStore()
	store.setOverride("acct-1", intPtr(7))
	cache := newGraceWindowCache()
	ctx := context.Background()

	// Populate.
	_, _ = cache.resolveGraceWindow(ctx, store, "acct-1")
	if h := store.getHitsFor("acct-1"); h != 1 {
		t.Fatalf("setup: store hits = %d, want 1", h)
	}

	// Admin updates the row.
	store.setOverride("acct-1", intPtr(14))

	// Without Invalidate, the cache still serves 7.
	got, _ := cache.resolveGraceWindow(ctx, store, "acct-1")
	if got == nil || *got != 7 {
		t.Errorf("pre-invalidate: got %v, want 7 (stale cache)", got)
	}

	// Invalidate (the PATCH handler's job) → next read fetches 14.
	cache.Invalidate("acct-1")
	got, _ = cache.resolveGraceWindow(ctx, store, "acct-1")
	if got == nil || *got != 14 {
		t.Errorf("post-invalidate: got %v, want 14", got)
	}
	if h := store.getHitsFor("acct-1"); h != 2 {
		t.Errorf("post-invalidate store hits: got %d, want 2", h)
	}
}

// TestGraceWindowCache_TTLExpiry covers the stale-read
// envelope. After 50 ms (test TTL) the cache must refetch.
func TestGraceWindowCache_TTLExpiry(t *testing.T) {
	store := newStubGraceStore()
	store.setOverride("acct-1", intPtr(7))
	clock := time.Now
	ttl := 50 * time.Millisecond
	cache := newGraceWindowCacheForTest(clock, ttl)
	ctx := context.Background()

	// First read at t=0.
	_, _ = cache.resolveGraceWindow(ctx, store, "acct-1")
	if h := store.getHitsFor("acct-1"); h != 1 {
		t.Fatalf("setup: store hits = %d, want 1", h)
	}

	// Update the store under the cache's feet.
	store.setOverride("acct-1", intPtr(14))

	// Read at t=10ms — still cached, still 7.
	cache.clock = func() time.Time { return clock().Add(10 * time.Millisecond) }
	got, _ := cache.resolveGraceWindow(ctx, store, "acct-1")
	if got == nil || *got != 7 {
		t.Errorf("t=10ms: got %v, want 7 (cached)", got)
	}

	// Read at t=100ms — past the 50ms TTL, refetched, gets 14.
	cache.clock = func() time.Time { return clock().Add(100 * time.Millisecond) }
	got, _ = cache.resolveGraceWindow(ctx, store, "acct-1")
	if got == nil || *got != 14 {
		t.Errorf("t=100ms: got %v, want 14 (refetched)", got)
	}
}

// TestGraceWindowCache_DefaultsToPlanLevel covers the
// nil-override path. The cache returns nil for accounts
// without an override; the rotation handler falls back to
// api.DefaultAPIKeyGraceWindowDays = 7.
func TestGraceWindowCache_DefaultsToPlanLevel(t *testing.T) {
	store := newStubGraceStore()
	// no override seeded.
	cache := newGraceWindowCache()
	ctx := context.Background()

	got, err := cache.resolveGraceWindow(ctx, store, "acct-no-override")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != nil {
		t.Errorf("no-override: got %v, want nil (handler falls back to plan default)", got)
	}
}

func intPtr(i int) *int { return &i }
