package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
)

func (m *MemStore) CreatePlatformTenantRateCard(_ context.Context, accountID, tenantID, currency string, price int64, effectiveFrom time.Time) (PlatformTenantRateCard, error) {
	currency = normalizePlatformTenantRateCardCurrency(currency)
	effectiveFrom = effectiveFrom.UTC()
	if err := validatePlatformTenantRateCardInput("CreatePlatformTenantRateCard", accountID, tenantID, currency, price, effectiveFrom); err != nil {
		return PlatformTenantRateCard{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return PlatformTenantRateCard{}, ErrNotFound
	}
	for _, card := range m.platformTenantRateCards {
		if card.TenantID != tenantID {
			continue
		}
		if card.Currency != currency {
			return PlatformTenantRateCard{}, ErrInvalidArgument
		}
		if card.EffectiveFrom.Equal(effectiveFrom) {
			return PlatformTenantRateCard{}, ErrConflict
		}
	}
	card := PlatformTenantRateCard{
		ID: uuid.NewString(), AccountID: accountID, TenantID: tenantID,
		Currency: currency, Unit: PlatformTenantRateCardUnitRequest,
		PriceMillicentsPerUnit: price, EffectiveFrom: effectiveFrom, CreatedAt: time.Now().UTC(),
	}
	m.platformTenantRateCards[card.ID] = card
	return card, nil
}

func (m *MemStore) ListPlatformTenantRateCards(_ context.Context, accountID, tenantID string) ([]PlatformTenantRateCard, error) {
	if _, err := uuid.Parse(accountID); err != nil {
		return nil, ErrInvalidArgument
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return nil, ErrNotFound
	}
	out := make([]PlatformTenantRateCard, 0)
	for _, card := range m.platformTenantRateCards {
		if card.AccountID == accountID && card.TenantID == tenantID {
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
