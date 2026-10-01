package billing

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 238
func TestBuildPlatformTenantStatementCrossAppAdjustments(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	appA, appB, consumerA, consumerB := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	usage := []state.APIConsumerUsageBucket{
		{AppID: appB, ConsumerKey: consumerB, WindowStart: start, BillableUnits: 2},
		{AppID: appA, ConsumerKey: consumerA, WindowStart: start, BillableUnits: 3},
	}
	cards := map[string][]state.APIConsumerRateCard{
		appA: {{ID: uuid.NewString(), AppID: appA, Currency: "EUR", Unit: state.APIConsumerRateCardUnitRequest, PriceMillicentsPerUnit: 10, EffectiveFrom: start}},
		appB: {{ID: uuid.NewString(), AppID: appB, Currency: "EUR", Unit: state.APIConsumerRateCardUnitRequest, PriceMillicentsPerUnit: 25, EffectiveFrom: start}},
	}
	first, err := BuildPlatformTenantStatement(uuid.NewString(), uuid.NewString(), start, start.Add(time.Hour), start.Add(time.Hour), usage, cards, nil, nil)
	if err != nil || first.Revision != 1 || first.Currency != "EUR" || first.BillableUnits != 5 || first.AmountMillicents != 80 || len(first.Lines) != 2 {
		t.Fatalf("initial cross-app statement = %+v, err=%v", first, err)
	}
	prior := []state.PlatformTenantStatement{{Revision: 1, Status: state.APIConsumerUsageStatementFinalized, Lines: first.Lines}}
	if _, err := BuildPlatformTenantStatement(first.AccountID, first.TenantID, start, start.Add(time.Hour), start.Add(time.Hour), usage, cards, nil, prior); !errors.Is(err, ErrNoNewTenantUsage) {
		t.Fatalf("unchanged usage err=%v, want no new usage", err)
	}
	usage[0].BillableUnits = 4
	adjustment, err := BuildPlatformTenantStatement(first.AccountID, first.TenantID, start, start.Add(time.Hour), start.Add(time.Hour), usage, cards, nil, prior)
	if err != nil || adjustment.Revision != 2 || adjustment.BillableUnits != 2 || adjustment.AmountMillicents != 50 || len(adjustment.Lines) != 1 || adjustment.Lines[0].AppID != appB {
		t.Fatalf("late-usage adjustment = %+v, err=%v", adjustment, err)
	}
	fromDelta, err := BuildPlatformTenantStatementFromDelta(first.AccountID, first.TenantID, start, start.Add(time.Hour),
		start.Add(time.Hour), 2, state.APIConsumerUsageStatementFinalized,
		[]state.APIConsumerUsageBucket{{AppID: appB, ConsumerKey: consumerB, WindowStart: start, BillableUnits: 2}}, cards, nil)
	if err != nil || fromDelta.BillableUnits != adjustment.BillableUnits || fromDelta.AmountMillicents != adjustment.AmountMillicents ||
		len(fromDelta.Coverage) != 1 || fromDelta.Coverage[0].BillableUnits != 2 {
		t.Fatalf("delta builder result = %+v, err=%v", fromDelta, err)
	}
	usage[0].BillableUnits = 1
	if _, err := BuildPlatformTenantStatement(first.AccountID, first.TenantID, start, start.Add(time.Hour), start.Add(time.Hour), usage, cards, nil, prior); !errors.Is(err, ErrTenantUsageRegressed) {
		t.Fatalf("regressed usage err=%v", err)
	}
	cards[appB][0].Currency = "USD"
	usage[0].BillableUnits = 2
	if _, err := BuildPlatformTenantStatement(first.AccountID, first.TenantID, start, start.Add(time.Hour), start.Add(time.Hour), usage, cards, nil, nil); !errors.Is(err, ErrMixedTenantCurrency) {
		t.Fatalf("mixed currencies err=%v", err)
	}
}

