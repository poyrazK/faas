package billing

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

const maxInt64 = int64(1<<63 - 1)

var ErrMixedAPIConsumerRateCardCurrency = errors.New("billing: API consumer rate cards use multiple currencies")

// ErrAPIConsumerAllowanceInTenantStatement rejects a cross-app statement that
// would fall back to an app rate card with a monthly allowance (ADR-935), a
// graduated ladder (ADR-936), route weights (ADR-937), or that belong to a
// consumer plan (ADR-938).
var ErrAPIConsumerAllowanceInTenantStatement = errors.New("billing: app rate cards with included units, tiers, route weights, or consumer plans cannot price platform tenant statements")

// APIConsumerUsageChargeBucket is the priced form of one durable usage
// minute. RateCardID is empty when no card was effective for that minute.
type APIConsumerUsageChargeBucket struct {
	WindowStart              time.Time
	BillableUnits            int64
	RateCardID               string
	PlatformTenantRateCardID string
	Currency                 string
	PriceMillicentsPerUnit   int64
	// ChargedUnits is how many units are billed at the price; the rest were
	// covered by the card's monthly allowance (ADR-935).
	ChargedUnits int64
	// TierUnits splits BillableUnits across a tiered card's ladder steps
	// (ADR-936); nil for flat and allowance cards.
	TierUnits        []int64
	AmountMillicents int64
}

