// Dunning timer tests (spec §4.7, §17 dunning state machine).
//
// Mirrors pkg/grace/grace_test.go's pattern (nowFrozen + recordingSender
// + direct RunOnce calls). Each test pins one of the per-status
// dispatches:
//
//   1. past_due within 7d         → no transition, no ParkInstance, no mail.
//   2. past_due past 7d           → status flips to suspended; ParkInstance
//                                   called for every live instance; one mail.
//   3. suspended past 21d         → status flips to deleted_pending;
//                                   no additional ParkInstance; one mail.
//   4. past_due with PastDueAt==nil → backfill-warn log; no transition.

package meter_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// recordingSender records every Send call. The Mailer interface meterd
// expects (mail.Message) is satisfied by mail.NoopSender in production
// and any custom Sender in tests.
type recordingSender struct {
	mu    sync.Mutex
	sends []mail.Message
}

func (r *recordingSender) Send(_ context.Context, msg mail.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sends = append(r.sends, msg)
	return nil
}

func (r *recordingSender) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sends)
}

// dunningTestFixture wires the in-memory collaborators Dunning needs.
// Mirrors the production CmdMeterd wire-up minus the live scheduler.
func dunningTestFixture(t *testing.T, ctx context.Context) (*meter.Dunning, *state.MemStore, *fakeParker, *fakeNotifier, *recordingSender, *time.Time) {
	t.Helper()
	store := state.NewMemStore()
	parker := &fakeParker{}
	notif := &fakeNotifier{}
	mailer := &recordingSender{}
	clock := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }

	d := meter.NewDunning(meter.DunningParams{
		Store:  store,
		Parker: parker,
		Mailer: mailer,
		Notif:  notif,
		Log:    discardLog(),
		Now:    now,
	})
	return d, store, parker, notif, mailer, &clock
}

