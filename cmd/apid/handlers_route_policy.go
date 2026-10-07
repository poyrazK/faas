package main

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routerequirements"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) routePolicyPlanner(request api.RoutePolicyPlanRequest) state.RoutePolicyPlanner {
	return func(snapshot state.RoutePolicySnapshot) (api.RoutePolicyPlan, error) {
		context := s.routeRequirementsContext(snapshot)
		options := routerequirements.PlanOptions{PlanName: string(snapshot.Account.Plan), ThrottleBurst: request.ThrottleBurst, ConsolidateBudgets: request.ConsolidateBudgets}
		config := request.Requirements
		if request.Saved {
			if snapshot.SavedRequirements == nil {
				return api.RoutePolicyPlan{}, state.ErrNotFound
			}
			config = snapshot.SavedRequirements.Requirements
			options.RequirementsRevision = snapshot.SavedRequirements.Revision
		}
		if config.Version == 2 {
			return routerequirements.BuildServerGroupPlan(config, context, options, routePolicyInventory(snapshot.Contract, snapshot.Account.Plan))
		}
		return routerequirements.BuildServerPlan(config, context, options)
	}
}

func (s *server) routeRequirementsContext(snapshot state.RoutePolicySnapshot) routerequirements.Context {
	app := snapshot.App
	mode := app.ConsumerAuthMode
	if mode == "" {
		mode = api.ConsumerAuthModeOptional
	}
	context := routerequirements.Context{Host: appHostForDomain(app.Slug, s.domain), Rules: snapshot.Rules,
		App: api.AppResponse{ID: app.ID, Slug: app.Slug, ConsumerAuthMode: string(mode), MaintenanceMode: app.MaintenanceMode,
			RequestTimeoutS: app.Manifest.RequestTimeoutS, EffectiveLimits: appEffectiveLimits(app, snapshot.Account.Plan)}}
	if !snapshot.Account.MayDeploy() {
		context.Unavailable = "account_ineligible"
	}
	return context
}

func normalizeRoutePolicyRequest(request *api.RoutePolicyPlanRequest) error {
	if err := state.ValidateRoutePolicySource(*request); err != nil {
		return err
	}
	if request.Saved {
		return nil
	}
	config, _, err := routerequirements.NormalizeCoverage(request.Requirements)
	if err != nil {
		return err
	}
	if request.ThrottleBurst < 0 {
		return errors.New("throttle_burst must be nonnegative")
	}
	if config.Version == 2 {
		if id, err := uuid.Parse(request.DeploymentID); err != nil || id.String() != request.DeploymentID {
			return errors.New("version 2 requirements need a canonical deployment_id UUID")
		}
	} else if request.ConsolidateBudgets {
		return errors.New("consolidate_budgets requires version 2 route groups")
	} else if request.DeploymentID != "" {
		return errors.New("deployment_id is only supported with version 2 requirements")
	}
	request.Requirements = config
	return nil
}

func (s *server) routePolicyTarget(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.RoutePolicyStore, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return state.App{}, nil, false
	}
	store, ok := s.store.(state.RoutePolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("route policy transactions are unavailable"))
	}
	return app, store, ok
}

func (s *server) postRoutePolicyPlan(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var request api.RoutePolicyPlanRequest
	if err := decodeJSONSized(r, &request, api.RoutePolicyRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid route policy request"))
		return
	}
	if err := normalizeRoutePolicyRequest(&request); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	app, store, ok := s.routePolicyTarget(w, r, acct)
	if !ok {
		return
	}
	plan, err := store.PlanRoutePolicy(r.Context(), acct.ID, app.ID, request, s.routePolicyPlanner(request))
	if err != nil {
		s.routePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *server) postRoutePolicyApply(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var request api.RoutePolicyApplyRequest
	if err := decodeJSONSized(r, &request, api.RoutePolicyRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid route policy apply request"))
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if err := state.ValidateRoutePolicyApply(key, request); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if err := normalizeRoutePolicyRequest(&request.RoutePolicyPlanRequest); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	app, store, ok := s.routePolicyTarget(w, r, acct)
	if !ok {
		return
	}
	s.applyRoutePolicy(w, r, acct, app, store, key, request)
}

func (s *server) applyRoutePolicy(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, store state.RoutePolicyStore, key string, request api.RoutePolicyApplyRequest) {
	previous, lookupErr := store.FindRoutePolicyReceipt(r.Context(), acct.ID, app.ID, key, request)
	if lookupErr != nil && !errors.Is(lookupErr, state.ErrNotFound) {
		s.routePolicyError(w, lookupErr)
		return
	}
	conv, err := s.prepareEdgeRuleMutation(r.Context(), app.ID, "", "updated", appHostForDomain(app.Slug, s.domain))
	if err != nil {
		if lookupErr == nil {
			writeJSON(w, http.StatusOK, api.RoutePolicyApplyResponse{Receipt: previous, Replayed: true, GatewayState: "unknown"})
		} else {
			api.WriteProblem(w, api.ErrCapacity("serving gateways could not prepare this policy change; no changes were applied"))
		}
		return
	}
	receipt, replayed, err := store.ApplyRoutePolicy(r.Context(), acct.ID, app.ID, key, request, s.routePolicyPlanner(request.RoutePolicyPlanRequest))
	if err != nil {
		conv.abort(r.Context())
		s.routePolicyError(w, err)
		return
	}
	if !replayed {
		s.audit.Emit(r.Context(), "route_policy.applied", &acct.ID, map[string]any{
			"app_id": app.ID, "receipt_id": receipt.ID, "plan_sha256": receipt.PlanSHA256, "changes": receipt.Changes})
	}
	gatewayState := "active"
	if err := conv.apply(r.Context(), ""); err != nil {
		gatewayState = "converging"
		s.log.Warn("route policy committed; gateway confirmation incomplete", "app_id", app.ID, "receipt_id", receipt.ID, "generation", conv.generation)
	} else if len(conv.expected) == 0 {
		gatewayState = "unobserved"
	}
	conv.setResponseState(w, gatewayState)
	writeJSON(w, http.StatusOK, api.RoutePolicyApplyResponse{Receipt: receipt, Replayed: replayed, GatewayState: gatewayState, GatewayGeneration: conv.generation})
}

func (s *server) getRoutePolicyReceipt(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("receipt_id")
	if _, err := uuid.Parse(id); err != nil {
		s.notFound(w, "route policy receipt")
		return
	}
	app, store, ok := s.routePolicyTarget(w, r, acct)
	if !ok {
		return
	}
	receipt, err := store.GetRoutePolicyReceipt(r.Context(), acct.ID, app.ID, id)
	if err != nil {
		s.routePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}

func (s *server) routePolicyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "app, saved requirements, captured deployment or route policy receipt")
	case errors.Is(err, state.ErrRouteRequirementsRevision):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Saved route requirements changed", "Regenerate and review the plan against the current saved requirements before applying. No changes were applied."))
	case errors.Is(err, state.ErrRoutePolicyStale):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Route policy plan is stale", "Regenerate and review the plan against current configuration before applying."))
	case errors.Is(err, state.ErrRoutePolicyUnresolved):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Route policy requirements unresolved", "No changes were applied. Review the plan and account eligibility."))
	case errors.Is(err, state.ErrRoutePolicyKeyReused):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Idempotency key reused", "This key belongs to a different route policy request."))
	default:
		api.WriteProblem(w, api.ErrCapacity("route policy transaction could not complete; retry the same idempotency key to recover its outcome"))
	}
}
