package apphealth

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// countingClaims counts the collector's claims and the ones that found
// nothing due.
type countingClaims struct {
	CollectorStore
	claims, empty atomic.Int32
}

func (s *countingClaims) ClaimAppHealth(ctx context.Context, token string, now time.Time) (state.AppHealthClaim, error) {
	s.claims.Add(1)
	claim, err := s.CollectorStore.ClaimAppHealth(ctx, token, now)
	if err != nil {
		s.empty.Add(1)
	}
	return claim, err
}

// Each claim scans the apps on the control plane. A sweep that finds nothing
// due costs one claim, not one per worker, and a sweep that collects a few
// apps stops claiming once the queue is empty (prod 2026-11-10: ~4 of 5.6
// claims a second were empty polls).
func TestAppHealthCollectorClaimsOnlyWhatIsDue(t *testing.T) {
	s, account, _ := collectorFixture(t)
	const due = 3
	for i := 1; i < due; i++ {
		if _, err := s.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: fmt.Sprintf("claims-%d", i), Status: state.AppActive}); err != nil {
			t.Fatal(err)
		}
	}
	store := &countingClaims{CollectorStore: s}
	c := Collector{Store: store}
	n, err := c.Sweep(t.Context())
	if err != nil || n != due {
		t.Fatalf("sweep collected %d %v, want %d", n, err, due)
	}
	if empty := store.empty.Load(); empty < 1 || int(empty) > api.AppHealthCollectorWorkers {
		t.Fatalf("busy sweep made %d empty claims, want 1..%d", empty, api.AppHealthCollectorWorkers)
	}

	store.claims.Store(0)
	store.empty.Store(0)
	for range 3 {
		if n, err := c.Sweep(t.Context()); err != nil || n != 0 {
			t.Fatalf("idle sweep collected %d %v, want 0", n, err)
		}
	}
	if got := store.claims.Load(); got != 3 {
		t.Fatalf("3 idle sweeps made %d claims, want 3 (one probe each)", got)
	}
}
