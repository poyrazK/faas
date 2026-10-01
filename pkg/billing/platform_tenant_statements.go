package billing

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

var (
	ErrNoNewTenantUsage     = errors.New("no new platform tenant usage")
	ErrMixedTenantCurrency  = errors.New("platform tenant usage spans multiple currencies")
	ErrTenantUsageRegressed = state.ErrPlatformTenantUsageRegressed
)

// BuildPlatformTenantStatement computes a delta from every prior immutable
// revision. Replaying a finalized period without new usage is a no-op; late
// events become a new adjustment rather than editing an earlier statement.
func BuildPlatformTenantStatement(accountID, tenantID string, start, end, asOf time.Time,
	usage []state.APIConsumerUsageBucket, cardsByApp map[string][]state.APIConsumerRateCard,
	tenantCards []state.PlatformTenantRateCard, previous []state.PlatformTenantStatement) (state.PlatformTenantStatementInput, error) {
	in := state.PlatformTenantStatementInput{AccountID: accountID, TenantID: tenantID,
		PeriodStart: start, PeriodEnd: end, AsOf: asOf, Revision: len(previous) + 1}
	if len(previous) > 0 {
		in.PriorStatus = previous[len(previous)-1].Status
	}
	covered := map[string]int64{}
	for _, statement := range previous {
		if statement.Status != state.APIConsumerUsageStatementFinalized {
			continue // drafts and superseded drafts never reserve billable usage
		}
		coverage := statement.Coverage
		if len(coverage) == 0 {
			// Statements created before the coverage column stored one exact
			// minute per public line. Read those as legacy minute evidence.
			for _, line := range statement.Lines {
				if !line.WindowEnd.IsZero() && !line.WindowEnd.Equal(line.WindowStart.Add(time.Minute)) {
					return in, ErrTenantUsageRegressed
				}
				coverage = append(coverage, state.PlatformTenantStatementCoverage{
					AppID: line.AppID, ConsumerID: line.ConsumerID, SurfaceID: line.SurfaceID,
					JWTAuthorizationRuleID: line.JWTAuthorizationRuleID,
					WindowStart:            line.WindowStart, BillableUnits: line.BillableUnits,
				})
			}
		}
		for _, minute := range coverage {
			key := tenantUsageKey(minute.AppID, minute.ConsumerID, minute.SurfaceID, minute.JWTAuthorizationRuleID, minute.WindowStart)
			if covered[key] > maxInt64-minute.BillableUnits {
				return in, fmt.Errorf("platform tenant coverage overflow")
			}
			covered[key] += minute.BillableUnits
		}
	}
	usageDelta := make([]state.APIConsumerUsageBucket, 0, len(usage))
	for _, bucket := range usage {
		key := tenantUsageKey(bucket.AppID, bucket.ConsumerKey, bucket.SurfaceID, bucket.JWTAuthorizationRuleID, bucket.WindowStart)
		prior := covered[key]
		if bucket.BillableUnits < prior {
			return in, ErrTenantUsageRegressed
		}
		delete(covered, key)
		bucket.BillableUnits -= prior
		if bucket.BillableUnits == 0 {
			continue
		}
		usageDelta = append(usageDelta, bucket)
	}
	for _, remaining := range covered {
		if remaining > 0 {
			return in, ErrTenantUsageRegressed
		}
	}
	return BuildPlatformTenantStatementFromDelta(accountID, tenantID, start, end, asOf, in.Revision, in.PriorStatus,
		usageDelta, cardsByApp, tenantCards)
}

