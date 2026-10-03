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
	"strings"
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

// recordingAuditor captures every Emit so the test can assert the
// audit row was emitted (issue #755 / PR-5.5). Minimal interface
// match: grace.Auditor is a 4-method interface and this satisfies it
// without pulling in the apid auditor concrete type.
type recordingAuditor struct {
	mu         sync.Mutex
	kinds      []string
	accountIDs []string
	data       []map[string]any
}

func (r *recordingAuditor) Emit(_ context.Context, kind string, accountID *string, data map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.kinds = append(r.kinds, kind)
	acctID := ""
	if accountID != nil {
		acctID = *accountID
	}
	r.accountIDs = append(r.accountIDs, acctID)
	r.data = append(r.data, data)
}

// runOnceInterval is the Interval passed to params() in tests that
// drive RunOnce directly. RunOnce never reads the ticker Interval,
// so any positive value works; one hour is the same value Run would
// use under load and keeps a future reader from wondering "why 1h?".
const runOnceInterval = time.Hour

// Sentinel errors used by the error-injection tests. The RunOnce
// callers assert errors.Is so the assertion survives a string rename;
// the Run ticker test asserts against the slog-rendered message text,
// which is a different contract (the operator-visible log line).
var (
	errListAllBoom    = errors.New("boom")
	errGdprAuditBoom  = errors.New("audit db down")
	errNotifierBoom   = errors.New("pg_notify down")
	errMailerBoom     = errors.New("postmark 503")
	errHardDeleteBoom = errors.New("fk violation")
)

// nowFrozen returns a clock fixed at t. RunOnce's "past grace?"
// branch reads Params.Now, so freezing it lets us drive the cutoff
// deterministically.
func nowFrozen(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// params assembles a fully-stubbed Params around store. interval is
// what the constructor's `Interval <= 0` default branch overrides; the
// tests pass a positive value so the constructed Grace uses it as-is.
// now is the clock the sweep reads; RunOnce tests pin it to a frozen
// timestamp so the cutoff is deterministic.
func params(store state.Store, mailer grace.Sender, notif func(context.Context, string, string) error, now func() time.Time, interval time.Duration) grace.Params {
	return grace.Params{
		Store:    store,
		Mailer:   mailer,
		Now:      now,
		Interval: interval,
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Notif:    notif,
	}
}

// seedAccount inserts one account on the Hobby plan and returns it.
func seedAccount(t *testing.T, s state.Store) state.Account {
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
	g := grace.New(params(store, mailer, makeNotifier(notif), nowFrozen(future), runOnceInterval))
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

	g := grace.New(params(store, mailer, makeNotifier(notif), time.Now, runOnceInterval))
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
	g := grace.New(params(store, mailer, makeNotifier(notif), nowFrozen(future), runOnceInterval))
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
	g := grace.New(params(store, mailer, makeNotifier(notif), nowFrozen(future), runOnceInterval))
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
	g := grace.New(params(store, mailer, makeNotifier(notif), nowFrozen(future), runOnceInterval))
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
	g := grace.New(params(store, mailer, makeNotifier(notif), nil, runOnceInterval)) // nil Now → time.Now
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
	g := grace.New(params(store, mailer, makeNotifier(notif), nowFrozen(future), runOnceInterval))
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

// TestNew_PanicOnNilStore — the only constructor invariant is that a
// Store is provided. Every other Params field is optional and gets a
// default. A panic is the loudest signal that the caller wired up
// the timer without a Store, which is the one configuration that
// would silently no-op every tick otherwise.
func TestNew_PanicOnNilStore(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("grace.New(grace.Params{}) did not panic")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value type = %T, want string", r)
		}
		if msg != "grace: Params.Store is required" {
			t.Errorf("panic message = %q, want %q",
				msg, "grace: Params.Store is required")
		}
	}()
	_ = grace.New(grace.Params{})
}

