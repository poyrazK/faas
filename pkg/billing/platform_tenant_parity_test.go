package billing

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func tenantCard(unit string, price int64, from time.Time) state.PlatformTenantRateCard {
	return state.PlatformTenantRateCard{ID: uuid.NewString(), Currency: "EUR", Unit: unit, PriceMillicentsPerUnit: price, EffectiveFrom: from}
}

func lineAmounts(in state.PlatformTenantStatementInput) map[string]lineSummary {
	out := map[string]lineSummary{}
	for _, line := range in.Lines {
		got := out[line.PlatformTenantRateCardID]
		got.units += line.BillableUnits
		got.amount += line.AmountMillicents
		out[line.PlatformTenantRateCardID] = got
	}
	return out
}

// adr: 1319 — a tenant's request and compute cards price side by side, each
// across every subject; coverage stays in raw request units.
func TestTenantUnitStreamsPriceSideBySide(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	requests := tenantCard(state.PlatformTenantRateCardUnitRequest, 10, start)
	compute := tenantCard(state.APIConsumerRateCardUnitComputeSecond, 50, start)
	app := uuid.NewString()
	minute := tenantBucket(app, uuid.NewString(), start.Add(time.Minute), 3)
	minute.ComputeMS = 2000
	usage := []state.APIConsumerUsageBucket{minute}
	in, err := BuildTenantMonthStatement(TenantMonth{AccountID: "acct", TenantID: "tenant", Start: start, End: end, AsOf: end,
		Revision: 1, UsageDelta: usage, MonthUsage: usage, TenantCards: []state.PlatformTenantRateCard{requests, compute}})
	if err != nil {
		t.Fatal(err)
	}
	got := lineAmounts(in)
	if got[requests.ID] != (lineSummary{3, 30}) || got[compute.ID] != (lineSummary{2, 100}) || in.AmountMillicents != 130 {
		t.Fatalf("lines %+v amount %d, want 3 requests (30) and 2 compute seconds (100)", got, in.AmountMillicents)
	}
	if !in.Weighted || len(in.Coverage) != 1 || in.Coverage[0].BillableUnits != 3 {
		t.Fatalf("weighted %v coverage %+v, want weighted with 3 raw request units", in.Weighted, in.Coverage)
	}
	if !TenantMonthlyPricing([]state.PlatformTenantRateCard{requests, compute}, start, end) {
		t.Fatal("a compute stream must re-price whole months")
	}
}

// adr: 1319 — a tenant card prices the units an app reports for each
// subject, consumers and surfaces alike.
func TestTenantAppReportedUnits(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	tokens := tenantCard("app:tokens", 2, start)
	app, consumer, surface := uuid.NewString(), uuid.NewString(), uuid.NewString()
	m := start.Add(3 * time.Minute)
	usage := []state.APIConsumerUsageBucket{tenantBucket(app, consumer, m, 1),
		{AppID: app, SurfaceID: surface, WindowStart: m, RequestCount: 1, BillableUnits: 1}}
	meters := map[string][]state.PlatformTenantMeterUsageBucket{"tokens": {
		{AppID: app, SourceKind: "consumer", SubjectKey: consumer, WindowStart: m, Meter: "tokens", Units: 100},
		{AppID: app, SourceKind: "surface", SubjectKey: surface, WindowStart: m, Meter: "tokens", Units: 50},
	}}
	in, err := BuildTenantMonthStatement(TenantMonth{AccountID: "acct", TenantID: "tenant", Start: start, End: end, AsOf: end,
		Revision: 1, UsageDelta: usage, MonthUsage: usage, TenantCards: []state.PlatformTenantRateCard{tokens},
		Inputs: TenantUsageInputs{Meters: meters}})
	if err != nil {
		t.Fatal(err)
	}
	if got := lineAmounts(in)[tokens.ID]; got != (lineSummary{150, 300}) || len(in.Lines) != 2 {
		t.Fatalf("token lines %+v (%d lines), want 150 tokens = 300 on two subjects", got, len(in.Lines))
	}
}

