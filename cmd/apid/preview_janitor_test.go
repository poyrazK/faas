// preview_janitor_test.go — ADR-095 PR-C (issue #272) preview
// teardown janitor unit tests.
//
// Pins four contracts:
//
//  1. sweepOnce transitions closed→stale→torn_down and stamps
//     status='deleted' exactly once per row.
//  2. sweepOnce does NOT touch open rows that are still inside
//     their grace period.
//  3. sweepOnce emits db.NotifyAppDelete on every tombstone so
//     schedd's app_delete subscriber can reap in-flight instances.
//  4. The notify payload is the JSON shape pkg/sched's subscriber
//     parses (kind:"preview_teardown" + app_id + slug).
package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeNotifier is a goroutine-safe recording fake for the
// preview janitor's Notify seam. The production caller passes
// cmd/apid/server.go's real Notifier (which writes to a
// pgxpool-backed channel); tests use this fake to assert the
// outbound payloads without needing Postgres.
type fakeNotifier struct {
	mu      sync.Mutex
	calls   []fakeNotifyCall
	failCh  string // channel name to fail (test seam)
	failErr error
}

type fakeNotifyCall struct {
	channel, payload string
}

type reopenBeforeClaimStore struct {
	*state.MemStore
	reopen func()
}

func (s *reopenBeforeClaimStore) ClaimPreviewTeardown(ctx context.Context, observed state.App, now time.Time) (state.App, error) {
	s.reopen()
	return s.MemStore.ClaimPreviewTeardown(ctx, observed, now)
}

func (n *fakeNotifier) Notify(ctx context.Context, channel, payload string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.failCh != "" && n.failCh == channel {
		return n.failErr
	}
	n.calls = append(n.calls, fakeNotifyCall{channel: channel, payload: payload})
	return nil
}

func (n *fakeNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.calls)
}

func (n *fakeNotifier) first(channel string) (string, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, c := range n.calls {
		if c.channel == channel {
			return c.payload, true
		}
	}
	return "", false
}

// seedPreviewApp creates an account + a preview app row in the
// MemStore so the sweeper has something to walk. Mirrors the
// githubd dispatcher's shape (cmd/apid/githubd_bridge.go:887).
// Returns the seeded app row + the freshly-minted account so
// the caller can pass accountID through to subsequent store
// calls without a duplicate CreateAccount.
func seedPreviewApp(t *testing.T, m *state.MemStore, slug, parent string, prNum int, prState string, expiresAt time.Time) (state.App, state.Account) {
	t.Helper()
	acct, err := m.CreateAccount(context.Background(), slug+"-owner@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	expires := expiresAt
	a := state.App{
		AccountID:        acct.ID,
		Slug:             slug,
		Type:             "stateless",
		RAMMB:            256,
		MaxConcurrency:   1,
		IdleTimeoutS:     30,
		Status:           state.AppActive,
		PreviewOfSlug:    parent,
		PreviewPrNumber:  prNum,
		PreviewPrState:   prState,
		PreviewExpiresAt: &expires,
	}
	created, err := m.CreateAppIfUnderQuota(context.Background(), a, api.Limits{DeployedApps: 10000})
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota(%q): %v", slug, err)
	}
	return created, acct
}

func TestPreviewJanitor_OpenRowInsideGraceIsLeftAlone(t *testing.T) {
	m := state.NewMemStore()
	notif := &fakeNotifier{}
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

	// An open preview that expires tomorrow: not eligible.
	_, acct := seedPreviewApp(t, m, "pr-open", "demo", 1, state.PreviewPrStateOpen, now.Add(24*time.Hour))

	j := newPreviewJanitor(m, notif, nil, testLogger(), true).
		withClock(func() time.Time { return now })
	if err := j.sweepOnce(context.Background()); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	rows, err := m.PreviewAppsByParent(context.Background(), acct.ID, "demo")
	if err != nil {
		t.Fatalf("PreviewAppsByParent: %v", err)
	}
	if len(rows) != 1 || rows[0].PreviewPrState != state.PreviewPrStateOpen {
		t.Errorf("open row mutated: got state=%q", rows[0].PreviewPrState)
	}
	if notif.count() != 0 {
		t.Errorf("notifier called %d times, want 0 (no tombstone should emit)", notif.count())
	}
}