// TestNew_DefaultsLogMailerNotifAndNoopSend — the constructor must
// silently fall back when Mailer/Log/Notif are nil: Mailer → the
// internal noopSender, Log → slog.Default, Notif → a closure that
// swallows the publish. Building a Grace with only Store populated
// and driving RunOnce exercises every default branch in one test and
// also covers the noopSender.Send body (which is the function-level
// method at grace.go:90 — unreachable without Mailer being nil).
func TestNew_DefaultsLogMailerNotifAndNoopSend(t *testing.T) {
	store := state.NewMemStore()
	acct := seedAccount(t, store)
	if err := store.MarkAccountDeletionPending(context.Background(), acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}

	// Only Store, Now, and Interval populated. Interval is positive so
	// the constructor's `Interval <= 0` default branch does not fire
	// (that's the branch we intentionally leave at 0% — see
	// grace-interval-default-branch-decision memory). Log, Mailer, and
	// Notif are nil → defaults fire (slog.Default, noopSender,
	// swallow-and-return-nil closure).
	future := time.Now().Add(31 * 24 * time.Hour)
	g := grace.New(grace.Params{
		Store:    store,
		Now:      nowFrozen(future),
		Interval: runOnceInterval,
	})

	if err := g.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce with default Sender/Notifier: %v", err)
	}
	if _, err := store.AccountByID(context.Background(), acct.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("account not deleted under defaults: %v", err)
	}
}

// TestRunOnce_ListAllAccountsError — RunOnce must surface a transient
// store outage so the caller (apid's main goroutine) can log + decide
// whether to back off. This is the only branch that returns a
// non-nil error from RunOnce; every other failure path logs Warn and
// keeps walking the loop. The error returned here is propagated
// upward unchanged.
func TestRunOnce_ListAllAccountsError(t *testing.T) {
	store := &stubStore{
		Store:      state.NewMemStore(),
		listAllErr: errListAllBoom,
	}
	mailer := &recordingSender{}
	notif := &recordingNotifier{}
	g := grace.New(params(store, mailer, makeNotifier(notif), time.Now, runOnceInterval))
	err := g.RunOnce(context.Background())
	if !errors.Is(err, errListAllBoom) {
		t.Errorf("RunOnce returned %v, want errListAllBoom", err)
	}
	if len(notif.channels) != 0 {
		t.Errorf("notifier fired despite ListAllAccounts error: %v", notif.channels)
	}
	if len(mailer.sent) != 0 {
		t.Errorf("mailer fired despite ListAllAccounts error: %+v", mailer.sent)
	}
}

// TestRunOnce_CompleteGdprRequestError — happy-path delete + the
// audit-stamp step fails with a non-ErrNotFound error. RunOnce must
// log Warn and keep walking; the row is still hard-deleted and the
// notify + mail side effects still fire (they're independent of the
// GDPR ledger stamp).
func TestRunOnce_CompleteGdprRequestError(t *testing.T) {
	store := &stubStore{
		Store:           state.NewMemStore(),
		completeGdprErr: errGdprAuditBoom,
	}
	mailer := &recordingSender{}
	notif := &recordingNotifier{}
	acct := seedAccount(t, store)
	if err := store.MarkAccountDeletionPending(context.Background(), acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	future := time.Now().Add(31 * 24 * time.Hour)
	g := grace.New(params(store, mailer, makeNotifier(notif), nowFrozen(future), runOnceInterval))
	if err := g.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce returned %v, want nil (CompleteGdprRequest error must be swallowed)", err)
	}
	if _, err := store.AccountByID(context.Background(), acct.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("account not deleted: %v", err)
	}
	if len(notif.channels) != 1 {
		t.Errorf("notifier fired %d times, want 1", len(notif.channels))
	}
	if len(mailer.sent) != 1 {
		t.Errorf("mailer fired %d times, want 1", len(mailer.sent))
	}
}

// TestRunOnce_NotifierError — pg_notify can fail (transient
// connection error). The contract is log-and-continue: the row is
// hard-deleted and the mail still goes out. We assert the call ran,
// then that RunOnce didn't bubble the notify error up.
func TestRunOnce_NotifierError(t *testing.T) {
	store := state.NewMemStore()
	mailer := &recordingSender{}
	acct := seedAccount(t, store)
	if err := store.MarkAccountDeletionPending(context.Background(), acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	future := time.Now().Add(31 * 24 * time.Hour)
	failing := func(_ context.Context, _, _ string) error {
		return errNotifierBoom
	}
	g := grace.New(params(store, mailer, failing, nowFrozen(future), runOnceInterval))
	if err := g.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce returned %v, want nil (Notifier error must be swallowed)", err)
	}
	if _, err := store.AccountByID(context.Background(), acct.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("account not deleted: %v", err)
	}
	if len(mailer.sent) != 1 {
		t.Errorf("mailer fired %d times, want 1 (mail must still go out)", len(mailer.sent))
	}
}

