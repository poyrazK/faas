package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemStoreAPIConsumerRateCardsVersionedAndSorted(t *testing.T) {
	store := NewMemStore()
	accountID, appID := uuid.NewString(), uuid.NewString()
	newer := time.Date(2026, 9, 12, 12, 1, 0, 0, time.UTC)
	older := newer.Add(-time.Minute)
	card, err := store.CreateAPIConsumerRateCard(context.Background(), accountID, appID, "eur", 1250, newer)
	if err != nil {
		t.Fatalf("create newer rate card: %v", err)
	}
	if card.Currency != "EUR" || card.Unit != APIConsumerRateCardUnitRequest {
		t.Fatalf("normalized card = %+v", card)
	}
	if _, err := store.CreateAPIConsumerRateCard(context.Background(), accountID, appID, "EUR", 500, older); err != nil {
		t.Fatalf("create older rate card: %v", err)
	}
	if _, err := store.CreateAPIConsumerRateCard(context.Background(), accountID, appID, "EUR", 750, older); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate effective_from = %v, want ErrConflict", err)
	}
	cards, err := store.ListAPIConsumerRateCardsForApp(context.Background(), accountID, appID)
	if err != nil {
		t.Fatalf("list rate cards: %v", err)
	}
	if len(cards) != 2 || !cards[0].EffectiveFrom.Equal(older) || !cards[1].EffectiveFrom.Equal(newer) {
		t.Fatalf("cards = %+v, want chronological versions", cards)
	}
	back, err := store.GetAPIConsumerRateCardByID(context.Background(), accountID, card.ID)
	if err != nil || back.ID != card.ID {
		t.Fatalf("get rate card = %+v, %v", back, err)
	}
}

func TestMemStoreAPIConsumerRateCardsRejectInvalidMinute(t *testing.T) {
	store := NewMemStore()
	_, err := store.CreateAPIConsumerRateCard(context.Background(), uuid.NewString(), uuid.NewString(), "EUR", 1, time.Now().UTC().Truncate(time.Minute).Add(30*time.Second))
	if err == nil {
		t.Fatal("rate card with partial minute: expected error")
	}
}
