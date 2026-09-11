package billing

import (
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

const maxInt64 = int64(1<<63 - 1)

// APIConsumerUsageChargeBucket is the priced form of one durable usage
// minute. RateCardID is empty when no card was effective for that minute.
type APIConsumerUsageChargeBucket struct {
	WindowStart            time.Time
	BillableUnits          int64
	RateCardID             string
	Currency               string
	PriceMillicentsPerUnit int64
	AmountMillicents       int64
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

// QuoteAPIConsumerUsage applies the latest rate card effective at each UTC
// usage minute. Rate cards are append-only and callers may pass them in any
// order; the function sorts a copy and never mutates caller-owned slices.
func QuoteAPIConsumerUsage(cards []state.APIConsumerRateCard, usage []state.APIConsumerUsageBucket) (APIConsumerUsageQuote, error) {
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
			return APIConsumerUsageQuote{}, fmt.Errorf("billing: API consumer rate cards use multiple currencies")
		}
	}

	quote.Buckets = make([]APIConsumerUsageChargeBucket, 0, len(usage))
	cardIndex := -1
	for _, bucket := range usage {
		if bucket.BillableUnits < 0 {
			return APIConsumerUsageQuote{}, fmt.Errorf("billing: negative API consumer billable units")
		}
		if quote.BillableUnits > maxInt64-bucket.BillableUnits {
			return APIConsumerUsageQuote{}, fmt.Errorf("billing: API consumer usage total overflow")
		}
		quote.BillableUnits += bucket.BillableUnits
		for cardIndex+1 < len(ordered) && !ordered[cardIndex+1].EffectiveFrom.After(bucket.WindowStart.UTC()) {
			cardIndex++
		}
		priced := APIConsumerUsageChargeBucket{
			WindowStart:   bucket.WindowStart.UTC(),
			BillableUnits: bucket.BillableUnits,
		}
		if cardIndex < 0 {
			if quote.UnpricedUnits > maxInt64-bucket.BillableUnits {
				return APIConsumerUsageQuote{}, fmt.Errorf("billing: API consumer unpriced usage total overflow")
			}
			quote.UnpricedUnits += bucket.BillableUnits
		} else {
			card := ordered[cardIndex]
			priced.RateCardID = card.ID
			priced.Currency = card.Currency
			priced.PriceMillicentsPerUnit = card.PriceMillicentsPerUnit
			amount, err := multiplyMillicents(bucket.BillableUnits, card.PriceMillicentsPerUnit)
			if err != nil {
				return APIConsumerUsageQuote{}, err
			}
			priced.AmountMillicents = amount
			if quote.AmountMillicents > maxInt64-amount {
				return APIConsumerUsageQuote{}, fmt.Errorf("billing: API consumer charge total overflow")
			}
			quote.AmountMillicents += amount
		}
		quote.Buckets = append(quote.Buckets, priced)
	}
	quote.Priced = len(ordered) > 0 && quote.UnpricedUnits == 0
	return quote, nil
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
