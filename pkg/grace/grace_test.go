package grace_test

// G6 grace-timer tests (spec §17 G6, ADR-021). Drives pkg/grace.RunOnce
// against an in-memory MemStore and a recording notifier + mailer so
// we can assert the side effects (delete, notify, mail) deterministically
// without spinning up Postgres or a real ticker.
//
// RunOnce is the test entry point — Run() just calls it on a ticker,
// so covering RunOnce is sufficient (ADR-021: RunOnce exported for
// this reason).

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grace"
	"github.com/onebox-faas/faas/pkg/state"
)

// recordingSender captures every (to, subject, body) for assertions.
type recordingSender struct {
	mu   sync.Mutex
	sent []sentMsg
}

type sentMsg struct {
	To      []string
	Subject string
	Body    string
}

func (r *recordingSender) Send(_ context.Context, to []string, subject, body string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, sentMsg{To: append([]string(nil), to...), Subject: subject, Body: body})
	return nil
}

// recordingNotifier captures every (channel, payload) published.
type recordingNotifier struct {
	mu       sync.Mutex
	channels []string
	payloads []string
}

func (r *recordingNotifier) publish(channel, payload string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.channels = append(r.channels, channel)
	r.payloads = append(r.payloads, payload)
}

// makeNotifier returns a Notifier function that records each publish.
func makeNotifier(rec *recordingNotifier) func(context.Context, string, string) error {
	return func(_ context.Context, channel, payload string) error {
		rec.publish(channel, payload)
		return nil
	}
}

