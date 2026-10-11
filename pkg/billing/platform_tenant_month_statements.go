package billing

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// ErrTenantMonthPeriodRequired reports a statement whose period a tenant
// card with a monthly allowance or tiers prices but that is not exactly one
// UTC calendar month (ADR-975).
var ErrTenantMonthPeriodRequired = errors.New("platform tenant statements priced by a monthly allowance or tiers must cover one UTC calendar month")

// ErrTenantChargeDecreased reports that re-pricing a month would bill less
// than its finalized revisions did. Gregale never issues credits.
var ErrTenantChargeDecreased = errors.New("platform tenant charges would fall below a finalized statement")

// ErrTenantMonthOverlap reports a month whose minutes a finalized statement
// for a different period already billed, so re-pricing the month would
// bill them twice.
var ErrTenantMonthOverlap = errors.New("platform tenant month overlaps a finalized statement for another period")

// TenantMonthlyPricing reports whether a tenant card with a monthly
// allowance or tiers is effective for any minute of [start, end).
func TenantMonthlyPricing(cards []state.PlatformTenantRateCard, start, end time.Time) bool {
	ordered := sortedTenantCards(cards)
	for i, card := range ordered {
		until := end
		if i+1 < len(ordered) && ordered[i+1].EffectiveFrom.Before(end) {
			until = ordered[i+1].EffectiveFrom
		}
		if card.MonthlyPricing() && card.EffectiveFrom.Before(end) && until.After(start) {
			return true
		}
	}
	return false
}

func sortedTenantCards(cards []state.PlatformTenantRateCard) []state.PlatformTenantRateCard {
	ordered := append([]state.PlatformTenantRateCard(nil), cards...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].EffectiveFrom.Equal(ordered[j].EffectiveFrom) {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].EffectiveFrom.Before(ordered[j].EffectiveFrom)
	})
	return ordered
}

// tenantLine accumulates one statement line: a subject, a pricing source,
// and one unit price.
type tenantLine struct {
	line state.PlatformTenantStatementLine
}

func (l *tenantLine) add(units, amount int64, minute time.Time) error {
	if !addsWithinInt64(l.line.BillableUnits, units) || !addsWithinInt64(l.line.AmountMillicents, amount) {
		return fmt.Errorf("platform tenant invoice line overflow")
	}
	l.line.BillableUnits += units
	l.line.AmountMillicents += amount
	if l.line.WindowStart.IsZero() || minute.Before(l.line.WindowStart) {
		l.line.WindowStart = minute
	}
	if end := minute.Add(time.Minute); l.line.WindowEnd.IsZero() || end.After(l.line.WindowEnd) {
		l.line.WindowEnd = end
	}
	return nil
}

func addsWithinInt64(total, delta int64) bool {
	if delta >= 0 {
		return total <= maxInt64-delta
	}
	return total >= -maxInt64-delta
}