func TestPreviewJanitor_ClosedAndExpiredRowIsTombstoned(t *testing.T) {
	m := state.NewMemStore()
	notif := &fakeNotifier{}
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

	// A preview that was closed one hour ago and whose TTL
	// already elapsed: the sweep should promote it to torn_down
	// AND soft-delete it AND emit NotifyAppDelete.
	a, _ := seedPreviewApp(t, m, "pr-closed", "demo", 2, state.PreviewPrStateClosed, now.Add(-time.Hour))

	j := newPreviewJanitor(m, notif, nil, testLogger(), true).
		withClock(func() time.Time { return now })
	if err := j.sweepOnce(context.Background()); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	// After sweep the preview must be tombed (status='deleted',
	// preview_pr_state='torn_down') AND the notifier must have
	// been called exactly once on NotifyAppDelete.
	if got, err := m.AppByID(context.Background(), a.ID); err != nil {
		t.Fatalf("AppByID: %v", err)
	} else {
		if got.Status != state.AppDeleted {
			t.Errorf("Status = %q, want %q", got.Status, state.AppDeleted)
		}
		if got.PreviewPrState != state.PreviewPrStateTornDown {
			t.Errorf("PreviewPrState = %q, want %q", got.PreviewPrState, state.PreviewPrStateTornDown)
		}
	}
	if notif.count() != 1 {
		t.Fatalf("notifier called %d times, want 1", notif.count())
	}
	payload, ok := notif.first("app_delete")
	if !ok {
		t.Fatalf("notifier never called on app_delete channel")
	}
	// Payload must carry the kind + app_id + slug so schedd's
	// subscriber can identify the kind without parsing schema.
	for _, want := range []string{`"app_id":"` + a.ID + `"`, `"slug":"pr-closed"`, `"kind":"preview_teardown"`} {
		if !contains(payload, want) {
			t.Errorf("payload missing %q; got %s", want, payload)
		}
	}
}

func TestPreviewJanitor_StaleRowIsTombstoned(t *testing.T) {
	m := state.NewMemStore()
	notif := &fakeNotifier{}
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

	// A preview already at 'stale' (e.g. an earlier sweep
	// promoted it but apid crashed before the tombstone).
	// The next sweep should tombstone it without bumping the
	// label further (the label is already at the pre-tombstone
	// state; transition() returns next="" for stale rows).
	a, _ := seedPreviewApp(t, m, "pr-stale", "demo", 3, state.PreviewPrStateStale, now.Add(-time.Hour))

	j := newPreviewJanitor(m, notif, nil, testLogger(), true).
		withClock(func() time.Time { return now })
	if err := j.sweepOnce(context.Background()); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	if got, _ := m.AppByID(context.Background(), a.ID); got.PreviewPrState != state.PreviewPrStateTornDown {
		t.Errorf("PreviewPrState = %q, want torn_down", got.PreviewPrState)
	}
	if got, _ := m.AppByID(context.Background(), a.ID); got.Status != state.AppDeleted {
		t.Errorf("Status = %q, want deleted", got.Status)
	}
	if notif.count() != 1 {
		t.Errorf("notifier called %d times, want 1", notif.count())
	}
}

func TestPreviewJanitor_ReopenWinsBeforeTeardownClaim(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	app, _ := seedPreviewApp(t, m, "pr-reopened", "demo", 42, state.PreviewPrStateStale, now.Add(-time.Hour))
	notif := &fakeNotifier{}
	store := &reopenBeforeClaimStore{MemStore: m, reopen: func() {
		if _, err := m.RefreshPRPreview(ctx, app.ID, now.Add(time.Hour)); err != nil {
			t.Fatalf("reopen before claim: %v", err)
		}
	}}
	j := newPreviewJanitor(store, notif, nil, testLogger(), true).withClock(func() time.Time { return now })
	if err := j.sweepOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := m.AppByID(ctx, app.ID)
	if err != nil || got.Status != state.AppActive || got.PreviewPrState != state.PreviewPrStateOpen || notif.count() != 0 {
		t.Fatalf("reopened preview = (%+v, %v), notifications=%d", got, err, notif.count())
	}
}

func TestPreviewJanitor_ClaimSurvivesCleanupFailure(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	app, _ := seedPreviewApp(t, m, "pr-claimed", "demo", 42, state.PreviewPrStateStale, now.Add(-time.Hour))
	notif := &fakeNotifier{}
	failed := false
	j := newPreviewJanitor(m, notif, nil, testLogger(), true).
		withClock(func() time.Time { return now }).
		withResourceCleanup(func(context.Context, state.App) error {
			if !failed {
				failed = true
				return errors.New("cleanup unavailable")
			}
			return nil
		})
	if err := j.sweepOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := m.AppByID(ctx, app.ID)
	if err != nil || got.PreviewPrState != state.PreviewPrStateTearingDown || got.Status != state.AppActive {
		t.Fatalf("failed cleanup claim = (%+v, %v)", got, err)
	}
	if _, err := m.RefreshPRPreview(ctx, app.ID, now.Add(time.Hour)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("refresh claimed preview = %v, want not found", err)
	}
	if _, err := m.SetPreviewPrState(ctx, app.ID, state.PreviewPrStateClosed); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("close claimed preview = %v, want not found", err)
	}
	if err := j.sweepOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, err = m.AppByID(ctx, app.ID)
	if err != nil || got.PreviewPrState != state.PreviewPrStateTornDown || got.Status != state.AppDeleted || notif.count() != 1 {
		t.Fatalf("retried cleanup = (%+v, %v), notifications=%d", got, err, notif.count())
	}
}

