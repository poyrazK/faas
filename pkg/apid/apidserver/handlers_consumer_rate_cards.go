package apidserver

import (
	"context"
	"errors"
	"fmt"
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
		PriceMillicentsPerUnit: card.PriceMillicentsPerUnit, IncludedUnitsPerMonth: card.IncludedUnitsPerMonth,
		Tiers: apiRateCardTiers(card.Tiers), RouteWeights: card.RouteWeights, PlanID: card.PlanID,
		EffectiveFrom: card.EffectiveFrom.UTC(), CreatedAt: card.CreatedAt.UTC(),
	}
}

func apiRateCardTiers(tiers []state.APIConsumerRateCardTier) []api.APIConsumerRateCardTier {
	if len(tiers) == 0 {
		return nil
	}
	out := make([]api.APIConsumerRateCardTier, len(tiers))
	for i, tier := range tiers {
		out[i] = api.APIConsumerRateCardTier{UpTo: tier.UpTo, PriceMillicentsPerUnit: tier.PriceMillicentsPerUnit}
	}
	return out
}

func stateRateCardTiers(tiers []api.APIConsumerRateCardTier) []state.APIConsumerRateCardTier {
	if len(tiers) == 0 {
		return nil
	}
	out := make([]state.APIConsumerRateCardTier, len(tiers))
	for i, tier := range tiers {
		out[i] = state.APIConsumerRateCardTier{UpTo: tier.UpTo, PriceMillicentsPerUnit: tier.PriceMillicentsPerUnit}
	}
	return out
}

// rateCardAllowanceProblem rejects a card that would change which past
// minutes were free or in which ladder step (ADR-950, ADR-951). Usage is
// consumed in minute order from the start of each month, so once any card
// carries an allowance or tiers, a backdated card could re-split units that
// statements already billed.
func rateCardAllowanceProblem(req api.CreateAPIConsumerRateCardRequest, effectiveFrom time.Time, existing []state.APIConsumerRateCard) *api.Problem {
	invalid := func(detail string) *api.Problem {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid rate card", detail)
	}
	if req.IncludedUnitsPerMonth < 0 {
		return invalid("included_units_per_month must be non-negative")
	}
	if err := state.ValidateAPIConsumerRouteWeights(req.RouteWeights); err != nil {
		return invalid(strings.TrimPrefix(err.Error(), "rate card route weights: ") + " (route_weights)")
	}
	if len(req.Tiers) > 0 {
		if err := state.ValidateAPIConsumerRateCardTiers(stateRateCardTiers(req.Tiers)); err != nil {
			return invalid(strings.TrimPrefix(err.Error(), "rate card tiers: ") + " (tiers)")
		}
		if req.IncludedUnitsPerMonth != 0 {
			return invalid("tiers replace included_units_per_month; make the first step free instead")
		}
	}
	if problem := rateCardUnitProblem(req, effectiveFrom, existing); problem != nil {
		return problem
	}
	// Plan prices (ADR-953) are never backdated either: assignments price
	// minutes by the plan's history, so a backdated plan card would re-price
	// minutes that statements may already have billed. Compute and
	// response-size cards (ADR-1308) re-price minutes the same way.
	positional := req.IncludedUnitsPerMonth > 0 || len(req.Tiers) > 0 || len(req.RouteWeights) > 0 || req.PlanID != "" ||
		(req.Unit != "" && req.Unit != state.APIConsumerRateCardUnitRequest)
	for _, card := range existing {
		positional = positional || card.IncludedUnitsPerMonth > 0 || len(card.Tiers) > 0 || len(card.RouteWeights) > 0 || card.PlanID != "" ||
			card.Unit != state.APIConsumerRateCardUnitRequest
	}
	if positional && effectiveFrom.Before(time.Now().UTC().Truncate(time.Minute)) {
		return invalid("effective_from cannot be in the past once a rate card includes units, tiers, route weights, plans, or compute or response-size units")
	}
	return nil
}