// QuotePlatformTenantUsage applies a tenant-wide customer tariff when one is
// effective for a usage minute. Before the tenant's first effective card, it
// falls back to the app-level price. The output records which pricing source
// determined each immutable statement line.
func QuotePlatformTenantUsage(appCards []state.APIConsumerRateCard, tenantCards []state.PlatformTenantRateCard, usage []state.APIConsumerUsageBucket) (APIConsumerUsageQuote, error) {
	for _, card := range appCards {
		if card.IncludedUnitsPerMonth > 0 || len(card.Tiers) > 0 || len(card.RouteWeights) > 0 || card.PlanID != "" {
			// Allowances, tiers, weights, and plans are per consumer; a cross-app statement mixes sources
			// and prices only usage deltas, so it cannot apply them correctly.
			return APIConsumerUsageQuote{}, ErrAPIConsumerAllowanceInTenantStatement
		}
	}
	if len(tenantCards) == 0 {
		return QuoteAPIConsumerUsage(appCards, usage)
	}
	orderedTenantCards := append([]state.PlatformTenantRateCard(nil), tenantCards...)
	sort.Slice(orderedTenantCards, func(i, j int) bool {
		if orderedTenantCards[i].EffectiveFrom.Equal(orderedTenantCards[j].EffectiveFrom) {
			return orderedTenantCards[i].ID < orderedTenantCards[j].ID
		}
		return orderedTenantCards[i].EffectiveFrom.Before(orderedTenantCards[j].EffectiveFrom)
	})
	tenantCardsAsAppCards := make([]state.APIConsumerRateCard, 0, len(orderedTenantCards))
	for _, card := range orderedTenantCards {
		if card.Unit != state.PlatformTenantRateCardUnitRequest {
			return APIConsumerUsageQuote{}, fmt.Errorf("billing: unsupported platform tenant rate-card unit %q", card.Unit)
		}
		tenantCardsAsAppCards = append(tenantCardsAsAppCards, state.APIConsumerRateCard{
			ID: card.ID, Currency: card.Currency, Unit: card.Unit,
			PriceMillicentsPerUnit: card.PriceMillicentsPerUnit, EffectiveFrom: card.EffectiveFrom,
		})
	}
	// Validate the complete append-only history, including duplicate effective
	// minutes and currency/unit invariants, before selecting a card per minute.
	if _, err := QuoteAPIConsumerUsage(tenantCardsAsAppCards, nil); err != nil {
		return APIConsumerUsageQuote{}, err
	}

	orderedUsage := append([]state.APIConsumerUsageBucket(nil), usage...)
	sort.Slice(orderedUsage, func(i, j int) bool {
		if orderedUsage[i].WindowStart.Equal(orderedUsage[j].WindowStart) {
			return orderedUsage[i].AppID < orderedUsage[j].AppID
		}
		return orderedUsage[i].WindowStart.Before(orderedUsage[j].WindowStart)
	})
	tenantCardForUsage := make([]int, len(orderedUsage))
	fallbackUsage := make([]state.APIConsumerUsageBucket, 0, len(orderedUsage))
	tenantCardIndex := -1
	for i, bucket := range orderedUsage {
		if bucket.BillableUnits < 0 {
			return APIConsumerUsageQuote{}, fmt.Errorf("billing: negative platform tenant billable units")
		}
		for tenantCardIndex+1 < len(orderedTenantCards) && !orderedTenantCards[tenantCardIndex+1].EffectiveFrom.After(bucket.WindowStart.UTC()) {
			tenantCardIndex++
		}
		tenantCardForUsage[i] = tenantCardIndex
		if tenantCardIndex < 0 {
			fallbackUsage = append(fallbackUsage, bucket)
		}
	}
	var fallbackQuote APIConsumerUsageQuote
	var err error
	if len(fallbackUsage) > 0 {
		fallbackQuote, err = QuoteAPIConsumerUsage(appCards, fallbackUsage)
		if err != nil {
			return APIConsumerUsageQuote{}, err
		}
	}

	quote := APIConsumerUsageQuote{Buckets: make([]APIConsumerUsageChargeBucket, 0, len(orderedUsage))}
	fallbackIndex := 0
	for i, bucket := range orderedUsage {
		if quote.BillableUnits > maxInt64-bucket.BillableUnits {
			return APIConsumerUsageQuote{}, fmt.Errorf("billing: platform tenant usage total overflow")
		}
		quote.BillableUnits += bucket.BillableUnits
		var priced APIConsumerUsageChargeBucket
		if tenantCardForUsage[i] >= 0 {
			card := orderedTenantCards[tenantCardForUsage[i]]
			amount, err := multiplyMillicents(bucket.BillableUnits, card.PriceMillicentsPerUnit)
			if err != nil {
				return APIConsumerUsageQuote{}, err
			}
			priced = APIConsumerUsageChargeBucket{WindowStart: bucket.WindowStart.UTC(), BillableUnits: bucket.BillableUnits,
				PlatformTenantRateCardID: card.ID, Currency: card.Currency,
				PriceMillicentsPerUnit: card.PriceMillicentsPerUnit, ChargedUnits: bucket.BillableUnits, AmountMillicents: amount}
		} else {
			priced = fallbackQuote.Buckets[fallbackIndex]
			fallbackIndex++
		}
		if priced.RateCardID == "" && priced.PlatformTenantRateCardID == "" {
			if quote.UnpricedUnits > maxInt64-bucket.BillableUnits {
				return APIConsumerUsageQuote{}, fmt.Errorf("billing: platform tenant unpriced usage total overflow")
			}
			quote.UnpricedUnits += bucket.BillableUnits
		} else {
			if quote.Currency != "" && quote.Currency != priced.Currency {
				return APIConsumerUsageQuote{}, ErrMixedAPIConsumerRateCardCurrency
			}
			quote.Currency = priced.Currency
			if quote.AmountMillicents > maxInt64-priced.AmountMillicents {
				return APIConsumerUsageQuote{}, fmt.Errorf("billing: platform tenant charge total overflow")
			}
			quote.AmountMillicents += priced.AmountMillicents
		}
		quote.Buckets = append(quote.Buckets, priced)
	}
	quote.Priced = quote.BillableUnits > 0 && quote.UnpricedUnits == 0 && quote.Currency != ""
	return quote, nil
}

// APIConsumerUsageQuote is a deterministic estimate, not an invoice or a
// payment authorization. UnpricedUnits makes a missing rate card explicit
// instead of silently presenting a zero charge as free usage.
type APIConsumerUsageQuote struct {
	Currency         string
	BillableUnits    int64
	UnpricedUnits    int64
	AmountMillicents int64
	Priced           bool
	Buckets          []APIConsumerUsageChargeBucket
}

