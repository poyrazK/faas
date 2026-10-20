package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// checkBillingProviders runs the ADR-1507 store contract: Lago connections
// keep their API URL, unknown providers are refused, and a statement's
// export cannot switch provider.
func checkBillingProviders(t *testing.T, ctx context.Context, store state.StatementInvoiceExportStore, accountID string) {
	t.Helper()
	lago, err := store.PutBillingConnection(ctx, state.BillingConnection{AccountID: accountID, Provider: state.BillingProviderLago,
		SealedSecret: []byte("sealed"), KeyLast4: "1234", Livemode: true, APIURL: "https://lago.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetBillingConnection(ctx, accountID, state.BillingProviderLago); err != nil || got.APIURL != lago.APIURL {
		t.Fatalf("lago connection = %+v, %v", got, err)
	}
	bad := []state.BillingConnection{
		{AccountID: accountID, Provider: "chargebee", SealedSecret: []byte("s"), KeyLast4: "1234"},
		{AccountID: accountID, Provider: state.BillingProviderLago, SealedSecret: []byte("s"), KeyLast4: "1234", APIURL: "http://lago.internal"},
	}
	for _, c := range bad {
		if _, err := store.PutBillingConnection(ctx, c); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("PutBillingConnection(%s, %q) = %v, want ErrInvalidArgument", c.Provider, c.APIURL, err)
		}
	}
	export := state.StatementInvoiceExport{AccountID: accountID, SourceKind: state.StatementSourceAppConsumer,
		StatementID: uuid.NewString(), Provider: state.BillingProviderPaddle, Customer: "ctm_01abc", Currency: "EUR", AmountMinor: 375}
	if _, existing, err := store.BeginStatementInvoiceExport(ctx, export); err != nil || existing {
		t.Fatalf("begin paddle export: existing=%v, %v", existing, err)
	}
	export.Provider = state.BillingProviderLago
	if _, _, err := store.BeginStatementInvoiceExport(ctx, export); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("begin lago export of a paddle statement = %v, want ErrConflict", err)
	}
	// ADR-20261018212918: Product Control statements export like other sources.
	product := export
	product.SourceKind, product.StatementID, product.Provider, product.Customer =
		state.StatementSourceProductControl, uuid.NewString(), state.BillingProviderStripe, "cus_product"
	if _, existing, err := store.BeginStatementInvoiceExport(ctx, product); err != nil || existing {
		t.Fatalf("begin product control export: existing=%v, %v", existing, err)
	}
	product.SourceKind, product.StatementID = "invoice", uuid.NewString()
	if _, _, err := store.BeginStatementInvoiceExport(ctx, product); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("begin export of an unknown statement source = %v, want ErrInvalidArgument", err)
	}
}

// adr: 1507
func TestMemStoreBillingProviders(t *testing.T) {
	ctx := context.Background()
	checkBillingProviders(t, ctx, state.NewMemStore(), uuid.NewString())
}
