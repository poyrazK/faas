package state

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Both stores run this contract after finalizing a fee-only workspace
// whose plan was activated at the start of the closed month.
func testProductOwnerInvoiceQueue(t *testing.T, s productOwnerBillingQueueTestStore, scope ProductBillingScope,
	statement api.ProductBillingDocument, trial ProductBillingScope, manualWorkspace func() ProductBillingScope) {
	t.Helper()
	ctx := t.Context()
	assertQueue := func(document, provider string) {
		t.Helper()
		page, err := s.ListProductOwnerInvoiceQueue(ctx, scope.AccountID, scope.AppID, scope.Period, "")
		if err != nil || page.NextCursor != "" {
			t.Fatalf("invoice queue = %+v, %v", page, err)
		}
		if document == "" {
			if len(page.Items) != 0 {
				t.Fatalf("invoice queue should be empty: %+v", page.Items)
			}
			return
		}
		if len(page.Items) != 1 || page.Items[0].DocumentID != document || page.Items[0].Provider != provider ||
			page.Items[0].AmountMillicents != statement.AmountMillicents {
			t.Fatalf("invoice queue = %+v, want %s / %s", page.Items, document, provider)
		}
	}
	assertQueue(statement.ID, "")
	export, _, err := s.BeginStatementInvoiceExport(ctx, StatementInvoiceExport{
		AccountID: scope.AccountID, SourceKind: StatementSourceProductControl, StatementID: statement.ID,
		Provider: BillingProviderStripe, Customer: "cus_queue", Currency: statement.Currency, AmountMinor: 900,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertQueue(statement.ID, BillingProviderStripe)
	if _, err := s.HandoffProductBilling(ctx, scope, statement.ID, "manual-race"); !errors.Is(err, ErrConflict) {
		t.Fatalf("manual handoff raced an export: %v", err)
	}
	if err := s.SetStatementInvoiceExportInvoice(ctx, scope.AccountID, export.ID, "in_queue"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HandoffProductBilling(ctx, scope, statement.ID, "in_queue"); err != nil {
		t.Fatal(err)
	}
	// A crash between writing the handoff and completing the export still
	// leaves the recorded provider invoice resumable.
	assertQueue(statement.ID, BillingProviderStripe)
	completed, err := s.CompleteStatementInvoiceExport(ctx, scope.AccountID, export.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertQueue("", "")
	t.Run("payment lifecycle", func(t *testing.T) {
		testProductOwnerPaymentQueue(t, s, scope, completed)
	})

	finalize := func(customer ProductBillingScope) api.ProductBillingDocument {
		t.Helper()
		review, err := s.ReviewProductBilling(ctx, customer)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := s.FinalizeProductBilling(ctx, customer, review.ReviewHash)
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	manual := manualWorkspace()
	doc := finalize(manual)
	assertQueue(doc.ID, "")
	if _, err := s.HandoffProductBilling(ctx, manual, doc.ID, "external-invoice-1"); err != nil {
		t.Fatal(err)
	}
	assertQueue("", "")
	// Simulate a handoff that commits after export preparation read the
	// statement but before the export tried to reserve it.
	racing, _, err := s.BeginStatementInvoiceExport(ctx, StatementInvoiceExport{
		AccountID: scope.AccountID, SourceKind: StatementSourceProductControl, StatementID: doc.ID,
		Provider: BillingProviderStripe, Customer: "cus_queue", Currency: doc.Currency, AmountMinor: 900,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveStatementHandoff(ctx, scope.AccountID, racing.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("export reservation bypassed a recorded handoff: %v", err)
	}
	assertQueue("", "")
	if zero := finalize(trial); zero.AmountMillicents != 0 {
		t.Fatalf("full-trial statement has a balance: %+v", zero)
	}
	assertQueue("", "")
	if _, err := s.ListProductOwnerInvoiceQueue(ctx, uuid.NewString(), scope.AppID, scope.Period, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign account returned %v", err)
	}
}
