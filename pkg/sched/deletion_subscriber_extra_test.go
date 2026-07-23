package sched

// Extra coverage for the deletion subscriber's internal helpers.
// deletion_subscriber_test.go covers the public Run/handle entry
// points; these tests pin the branches inside evictAccount (62.5%
// coverage at sched/deletion_subscriber.go:116) and first64 (75% at
// line 149) that the Run-level tests don't reach.
//
// All tests are package-internal so they can call the unexported
// evictAccount and first64 directly. The seedOneAccount helper from
// deletion_subscriber_test.go is in the same package so it's
// reachable.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// errStore is a minimal state.Store stub used by the
// evictAccount-error-path test. Every method panics except
// ListInstancesForAccount, which returns the configured error. This
// guarantees an accidental call to a different method from
// evictAccount surfaces as a test crash instead of a silent pass.
type errStore struct {
	state.Store
	listErr error
}

func (e *errStore) ListInstancesForAccount(_ context.Context, _ string) ([]state.Instance, error) {
	return nil, e.listErr
}

// newSubscriberForTest constructs a DeletionSubscriber with the
// supplied store. The Engine field is only used for store access,
// so a minimal stub is enough — see the helper below.
func newSubscriberForTest(store state.Store) *DeletionSubscriber {
	return &DeletionSubscriber{
		engine: &Engine{store: store},
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// TestEvictAccount_StoreErrorLogsWarning exercises the error path at
// deletion_subscriber.go:117-122 — if ListInstancesForAccount fails,
// evictAccount returns without panicking and ParkInstance is never
// called. The subscriber's blast radius is intentionally tiny (single
// SQL UPDATE per instance) so a transient store outage must not
// block the channel consumer.
func TestEvictAccount_StoreErrorLogsWarning(t *testing.T) {
	store := &errStore{listErr: errors.New("simulated store failure")}
	sub := newSubscriberForTest(store)

	// Should return without panic. The warn log is captured via the
	// discard-backed logger, so the assertion is "no panic + no row
	// mutation" (the errStore has no UpdateInstanceState backing).
	sub.evictAccount(context.Background(), "any-account")
}

// TestEvictAccount_NoLiveInstances exercises the empty-rows branch
// at deletion_subscriber.go:123-127 — when an account has no
// instances at all, evictAccount logs an info message and returns.
// This is the typical case for a brand-new account that scheduled
// deletion before ever waking an instance.
func TestEvictAccount_NoLiveInstances(t *testing.T) {
	store := state.NewMemStore()
	sub := newSubscriberForTest(store)

	// Create an account but no instances.
	acct, err := store.CreateAccount(context.Background(), "no-instances@example.com", "free")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	sub.evictAccount(context.Background(), acct.ID)

	// Confirm the account still exists (no row-level mutation in
	// the empty-rows branch).
	got, err := store.AccountByID(context.Background(), acct.ID)
	if err != nil {
		t.Fatalf("AccountByID after empty-rows evict: %v", err)
	}
	if got.ID != acct.ID {
		t.Errorf("account row missing after no-op evict: got %q, want %q", got.ID, acct.ID)
	}
}

// TestEvictAccount_SkipsTerminalInstances exercises the per-instance
// `state.IsLive(ins.State)` guard at deletion_subscriber.go:133 —
// only RUNNING / WAKING / COLD_BOOTING instances transition to the
// new "evicting_account_deleting" terminal state. Already-parked
// instances must be skipped (no double-park, no log spam). This is
// the property the §6.1 watchdog relies on to avoid parking rows
// that are already in the parked set.
func TestEvictAccount_SkipsTerminalInstances(t *testing.T) {
	store := state.NewMemStore()
	acct, app, dep := seedOneAccount(t, store, "mixed-account@example.com")
	if _, err := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateRunning), 128, state.DefaultLocalNodeName, ""); err != nil {
		t.Fatalf("create RUNNING instance: %v", err)
	}
	if _, err := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateParked), 128, state.DefaultLocalNodeName, ""); err != nil {
		t.Fatalf("create PARKED instance: %v", err)
	}

	sub := newSubscriberForTest(store)
	sub.evictAccount(context.Background(), acct.ID)

	// Confirm exactly one instance transitioned to EvictingAccountDeleting
	// and exactly one stayed in the parked set. We count rather than
	// peek a specific row's State so a future refactor that swaps the
	// selection (e.g. random pick) can't silently double-park or
	// silently drop the parked check.
	insts, err := store.ListInstancesForAccount(context.Background(), acct.ID)
	if err != nil {
		t.Fatalf("ListInstancesForAccount: %v", err)
	}
	if len(insts) != 2 {
		t.Fatalf("len(insts) = %d, want 2", len(insts))
	}
	var evicting, parked int
	for _, ins := range insts {
		switch ins.State {
		case string(state.StateEvictingAccountDeleting):
			evicting++
		case string(state.StateParked):
			parked++
		}
	}
	if evicting != 1 {
		t.Errorf("EvictingAccountDeleting count = %d, want 1; states = %v", evicting, states(insts))
	}
	if parked != 1 {
		t.Errorf("Parked count = %d, want 1 (must NOT have been re-parked); states = %v", parked, states(insts))
	}
}

// TestFirst64_TrimsAt64Bytes exercises the trim-suffix path at
// deletion_subscriber.go:149-154 — strings longer than 64 bytes get
// cut and a `…` is appended; shorter strings pass through unchanged.
// The helper exists to keep slog lines readable when an upstream
// payload is unexpectedly huge.
func TestFirst64_TrimsAt64Bytes(t *testing.T) {
	// Short string: unchanged.
	if got := first64("hello"); got != "hello" {
		t.Errorf("short: got %q, want %q", got, "hello")
	}
	// Exactly 64 bytes: unchanged (no trim needed).
	exact := strings.Repeat("a", 64)
	if got := first64(exact); got != exact {
		t.Errorf("64-byte exact: got %q, want unchanged", got)
	}
	// 65 bytes: first64 cuts at 64 bytes, then appends "…" (3 bytes
	// in UTF-8 = 1 rune), so the total is 64 + 3 = 67 bytes.
	long := strings.Repeat("a", 65)
	if got := first64(long); len(got) != 67 || !strings.HasSuffix(got, "…") {
		t.Errorf("65-byte: got %q (len %d), want trimmed with … suffix (67 bytes)", got, len(got))
	}
	// Very long: still 64 + 3 = 67 bytes total (the helper slices by
	// byte count, not rune count, so multibyte UTF-8 inputs can
	// truncate mid-rune — that's acceptable; the goal is a bounded
	// log line, not a Unicode-correct one).
	huge := strings.Repeat("z", 10_000)
	if got := first64(huge); len(got) != 67 {
		t.Errorf("10k-byte: len = %d, want 67", len(got))
	}
}

// states is a small helper that returns just the State field of
// each instance — used for assertion-error messages so the reader
// doesn't have to scroll past all the other columns.
func states(insts []state.Instance) []string {
	out := make([]string, len(insts))
	for i, ins := range insts {
		out[i] = ins.State
	}
	return out
}
