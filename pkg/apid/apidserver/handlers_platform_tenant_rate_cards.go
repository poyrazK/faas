package apidserver

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

func platformTenantRateCardResponse(card state.PlatformTenantRateCard) api.PlatformTenantRateCardResponse {
	return api.PlatformTenantRateCardResponse{ID: card.ID, TenantID: card.TenantID,
		Currency: card.Currency, Unit: card.Unit, PriceMillicentsPerUnit: card.PriceMillicentsPerUnit,
		IncludedUnitsPerMonth: card.IncludedUnitsPerMonth, Tiers: apiRateCardTiers(card.Tiers), TierMode: rateCardTierMode(card.TierMode),
		RouteWeights: card.RouteWeights, EffectiveFrom: card.EffectiveFrom.UTC(), CreatedAt: card.CreatedAt.UTC()}
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
	card, problem := s.createTenantRateCard(r.Context(), acct, tenant.ID, store, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusCreated, platformTenantRateCardResponse(card))
}

// createTenantRateCard validates and stores a tenant price version and
// audits it. The handler and tenant plan assignment (ADR-1307) share it.
func (s *server) createTenantRateCard(ctx context.Context, acct state.Account, tenantID string, store state.PlatformTenantRateCardStore,
	req api.CreatePlatformTenantRateCardRequest) (state.PlatformTenantRateCard, *api.Problem) {
	existing, err := store.ListPlatformTenantRateCards(ctx, acct.ID, tenantID)
	if err != nil {
		return state.PlatformTenantRateCard{}, api.ErrInternal("could not load platform tenant rate cards")
	}
	input, problem := platformTenantRateCardInput(req, acct.ID, tenantID, existing)
	if problem != nil {
		return state.PlatformTenantRateCard{}, problem
	}
	card, err := store.CreatePlatformTenantRateCardVersion(ctx, input)
	if problem := platformTenantRateCardError(err); problem != nil {
		return state.PlatformTenantRateCard{}, problem
	}
	s.audit.Emit(ctx, "platform_tenant_rate_card.created", &acct.ID, map[string]any{
		"platform_tenant_id": tenantID, "rate_card_id": card.ID, "currency": card.Currency,
		"price_millicents_per_unit": card.PriceMillicentsPerUnit,
		"included_units_per_month":  card.IncludedUnitsPerMonth, "tiers": len(card.Tiers), "route_weight_count": len(card.RouteWeights),
		"effective_from": card.EffectiveFrom.UTC().Format(time.RFC3339),
	})
	return card, nil
}

// platformTenantRateCardInput validates a tenant price version. Once any
// tenant card counts units by month position (ADR-975), a backdated card
// could re-split units that statements already billed, so effective_from
// cannot be in the past.
func platformTenantRateCardInput(req api.CreatePlatformTenantRateCardRequest, accountID, tenantID string,
	existing []state.PlatformTenantRateCard) (state.PlatformTenantRateCardInput, *api.Problem) {
	invalid := func(detail string) *api.Problem {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid tenant rate card", detail)
	}
	in := state.PlatformTenantRateCardInput{AccountID: accountID, TenantID: tenantID,
		Currency: strings.ToUpper(strings.TrimSpace(req.Currency)), PriceMillicentsPerUnit: req.PriceMillicentsPerUnit,
		IncludedUnitsPerMonth: req.IncludedUnitsPerMonth, Tiers: stateRateCardTiers(req.Tiers), RouteWeights: req.RouteWeights,
		EffectiveFrom: time.Now().UTC().Truncate(time.Minute).Add(time.Minute), Unit: req.Unit, TierMode: req.TierMode}
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
	if err := state.ValidateAPIConsumerRouteWeights(in.RouteWeights); err != nil {
		return in, invalid(strings.TrimPrefix(err.Error(), "rate card route weights: ") + " (route_weights)")
	}
	if req.EffectiveFrom != nil {
		in.EffectiveFrom = req.EffectiveFrom.UTC()
		if !in.EffectiveFrom.Equal(in.EffectiveFrom.Truncate(time.Minute)) {
			return in, invalid("effective_from must be a UTC minute")
		}
	}
	if detail := tenantCardUnitProblem(in, existing); detail != "" {
		return in, invalid(detail)
	}
	monthly := state.PlatformTenantRateCard{IncludedUnitsPerMonth: in.IncludedUnitsPerMonth, Tiers: in.Tiers,
		RouteWeights: in.RouteWeights, Unit: in.Unit}.MonthlyPricing()
	for _, card := range existing {
		monthly = monthly || card.MonthlyPricing()
	}
	if monthly && in.EffectiveFrom.Before(time.Now().UTC().Truncate(time.Minute)) {
		return in, invalid("effective_from cannot be in the past once a tenant rate card includes units, tiers, route weights, or compute or response-size units")
	}
	return in, nil
}

// tenantCardUnitProblem checks a tenant card's unit (ADR-1310) and ladder
// mode (ADR-1309): known values, route weights only on request cards, and a
// change of unit only at a UTC month boundary, so one month's allowance and
// tiers never count two kinds of unit.
func tenantCardUnitProblem(in state.PlatformTenantRateCardInput, existing []state.PlatformTenantRateCard) string {
	unit := in.Unit
	if unit == "" {
		unit = state.PlatformTenantRateCardUnitRequest
	}
	switch {
	case !state.ValidAPIConsumerRateCardUnit(unit):
		return "unit must be request, compute_second or response_megabyte"
	case unit != state.PlatformTenantRateCardUnitRequest && len(in.RouteWeights) > 0:
		return "route_weights apply to request cards only"
	case in.TierMode != "" && in.TierMode != state.RateCardTierModeGraduated && in.TierMode != state.RateCardTierModeVolume:
		return "tier_mode must be graduated or volume"
	case in.TierMode == state.RateCardTierModeVolume && len(in.Tiers) == 0:
		return "tier_mode volume needs tiers"
	}
	var previous *state.PlatformTenantRateCard
	for i, card := range existing {
		if card.EffectiveFrom.Before(in.EffectiveFrom) && (previous == nil || card.EffectiveFrom.After(previous.EffectiveFrom)) {
			previous = &existing[i]
		}
	}
	if previous != nil && tenantUnit(previous.Unit) != unit && !in.EffectiveFrom.Equal(billing.MonthStart(in.EffectiveFrom)) {
		return "a card that changes the unit must start at a UTC month boundary (effective_from on the 1st at 00:00 UTC)"
	}
	return ""
}

func tenantUnit(unit string) string {
	if unit == "" {
		return state.PlatformTenantRateCardUnitRequest
	}
	return unit
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
