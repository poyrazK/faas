package main

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func platformTenantRateCardResponse(card state.PlatformTenantRateCard) api.PlatformTenantRateCardResponse {
	return api.PlatformTenantRateCardResponse{ID: card.ID, TenantID: card.TenantID,
		Currency: card.Currency, Unit: card.Unit, PriceMillicentsPerUnit: card.PriceMillicentsPerUnit,
		IncludedUnitsPerMonth: card.IncludedUnitsPerMonth, Tiers: apiRateCardTiers(card.Tiers),
		EffectiveFrom: card.EffectiveFrom.UTC(), CreatedAt: card.CreatedAt.UTC()}
}

func (s *server) platformTenantRateCardStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.PlatformTenant, state.PlatformTenantRateCardStore, bool) {
	tenants, ok := s.platformTenantStore(w, acct)
	if !ok {
		return state.PlatformTenant{}, nil, false
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, tenants)
	if !ok {
		return state.PlatformTenant{}, nil, false
	}
	store, ok := s.store.(state.PlatformTenantRateCardStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant pricing is unavailable"))
	}
	return tenant, store, ok
}

func (s *server) listPlatformTenantRateCards(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, store, ok := s.platformTenantRateCardStore(w, r, acct)
	if !ok {
		return
	}
	cards, err := store.ListPlatformTenantRateCards(r.Context(), acct.ID, tenant.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list platform tenant rate cards"))
		return
	}
	out := api.PlatformTenantRateCardListResponse{RateCards: make([]api.PlatformTenantRateCardResponse, 0, len(cards))}
	for _, card := range cards {
		out.RateCards = append(out.RateCards, platformTenantRateCardResponse(card))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createPlatformTenantRateCard(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, store, ok := s.platformTenantRateCardStore(w, r, acct)
	if !ok {
		return
	}
	var req api.CreatePlatformTenantRateCardRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	existing, err := store.ListPlatformTenantRateCards(r.Context(), acct.ID, tenant.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load platform tenant rate cards"))
		return
	}
	input, problem := platformTenantRateCardInput(req, acct.ID, tenant.ID, existing)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	card, err := store.CreatePlatformTenantRateCardVersion(r.Context(), input)
	if problem := platformTenantRateCardError(err); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.audit.Emit(r.Context(), "platform_tenant_rate_card.created", &acct.ID, map[string]any{
		"platform_tenant_id": tenant.ID, "rate_card_id": card.ID, "currency": card.Currency,
		"price_millicents_per_unit": card.PriceMillicentsPerUnit,
		"included_units_per_month":  card.IncludedUnitsPerMonth, "tiers": len(card.Tiers),
		"effective_from": card.EffectiveFrom.UTC().Format(time.RFC3339),
	})
	writeJSON(w, http.StatusCreated, platformTenantRateCardResponse(card))
}

// platformTenantRateCardInput validates a tenant price version. Once any
// tenant card counts units by month position (ADR-939), a backdated card
// could re-split units that statements already billed, so effective_from
// cannot be in the past.
func platformTenantRateCardInput(req api.CreatePlatformTenantRateCardRequest, accountID, tenantID string,
	existing []state.PlatformTenantRateCard) (state.PlatformTenantRateCardInput, *api.Problem) {
	invalid := func(detail string) *api.Problem {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid tenant rate card", detail)
	}
	in := state.PlatformTenantRateCardInput{AccountID: accountID, TenantID: tenantID,
		Currency: strings.ToUpper(strings.TrimSpace(req.Currency)), PriceMillicentsPerUnit: req.PriceMillicentsPerUnit,
		IncludedUnitsPerMonth: req.IncludedUnitsPerMonth, Tiers: stateRateCardTiers(req.Tiers),
		EffectiveFrom: time.Now().UTC().Truncate(time.Minute).Add(time.Minute)}
	if in.Currency == "" {
		in.Currency = "EUR"
	}
	switch {
	case len(in.Currency) != 3 || !isUpperASCIICurrency(in.Currency):
		return in, invalid("currency must be a three-letter uppercase ISO code")
	case req.PriceMillicentsPerUnit < 0:
		return in, invalid("price_millicents_per_unit must be non-negative")
	case req.IncludedUnitsPerMonth < 0:
		return in, invalid("included_units_per_month must be non-negative")
	case len(req.Tiers) > 0 && req.IncludedUnitsPerMonth != 0:
		return in, invalid("tiers replace included_units_per_month; make the first step free instead")
	}
	if err := state.ValidateAPIConsumerRateCardTiers(in.Tiers); err != nil {
		return in, invalid(strings.TrimPrefix(err.Error(), "rate card tiers: ") + " (tiers)")
	}
	if req.EffectiveFrom != nil {
		in.EffectiveFrom = req.EffectiveFrom.UTC()
		if !in.EffectiveFrom.Equal(in.EffectiveFrom.Truncate(time.Minute)) {
			return in, invalid("effective_from must be a UTC minute")
		}
	}
	monthly := req.IncludedUnitsPerMonth > 0 || len(req.Tiers) > 0
	for _, card := range existing {
		monthly = monthly || card.MonthlyPricing()
	}
	if monthly && in.EffectiveFrom.Before(time.Now().UTC().Truncate(time.Minute)) {
		return in, invalid("effective_from cannot be in the past once a tenant rate card includes units or tiers")
	}
	return in, nil
}

func platformTenantRateCardError(err error) *api.Problem {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, state.ErrConflict):
		return api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Tenant rate card already exists", "another rate card already uses this effective_from")
	case errors.Is(err, state.ErrInvalidArgument):
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid tenant rate card", "all versions for a tenant must use the same currency")
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such platform tenant")
	default:
		return api.ErrInternal("could not create platform tenant rate card")
	}
}
