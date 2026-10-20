// memstore_paddle_overage_schema_test.go — pins the contract that
// PaddleOverageDedupeSchema reports TableExists=false when the
// memstore has rows only in the legacy per-month dedupe set
// (paddleOverageMonths, populated by RecordPaddleOverageMonth) and
// nothing in the per-window dedupe set (paddleOverageWindows,
// populated by ClaimPaddleOverageWindow / CompletePaddleOverageWindow).
//
// Why this matters: the B4 pre-flight is meant to certify the
// per-window pusher has a backing table with the 00041 columns. A
// memstore that has only months-rows proves the window pusher has
// never run — a "table exists, columns=ok" hint for that path
// would mislead the operator. The regression-test decision: the
// memstore mirrors production semantics, and production would
// have a fully-populated table only after the pusher has run;
// months-only in memstore is a state production never has, so
// the probe should report TableExists=false (let the CLI emit
// "apply 00034 then 00041") rather than a false green light.

package state_test

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

// TestMemStorePaddleOverageDedupeSchema_FreshStaysMissing asserts
// the baseline: a memstore with no months-row and no windows-row
// reports TableExists=false and all four HasX=false. This is the
// tripwire the CLI maps to "apply 00034 then 00041".
func TestMemStorePaddleOverageDedupeSchema_FreshStaysMissing(t *testing.T) {
	m := state.NewMemStore()
	res, err := m.PaddleOverageDedupeSchema(context.Background())
	if err != nil {
		t.Fatalf("PaddleOverageDedupeSchema: %v", err)
	}
	if res.TableExists {
		t.Errorf("TableExists = true on fresh memstore, want false")
	}
	if res.HasWindowStart || res.HasState || res.HasClaimedAt || res.HasClaimedBy {
		t.Errorf("all HasX must be false on fresh memstore; got %+v", res)
	}
	if res.PendingRows != 0 || res.CompletedRows != 0 {
		t.Errorf("counts on fresh memstore must be 0; got pending=%d completed=%d",
			res.PendingRows, res.CompletedRows)
	}
}

// TestMemStorePaddleOverageDedupeSchema_MonthsOnlyStaysMissing is
// the regression-pin for the review finding: the legacy per-month
// dedupe path (RecordPaddleOverageMonth) writes to
// paddleOverageMonths, not paddleOverageWindows. The pre-flight
// is window-pusher-shaped, so a memstore with only months-rows
// must report TableExists=false to avoid a false green light —
// the window pusher has never been exercised in that state.
//
// A future regression that re-adds the OR over
// (paddleOverageMonths, paddleOverageWindows) will flip this
// test red; the memstore must key off paddleOverageWindows only.
func TestMemStorePaddleOverageDedupeSchema_MonthsOnlyStaysMissing(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()
	month := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if err := m.RecordPaddleOverageMonth(ctx, "acct-A", month); err != nil {
		t.Fatalf("RecordPaddleOverageMonth: %v", err)
	}
	res, err := m.PaddleOverageDedupeSchema(ctx)
	if err != nil {
		t.Fatalf("PaddleOverageDedupeSchema: %v", err)
	}
	if res.TableExists {
		t.Errorf("TableExists = true with months-only rows, want false " +
			"(the pre-flight is window-pusher-shaped; a months-only " +
			"memstore has never exercised the window path)")
	}
	if res.HasWindowStart || res.HasState || res.HasClaimedAt || res.HasClaimedBy {
		t.Errorf("all HasX must be false with months-only rows; got %+v", res)
	}
	if res.PendingRows != 0 || res.CompletedRows != 0 {
		t.Errorf("counts must be 0 with months-only rows; got pending=%d completed=%d",
			res.PendingRows, res.CompletedRows)
	}
}

// TestMemStorePaddleOverageDedupeSchema_WindowsRowsReportShape is
// the happy path: once ClaimPaddleOverageWindow has rows (regardless
// of whether CompletePaddleOverageWindow has been called), the
// probe flips TableExists=true and all four HasX=true. The counts
// then match the per-row state.
func TestMemStorePaddleOverageDedupeSchema_WindowsRowsReportShape(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Hour)
	lease := time.Hour
	if ok, err := m.ClaimPaddleOverageWindow(ctx, "acct-A", now, "pod-1", lease); err != nil || !ok {
		t.Fatalf("claim A: ok=%v err=%v", ok, err)
	}
	if ok, err := m.ClaimPaddleOverageWindow(ctx, "acct-B", now.Add(time.Hour), "pod-2", lease); err != nil || !ok {
		t.Fatalf("claim B: ok=%v err=%v", ok, err)
	}
	if err := m.CompletePaddleOverageWindow(ctx, "acct-B", now.Add(time.Hour), 100); err != nil {
		t.Fatalf("complete B: %v", err)
	}
	res, err := m.PaddleOverageDedupeSchema(ctx)
	if err != nil {
		t.Fatalf("PaddleOverageDedupeSchema: %v", err)
	}
	if !res.TableExists {
		t.Errorf("TableExists = false after seeding windows rows, want true")
	}
	if !res.HasWindowStart || !res.HasState || !res.HasClaimedAt || !res.HasClaimedBy {
		t.Errorf("all HasX must be true after seeding; got %+v", res)
	}
	if res.PendingRows != 1 {
		t.Errorf("PendingRows = %d, want 1 (acct-A@now)", res.PendingRows)
	}
	if res.CompletedRows != 1 {
		t.Errorf("CompletedRows = %d, want 1 (acct-B@now+1h)", res.CompletedRows)
	}
}
