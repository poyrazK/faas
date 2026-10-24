package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// checkStatementHandoffReservations runs the ADR-20261020211500 store
// contract: an export's reservation counts as a handoff under ADR-238's
// overlap rules, so an overlapping statement can be neither reserved nor
// handed off, and a refused attempt releases it.
func checkStatementHandoffReservations(t *testing.T, ctx context.Context, st state.Store, accountID, appID string) {
	t.Helper()
	statements := st.(state.APIConsumerUsageStatementStore)
	exports := st.(state.StatementInvoiceExportStore)
	consumer, err := st.CreateAPIConsumer(ctx, accountID, appID, "reserve-consumer", "Reserving customer")
	if err != nil {
		t.Fatal(err)
	}
	finalized := func(start time.Time) state.APIConsumerUsageStatement {
		t.Helper()
		created, _, err := statements.CreateAPIConsumerUsageStatement(ctx, state.APIConsumerUsageStatementInput{
			AccountID: accountID, AppID: appID, ConsumerID: consumer.ID, Revision: 1, PeriodStart: start, PeriodEnd: start.Add(2 * time.Hour),
			Currency: "EUR", BillableUnits: 4, AmountMillicents: 100, Priced: true, AsOf: time.Now().UTC(),
			Buckets: []state.APIConsumerUsageStatementBucket{{WindowStart: start, BillableUnits: 4, RateCardID: uuid.NewString(),
				Currency: "EUR", PriceMillicentsPerUnit: 25, AmountMillicents: 100}},
		})
		if err != nil {
			t.Fatal(err)
		}
		final, _, err := statements.FinalizeAPIConsumerUsageStatement(ctx, accountID, appID, consumer.ID, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		return final
	}
	begin := func(statementID string) state.StatementInvoiceExport {
		t.Helper()
		export, _, err := exports.BeginStatementInvoiceExport(ctx, state.StatementInvoiceExport{AccountID: accountID,
			SourceKind: state.StatementSourceAppConsumer, StatementID: statementID, Provider: state.BillingProviderStripe,
			Customer: "cus_Reserve", Currency: "EUR", AmountMinor: 1})
		if err != nil {
			t.Fatal(err)
		}
		return export
	}
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	first, overlapping := finalized(start), finalized(start.Add(time.Hour))
	firstExport, overlappingExport := begin(first.ID), begin(overlapping.ID)

	if err := exports.ReserveStatementHandoff(ctx, accountID, firstExport.ID); err != nil {
		t.Fatal(err)
	}
	if err := exports.ReserveStatementHandoff(ctx, accountID, firstExport.ID); err != nil {
		t.Fatalf("second reservation of the same export: %v", err)
	}
	if blocked, err := st.(state.StatementHandoffChecker).StatementHandoffBlocked(ctx, accountID, state.StatementSourceAppConsumer, overlapping.ID); err != nil || !blocked {
		t.Fatalf("overlapping statement blocked = %t, %v; want blocked by the reservation", blocked, err)
	}
	if err := exports.ReserveStatementHandoff(ctx, accountID, overlappingExport.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("reserving an overlapping statement: %v, want conflict", err)
	}
	if _, _, err := st.(state.APIConsumerUsageStatementHandoffStore).CreateAPIConsumerUsageStatementHandoff(ctx, state.APIConsumerUsageStatementHandoffInput{AccountID: accountID,
		AppID: appID, ConsumerID: consumer.ID, StatementID: overlapping.ID, ExternalInvoiceID: "in_overlap"}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("handing off an overlapping statement: %v, want conflict", err)
	}
	if err := exports.ReserveStatementHandoff(ctx, accountID, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("reserving an unknown export: %v", err)
	}

	token := uuid.NewString()
	if _, err := exports.ClaimStatementInvoiceExport(ctx, accountID, firstExport.ID, token, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := exports.RejectStatementInvoiceExport(ctx, accountID, firstExport.ID, token); err != nil {
		t.Fatal(err)
	}
	if got, err := exports.GetStatementInvoiceExport(ctx, accountID, state.StatementSourceAppConsumer, first.ID); err != nil || got.HandoffReserved {
		t.Fatalf("refused export = %+v, %v; want its reservation released", got, err)
	}
	if err := exports.ReserveStatementHandoff(ctx, accountID, overlappingExport.ID); err != nil {
		t.Fatalf("reserving after the refusal released the first: %v", err)
	}
	if blocked, err := st.(state.StatementHandoffChecker).StatementHandoffBlocked(ctx, accountID, state.StatementSourceAppConsumer, first.ID); err != nil || !blocked {
		t.Fatalf("first statement blocked = %t, %v; want blocked by the new reservation", blocked, err)
	}
}

// adr: 20261020211500
func TestMemStoreStatementHandoffReservations(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkStatementHandoffReservations(t, ctx, store, accountID, appID)
}

// adr: 20261020211500
func TestPgStoreStatementHandoffReservations(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkStatementHandoffReservations(t, ctx, store, accountID, appID)
}
