package state

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func testProductOwnerPaymentQueue(t *testing.T, s productOwnerBillingQueueTestStore, scope ProductBillingScope, export StatementInvoiceExport) {
	t.Helper()
	ctx := t.Context()
	now := export.CompletedAt.Add(time.Minute).Truncate(time.Microsecond)
	list := func(at time.Time, action ProductOwnerPaymentAction) ProductOwnerPaymentQueueItem {
		t.Helper()
		page, err := s.ListProductOwnerPaymentQueue(ctx, scope.AccountID, scope.AppID, "", at)
		if err != nil || page.NextCursor != "" {
			t.Fatalf("payment queue = %+v, %v", page, err)
		}
		if action == "" && len(page.Items) == 0 {
			return ProductOwnerPaymentQueueItem{}
		}
		if len(page.Items) != 1 {
			t.Fatalf("payment queue = %+v, want %q", page.Items, action)
		}
		item := page.Items[0]
		if item.Action != action || item.DocumentID != export.StatementID || item.TenantID != scope.TenantID || item.Period != scope.Period {
			t.Fatalf("payment queue item = %+v, want action %q", item, action)
		}
		return item
	}
	record := func(at time.Time, payment StatementInvoicePayment) {
		t.Helper()
		if _, err := s.RecordStatementInvoicePayment(ctx, export.ID, payment, at, nil); err != nil {
			t.Fatal(err)
		}
	}
	draft := list(now, ProductOwnerPaymentDraft)
	if !draft.StripeSend || draft.StatusSyncedAt != nil || draft.DueAt != nil || draft.AmountRemainingMinor != nil {
		t.Fatalf("unsynced draft has provider details: %+v", draft)
	}
	list(now.Add(api.ConsumerOverdueStatusMaxAge), ProductOwnerPaymentStale)
	due := now.Add(time.Hour)
	checked := now.Add(time.Minute)
	open := StatementInvoicePayment{Status: InvoicePaymentOpen, DueAt: &due, AmountRemainingMinor: 700, HostedInvoiceURL: "https://invoice.stripe.com/i/queue"}
	record(checked, open)
	list(due, "")
	token := uuid.NewString()
	if _, err := s.ClaimStatementInvoiceExport(ctx, scope.AccountID, export.ID, token, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	attempt := ProductStripeInvoiceSend{ReviewHash: strings.Repeat("a", 64), TermsHash: strings.Repeat("b", 64), DueAt: due}
	if err := s.BeginProductStripeInvoiceSend(ctx, scope.AccountID, export.ID, uuid.NewString(), attempt); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong claimant = %v", err)
	}
	if err := s.BeginProductStripeInvoiceSend(ctx, scope.AccountID, export.ID, token, attempt); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceProductStripeInvoiceSend(ctx, scope.AccountID, export.ID, token, StripeInvoiceSending, checked); !errors.Is(err, ErrConflict) {
		t.Fatalf("skipping finalization = %v", err)
	}
	for _, stage := range []string{StripeInvoiceFinalizing, StripeInvoiceSending} {
		if err := s.AdvanceProductStripeInvoiceSend(ctx, scope.AccountID, export.ID, token, stage, checked); err != nil {
			t.Fatal(err)
		}
	}
	pending := list(checked, ProductOwnerPaymentStripeSend)
	if err := s.BeginProductStripeInvoiceReminder(ctx, scope.AccountID, export.ID, token, attempt.ReviewHash, checked); !errors.Is(err, ErrConflict) {
		t.Fatalf("reminder accepted while the initial send was unconfirmed: %v", err)
	}
	if !pending.StripeSend || pending.StripeReminder {
		t.Fatalf("open invoice lost its pending send: %+v", pending)
	}
	if _, err := s.GetProductStripeInvoiceSend(ctx, uuid.NewString(), export.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign attempt read = %v", err)
	}
	expired := list(checked.Add(api.ProductStripeInvoiceSendRetryWindow), ProductOwnerPaymentStripeReview)
	if expired.ProviderInvoiceID != export.ProviderInvoiceID || expired.Livemode != export.Livemode {
		t.Fatalf("expired send recovery targets the wrong invoice: %+v", expired)
	}
	eventAt := checked.Truncate(time.Second)
	if err := s.VerifyProductStripeInvoiceSend(ctx, scope.AccountID, export.ID, token, "evt_confirmed", eventAt, checked.Add(-time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("verified a send before the retry window ended: %v", err)
	}
	if err := s.VerifyProductStripeInvoiceSend(ctx, scope.AccountID, export.ID, uuid.NewString(), "evt_confirmed", eventAt, checked); !errors.Is(err, ErrConflict) {
		t.Fatalf("verified a send with the wrong claimant: %v", err)
	}
	if err := s.VerifyProductStripeInvoiceSend(ctx, scope.AccountID, export.ID, token, "evt_earlier", eventAt.Add(-time.Second), checked); !errors.Is(err, ErrConflict) {
		t.Fatalf("verified a send with an earlier event: %v", err)
	}
	if err := s.VerifyProductStripeInvoiceSend(ctx, scope.AccountID, export.ID, token, "evt_confirmed", eventAt, checked); err != nil {
		t.Fatal(err)
	}
	saved, err := s.GetProductStripeInvoiceSend(ctx, scope.AccountID, export.ID)
	if err != nil || saved.Stage != StripeInvoiceSent || !saved.SendStartedAt.Equal(checked) || saved.ReviewHash != attempt.ReviewHash ||
		saved.TermsHash != attempt.TermsHash || !saved.DueAt.Equal(due) || saved.SendEventID != "evt_confirmed" || !saved.SendEventAt.Equal(eventAt) {
		t.Fatalf("saved send = %+v, %v", saved, err)
	}
	list(due, "")
	reminderAt := due.Add(time.Minute)
	if !list(reminderAt, ProductOwnerPaymentOverdue).StripeReminder {
		t.Fatal("confirmed overdue Stripe invoice is missing reminder eligibility")
	}
	if err := s.BeginProductStripeInvoiceReminder(ctx, scope.AccountID, export.ID, uuid.NewString(), attempt.ReviewHash, reminderAt); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong reminder claimant: %v", err)
	}
	if err := s.BeginProductStripeInvoiceReminder(ctx, scope.AccountID, export.ID, token, attempt.ReviewHash, reminderAt); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginProductStripeInvoiceReminder(ctx, scope.AccountID, export.ID, token, attempt.ReviewHash, reminderAt); !errors.Is(err, ErrConflict) {
		t.Fatalf("started a second reminder: %v", err)
	}
	reminderPending := list(reminderAt, ProductOwnerPaymentOverdue)
	if !reminderPending.ReminderStartedAt.Equal(reminderAt) || !reminderPending.ReminderSentAt.IsZero() {
		t.Fatalf("pending reminder details = %+v", reminderPending)
	}
	if err := s.CompleteProductStripeInvoiceReminder(ctx, scope.AccountID, export.ID, token, attempt.TermsHash, reminderAt); !errors.Is(err, ErrConflict) {
		t.Fatalf("completed reminder with different review terms: %v", err)
	}
	if err := s.CompleteProductStripeInvoiceReminder(ctx, scope.AccountID, export.ID, token, attempt.ReviewHash, reminderAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	overdue := list(checked.Add(api.ConsumerOverdueStatusMaxAge), ProductOwnerPaymentOverdue)
	if !overdue.ReminderSentAt.Equal(reminderAt.Add(time.Second)) {
		t.Fatalf("missing reminder acknowledgment: %+v", overdue)
	}
	if overdue.DueAt == nil || !overdue.DueAt.Equal(due) || overdue.StatusSyncedAt == nil ||
		!overdue.StatusSyncedAt.Equal(checked) || overdue.AmountRemainingMinor == nil ||
		*overdue.AmountRemainingMinor != 700 || overdue.HostedInvoiceURL != open.HostedInvoiceURL {
		t.Fatalf("overdue payment details = %+v", overdue)
	}
	staleAt := checked.Add(api.ConsumerOverdueStatusMaxAge + time.Second)
	list(staleAt, ProductOwnerPaymentStale)
	record(staleAt, StatementInvoicePayment{Status: InvoicePaymentPaid, AmountPaidMinor: 900})
	// A background observation that started before a newer sync completed
	// must not restore the previous state.
	if _, err := s.RecordStatementInvoicePayment(ctx, export.ID, open, checked, nil); !errors.Is(err, ErrStatementInvoicePaymentStale) {
		t.Fatalf("stale observation returned %v", err)
	}
	list(staleAt.Add(api.ConsumerOverdueStatusMaxAge+time.Hour), "")
	if _, err := s.ListProductOwnerPaymentQueue(ctx, uuid.NewString(), scope.AppID, "", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign account returned %v", err)
	}
	foreignCursor := encodeProductOwnerCursor(productOwnerCursor{
		Scope:  productOwnerCursorScope("payment-queue", scope.AccountID, uuid.NewString(), "", ""),
		Period: scope.Period, ID: export.StatementID,
	})
	if _, err := s.ListProductOwnerPaymentQueue(ctx, scope.AccountID, scope.AppID, foreignCursor, now); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("foreign app cursor returned %v", err)
	}
}