// BuildPlatformTenantStatementFromDelta prices only positive coverage deltas
// already computed by the store. It also records that exact delta as the next
// immutable revision's private coverage.
func BuildPlatformTenantStatementFromDelta(accountID, tenantID string, start, end, asOf time.Time,
	revision int, priorStatus state.APIConsumerUsageStatementStatus, usageDelta []state.APIConsumerUsageBucket,
	cardsByApp map[string][]state.APIConsumerRateCard, tenantCards []state.PlatformTenantRateCard) (state.PlatformTenantStatementInput, error) {
	in := state.PlatformTenantStatementInput{AccountID: accountID, TenantID: tenantID,
		PeriodStart: start, PeriodEnd: end, AsOf: asOf, Revision: revision, PriorStatus: priorStatus}
	bySubject := map[string][]state.APIConsumerUsageBucket{}
	for _, bucket := range usageDelta {
		if bucket.BillableUnits <= 0 {
			continue
		}
		in.Coverage = append(in.Coverage, state.PlatformTenantStatementCoverage{
			AppID: bucket.AppID, ConsumerID: bucket.ConsumerKey, SurfaceID: bucket.SurfaceID,
			JWTAuthorizationRuleID: bucket.JWTAuthorizationRuleID,
			WindowStart:            bucket.WindowStart.UTC(), BillableUnits: bucket.BillableUnits,
		})
		subject := bucket.AppID + "\x00" + bucket.ConsumerKey + "\x00" + bucket.SurfaceID + "\x00" + bucket.JWTAuthorizationRuleID
		bySubject[subject] = append(bySubject[subject], bucket)
	}
	if len(bySubject) == 0 {
		return in, ErrNoNewTenantUsage
	}
	keys := make([]string, 0, len(bySubject))
	for key := range bySubject {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lineGroups := map[string]*state.PlatformTenantStatementLine{}
	for _, key := range keys {
		buckets := bySubject[key]
		sort.Slice(buckets, func(i, j int) bool { return buckets[i].WindowStart.Before(buckets[j].WindowStart) })
		appID, consumerID, surfaceID, jwtRuleID := buckets[0].AppID, buckets[0].ConsumerKey, buckets[0].SurfaceID, buckets[0].JWTAuthorizationRuleID
		quote, err := QuotePlatformTenantUsage(cardsByApp[appID], tenantCards, buckets)
		if err != nil {
			if errors.Is(err, ErrMixedAPIConsumerRateCardCurrency) {
				return in, ErrMixedTenantCurrency
			}
			return in, err
		}
		if quote.Currency != "" {
			if in.Currency != "" && in.Currency != quote.Currency {
				return in, ErrMixedTenantCurrency
			}
			in.Currency = quote.Currency
		}
		if in.BillableUnits > maxInt64-quote.BillableUnits || in.UnpricedUnits > maxInt64-quote.UnpricedUnits ||
			in.AmountMillicents > maxInt64-quote.AmountMillicents {
			return in, fmt.Errorf("platform tenant statement total overflow")
		}
		in.BillableUnits += quote.BillableUnits
		in.UnpricedUnits += quote.UnpricedUnits
		in.AmountMillicents += quote.AmountMillicents
		for _, priced := range quote.Buckets {
			key := tenantStatementLineKey(appID, consumerID, surfaceID, jwtRuleID, priced.RateCardID,
				priced.PlatformTenantRateCardID, priced.Currency, priced.PriceMillicentsPerUnit)
			line := lineGroups[key]
			if line == nil {
				line = &state.PlatformTenantStatementLine{
					AppID: appID, ConsumerID: consumerID, SurfaceID: surfaceID, JWTAuthorizationRuleID: jwtRuleID,
					WindowStart: priced.WindowStart, WindowEnd: priced.WindowStart.Add(time.Minute),
					RateCardID: priced.RateCardID, PlatformTenantRateCardID: priced.PlatformTenantRateCardID,
					Currency: priced.Currency, PriceMillicentsPerUnit: priced.PriceMillicentsPerUnit,
				}
				lineGroups[key] = line
			} else {
				if priced.WindowStart.Before(line.WindowStart) {
					line.WindowStart = priced.WindowStart
				}
				if end := priced.WindowStart.Add(time.Minute); end.After(line.WindowEnd) {
					line.WindowEnd = end
				}
			}
			if line.BillableUnits > maxInt64-priced.BillableUnits || line.AmountMillicents > maxInt64-priced.AmountMillicents {
				return in, fmt.Errorf("platform tenant invoice line overflow")
			}
			line.BillableUnits += priced.BillableUnits
			line.AmountMillicents += priced.AmountMillicents
		}
	}
	lineKeys := make([]string, 0, len(lineGroups))
	for key := range lineGroups {
		lineKeys = append(lineKeys, key)
	}
	sort.Strings(lineKeys)
	for _, key := range lineKeys {
		in.Lines = append(in.Lines, *lineGroups[key])
	}
	sort.Slice(in.Lines, func(i, j int) bool {
		left, right := in.Lines[i], in.Lines[j]
		if !left.WindowStart.Equal(right.WindowStart) {
			return left.WindowStart.Before(right.WindowStart)
		}
		leftKey := tenantStatementLineKey(left.AppID, left.ConsumerID, left.SurfaceID, left.JWTAuthorizationRuleID,
			left.RateCardID, left.PlatformTenantRateCardID, left.Currency, left.PriceMillicentsPerUnit)
		rightKey := tenantStatementLineKey(right.AppID, right.ConsumerID, right.SurfaceID, right.JWTAuthorizationRuleID,
			right.RateCardID, right.PlatformTenantRateCardID, right.Currency, right.PriceMillicentsPerUnit)
		return leftKey < rightKey
	})
	return in, nil
}

func tenantStatementLineKey(appID, consumerID, surfaceID, jwtRuleID, rateCardID, tenantRateCardID, currency string, price int64) string {
	return appID + "\x00" + consumerID + "\x00" + surfaceID + "\x00" + jwtRuleID + "\x00" +
		rateCardID + "\x00" + tenantRateCardID + "\x00" + currency + "\x00" + fmt.Sprint(price)
}

func tenantUsageKey(appID, consumerID, surfaceID, jwtRuleID string, minute time.Time) string {
	return appID + "\x00" + consumerID + "\x00" + surfaceID + "\x00" + jwtRuleID + "\x00" + minute.UTC().Format(time.RFC3339)
}