// TestRunOnce_MailerError — Postmark/Resend can fail. Same contract:
// log-and-continue. Account is still gone from the store; no notifier
// or mailer side effects raise out of RunOnce.
func TestRunOnce_MailerError(t *testing.T) {
	store := state.NewMemStore()
	mailer := &failingSender{err: errMailerBoom}
	notif := &recordingNotifier{}
	acct := seedAccount(t, store)
	if err := store.MarkAccountDeletionPending(context.Background(), acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	future := time.Now().Add(31 * 24 * time.Hour)
	g := grace.New(params(store, mailer, makeNotifier(notif), nowFrozen(future), runOnceInterval))
	if err := g.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce returned %v, want nil (Mailer error must be swallowed)", err)
	}
	if _, err := store.AccountByID(context.Background(), acct.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("account not deleted: %v", err)
	}
	if len(notif.channels) != 1 {
		t.Errorf("notifier fired %d times, want 1", len(notif.channels))
	}
}

// TestRun_CancelsCleanly — Run's select arms are ctx.Done() (the
// "stop signal" branch) and t.C (the "tick" branch). With a long
// Interval and an immediate cancel, Run must observe ctx.Done()
// before any tick fires and return nil. This is the contract apid's
// main goroutine depends on when SIGINT lands.
func TestRun_CancelsCleanly(t *testing.T) {
	store := state.NewMemStore()
	mailer := &recordingSender{}
	notif := &recordingNotifier{}
	// A one-hour Interval means no tick fires during the test; we're
	// proving the ctx.Done() branch. Using the constant fails open
	// without a Params seam.
	g := grace.New(params(store, mailer, makeNotifier(notif), time.Now, time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Run(ctx) }()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil on ctx cancel", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return within 1s of ctx cancel")
	}
}

// TestRun_TickInvokesRunOnce — with a fast ticker and a stub Store
// that errors on ListAllAccounts, the ticker arm must fire
// RunOnce. RunOnce returns an error; Run logs Warn and keeps looping
// until ctx cancel. We assert (a) Run returns nil on cancel and (b)
// the error was logged via the configured Log sink so we know the
// ticker arm really fired.
func TestRun_TickInvokesRunOnce(t *testing.T) {
	store := &stubStore{
		Store:      state.NewMemStore(),
		listAllErr: errListAllBoom,
	}
	logBuf := &safeBuffer{}
	mailer := &recordingSender{}
	notif := &recordingNotifier{}
	g := grace.New(grace.Params{
		Store:    store,
		Mailer:   mailer,
		Interval: time.Millisecond,
		Now:      time.Now,
		Log:      slog.New(slog.NewTextHandler(logBuf, nil)),
		Notif:    makeNotifier(notif),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Run(ctx) }()

	// Wait long enough for ≥1 tick (interval is 1 ms; 100 ms is generous
	// on busy CI where the ticker goroutine can be starved briefly — see
	// iam-2-testmfarecover-concurrentburn-flake memory for prior CI flakes
	// with a 50ms wait window).
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil on ctx cancel", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return within 1s of ctx cancel")
	}
	if !strings.Contains(logBuf.String(), "boom") {
		t.Errorf("Run did not log the RunOnce error; log = %q", logBuf.String())
	}
}

// TestRunOnce_SkipsPendingRowWithNilDeletionRequestedAt — RunOnce's
// defensive guard against a row whose Status is deleted_pending but
// whose DeletionRequestedAt is nil (a corner case that can arise from
// an interrupted MarkPending transaction). RunOnce must skip the row
// rather than call DeleteAccount with a "zero-time" cutoff, because
// every real timestamp is past zero-time and the row would be
// deleted on the next tick. We drive this by feeding a hand-crafted
// Account slice into the stub Store's ListAllAccounts override.
func TestRunOnce_SkipsPendingRowWithNilDeletionRequestedAt(t *testing.T) {
	store := &stubStore{
		Store: state.NewMemStore(),
		listAllAccounts: []state.Account{
			{ID: "ghost", Status: state.AccountDeletedPending,
				DeletionRequestedAt: nil, Email: "ghost@example.com"},
		},
	}
	mailer := &recordingSender{}
	notif := &recordingNotifier{}
	g := grace.New(params(store, mailer, makeNotifier(notif), time.Now, runOnceInterval))
	if err := g.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce returned %v, want nil", err)
	}
	if len(notif.channels) != 0 {
		t.Errorf("notifier fired for nil-DeletionRequestedAt row: %v", notif.channels)
	}
	if len(mailer.sent) != 0 {
		t.Errorf("mailer fired for nil-DeletionRequestedAt row: %+v", mailer.sent)
	}
}

