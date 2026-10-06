// adr: 429 — the dedicated promotion route enforces preflight at the traffic write.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) promoteDeploymentWithBindings(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.promoteDeploymentWithBindingPolicy(w, r, acct, false)
}

func (s *server) promoteDeploymentWithApplicationAck(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.promoteDeploymentWithBindingPolicy(w, r, acct, true)
}

func (s *server) promoteDeploymentWithBindingPolicy(w http.ResponseWriter, r *http.Request, acct state.Account, requireAck bool) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	app, deployment, problem := s.bindingPromotionTarget(r, acct)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	req, age, problem := bindingPromotionPolicy(r, acct, deployment)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	req.RequireApplicationAck = req.RequireApplicationAck || requireAck
	result, report, problem := s.executeBindingPromotion(r, acct, app, deployment, req, age)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.emitBindingPromotion(r, acct, app, result, report)
	writeJSON(w, http.StatusOK, api.BindingPromotionResponse{
		Deployment: s.deploymentResponse(result.Deployment, app), FromPercent: result.FromPercent,
		ToPercent: 100, AlreadyPromoted: result.FromPercent == 100, BindingsCheck: &report,
	})
}

func (s *server) bindingPromotionTarget(r *http.Request, acct state.Account) (state.App, state.Deployment, *api.Problem) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return state.App{}, state.Deployment{}, bindingPromotionValidation("promotion requires a deployment UUID")
	}
	d, err := s.store.DeploymentByID(r.Context(), id.String())
	if errors.Is(err, state.ErrNotFound) {
		d, err = s.store.DeploymentByID(r.Context(), strings.ReplaceAll(id.String(), "-", ""))
	}
	if errors.Is(err, state.ErrNotFound) {
		return state.App{}, state.Deployment{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Deployment not found", "No such deployment.")
	}
	if err != nil {
		return state.App{}, state.Deployment{}, api.ErrInternal("could not read deployment")
	}
	app, err := s.store.AppByID(r.Context(), d.AppID)
	if err != nil || app.AccountID != acct.ID || app.Status == state.AppDeleted {
		return state.App{}, state.Deployment{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Deployment not found", "No such deployment.")
	}
	if d.Status != state.DeployLive || d.RootfsKey == "" || d.ImageDigest == "" {
		return app, d, appTaskDeploymentUnavailableProblem()
	}
	return app, d, nil
}

func bindingPromotionPolicy(r *http.Request, acct state.Account, deployment state.Deployment) (api.BindingPromotionRequest, time.Duration, *api.Problem) {
	var req api.BindingPromotionRequest
	if err := decodeJSON(r, &req); err != nil {
		return req, 0, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error())
	}
	age := api.DefaultBindingVerificationAge
	if req.MaxVerificationAge != "" {
		var err error
		age, err = time.ParseDuration(req.MaxVerificationAge)
		if err != nil || age <= 0 {
			return req, 0, bindingPromotionValidation("max_verification_age must be a positive duration, for example 10m")
		}
	}
	if req.ExpectedServingDeploymentID != nil {
		id, err := uuid.Parse(*req.ExpectedServingDeploymentID)
		candidate, _ := uuid.Parse(deployment.ID)
		if err != nil || id == candidate {
			return req, 0, bindingPromotionValidation("expected_serving_deployment_id must name a different deployment UUID")
		}
		canonical := id.String()
		req.ExpectedServingDeploymentID = &canonical
	}
	if !acct.Plan.TrafficSplitAllowed() {
		return req, 0, api.ErrPlanTrafficSplitNotAllowed(acct.Plan)
	}
	return req, age, nil
}

type bindingPromotionObservation struct {
	store      state.BindingPromotionStore
	domain     managedpostgres.PromotionFence
	fence      state.BindingPromotionFence
	report     api.BindingCheckReport
	expiration api.BindingCheckFinding
}

