package state_test

// PgStore coverage gap tests for the deployment-logs surface.
//
// This file covers Store methods that had no PgStore test before slice 6:
//
//   AppendDeploymentLog: monotonic-seq INSERT...RETURNING shape.
//   ListDeploymentLogs: 5 branches — empty, pagination with hasMore,
//                       beforeSeq boundary, clamp overlarge limit,
//                       FK enforcement on unknown deployment.
//
// Helpers reused: pgStore(t), seedLiveDeploy(t,s,ctx) → (acctID,
// appID, depID). The MemStore contract at memstore_test.go:520-590
// (TestDeploymentLogsAppendAndPage) is the reference for the
// assertion shape — the SQL differs but the contract is identical.

import (
	"context"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// appendN appends n lines for the given deployment, returning the seqs
// in append order (ascending). The lines are "line0", "line1", ..., so
// tests can pin individual entries by content.
//
// Appends are server-side bigserial — seq values are NOT guaranteed
// to start at 1 or be contiguous. The function returns the actual
// returned seqs; callers use those to drive the pagination assertions.
func appendN(t *testing.T, s *state.PgStore, ctx context.Context, depID string, n int) []int64 {
	t.Helper()
	seqs := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		seq, err := s.AppendDeploymentLog(ctx, depID, "stdout", fmt.Sprintf("line%d", i))
		if err != nil {
			t.Fatalf("AppendDeploymentLog[%d]: %v", i, err)
		}
		seqs = append(seqs, seq)
	}
	return seqs
}

// assertPage is a small assertion helper used by the pagination test
// to compare a returned (page, hasMore) tuple against expected
// (len, hasMore) without writing the same 4-line block at every
// boundary.
//
// It is intentionally narrow: pin length, hasMore, and (when length
// is non-zero) the seq of the first and last entry. Per-page content
// is exercised separately to keep each test's failure message
// actionable.
func assertPage(t *testing.T, label string, page []state.LogEntry, hasMore bool, wantLen int, wantHasMore bool, firstSeq, lastSeq int64) {
	t.Helper()
	if len(page) != wantLen {
		t.Errorf("%s page len = %d, want %d", label, len(page), wantLen)
	}
	if hasMore != wantHasMore {
		t.Errorf("%s hasMore = %v, want %v", label, hasMore, wantHasMore)
	}
	if wantLen == 0 {
		return
	}
	if page[0].Seq != firstSeq {
		t.Errorf("%s page[0].Seq = %d, want %d", label, page[0].Seq, firstSeq)
	}
	if page[len(page)-1].Seq != lastSeq {
		t.Errorf("%s page[%d].Seq = %d, want %d", label, len(page)-1, page[len(page)-1].Seq, lastSeq)
	}
}

// TestPg_AppendDeploymentLog_ReturnsMonotonicSeq pins the
// INSERT...RETURNING seq shape: every appended line gets a fresh seq,
// strictly greater than the previous one. The dashboard's SSE stream
// tails by `seq > cursor` so monotonicity is the load-bearing invariant.
func TestPg_AppendDeploymentLog_ReturnsMonotonicSeq(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedLiveDeploy(t, s, ctx)

	seqs := appendN(t, s, ctx, depID, 5)
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Errorf("seq[%d]=%d not strictly greater than seq[%d]=%d", i, seqs[i], i-1, seqs[i-1])
		}
	}
}

// TestPg_ListDeploymentLogs_EmptyDeploymentReturnsEmpty pins the
// "no rows yet" branch — the SSE handler always opens with a page,
// even when nothing has been logged yet. Both result and hasMore must
// be empty.
func TestPg_ListDeploymentLogs_EmptyDeploymentReturnsEmpty(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedLiveDeploy(t, s, ctx)

	page, hasMore, err := s.ListDeploymentLogs(ctx, depID, 0, 50)
	if err != nil {
		t.Fatalf("ListDeploymentLogs: %v", err)
	}
	assertPage(t, "empty", page, hasMore, 0, false, 0, 0)
}

