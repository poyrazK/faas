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

func apiConsumerPlanResponse(plan state.APIConsumerPlan) api.APIConsumerPlanResponse {
	return api.APIConsumerPlanResponse{ID: plan.ID, AppID: plan.AppID, Name: plan.Name,
		MaxRequestsPerMinute: plan.MaxRequestsPerMinute, MaxUnitsPerMonth: plan.MaxUnitsPerMonth,
		AlertThresholdsPercent: append([]int32{}, plan.AlertThresholdsPercent...),
		CreatedAt:              plan.CreatedAt.UTC(), UpdatedAt: plan.UpdatedAt.UTC()}
}

func apiConsumerPlanAssignmentResponse(a state.APIConsumerPlanAssignment) api.APIConsumerPlanAssignmentResponse {
	return api.APIConsumerPlanAssignmentResponse{ID: a.ID, ConsumerID: a.ConsumerID, PlanID: a.PlanID,
		EffectiveFrom: a.EffectiveFrom.UTC(), CreatedAt: a.CreatedAt.UTC()}
}

func (s *server) apiConsumerPlanStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.APIConsumerPlanStore, bool) {
	if !s.consumerFeatureAllowed(w, acct) {
		return state.App{}, nil, false
	}
	app, ok := s.consumerApp(w, r, acct)
	if !ok {
		return state.App{}, nil, false
	}
	store, ok := s.store.(state.APIConsumerPlanStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("API consumer plans are unavailable"))
		return state.App{}, nil, false
	}
	return app, store, true
}

func planProblem(detail string) *api.Problem {
	return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid consumer plan", detail)
}

func (s *server) listAPIConsumerPlans(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.apiConsumerPlanStore(w, r, acct)
	if !ok {
		return
	}
	plans, err := store.ListAPIConsumerPlans(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list consumer plans"))
		return
	}
	out := api.APIConsumerPlanListResponse{Plans: make([]api.APIConsumerPlanResponse, 0, len(plans))}
	for _, plan := range plans {
		out.Plans = append(out.Plans, apiConsumerPlanResponse(plan))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createAPIConsumerPlan(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.apiConsumerPlanStore(w, r, acct)
	if !ok {
		return
	}
	var req api.CreateAPIConsumerPlanRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	plan := state.APIConsumerPlan{AccountID: acct.ID, AppID: app.ID, Name: req.Name,
		MaxRequestsPerMinute: req.MaxRequestsPerMinute, MaxUnitsPerMonth: req.MaxUnitsPerMonth,
		AlertThresholdsPercent: req.AlertThresholdsPercent}
	if err := state.ValidateAPIConsumerPlan(plan); err != nil {
		api.WriteProblem(w, planProblem(err.Error()))
		return
	}
	created, err := store.CreateAPIConsumerPlan(r.Context(), plan)
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Consumer plan conflict",
			"the plan name is taken or the app already has the maximum number of plans"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not create consumer plan"))
		return
	}
	s.audit.Emit(r.Context(), "api_consumer_plan.created", &acct.ID, map[string]any{
		"app_id": app.ID, "plan_id": created.ID, "name": created.Name,
		"max_requests_per_minute": created.MaxRequestsPerMinute, "max_units_per_month": created.MaxUnitsPerMonth,
		"alert_thresholds_percent": created.AlertThresholdsPercent,
	})
	writeJSON(w, http.StatusCreated, apiConsumerPlanResponse(created))
}

func (s *server) updateAPIConsumerPlanLimits(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.apiConsumerPlanStore(w, r, acct)
	if !ok {
		return
	}
	var req api.UpdateAPIConsumerPlanLimitsRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.MaxRequestsPerMinute < 0 || req.MaxUnitsPerMonth < 0 {
		api.WriteProblem(w, planProblem("limits must be non-negative"))
		return
	}
	var alerts []int32
	if req.AlertThresholdsPercent != nil {
		alerts = append([]int32{}, *req.AlertThresholdsPercent...)
	}
	plan, err := store.UpdateAPIConsumerPlanLimits(r.Context(), acct.ID, app.ID, r.PathValue("plan_id"), req.MaxRequestsPerMinute, req.MaxUnitsPerMonth, alerts)
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, planProblem(strings.TrimPrefix(err.Error(), state.ErrInvalidArgument.Error()+": ")))
		return
	case err != nil:
		s.notFound(w, "no such consumer plan")
		return
	}
	s.audit.Emit(r.Context(), "api_consumer_plan.limits_updated", &acct.ID, map[string]any{
		"app_id": app.ID, "plan_id": plan.ID,
		"max_requests_per_minute": plan.MaxRequestsPerMinute, "max_units_per_month": plan.MaxUnitsPerMonth,
		"alert_thresholds_percent": plan.AlertThresholdsPercent,
	})
	writeJSON(w, http.StatusOK, apiConsumerPlanResponse(plan))
}