func (s *server) observeBindingPromotion(r *http.Request, acct state.Account, app state.App, deployment state.Deployment, req api.BindingPromotionRequest, age time.Duration) (bindingPromotionObservation, *api.Problem) {
	var observation bindingPromotionObservation
	store, ok := s.store.(state.BindingPromotionStore)
	if !ok || s.managedPostgresBindings == nil {
		return observation, api.NewProblem(http.StatusServiceUnavailable, "bindings_gate_unavailable", "Bindings gate unavailable", "The binding catalogs cannot enforce an atomic promotion check.")
	}
	observation.store = store
	revision, err := store.ReadBindingPromotionRevision(r.Context(), acct.ID, app.ID)
	if err != nil {
		return observation, api.ErrInternal("could not capture binding promotion revision")
	}
	observation.domain, err = s.managedPostgresBindings.ReadPromotionFence(r.Context(), acct.ID, app.ID)
	if err != nil {
		return observation, api.NewProblem(http.StatusServiceUnavailable, "bindings_gate_unavailable", "Bindings gate unavailable", "The managed PostgreSQL catalog cannot enforce a promotion check.")
	}
	acct, err = s.store.AccountByID(r.Context(), acct.ID)
	if err != nil {
		return observation, api.ErrInternal("could not read promotion account policy")
	}
	if !acct.Plan.TrafficSplitAllowed() {
		return observation, api.ErrPlanTrafficSplitNotAllowed(acct.Plan)
	}
	// Reload mutable app configuration only after capturing the revision.
	app, err = s.store.AppByID(r.Context(), app.ID)
	if err != nil || app.AccountID != acct.ID || app.Status == state.AppDeleted {
		return observation, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Deployment not found", "No such deployment.")
	}
	deployment, problem := s.selectBindingVerificationDeployment(r.Context(), app, deployment.ID)
	if problem != nil {
		return observation, problem
	}
	policyStore, ok := s.store.(state.BindingReleasePolicyStore)
	if !ok {
		return observation, api.ErrCapacity("binding release policies are unavailable")
	}
	releasePolicy, err := policyStore.GetBindingReleasePolicy(r.Context(), acct.ID, app.ID, deployment.Scope)
	if err != nil {
		return observation, api.ErrCapacity("binding release policy could not be read")
	}
	if releasePolicy.Mode == "enforce" {
		storedAge, err := time.ParseDuration(releasePolicy.MaxVerificationAge)
		if err != nil {
			return observation, api.ErrCapacity("binding release policy is invalid")
		}
		if age > storedAge {
			age = storedAge
		}
		req.AllowUnsupported = false
		req.RequireApplicationAck = req.RequireApplicationAck || releasePolicy.RequireApplicationAck
	}
	inventoryRequest := r.Clone(r.Context())
	query := inventoryRequest.URL.Query()
	query.Set("deployment_id", deployment.ID)
	query.Del("scope")
	inventoryRequest.URL.RawQuery = query.Encode()
	inventory := s.collectBindingInventory(r.Context(), inventoryRequest, acct, app, "")
	report, err := bindingcheck.Evaluate(inventory, bindingcheck.Policy{App: app.Slug, DeploymentID: deployment.ID, MaxVerificationAge: age, AllowUnsupported: req.AllowUnsupported, RequireApplicationAck: req.RequireApplicationAck}, time.Now())
	if err != nil {
		return observation, api.ErrInternal("could not evaluate binding promotion policy")
	}
	observation.report = api.BindingCheckReport(report)
	if !report.Passed {
		return observation, bindingPromotionProblem("bindings_check_failed", "Bindings check failed", "Resolve the binding check blockers and verify this deployment before promoting.", observation.report)
	}
	deadline, expiration := bindingPromotionDeadline(observation.report, inventory, age)
	observation.expiration = expiration
	observation.fence = state.BindingPromotionFence{AccountID: acct.ID, AppID: app.ID, DeploymentID: deployment.ID, Scope: report.Scope, Revision: revision, ValidUntil: deadline, PolicyRevision: releasePolicy.Revision, MaxVerificationAge: age, AllowUnsupported: req.AllowUnsupported, RequireApplicationAck: req.RequireApplicationAck}
	return observation, nil
}

// Only called after Evaluate passes. Queue polls expire independently of probe
// age, including when unsupported connectivity coverage was explicitly waived.
func bindingPromotionDeadline(report api.BindingCheckReport, inventory api.AppBindingInventory, age time.Duration) (time.Time, api.BindingCheckFinding) {
	var deadline time.Time
	expiration := api.BindingCheckFinding{Code: "verification_expired", Scope: report.Scope, DeploymentID: report.DeploymentID,
		Message: "Binding verification expired before promotion; verify this deployment again."}
	for _, binding := range report.Bindings {
		if binding.Status != "passed" || binding.CheckedAt == nil {
			continue
		}
		end := binding.CheckedAt.Add(age)
		if deadline.IsZero() || end.Before(deadline) {
			deadline = end
		}
	}
	for _, item := range inventory.Bindings {
		if item.Type != api.BindingTypeQueue || item.State != "enabled" || item.Access != "push" || item.ObservedAt == nil {
			continue
		}
		end := item.ObservedAt.Add(api.QueueConsumerMaxPollAge)
		if deadline.IsZero() || end.Before(deadline) {
			deadline = end
			expiration = api.BindingCheckFinding{Code: "queue_consumer_stale", Type: item.Type, Name: item.Name, Binding: item.Binding, Scope: item.Scope,
				Message: "The push consumer poll expired before promotion; restore scheduler polling, recheck and retry."}
		}
	}
	return deadline, expiration
}

