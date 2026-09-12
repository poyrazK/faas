package main

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

func apiConsumerRateCardResponse(card state.APIConsumerRateCard) api.APIConsumerRateCardResponse {
	return api.APIConsumerRateCardResponse{
		ID: card.ID, AppID: card.AppID, Currency: card.Currency, Unit: card.Unit,
		PriceMillicentsPerUnit: card.PriceMillicentsPerUnit,
		EffectiveFrom:          card.EffectiveFrom.UTC(), CreatedAt: card.CreatedAt.UTC(),
	}
}

func (s *server) apiConsumerRateCardStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.APIConsumerRateCardStore, bool) {
	if !s.consumerFeatureAllowed(w, acct) {
		return state.App{}, nil, false
	}
	app, ok := s.consumerApp(w, r, acct)
	if !ok {
		return state.App{}, nil, false
	}
	store, ok := s.store.(state.APIConsumerRateCardStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("API consumer pricing is unavailable"))
		return state.App{}, nil, false
	}
	return app, store, true
}

func (s *server) listAPIConsumerRateCards(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.apiConsumerRateCardStore(w, r, acct)
	if !ok {
		return
	}
	cards, err := store.ListAPIConsumerRateCardsForApp(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list API consumer rate cards"))
		return
	}
	out := api.APIConsumerRateCardListResponse{RateCards: make([]api.APIConsumerRateCardResponse, 0, len(cards))}
	for _, card := range cards {
		out.RateCards = append(out.RateCards, apiConsumerRateCardResponse(card))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createAPIConsumerRateCard(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.apiConsumerRateCardStore(w, r, acct)
	if !ok {
		return
	}
	var req api.CreateAPIConsumerRateCardRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	req.Currency = strings.ToUpper(strings.TrimSpace(req.Currency))
	if req.Currency == "" {
		req.Currency = "EUR"
	}
	if len(req.Currency) != 3 || !isUpperASCIICurrency(req.Currency) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid rate card", "currency must be a three-letter uppercase ISO code"))
		return
	}
	if req.PriceMillicentsPerUnit < 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid rate card", "price_millicents_per_unit must be non-negative"))
		return
	}
	effectiveFrom := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
	if req.EffectiveFrom != nil {
		effectiveFrom = req.EffectiveFrom.UTC()
		if !effectiveFrom.Equal(effectiveFrom.Truncate(time.Minute)) {
			api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
				"Invalid rate card", "effective_from must be a UTC minute"))
			return
		}
	}
	// One currency per app keeps a quote's total meaningful. Cards are
	// append-only, so a currency change would make a later invoice ambiguous.
	existing, err := store.ListAPIConsumerRateCardsForApp(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not inspect API consumer rate cards"))
		return
	}
	for _, card := range existing {
		if card.Currency != req.Currency {
			api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
				"Invalid rate card", "an app's rate cards must use one currency"))
			return
		}
	}
	card, err := store.CreateAPIConsumerRateCard(r.Context(), acct.ID, app.ID, req.Currency, req.PriceMillicentsPerUnit, effectiveFrom)
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
				"Rate card already exists", "another rate card already uses this effective_from"))
			return
		}
		api.WriteProblem(w, api.ErrInternal("could not create API consumer rate card"))
		return
	}
	s.audit.Emit(r.Context(), "api_consumer_rate_card.created", &acct.ID, map[string]any{
		"app_id": app.ID, "rate_card_id": card.ID, "currency": card.Currency,
		"price_millicents_per_unit": card.PriceMillicentsPerUnit,
		"effective_from":            card.EffectiveFrom.UTC().Format(time.RFC3339),
	})
	writeJSON(w, http.StatusCreated, apiConsumerRateCardResponse(card))
}

func (s *server) getAPIConsumerRateCard(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.apiConsumerRateCardStore(w, r, acct)
	if !ok {
		return
	}
	card, err := store.GetAPIConsumerRateCardByID(r.Context(), acct.ID, r.PathValue("rate_card_id"))
	if err != nil || card.AppID != app.ID {
		s.notFound(w, "no such rate card")
		return
	}
	writeJSON(w, http.StatusOK, apiConsumerRateCardResponse(card))
}

func (s *server) getAPIConsumerUsageQuote(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, cardsStore, ok := s.apiConsumerRateCardStore(w, r, acct)
	if !ok {
		return
	}
	consumer, err := s.store.GetAPIConsumerByID(r.Context(), acct.ID, r.PathValue("consumer_id"))
	if err != nil || consumer.AppID != app.ID {
		s.notFound(w, "no such consumer")
		return
	}
	since, until, windowErr := parseUsageWindow(r, time.Now().UTC())
	if windowErr != nil {
		api.WriteProblem(w, windowErr)
		return
	}
	usageStore, ok := s.store.(state.ConsumerUsageStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("consumer usage ledger is unavailable"))
		return
	}
	cards, err := cardsStore.ListAPIConsumerRateCardsForApp(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load API consumer rate cards"))
		return
	}
	usage, err := usageStore.ListAPIConsumerUsage(r.Context(), acct.ID, app.ID, consumer.ID, since, until)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load API consumer usage"))
		return
	}
	quote, err := billing.QuoteAPIConsumerUsage(cards, usage)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not calculate API consumer usage quote"))
		return
	}
	out := api.APIConsumerUsageQuoteResponse{
		ConsumerID: consumer.ID, PeriodStart: since.UTC(), PeriodEnd: until.UTC(),
		Currency: quote.Currency, BillableUnits: quote.BillableUnits,
		UnpricedUnits: quote.UnpricedUnits, AmountMillicents: quote.AmountMillicents,
		Priced: quote.Priced, Buckets: make([]api.APIConsumerUsageQuoteBucketResponse, 0, len(quote.Buckets)),
		AsOf: time.Now().UTC().Format(time.RFC3339Nano),
	}
	for _, bucket := range quote.Buckets {
		out.Buckets = append(out.Buckets, api.APIConsumerUsageQuoteBucketResponse{
			WindowStart: bucket.WindowStart, BillableUnits: bucket.BillableUnits,
			RateCardID: bucket.RateCardID, Currency: bucket.Currency,
			PriceMillicentsPerUnit: bucket.PriceMillicentsPerUnit,
			AmountMillicents:       bucket.AmountMillicents,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func isUpperASCIICurrency(currency string) bool {
	for i := 0; i < len(currency); i++ {
		if currency[i] < 'A' || currency[i] > 'Z' {
			return false
		}
	}
	return true
}
