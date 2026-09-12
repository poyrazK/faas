package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAPIConsumerRateCardsAndUsageQuote(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "consumer-pricing-app")
	created := e.do(t, http.MethodPost, "/v1/apps/consumer-pricing-app/consumers", api.CreateAPIConsumerRequest{
		ExternalRef: "priced-customer", Name: "Priced Customer",
	}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create consumer: %d %s", created.Code, created.Body)
	}
	var consumer api.APIConsumerResponse
	if err := json.Unmarshal(created.Body.Bytes(), &consumer); err != nil {
		t.Fatalf("decode consumer: %v", err)
	}

	minute := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Minute)
	rate := e.do(t, http.MethodPost, "/v1/apps/consumer-pricing-app/rate-cards", api.CreateAPIConsumerRateCardRequest{
		Currency: "eur", PriceMillicentsPerUnit: 125, EffectiveFrom: &minute,
	}, nil)
	if rate.Code != http.StatusCreated {
		t.Fatalf("create rate card: %d %s", rate.Code, rate.Body)
	}
	var card api.APIConsumerRateCardResponse
	if err := json.Unmarshal(rate.Body.Bytes(), &card); err != nil {
		t.Fatalf("decode rate card: %v", err)
	}
	if card.Currency != "EUR" || card.Unit != "request" || card.PriceMillicentsPerUnit != 125 || card.ID == "" {
		t.Fatalf("rate card = %+v", card)
	}

	listed := e.do(t, http.MethodGet, "/v1/apps/consumer-pricing-app/rate-cards", nil, nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list rate cards: %d %s", listed.Code, listed.Body)
	}
	var cards api.APIConsumerRateCardListResponse
	if err := json.Unmarshal(listed.Body.Bytes(), &cards); err != nil {
		t.Fatalf("decode rate cards: %v", err)
	}
	if len(cards.RateCards) != 1 || cards.RateCards[0].ID != card.ID {
		t.Fatalf("rate cards = %+v", cards)
	}

	if _, err := e.store.RecordAPIConsumerUsage(context.Background(), state.APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: e.acct.ID, AppID: consumer.AppID,
		ConsumerKey: consumer.ID, WindowStart: minute,
		RequestCount: 4, ErrorCount: 1, BillableUnits: 4,
	}); err != nil {
		t.Fatalf("record usage: %v", err)
	}
	quotePath := "/v1/apps/consumer-pricing-app/consumers/" + consumer.ID + "/usage/quote"
	quoted := e.do(t, http.MethodGet, quotePath, nil, nil)
	if quoted.Code != http.StatusOK {
		t.Fatalf("quote usage: %d %s", quoted.Code, quoted.Body)
	}
	var quote api.APIConsumerUsageQuoteResponse
	if err := json.Unmarshal(quoted.Body.Bytes(), &quote); err != nil {
		t.Fatalf("decode quote: %v", err)
	}
	if !quote.Priced || quote.Currency != "EUR" || quote.BillableUnits != 4 || quote.UnpricedUnits != 0 || quote.AmountMillicents != 500 || len(quote.Buckets) != 1 {
		t.Fatalf("quote = %+v", quote)
	}
	if quote.Buckets[0].RateCardID != card.ID || quote.Buckets[0].AmountMillicents != 500 {
		t.Fatalf("quote bucket = %+v", quote.Buckets[0])
	}

	gotCard := e.do(t, http.MethodGet, "/v1/apps/consumer-pricing-app/rate-cards/"+card.ID, nil, nil)
	if gotCard.Code != http.StatusOK {
		t.Fatalf("get rate card: %d %s", gotCard.Code, gotCard.Body)
	}
}

func TestAPIConsumerRateCardCurrencyAndPlanGates(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "consumer-pricing-gates")
	first := e.do(t, http.MethodPost, "/v1/apps/consumer-pricing-gates/rate-cards", api.CreateAPIConsumerRateCardRequest{
		Currency: "EUR", PriceMillicentsPerUnit: 1,
	}, nil)
	if first.Code != http.StatusCreated {
		t.Fatalf("create first rate card: %d %s", first.Code, first.Body)
	}
	second := e.do(t, http.MethodPost, "/v1/apps/consumer-pricing-gates/rate-cards", api.CreateAPIConsumerRateCardRequest{
		Currency: "USD", PriceMillicentsPerUnit: 1,
	}, nil)
	if second.Code != http.StatusUnprocessableEntity {
		t.Fatalf("currency change: got %d %s, want 422", second.Code, second.Body)
	}

	free := setup(t, api.PlanFree)
	mustSeedApp(t, free, "consumer-pricing-free")
	blocked := free.do(t, http.MethodGet, "/v1/apps/consumer-pricing-free/rate-cards", nil, nil)
	assertProblem(t, blocked, http.StatusPaymentRequired, api.CodeConsumerKeysNotAllowed)
}