// priceTenantMonth prices a whole UTC month of tenant-attributed usage into
// statement lines keyed like tenantStatementLineKey. Tenant cards count their
// allowance and tiers across every subject in minute order, ties broken by
// subject, so the result is deterministic. Minutes before the tenant's first
// card fall back to flat app cards. A line is split per unit price, so free
// allowance units and each ladder step become their own lines and every line
// stays exactly units × price.
func priceTenantMonth(cardsByApp map[string][]state.APIConsumerRateCard, tenantCards []state.PlatformTenantRateCard,
	usage []state.APIConsumerUsageBucket) (map[string]*tenantLine, string, error) {
	ordered := append([]state.APIConsumerUsageBucket(nil), usage...)
	sort.Slice(ordered, func(i, j int) bool {
		if !ordered[i].WindowStart.Equal(ordered[j].WindowStart) {
			return ordered[i].WindowStart.Before(ordered[j].WindowStart)
		}
		return tenantUsageKey(ordered[i].AppID, ordered[i].ConsumerKey, ordered[i].SurfaceID, ordered[i].JWTAuthorizationRuleID, ordered[i].WindowStart) <
			tenantUsageKey(ordered[j].AppID, ordered[j].ConsumerKey, ordered[j].SurfaceID, ordered[j].JWTAuthorizationRuleID, ordered[j].WindowStart)
	})
	cards := sortedTenantCards(tenantCards)
	asApp := make([]state.APIConsumerRateCard, 0, len(cards))
	tiersByCard := map[string][]state.APIConsumerRateCardTier{}
	for _, card := range cards {
		if card.Unit != state.PlatformTenantRateCardUnitRequest {
			return nil, "", fmt.Errorf("billing: unsupported platform tenant rate-card unit %q", card.Unit)
		}
		asApp = append(asApp, state.APIConsumerRateCard{ID: card.ID, Currency: card.Currency, Unit: state.APIConsumerRateCardUnitRequest,
			PriceMillicentsPerUnit: card.PriceMillicentsPerUnit, IncludedUnitsPerMonth: card.IncludedUnitsPerMonth,
			Tiers: card.Tiers, EffectiveFrom: card.EffectiveFrom})
		tiersByCard[card.ID] = card.Tiers
	}
	var tenantUsage []state.APIConsumerUsageBucket
	fallbackByApp := map[string][]state.APIConsumerUsageBucket{}
	for _, bucket := range ordered {
		if len(cards) > 0 && !cards[0].EffectiveFrom.After(bucket.WindowStart.UTC()) {
			tenantUsage = append(tenantUsage, bucket)
		} else {
			fallbackByApp[bucket.AppID] = append(fallbackByApp[bucket.AppID], bucket)
		}
	}
	lines := map[string]*tenantLine{}
	currency := ""
	record := func(bucket state.APIConsumerUsageBucket, priced APIConsumerUsageChargeBucket, tenant bool) error {
		if priced.Currency != "" {
			if currency != "" && currency != priced.Currency {
				return ErrMixedTenantCurrency
			}
			currency = priced.Currency
		}
		appCardID, tenantCardID := priced.RateCardID, ""
		if tenant {
			appCardID, tenantCardID = "", priced.RateCardID
		}
		add := func(units, price int64) error {
			if units == 0 {
				return nil
			}
			amount, err := multiplyMillicents(units, price)
			if err != nil {
				return err
			}
			cur := priced.Currency
			if appCardID == "" && tenantCardID == "" {
				cur, price, amount = "", 0, 0
			}
			key := tenantStatementLineKey(bucket.AppID, bucket.ConsumerKey, bucket.SurfaceID, bucket.JWTAuthorizationRuleID,
				appCardID, tenantCardID, cur, price)
			line := lines[key]
			if line == nil {
				line = &tenantLine{line: state.PlatformTenantStatementLine{AppID: bucket.AppID, ConsumerID: bucket.ConsumerKey,
					SurfaceID: bucket.SurfaceID, JWTAuthorizationRuleID: bucket.JWTAuthorizationRuleID,
					RateCardID: appCardID, PlatformTenantRateCardID: tenantCardID, Currency: cur, PriceMillicentsPerUnit: price}}
				lines[key] = line
			}
			return line.add(units, amount, bucket.WindowStart.UTC())
		}
		if priced.TierUnits != nil {
			tiers := tiersByCard[tenantCardID]
			for i, units := range priced.TierUnits {
				if err := add(units, tiers[i].PriceMillicentsPerUnit); err != nil {
					return err
				}
			}
			return nil
		}
		if err := add(priced.BillableUnits-priced.ChargedUnits, 0); err != nil {
			return err
		}
		return add(priced.ChargedUnits, priced.PriceMillicentsPerUnit)
	}
	if len(tenantUsage) > 0 {
		quote, err := QuoteAPIConsumerUsageFrom(asApp, tenantUsage, time.Time{})
		if err != nil {
			return nil, "", err
		}
		for i, priced := range quote.Buckets {
			if err := record(tenantUsage[i], priced, true); err != nil {
				return nil, "", err
			}
		}
	}
	apps := make([]string, 0, len(fallbackByApp))
	for appID := range fallbackByApp {
		apps = append(apps, appID)
	}
	sort.Strings(apps)
	for _, appID := range apps {
		if err := requireFlatAppCards(cardsByApp[appID]); err != nil {
			return nil, "", err
		}
		quote, err := QuoteAPIConsumerUsage(cardsByApp[appID], fallbackByApp[appID])
		if err != nil {
			if errors.Is(err, ErrMixedAPIConsumerRateCardCurrency) {
				return nil, "", ErrMixedTenantCurrency
			}
			return nil, "", err
		}
		for i, priced := range quote.Buckets {
			if err := record(fallbackByApp[appID][i], priced, false); err != nil {
				return nil, "", err
			}
		}
	}
	return lines, currency, nil
}