// TestRunOnce_LogsHardDeleteError — when DeleteAccount returns a
// non-ErrNotFound error (e.g. a transient FK constraint violation),
// RunOnce must log Warn, swallow the error, and keep walking the
// loop. The row stays in the store; the next tick will retry. We
// assert the row is still present and no side-effects (notify, mail,
// audit-stamp) fired.
func TestRunOnce_LogsHardDeleteError(t *testing.T) {
	store := &stubStore{
		Store:            state.NewMemStore(),
		deleteAccountErr: errHardDeleteBoom,
	}
	mailer := &recordingSender{}
	notif := &recordingNotifier{}
	acct := seedAccount(t, store)
	if err := store.MarkAccountDeletionPending(context.Background(), acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	logBuf := &safeBuffer{}
	future := time.Now().Add(31 * 24 * time.Hour)
	g := grace.New(grace.Params{
		Store:    store,
		Mailer:   mailer,
		Interval: time.Hour,
		Now:      nowFrozen(future),
		Log:      slog.New(slog.NewTextHandler(logBuf, nil)),
		Notif:    makeNotifier(notif),
	})
	if err := g.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce returned %v, want nil (hard-delete error must be swallowed)", err)
	}
	if _, err := store.AccountByID(context.Background(), acct.ID); err != nil {
		t.Errorf("account was hard-deleted despite the error: %v", err)
	}
	if len(notif.channels) != 0 {
		t.Errorf("notifier fired despite hard-delete error: %v", notif.channels)
	}
	if len(mailer.sent) != 0 {
		t.Errorf("mailer fired despite hard-delete error: %+v", mailer.sent)
	}
	if !strings.Contains(logBuf.String(), "fk violation") {
		t.Errorf("RunOnce did not log the hard-delete error; log = %q", logBuf.String())
	}
}

// --- stubs and helpers below this line -----------------------------------

// failingSender is a Sender that records nothing and returns the
// configured error from every Send call. Lets us drive the
// "post-delete mail failed" log branch without standing up a real
// mail client.
type failingSender struct {
	err error
}

func (f *failingSender) Send(_ context.Context, _ []string, _, _ string) error {
	return f.err
}

// stubStore wraps a real *state.MemStore and lets a test inject
// errors on the small subset of methods the grace sweep actually
// calls. We embed the concrete store so every other method on the
// state.Store interface is satisfied for free; only the overrides
// declared here change behaviour. The wrap is the canonical pattern
// for testing grace-side error paths without standing up Postgres.
type stubStore struct {
	state.Store
	completeGdprErr  error
	deleteAccountErr error
	listAllErr       error
	listAllAccounts  []state.Account
}

func (s *stubStore) ListAllAccounts(ctx context.Context) ([]state.Account, error) {
	if s.listAllErr != nil {
		return nil, s.listAllErr
	}
	if s.listAllAccounts != nil {
		return s.listAllAccounts, nil
	}
	return s.Store.ListAllAccounts(ctx)
}

func (s *stubStore) CompleteGdprRequest(ctx context.Context, accountID, action string) error {
	if s.completeGdprErr != nil {
		return s.completeGdprErr
	}
	return s.Store.CompleteGdprRequest(ctx, accountID, action)
}

func (s *stubStore) DeleteAccount(ctx context.Context, id string) error {
	if s.deleteAccountErr != nil {
		return s.deleteAccountErr
	}
	return s.Store.DeleteAccount(ctx, id)
}

