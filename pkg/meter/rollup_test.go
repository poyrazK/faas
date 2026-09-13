// Usage_daily rollup tests (ADR-048 §5). Exercises the OVERWRITE
// (point-in-time) contract under three scenarios:
//
//   1. First tick on a fresh window — INSERT path.
//   2. Second tick on the same window — UPDATE path; the SUM
//      REPLACES the prior partial (NOT additive merge — additive
//      would inflate ~288× per day at 5-min cadence; see PR #428
//      review blocker #1).
//   3. Empty window — 0 rows touched; no error.
//
// Tests use a stub execer so pkg/meter stays pgxpool-free. The
// full SQL is exercised by migrations/00067_test.go against a
// real Postgres; here we assert the contract of RollupOnce /
// RollupLoop on the surface that unit tests can reach — most
// importantly, that rollupSQL does NOT use additive merge.

package meter

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubExecer is a recording stub of the execer interface. It
// captures the (sql, args) tuple for inspection and returns a
// canned rowsAffected / error.
type stubExecer struct {
	mu    sync.Mutex
	calls []stubCall
	rows  int64
	err   error
}

type stubCall struct {
	SQL  string
	Args []any
}

func (s *stubExecer) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Defensive copy of args.
	cp := make([]any, len(args))
	copy(cp, args)
	s.calls = append(s.calls, stubCall{SQL: sql, Args: cp})
	return s.rows, s.err
}

func (s *stubExecer) callsCopy() []stubCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]stubCall, len(s.calls))
	copy(out, s.calls)
	return out
}

func TestRollupOnce_FirstTickCallsExec(t *testing.T) {
	db := &stubExecer{rows: 1}
	start := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	n, err := RollupOnce(context.Background(), db, start, end)
	if err != nil {
		t.Fatalf("RollupOnce: %v", err)
	}
	if n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
	calls := db.callsCopy()
	if len(calls) != 1 {
		t.Fatalf("Exec calls = %d, want 1", len(calls))
	}
	if calls[0].SQL != rollupSQL {
		t.Errorf("SQL mismatch — calls[0].SQL differs from rollupSQL constant")
	}
	if len(calls[0].Args) != 2 {
		t.Fatalf("Exec args = %d, want 2 (start, end)", len(calls[0].Args))
	}
	if !calls[0].Args[0].(time.Time).Equal(start) {
		t.Errorf("arg[0] = %v, want %v", calls[0].Args[0], start)
	}
	if !calls[0].Args[1].(time.Time).Equal(end) {
		t.Errorf("arg[1] = %v, want %v", calls[0].Args[1], end)
	}
}

func TestRollupOnce_ErrorPropagates(t *testing.T) {
	db := &stubExecer{err: context.DeadlineExceeded}
	_, err := RollupOnce(context.Background(), db, time.Now(), time.Now().Add(time.Minute))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want DeadlineExceeded", err)
	}
}

func TestRollupOnce_EmptyWindowReturnsZero(t *testing.T) {
	db := &stubExecer{rows: 0}
	start := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	n, err := RollupOnce(context.Background(), db, start, end)
	if err != nil {
		t.Fatalf("RollupOnce: %v", err)
	}
	if n != 0 {
		t.Errorf("rows = %d, want 0 for empty window", n)
	}
}

func TestRollupLoop_StopsOnContextCancel(t *testing.T) {
	db := &stubExecer{rows: 1}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RollupLoop(ctx, db, 20*time.Millisecond, nil)
		close(done)
	}()
	// Let it tick at least twice (initial + one loop tick).
	time.Sleep(60 * time.Millisecond)
	cancel()
	select {
	case <-done:
		// RollupLoop exited cleanly.
	case <-time.After(2 * time.Second):
		t.Fatalf("RollupLoop did not exit within 2s of cancel")
	}
	calls := db.callsCopy()
	if len(calls) < 2 {
		t.Errorf("Exec calls = %d, want >= 2 (initial + at least one tick)", len(calls))
	}
}

func TestRollupLoop_DefaultInterval(t *testing.T) {
	// Sanity: zero interval doesn't panic, falls back to 5 min.
	db := &stubExecer{rows: 1}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RollupLoop(ctx, db, 0, nil)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
}