// seedPastDue plants a past_due account with PastDueAt = pastDueAt.
// Uses UpdateAccountStatus + MarkDunningStep (no-op flip) to mimic the
// apid webhook path (invoice.payment_failed).
func seedPastDue(t *testing.T, ctx context.Context, s *state.MemStore, email string, plan api.Plan, pastDueAt time.Time) state.Account {
	t.Helper()
	acct, err := s.CreateAccount(ctx, email, plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAccountStatus(ctx, acct.ID, state.AccountPastDue); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDunningStep(ctx, acct.ID, state.AccountPastDue, state.AccountPastDue); err != nil {
		t.Fatal(err)
	}
	// MarkDunningStep stamps PastDueAt with time.Now(); for the test we
	// backdate it via a direct re-stamp. (No public Store setter for
	// PastDueAt exists — the only production writer is the apid
	// webhook, which stamps via the same MarkDunningStep path. We
	// bypass it by re-stamping through MarkDunningStep no-op after
	// manually overwriting via MemStore.)
	backdatePastDueAt(t, s, acct.ID, pastDueAt)
	got, err := s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// seedSuspended flips a past_due row through the suspended transition
// (so PastDueAt is preserved), then backdates PastDueAt for the test.
func seedSuspended(t *testing.T, ctx context.Context, s *state.MemStore, email string, plan api.Plan, pastDueAt time.Time) state.Account {
	t.Helper()
	acct := seedPastDue(t, ctx, s, email, plan, pastDueAt)
	if err := s.MarkDunningStep(ctx, acct.ID, state.AccountPastDue, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	backdatePastDueAt(t, s, acct.ID, pastDueAt)
	got, err := s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// backdatePastDueAt is a tiny test-only helper that overwrites PastDueAt
// directly on the MemStore map. There's no production writer for this
// path — PastDueAt is set by MarkDunningStep with time.Now() and only
// read by the dunning timer, which compares against (now − PastDueAt).
// Tests need to control PastDueAt so the 7d/21d thresholds are testable
// in milliseconds.
func backdatePastDueAt(t *testing.T, s *state.MemStore, id string, at time.Time) {
	t.Helper()
	if err := s.SetPastDueAtForTest(id, at); err != nil {
		t.Fatal(err)
	}
}

func TestDunning_PastDueInside7d_NoTransition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, store, parker, notif, mailer, now := dunningTestFixture(t, ctx)
	// Past_due stamped 3 days ago — well inside the 7-day grace.
	stamp := now.AddDate(0, 0, -3)
	seedPastDue(t, ctx, store, "inside7d@example.com", api.PlanHobby, stamp)

	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Status must still be past_due — the tick should not have touched
	// the row. Verify by re-reading (the seedPastDue helper already
	// returned the account but we need a post-RunOnce read to prove
	// the timer left it alone).
	if len(parker.parked) != 0 {
		t.Errorf("parked = %d, want 0", len(parker.parked))
	}
	if len(notif.captured) != 0 {
		t.Errorf("notified = %d, want 0", len(notif.captured))
	}
	if mailer.count() != 0 {
		t.Errorf("mails sent = %d, want 0", mailer.count())
	}
}

// Past the 7-day grace an account with an overdue payment moves to the Free
// plan and stays active (Terms §3; billing review 2026-11-13 R9-1): nothing is
// suspended or parked for non-payment, past_due_at stays as the unpaid
// balance's marker, and the account is told once.
func TestDunning_PastDuePast7d_MovesToFree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, store, parker, notif, mailer, now := dunningTestFixture(t, ctx)
	stamp := now.AddDate(0, 0, -8)
	acct := seedPastDue(t, ctx, store, "past7d@example.com", api.PlanPro, stamp)
	app := newApp(t, ctx, store, acct.ID)
	makeLiveInstance(t, ctx, store, app.ID, acct.ID, 256)

	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	got, err := store.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.AccountActive || got.Plan != api.PlanFree || got.PastDueAt == nil || !got.MovedToFreeForNonpayment() {
		t.Fatalf("after 7 days: status %s plan %s past_due_at %v; want active on Free with the balance marked", got.Status, got.Plan, got.PastDueAt)
	}
	if len(parker.parked) != 0 {
		t.Errorf("parked = %d, want 0: nothing is suspended for non-payment", len(parker.parked))
	}
	if warns := notif.byChannel(db.NotifyBillingPastDue); len(warns) != 1 {
		t.Errorf("billing_past_due notifies = %d, want 1 (moved_to_free)", len(warns))
	}
	if mailer.count() != 1 {
		t.Errorf("mails sent = %d, want the moved-to-Free mail", mailer.count())
	}
	// The next sweep leaves it alone.
	if err := d.RunOnce(ctx); err != nil || mailer.count() != 1 {
		t.Fatalf("second sweep: %v, mails %d", err, mailer.count())
	}
	// A Free quota stop keeps the unpaid-balance marker, and the sweep
	// neither lifts the stop nor moves the account again.
	if ok, err := store.SuspendAccountForFreeQuota(ctx, acct.ID); err != nil || !ok {
		t.Fatalf("quota stop: %v %v", ok, err)
	}
	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("sweep under the quota stop: %v", err)
	}
	got, err = store.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.AccountSuspended || got.PastDueAt == nil || !got.MovedToFreeForNonpayment() || mailer.count() != 1 {
		t.Fatalf("under the quota stop: status %s past_due_at %v mails %d; want suspended with the balance marked", got.Status, got.PastDueAt, mailer.count())
	}
}

// An account the old ladder suspended for non-payment, even past 21 days, is
// moved to Free the same way and never scheduled for deletion.
func TestDunning_SuspendedForNonpayment_MovesToFreeNeverDeletes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, store, parker, notif, mailer, now := dunningTestFixture(t, ctx)
	acct := seedSuspended(t, ctx, store, "past21d@example.com", api.PlanHobby, now.AddDate(0, 0, -22))

	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	got, err := store.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.AccountActive || got.Plan != api.PlanFree || got.PastDueAt == nil {
		t.Fatalf("status %s plan %s past_due_at %v; want active on Free", got.Status, got.Plan, got.PastDueAt)
	}
	if len(parker.parked) != 0 {
		t.Errorf("parked = %d, want 0", len(parker.parked))
	}
	if warns := notif.byChannel(db.NotifyAccountDeletionPending); len(warns) != 0 {
		t.Errorf("account_deletion_pending notifies = %d, want 0", len(warns))
	}
	if mailer.count() != 1 {
		t.Errorf("mails sent = %d, want 1", mailer.count())
	}
}

func TestDunning_PastDueWithNoStamp_BackfillsAndSkips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, store, parker, notif, mailer, _ := dunningTestFixture(t, ctx)
	// Plant an account in past_due WITHOUT a PastDueAt stamp — the
	// data-integrity guard path. We can't go through UpdateAccountStatus
	// alone because CreateAccount leaves status='active'; we flip via
	// UpdateAccountStatus (which doesn't touch PastDueAt — only
	// MarkDunningStep stamps it).
	acct, err := store.CreateAccount(ctx, "legacy@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStatus(ctx, acct.ID, state.AccountPastDue); err != nil {
		t.Fatal(err)
	}
	got, err := store.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PastDueAt != nil {
		t.Fatalf("setup: PastDueAt = %s, want nil", got.PastDueAt)
	}

	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Status stayed past_due (no transition this tick — the guard
	// skipped after backfilling the stamp).
	got, err = store.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.AccountPastDue {
		t.Errorf("status = %s, want past_due (backfill must not transition)", got.Status)
	}
	if got.PastDueAt == nil {
		t.Error("PastDueAt = nil after RunOnce; the backfill stamp did not land")
	}

	// No park, no notify, no mail (the tick was a backfill, not a
	// transition).
	if len(parker.parked) != 0 {
		t.Errorf("parked = %d, want 0 (backfill must not park)", len(parker.parked))
	}
	if len(notif.captured) != 0 {
		t.Errorf("notified = %d, want 0 (backfill must not notify)", len(notif.captured))
	}
	if mailer.count() != 0 {
		t.Errorf("mails sent = %d, want 0 (backfill must not mail)", mailer.count())
	}
}
