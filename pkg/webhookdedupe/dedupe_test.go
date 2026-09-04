package webhookdedupe

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// resetStoreForTest wipes the package-level sync.Map between tests.
// The store is process-local by design; tests share it and must
// not pollute each other.
func resetStoreForTest(t *testing.T) {
	t.Helper()
	store.Range(func(k, _ any) bool { store.Delete(k); return true })
}

// setNowForTest swaps the package clock seam and returns a restore
// closure. Tests that want to drive the TTL boundary without
// sleeping can pin the clock to a known value.
func setNowForTest(t *testing.T, when time.Time) func() {
	t.Helper()
	prev := nowFunc
	nowFunc = func() time.Time { return when }
	return func() { nowFunc = prev }
}

// TestCheckReplay_FirstDelivery_Fresh covers the happy path: empty
// store → CheckReplay returns nil and records the entry.
func TestCheckReplay_FirstDelivery_Fresh(t *testing.T) {
	resetStoreForTest(t)
	if err := CheckReplay(context.Background(), ProviderGitHub, "delivery-abc-123"); err != nil {
		t.Fatalf("first delivery should be fresh; err=%v", err)
	}
}

// TestCheckReplay_SecondDeliveryWithinTTL_Rejected covers the
// security-critical branch: the same (provider, deliveryID) pair
// arriving twice in the TTL window returns *Replay.
func TestCheckReplay_SecondDeliveryWithinTTL_Rejected(t *testing.T) {
	resetStoreForTest(t)
	if err := CheckReplay(context.Background(), ProviderGitHub, "delivery-abc-123"); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	err := CheckReplay(context.Background(), ProviderGitHub, "delivery-abc-123")
	if err == nil {
		t.Fatalf("second delivery within TTL should be rejected")
	}
	if !IsReplay(err) {
		t.Errorf("IsReplay(err) = false; got %v", err)
	}
	if !errors.Is(err, ErrReplay) {
		t.Errorf("errors.Is(err, ErrReplay) = false; got %v", err)
	}
	var replay *Replay
	if !errors.As(err, &replay) {
		t.Fatalf("errors.As(*Replay) should succeed; got %T", err)
	}
	if replay.Provider != ProviderGitHub || replay.DeliveryID != "delivery-abc-123" {
		t.Errorf("Replay payload wrong: %+v", replay)
	}
}

func TestReleaseReplay_AllowsRetryAfterFailedApplication(t *testing.T) {
	resetStoreForTest(t)
	ctx := context.Background()
	if err := CheckReplay(ctx, ProviderStripe, "delivery-release"); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	ReleaseReplay(ctx, ProviderStripe, "delivery-release")
	if err := CheckReplay(ctx, ProviderStripe, "delivery-release"); err != nil {
		t.Fatalf("delivery after rollback should be fresh: %v", err)
	}
}

// TestCheckReplay_DeliveryAfterTTL_Fresh covers the TTL boundary:
// a delivery whose stored expires_at is older than the cutoff
// (computed as now-TTL inside CheckReplay) is treated as fresh.
// We pin the clock to seed an entry, advance, and re-check.
func TestCheckReplay_DeliveryAfterTTL_Fresh(t *testing.T) {
	resetStoreForTest(t)
	restore := setNowForTest(t, time.Now())
	defer restore()
	if err := CheckReplay(context.Background(), ProviderGitHub, "stale"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Advance the clock past the TTL window so the entry is stale.
	nowFunc = func() time.Time { return time.Now().Add(2 * TTL) }
	if err := CheckReplay(context.Background(), ProviderGitHub, "stale"); err != nil {
		t.Fatalf("delivery after TTL should be fresh; err=%v", err)
	}
}

// TestCheckReplay_DifferentProviders_Independent covers the
// provider-axis: same deliveryID, different provider → both fresh
// (the dedupe key is (provider, deliveryID), not deliveryID alone).
func TestCheckReplay_DifferentProviders_Independent(t *testing.T) {
	resetStoreForTest(t)
	ctx := context.Background()
	if err := CheckReplay(ctx, ProviderGitHub, "shared-id"); err != nil {
		t.Fatalf("github first delivery: %v", err)
	}
	if err := CheckReplay(ctx, ProviderStripe, "shared-id"); err != nil {
		t.Fatalf("stripe first delivery (different provider, same id) should be fresh; err=%v", err)
	}
	if err := CheckReplay(ctx, ProviderPaddle, "shared-id"); err != nil {
		t.Fatalf("paddle first delivery (different provider, same id) should be fresh; err=%v", err)
	}
}

// TestCheckReplay_DifferentDeliveryIDs_Independent covers the
// deliveryID-axis: same provider, different deliveryID → both
// fresh.
func TestCheckReplay_DifferentDeliveryIDs_Independent(t *testing.T) {
	resetStoreForTest(t)
	ctx := context.Background()
	if err := CheckReplay(ctx, ProviderGitHub, "delivery-1"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := CheckReplay(ctx, ProviderGitHub, "delivery-2"); err != nil {
		t.Fatalf("different deliveryID should be fresh; err=%v", err)
	}
}

// TestCheckReplay_ConcurrentSafe is a smoke test for the
// sync.Map-backed store: 50 goroutines hammer the same
// (provider, deliveryID) — exactly one should observe nil, the
// other 49 should observe a replay. Catches accidental
// reintroductions of a non-thread-safe map.
func TestCheckReplay_ConcurrentSafe(t *testing.T) {
	resetStoreForTest(t)
	const N = 50
	var wg sync.WaitGroup
	results := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = CheckReplay(context.Background(), ProviderGitHub, "concurrent-id")
		}(i)
	}
	wg.Wait()
	var fresh, replay int
	for _, err := range results {
		if err == nil {
			fresh++
		} else if IsReplay(err) {
			replay++
		} else {
			t.Errorf("unexpected error: %v", err)
		}
	}
	if fresh != 1 {
		t.Errorf("fresh count = %d, want 1; replay count = %d", fresh, replay)
	}
	if replay != N-1 {
		t.Errorf("replay count = %d, want %d", replay, N-1)
	}
}

// TestErrReplay_Sentinel pins the wrapper contract:
// webhookdedupe.ErrReplay is a package-local sentinel, and
// *Replay.Is(target) matches it. Callers can use either
// errors.Is(err, ErrReplay) or IsReplay(err).
func TestErrReplay_Sentinel(t *testing.T) {
	if !IsReplay(ErrReplay) {
		t.Errorf("IsReplay(ErrReplay) = false; want true (bare sentinel)")
	}
	wrapped := &Replay{Provider: ProviderStripe, DeliveryID: "evt_test_123"}
	if !IsReplay(wrapped) {
		t.Errorf("IsReplay(*Replay) = false; want true (wrapped)")
	}
	if !errors.Is(wrapped, ErrReplay) {
		t.Errorf("errors.Is(*Replay, ErrReplay) = false; want true")
	}
}
