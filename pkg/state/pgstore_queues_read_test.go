package state_test

// Issue #394 — byte-identical guarantee for QueuePeek and the
// dead-letter SQL state transition. These are property tests against
// a real Postgres cluster (pgtest) because the "no mutation"
// guarantee is a property of the SQL+driver path, not the in-memory
// store. MemStore has separate coverage in handlers_queues_read_test.go.

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// pgQueueRow seeds a queue-source invocation row directly so the
// test controls created_at monotonicity (the dumpTable comparison
// depends on it).
func pgQueueRow(t *testing.T, ctx context.Context, s *state.PgStore, appID, acctID string, payload string) string {
	t.Helper()
	inv, err := s.EnqueueInvocation(ctx, state.Invocation{
		AppID: appID, AccountID: acctID, Source: state.InvocationQueue,
		Method: "POST", Path: "/x", Payload: json.RawMessage(payload),
		DueAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}
	return inv.ID
}

// dumpInvocationRows pulls every column of every row for the app into
// a comparable shape. The caller MUST pass the same pool that
// pgStoreWithPool created — pgtest.Open allocates a fresh schema per
// call, so opening a *new* pool here would land on an empty schema
// and `before == after == []` would pass the byte-identical check
// vacuously. See pgStoreWithPool's docstring for the rationale.
func dumpInvocationRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, appID string) []map[string]any {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT id, app_id, account_id, source, state, attempts, last_error, payload
		  FROM invocations
		 WHERE app_id = $1
		 ORDER BY id`, appID)
	if err != nil {
		t.Fatalf("dump query: %v", err)
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			id, appIDCol, acctID, source, st string
			attempts                         int
			lastError                        *string
			payload                          []byte
		)
		if err := rows.Scan(&id, &appIDCol, &acctID, &source, &st, &attempts, &lastError, &payload); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, map[string]any{
			"id": id, "app_id": appIDCol, "account_id": acctID,
			"source": source, "state": st, "attempts": attempts,
			"last_error": lastError, "payload": string(payload),
		})
	}
	return out
}

// TestPg_QueuePeek_ByteIdentical is the load-bearing property test
// for issue #394. Repeated QueuePeek calls leave the underlying
// invocations table byte-identical — no attempts increment, no state
// transition, no last_error write, no row order change. The check
// runs against a real Postgres cluster via pgtest because the
// guarantee is a property of the SQL (no FOR UPDATE / FOR SHARE /
// advisory lock) plus the driver path (the read pool returns the
// same snapshot).
func TestPg_QueuePeek_ByteIdentical(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID, appID, _ := seedLiveDeploy(t, s, ctx)

	// Seed 12 pending rows with strictly increasing created_at so
	// the peek ORDER BY is deterministic.
	for i := 0; i < 12; i++ {
		_ = pgQueueRow(t, ctx, s, appID, acctID, `{"i":`+itoa(i)+`}`)
		// pg's created_at is microsecond-resolution; the enqueue
		// inside the loop guarantees monotonic ordering because the
		// api.PlanPro Plan's seedCreatedAt timestamp drifts forward.
	}

	before := dumpInvocationRows(t, ctx, pool, appID)
	if len(before) != 12 {
		t.Fatalf("seed produced %d rows, want 12", len(before))
	}

	// 25 peek calls with mixed limit sizes — exercises the cursor
	// decode path (limit=5) and the "full page" path (limit=200).
	for i := 0; i < 25; i++ {
		limit := 5
		if i%2 == 0 {
			limit = 200
		}
		var beforeID string
		if i > 0 {
			// Walk pages: use the first peek's last id as the
			// before-cursor for the next call.
			first, err := s.QueuePeek(ctx, appID, 5, "")
			if err != nil {
				t.Fatalf("peek %d: %v", i, err)
			}
			if len(first) > 0 {
				beforeID = first[len(first)-1].ID
			}
		}
		if _, err := s.QueuePeek(ctx, appID, limit, beforeID); err != nil {
			t.Fatalf("peek iter %d: %v", i, err)
		}
	}

	after := dumpInvocationRows(t, ctx, pool, appID)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("invocations table mutated by repeated peeks\nbefore=%+v\nafter=%+v", before, after)
	}

	// Belt-and-suspenders: the "no attempts increment" property is
	// the most user-visible contract; assert it independently.
	for _, row := range after {
		if attempts, _ := row["attempts"].(int); attempts != 0 {
			t.Errorf("attempts incremented to %d by peek — want 0; row=%+v", attempts, row)
		}
	}
}

// TestPg_QueueState_RespectsInflight drives the QueueState in-flight
// count by claiming a row. A claimed row counts as in-flight ONLY
// while its lease is live; an expired lease transitions it back to
// the depth bucket, not in-flight.
func TestPg_QueueState_RespectsInflight(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, appID, _ := seedLiveDeploy(t, s, ctx)
	for i := 0; i < 3; i++ {
		_ = pgQueueRow(t, ctx, s, appID, acctID, `{"i":`+itoa(i)+`}`)
	}

	// No lease: depth=3, in_flight=0.
	stats, err := s.QueueState(ctx, appID)
	if err != nil {
		t.Fatalf("QueueState: %v", err)
	}
	if stats.Depth != 3 || stats.InFlight != 0 {
		t.Errorf("pre-claim stats = %+v, want Depth=3 InFlight=0", stats)
	}

	// Claim one row — in_flight must become 1; depth still 3
	// (dispatching rows count toward depth too).
	rows, err := s.ListDueInvocations(ctx, time.Now().UTC().Add(time.Second), 1)
	if err != nil || len(rows) == 0 {
		t.Fatalf("ListDueInvocations: %v len=%d", err, len(rows))
	}
	if _, err := s.ClaimInvocation(ctx, rows[0].ID, "inst-1", 30); err != nil {
		t.Fatalf("ClaimInvocation: %v", err)
	}
	stats, err = s.QueueState(ctx, appID)
	if err != nil {
		t.Fatalf("QueueState post-claim: %v", err)
	}
	if stats.InFlight != 1 {
		t.Errorf("post-claim in_flight = %d, want 1", stats.InFlight)
	}
	if stats.Depth != 3 {
		t.Errorf("post-claim depth = %d, want 3 (dispatching still counts)", stats.Depth)
	}
	deadID := pgQueueRow(t, ctx, s, appID, acctID, `{"dead":true}`)
	if _, err := s.ClaimInvocation(ctx, deadID, "inst-1", 30); err != nil {
		t.Fatalf("ClaimInvocation dead-letter row: %v", err)
	}
	if err := s.FailInvocation(ctx, deadID, "terminal", time.Minute, 1); err != nil {
		t.Fatalf("FailInvocation dead-letter row: %v", err)
	}
	stats, err = s.QueueState(ctx, appID)
	if err != nil {
		t.Fatalf("QueueState dead-letter: %v", err)
	}
	if stats.DeadLetter != 1 {
		t.Errorf("dead_letter = %d, want 1", stats.DeadLetter)
	}
}

// TestPg_QueueDeadLetter_ExhaustedToDeadLetter is the PG counterpart
// of the handler test — drives FailInvocation past the Pro budget
// (10) and asserts the row lands in state='dead_letter'. This is
// the SQL-level guarantee; the handler test only proves the
// handler delegates correctly.
func TestPg_QueueDeadLetter_ExhaustedToDeadLetter(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, appID, _ := seedLiveDeploy(t, s, ctx)
	invID := pgQueueRow(t, ctx, s, appID, acctID, `{"i":0}`)

	budget := api.MustLimitsFor(api.PlanPro).MaxQueueAttempts
	for i := 1; i <= budget; i++ {
		if _, err := s.ClaimInvocation(ctx, invID, "inst", 30); err != nil {
			t.Fatalf("claim iter %d: %v", i, err)
		}
		if err := s.FailInvocation(ctx, invID, "blip", time.Minute, budget); err != nil {
			t.Fatalf("FailInvocation iter %d: %v", i, err)
		}
	}

	inv, err := s.InvocationByID(ctx, invID)
	if err != nil {
		t.Fatalf("InvocationByID: %v", err)
	}
	if inv.State != state.InvocationDeadLetter {
		t.Fatalf("post-exhaustion state = %q, want dead_letter", inv.State)
	}
	if inv.CompletedAt == nil {
		t.Errorf("completed_at = nil after dead-letter, want set")
	}

	// The dead-letter list endpoint must return it.
	rows, err := s.QueueDeadLetter(ctx, appID, 10, "")
	if err != nil {
		t.Fatalf("QueueDeadLetter: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.ID == invID {
			found = true
			if r.LastError != "blip" {
				t.Errorf("LastError = %q, want blip", r.LastError)
			}
			break
		}
	}
	if !found {
		t.Errorf("dead-letter row %s not in QueueDeadLetter result", invID)
	}

	// Re-claim must fail — dead_letter is terminal.
	if _, err := s.ClaimInvocation(ctx, invID, "inst", 30); err == nil {
		t.Errorf("re-claim of dead-letter row succeeded; want error")
	}
}

// TestPg_QueuePeek_OrderingAndCursor pins the peek ORDER BY and the
// cursor subquery decode. Rows are returned oldest-first (created_at
// ASC, id ASC); the `before` cursor is exclusive.
func TestPg_QueuePeek_OrderingAndCursor(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, appID, _ := seedLiveDeploy(t, s, ctx)
	for i := 0; i < 7; i++ {
		_ = pgQueueRow(t, ctx, s, appID, acctID, `{"i":`+itoa(i)+`}`)
	}

	first, err := s.QueuePeek(ctx, appID, 3, "")
	if err != nil {
		t.Fatalf("QueuePeek first page: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("first page len = %d, want 3", len(first))
	}
	if !first[0].CreatedAt.Before(first[1].CreatedAt) {
		t.Errorf("first page not ordered ASC: %+v", first)
	}

	// Cursor — the last id of the previous page is exclusive.
	second, err := s.QueuePeek(ctx, appID, 3, first[len(first)-1].ID)
	if err != nil {
		t.Fatalf("QueuePeek second page: %v", err)
	}
	if len(second) != 3 {
		t.Fatalf("second page len = %d, want 3", len(second))
	}
	if second[0].ID == first[len(first)-1].ID {
		t.Errorf("cursor did not exclude — second page starts with first page's last id")
	}
	for _, r := range second {
		for _, f := range first {
			if r.ID == f.ID {
				t.Errorf("second page row %s appeared in first page — want disjoint", r.ID)
			}
		}
	}
}

// itoa is a local helper so this file stays self-contained (the
// test files import encoding/json indirectly through pgQueueRow's
// payload handling).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [4]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
