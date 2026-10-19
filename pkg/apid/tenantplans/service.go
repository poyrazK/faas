// Package tenantplans serves platform tenant plans (ADR-1307): reusable
// bundles of tenant card terms and a request budget. Assigning a plan
// creates the tenant's rate card from the next minute and sets its budget.
// Like the other apid domains (ADR-982), it reaches apid only through Host;
// apidserver keeps the route table.
package tenantplans

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// Host is what tenant plans need from apid.
type Host interface {
	Store() state.Store
	// TenantsAllowed reports whether the account may use platform tenants;
	// on false it has written the problem.
	TenantsAllowed(w http.ResponseWriter, acct state.Account) bool
	// TenantByPath resolves the {id} tenant; on false it has written the
	// problem.
	TenantByPath(w http.ResponseWriter, r *http.Request, acct state.Account) (state.PlatformTenant, bool)
	// CreateTenantRateCard validates, stores and audits a tenant card.
	CreateTenantRateCard(ctx context.Context, acct state.Account, tenantID string, req api.CreatePlatformTenantRateCardRequest) (state.PlatformTenantRateCard, *api.Problem)
	// SetTenantRequestBudget sets a tenant's request budget (ADR-240).
	SetTenantRequestBudget(ctx context.Context, acct state.Account, tenantID string, perMinute, perDay int64) error
	Audit(ctx context.Context, event, accountID string, fields map[string]any)
}

// Service serves tenant plans.
type Service struct {
	Host Host
}

func (s *Service) store(w http.ResponseWriter, acct state.Account) (state.PlatformTenantPlanStore, bool) {
	if !s.Host.TenantsAllowed(w, acct) {
		return nil, false
	}
	store, ok := s.Host.Store().(state.PlatformTenantPlanStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant plans are unavailable"))
	}
	return store, ok
}

func planResponse(p state.PlatformTenantPlan) api.PlatformTenantPlanResponse {
	out := api.PlatformTenantPlanResponse{ID: p.ID, Name: p.Name, Currency: p.Currency, PriceMillicentsPerUnit: p.PriceMillicentsPerUnit,
		IncludedUnitsPerMonth: p.IncludedUnitsPerMonth, RouteWeights: p.RouteWeights, Unit: p.Unit, TierMode: p.TierMode,
		BaseFeeMillicentsPerMonth: p.BaseFeeMillicentsPerMonth, MinimumMillicentsPerMonth: p.MinimumMillicentsPerMonth,
		ProrateMonthlyFees: p.ProrateMonthlyFees, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
	if out.Unit == "" {
		out.Unit = state.PlatformTenantRateCardUnitRequest
	}
	if out.TierMode == "" {
		out.TierMode = state.RateCardTierModeGraduated
	}
	for _, tier := range p.Tiers {
		out.Tiers = append(out.Tiers, api.APIConsumerRateCardTier{UpTo: tier.UpTo, PriceMillicentsPerUnit: tier.PriceMillicentsPerUnit})
	}
	if p.MaxRequestsPerMinute != nil {
		out.RequestBudget = &api.PlatformTenantPlanBudget{MaxRequestsPerMinute: *p.MaxRequestsPerMinute, MaxRequestsPerDay: *p.MaxRequestsPerDay}
	}
	return out
}

func validation(detail string) *api.Problem {
	return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid tenant plan", detail)
}

// planFromRequest checks a plan body like a tenant rate card.
func planFromRequest(req api.PlatformTenantPlanRequest, accountID string) (state.PlatformTenantPlan, *api.Problem) {
	p := state.PlatformTenantPlan{AccountID: accountID, Name: strings.TrimSpace(req.Name),
		Currency: strings.ToUpper(strings.TrimSpace(req.Currency)), PriceMillicentsPerUnit: req.PriceMillicentsPerUnit,
		IncludedUnitsPerMonth: req.IncludedUnitsPerMonth, RouteWeights: req.RouteWeights, Unit: req.Unit, TierMode: req.TierMode,
		BaseFeeMillicentsPerMonth: req.BaseFeeMillicentsPerMonth, MinimumMillicentsPerMonth: req.MinimumMillicentsPerMonth,
		ProrateMonthlyFees: req.ProrateMonthlyFees}
	for _, tier := range req.Tiers {
		p.Tiers = append(p.Tiers, state.APIConsumerRateCardTier{UpTo: tier.UpTo, PriceMillicentsPerUnit: tier.PriceMillicentsPerUnit})
	}
	if b := req.RequestBudget; b != nil {
		if b.MaxRequestsPerMinute < 0 || b.MaxRequestsPerMinute > api.MaxPlatformTenantRequestsPerMinute ||
			b.MaxRequestsPerDay < 0 || b.MaxRequestsPerDay > api.MaxPlatformTenantRequestsPerDay {
			return p, validation("request_budget ceilings must be non-negative and within the supported safety bounds")
		}
		perMinute, perDay := b.MaxRequestsPerMinute, b.MaxRequestsPerDay
		p.MaxRequestsPerMinute, p.MaxRequestsPerDay = &perMinute, &perDay
	}
	if err := state.ValidatePlatformTenantPlan(p); err != nil {
		return p, validation(strings.TrimPrefix(err.Error(), state.ErrInvalidArgument.Error()+": "))
	}
	return p, nil
}

func storeProblem(err error) *api.Problem {
	switch {
	case errors.Is(err, state.ErrConflict):
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Plan name taken", "another tenant plan already uses this name")
	case errors.Is(err, state.ErrPlatformTenantPlanLimit):
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Too many tenant plans",
			fmt.Sprintf("an account keeps at most %d tenant plans", api.PlatformTenantPlansMax)).
			WithLimit(int64(api.PlatformTenantPlansMax), int64(api.PlatformTenantPlansMax))
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such tenant plan")
	case errors.Is(err, state.ErrInvalidArgument):
		return validation(err.Error())
	}
	return api.ErrInternal("could not store the tenant plan")
}

