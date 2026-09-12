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
