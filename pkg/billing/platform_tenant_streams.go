package billing

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Tenant unit streams (ADR-1319): a platform tenant's cards of different
// units are parallel price histories, as an app's are (ADR-1317). Each
// stream prices its unit across every attributed subject from its first
// card, with its own included units, tiers, volume and fees.

// TenantUsageInputs are what tenant pricing reads beyond usage minutes.
type TenantUsageInputs struct {
	// Routes are subjects' route counts for request cards with route
	// weights (ADR-1307, ADR-1310).
	Routes TenantRouteUsage
	// Meters are subjects' app-reported units by meter (ADR-1319).
	Meters map[string][]state.PlatformTenantMeterUsageBucket
}

// TenantUnitStreams groups tenant cards by unit, each sorted by
// effective_from: request first, then units by name.
func TenantUnitStreams(cards []state.PlatformTenantRateCard) ([]string, map[string][]state.PlatformTenantRateCard) {
	streams := map[string][]state.PlatformTenantRateCard{}
	for _, card := range cards {
		unit := tenantCardUnit(card)
		streams[unit] = append(streams[unit], card)
	}
	units := make([]string, 0, len(streams))
	for unit, stream := range streams {
		streams[unit] = sortedTenantCards(stream)
		units = append(units, unit)
	}
	sort.Slice(units, func(i, j int) bool {
		if (units[i] == state.APIConsumerRateCardUnitRequest) != (units[j] == state.APIConsumerRateCardUnitRequest) {
			return units[i] == state.APIConsumerRateCardUnitRequest
		}
		return units[i] < units[j]
	})
	return units, streams
}

// tenantCardInForceIn reports whether any card matching pred is in force
// for a minute of [start, end), each stream's cards superseding only each
// other.
func tenantCardInForceIn(cards []state.PlatformTenantRateCard, start, end time.Time, pred func(state.PlatformTenantRateCard) bool) bool {
	_, streams := TenantUnitStreams(cards)
	for _, stream := range streams {
		for i, card := range stream {
			until := end
			if i+1 < len(stream) && stream[i+1].EffectiveFrom.Before(end) {
				until = stream[i+1].EffectiveFrom
			}
			if pred(card) && card.EffectiveFrom.Before(end) && until.After(start) {
				return true
			}
		}
	}
	return false
}

// tenantStreamCardsAt returns, for each stream, the card in force at
// minute; streams that have not started are absent.
func tenantStreamCardsAt(cards []state.PlatformTenantRateCard, minute time.Time) []state.PlatformTenantRateCard {
	units, streams := TenantUnitStreams(cards)
	var out []state.PlatformTenantRateCard
	for _, unit := range units {
		if card := tenantCardAt(streams[unit], minute); card != nil {
			out = append(out, *card)
		}
	}
	return out
}

func tenantCardsAsApp(cards []state.PlatformTenantRateCard) []state.APIConsumerRateCard {
	out := make([]state.APIConsumerRateCard, 0, len(cards))
	for _, card := range cards {
		out = append(out, state.APIConsumerRateCard{ID: card.ID, Currency: card.Currency, Unit: tenantCardUnit(card),
			PriceMillicentsPerUnit: card.PriceMillicentsPerUnit, IncludedUnitsPerMonth: card.IncludedUnitsPerMonth,
			Tiers: card.Tiers, TierMode: card.TierMode, EffectiveFrom: card.EffectiveFrom})
	}
	return out
}

// tenantStreamUsage converts subject minutes into a stream's units:
// route-weighted requests, rounded compute seconds or response megabytes
// (ADR-1310), or the subject's app-reported units for the stream's meter.
func tenantStreamUsage(unit string, cards []state.PlatformTenantRateCard, usage []state.APIConsumerUsageBucket,
	in TenantUsageInputs) ([]state.APIConsumerUsageBucket, error) {
	if meter, ok := state.AppMeterOfUnit(unit); ok {
		return tenantMeterUsage(usage, in.Meters[meter]), nil
	}
	weighted, err := weightTenantUsage(cards, usage, in.Routes)
	if err != nil {
		return nil, err
	}
	// Each subject minute rounds its own compute time or response size
	// (ADR-1310), as a consumer minute does (ADR-1308).
	return MeterAPIConsumerUsage(tenantCardsAsApp(cards), weighted), nil
}

