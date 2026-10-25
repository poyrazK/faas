package state_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261022105846 amendment 1: shared penalty boxes read back unexpired,
// in (created_at, id) order and paged by cursor; prune removes ended ones.
func TestMemStoreEdgePenaltyBoxes(t *testing.T) {
	testEdgePenaltyBoxes(t, t.Context(), state.NewMemStore())
}

func TestPgStore_EdgePenaltyBoxes(t *testing.T) {
	s, ctx := pgStore(t)
	testEdgePenaltyBoxes(t, ctx, s)
}

func testEdgePenaltyBoxes(t *testing.T, ctx context.Context, s state.EdgePenaltyBoxStore) {
	t.Helper()
	app, rule := uuid.NewString(), uuid.NewString()
	now := time.Now().UTC().Truncate(time.Microsecond)
	v4 := netip.MustParsePrefix("203.0.113.7/32")
	v6 := netip.MustParsePrefix("2001:db8:1:2::/64")
	if err := s.RecordEdgePenaltyBoxes(ctx, []state.EdgePenaltyBox{
		{AppID: app, RuleID: rule, Client: v4, Until: now.Add(10 * time.Minute)},
		{AppID: app, RuleID: rule, Client: netip.MustParsePrefix("198.51.100.9/32"), Until: now.Add(-time.Minute)},
		{AppID: app, RuleID: rule, Client: v6, Until: now.Add(20 * time.Minute)},
	}, db.NotifyEdgePenaltyBox); err != nil {
		t.Fatal(err)
	}
	var cursor state.EdgePenaltyBoxCursor
	var read []state.EdgePenaltyBox
	for range 3 {
		page, clock, err := s.ListEdgePenaltyBoxes(ctx, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		if clock.Before(now.Add(-time.Minute)) {
			t.Fatalf("store clock %v is behind the test's %v", clock, now)
		}
		if len(page) == 0 {
			break
		}
		read = append(read, page...)
		cursor = state.EdgePenaltyBoxCursor{CreatedAt: page[0].CreatedAt, ID: page[0].ID}
	}
	if len(read) != 2 || read[0].Client != v4 || read[1].Client != v6 {
		t.Fatalf("read %+v, want the v4 then the v6 box, the ended one left out", read)
	}
	if b := read[1]; b.AppID != app || b.RuleID != rule || !b.Until.Equal(now.Add(20*time.Minute)) || b.ID <= read[0].ID {
		t.Fatalf("box = %+v", b)
	}
	if n, err := s.PruneEdgePenaltyBoxes(ctx, now); err != nil || n != 1 {
		t.Fatalf("prune = %d, %v; want the ended box", n, err)
	}
	if page, _, err := s.ListEdgePenaltyBoxes(ctx, state.EdgePenaltyBoxCursor{}, 10); err != nil || len(page) != 2 {
		t.Fatalf("after prune = %d boxes, %v; want 2", len(page), err)
	}
}
