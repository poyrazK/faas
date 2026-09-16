package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgAPIConsumerUsageStatementHandoffIsIdempotentAndUnique(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	consumer, err := store.CreateAPIConsumer(ctx, accountID, appID, "handoff-consumer", "Handoff customer")
	if err != nil {
		t.Fatalf("CreateAPIConsumer: %v", err)
	}

	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	createStatement := func(periodStart time.Time) state.APIConsumerUsageStatement {
		t.Helper()
		statement, created, err := store.CreateAPIConsumerUsageStatement(ctx, state.APIConsumerUsageStatementInput{
			AccountID: accountID, AppID: appID, ConsumerID: consumer.ID,
			PeriodStart: periodStart, PeriodEnd: periodStart.Add(time.Hour), Currency: "EUR",
			BillableUnits: 4, AmountMillicents: 100, Priced: true, AsOf: time.Now().UTC(),
			Buckets: []state.APIConsumerUsageStatementBucket{{
				WindowStart: periodStart, BillableUnits: 4, RateCardID: uuid.NewString(),
				Currency: "EUR", PriceMillicentsPerUnit: 25, AmountMillicents: 100,
			}},
		})
		if err != nil || !created {
			t.Fatalf("CreateAPIConsumerUsageStatement: statement=%+v created=%v err=%v", statement, created, err)
		}
		finalized, changed, err := store.FinalizeAPIConsumerUsageStatement(ctx, accountID, appID, consumer.ID, statement.ID)
		if err != nil || !changed || finalized.Status != state.APIConsumerUsageStatementFinalized {
			t.Fatalf("FinalizeAPIConsumerUsageStatement: statement=%+v changed=%v err=%v", finalized, changed, err)
		}
		return finalized
	}

	statement := createStatement(start)
	input := state.APIConsumerUsageStatementHandoffInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumer.ID,
		StatementID: statement.ID, ExternalInvoiceID: "pg-invoice-1001",
	}
	first, created, err := store.CreateAPIConsumerUsageStatementHandoff(ctx, input)
	if err != nil || !created || first.StatementID != statement.ID || first.ExternalInvoiceID != input.ExternalInvoiceID || first.AmountMillicents != 100 || first.Currency != "EUR" {
		t.Fatalf("CreateAPIConsumerUsageStatementHandoff: handoff=%+v created=%v err=%v", first, created, err)
	}
	replay, created, err := store.CreateAPIConsumerUsageStatementHandoff(ctx, input)
	if err != nil || created || replay.ID != first.ID {
		t.Fatalf("handoff replay: handoff=%+v created=%v err=%v", replay, created, err)
	}
	if _, _, err := store.CreateAPIConsumerUsageStatementHandoff(ctx, state.APIConsumerUsageStatementHandoffInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumer.ID,
		StatementID: statement.ID, ExternalInvoiceID: "pg-invoice-1002",
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("statement reuse: err=%v, want ErrConflict", err)
	}

	other := createStatement(start.Add(2 * time.Hour))
	if _, _, err := store.CreateAPIConsumerUsageStatementHandoff(ctx, state.APIConsumerUsageStatementHandoffInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumer.ID,
		StatementID: other.ID, ExternalInvoiceID: input.ExternalInvoiceID,
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("external invoice reuse: err=%v, want ErrConflict", err)
	}

	got, err := store.GetAPIConsumerUsageStatementHandoff(ctx, accountID, appID, consumer.ID, statement.ID)
	if err != nil || got.ID != first.ID || got.ExternalInvoiceID != first.ExternalInvoiceID {
		t.Fatalf("GetAPIConsumerUsageStatementHandoff: handoff=%+v err=%v", got, err)
	}
	if _, err := store.GetAPIConsumerUsageStatementHandoff(ctx, accountID, appID, consumer.ID, other.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing handoff: err=%v, want ErrNotFound", err)
	}
}