// rateCardUnitProblem checks a card's unit (ADR-1308): known, without route
// weights unless it prices requests, and changing the price history's unit
// only at a UTC month boundary, so allowances and tiers never count two
// kinds of unit in one month.
func rateCardUnitProblem(req api.CreateAPIConsumerRateCardRequest, effectiveFrom time.Time, existing []state.APIConsumerRateCard) *api.Problem {
	invalid := func(detail string) *api.Problem {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid rate card", detail)
	}
	unit := req.Unit
	if unit == "" {
		unit = state.APIConsumerRateCardUnitRequest
	}
	if !state.ValidAPIConsumerRateCardUnit(unit) {
		return invalid("unit must be request, compute_second or response_megabyte")
	}
	if unit != state.APIConsumerRateCardUnitRequest && len(req.RouteWeights) > 0 {
		return invalid("route_weights apply to request cards only")
	}
	var previous *state.APIConsumerRateCard
	for i, card := range existing {
		if card.PlanID == req.PlanID && card.EffectiveFrom.Before(effectiveFrom) &&
			(previous == nil || card.EffectiveFrom.After(previous.EffectiveFrom)) {
			previous = &existing[i]
		}
	}
	if previous != nil && previous.Unit != unit && !effectiveFrom.Equal(billing.MonthStart(effectiveFrom)) {
		return invalid("a card that changes the unit must start at a UTC month boundary (effective_from on the 1st at 00:00 UTC)")
	}
	return nil
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
	if problem := rateCardAllowanceProblem(req, effectiveFrom, existing); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	card, err := store.CreateAPIConsumerRateCardVersion(r.Context(), state.APIConsumerRateCardInput{
		AccountID: acct.ID, AppID: app.ID, Currency: req.Currency, PriceMillicentsPerUnit: req.PriceMillicentsPerUnit,
		IncludedUnitsPerMonth: req.IncludedUnitsPerMonth, Tiers: stateRateCardTiers(req.Tiers),
		RouteWeights: req.RouteWeights, PlanID: req.PlanID, EffectiveFrom: effectiveFrom, Unit: req.Unit,
	})
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such consumer plan")
			return
		}
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
		"included_units_per_month":  card.IncludedUnitsPerMonth,
		"tier_count":                len(card.Tiers),
		"route_weight_count":        len(card.RouteWeights),
		"plan_id":                   card.PlanID,
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
	app, _, ok := s.apiConsumerRateCardStore(w, r, acct)
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
	quote, err := s.quoteConsumerUsage(r.Context(), acct.ID, app.ID, consumer.ID, since, until)
	if err != nil {
		s.log.ErrorContext(r.Context(), "quote api consumer usage", "app_id", app.ID, "consumer_id", consumer.ID, "err", err)
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
			ChargedUnits:           bucket.ChargedUnits,
			TierUnits:              bucket.TierUnits,
			AmountMillicents:       bucket.AmountMillicents,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// quoteConsumerUsage prices a consumer's usage in [since, until) exactly as
// statements do (ADR-950–953): earlier usage in since's month consumes the
// monthly allowance first. The quote endpoint and consumer billing
// self-service (ADR-1304) share it.
func (s *server) quoteConsumerUsage(ctx context.Context, accountID, appID, consumerID string, since, until time.Time) (billing.APIConsumerUsageQuote, error) {
	usageStore, okUsage := s.store.(state.ConsumerUsageStore)
	cardsStore, okCards := s.store.(state.APIConsumerRateCardStore)
	if !okUsage || !okCards {
		return billing.APIConsumerUsageQuote{}, errors.New("consumer pricing is unavailable")
	}
	cards, err := cardsStore.ListAPIConsumerRateCardsForApp(ctx, accountID, appID)
	if err == nil {
		cards, err = s.consumerPriceHistory(ctx, cards, accountID, appID, consumerID)
	}
	if err != nil {
		return billing.APIConsumerUsageQuote{}, fmt.Errorf("load rate cards: %w", err)
	}
	// A same-day window on the 1st snaps to an empty one, which the ledger
	// rejects; it has no usage to price (hunt #9 H9-57).
	var usage []state.APIConsumerUsageBucket
	if until.After(billing.MonthStart(since)) {
		if usage, err = usageStore.ListAPIConsumerUsage(ctx, accountID, appID, consumerID, billing.MonthStart(since), until); err != nil {
			return billing.APIConsumerUsageQuote{}, fmt.Errorf("list usage: %w", err)
		}
	}
	if usage, err = s.weightConsumerUsage(ctx, cards, usage, accountID, appID, consumerID, billing.MonthStart(since), until); err != nil {
		return billing.APIConsumerUsageQuote{}, fmt.Errorf("weight route usage: %w", err)
	}
	return billing.QuoteAPIConsumerUsageFrom(cards, usage, since)
}

func isUpperASCIICurrency(currency string) bool {
	for i := 0; i < len(currency); i++ {
		if currency[i] < 'A' || currency[i] > 'Z' {
			return false
		}
	}
	return true
}

// weightConsumerUsage converts minutes into the units the effective cards
// price: route-weighted requests (ADR-952), or compute seconds and response
// megabytes (ADR-1308). Route rows are read only when some card weights
// routes.
func (s *server) weightConsumerUsage(ctx context.Context, cards []state.APIConsumerRateCard, usage []state.APIConsumerUsageBucket,
	accountID, appID, consumerID string, since, until time.Time) ([]state.APIConsumerUsageBucket, error) {
	weighted := false
	for _, card := range cards {
		weighted = weighted || len(card.RouteWeights) > 0
	}
	routeStore, ok := s.store.(state.ConsumerRouteUsageStore)
	if weighted && ok && until.After(since) {
		routes, err := routeStore.ListAPIConsumerRouteUsage(ctx, accountID, appID, consumerID, since, until)
		if err != nil {
			return nil, err
		}
		if usage, err = billing.WeightAPIConsumerUsage(cards, usage, routes); err != nil {
			return nil, err
		}
	}
	return billing.MeterAPIConsumerUsage(cards, usage), nil
}