func TestDailyRollupWindows_IncludeCurrentPartialDay(t *testing.T) {
	now := time.Date(2026, 9, 13, 17, 37, 11, 0, time.FixedZone("test", 3*60*60))
	windows := dailyRollupWindows(now)
	if len(windows) != 2 {
		t.Fatalf("windows = %d, want yesterday and today", len(windows))
	}
	today := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	if !windows[0].start.Equal(today.Add(-24*time.Hour)) || !windows[0].end.Equal(today) {
		t.Fatalf("yesterday window = [%s,%s)", windows[0].start, windows[0].end)
	}
	if !windows[1].start.Equal(today) || !windows[1].end.Equal(now.UTC()) {
		t.Fatalf("current window = [%s,%s), want [%s,%s)", windows[1].start, windows[1].end, today, now.UTC())
	}
}

// TestRollupSQL_OverwriteSemantics pins the overwrite contract.
// Additive merge on `usage_daily` would multiply the day's totals
// by the number of cron ticks (~288× per day at 5-min cadence) and
// silently inflate the dashboard. If a future refactor flips this
// back to additive, the dashboard's "yesterday's traffic" panel
// would diverge from `usage_minutes` totals — this test catches
// the regression at unit-test time.
//
// We assert that for every numeric column, the ON CONFLICT clause
// uses "col = EXCLUDED.col" rather than "col = col + EXCLUDED.col".
func TestRollupSQL_OverwriteSemantics(t *testing.T) {
	checkCols := []string{
		"mb_seconds", "requests", "cpu_usec", "tx_bytes",
		"net_tx_bytes", "net_rx_bytes", "cold_boot_count",
		"builder_seconds", "tail_seconds",
	}
	for _, c := range checkCols {
		// Restrict the assertion to the UPDATE SET clause so we don't
		// pick up the column name from the SELECT projection above.
		updateClause := extractUpdateClause(rollupSQL)
		if updateClause == "" {
			t.Fatalf("rollupSQL has no ON CONFLICT … DO UPDATE SET clause")
		}
		// Additive form we want to reject.
		if strings.Contains(updateClause, c+" = usage_daily."+c+" + EXCLUDED."+c) {
			t.Errorf("rollupSQL uses additive merge for %q — switch to overwrite (col = EXCLUDED.col); see ADR-048 §5 and PR #428 review blocker #1", c)
		}
		// Overwrite form: allow leading whitespace before `col = EXCLUDED.col`.
		// Strip interior whitespace before matching so the SQL formatter's
		// padding doesn't matter.
		if !containsAssignment(updateClause, c, "EXCLUDED."+c) {
			t.Errorf("rollupSQL missing overwrite assignment for %q in UPDATE SET; got clause: %q", c, updateClause)
		}
	}
}

// extractUpdateClause returns everything from "ON CONFLICT" to the
// end of the SQL — the UPDATE SET clause is at the tail.
func extractUpdateClause(sql string) string {
	idx := strings.Index(sql, "ON CONFLICT")
	if idx < 0 {
		return ""
	}
	return sql[idx:]
}

// containsAssignment returns true if `clause` contains a column
// assignment of the form `<column>[whitespace]=[whitespace]<rhs>`
// (the SQL formatter pads both sides of `=`; we tolerate that).
func containsAssignment(clause, column, rhs string) bool {
	// Normalise whitespace runs to a single space for the lookup.
	needle := column
	// Try all positions of the column name in the clause.
	for i := 0; i < len(clause); {
		j := strings.Index(clause[i:], needle)
		if j < 0 {
			return false
		}
		j += i
		i = j + len(needle)
		// Skip whitespace after column.
		k := i
		for k < len(clause) && (clause[k] == ' ' || clause[k] == '\t' || clause[k] == '\n') {
			k++
		}
		if k >= len(clause) || clause[k] != '=' {
			continue
		}
		// Skip whitespace after `=`.
		k++
		for k < len(clause) && (clause[k] == ' ' || clause[k] == '\t' || clause[k] == '\n') {
			k++
		}
		if k+len(rhs) > len(clause) {
			continue
		}
		if clause[k:k+len(rhs)] == rhs {
			// Make sure the next char (if any) is end-of-token: ',' or
			// whitespace, not part of a longer identifier.
			end := k + len(rhs)
			if end == len(clause) || clause[end] == ',' || clause[end] == ' ' || clause[end] == '\t' || clause[end] == '\n' {
				return true
			}
		}
	}
	return false
}
