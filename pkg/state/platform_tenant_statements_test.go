package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemPlatformTenantStatementExpectedPriorStatus(t *testing.T) {
	m := state.NewMemStore()
	accountID, tenantID, appID, consumerID, rateID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	(*m.PlatformTenants())[tenantID] = state.PlatformTenant{ID: tenantID, AccountID: accountID}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	in := state.PlatformTenantStatementInput{AccountID: accountID, TenantID: tenantID,
		PeriodStart: start, PeriodEnd: start.Add(time.Hour), Revision: 1, Currency: "EUR",
		BillableUnits: 2, AmountMillicents: 20, AsOf: time.Now().UTC(),
		Lines: []state.PlatformTenantStatementLine{{AppID: appID, ConsumerID: consumerID, WindowStart: start,
			BillableUnits: 2, RateCardID: rateID, Currency: "EUR", PriceMillicentsPerUnit: 10, AmountMillicents: 20}},
	}
	first, created, err := m.CreatePlatformTenantStatement(context.Background(), in)
	if err != nil || !created {
		t.Fatalf("initial statement: %v, %v", created, err)
	}
	if _, _, err := m.FinalizePlatformTenantStatement(context.Background(), accountID, tenantID, first.ID); err != nil {
		t.Fatal(err)
	}
	in.Revision = 2
	in.PriorStatus = state.APIConsumerUsageStatementDraft // stale snapshot read before finalization
	if _, _, err := m.CreatePlatformTenantStatement(context.Background(), in); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale prior status err=%v", err)
	}
	in.PriorStatus = state.APIConsumerUsageStatementFinalized
	if _, created, err := m.CreatePlatformTenantStatement(context.Background(), in); err != nil || !created {
		t.Fatalf("fresh adjustment: %v, %v", created, err)
	}
}

func TestMemPlatformTenantStatementPlanReturnsOnlyUncoveredUsage(t *testing.T) {
	m := state.NewMemStore()
	accountID, tenantID, appID, consumerID, rateCardID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	(*m.PlatformTenants())[tenantID] = state.PlatformTenant{ID: tenantID, AccountID: accountID}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	usageKey := state.PlatformTenantUsageBucketKey(accountID, tenantID, appID, consumerID, start)
	(*m.PlatformTenantUsage())[usageKey] = state.APIConsumerUsageBucket{AccountID: accountID, AppID: appID,
		ConsumerKey: consumerID, WindowStart: start, RequestCount: 2, BillableUnits: 2}

	plan, err := m.PlanPlatformTenantStatement(context.Background(), accountID, tenantID, start, end)
	if err != nil || plan.HasLatest || len(plan.UsageDelta) != 1 || plan.UsageDelta[0].BillableUnits != 2 {
		t.Fatalf("initial usage plan = %+v, err=%v", plan, err)
	}
	firstInput := state.PlatformTenantStatementInput{AccountID: accountID, TenantID: tenantID,
		PeriodStart: start, PeriodEnd: end, Revision: 1, Currency: "EUR", BillableUnits: 2,
		AmountMillicents: 20, AsOf: end,
		Lines: []state.PlatformTenantStatementLine{{AppID: appID, ConsumerID: consumerID, WindowStart: start,
			WindowEnd: start.Add(time.Minute), BillableUnits: 2, RateCardID: rateCardID,
			Currency: "EUR", PriceMillicentsPerUnit: 10, AmountMillicents: 20}},
		Coverage: []state.PlatformTenantStatementCoverage{{AppID: appID, ConsumerID: consumerID, WindowStart: start, BillableUnits: 2}},
	}
	first, created, err := m.CreatePlatformTenantStatement(context.Background(), firstInput)
	if err != nil || !created {
		t.Fatalf("create first statement: created=%t err=%v", created, err)
	}
	if _, changed, err := m.FinalizePlatformTenantStatement(context.Background(), accountID, tenantID, first.ID); err != nil || !changed {
		t.Fatalf("finalize first statement: changed=%t err=%v", changed, err)
	}
	if replayed, changed, err := m.FinalizePlatformTenantStatement(context.Background(), accountID, tenantID, first.ID); err != nil || changed || replayed.Coverage != nil {
		t.Fatalf("finalize replay: changed=%t coverage=%d err=%v", changed, len(replayed.Coverage), err)
	}

	plan, err = m.PlanPlatformTenantStatement(context.Background(), accountID, tenantID, start, end)
	if err != nil || !plan.HasLatest || len(plan.UsageDelta) != 0 || plan.Latest.Coverage != nil {
		t.Fatalf("fully covered usage plan = %+v, err=%v", plan, err)
	}
	usage := (*m.PlatformTenantUsage())[usageKey]
	usage.RequestCount++
	usage.BillableUnits++
	(*m.PlatformTenantUsage())[usageKey] = usage
	plan, err = m.PlanPlatformTenantStatement(context.Background(), accountID, tenantID, start, end)
	if err != nil || len(plan.UsageDelta) != 1 || plan.UsageDelta[0].BillableUnits != 1 || plan.Latest.Revision != 1 {
		t.Fatalf("late-unit usage plan = %+v, err=%v", plan, err)
	}

	usage.BillableUnits = 1
	(*m.PlatformTenantUsage())[usageKey] = usage
	if _, err := m.PlanPlatformTenantStatement(context.Background(), accountID, tenantID, start, end); !errors.Is(err, state.ErrPlatformTenantUsageRegressed) {
		t.Fatalf("regressed usage plan err=%v, want ErrPlatformTenantUsageRegressed", err)
	}
	delete((*m.PlatformTenantUsage()), usageKey)
	if _, err := m.PlanPlatformTenantStatement(context.Background(), accountID, tenantID, start, end); !errors.Is(err, state.ErrPlatformTenantUsageRegressed) {
		t.Fatalf("missing usage plan err=%v, want ErrPlatformTenantUsageRegressed", err)
	}
}