// tenantMeterUsage replaces each subject minute's units with the subject's
// reported units for one meter; a minute without a report counts zero.
func tenantMeterUsage(usage []state.APIConsumerUsageBucket, rows []state.PlatformTenantMeterUsageBucket) []state.APIConsumerUsageBucket {
	byMinute := map[string]int64{}
	for _, row := range rows {
		byMinute[tenantMeterSubjectKey(row.AppID, row.SourceKind, row.SubjectKey)+"\x00"+row.WindowStart.UTC().Format(time.RFC3339)] += row.Units
	}
	out := make([]state.APIConsumerUsageBucket, 0, len(usage))
	for _, bucket := range usage {
		bucket.BillableUnits = byMinute[tenantSubjectRouteKey(bucket)+"\x00"+bucket.WindowStart.UTC().Format(time.RFC3339)]
		out = append(out, bucket)
	}
	return out
}

// tenantMeterSubjectKey keys a meter row's subject like tenantSubjectRouteKey.
func tenantMeterSubjectKey(appID, kind, subject string) string {
	if kind == "consumer" {
		return TenantRouteKey(appID, subject)
	}
	return TenantSourceRouteKey(appID, kind, subject)
}

// TenantMonth is everything one tenant month statement revision is priced
// from (ADR-975, ADR-1319, ADR-1322).
type TenantMonth struct {
	AccountID, TenantID string
	Start, End, AsOf    time.Time
	Revision            int
	PriorStatus         state.APIConsumerUsageStatementStatus
	// UsageDelta is usage not yet covered by finalized revisions;
	// MonthUsage all of the month's usage.
	UsageDelta, MonthUsage []state.APIConsumerUsageBucket
	CardsByApp             map[string][]state.APIConsumerRateCard
	TenantCards            []state.PlatformTenantRateCard
	Previous               []state.PlatformTenantStatement
	Inputs                 TenantUsageInputs
	// TenantCreated is when the tenant was created; zero bills no monthly
	// fees.
	TenantCreated time.Time
	Options       QuoteOptions
}

// tenantStatementFees returns what a revision bills in fees beyond the
// month's finalized revisions (ADR-1322): each stream's card in force at the
// tenant's fee instant charges its base fee and tops its stream's usage
// charge up to its minimum. streamUsage is the stream's charge for the
// whole month as priced now. A trial running at the fee instant, or
// starting the minute after, waives them.
func tenantStatementFees(m TenantMonth, streamUsage map[string]int64) []state.APIConsumerStatementFee {
	if m.TenantCreated.IsZero() || !IsCalendarMonth(m.Start, m.End) {
		return nil
	}
	at, ok := FeeInstant(m.Start, m.TenantCreated)
	if !ok || m.Options.inTrial(at) || m.Options.inTrial(at.Add(time.Minute)) {
		return nil
	}
	var finalized [][]state.APIConsumerStatementFee
	for _, statement := range m.Previous {
		if statement.Status == state.APIConsumerUsageStatementFinalized && statement.PeriodStart.Equal(m.Start) && statement.PeriodEnd.Equal(m.End) {
			finalized = append(finalized, statement.Fees)
		}
	}
	var inForce []feeCard
	for _, card := range tenantStreamCardsAt(m.TenantCards, at) {
		inForce = append(inForce, feeCard{id: card.ID, stream: tenantCardUnit(card),
			base: card.BaseFeeMillicentsPerMonth, minimum: card.MinimumMillicentsPerMonth,
			prorate: card.ProrateMonthlyFees, hasMonthlyCharge: card.MonthlyFees()})
	}
	return monthlyStatementFees(inForce, at, streamUsage, billedFees(finalized...))
}