// CreatePlan creates a tenant plan.
func (s *Service) CreatePlan(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store(w, acct)
	if !ok {
		return
	}
	var req api.PlatformTenantPlanRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	plan, problem := planFromRequest(req, acct.ID)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	created, err := store.CreatePlatformTenantPlan(r.Context(), plan)
	if err != nil {
		api.WriteProblem(w, storeProblem(err))
		return
	}
	s.Host.Audit(r.Context(), "platform_tenant_plan.created", acct.ID, map[string]any{"plan_id": created.ID, "name": created.Name})
	httpkit.WriteJSON(w, http.StatusCreated, planResponse(created))
}

func (s *Service) ListPlans(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store(w, acct)
	if !ok {
		return
	}
	plans, err := store.ListPlatformTenantPlans(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list tenant plans"))
		return
	}
	out := api.PlatformTenantPlanListResponse{Plans: make([]api.PlatformTenantPlanResponse, 0, len(plans))}
	for _, p := range plans {
		out.Plans = append(out.Plans, planResponse(p))
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

func (s *Service) GetPlan(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store(w, acct)
	if !ok {
		return
	}
	plan, err := store.GetPlatformTenantPlan(r.Context(), acct.ID, r.PathValue("plan_id"))
	if err != nil {
		api.WriteProblem(w, storeProblem(err))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, planResponse(plan))
}

// UpdatePlan replaces a plan's name and terms. Tenants already on it keep
// their cards until the plan is assigned again.
func (s *Service) UpdatePlan(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store(w, acct)
	if !ok {
		return
	}
	var req api.PlatformTenantPlanRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	plan, problem := planFromRequest(req, acct.ID)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	plan.ID = r.PathValue("plan_id")
	updated, err := store.UpdatePlatformTenantPlan(r.Context(), plan)
	if err != nil {
		api.WriteProblem(w, storeProblem(err))
		return
	}
	s.Host.Audit(r.Context(), "platform_tenant_plan.updated", acct.ID, map[string]any{"plan_id": updated.ID, "name": updated.Name})
	httpkit.WriteJSON(w, http.StatusOK, planResponse(updated))
}

// AssignPlan creates the tenant's rate card from the plan, effective the
// next minute, stops the tenant's other priced units at that minute, sets
// the plan's request budget, and records the assignment.
func (s *Service) AssignPlan(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store(w, acct)
	if !ok {
		return
	}
	tenant, ok := s.Host.TenantByPath(w, r, acct)
	if !ok {
		return
	}
	var req api.AssignPlatformTenantPlanRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	plan, err := store.GetPlatformTenantPlan(r.Context(), acct.ID, req.PlanID)
	if err != nil {
		api.WriteProblem(w, storeProblem(err))
		return
	}
	assignment, problem := s.assign(r.Context(), acct, tenant, plan, store)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	httpkit.WriteJSON(w, http.StatusCreated, assignmentResponse(assignment, plan.Name))
}

// planStart is when an assigned plan's card starts: the next minute, or the
// minute after the tenant's latest card of another unit when that is later,
// so the free cards stopOtherUnits adds never share a minute with one.
func (s *Service) planStart(ctx context.Context, acct state.Account, tenantID, unit string) (time.Time, *api.Problem) {
	start := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
	cards, problem := s.tenantCards(ctx, acct, tenantID)
	if problem != nil {
		return start, problem
	}
	for _, card := range cards {
		if billing.StreamUnit(card.Unit) != billing.StreamUnit(unit) && !card.EffectiveFrom.Before(start) {
			start = card.EffectiveFrom.Add(time.Minute)
		}
	}
	return start, nil
}

func (s *Service) tenantCards(ctx context.Context, acct state.Account, tenantID string) ([]state.PlatformTenantRateCard, *api.Problem) {
	store, ok := s.Host.Store().(state.PlatformTenantRateCardStore)
	if !ok {
		return nil, api.ErrInternal("platform tenant pricing is unavailable")
	}
	cards, err := store.ListPlatformTenantRateCards(ctx, acct.ID, tenantID)
	if err != nil {
		return nil, api.ErrInternal("could not load platform tenant rate cards")
	}
	return cards, nil
}

// stopOtherUnits stops the tenant's other priced units when a plan's card
// starts: a plan is the tenant's whole price, and each unit is its own
// price history (ADR-1319), so a free card of each other unit starts at the
// same minute.
func (s *Service) stopOtherUnits(ctx context.Context, acct state.Account, tenantID string, card state.PlatformTenantRateCard) *api.Problem {
	cards, problem := s.tenantCards(ctx, acct, tenantID)
	if problem != nil {
		return problem
	}
	units, streams := billing.TenantUnitStreams(cards)
	for _, unit := range units {
		if unit == billing.StreamUnit(card.Unit) {
			continue
		}
		var current *state.PlatformTenantRateCard
		for i, c := range streams[unit] {
			if !c.EffectiveFrom.After(card.EffectiveFrom) {
				current = &streams[unit][i]
			}
		}
		if current == nil || freeCard(*current) {
			continue
		}
		start := card.EffectiveFrom
		if _, problem := s.Host.CreateTenantRateCard(ctx, acct, tenantID, api.CreatePlatformTenantRateCardRequest{
			Currency: card.Currency, Unit: unit, EffectiveFrom: &start}); problem != nil {
			return problem
		}
	}
	return nil
}

// freeCard reports whether a card charges nothing.
func freeCard(c state.PlatformTenantRateCard) bool {
	if c.PriceMillicentsPerUnit != 0 || c.MonthlyFees() {
		return false
	}
	for _, tier := range c.Tiers {
		if tier.PriceMillicentsPerUnit != 0 {
			return false
		}
	}
	return true
}

func (s *Service) assign(ctx context.Context, acct state.Account, tenant state.PlatformTenant, plan state.PlatformTenantPlan,
	store state.PlatformTenantPlanStore) (state.PlatformTenantPlanAssignment, *api.Problem) {
	cardReq := api.CreatePlatformTenantRateCardRequest{Currency: plan.Currency, PriceMillicentsPerUnit: plan.PriceMillicentsPerUnit,
		IncludedUnitsPerMonth: plan.IncludedUnitsPerMonth, RouteWeights: plan.RouteWeights, Unit: plan.Unit, TierMode: plan.TierMode,
		BaseFeeMillicentsPerMonth: plan.BaseFeeMillicentsPerMonth, MinimumMillicentsPerMonth: plan.MinimumMillicentsPerMonth,
		ProrateMonthlyFees: plan.ProrateMonthlyFees}
	for _, tier := range plan.Tiers {
		cardReq.Tiers = append(cardReq.Tiers, api.APIConsumerRateCardTier{UpTo: tier.UpTo, PriceMillicentsPerUnit: tier.PriceMillicentsPerUnit})
	}
	start, problem := s.planStart(ctx, acct, tenant.ID, plan.Unit)
	if problem != nil {
		return state.PlatformTenantPlanAssignment{}, problem
	}
	cardReq.EffectiveFrom = &start
	card, problem := s.Host.CreateTenantRateCard(ctx, acct, tenant.ID, cardReq)
	if problem != nil {
		return state.PlatformTenantPlanAssignment{}, problem
	}
	if problem := s.stopOtherUnits(ctx, acct, tenant.ID, card); problem != nil {
		return state.PlatformTenantPlanAssignment{}, problem
	}
	if plan.MaxRequestsPerMinute != nil {
		if err := s.Host.SetTenantRequestBudget(ctx, acct, tenant.ID, *plan.MaxRequestsPerMinute, *plan.MaxRequestsPerDay); err != nil {
			return state.PlatformTenantPlanAssignment{}, api.ErrInternal("could not set the tenant's request budget; assign the plan again")
		}
	}
	assignment, err := store.RecordPlatformTenantPlanAssignment(ctx, state.PlatformTenantPlanAssignment{AccountID: acct.ID,
		TenantID: tenant.ID, PlanID: plan.ID, RateCardID: card.ID, EffectiveFrom: card.EffectiveFrom})
	if err != nil {
		return state.PlatformTenantPlanAssignment{}, storeProblem(err)
	}
	s.Host.Audit(ctx, "platform_tenant.plan_assigned", acct.ID, map[string]any{"tenant_id": tenant.ID, "plan_id": plan.ID,
		"rate_card_id": card.ID, "effective_from": card.EffectiveFrom})
	return assignment, nil
}

func assignmentResponse(a state.PlatformTenantPlanAssignment, planName string) api.PlatformTenantPlanAssignmentResponse {
	return api.PlatformTenantPlanAssignmentResponse{ID: a.ID, TenantID: a.TenantID, PlanID: a.PlanID, PlanName: planName,
		RateCardID: a.RateCardID, EffectiveFrom: a.EffectiveFrom, CreatedAt: a.CreatedAt}
}

// ListAssignments lists a tenant's plan assignments, newest first.
func (s *Service) ListAssignments(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store(w, acct)
	if !ok {
		return
	}
	tenant, ok := s.Host.TenantByPath(w, r, acct)
	if !ok {
		return
	}
	assignments, err := store.ListPlatformTenantPlanAssignments(r.Context(), acct.ID, tenant.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list tenant plan assignments"))
		return
	}
	names := map[string]string{}
	if plans, err := store.ListPlatformTenantPlans(r.Context(), acct.ID); err == nil {
		for _, p := range plans {
			names[p.ID] = p.Name
		}
	}
	out := api.PlatformTenantPlanAssignmentListResponse{Assignments: make([]api.PlatformTenantPlanAssignmentResponse, 0, len(assignments))}
	for _, a := range assignments {
		out.Assignments = append(out.Assignments, assignmentResponse(a, names[a.PlanID]))
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}