func (s *server) listAPIConsumerPlanAssignments(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.apiConsumerPlanStore(w, r, acct)
	if !ok {
		return
	}
	consumer, err := s.store.GetAPIConsumerByID(r.Context(), acct.ID, r.PathValue("consumer_id"))
	if err != nil || consumer.AppID != app.ID {
		s.notFound(w, "no such consumer")
		return
	}
	assignments, err := store.ListAPIConsumerPlanAssignments(r.Context(), acct.ID, app.ID, consumer.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list consumer plan assignments"))
		return
	}
	out := api.APIConsumerPlanAssignmentListResponse{Assignments: make([]api.APIConsumerPlanAssignmentResponse, 0, len(assignments))}
	for _, a := range assignments {
		out.Assignments = append(out.Assignments, apiConsumerPlanAssignmentResponse(a))
	}
	writeJSON(w, http.StatusOK, out)
}

// assignAPIConsumerPlan moves a consumer onto a plan from a minute that is
// not in the past, and only when that plan has a price in force then, so a
// switch never re-prices billed minutes or leaves minutes on the old price.
func (s *server) assignAPIConsumerPlan(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.apiConsumerPlanStore(w, r, acct)
	if !ok {
		return
	}
	consumer, err := s.store.GetAPIConsumerByID(r.Context(), acct.ID, r.PathValue("consumer_id"))
	if err != nil || consumer.AppID != app.ID {
		s.notFound(w, "no such consumer")
		return
	}
	var req api.AssignAPIConsumerPlanRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	effectiveFrom, problem := planAssignmentStart(req.EffectiveFrom, time.Now().UTC())
	if problem == nil {
		problem = s.planPricedProblem(r, acct.ID, app.ID, req.PlanID, effectiveFrom)
	}
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	assignment, err := store.AssignAPIConsumerPlan(r.Context(), state.APIConsumerPlanAssignment{
		AccountID: acct.ID, AppID: app.ID, ConsumerID: consumer.ID, PlanID: req.PlanID, EffectiveFrom: effectiveFrom,
	})
	s.writePlanAssignment(w, r, acct, app, assignment, err)
}

func planAssignmentStart(requested *time.Time, now time.Time) (time.Time, *api.Problem) {
	start := now.Truncate(time.Minute).Add(time.Minute)
	if requested == nil {
		return start, nil
	}
	at := requested.UTC()
	if !at.Equal(at.Truncate(time.Minute)) {
		return time.Time{}, planProblem("effective_from must be a UTC minute")
	}
	if at.Before(now.Truncate(time.Minute)) {
		return time.Time{}, planProblem("effective_from cannot be in the past")
	}
	return at, nil
}

func (s *server) planPricedProblem(r *http.Request, accountID, appID, planID string, at time.Time) *api.Problem {
	cardsStore, ok := s.store.(state.APIConsumerRateCardStore)
	if !ok {
		return api.ErrInternal("API consumer pricing is unavailable")
	}
	cards, err := cardsStore.ListAPIConsumerRateCardsForApp(r.Context(), accountID, appID)
	if err != nil {
		return api.ErrInternal("could not load API consumer rate cards")
	}
	if !billing.PlanPricedAt(cards, planID, at) {
		return planProblem("the target plan has no rate card in force at effective_from; add one first")
	}
	return nil
}

func (s *server) writePlanAssignment(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, assignment state.APIConsumerPlanAssignment, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no such consumer plan")
		return
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Plan assignment conflict",
			"the consumer already changes plans at that minute"))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not assign consumer plan"))
		return
	}
	s.audit.Emit(r.Context(), "api_consumer_plan.assigned", &acct.ID, map[string]any{
		"app_id": app.ID, "consumer_id": assignment.ConsumerID, "plan_id": assignment.PlanID,
		"effective_from": assignment.EffectiveFrom.Format(time.RFC3339),
	})
	writeJSON(w, http.StatusCreated, apiConsumerPlanAssignmentResponse(assignment))
}

// consumerPriceHistory returns the cards that priced one consumer: the app
// default cards and plan cards, resolved through the consumer's plan
// assignments (ADR-938).
func (s *server) consumerPriceHistory(r *http.Request, cards []state.APIConsumerRateCard, accountID, appID, consumerID string) ([]state.APIConsumerRateCard, error) {
	store, ok := s.store.(state.APIConsumerPlanStore)
	if !ok {
		return billing.PlanCardTimeline(cards, nil), nil
	}
	assignments, err := store.ListAPIConsumerPlanAssignments(r.Context(), accountID, appID, consumerID)
	if err != nil {
		return nil, err
	}
	return billing.PlanCardTimeline(cards, assignments), nil
}