func (s *server) executeBindingPromotion(r *http.Request, acct state.Account, app state.App, deployment state.Deployment, req api.BindingPromotionRequest, age time.Duration) (state.BindingPromotionResult, api.BindingCheckReport, *api.Problem) {
	observation, problem := s.observeBindingPromotion(r, acct, app, deployment, req, age)
	if problem != nil {
		return state.BindingPromotionResult{}, observation.report, problem
	}
	var result state.BindingPromotionResult
	expected := ""
	if req.ExpectedServingDeploymentID != nil {
		expected = *req.ExpectedServingDeploymentID
	}
	err := s.managedPostgresBindings.GuardPromotion(r.Context(), acct.ID, app.ID, observation.domain, observation.store.BindingPromotionBackend(), func(ctx context.Context) error {
		var err error
		result, err = observation.store.PromoteDeploymentWithBindings(ctx, deployment.ID, observation.fence, expected)
		return err
	})
	if err != nil {
		return result, observation.report, bindingPromotionWriteProblem(err, observation.report, observation.expiration)
	}
	observation.report.CheckedAt = result.CheckedAt
	return result, observation.report, nil
}

func bindingPromotionProblem(code, title, detail string, report api.BindingCheckReport) *api.Problem {
	problem := api.NewProblem(http.StatusConflict, code, title, detail)
	problem.BindingsCheck = &report
	return problem
}

func bindingPromotionWriteProblem(err error, report api.BindingCheckReport, expiration api.BindingCheckFinding) *api.Problem {
	switch {
	case errors.Is(err, state.ErrCheckedRollbackRequired):
		return api.NewProblem(http.StatusConflict, "rollback_operation_required", "Checked rollback in progress", "Use the exact rollback operation status to inspect this traffic handoff.")
	case state.IsBindingReleaseRequired(err):
		return bindingReleaseRequiredProblem()
	case errors.Is(err, state.ErrBindingPromotionChanged), errors.Is(err, state.ErrBindingPromotionExpired), errors.Is(err, managedpostgres.ErrConflict):
		report.Passed = false
		finding := api.BindingCheckFinding{Code: "promotion_observations_changed", Scope: report.Scope, DeploymentID: report.DeploymentID,
			Message: "Binding or runtime observations changed before promotion; recheck and retry."}
		if errors.Is(err, state.ErrBindingPromotionExpired) {
			finding = expiration
		}
		report.Blockers = append(report.Blockers, finding)
		return bindingPromotionProblem("bindings_check_changed", "Bindings check changed", finding.Message, report)
	case errors.Is(err, state.ErrTrafficServingChanged):
		return api.ErrTrafficServingChanged()
	case errors.Is(err, state.ErrTrafficChangeDuringCanary):
		return api.ErrTrafficChangeDuringCanary()
	case errors.Is(err, state.ErrDeploymentNotLive):
		return api.ErrDeploymentNotLive("unavailable")
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Deployment not found", "No such deployment.")
	case errors.Is(err, managedpostgres.ErrUnsupported):
		return api.NewProblem(http.StatusServiceUnavailable, "bindings_gate_unavailable", "Bindings gate unavailable", "Binding catalogs must share the traffic transaction backend.")
	default:
		return api.ErrInternal("could not promote deployment with bindings")
	}
}

func (s *server) emitBindingPromotion(r *http.Request, acct state.Account, app state.App, result state.BindingPromotionResult, report api.BindingCheckReport) {
	if result.FromPercent == 100 {
		return
	}
	if s.audit != nil {
		s.audit.Emit(r.Context(), "deployment.traffic_percent_changed", &acct.ID, map[string]any{
			"app": app.ID, "deployment": result.Deployment.ID, "traffic_percent": 100, "prev": result.FromPercent,
			"require_bindings": true, "bindings_coverage": report.Coverage, "bindings_checked_at": report.CheckedAt,
			"max_verification_age": report.MaxVerificationAge, "allow_unsupported": report.AllowUnsupported, "require_application_ack": report.RequireApplicationAck,
		})
	}
	if s.notif != nil {
		if err := s.notif.Notify(r.Context(), db.NotifyDeploymentChanged, fmt.Sprintf(`{"kind":"traffic","app_id":"%s","deployment_id":"%s","traffic_percent":100}`, app.ID, result.Deployment.ID)); err != nil {
			s.log.Warn("apid: notify deployment_changed (binding promotion) failed", "err", err)
		}
	}
}

func bindingPromotionValidation(detail string) *api.Problem {
	return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid promotion policy", detail)
}
