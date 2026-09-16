package state

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAPIConsumerUsageStatementHandoffValidation(t *testing.T) {
	valid := APIConsumerUsageStatementHandoffInput{
		AccountID: uuid.NewString(), AppID: uuid.NewString(), ConsumerID: uuid.NewString(),
		StatementID: uuid.NewString(), ExternalInvoiceID: "invoice-1001",
	}
	tests := []struct {
		name  string
		input APIConsumerUsageStatementHandoffInput
	}{
		{name: "missing account", input: func() APIConsumerUsageStatementHandoffInput { in := valid; in.AccountID = ""; return in }()},
		{name: "missing external invoice", input: func() APIConsumerUsageStatementHandoffInput { in := valid; in.ExternalInvoiceID = ""; return in }()},
		{name: "oversized external invoice", input: func() APIConsumerUsageStatementHandoffInput {
			in := valid
			in.ExternalInvoiceID = strings.Repeat("x", maxAPIConsumerUsageStatementExternalInvoiceIDBytes+1)
			return in
		}()},
		{name: "external invoice whitespace", input: func() APIConsumerUsageStatementHandoffInput {
			in := valid
			in.ExternalInvoiceID = " invoice-1001"
			return in
		}()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateAPIConsumerUsageStatementHandoffInput(tt.input); err == nil {
				t.Fatal("validation unexpectedly succeeded")
			}
		})
	}
}

type apiConsumerUsageStatementHandoffScanRowStub struct {
	err error
}

func (r apiConsumerUsageStatementHandoffScanRowStub) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*string) = "handoff-id"
	*dest[1].(*string) = "account-id"
	*dest[2].(*string) = "app-id"
	*dest[3].(*string) = "consumer-id"
	*dest[4].(*string) = "statement-id"
	*dest[5].(*string) = "invoice-1001"
	*dest[6].(*string) = "EUR"
	*dest[7].(*int64) = 100
	*dest[8].(*time.Time) = time.Date(2026, 9, 1, 0, 0, 0, 0, time.FixedZone("test", 2*60*60))
	return nil
}

func TestScanAPIConsumerUsageStatementHandoffRow(t *testing.T) {
	got, err := scanAPIConsumerUsageStatementHandoffRow(apiConsumerUsageStatementHandoffScanRowStub{})
	if err != nil {
		t.Fatalf("scan handoff: %v", err)
	}
	if got.ID != "handoff-id" || got.ExternalInvoiceID != "invoice-1001" || got.Currency != "EUR" || got.AmountMillicents != 100 {
		t.Fatalf("scanned handoff = %+v", got)
	}
	if !got.CreatedAt.Equal(time.Date(2026, 8, 31, 22, 0, 0, 0, time.UTC)) {
		t.Fatalf("created_at = %v, want UTC-normalized timestamp", got.CreatedAt)
	}

	scanErr := errors.New("scan failed")
	if _, err := scanAPIConsumerUsageStatementHandoffRow(apiConsumerUsageStatementHandoffScanRowStub{err: scanErr}); !errors.Is(err, scanErr) {
		t.Fatalf("scan handoff error = %v, want %v", err, scanErr)
	}
}

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