// QuoteAPIConsumerUsageFrom applies the latest rate card effective at each
// UTC usage minute. Rate cards are append-only and callers may pass them in
// any order; the function sorts a copy and never mutates caller-owned slices.
// usage must be ascending by minute. A card's monthly allowance (ADR-935) is
// consumed in minute order from the first bucket of each UTC calendar month,
// so callers pass usage from the start of from's month; buckets before from
// only consume allowance and are not reported.
func QuoteAPIConsumerUsageFrom(cards []state.APIConsumerRateCard, usage []state.APIConsumerUsageBucket, from time.Time) (APIConsumerUsageQuote, error) {
	ordered := append([]state.APIConsumerRateCard(nil), cards...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].EffectiveFrom.Equal(ordered[j].EffectiveFrom) {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].EffectiveFrom.Before(ordered[j].EffectiveFrom)
	})
	var quote APIConsumerUsageQuote
	for i, card := range ordered {
		if card.Unit != state.APIConsumerRateCardUnitRequest {
			return APIConsumerUsageQuote{}, fmt.Errorf("billing: unsupported API consumer rate-card unit %q", card.Unit)
		}
		if card.Currency == "" || len(card.Currency) != 3 || !isUpperASCIICurrency(card.Currency) {
			return APIConsumerUsageQuote{}, fmt.Errorf("billing: invalid API consumer rate-card currency %q", card.Currency)
		}
		if card.PriceMillicentsPerUnit < 0 {
			return APIConsumerUsageQuote{}, fmt.Errorf("billing: negative API consumer rate-card price")
		}
		if card.EffectiveFrom.IsZero() || !card.EffectiveFrom.Equal(card.EffectiveFrom.UTC().Truncate(time.Minute)) {
			return APIConsumerUsageQuote{}, fmt.Errorf("billing: API consumer rate-card effective_from must be a UTC minute")
		}
		if i > 0 && ordered[i-1].EffectiveFrom.Equal(card.EffectiveFrom) {
			return APIConsumerUsageQuote{}, fmt.Errorf("billing: duplicate API consumer rate-card effective_from %s", card.EffectiveFrom.Format(time.RFC3339))
		}
		if quote.Currency == "" {
			quote.Currency = card.Currency
		} else if quote.Currency != card.Currency {
			return APIConsumerUsageQuote{}, ErrMixedAPIConsumerRateCardCurrency
		}
	}

	quote.Buckets = make([]APIConsumerUsageChargeBucket, 0, len(usage))
	cardIndex := -1
	var month time.Time
	var usedThisMonth int64
	for _, bucket := range usage {
		if bucket.BillableUnits < 0 {
			return APIConsumerUsageQuote{}, fmt.Errorf("billing: negative API consumer billable units")
		}
		minute := bucket.WindowStart.UTC()
		if bucketMonth := time.Date(minute.Year(), minute.Month(), 1, 0, 0, 0, 0, time.UTC); !bucketMonth.Equal(month) {
			month, usedThisMonth = bucketMonth, 0
		}
		for cardIndex+1 < len(ordered) && !ordered[cardIndex+1].EffectiveFrom.After(minute) {
			cardIndex++
		}
		priced := APIConsumerUsageChargeBucket{WindowStart: minute, BillableUnits: bucket.BillableUnits}
		if cardIndex >= 0 {
			var err error
			if priced, err = priceMinute(ordered[cardIndex], priced, usedThisMonth); err != nil {
				return APIConsumerUsageQuote{}, err
			}
		}
		if usedThisMonth > maxInt64-bucket.BillableUnits {
			return APIConsumerUsageQuote{}, fmt.Errorf("billing: API consumer monthly usage overflow")
		}
		usedThisMonth += bucket.BillableUnits
		if minute.Before(from) {
			continue // history that only consumes the month's allowance
		}
		if err := quote.add(priced); err != nil {
			return APIConsumerUsageQuote{}, err
		}
	}
	quote.Priced = len(ordered) > 0 && quote.UnpricedUnits == 0
	return quote, nil
}

// QuoteAPIConsumerUsage prices usage whose first bucket starts its month's
// allowance count. Use QuoteAPIConsumerUsageFrom when earlier usage of the
// same month exists.
func QuoteAPIConsumerUsage(cards []state.APIConsumerRateCard, usage []state.APIConsumerUsageBucket) (APIConsumerUsageQuote, error) {
	return QuoteAPIConsumerUsageFrom(cards, usage, time.Time{})
}

