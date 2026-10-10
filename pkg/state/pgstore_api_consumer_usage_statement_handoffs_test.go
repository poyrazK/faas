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
			AccountID: accountID, AppID: appID, ConsumerID: consumer.ID, Revision: 1,
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

// adr: 934
func TestPgAPIConsumerUsageStatementRevisionsAndAdjustmentHandoff(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	consumer, err := store.CreateAPIConsumer(ctx, accountID, appID, "revision-consumer", "Revision customer")
	if err != nil {
		t.Fatalf("CreateAPIConsumer: %v", err)
	}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	quote := func(revision int, prior state.APIConsumerUsageStatementStatus, units int64) state.APIConsumerUsageStatementInput {
		return state.APIConsumerUsageStatementInput{
			AccountID: accountID, AppID: appID, ConsumerID: consumer.ID, Revision: revision, PriorStatus: prior,
			PeriodStart: start, PeriodEnd: start.Add(time.Hour), Currency: "EUR",
			BillableUnits: units, AmountMillicents: units * 10, Priced: true, AsOf: time.Now().UTC(),
			Buckets: []state.APIConsumerUsageStatementBucket{{
				WindowStart: start, BillableUnits: units, RateCardID: uuid.NewString(),
				Currency: "EUR", PriceMillicentsPerUnit: 10, AmountMillicents: units * 10,
			}},
		}
	}
	first, created, err := store.CreateAPIConsumerUsageStatement(ctx, quote(1, "", 2))
	if err != nil || !created || first.Revision != 1 {
		t.Fatalf("revision 1: statement=%+v created=%v err=%v", first, created, err)
	}
	unchanged := quote(2, state.APIConsumerUsageStatementDraft, 2)
	unchanged.Buckets[0].RateCardID = first.Buckets[0].RateCardID
	if same, created, err := store.CreateAPIConsumerUsageStatement(ctx, unchanged); err != nil || created || same.ID != first.ID {
		t.Fatalf("unchanged draft: statement=%+v created=%v err=%v, want replay", same, created, err)
	}
	if _, _, err := store.CreateAPIConsumerUsageStatement(ctx, quote(3, state.APIConsumerUsageStatementDraft, 3)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("skipped revision: err=%v, want ErrConflict", err)
	}
	second, created, err := store.CreateAPIConsumerUsageStatement(ctx, quote(2, state.APIConsumerUsageStatementDraft, 3))
	if err != nil || !created || second.Revision != 2 {
		t.Fatalf("revision 2: statement=%+v created=%v err=%v", second, created, err)
	}
	if _, _, err := store.FinalizeAPIConsumerUsageStatement(ctx, accountID, appID, consumer.ID, first.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("finalize superseded: err=%v, want ErrConflict", err)
	}
	if _, changed, err := store.FinalizeAPIConsumerUsageStatement(ctx, accountID, appID, consumer.ID, second.ID); err != nil || !changed {
		t.Fatalf("finalize revision 2: changed=%v err=%v", changed, err)
	}
	adjustment, created, err := store.CreateAPIConsumerUsageStatement(ctx, quote(3, state.APIConsumerUsageStatementFinalized, 1))
	if err != nil || !created || adjustment.Revision != 3 {
		t.Fatalf("adjustment: statement=%+v created=%v err=%v", adjustment, created, err)
	}
	if _, changed, err := store.FinalizeAPIConsumerUsageStatement(ctx, accountID, appID, consumer.ID, adjustment.ID); err != nil || !changed {
		t.Fatalf("finalize adjustment: changed=%v err=%v", changed, err)
	}
	revisions, err := store.ListAPIConsumerUsageStatementRevisions(ctx, accountID, appID, consumer.ID, start, start.Add(time.Hour))
	if err != nil || len(revisions) != 3 || revisions[0].Status != state.APIConsumerUsageStatementSuperseded ||
		revisions[1].Status != state.APIConsumerUsageStatementFinalized || revisions[2].ID != adjustment.ID {
		t.Fatalf("revisions = %+v err=%v", revisions, err)
	}
	for i, statement := range []state.APIConsumerUsageStatement{second, adjustment} {
		if _, created, err := store.CreateAPIConsumerUsageStatementHandoff(ctx, state.APIConsumerUsageStatementHandoffInput{
			AccountID: accountID, AppID: appID, ConsumerID: consumer.ID,
			StatementID: statement.ID, ExternalInvoiceID: "pg-revision-invoice-" + string(rune('a'+i)),
		}); err != nil || !created {
			t.Fatalf("handoff revision %d: created=%v err=%v", statement.Revision, created, err)
		}
	}
}