// adr: 239
func TestBuildPlatformTenantStatementSeparatesConsumerAndSurfaceMinutes(t *testing.T) {
	start := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	accountID, tenantID, appID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	consumerID, surfaceID := uuid.NewString(), uuid.NewString()
	usage := []state.APIConsumerUsageBucket{
		{AppID: appID, ConsumerKey: consumerID, WindowStart: start, BillableUnits: 2},
		{AppID: appID, SurfaceID: surfaceID, WindowStart: start, BillableUnits: 3},
	}
	cards := map[string][]state.APIConsumerRateCard{appID: {{ID: uuid.NewString(), AppID: appID,
		Currency: "EUR", Unit: state.APIConsumerRateCardUnitRequest, PriceMillicentsPerUnit: 7, EffectiveFrom: start}}}
	first, err := BuildPlatformTenantStatement(accountID, tenantID, start, start.Add(time.Hour), start.Add(time.Hour), usage, cards, nil, nil)
	if err != nil || first.BillableUnits != 5 || first.AmountMillicents != 35 || len(first.Lines) != 2 {
		t.Fatalf("mixed-source statement = %+v, err=%v", first, err)
	}
	var sawConsumer, sawSurface bool
	for _, line := range first.Lines {
		sawConsumer = sawConsumer || line.ConsumerID == consumerID && line.SurfaceID == ""
		sawSurface = sawSurface || line.SurfaceID == surfaceID && line.ConsumerID == ""
	}
	if !sawConsumer || !sawSurface {
		t.Fatalf("source identities lost: %+v", first.Lines)
	}
	prior := []state.PlatformTenantStatement{{Revision: 1, Status: state.APIConsumerUsageStatementFinalized, Lines: first.Lines}}
	usage[1].BillableUnits = 5
	adjustment, err := BuildPlatformTenantStatement(accountID, tenantID, start, start.Add(time.Hour), start.Add(time.Hour), usage, cards, nil, prior)
	if err != nil || adjustment.BillableUnits != 2 || adjustment.AmountMillicents != 14 || len(adjustment.Lines) != 1 || adjustment.Lines[0].SurfaceID != surfaceID {
		t.Fatalf("surface adjustment = %+v, err=%v", adjustment, err)
	}
}

func TestBuildPlatformTenantStatementCompactsLinesButRetainsMinuteCoverage(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(31 * 24 * time.Hour)
	accountID, tenantID := uuid.NewString(), uuid.NewString()
	apps := []string{uuid.NewString(), uuid.NewString()}
	consumers := []string{uuid.NewString(), uuid.NewString()}
	cards := map[string][]state.APIConsumerRateCard{}
	usage := make([]state.APIConsumerUsageBucket, 0, 31*24*60*len(apps))
	for appIndex, appID := range apps {
		cards[appID] = []state.APIConsumerRateCard{{ID: uuid.NewString(), AppID: appID,
			Currency: "EUR", Unit: state.APIConsumerRateCardUnitRequest,
			PriceMillicentsPerUnit: int64(appIndex + 1), EffectiveFrom: start}}
	}
	for minute := start; minute.Before(end); minute = minute.Add(time.Minute) {
		for appIndex, appID := range apps {
			usage = append(usage, state.APIConsumerUsageBucket{AppID: appID, ConsumerKey: consumers[appIndex],
				WindowStart: minute, BillableUnits: 1})
		}
	}

	first, err := BuildPlatformTenantStatement(accountID, tenantID, start, end, end, usage, cards, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	const expectedMinutes = 31 * 24 * 60 * 2
	if first.BillableUnits != int64(expectedMinutes) || len(first.Coverage) != expectedMinutes ||
		len(first.Lines) != len(apps) || first.AmountMillicents != 31*24*60*3 {
		t.Fatalf("large statement totals/shape: units=%d coverage=%d lines=%d amount=%d",
			first.BillableUnits, len(first.Coverage), len(first.Lines), first.AmountMillicents)
	}
	for _, line := range first.Lines {
		if !line.WindowStart.Equal(start) || !line.WindowEnd.Equal(end) || line.BillableUnits != 31*24*60 {
			t.Fatalf("compacted invoice line lost its minute span: %+v", line)
		}
	}

	usage[0].BillableUnits++ // Late units in a minute already covered by revision 1.
	prior := []state.PlatformTenantStatement{{Revision: first.Revision, Status: state.APIConsumerUsageStatementFinalized,
		Lines: first.Lines, Coverage: first.Coverage}}
	adjustment, err := BuildPlatformTenantStatement(accountID, tenantID, start, end, end.Add(time.Hour), usage, cards, nil, prior)
	if err != nil {
		t.Fatal(err)
	}
	if adjustment.BillableUnits != 1 || len(adjustment.Coverage) != 1 || len(adjustment.Lines) != 1 ||
		adjustment.Lines[0].AppID != apps[0] || adjustment.Lines[0].BillableUnits != 1 ||
		!adjustment.Lines[0].WindowStart.Equal(start) || !adjustment.Lines[0].WindowEnd.Equal(start.Add(time.Minute)) {
		t.Fatalf("late minute adjustment = %+v", adjustment)
	}
}
