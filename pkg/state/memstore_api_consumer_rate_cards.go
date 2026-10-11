package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
)

func (m *MemStore) CreateAPIConsumerRateCard(ctx context.Context, accountID, appID, currency string, price int64, effectiveFrom time.Time) (APIConsumerRateCard, error) {
	return m.CreateAPIConsumerRateCardVersion(ctx, APIConsumerRateCardInput{
		AccountID: accountID, AppID: appID, Currency: currency, PriceMillicentsPerUnit: price, EffectiveFrom: effectiveFrom,
	})
}

func (m *MemStore) CreateAPIConsumerRateCardVersion(_ context.Context, in APIConsumerRateCardInput) (APIConsumerRateCard, error) {
	in, err := normalizeAPIConsumerRateCardInput(in)
	if err != nil {
		return APIConsumerRateCard{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if in.PlanID != "" {
		plan, ok := m.apiConsumerPlans[in.PlanID]
		if !ok || plan.AccountID != in.AccountID || plan.AppID != in.AppID {
			return APIConsumerRateCard{}, ErrNotFound
		}
	}
	for _, card := range m.apiConsumerRateCards {
		if card.AppID == in.AppID && card.PlanID == in.PlanID && card.EffectiveFrom.Equal(in.EffectiveFrom) {
			return APIConsumerRateCard{}, ErrConflict
		}
	}
	card := APIConsumerRateCard{
		ID:                     uuid.NewString(),
		AccountID:              in.AccountID,
		AppID:                  in.AppID,
		Currency:               in.Currency,
		Unit:                   APIConsumerRateCardUnitRequest,
		PriceMillicentsPerUnit: in.PriceMillicentsPerUnit,
		IncludedUnitsPerMonth:  in.IncludedUnitsPerMonth,
		Tiers:                  in.Tiers,
		RouteWeights:           in.RouteWeights,
		PlanID:                 in.PlanID,
		EffectiveFrom:          in.EffectiveFrom,
		CreatedAt:              time.Now().UTC(),
	}
	m.apiConsumerRateCards[card.ID] = card
	out := card
	out.Tiers = cloneRateCardTiers(card.Tiers)
	out.RouteWeights = cloneRouteWeights(card.RouteWeights)
	return out, nil
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
			card.Tiers = cloneRateCardTiers(card.Tiers)
			card.RouteWeights = cloneRouteWeights(card.RouteWeights)
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
	card.Tiers = cloneRateCardTiers(card.Tiers)
	card.RouteWeights = cloneRouteWeights(card.RouteWeights)
	return card, nil
}
