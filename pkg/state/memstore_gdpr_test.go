package state

// GDPR ledger round-trips on MemStore. Mirrors pgstore_gdpr_test.go
// for the in-memory implementation — same property surface, no Postgres
// required so this file always runs (no pgtest.Open skip). The
// MemStore's behavior is the spec the dashboard wires against in
// `make test` runs and on developer laptops; PgStore must keep parity
// for CI (spec §17 G6, ADR-021).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// memTestGdprAccount mints a fresh account in the MemStore and returns
// its id. The gdpr_requests ledger is keyed by account_id; per-test
// accounts keep the tests order-independent and avoid `t.Parallel()`
// shenanigans (the MemStore is not safe for concurrent test use).
func memTestGdprAccount(t *testing.T, m *MemStore) string {
	t.Helper()
	email := fmt.Sprintf("mem-gdpr+%s@example.com", t.Name())
	acct, err := m.CreateAccount(context.Background(), email, api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	return acct.ID
}

// TestMem_AppendGdprRequest_RoundTrip exercises the happy path on the
// in-memory ledger. Catches column-name typos in scanAccounts-style
// accessors and validates that the append-side default timestamp is
// applied when RequestedAt is zero.
func TestMem_AppendGdprRequest_RoundTrip(t *testing.T) {
	m := NewMemStore()
	acctID := memTestGdprAccount(t, m)

	id := uuid.NewString()
	at := time.Date(2026, 1, 15, 12, 30, 0, 0, time.UTC)
	if err := m.AppendGdprRequest(context.Background(), GdprRequest{
		ID:           id,
		AccountID:    acctID,
		AccountEmail: "mem-round-trip@example.com",
		Action:       GdprActionRestore,
		RequestedAt:  at,
	}); err != nil {
		t.Fatalf("AppendGdprRequest: %v", err)
	}
	rows, err := m.ListGdprRequestsForAccount(context.Background(), acctID, 10)
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
	if r.AccountEmail != "mem-round-trip@example.com" {
		t.Errorf("AccountEmail = %q, want %q", r.AccountEmail, "mem-round-trip@example.com")
	}
	if r.Action != GdprActionRestore {
		t.Errorf("Action = %q, want %q", r.Action, GdprActionRestore)
	}
	if !r.RequestedAt.Equal(at) {
		t.Errorf("RequestedAt = %v, want %v", r.RequestedAt, at)
	}
	if !r.CompletedAt.IsZero() {
		t.Errorf("CompletedAt = %v, want zero", r.CompletedAt)
	}
}

// TestMem_AppendGdprRequest_EmptyIDReturnsError confirms the
// empty-ID guard fires before mutating the ledger. Same rationale as
// the PgStore test: the caller is responsible for UUID minting, the
// guard exists to surface programming errors loudly.
func TestMem_AppendGdprRequest_EmptyIDReturnsError(t *testing.T) {
	m := NewMemStore()
	acctID := memTestGdprAccount(t, m)

	err := m.AppendGdprRequest(context.Background(), GdprRequest{
		AccountID: acctID, Action: GdprActionExport,
	})
	if err == nil {
		t.Fatal("AppendGdprRequest with empty ID returned nil, want error")
	}
	if !strings.Contains(err.Error(), "id is required") {
		t.Errorf("error message = %q, want it to mention 'id is required'", err.Error())
	}
	// No row should have been added.
	rows, _ := m.ListGdprRequestsForAccount(context.Background(), acctID, 10)
	if len(rows) != 0 {
		t.Errorf("len(rows) after failed Append = %d, want 0", len(rows))
	}
}

// TestMem_AppendGdprRequest_SetsDefaultTimestamp confirms the zero
// RequestedAt default branch is the same on MemStore as on PgStore.
// We insert with no explicit timestamp, then read back and confirm the
// persisted timestamp is within tolerance of now.
func TestMem_AppendGdprRequest_SetsDefaultTimestamp(t *testing.T) {
	m := NewMemStore()
	acctID := memTestGdprAccount(t, m)

	before := time.Now().UTC().Add(-2 * time.Second)
	if err := m.AppendGdprRequest(context.Background(), GdprRequest{
		ID:        uuid.NewString(),
		AccountID: acctID,
		Action:    GdprActionDelete,
	}); err != nil {
		t.Fatalf("AppendGdprRequest: %v", err)
	}
	after := time.Now().UTC().Add(2 * time.Second)

	rows, _ := m.ListGdprRequestsForAccount(context.Background(), acctID, 10)
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	got := rows[0].RequestedAt
	if got.Before(before) || got.After(after) {
		t.Errorf("RequestedAt = %v, want within [%v, %v]", got, before, after)
	}
}

// TestMem_ListGdprRequestsForAccount_LimitZero confirms the API
// convention `limit <= 0` returns a nil/empty slice without erroring.
// MemStore returns literal nil (per the source: `return nil, nil`)
// for this case; callers should treat nil/empty equivalently.
func TestMem_ListGdprRequestsForAccount_LimitZero(t *testing.T) {
	m := NewMemStore()
	acctID := memTestGdprAccount(t, m)

	if err := m.AppendGdprRequest(context.Background(), GdprRequest{
		ID: uuid.NewString(), AccountID: acctID, Action: GdprActionExport,
		RequestedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("AppendGdprRequest: %v", err)
	}
	for _, lim := range []int{0, -1, -100} {
		rows, err := m.ListGdprRequestsForAccount(context.Background(), acctID, lim)
		if err != nil {
			t.Errorf("limit=%d: %v", lim, err)
		}
		if len(rows) != 0 {
			t.Errorf("limit=%d: len(rows) = %d, want 0", lim, len(rows))
		}
	}
}

// TestMem_CompleteGdprRequest_StampsCompletedAt confirms the happy
// path of Complete on MemStore. Same contract as PgStore: Append →
// Complete → List shows CompletedAt non-zero. The in-memory impl walks
// the slice in reverse (most recent first), matching the SQL `ORDER BY
// requested_at DESC` semantics.
func TestMem_CompleteGdprRequest_StampsCompletedAt(t *testing.T) {
	m := NewMemStore()
	acctID := memTestGdprAccount(t, m)

	id := uuid.NewString()
	if err := m.AppendGdprRequest(context.Background(), GdprRequest{
		ID: id, AccountID: acctID, Action: GdprActionDelete,
		RequestedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("AppendGdprRequest: %v", err)
	}
	if err := m.CompleteGdprRequest(context.Background(), acctID, string(GdprActionDelete)); err != nil {
		t.Fatalf("CompleteGdprRequest: %v", err)
	}
	rows, err := m.ListGdprRequestsForAccount(context.Background(), acctID, 10)
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

// TestMem_CompleteGdprRequest_NoMatchReturnsErrNotFound confirms the
// not-found sentinel. pg/grace uses this same ErrNotFound signal to
// detect a stale tick on MemStore (in tests / dev shells).
func TestMem_CompleteGdprRequest_NoMatchReturnsErrNotFound(t *testing.T) {
	m := NewMemStore()

	err := m.CompleteGdprRequest(context.Background(), "", string(GdprActionDelete))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Complete on empty account: err = %v, want ErrNotFound", err)
	}
}
