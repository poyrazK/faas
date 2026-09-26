package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgPlatformTenantRateCardsVersionedAndTenantScoped(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, _ := seedConsumerKeyAccountApp(t, ctx, store)
	tenant, _, err := store.CreatePlatformTenant(ctx, accountID, "rate-card-"+uuid.NewString()[:8], "Rate card tenant", 250)
	if err != nil {
		t.Fatal(err)
	}
	effective := time.Date(2026, 9, 25, 12, 1, 0, 0, time.UTC)
	newer, err := store.CreatePlatformTenantRateCard(ctx, accountID, tenant.ID, "EUR", 2500, effective)
	if err != nil {
		t.Fatalf("create newer: %v", err)
	}
	if _, err := store.CreatePlatformTenantRateCard(ctx, accountID, tenant.ID, "EUR", 3000, effective); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate effective minute = %v, want ErrConflict", err)
	}
	if _, err := store.CreatePlatformTenantRateCard(ctx, accountID, tenant.ID, "USD", 3000, effective.Add(time.Minute)); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("currency change = %v, want ErrInvalidArgument", err)
	}
	if _, err := store.CreatePlatformTenantRateCard(ctx, uuid.NewString(), tenant.ID, "EUR", 3000, effective.Add(time.Minute)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account create = %v, want ErrNotFound", err)
	}
	if _, err := store.CreatePlatformTenantRateCard(ctx, accountID, tenant.ID, "EUR", 2000, effective.Add(-time.Minute)); err != nil {
		t.Fatalf("create older: %v", err)
	}
	cards, err := store.ListPlatformTenantRateCards(ctx, accountID, tenant.ID)
	if err != nil || len(cards) != 2 || cards[0].PriceMillicentsPerUnit != 2000 || cards[1].ID != newer.ID {
		t.Fatalf("cards = %+v, err=%v; want chronological tenant history", cards, err)
	}
}