// safeBuffer is a tiny mutex-guarded bytes.Buffer. Used by
// TestRun_TickInvokesRunOnce so the slog handler writing to it and
// the test goroutine reading it can run under -race without
// tripping the race detector (a bare *bytes.Buffer races here —
// this is the same pattern documented in pkg/e2etest).
type safeBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestRunOnce_EmitsAccountDeletedAudit (issue #755 / PR-5.5).
// After DeleteAccount fires, the sweep must Emit one
// "account.deleted" audit row carrying actor=source=grace-sweep
// and the deleted account's email. The audit row is what the
// audit_log table backfills at DeleteAccount-time so the regulator
// can re-derive the post-deletion state.
func TestRunOnce_EmitsAccountDeletedAudit(t *testing.T) {
	store := state.NewMemStore()
	mailer := &recordingSender{}
	notif := &recordingNotifier{}
	audit := &recordingAuditor{}
	acct := seedAccount(t, store)
	if err := store.MarkAccountDeletionPending(context.Background(), acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}

	future := time.Now().Add(31 * 24 * time.Hour)
	g := grace.New(grace.Params{
		Store:    store,
		Mailer:   mailer,
		Now:      nowFrozen(future),
		Interval: runOnceInterval,
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Notif:    makeNotifier(notif),
		Audit:    audit,
	})
	if err := g.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if len(audit.kinds) != 1 {
		t.Fatalf("audit emissions = %d, want 1: kinds=%v", len(audit.kinds), audit.kinds)
	}
	if audit.kinds[0] != "account.deleted" {
		t.Errorf("audit kind = %q, want \"account.deleted\"", audit.kinds[0])
	}
	if audit.accountIDs[0] != acct.ID {
		t.Errorf("audit account_id = %q, want %q", audit.accountIDs[0], acct.ID)
	}
	if got := audit.data[0]["actor"]; got != "grace-sweep" {
		t.Errorf("audit data.actor = %v, want \"grace-sweep\"", got)
	}
	if got := audit.data[0]["source"]; got != "grace-sweep" {
		t.Errorf("audit data.source = %v, want \"grace-sweep\"", got)
	}
	if got := audit.data[0]["email"]; got != acct.Email {
		t.Errorf("audit data.email = %v, want %q", got, acct.Email)
	}
}

// TestRunOnce_NoAuditEmittedWhenNothingDeleted (issue #755 / PR-5.5).
// A no-op sweep (no rows past grace) must NOT emit account.deleted —
// the audit log is for actual deletions, not for "sweep ran and saw
// nothing". This guards against a regression where the Emit was
// hoisted out of the post-DeleteAccount block.
func TestRunOnce_NoAuditEmittedWhenNothingDeleted(t *testing.T) {
	store := state.NewMemStore()
	mailer := &recordingSender{}
	notif := &recordingNotifier{}
	audit := &recordingAuditor{}
	// Account exists in 'active' state — never marked for deletion.
	_ = seedAccount(t, store)

	g := grace.New(grace.Params{
		Store:    store,
		Mailer:   mailer,
		Now:      time.Now,
		Interval: runOnceInterval,
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Notif:    makeNotifier(notif),
		Audit:    audit,
	})
	if err := g.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if len(audit.kinds) != 0 {
		t.Errorf("audit emissions on no-op sweep = %v, want none", audit.kinds)
	}
}

// TestRunOnce_PurgesDunningDeletion — the dunning timer's final step moved
// an unpaid account to deleted_pending without deletion_requested_at, so
// this sweep (which purges on that anchor) never deleted it, although the
// customer was emailed that deletion follows in 30 days.
func TestRunOnce_PurgesDunningDeletion(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	acct := seedAccount(t, store)
	for _, step := range [][2]state.AccountStatus{
		{state.AccountActive, state.AccountPastDue},
		{state.AccountPastDue, state.AccountSuspended},
		{state.AccountSuspended, state.AccountDeletedPending},
	} {
		if err := store.MarkDunningStep(ctx, acct.ID, step[0], step[1]); err != nil {
			t.Fatalf("MarkDunningStep(%s→%s): %v", step[0], step[1], err)
		}
	}
	g := grace.New(params(store, &recordingSender{}, makeNotifier(&recordingNotifier{}), nowFrozen(time.Now().Add(31*24*time.Hour)), runOnceInterval))
	if err := g.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if _, err := store.AccountByID(ctx, acct.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("dunning-deleted account survived the grace window: %v", err)
	}
}
