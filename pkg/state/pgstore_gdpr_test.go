package state_test

// GDPR ledger round-trips on PgStore. The spec calls the GDPR triad
// (Append / List / Complete) the audit log a DPO queries after a
// customer erasure — spec §17 G6, ADR-021. These tests pin the
// contract: Append defaults RequestedAt to now(), List returns rows
// in DESC order with limit=0 yielding an empty slice, Complete stamps
// completed_at on the most recent un-completed row or returns
// ErrNotFound. Mirrors the style of pgstore_account_deletion_test.go
// (pgtest.Open so the file skips cleanly when Postgres is offline).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// pgTestGdprAccount creates a fresh account + returns its id. The
// gdpr_requests ledger is keyed by account_id, so each test gets its
// own account to avoid cross-test bleed under `go test -count=N`.
func pgTestGdprAccount(t *testing.T, s *state.PgStore, ctx context.Context) string {
	t.Helper()
	email := fmt.Sprintf("gdpr+%s@example.com", t.Name())
	acct, err := s.CreateAccount(ctx, email, api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	return acct.ID
}

// TestPg_AppendGdprRequest_SetsDefaultTimestamp confirms the zero-value
// `RequestedAt` is overwritten with `time.Now().UTC()` by the call
// site. We seed a request with `RequestedAt = time.Time{}` and read it
// back; the persisted timestamp must be within a few seconds of now.
func TestPg_AppendGdprRequest_SetsDefaultTimestamp(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := pgTestGdprAccount(t, s, ctx)

	before := time.Now().UTC().Add(-2 * time.Second)
	if err := s.AppendGdprRequest(ctx, state.GdprRequest{
		ID:           uuid.NewString(),
		AccountID:    acctID,
		AccountEmail: "gdpr@example.com",
		Action:       state.GdprActionExport,
	}); err != nil {
		t.Fatalf("AppendGdprRequest: %v", err)
	}
	after := time.Now().UTC().Add(2 * time.Second)

	rows, err := s.ListGdprRequestsForAccount(ctx, acctID, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	got := rows[0].RequestedAt
	if got.Before(before) || got.After(after) {
		t.Errorf("RequestedAt = %v, want within [%v, %v]", got, before, after)
	}
}

// TestPg_AppendGdprRequest_EmptyIDReturnsError confirms the empty-ID
// guard fires before any SQL is attempted. The caller is responsible
// for minting a UUID — this is a defensive check that surfaces
// programming errors loudly rather than letting the INSERT fail with a
// generic FK error.
func TestPg_AppendGdprRequest_EmptyIDReturnsError(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := pgTestGdprAccount(t, s, ctx)

	err := s.AppendGdprRequest(ctx, state.GdprRequest{
		AccountID: acctID, Action: state.GdprActionExport,
	})
	if err == nil {
		t.Fatal("AppendGdprRequest with empty ID returned nil, want error")
	}
	if !strings.Contains(err.Error(), "id is required") {
		t.Errorf("error message = %q, want it to mention 'id is required'", err.Error())
	}
}

// TestPg_AppendGdprRequest_RoundTrip exercises the happy path: insert a
// row with a non-default timestamp, then read it back and confirm every
// field round-trips. Catches column-name typos in the INSERT/SELECT
// pair that would otherwise pass a "list returned 1 row" smoke check.
func TestPg_AppendGdprRequest_RoundTrip(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := pgTestGdprAccount(t, s, ctx)

	id := uuid.NewString()
	at := time.Date(2026, 1, 15, 12, 30, 0, 0, time.UTC)
	if err := s.AppendGdprRequest(ctx, state.GdprRequest{
		ID:           id,
		AccountID:    acctID,
		AccountEmail: "round-trip@example.com",
		Action:       state.GdprActionDelete,
		RequestedAt:  at,
	}); err != nil {
		t.Fatalf("AppendGdprRequest: %v", err)
	}
	rows, err := s.ListGdprRequestsForAccount(ctx, acctID, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.ID != id {
		t.Errorf("ID = %q, want %q", r.ID, id)
	}
	if r.AccountID != acctID {
		t.Errorf("AccountID = %q, want %q", r.AccountID, acctID)
	}
	if r.AccountEmail != "round-trip@example.com" {
		t.Errorf("AccountEmail = %q, want %q", r.AccountEmail, "round-trip@example.com")
	}
	if r.Action != state.GdprActionDelete {
		t.Errorf("Action = %q, want %q", r.Action, state.GdprActionDelete)
	}
	if !r.RequestedAt.Equal(at) {
		t.Errorf("RequestedAt = %v, want %v", r.RequestedAt, at)
	}
	if !r.CompletedAt.IsZero() {
		t.Errorf("CompletedAt = %v, want zero", r.CompletedAt)
	}
}

// TestPg_ListGdprRequestsForAccount_LimitZero confirms the API
// convention `limit <= 0` returns an empty slice rather than erroring.
// The dashboard uses this to skip the ledger query when an account
// has never had a GDPR action; the SQL `LIMIT 0` would return no rows
// but with extra network round-trip.
func TestPg_ListGdprRequestsForAccount_LimitZero(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := pgTestGdprAccount(t, s, ctx)

	if err := s.AppendGdprRequest(ctx, state.GdprRequest{
		ID: uuid.NewString(), AccountID: acctID, Action: state.GdprActionExport,
		RequestedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("AppendGdprRequest: %v", err)
	}
	for _, lim := range []int{0, -1, -100} {
		rows, err := s.ListGdprRequestsForAccount(ctx, acctID, lim)
		if err != nil {
			t.Errorf("limit=%d: %v", lim, err)
		}
		if len(rows) != 0 {
			t.Errorf("limit=%d: len(rows) = %d, want 0", lim, len(rows))
		}
	}
}

// TestPg_ListGdprRequestsForAccount_OrdersByRequestedAtDesc confirms
// the DESC ordering. Insert three rows with staggered timestamps and
// read them back; the read order must be the reverse of the insert
// order. This is the property the dashboard's "recent activity" view
// relies on.
func TestPg_ListGdprRequestsForAccount_OrdersByRequestedAtDesc(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := pgTestGdprAccount(t, s, ctx)

	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	want := []string{"oldest", "middle", "newest"}
	for i, tag := range want {
		if err := s.AppendGdprRequest(ctx, state.GdprRequest{
			ID: uuid.NewString(), AccountID: acctID, Action: state.GdprActionExport,
			AccountEmail: tag + "@example.com",
			RequestedAt:  base.Add(time.Duration(i) * time.Hour),
		}); err != nil {
			t.Fatalf("append %s: %v", tag, err)
		}
	}
	rows, err := s.ListGdprRequestsForAccount(ctx, acctID, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3", len(rows))
	}
	got := []string{rows[0].AccountEmail, rows[1].AccountEmail, rows[2].AccountEmail}
	wantRev := []string{"newest@example.com", "middle@example.com", "oldest@example.com"}
	for i := range got {
		if got[i] != wantRev[i] {
			t.Errorf("rows[%d].AccountEmail = %q, want %q", i, got[i], wantRev[i])
		}
	}
}

// TestPg_CompleteGdprRequest_StampsCompletedAt confirms the happy path
// of Complete: Append a request, Complete it, List shows the row with
// CompletedAt non-zero. pkg/grace uses this after a successful
// DeleteAccount to mark the deletion-request row as fully resolved.
func TestPg_CompleteGdprRequest_StampsCompletedAt(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := pgTestGdprAccount(t, s, ctx)

	id := uuid.NewString()
	if err := s.AppendGdprRequest(ctx, state.GdprRequest{
		ID: id, AccountID: acctID, Action: state.GdprActionDelete,
		RequestedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("AppendGdprRequest: %v", err)
	}
	if err := s.CompleteGdprRequest(ctx, acctID, string(state.GdprActionDelete)); err != nil {
		t.Fatalf("CompleteGdprRequest: %v", err)
	}
	rows, err := s.ListGdprRequestsForAccount(ctx, acctID, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if rows[0].CompletedAt.IsZero() {
		t.Error("CompletedAt is zero, want non-zero after CompleteGdprRequest")
	}
	if rows[0].ID != id {
		t.Errorf("ID = %q, want %q", rows[0].ID, id)
	}
}

// TestPg_CompleteGdprRequest_NoMatchReturnsErrNotFound confirms the
// not-found sentinel. This is the signal pkg/grace uses to detect a
// stale tick (the request row was already completed by a prior tick or
// never existed for this account/action pair).
func TestPg_CompleteGdprRequest_NoMatchReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)

	// Empty account id → no row matches the (account_id, action) predicate.
	err := s.CompleteGdprRequest(ctx, "", string(state.GdprActionDelete))
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Complete on empty account: err = %v, want ErrNotFound", err)
	}
}

// TestPg_CompleteGdprRequest_EmptyActionReturnsErrNotFound exercises
// the matching branch of the empty-input guard introduced in the
// Slice-3 fix. Empty action would otherwise bind the SQL UPDATE
// against `action = ”` and produce SQLSTATE 22023 (invalid_parameter_value)
// — the production caller in pkg/grace uses the same `errors.Is(err,
// ErrNotFound)` skip to dodge that path, so both empty inputs must
// surface the same sentinel.
func TestPg_CompleteGdprRequest_EmptyActionReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := pgTestGdprAccount(t, s, ctx)

	err := s.CompleteGdprRequest(ctx, acctID, "")
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Complete on empty action: err = %v, want ErrNotFound", err)
	}
	// Sanity: no row was inserted/touched.
	rows, err := s.ListGdprRequestsForAccount(ctx, acctID, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("len(rows) = %d, want 0 (no ledger mutation on guard return)", len(rows))
	}
}

// TestPg_CompleteGdprRequest_NoMatchingRowReturnsErrNotFound covers the
// SQL-execution-then-zero-rows branch of CompleteGdprRequest
// (pgstore.go:2704 — `tag.RowsAffected() == 0`). Distinct from the
// empty-input guard above: here we pass a valid accountID/action but
// the ledger has no row matching, so the UPDATE executes cleanly and
// then returns ErrNotFound. Pins the production contract used by
// pkg/grace for stale-tick detection.
func TestPg_CompleteGdprRequest_NoMatchingRowReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := pgTestGdprAccount(t, s, ctx)

	// Account exists, action string is valid, but we never Append a
	// matching row — the inner SELECT returns 0 rows, the outer UPDATE
	// affects 0 rows, ErrNotFound surfaces.
	err := s.CompleteGdprRequest(ctx, acctID, string(state.GdprActionDelete))
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("Complete on no-row: err = %v, want ErrNotFound", err)
	}
}
