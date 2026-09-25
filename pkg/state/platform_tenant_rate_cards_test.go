package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemPlatformTenantRateCardsVersionedAndTenantScoped(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	accountID := uuid.NewString()
	tenant, _, err := store.CreatePlatformTenant(ctx, accountID, "rate-card-"+uuid.NewString(), "Rate card tenant", 10)
	if err != nil {
		t.Fatal(err)
	}
	effective := time.Date(2026, 9, 25, 12, 1, 0, 0, time.UTC)
	newer, err := store.CreatePlatformTenantRateCard(ctx, accountID, tenant.ID, "eur", 2500, effective)
	if err != nil {
		t.Fatalf("create newer: %v", err)
	}
	if newer.Currency != "EUR" || newer.Unit != PlatformTenantRateCardUnitRequest {
		t.Fatalf("normalized card = %+v", newer)
	}
	older := effective.Add(-time.Minute)
	if _, err := store.CreatePlatformTenantRateCard(ctx, accountID, tenant.ID, "EUR", 2000, older); err != nil {
		t.Fatalf("create older: %v", err)
	}
	if _, err := store.CreatePlatformTenantRateCard(ctx, accountID, tenant.ID, "EUR", 3000, effective); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate effective minute = %v, want ErrConflict", err)
	}
	if _, err := store.CreatePlatformTenantRateCard(ctx, accountID, tenant.ID, "USD", 3000, effective.Add(time.Minute)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("currency change = %v, want ErrInvalidArgument", err)
	}
	cards, err := store.ListPlatformTenantRateCards(ctx, accountID, tenant.ID)
	if err != nil || len(cards) != 2 || !cards[0].EffectiveFrom.Equal(older) || !cards[1].EffectiveFrom.Equal(effective) {
		t.Fatalf("cards = %+v, err=%v; want chronological tenant history", cards, err)
	}
	if _, err := store.ListPlatformTenantRateCards(ctx, uuid.NewString(), tenant.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account list = %v, want ErrNotFound", err)
	}
}

func TestMemPlatformTenantRateCardRejectsPartialMinute(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	accountID := uuid.NewString()
	tenant, _, err := store.CreatePlatformTenant(ctx, accountID, "rate-card-"+uuid.NewString(), "Rate card tenant", 10)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreatePlatformTenantRateCard(ctx, accountID, tenant.ID, "EUR", 1, time.Now().UTC().Truncate(time.Minute).Add(30*time.Second))
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("partial-minute effective_from = %v, want ErrInvalidArgument", err)
	}
}
