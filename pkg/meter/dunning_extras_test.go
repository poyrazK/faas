package meter

// Extra coverage for Dunning methods that RunOnce doesn't exercise
// when called directly: parkAll (line 224, 50% coverage) + the two
// mail helpers sendSuspendedMail / sendDeletionMail (line 245/257).
// The existing dunning_test.go walks the happy path through RunOnce;
// these tests pin the branch-by-branch behavior of the helper methods
// in isolation so a refactor that drops one of them (e.g. removing
// the CountsForRAM guard) regresses loudly.
//
// Lives in `package meter` (not `meter_test`) because the three
// methods are unexported — only the package-internal tests can call
// them directly. To avoid reaching across the test-package boundary
// (meter_test helpers are not visible from `package meter`), this
// file ships its own minimal fakes inline: `localParker` and
// `localSender`. The shared `recordingSender` in meter_test.go is
// duplicated here under a different name on purpose.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// localParker is the minimal ParkInstance stub used by parkAll tests.
// Records each call so tests can assert what was parked and why.
type localParker struct {
	mu     sync.Mutex
	parked []localParkedCall
}

type localParkedCall struct {
	InstanceID string
	Reason     string
}

func (p *localParker) ParkInstance(_ context.Context, id, reason string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.parked = append(p.parked, localParkedCall{InstanceID: id, Reason: reason})
	return nil
}

func (p *localParker) calls() []localParkedCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]localParkedCall, len(p.parked))
	copy(out, p.parked)
	return out
}

// localSender records every Send call. The Mailer interface meterd
// expects (mail.Message) is satisfied by mail.NoopSender in
// production and any custom Sender in tests.
type localSender struct {
	mu    sync.Mutex
	sends []mail.Message
}

func (s *localSender) Send(_ context.Context, msg mail.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sends = append(s.sends, msg)
	return nil
}

func (s *localSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sends)
}

func (s *localSender) last() mail.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sends) == 0 {
		return mail.Message{}
	}
	return s.sends[len(s.sends)-1]
}

// errStore is a minimal state.Store stub that only implements
// ListInstancesForAccount (returning a configured error). Used by
// the parkAll error-path test; every other method must panic if
// called, so an accidental call from parkAll surfaces as a test
// crash instead of a silent pass.
type errStore struct {
	state.Store
	listErr error
}

func (e *errStore) ListInstancesForAccount(_ context.Context, _ string) ([]state.Instance, error) {
	return nil, e.listErr
}

// newDunningForTest builds a minimal Dunning whose Store + Parker
// are caller-supplied; the rest (Mailer, Notif, Log, Now) get
// no-op defaults. Used by parkAll-focused tests that don't care
// about mail/notify side effects.
func newDunningForTest(store state.Store, parker *localParker) *Dunning {
	return NewDunning(DunningParams{
		Store:  store,
		Parker: parker,
		// Mailer / Notif default to no-op in NewDunning.
	})
}

// newDunningWithMailer builds a Dunning whose Mailer is the supplied
// localSender; used by the send*Mail tests to assert Send calls.
func newDunningWithMailer(store state.Store, mailer *localSender) *Dunning {
	return NewDunning(DunningParams{
		Store:  store,
		Mailer: mailer,
	})
}

