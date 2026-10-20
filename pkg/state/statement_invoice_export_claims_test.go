package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// checkStatementInvoiceExportClaims runs the ADR-20261019180000 store
// contract: one attempt at a time holds an export, a claim ends on release,
// completion or expiry, and an export the provider refused before creating
// anything takes a new customer or provider as its next attempt.
func checkStatementInvoiceExportClaims(t *testing.T, ctx context.Context, store state.StatementInvoiceExportStore, accountID string) {
	t.Helper()
	export, existing, err := store.BeginStatementInvoiceExport(ctx, state.StatementInvoiceExport{AccountID: accountID,
		SourceKind: state.StatementSourceAppConsumer, StatementID: uuid.NewString(), Provider: state.BillingProviderStripe,
		Customer: "cus_Wrong", Currency: "EUR", AmountMinor: 375})
	if err != nil || existing || export.Attempt != 0 || export.RejectedAt != nil {
		t.Fatalf("begin = %+v, %t, %v", export, existing, err)
	}
	first, second := uuid.NewString(), uuid.NewString()
	until := time.Now().Add(time.Minute)
	if _, err := store.ClaimStatementInvoiceExport(ctx, accountID, export.ID, first, until); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimStatementInvoiceExport(ctx, accountID, export.ID, second, until); !errors.Is(err, state.ErrStatementInvoiceExportInProgress) {
		t.Fatalf("second claim while held: %v, want in progress", err)
	}
	if _, err := store.ClaimStatementInvoiceExport(ctx, accountID, uuid.NewString(), second, until); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("claim of an unknown export: %v", err)
	}
	retarget := state.StatementInvoiceExport{AccountID: accountID, SourceKind: export.SourceKind, StatementID: export.StatementID,
		Provider: state.BillingProviderPaddle, Customer: "ctm_01right", Currency: "EUR", AmountMinor: 375}
	if _, _, err := store.BeginStatementInvoiceExport(ctx, retarget); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("retarget before a rejection: %v, want conflict", err)
	}
	if err := store.RejectStatementInvoiceExport(ctx, accountID, export.ID, second); err == nil {
		t.Fatal("an attempt without the claim rejected the export")
	}
	if err := store.RejectStatementInvoiceExport(ctx, accountID, export.ID, first); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetStatementInvoiceExport(ctx, accountID, export.SourceKind, export.StatementID); err != nil || !got.Retargetable() {
		t.Fatalf("rejected export = %+v, %v", got, err)
	}

	moved, existing, err := store.BeginStatementInvoiceExport(ctx, retarget)
	if err != nil || !existing || moved.ID != export.ID || moved.Provider != state.BillingProviderPaddle || moved.Customer != "ctm_01right" ||
		moved.Attempt != 1 || moved.RejectedAt != nil {
		t.Fatalf("retargeted = %+v, %t, %v", moved, existing, err)
	}
	if _, err := store.ClaimStatementInvoiceExport(ctx, accountID, export.ID, second, until); err != nil {
		t.Fatalf("claim after the rejection released it: %v", err)
	}
	if err := store.SetStatementInvoiceExportInvoice(ctx, accountID, export.ID, "txn_01"); err != nil {
		t.Fatal(err)
	}
	if err := store.RejectStatementInvoiceExport(ctx, accountID, export.ID, second); err == nil {
		t.Fatal("rejected an export whose invoice exists")
	}
	if err := store.ReleaseStatementInvoiceExport(ctx, accountID, export.ID, second); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimStatementInvoiceExport(ctx, accountID, export.ID, first, until); err != nil {
		t.Fatalf("claim after release: %v", err)
	}
	if _, err := store.CompleteStatementInvoiceExport(ctx, accountID, export.ID); err != nil {
		t.Fatal(err)
	}
	if done, err := store.ClaimStatementInvoiceExport(ctx, accountID, export.ID, second, until); err != nil ||
		done.Status != state.StatementInvoiceExportCompleted {
		t.Fatalf("claim after completion = %+v, %v", done, err)
	}
	if _, _, err := store.BeginStatementInvoiceExport(ctx, state.StatementInvoiceExport{AccountID: accountID, SourceKind: export.SourceKind,
		StatementID: export.StatementID, Provider: state.BillingProviderStripe, Customer: "cus_Other", Currency: "EUR",
		AmountMinor: 375}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("retarget of a completed export: %v", err)
	}

	expired, _, err := store.BeginStatementInvoiceExport(ctx, state.StatementInvoiceExport{AccountID: accountID,
		SourceKind: state.StatementSourcePlatformTenant, StatementID: uuid.NewString(), Provider: state.BillingProviderStripe,
		Customer: "cus_Crash", Currency: "EUR", AmountMinor: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimStatementInvoiceExport(ctx, accountID, expired.ID, first, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimStatementInvoiceExport(ctx, accountID, expired.ID, second, until); err != nil {
		t.Fatalf("claim after an expired claim: %v", err)
	}
}

// adr: 20261019180000
func TestMemStoreStatementInvoiceExportClaims(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, _ := seedConsumerKeyAccountApp(t, ctx, store)
	checkStatementInvoiceExportClaims(t, ctx, store, accountID)
}
