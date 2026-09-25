package billing

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

var (
	ErrNoNewTenantUsage        = errors.New("no new platform tenant usage")
	ErrMixedTenantCurrency     = errors.New("platform tenant usage spans multiple currencies")
	ErrTenantUsageRegressed    = errors.New("platform tenant usage is less than prior statement coverage")
	ErrTenantStatementTooLarge = errors.New("platform tenant statement exceeds 20000 usage minutes")
)

// BuildPlatformTenantStatement computes a delta from every prior immutable
// revision. Replaying a finalized period without new usage is a no-op; late
// events become a new adjustment rather than editing an earlier statement.
func BuildPlatformTenantStatement(accountID, tenantID string, start, end, asOf time.Time,
	usage []state.APIConsumerUsageBucket, cardsByApp map[string][]state.APIConsumerRateCard,
	previous []state.PlatformTenantStatement) (state.PlatformTenantStatementInput, error) {
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
		for _, line := range statement.Lines {
			key := tenantUsageKey(line.AppID, line.ConsumerID, line.SurfaceID, line.JWTAuthorizationRuleID, line.WindowStart)
			if covered[key] > maxInt64-line.BillableUnits {
				return in, fmt.Errorf("platform tenant coverage overflow")
			}
			covered[key] += line.BillableUnits
		}
	}
	bySubject := map[string][]state.APIConsumerUsageBucket{}
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
		subject := bucket.AppID + "\x00" + bucket.ConsumerKey + "\x00" + bucket.SurfaceID + "\x00" + bucket.JWTAuthorizationRuleID
		bySubject[subject] = append(bySubject[subject], bucket)
	}
	for _, remaining := range covered {
		if remaining > 0 {
			return in, ErrTenantUsageRegressed
		}
	}
	if len(bySubject) == 0 {
		return in, ErrNoNewTenantUsage
	}
	keys := make([]string, 0, len(bySubject))
	for key := range bySubject {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		buckets := bySubject[key]
		sort.Slice(buckets, func(i, j int) bool { return buckets[i].WindowStart.Before(buckets[j].WindowStart) })
		appID, consumerID, surfaceID, jwtRuleID := buckets[0].AppID, buckets[0].ConsumerKey, buckets[0].SurfaceID, buckets[0].JWTAuthorizationRuleID
		quote, err := QuoteAPIConsumerUsage(cardsByApp[appID], buckets)
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
			in.Lines = append(in.Lines, state.PlatformTenantStatementLine{
				AppID: appID, ConsumerID: consumerID, SurfaceID: surfaceID, JWTAuthorizationRuleID: jwtRuleID, WindowStart: priced.WindowStart,
				BillableUnits: priced.BillableUnits, RateCardID: priced.RateCardID,
				Currency: priced.Currency, PriceMillicentsPerUnit: priced.PriceMillicentsPerUnit,
				AmountMillicents: priced.AmountMillicents,
			})
		}
	}
	if len(in.Lines) > 20000 {
		return in, ErrTenantStatementTooLarge
	}
	return in, nil
}

func tenantUsageKey(appID, consumerID, surfaceID, jwtRuleID string, minute time.Time) string {
	return appID + "\x00" + consumerID + "\x00" + surfaceID + "\x00" + jwtRuleID + "\x00" + minute.UTC().Format(time.RFC3339)
}