// adr: 1322 — a tenant card charges its base fee once and tops usage up to
// its minimum; an idle tenant gets a fee-only revision, and late usage lowers
// the top-up already billed.
func TestTenantMonthlyFees(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	card := tenantCard(state.PlatformTenantRateCardUnitRequest, 10, start.AddDate(0, -1, 0))
	card.BaseFeeMillicentsPerMonth, card.MinimumMillicentsPerMonth = 5000, 1000
	created := start.AddDate(0, -2, 0)
	month := func(revision int, prior state.APIConsumerUsageStatementStatus, delta, all []state.APIConsumerUsageBucket,
		previous []state.PlatformTenantStatement) (state.PlatformTenantStatementInput, error) {
		return BuildTenantMonthStatement(TenantMonth{AccountID: "acct", TenantID: "tenant", Start: start, End: end, AsOf: end,
			Revision: revision, PriorStatus: prior, UsageDelta: delta, MonthUsage: all,
			TenantCards: []state.PlatformTenantRateCard{card}, Previous: previous, TenantCreated: created})
	}

	idle, err := month(1, "", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if idle.AmountMillicents != 6000 || len(idle.Fees) != 2 || len(idle.Lines) != 0 || idle.Currency != "EUR" || idle.BillableUnits != 0 {
		t.Fatalf("idle tenant = %+v, want base 5000 + minimum 1000 and no lines", idle)
	}

	app := uuid.NewString()
	first := []state.APIConsumerUsageBucket{tenantBucket(app, uuid.NewString(), start.Add(time.Minute), 3)}
	rev1, err := month(1, "", first, first, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rev1.AmountMillicents != 30+5000+970 {
		t.Fatalf("revision 1 = %d (%+v), want 30 usage + 5000 base + 970 top-up", rev1.AmountMillicents, rev1.Fees)
	}
	finalized := state.PlatformTenantStatement{PeriodStart: start, PeriodEnd: end, Revision: 1, Status: state.APIConsumerUsageStatementFinalized,
		Lines: rev1.Lines, Coverage: rev1.Coverage, Fees: rev1.Fees}

	late := []state.APIConsumerUsageBucket{tenantBucket(app, uuid.NewString(), start.Add(2*time.Minute), 50)}
	rev2, err := month(2, state.APIConsumerUsageStatementFinalized, late, append(append([]state.APIConsumerUsageBucket(nil), first...), late...),
		[]state.PlatformTenantStatement{finalized})
	if err != nil {
		t.Fatal(err)
	}
	if rev2.AmountMillicents != 0 || len(rev2.Fees) != 1 || rev2.Fees[0].Kind != state.StatementFeeMinimum || rev2.Fees[0].AmountMillicents != -500 {
		t.Fatalf("revision 2 = %d fees %+v, want +500 usage and -500 top-up", rev2.AmountMillicents, rev2.Fees)
	}

	if _, err := month(2, state.APIConsumerUsageStatementFinalized, nil, first, []state.PlatformTenantStatement{finalized}); !errors.Is(err, ErrNoNewTenantUsage) {
		t.Fatalf("billed month without new usage: %v, want ErrNoNewTenantUsage", err)
	}
}

// adr: 1322 — a tenant trial prices its minutes at zero on both pricing
// paths and waives the month's fees.
func TestTenantTrial(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	card := tenantCard(state.PlatformTenantRateCardUnitRequest, 10, start.AddDate(0, -1, 0))
	card.BaseFeeMillicentsPerMonth = 5000
	trial := QuoteOptions{TrialStart: start, TrialEnd: start.AddDate(0, 0, 14)}
	app := uuid.NewString()
	usage := []state.APIConsumerUsageBucket{tenantBucket(app, uuid.NewString(), start.Add(time.Minute), 4),
		tenantBucket(app, uuid.NewString(), start.AddDate(0, 0, 20), 2)}
	in, err := BuildTenantMonthStatement(TenantMonth{AccountID: "acct", TenantID: "tenant", Start: start, End: end, AsOf: end,
		Revision: 1, UsageDelta: usage, MonthUsage: usage, TenantCards: []state.PlatformTenantRateCard{card},
		TenantCreated: start.AddDate(0, -1, 0), Options: trial})
	if err != nil {
		t.Fatal(err)
	}
	if in.AmountMillicents != 20 || len(in.Fees) != 0 {
		t.Fatalf("trial month = %d fees %+v, want only the 2 requests after the trial", in.AmountMillicents, in.Fees)
	}

	flat := tenantCard(state.PlatformTenantRateCardUnitRequest, 10, start.AddDate(0, -1, 0))
	quote, err := QuotePlatformTenantUsage(nil, []state.PlatformTenantRateCard{flat}, usage, trial)
	if err != nil {
		t.Fatal(err)
	}
	if quote.AmountMillicents != 20 || !quote.Buckets[0].Trial || quote.Buckets[0].PlatformTenantRateCardID != flat.ID {
		t.Fatalf("per-minute trial quote = %+v, want trial minutes free with their card", quote)
	}
}

// adr: 1325 — a prorating card scales the first month's base fee and
// minimum to the share of the month left after creation.
func TestProrateMonthlyFee(t *testing.T) {
	september := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		amount int64
		at     time.Time
		want   int64
	}{
		{1000, september, 1000},
		{1000, september.AddDate(0, 0, 15), 500},
		{1000, september.AddDate(0, 0, 29).Add(23 * time.Hour), 1},
		{1, september.AddDate(0, 0, 20), 0},
	} {
		if got := ProrateMonthlyFee(tc.amount, tc.at); got != tc.want {
			t.Errorf("ProrateMonthlyFee(%d, %s) = %d, want %d", tc.amount, tc.at, got, tc.want)
		}
	}

	card := state.APIConsumerRateCard{ID: uuid.NewString(), Currency: "EUR", PriceMillicentsPerUnit: 1,
		BaseFeeMillicentsPerMonth: 3000, MinimumMillicentsPerMonth: 600, ProrateMonthlyFees: true, EffectiveFrom: september.AddDate(0, -1, 0)}
	fees := APIConsumerStatementFees([]state.APIConsumerRateCard{card}, september, september.AddDate(0, 1, 0),
		september.AddDate(0, 0, 20), QuoteOptions{}, nil, nil)
	if len(fees) != 2 || fees[0].AmountMillicents != 1000 || fees[1].AmountMillicents != 200 {
		t.Fatalf("prorated fees = %+v, want a third of 3000 and of 600", fees)
	}
	card.ProrateMonthlyFees = false
	fees = APIConsumerStatementFees([]state.APIConsumerRateCard{card}, september, september.AddDate(0, 1, 0),
		september.AddDate(0, 0, 20), QuoteOptions{}, nil, nil)
	if len(fees) != 2 || fees[0].AmountMillicents != 3000 {
		t.Fatalf("unprorated fees = %+v, want the whole month", fees)
	}
}

// adr: 1319, 1322 — tenant invoice items name their unit and carry fees.
func TestTenantStatementInvoiceUnitsAndFees(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	compute := tenantCard(state.APIConsumerRateCardUnitComputeSecond, 50, start)
	app := uuid.NewString()
	statement := state.PlatformTenantStatement{Currency: "EUR", AmountMillicents: 100 + 5000,
		Lines: []state.PlatformTenantStatementLine{{AppID: app, ConsumerID: uuid.NewString(), WindowStart: start, WindowEnd: start.Add(time.Minute),
			BillableUnits: 2, PlatformTenantRateCardID: compute.ID, Currency: "EUR", PriceMillicentsPerUnit: 50, AmountMillicents: 100}},
		Fees: []state.APIConsumerStatementFee{{Kind: state.StatementFeeBase, RateCardID: compute.ID, AmountMillicents: 5000}}}
	inv, err := TenantStatementInvoice(statement, compute)
	if err != nil {
		t.Fatal(err)
	}
	var unit, fee string
	for _, line := range inv.Lines {
		if line.Fee != "" {
			fee = line.Fee
		} else {
			unit = line.Unit
		}
	}
	if len(inv.Lines) != 2 || unit != state.APIConsumerRateCardUnitComputeSecond || fee != state.StatementFeeBase || inv.TotalMinor != 5 {
		t.Fatalf("invoice = %+v, want a compute line and a base fee line", inv)
	}
}