// BuildPlatformTenantMonthStatement re-prices a whole UTC month (ADR-975)
// and bills the difference from every finalized revision's lines. Coverage
// still records the minute units not yet finalized, so regression checks are
// unchanged; the lines carry re-rated amounts, which may be negative per line
// but never in total.
func BuildPlatformTenantMonthStatement(accountID, tenantID string, start, end, asOf time.Time, revision int,
	priorStatus state.APIConsumerUsageStatementStatus, usageDelta, monthUsage []state.APIConsumerUsageBucket,
	cardsByApp map[string][]state.APIConsumerRateCard, tenantCards []state.PlatformTenantRateCard,
	previous []state.PlatformTenantStatement) (state.PlatformTenantStatementInput, error) {
	in := state.PlatformTenantStatementInput{AccountID: accountID, TenantID: tenantID, PeriodStart: start, PeriodEnd: end,
		AsOf: asOf, Revision: revision, PriorStatus: priorStatus, Repriced: true}
	if !IsCalendarMonth(start, end) {
		return in, ErrTenantMonthPeriodRequired
	}
	for _, bucket := range usageDelta {
		if bucket.BillableUnits > 0 {
			in.Coverage = append(in.Coverage, state.PlatformTenantStatementCoverage{
				AppID: bucket.AppID, ConsumerID: bucket.ConsumerKey, SurfaceID: bucket.SurfaceID,
				JWTAuthorizationRuleID: bucket.JWTAuthorizationRuleID,
				WindowStart:            bucket.WindowStart.UTC(), BillableUnits: bucket.BillableUnits,
			})
		}
	}
	if len(in.Coverage) == 0 {
		return in, ErrNoNewTenantUsage
	}
	current, currency, err := priceTenantMonth(cardsByApp, tenantCards, monthUsage)
	if err != nil {
		return in, err
	}
	in.Currency = currency
	for _, statement := range previous {
		if statement.Status != state.APIConsumerUsageStatementFinalized || !statement.PeriodStart.Equal(start) || !statement.PeriodEnd.Equal(end) {
			continue
		}
		for _, prior := range statement.Lines {
			key := tenantStatementLineKey(prior.AppID, prior.ConsumerID, prior.SurfaceID, prior.JWTAuthorizationRuleID,
				prior.RateCardID, prior.PlatformTenantRateCardID, prior.Currency, prior.PriceMillicentsPerUnit)
			line := current[key]
			if line == nil {
				line = &tenantLine{line: prior}
				line.line.BillableUnits, line.line.AmountMillicents = 0, 0
				current[key] = line
			}
			if !addsWithinInt64(line.line.BillableUnits, -prior.BillableUnits) || !addsWithinInt64(line.line.AmountMillicents, -prior.AmountMillicents) {
				return in, fmt.Errorf("platform tenant invoice line overflow")
			}
			line.line.BillableUnits -= prior.BillableUnits
			line.line.AmountMillicents -= prior.AmountMillicents
		}
	}
	keys := make([]string, 0, len(current))
	for key, line := range current {
		if line.line.BillableUnits != 0 || line.line.AmountMillicents != 0 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		line := current[key].line
		in.Lines = append(in.Lines, line)
		if !addsWithinInt64(in.BillableUnits, line.BillableUnits) || !addsWithinInt64(in.AmountMillicents, line.AmountMillicents) {
			return in, fmt.Errorf("platform tenant statement total overflow")
		}
		in.BillableUnits += line.BillableUnits
		in.AmountMillicents += line.AmountMillicents
		if line.RateCardID == "" && line.PlatformTenantRateCardID == "" {
			in.UnpricedUnits += line.BillableUnits
		}
	}
	var coverageUnits int64
	for _, minute := range in.Coverage {
		coverageUnits += minute.BillableUnits
	}
	if in.BillableUnits != coverageUnits {
		return in, ErrTenantMonthOverlap
	}
	if in.AmountMillicents < 0 {
		return in, ErrTenantChargeDecreased
	}
	if in.UnpricedUnits == in.BillableUnits {
		in.Currency = ""
	}
	sort.SliceStable(in.Lines, func(i, j int) bool { return in.Lines[i].WindowStart.Before(in.Lines[j].WindowStart) })
	return in, nil
}