// nowFrozen returns a clock fixed at t. RunOnce's "past grace?"
// branch reads g.p.Now(), so freezing it lets us drive the cutoff
// deterministically.
func nowFrozen(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// params assembles a fully-stubbed Params around store.
func params(store state.Store, mailer grace.Sender, notif func(context.Context, string, string) error, now func() time.Time) grace.Params {
	return grace.Params{
		Store:    store,
		Mailer:   mailer,
		Now:      now,
		Interval: time.Hour, // ignored by RunOnce
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Notif:    notif,
	}
}

// seedAccount inserts one account on the Hobby plan and returns it.
func seedAccount(t *testing.T, s *state.MemStore) state.Account {
	t.Helper()
	a, err := s.CreateAccount(context.Background(), "g6@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	return a
}

// TestRunOnce_DeletesOverdueRow — MarkAccountDeletionPending stamps
// "now" as the deletion time; RunOnce with Now set 31d in the future
// sees the row as overdue and hard-deletes it.
func TestRunOnce_DeletesOverdueRow(t *testing.T) {
	store := state.NewMemStore()
	mailer := &recordingSender{}
	notif := &recordingNotifier{}
	acct := seedAccount(t, store)
	if err := store.MarkAccountDeletionPending(context.Background(), acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}

	future := time.Now().Add(31 * 24 * time.Hour)
	g := grace.New(params(store, mailer, makeNotifier(notif), nowFrozen(future)))
	if err := g.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if _, err := store.AccountByID(context.Background(), acct.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("account not deleted: %v", err)
	}
	if len(notif.channels) != 1 || notif.channels[0] != "account_deleted" {
		t.Errorf("notifier channels = %v", notif.channels)
	}
	if len(mailer.sent) != 1 || mailer.sent[0].Subject == "" {
		t.Errorf("post-delete mail not sent: %+v", mailer.sent)
	}
}

// TestRunOnce_SkipsRowWithinGrace — MarkPending stamps now; RunOnce
// with the same clock must NOT delete (grace window still wide).
func TestRunOnce_SkipsRowWithinGrace(t *testing.T) {
	store := state.NewMemStore()
	mailer := &recordingSender{}
	notif := &recordingNotifier{}
	acct := seedAccount(t, store)
	if err := store.MarkAccountDeletionPending(context.Background(), acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}

	g := grace.New(params(store, mailer, makeNotifier(notif), time.Now))
	if err := g.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if _, err := store.AccountByID(context.Background(), acct.ID); err != nil {
		t.Errorf("account prematurely deleted: %v", err)
	}
	if len(notif.channels) != 0 {
		t.Errorf("notifier fired for in-grace row: %v", notif.channels)
	}
	if len(mailer.sent) != 0 {
		t.Errorf("mail sent for in-grace row: %+v", mailer.sent)
	}
}

// TestRunOnce_IdempotentOnSecondTick — first tick deletes, second tick
// must not crash. The deleted row is gone from ListAllAccounts so a
// second pass is a no-op; we drive this explicitly to prove the loop
// doesn't iterate a phantom row.
func TestRunOnce_IdempotentOnSecondTick(t *testing.T) {
	store := state.NewMemStore()
	mailer := &recordingSender{}
	notif := &recordingNotifier{}
	acct := seedAccount(t, store)
	if err := store.MarkAccountDeletionPending(context.Background(), acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	future := time.Now().Add(31 * 24 * time.Hour)
	g := grace.New(params(store, mailer, makeNotifier(notif), nowFrozen(future)))
	if err := g.RunOnce(context.Background()); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	if err := g.RunOnce(context.Background()); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if len(notif.channels) != 1 {
		t.Errorf("notifier fired %d times, want 1", len(notif.channels))
	}
}

// TestRunOnce_SwallowsErrNotFound — exercise the redelivered-tick
// race directly. The first call deletes the row; the second tick
// hits ErrNotFound inside the loop, which RunOnce must swallow.
func TestRunOnce_SwallowsErrNotFound(t *testing.T) {
	store := state.NewMemStore()
	acct := seedAccount(t, store)
	if err := store.MarkAccountDeletionPending(context.Background(), acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	if err := store.DeleteAccount(context.Background(), acct.ID); err != nil {
		t.Fatalf("manual delete: %v", err)
	}
	mailer := &recordingSender{}
	notif := &recordingNotifier{}
	future := time.Now().Add(31 * 24 * time.Hour)
	g := grace.New(params(store, mailer, makeNotifier(notif), nowFrozen(future)))
	if err := g.RunOnce(context.Background()); err != nil {
		t.Errorf("RunOnce returned %v, want nil (ErrNotFound must be swallowed)", err)
	}
	if len(notif.channels) != 0 {
		t.Errorf("notifier fired for missing row: %v", notif.channels)
	}
}

// TestRunOnce_OnlyDeletesPendingAccounts — an active account (never
// marked pending) must be left alone regardless of how far the clock
// is advanced. Catches a regression where the status guard is dropped.
func TestRunOnce_OnlyDeletesPendingAccounts(t *testing.T) {
	store := state.NewMemStore()
	mailer := &recordingSender{}
	notif := &recordingNotifier{}
	acct := seedAccount(t, store) // fresh, status=active
	future := time.Now().Add(31 * 24 * time.Hour)
	g := grace.New(params(store, mailer, makeNotifier(notif), nowFrozen(future)))
	if err := g.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if _, err := store.AccountByID(context.Background(), acct.ID); err != nil {
		t.Errorf("active account was deleted: %v", err)
	}
	if len(notif.channels) != 0 || len(mailer.sent) != 0 {
		t.Errorf("side effects on active account: notif=%v mail=%v", notif.channels, mailer.sent)
	}
}

// TestRunOnce_DefaultNowDoesNotDeleteFreshRow — sanity check the
// default Now path (time.Now) against a freshly-marked-pending row.
// No clock skew, no fast-forward; the row is well within grace.
func TestRunOnce_DefaultNowDoesNotDeleteFreshRow(t *testing.T) {
	store := state.NewMemStore()
	mailer := &recordingSender{}
	notif := &recordingNotifier{}
	acct := seedAccount(t, store)
	if err := store.MarkAccountDeletionPending(context.Background(), acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	g := grace.New(params(store, mailer, makeNotifier(notif), nil)) // nil Now → time.Now
	if err := g.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if _, err := store.AccountByID(context.Background(), acct.ID); err != nil {
		t.Errorf("fresh pending account prematurely deleted: %v", err)
	}
}

// TestRunOnce_RestoredAccountSurvivesTick is the regression for the
// restore→tick race (review of #46). Sequence:
//
//  1. Customer schedules deletion (MarkAccountDeletionPending).
//  2. Grace window lapses.
//  3. Customer races the timer and hits POST /v1/account/restore,
//     flipping status back to active.
//  4. RunOnce ticks, sees the row in ListAllAccounts, calls
//     DeleteAccount — which must now return ErrNotFound because the
//     conditional `WHERE status='deleted_pending'` matches zero rows.
//
// The customer's account must still exist and the timer must NOT have
// sent a "your account was deleted" email.
func TestRunOnce_RestoredAccountSurvivesTick(t *testing.T) {
	store := state.NewMemStore()
	mailer := &recordingSender{}
	notif := &recordingNotifier{}
	acct := seedAccount(t, store)
	if err := store.MarkAccountDeletionPending(context.Background(), acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	// Customer races the timer.
	if err := store.RestoreAccount(context.Background(), acct.ID); err != nil {
		t.Fatalf("RestoreAccount: %v", err)
	}

	// RunOnce with the clock past grace. The conditional DELETE inside
	// the timer's DeleteAccount call must match zero rows.
	future := time.Now().Add(31 * 24 * time.Hour)
	g := grace.New(params(store, mailer, makeNotifier(notif), nowFrozen(future)))
	if err := g.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Account must still exist (status=active).
	fresh, err := store.AccountByID(context.Background(), acct.ID)
	if err != nil {
		t.Fatalf("AccountByID after restore+race: %v, want nil "+
			"(the race must NOT delete a restored account)", err)
	}
	if fresh.Status != state.AccountActive {
		t.Errorf("status = %q, want active", fresh.Status)
	}
	// No "deleted" side effects.
	if len(notif.channels) != 0 {
		t.Errorf("notifier fired for restored row: %v", notif.channels)
	}
	if len(mailer.sent) != 0 {
		t.Errorf("post-delete mail sent for restored row: %+v", mailer.sent)
	}
}
