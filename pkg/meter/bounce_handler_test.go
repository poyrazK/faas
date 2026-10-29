package meter

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// silentLog keeps the bounce handler tests quiet while still
// exercising the logger field.
func silentLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// recordingAuditor captures every Emit call so tests can pin
// the audit row kind + account ID + payload shape.
type recordingAuditor struct {
	rows []recordedAuditRow
}

type recordedAuditRow struct {
	Kind      string
	AccountID *string
	Data      map[string]any
}

func (a *recordingAuditor) Emit(_ context.Context, kind string, accountID *string, data map[string]any) {
	a.rows = append(a.rows, recordedAuditRow{Kind: kind, AccountID: accountID, Data: data})
}

// newBounceFixture stands up a MemStore + a free-plan account
// for the bounce handler tests. Returns the store, account ID,
// email, and a no-op auditor stand-in.
func newBounceFixture(t *testing.T) (state.Store, string, string) {
	t.Helper()
	store := state.NewMemStore()
	email := "alice@example.com"
	acct, err := store.CreateAccount(context.Background(), email, api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	return store, acct.ID, email
}

func newHandler(store state.Store, aud *recordingAuditor) *BounceHandler {
	return &BounceHandler{Store: store, Auditor: aud, Log: silentLog()}
}

// TestBounceHandler_HardBounceSuppressesWithoutDunning pins the headline
// contract: a hard_bounce event (1) writes a mail_suppressions row, (2)
// emits a mail.bounce_hard audit row, and (3) leaves the account's billing
// status alone. It used to mark the account past_due, which suspended a
// paying customer a week after one bounce (bug hunt 2026-10-28 B26).
func TestBounceHandler_HardBounceSuppressesWithoutDunning(t *testing.T) {
	store, accountID, email := newBounceFixture(t)
	aud := &recordingAuditor{}
	h := newHandler(store, aud)
	// Use the Auditor stand-in via a tiny shim so the bounce
	// handler can emit the audit row.

	evtID := "evt_" + uuid.NewString()
	err := h.HandleMailBounce(context.Background(), MailBounce{
		Source:          "resend",
		ProviderEventID: evtID,
		Email:           email,
		Reason:          "hard_bounce",
	})
	if err != nil {
		t.Fatalf("HandleMailBounce returned %v, want nil", err)
	}

	// Suppression row written.
	suppressed, err := store.IsMailSuppressed(context.Background(), email)
	if err != nil || !suppressed {
		t.Fatalf("IsMailSuppressed(%q) = %v, %v; want true, nil", email, suppressed, err)
	}

	// Billing status untouched.
	acct, err := store.AccountByID(context.Background(), accountID)
	if err != nil {
		t.Fatal(err)
	}
	if acct.Status != state.AccountActive || acct.PastDueAt != nil {
		t.Fatalf("account status = %q, past_due_at %v; want active", acct.Status, acct.PastDueAt)
	}

	// Audit row emitted.
	if got := len(aud.rows); got != 1 {
		t.Fatalf("audit rows = %d, want 1", got)
	}
	row := aud.rows[0]
	if row.Kind != "mail.bounce_hard" {
		t.Fatalf("audit kind = %q, want mail.bounce_hard", row.Kind)
	}
	if row.AccountID == nil || *row.AccountID != accountID {
		t.Fatalf("audit AccountID = %v, want pointer to %s", row.AccountID, accountID)
	}
	if row.Data["source"] != "resend" {
		t.Fatalf("audit data.source = %v, want resend", row.Data["source"])
	}
	if row.Data["email"] != email {
		t.Fatalf("audit data.email = %v, want %s", row.Data["email"], email)
	}
	if row.Data["provider_event_id"] != evtID {
		t.Fatalf("audit data.provider_event_id = %v, want %s", row.Data["provider_event_id"], evtID)
	}
	if inserted, ok := row.Data["inserted"].(bool); !ok || !inserted {
		t.Fatalf("audit data.inserted = %v, want true", row.Data["inserted"])
	}
}

// TestBounceHandler_ComplaintDoesNotAdvanceDunning pins the
// "spam is not a billing event" policy: complaints suppress
// but do NOT call MarkDunningStep. Suspending an account
// because the recipient hit "spam" is hostile.
func TestBounceHandler_ComplaintDoesNotAdvanceDunning(t *testing.T) {
	store, accountID, email := newBounceFixture(t)
	aud := &recordingAuditor{}
	h := newHandler(store, aud)

	err := h.HandleMailBounce(context.Background(), MailBounce{
		Source:          "resend",
		ProviderEventID: "evt_" + uuid.NewString(),
		Email:           email,
		Reason:          "complaint",
	})
	if err != nil {
		t.Fatalf("HandleMailBounce returned %v, want nil", err)
	}

	// Suppression row written.
	suppressed, _ := store.IsMailSuppressed(context.Background(), email)
	if !suppressed {
		t.Fatal("complaint did not suppress the address")
	}

	// Account NOT advanced.
	acct, _ := store.AccountByID(context.Background(), accountID)
	if acct.Status != state.AccountActive {
		t.Fatalf("account status = %q, want active (complaint must not transition)", acct.Status)
	}

	// Audit row emitted with complaint kind.
	if got := len(aud.rows); got != 1 {
		t.Fatalf("audit rows = %d, want 1", got)
	}
	if aud.rows[0].Kind != "mail.bounce_complaint" {
		t.Fatalf("audit kind = %q, want mail.bounce_complaint", aud.rows[0].Kind)
	}
}

// TestBounceHandler_ReplayIsNoOp pins the redelivery-race guard:
// the (source, provider_event_id) unique index dedupes, and a
// replay must NOT advance dunning a second time.
func TestBounceHandler_ReplayIsNoOp(t *testing.T) {
	store, accountID, email := newBounceFixture(t)
	aud := &recordingAuditor{}
	h := newHandler(store, aud)

	evtID := "evt_" + uuid.NewString()
	bounce := MailBounce{
		Source:          "resend",
		ProviderEventID: evtID,
		Email:           email,
		Reason:          "hard_bounce",
	}
	// First delivery: writes suppression + advances dunning.
	if err := h.HandleMailBounce(context.Background(), bounce); err != nil {
		t.Fatal(err)
	}
	// Second delivery: same event ID. Must NOT advance dunning again.
	if err := h.HandleMailBounce(context.Background(), bounce); err != nil {
		t.Fatal(err)
	}
	// PastDueAt stamped exactly once — the second call must NOT
	// bump it. We assert by checking the field is still set and
	// the audit log shows exactly two rows (both legitimate
	// audit emissions, even on replay, for ops visibility).
	if got := len(aud.rows); got != 2 {
		t.Fatalf("audit rows = %d, want 2", got)
	}
	first := aud.rows[0]
	second := aud.rows[1]
	if !first.Data["inserted"].(bool) {
		t.Fatalf("first insert.inserted = %v, want true", first.Data["inserted"])
	}
	if second.Data["inserted"].(bool) {
		t.Fatalf("replay insert.inserted = %v, want false", second.Data["inserted"])
	}
	acct, _ := store.AccountByID(context.Background(), accountID)
	if acct.Status != state.AccountActive {
		t.Fatalf("account status after replay = %q, want active", acct.Status)
	}
}

// TestBounceHandler_SoftBounceIgnored pins that soft_bounces
// return ErrMailBounceIgnored and write nothing — Resend's free
// tier retries transient failures itself, and a
// soft-bounce-as-past_due would flip paying customers off the
// platform on a flaky SMTP path.
func TestBounceHandler_SoftBounceIgnored(t *testing.T) {
	store, accountID, email := newBounceFixture(t)
	aud := &recordingAuditor{}
	h := newHandler(store, aud)

	err := h.HandleMailBounce(context.Background(), MailBounce{
		Source:          "resend",
		ProviderEventID: "evt_" + uuid.NewString(),
		Email:           email,
		Reason:          "soft_bounce",
	})
	if !errors.Is(err, ErrMailBounceIgnored) {
		t.Fatalf("soft_bounce returned %v, want ErrMailBounceIgnored", err)
	}
	suppressed, _ := store.IsMailSuppressed(context.Background(), email)
	if suppressed {
		t.Fatal("soft_bounce unexpectedly suppressed the address")
	}
	acct, _ := store.AccountByID(context.Background(), accountID)
	if acct.Status != state.AccountActive {
		t.Fatalf("account status = %q, want active (soft_bounce must not transition)", acct.Status)
	}
	if got := len(aud.rows); got != 0 {
		t.Fatalf("audit rows = %d, want 0 (soft_bounce must not emit)", got)
	}
}

// TestBounceHandler_UnknownAddressStillSuppresses pins that an
// email address with no account on file still goes on the
// suppression list. The SuppressingSender decorator checks the
// list before any send; without this guarantee a future campaign
// to a typo address would re-bounce the same address forever.
func TestBounceHandler_UnknownAddressStillSuppresses(t *testing.T) {
	store := state.NewMemStore()
	aud := &recordingAuditor{}
	h := newHandler(store, aud)

	err := h.HandleMailBounce(context.Background(), MailBounce{
		Source:          "resend",
		ProviderEventID: "evt_" + uuid.NewString(),
		Email:           "ghost@example.com",
		Reason:          "hard_bounce",
	})
	if err != nil {
		t.Fatalf("HandleMailBounce returned %v, want nil", err)
	}
	suppressed, _ := store.IsMailSuppressed(context.Background(), "ghost@example.com")
	if !suppressed {
		t.Fatal("unknown address not suppressed")
	}
}

// TestBounceHandler_AlreadyPastDueDoesNotDoubleTransition pins
// that a hard_bounce landing on an already-past_due account
// returns nil rather than failing — the MarkDunningStep CAS
// returns ErrNotFound on the from-status mismatch and we treat
// it as the redelivery-race guard the existing handler relies
// on.
func TestBounceHandler_AlreadyPastDueDoesNotDoubleTransition(t *testing.T) {
	store, accountID, email := newBounceFixture(t)
	aud := &recordingAuditor{}
	h := newHandler(store, aud)

	// Flip the account to past_due via the existing store path.
	if err := store.MarkDunningStep(context.Background(), accountID, state.AccountActive, state.AccountPastDue); err != nil {
		t.Fatal(err)
	}

	// A second bounce event now must NOT error — the CAS
	// returns ErrNotFound and we swallow it as the
	// redelivery-race guard.
	err := h.HandleMailBounce(context.Background(), MailBounce{
		Source:          "resend",
		ProviderEventID: "evt_" + uuid.NewString(),
		Email:           email,
		Reason:          "hard_bounce",
	})
	if err != nil {
		t.Fatalf("already-past_due bounce returned %v, want nil", err)
	}
}

// TestBounceHandler_EmptyFieldsRejected pins the validation
// surface. A bounce with empty source / event id / email is a
// programming error (the webhook handler is supposed to reject
// those before this entry) and must fail loudly rather than
// silently dropping.
func TestBounceHandler_EmptyFieldsRejected(t *testing.T) {
	store, _, email := newBounceFixture(t)
	h := newHandler(store, &recordingAuditor{})

	cases := []MailBounce{
		{Source: "resend", ProviderEventID: "evt_1", Email: "", Reason: "hard_bounce"},
		{Source: "resend", ProviderEventID: "", Email: email, Reason: "hard_bounce"},
		{Source: "", ProviderEventID: "evt_2", Email: email, Reason: "hard_bounce"},
	}
	for i, b := range cases {
		if err := h.HandleMailBounce(context.Background(), b); err == nil {
			t.Fatalf("case %d: empty-field bounce returned nil, want error", i)
		} else if !strings.Contains(err.Error(), "is empty") {
			t.Fatalf("case %d: error %q missing 'is empty' sentinel", i, err)
		}
	}
}

// TestBounceHandler_NilDepsRejected pins the constructor-side
// guards: a handler with a nil Store or nil Auditor must NOT
// silently no-op (which would let a misconfigured wiring lose
// bounces).
func TestBounceHandler_NilDepsRejected(t *testing.T) {
	store := state.NewMemStore()
	h := &BounceHandler{Store: nil, Auditor: nil, Log: silentLog()}
	if err := h.HandleMailBounce(context.Background(), MailBounce{
		Source: "resend", ProviderEventID: "evt_1", Email: "x@example.com", Reason: "hard_bounce",
	}); err == nil {
		t.Fatal("nil Store should fail")
	}
	h2 := &BounceHandler{Store: store, Auditor: nil, Log: silentLog()}
	if err := h2.HandleMailBounce(context.Background(), MailBounce{
		Source: "resend", ProviderEventID: "evt_2", Email: "x@example.com", Reason: "hard_bounce",
	}); err == nil {
		t.Fatal("nil Auditor should fail")
	}
}

// TestAuditKindForBounce pins the audit kind mapping. A typo
// here would scatter the dashboard's bounce columns and break
// the Grafana panel the on-call uses to spot a billing-reachability
// incident, so the table is pinned.
func TestAuditKindForBounce(t *testing.T) {
	cases := map[string]string{
		"hard_bounce": "mail.bounce_hard",
		"complaint":   "mail.bounce_complaint",
		"unknown":     "mail.bounce_unknown",
	}
	for in, want := range cases {
		if got := auditKindForBounce(in); got != want {
			t.Fatalf("auditKindForBounce(%q) = %q, want %q", in, got, want)
		}
	}
}