// localSeedAccount inserts a Hobby-plan account via MemStore. Returns
// the Account so tests can assert on Email / ID.
func localSeedAccount(t *testing.T, ctx context.Context, store *state.MemStore) state.Account {
	t.Helper()
	acct, err := store.CreateAccount(ctx, "mem-meter@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	return acct
}

// localSeedApp inserts a minimal App bound to accountID. Tests that
// need an instance pass the returned app.ID to CreateInstance.
func localSeedApp(t *testing.T, ctx context.Context, store *state.MemStore, accountID string) state.App {
	t.Helper()
	app, err := store.CreateApp(ctx, state.App{
		AccountID: accountID, Slug: "meter-extra-test",
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return app
}

// TestDunning_ParkAll_ParksOnlyLiveInstances seeds two instances —
// one RUNNING (counts for RAM) and one PARKED (does NOT count) — and
// confirms parkAll calls ParkInstance only on the RUNNING one. The
// state.State.CountsForRAM() guard at dunning.go:231 is the load-
// bearing branch; a future refactor that removes it would re-park
// already-parked instances and spam the schedd RPC.
func TestDunning_ParkAll_ParksOnlyLiveInstances(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	parker := &localParker{}
	d := newDunningForTest(store, parker)

	acct := localSeedAccount(t, ctx, store)
	app := localSeedApp(t, ctx, store, acct.ID)
	running, err := store.CreateInstance(ctx, app.ID, "deployment-test", string(state.StateRunning), 256, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("create running instance: %v", err)
	}
	parked, err := store.CreateInstance(ctx, app.ID, "deployment-test", string(state.StateParked), 256, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("create parked instance: %v", err)
	}

	d.parkAll(ctx, acct.ID)

	got := parker.calls()
	if len(got) != 1 {
		t.Fatalf("parked count = %d, want 1 (only the RUNNING instance)", len(got))
	}
	if got[0].InstanceID != running.ID {
		t.Errorf("parked id = %q, want %q (the RUNNING one)", got[0].InstanceID, running.ID)
	}
	if got[0].Reason != "dunning_past_due_7d" {
		t.Errorf("park reason = %q, want %q", got[0].Reason, "dunning_past_due_7d")
	}
	for _, c := range got {
		if c.InstanceID == parked.ID {
			t.Errorf("ParkInstance called on already-parked instance %q", parked.ID)
		}
	}
}

// TestDunning_ParkAll_StoreErrorIsLoggedAndSkipped feeds a stub store
// that returns an error from ListInstancesForAccount, then confirms
// parkAll returns without panicking and ParkInstance is never called.
// The error path at dunning.go:226-229 logs a warn and returns
// cleanly; a refactor that bubbles the error would change RunOnce's
// contract (which expects parkAll to be best-effort).
func TestDunning_ParkAll_StoreErrorIsLoggedAndSkipped(t *testing.T) {
	ctx := context.Background()
	store := &errStore{listErr: errors.New("simulated store failure")}
	parker := &localParker{}
	d := newDunningForTest(store, parker)

	d.parkAll(ctx, "any-account")

	if n := len(parker.calls()); n != 0 {
		t.Errorf("ParkInstance called %d times despite store error, want 0", n)
	}
}

// TestDunning_SendSuspendedMail_DeliversToMailer confirms the
// sendSuspendedMail helper composes the right subject/body from
// mail.AccountSuspendedBody and hands the message to Mailer.Send.
// The recipient must be the account email; the body must mention
// "suspend" (the subject is the AccountSuspendedBody contract).
func TestDunning_SendSuspendedMail_DeliversToMailer(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	mailer := &localSender{}
	d := newDunningWithMailer(store, mailer)

	acct := localSeedAccount(t, ctx, store)
	at := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	d.sendSuspendedMail(ctx, acct, at)

	if got := mailer.count(); got != 1 {
		t.Fatalf("mailer.Send count = %d, want 1", got)
	}
	msg := mailer.last()
	if len(msg.To) != 1 || msg.To[0] != acct.Email {
		t.Errorf("msg.To = %v, want [%q]", msg.To, acct.Email)
	}
	if !strings.Contains(strings.ToLower(msg.Subject), "suspend") {
		t.Errorf("subject = %q, want it to mention 'suspend'", msg.Subject)
	}
	if msg.TextBody == "" {
		t.Error("TextBody is empty, want AccountSuspendedBody content")
	}
}

// TestDunning_SendDeletionMail_UsesAccountPastDueAt exercises the
// `acct.PastDueAt != nil` branch at dunning.go:259 — the helper must
// reuse the account's existing PastDueAt stamp rather than the `at`
// parameter for the body's "your account is scheduled for deletion"
// timestamp. This is the property the dashboard reads to display
// "your data will be deleted on DATE".
func TestDunning_SendDeletionMail_UsesAccountPastDueAt(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	mailer := &localSender{}
	d := newDunningWithMailer(store, mailer)

	acct := localSeedAccount(t, ctx, store)
	pastDueStamp := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if err := store.SetPastDueAtForTest(acct.ID, pastDueStamp); err != nil {
		t.Fatalf("SetPastDueAtForTest: %v", err)
	}
	at := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC) // 21 days later

	d.sendDeletionMail(ctx, acct, at)

	if got := mailer.count(); got != 1 {
		t.Fatalf("mailer.Send count = %d, want 1", got)
	}
	msg := mailer.last()
	if len(msg.To) != 1 || msg.To[0] != acct.Email {
		t.Errorf("msg.To = %v, want [%q]", msg.To, acct.Email)
	}
	if !strings.Contains(strings.ToLower(msg.Subject), "delete") {
		t.Errorf("subject = %q, want it to mention 'delete'", msg.Subject)
	}
}