// TestPg_ListDeploymentLogs_PaginationWithHasMore is the load-bearing
// pagination test. We append 200 rows and page through them with a
// limit of 50 — exactly the boundary case that the F-7 review finding
// flagged. Every page except the last must report hasMore=true; the
// last page must report hasMore=false with exactly 50 rows.
//
// The MemStore contract (memstore_test.go:520) is the reference; the
// SQL differs but the contract is identical.
func TestPg_ListDeploymentLogs_PaginationWithHasMore(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedLiveDeploy(t, s, ctx)

	const total = 200
	const limit = 50
	seqs := appendN(t, s, ctx, depID, total)
	oldest := seqs[0]           // smallest seq — appears on the last page
	newest := seqs[len(seqs)-1] // largest seq — appears on the first page

	// Page 1: 50 newest rows, hasMore=true.
	page1, hasMore1, err := s.ListDeploymentLogs(ctx, depID, 0, limit)
	if err != nil {
		t.Fatalf("ListDeploymentLogs(page1): %v", err)
	}
	assertPage(t, "page1", page1, hasMore1, limit, true, newest, newest-int64(limit-1))

	// Page 2: next 50, hasMore=true. Cursor = page1[len-1].Seq.
	cursor2 := page1[len(page1)-1].Seq
	page2, hasMore2, err := s.ListDeploymentLogs(ctx, depID, cursor2, limit)
	if err != nil {
		t.Fatalf("ListDeploymentLogs(page2): %v", err)
	}
	assertPage(t, "page2", page2, hasMore2, limit, true, cursor2-1, cursor2-int64(limit))

	// Page 3: middle 50.
	cursor3 := page2[len(page2)-1].Seq
	page3, hasMore3, err := s.ListDeploymentLogs(ctx, depID, cursor3, limit)
	if err != nil {
		t.Fatalf("ListDeploymentLogs(page3): %v", err)
	}
	assertPage(t, "page3", page3, hasMore3, limit, true, cursor3-1, cursor3-int64(limit))

	// Page 4: oldest 50 — hasMore MUST be false (this is the F-7
	// boundary: page exactly = limit but no rows beyond).
	cursor4 := page3[len(page3)-1].Seq
	page4, hasMore4, err := s.ListDeploymentLogs(ctx, depID, cursor4, limit)
	if err != nil {
		t.Fatalf("ListDeploymentLogs(page4): %v", err)
	}
	assertPage(t, "page4", page4, hasMore4, limit, false, cursor4-1, oldest)

	// Page 5: past the oldest — empty.
	cursor5 := page4[len(page4)-1].Seq
	page5, hasMore5, err := s.ListDeploymentLogs(ctx, depID, cursor5, limit)
	if err != nil {
		t.Fatalf("ListDeploymentLogs(page5): %v", err)
	}
	assertPage(t, "page5", page5, hasMore5, 0, false, 0, 0)
}

// TestPg_ListDeploymentLogs_BoundaryBeforeSeq pins the
// `seq < $2` WHERE branch: a cursor equal to a known seq excludes
// that seq (strict-less), so the page begins at seq-1.
//
// This is the subtle boundary that "before" semantics (cursor
// exclusive of itself) demands. A future refactor that flips the
// comparator to `<=` would silently re-emit the cursor row.
func TestPg_ListDeploymentLogs_BoundaryBeforeSeq(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedLiveDeploy(t, s, ctx)

	seqs := appendN(t, s, ctx, depID, 3)
	// Cursor = seqs[1] (the middle seq). Page must contain only
	// seqs[0] — exactly 1 row, hasMore=false.
	page, hasMore, err := s.ListDeploymentLogs(ctx, depID, seqs[1], 50)
	if err != nil {
		t.Fatalf("ListDeploymentLogs: %v", err)
	}
	assertPage(t, "boundary", page, hasMore, 1, false, seqs[0], seqs[0])
}

// TestPg_ListDeploymentLogs_ClampsOverlargeLimit pins the clamp at
// MaxDeploymentLogPage=500. The implementation's contract is
// caller-supplied limit > 500 must clamp to 500; a hostile caller
// cannot trigger an oversized slice allocation.
func TestPg_ListDeploymentLogs_ClampsOverlargeLimit(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedLiveDeploy(t, s, ctx)

	// Seed enough rows to overflow the clamp — twice the cap.
	const total = state.MaxDeploymentLogPage * 2
	appendN(t, s, ctx, depID, total)

	// Caller requests 1_000_000 → must clamp to MaxDeploymentLogPage.
	page, hasMore, err := s.ListDeploymentLogs(ctx, depID, 0, 1_000_000)
	if err != nil {
		t.Fatalf("ListDeploymentLogs: %v", err)
	}
	if len(page) != state.MaxDeploymentLogPage {
		t.Errorf("clamped page len = %d, want %d", len(page), state.MaxDeploymentLogPage)
	}
	if !hasMore {
		t.Errorf("hasMore = false, want true (rows remain past the clamp)")
	}
}

// TestPg_AppendDeploymentLog_FKEnforcedOnUnknownDeployment pins the
// FK contract: deployment_logs.deployment_id → deployments.id
// (migration 00006). A bogus deployment id must error, NOT be wrapped
// as ErrNotFound by the store — the foreign-key violation bubbles
// up as the raw pgx error.
//
// The dashboard's never-logged-yet open path is in
// TestPg_ListDeploymentLogs_EmptyDeploymentReturnsEmpty above; this
// test pins the error side of the same scenario for the Append path.
func TestPg_AppendDeploymentLog_FKEnforcedOnUnknownDeployment(t *testing.T) {
	s, ctx := pgStore(t)
	_, err := s.AppendDeploymentLog(ctx, "00000000-0000-0000-0000-000000000000", "stdout", "line0")
	if err == nil {
		t.Fatalf("AppendDeploymentLog(unknown dep) = nil, want FK violation error")
	}
}