// priceMinute prices one minute's units given the units the consumer's month
// already used. A tiered card (ADR-936) splits them across its ladder by
// position; other cards charge the units beyond the allowance (ADR-935) at
// the flat price.
func priceMinute(card state.APIConsumerRateCard, bucket APIConsumerUsageChargeBucket, usedBefore int64) (APIConsumerUsageChargeBucket, error) {
	bucket.RateCardID, bucket.Currency = card.ID, card.Currency
	if len(card.Tiers) == 0 {
		charged := bucket.BillableUnits - includedUnits(card.IncludedUnitsPerMonth, usedBefore, bucket.BillableUnits)
		amount, err := multiplyMillicents(charged, card.PriceMillicentsPerUnit)
		if err != nil {
			return bucket, err
		}
		bucket.PriceMillicentsPerUnit, bucket.ChargedUnits, bucket.AmountMillicents = card.PriceMillicentsPerUnit, charged, amount
		return bucket, nil
	}
	bucket.TierUnits = make([]int64, len(card.Tiers))
	position, remaining := usedBefore, bucket.BillableUnits
	var lower int64
	for i, tier := range card.Tiers {
		inTier := remaining
		if tier.UpTo != nil {
			inTier = min(remaining, max(0, *tier.UpTo-max(position, lower)))
			lower = *tier.UpTo
		}
		amount, err := multiplyMillicents(inTier, tier.PriceMillicentsPerUnit)
		if err != nil || bucket.AmountMillicents > maxInt64-amount {
			return bucket, fmt.Errorf("billing: API consumer tiered charge overflow")
		}
		bucket.TierUnits[i] = inTier
		bucket.AmountMillicents += amount
		if tier.PriceMillicentsPerUnit > 0 {
			bucket.ChargedUnits += inTier
		}
		position += inTier
		remaining -= inTier
	}
	return bucket, nil
}

// TieredCardEffectiveIn reports whether a tiered card prices any minute of
// [start, end). Such periods must be whole UTC months (ADR-936).
func TieredCardEffectiveIn(cards []state.APIConsumerRateCard, start, end time.Time) bool {
	ordered := append([]state.APIConsumerRateCard(nil), cards...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].EffectiveFrom.Before(ordered[j].EffectiveFrom) })
	for i, card := range ordered {
		until := end
		if i+1 < len(ordered) {
			until = ordered[i+1].EffectiveFrom
		}
		if len(card.Tiers) > 0 && card.EffectiveFrom.Before(end) && until.After(start) {
			return true
		}
	}
	return false
}

// includedUnits returns how many of a minute's units the month's allowance
// still covers, given the units the month already used before this minute.
func includedUnits(allowance, usedBefore, units int64) int64 {
	remaining := allowance - usedBefore
	if remaining <= 0 {
		return 0
	}
	return min(remaining, units)
}

// add appends one priced bucket and folds it into the quote totals.
func (q *APIConsumerUsageQuote) add(bucket APIConsumerUsageChargeBucket) error {
	if q.BillableUnits > maxInt64-bucket.BillableUnits {
		return fmt.Errorf("billing: API consumer usage total overflow")
	}
	q.BillableUnits += bucket.BillableUnits
	if bucket.RateCardID == "" {
		if q.UnpricedUnits > maxInt64-bucket.BillableUnits {
			return fmt.Errorf("billing: API consumer unpriced usage total overflow")
		}
		q.UnpricedUnits += bucket.BillableUnits
	} else {
		// A re-rated tiered adjustment bucket may be negative (ADR-936).
		if (bucket.AmountMillicents > 0 && q.AmountMillicents > maxInt64-bucket.AmountMillicents) ||
			(bucket.AmountMillicents < 0 && q.AmountMillicents < -maxInt64-bucket.AmountMillicents) {
			return fmt.Errorf("billing: API consumer charge total overflow")
		}
		q.AmountMillicents += bucket.AmountMillicents
	}
	q.Buckets = append(q.Buckets, bucket)
	return nil
}

func isUpperASCIICurrency(currency string) bool {
	for i := 0; i < len(currency); i++ {
		if currency[i] < 'A' || currency[i] > 'Z' {
			return false
		}
	}
	return true
}

func multiplyMillicents(units, price int64) (int64, error) {
	if units < 0 || price < 0 {
		return 0, fmt.Errorf("billing: negative API consumer charge input")
	}
	if price != 0 && units > maxInt64/price {
		return 0, fmt.Errorf("billing: API consumer charge overflow")
	}
	return units * price, nil
}
