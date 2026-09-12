package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemAPIConsumerUsageStatementsAreImmutableAndIdempotent(t *testing.T) {
	m := NewMemStore()
	accountID, appID, consumerID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	rateCardID := uuid.NewString()
	input := APIConsumerUsageStatementInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumerID,
		PeriodStart: start, PeriodEnd: end, Currency: "eur",
		BillableUnits: 2, AmountMillicents: 20, Priced: true, AsOf: time.Now().UTC(),
		Buckets: []APIConsumerUsageStatementBucket{{WindowStart: start, BillableUnits: 2, RateCardID: rateCardID, Currency: "EUR", PriceMillicentsPerUnit: 10, AmountMillicents: 20}},
	}
	first, created, err := m.CreateAPIConsumerUsageStatement(context.Background(), input)
	if err != nil || !created {
		t.Fatalf("create: statement=%+v created=%v err=%v", first, created, err)
	}
	second, created, err := m.CreateAPIConsumerUsageStatement(context.Background(), input)
	if err != nil || created || second.ID != first.ID || second.AmountMillicents != first.AmountMillicents {
		t.Fatalf("duplicate: statement=%+v created=%v err=%v", second, created, err)
	}
	first.Buckets[0].AmountMillicents = 999
	got, err := m.GetAPIConsumerUsageStatement(context.Background(), accountID, appID, consumerID, first.ID)
	if err != nil || got.Buckets[0].AmountMillicents != 20 {
		t.Fatalf("snapshot mutated through returned slice: %+v err=%v", got, err)
	}
	finalized, changed, err := m.FinalizeAPIConsumerUsageStatement(context.Background(), accountID, appID, consumerID, first.ID)
	if err != nil || !changed || finalized.Status != APIConsumerUsageStatementFinalized || finalized.FinalizedAt == nil {
		t.Fatalf("finalize: %+v changed=%v err=%v", finalized, changed, err)
	}
	replayed, changed, err := m.FinalizeAPIConsumerUsageStatement(context.Background(), accountID, appID, consumerID, first.ID)
	if err != nil || changed || replayed.ID != first.ID {
		t.Fatalf("finalize replay: %+v changed=%v err=%v", replayed, changed, err)
	}
}

func TestMemAPIConsumerUsageStatementRejectsUnpricedFinalize(t *testing.T) {
	m := NewMemStore()
	accountID, appID, consumerID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	statement, _, err := m.CreateAPIConsumerUsageStatement(context.Background(), APIConsumerUsageStatementInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumerID, PeriodStart: start, PeriodEnd: start.Add(time.Hour),
		BillableUnits: 1, UnpricedUnits: 1, AsOf: time.Now().UTC(),
		Buckets: []APIConsumerUsageStatementBucket{{WindowStart: start, BillableUnits: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, changed, err := m.FinalizeAPIConsumerUsageStatement(context.Background(), accountID, appID, consumerID, statement.ID); !errors.Is(err, ErrConflict) || changed {
		t.Fatalf("expected unpriced conflict, changed=%v err=%v", changed, err)
	}
}

func TestMemAPIConsumerUsageStatementHandoffIsIdempotentAndImmutable(t *testing.T) {
	m := NewMemStore()
	accountID, appID, consumerID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	statement, _, err := m.CreateAPIConsumerUsageStatement(context.Background(), APIConsumerUsageStatementInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumerID, PeriodStart: start, PeriodEnd: start.Add(time.Hour),
		Currency: "EUR", BillableUnits: 4, AmountMillicents: 100, Priced: true, AsOf: time.Now().UTC(),
		Buckets: []APIConsumerUsageStatementBucket{{WindowStart: start, BillableUnits: 4, RateCardID: uuid.NewString(), Currency: "EUR", PriceMillicentsPerUnit: 25, AmountMillicents: 100}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.CreateAPIConsumerUsageStatementHandoff(context.Background(), APIConsumerUsageStatementHandoffInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumerID, StatementID: statement.ID, ExternalInvoiceID: "inv-before-finalize",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("draft handoff err=%v, want ErrConflict", err)
	}
	if _, _, err := m.FinalizeAPIConsumerUsageStatement(context.Background(), accountID, appID, consumerID, statement.ID); err != nil {
		t.Fatal(err)
	}
	input := APIConsumerUsageStatementHandoffInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumerID, StatementID: statement.ID, ExternalInvoiceID: "inv-1001",
	}
	first, created, err := m.CreateAPIConsumerUsageStatementHandoff(context.Background(), input)
	if err != nil || !created || first.AmountMillicents != 100 || first.Currency != "EUR" {
		t.Fatalf("claim: handoff=%+v created=%v err=%v", first, created, err)
	}
	replay, created, err := m.CreateAPIConsumerUsageStatementHandoff(context.Background(), input)
	if err != nil || created || replay.ID != first.ID {
		t.Fatalf("claim replay: handoff=%+v created=%v err=%v", replay, created, err)
	}
	if _, _, err := m.CreateAPIConsumerUsageStatementHandoff(context.Background(), APIConsumerUsageStatementHandoffInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumerID, StatementID: statement.ID, ExternalInvoiceID: "inv-1002",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("statement reuse err=%v, want ErrConflict", err)
	}
	other, _, err := m.CreateAPIConsumerUsageStatement(context.Background(), APIConsumerUsageStatementInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumerID, PeriodStart: start.Add(2 * time.Hour), PeriodEnd: start.Add(3 * time.Hour),
		Currency: "EUR", BillableUnits: 1, AmountMillicents: 25, Priced: true, AsOf: time.Now().UTC(),
		Buckets: []APIConsumerUsageStatementBucket{{WindowStart: start.Add(2 * time.Hour), BillableUnits: 1, RateCardID: uuid.NewString(), Currency: "EUR", PriceMillicentsPerUnit: 25, AmountMillicents: 25}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.FinalizeAPIConsumerUsageStatement(context.Background(), accountID, appID, consumerID, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.CreateAPIConsumerUsageStatementHandoff(context.Background(), APIConsumerUsageStatementHandoffInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumerID, StatementID: other.ID, ExternalInvoiceID: input.ExternalInvoiceID,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("external invoice reuse err=%v, want ErrConflict", err)
	}
	got, err := m.GetAPIConsumerUsageStatementHandoff(context.Background(), accountID, appID, consumerID, statement.ID)
	if err != nil || got.ID != first.ID || got.ExternalInvoiceID != input.ExternalInvoiceID {
		t.Fatalf("get handoff: %+v err=%v", got, err)
	}
}
