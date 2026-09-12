package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
)

func (m *MemStore) CreateAPIConsumerRateCard(_ context.Context, accountID, appID, currency string, price int64, effectiveFrom time.Time) (APIConsumerRateCard, error) {
	currency = normalizeAPIConsumerRateCardCurrency(currency)
	effectiveFrom = effectiveFrom.UTC()
	if err := validateAPIConsumerRateCardInput("CreateAPIConsumerRateCard", accountID, appID, currency, price, effectiveFrom); err != nil {
		return APIConsumerRateCard{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, card := range m.apiConsumerRateCards {
		if card.AppID == appID && card.EffectiveFrom.Equal(effectiveFrom) {
			return APIConsumerRateCard{}, ErrConflict
		}
	}
	now := time.Now().UTC()
	card := APIConsumerRateCard{
		ID:                     uuid.NewString(),
		AccountID:              accountID,
		AppID:                  appID,
		Currency:               currency,
		Unit:                   APIConsumerRateCardUnitRequest,
		PriceMillicentsPerUnit: price,
		EffectiveFrom:          effectiveFrom,
		CreatedAt:              now,
	}
	m.apiConsumerRateCards[card.ID] = card
	return card, nil
}

func (m *MemStore) ListAPIConsumerRateCardsForApp(_ context.Context, accountID, appID string) ([]APIConsumerRateCard, error) {
	if accountID == "" || appID == "" {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []APIConsumerRateCard
	for _, card := range m.apiConsumerRateCards {
		if card.AccountID == accountID && card.AppID == appID {
			out = append(out, card)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EffectiveFrom.Equal(out[j].EffectiveFrom) {
			return out[i].ID < out[j].ID
		}
		return out[i].EffectiveFrom.Before(out[j].EffectiveFrom)
	})
	return out, nil
}

func (m *MemStore) GetAPIConsumerRateCardByID(_ context.Context, accountID, cardID string) (APIConsumerRateCard, error) {
	if accountID == "" || cardID == "" {
		return APIConsumerRateCard{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	card, ok := m.apiConsumerRateCards[cardID]
	if !ok || card.AccountID != accountID {
		return APIConsumerRateCard{}, ErrNotFound
	}
	return card, nil
}