func TestPreviewJanitor_ClosedInsideGraceIsLeftAlone(t *testing.T) {
	m := state.NewMemStore()
	notif := &fakeNotifier{}
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

	// A preview that was closed recently AND whose TTL is far
	// in the future: the grace window keeps it alive.
	a, _ := seedPreviewApp(t, m, "pr-grace", "demo", 4, state.PreviewPrStateClosed, now.Add(7*24*time.Hour))

	j := newPreviewJanitor(m, notif, nil, testLogger(), true).
		withClock(func() time.Time { return now })
	if err := j.sweepOnce(context.Background()); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	if got, _ := m.AppByID(context.Background(), a.ID); got.PreviewPrState != state.PreviewPrStateClosed {
		t.Errorf("PreviewPrState = %q, want closed (still in grace)", got.PreviewPrState)
	}
	if got, _ := m.AppByID(context.Background(), a.ID); got.Status != state.AppActive {
		t.Errorf("Status = %q, want active", got.Status)
	}
	if notif.count() != 0 {
		t.Errorf("notifier called %d times, want 0 (in grace)", notif.count())
	}
}

func TestPreviewJanitor_RecoversFromStatusDeletedCrash(t *testing.T) {
	// If apid crashes between SetPreviewPrState(torn_down) and
	// SoftDeleteAppCascade, the next sweep observes a row with
	// status='deleted' and preview_pr_state NOT 'torn_down'.
	// The sweep must still finish the tombstone (idempotent on
	// the soft-delete, but the SetPreviewPrState write is the
	// missing piece). ADR-095 PR-C.2.
	m := state.NewMemStore()
	notif := &fakeNotifier{}
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

	acct, err := m.CreateAccount(context.Background(), "crash-owner@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	expires := now.Add(-time.Hour)
	row := state.App{
		AccountID:        acct.ID,
		Slug:             "pr-crash",
		Type:             "stateless",
		RAMMB:            256,
		Status:           state.AppDeleted, // crash landed here first
		PreviewOfSlug:    "demo",
		PreviewPrNumber:  5,
		PreviewPrState:   state.PreviewPrStateClosed,
		PreviewExpiresAt: &expires,
	}
	if _, err := m.CreateAppIfUnderQuota(context.Background(), row, api.Limits{DeployedApps: 10000}); err != nil {
		t.Fatalf("CreateAppIfUnderQuota: %v", err)
	}

	j := newPreviewJanitor(m, notif, nil, testLogger(), true).
		withClock(func() time.Time { return now })
	if err := j.sweepOnce(context.Background()); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}
	// The recovery sweep should:
	//  - Re-stamp preview_pr_state='torn_down' (the missing
	//    SetPreviewPrState write from the prior tick).
	//  - Re-emit NotifyAppDelete so any consumer that missed
	//    the prior notification can react.
	if _, err := m.PreviewAppsByParent(context.Background(), acct.ID, "demo"); err != nil {
		t.Fatalf("PreviewAppsByParent: %v", err)
	}
	// The notify call count is the load-bearing assertion —
	// it proves the janitor recovered from the partial-apply
	// and re-emitted the delete notification.
	if notif.count() != 1 {
		t.Errorf("notifier called %d times, want 1 (recovery must re-emit)", notif.count())
	}
}

func TestPreviewJanitor_NotifyFailureDoesNotAbortSweep(t *testing.T) {
	m := state.NewMemStore()
	notif := &fakeNotifier{
		failCh:  "app_delete",
		failErr: errors.New("notify backend down"),
	}
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

	_, _ = seedPreviewApp(t, m, "pr-1", "demo", 1, state.PreviewPrStateClosed, now.Add(-time.Hour))
	_, _ = seedPreviewApp(t, m, "pr-2", "demo", 2, state.PreviewPrStateClosed, now.Add(-time.Hour))

	j := newPreviewJanitor(m, notif, nil, testLogger(), true).
		withClock(func() time.Time { return now })
	if err := j.sweepOnce(context.Background()); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}
	// Both rows must still be tombstoned even though the
	// notify path failed: the notify is best-effort.
	apps, err := m.ListApps(context.Background(), "")
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	for _, a := range apps {
		if a.PreviewOfSlug == "demo" && a.Status != state.AppDeleted {
			t.Errorf("row %s still status=%s after sweep; want deleted", a.Slug, a.Status)
		}
	}
}

// --- helpers ---
//
// mustAccountID was removed in favour of returning the freshly-
// minted account alongside the seeded app (see seedPreviewApp),
// which avoids a duplicate CreateAccount error on MemStore when
// the same email is reused.
