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
	req.Currency = strings.ToUpper(strings.TrimSpace(req.Currency))
	if req.Currency == "" {
		req.Currency = "EUR"
	}
	if len(req.Currency) != 3 || !isUpperASCIICurrency(req.Currency) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid tenant rate card", "currency must be a three-letter uppercase ISO code"))
		return
	}
	if req.PriceMillicentsPerUnit < 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid tenant rate card", "price_millicents_per_unit must be non-negative"))
		return
	}
	effectiveFrom := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
	if req.EffectiveFrom != nil {
		effectiveFrom = req.EffectiveFrom.UTC()
		if !effectiveFrom.Equal(effectiveFrom.Truncate(time.Minute)) {
			api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
				"Invalid tenant rate card", "effective_from must be a UTC minute"))
			return
		}
	}
	card, err := store.CreatePlatformTenantRateCard(r.Context(), acct.ID, tenant.ID, req.Currency, req.PriceMillicentsPerUnit, effectiveFrom)
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Tenant rate card already exists", "another rate card already uses this effective_from"))
		return
	}
	if errors.Is(err, state.ErrInvalidArgument) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid tenant rate card", "all versions for a tenant must use the same currency"))
		return
	}
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such platform tenant")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not create platform tenant rate card"))
		return
	}
	s.audit.Emit(r.Context(), "platform_tenant_rate_card.created", &acct.ID, map[string]any{
		"platform_tenant_id": tenant.ID, "rate_card_id": card.ID, "currency": card.Currency,
		"price_millicents_per_unit": card.PriceMillicentsPerUnit,
		"effective_from":            card.EffectiveFrom.UTC().Format(time.RFC3339),
	})
	writeJSON(w, http.StatusCreated, platformTenantRateCardResponse(card))
}