func TestValidatePlatformTenantStatementTenantRateCardLine(t *testing.T) {
	accountID, tenantID, appID, consumerID, rateCardID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	start := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	input := state.PlatformTenantStatementInput{AccountID: accountID, TenantID: tenantID,
		PeriodStart: start, PeriodEnd: start.Add(time.Hour), Revision: 1, Currency: "EUR",
		BillableUnits: 2, AmountMillicents: 50, AsOf: start.Add(time.Hour),
		Lines: []state.PlatformTenantStatementLine{{AppID: appID, ConsumerID: consumerID, WindowStart: start,
			BillableUnits: 2, PlatformTenantRateCardID: rateCardID, Currency: "EUR",
			PriceMillicentsPerUnit: 25, AmountMillicents: 50}},
	}
	if err := state.ValidatePlatformTenantStatementInput(input); err != nil {
		t.Fatalf("tenant-rate-card statement line: %v", err)
	}
	input.Lines[0].RateCardID = uuid.NewString()
	if err := state.ValidatePlatformTenantStatementInput(input); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("both rate-card references = %v, want ErrInvalidArgument", err)
	}
}

func TestValidatePlatformTenantCompactStatementCoverage(t *testing.T) {
	accountID, tenantID, appID, consumerID, rateCardID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	start := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	input := state.PlatformTenantStatementInput{AccountID: accountID, TenantID: tenantID,
		PeriodStart: start, PeriodEnd: start.Add(time.Hour), Revision: 1, Currency: "EUR",
		BillableUnits: 3, AmountMillicents: 15, AsOf: start.Add(time.Hour),
		Lines: []state.PlatformTenantStatementLine{{AppID: appID, ConsumerID: consumerID, WindowStart: start,
			WindowEnd: start.Add(3 * time.Minute), BillableUnits: 3, RateCardID: rateCardID, Currency: "EUR",
			PriceMillicentsPerUnit: 5, AmountMillicents: 15}},
		Coverage: []state.PlatformTenantStatementCoverage{
			{AppID: appID, ConsumerID: consumerID, WindowStart: start, BillableUnits: 1},
			{AppID: appID, ConsumerID: consumerID, WindowStart: start.Add(2 * time.Minute), BillableUnits: 2},
		},
	}
	if err := state.ValidatePlatformTenantStatementInput(input); err != nil {
		t.Fatalf("valid compact statement with a minute gap: %v", err)
	}
	withoutCoverage := input
	withoutCoverage.Coverage = nil
	if err := state.ValidatePlatformTenantStatementInput(withoutCoverage); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("compact statement without exact coverage = %v, want ErrInvalidArgument", err)
	}
	input.Coverage = append(input.Coverage, input.Coverage[0])
	if err := state.ValidatePlatformTenantStatementInput(input); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("duplicate exact minute coverage = %v, want ErrInvalidArgument", err)
	}
}

func TestValidatePlatformTenantStatementBeyondFormerMinuteLimit(t *testing.T) {
	accountID, tenantID, appID, consumerID, rateCardID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	const minuteCount = 20001
	end := start.Add(minuteCount * time.Minute)
	input := state.PlatformTenantStatementInput{AccountID: accountID, TenantID: tenantID,
		PeriodStart: start, PeriodEnd: end, Revision: 1, Currency: "EUR",
		BillableUnits: minuteCount, AmountMillicents: minuteCount, AsOf: end,
		Lines: []state.PlatformTenantStatementLine{{AppID: appID, ConsumerID: consumerID, WindowStart: start,
			WindowEnd: end, BillableUnits: minuteCount, RateCardID: rateCardID, Currency: "EUR",
			PriceMillicentsPerUnit: 1, AmountMillicents: minuteCount}},
		Coverage: make([]state.PlatformTenantStatementCoverage, 0, minuteCount),
	}
	for minute := 0; minute < minuteCount; minute++ {
		input.Coverage = append(input.Coverage, state.PlatformTenantStatementCoverage{
			AppID: appID, ConsumerID: consumerID, WindowStart: start.Add(time.Duration(minute) * time.Minute), BillableUnits: 1,
		})
	}
	if err := state.ValidatePlatformTenantStatementInput(input); err != nil {
		t.Fatalf("statement above former 20,000-minute ceiling: %v", err)
	}
}
