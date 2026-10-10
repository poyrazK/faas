package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) invokeDurableEntity(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if s.durableEntities == nil || !s.durableEntityApps[app.ID] {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "durable_entities_unavailable", "Durable entities unavailable", "this app is not enabled for the durable entity preview"))
		return
	}
	if !app.AcceptsRequestInvocations() {
		api.WriteProblem(w, api.ErrInvocationWorkloadClass(string(app.WorkloadClass), app.Manifest.ExecutionMode))
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.AsyncInvokeAllowed {
		api.WriteProblem(w, api.ErrPlanFeatureGated("durable_entities_preview", acct.Plan))
		return
	}
	var request api.DurableEntityInvokeRequest
	if !decodeJSONLimit(w, r, &request, int64(limits.MaxSourceBytesPerInvocation)) {
		return
	}
	id, scope, problem := s.durableEntityIdentity(r, acct, app, request)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.DurableEntityInvokeTimeout)
	defer cancel()
	started := time.Now()
	result, err := s.durableEntities.Invoke(ctx, id, s.durableEntityOwner, durableentity.Request{ID: request.RequestID, Payload: request.Payload}, func(ctx context.Context, view durableentity.View) (durableentity.Transition, error) {
		return s.invokeDurableEntityHandler(ctx, acct, app, scope, id, request, view)
	})
	s.durableEntityMetrics.observeResult("invoke", result, err)
	s.durableEntityMetrics.observeDuration("invoke", started)
	if err != nil {
		writeDurableEntityProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.DurableEntityInvokeResponse{Value: result.Value, Version: result.Version, Replayed: result.Replayed})
}

func (s *server) durableEntityIdentity(r *http.Request, acct state.Account, app state.App, request api.DurableEntityInvokeRequest) (durableentity.ID, string, *api.Problem) {
	return s.resolveDurableEntityIdentity(r, acct, app, request, false)
}

// Owner inspection can diagnose held tenant work without granting execution to
// a suspended tenant or requiring its current plan to permit new invocations.
func (s *server) resolveDurableEntityIdentity(r *http.Request, acct state.Account, app state.App, request api.DurableEntityInvokeRequest, inspect bool) (durableentity.ID, string, *api.Problem) {
	id := durableentity.ID{AccountID: acct.ID, AppID: app.ID, Namespace: request.Namespace, Key: request.Key}
	scope, env, problem := s.resolveQueueEnvironment(r.Context(), acct, app, request.Environment)
	if problem != nil {
		return id, scope, problem
	}
	// Project apps require a catalog identity, including production. Deleting
	// and recreating an environment must not attach it to old entity data.
	id.EnvironmentID = app.ID
	if app.ProjectID != "" && app.PreviewOfSlug == "" {
		if env == nil {
			return id, scope, api.ErrValidation("register the selected project environment before invoking entities")
		}
		id.EnvironmentID = env.ID
	}
	if request.PlatformTenantID != "" {
		if !inspect && acct.Plan.ConsumerKeysPerApp() <= 0 {
			return id, scope, api.ErrPlanFeatureGated("platform_tenants", acct.Plan)
		}
		store, ok := s.store.(state.PlatformTenantStore)
		tenantID, parseErr := uuid.Parse(request.PlatformTenantID)
		if !ok || parseErr != nil || tenantID == uuid.Nil {
			return id, scope, api.ErrValidation("platform_tenant_id must identify a customer in this account")
		}
		tenant, err := store.GetPlatformTenant(r.Context(), acct.ID, tenantID.String())
		if errors.Is(err, state.ErrNotFound) {
			return id, scope, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Customer not found", "the customer does not belong to this account")
		}
		if err != nil {
			return id, scope, api.ErrCapacity("load entity customer")
		}
		if !inspect && tenant.Status != state.PlatformTenantActive {
			return id, scope, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Customer suspended", "resume the customer before invoking entities")
		}
		id.TenantID = tenant.ID
	}
	return id, scope, nil
}

func writeDurableEntityProblem(w http.ResponseWriter, err error) {
	var problem *api.Problem
	if errors.As(err, &problem) {
		api.WriteProblem(w, problem)
		return
	}
	status, code, detail := http.StatusServiceUnavailable, "durable_entity_unavailable", "entity storage or execution is unavailable; retry the same request_id and exact payload"
	switch {
	case errors.Is(err, durableentity.ErrInvalid):
		status, code, detail = http.StatusUnprocessableEntity, "durable_entity_invalid", "invalid entity identity, request, or handler transition"
	case errors.Is(err, durableentity.ErrLimit):
		status, code, detail = http.StatusConflict, "durable_entity_limit", "the entity state or receipt object budget is full"
	case errors.Is(err, durableentity.ErrInventoryPending):
		code, detail = "durable_entity_inventory_pending", "entity storage accounting is being established; retry the same request_id and exact payload later"
	case errors.Is(err, durableentity.ErrRequestConflict):
		status, code, detail = http.StatusConflict, "durable_entity_request_conflict", "request_id was already committed with a different payload"
	case errors.Is(err, durableentity.ErrBusy), errors.Is(err, durableentity.ErrConflict), errors.Is(err, durableentity.ErrStaleOwner):
		code, detail = "durable_entity_busy", "another owner or transition won; retry the same request_id and exact payload"
	case errors.Is(err, durableentity.ErrUncertain):
		code = "durable_entity_outcome_uncertain"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		status, code = http.StatusGatewayTimeout, "durable_entity_timeout"
	}
	problem = api.NewProblem(status, code, "Durable entity call failed", detail)
	var limit *durableentity.LimitError
	if errors.As(err, &limit) {
		if limit.Budget == "committed_storage_bytes" {
			problem = api.NewProblem(http.StatusConflict, "durable_entity_storage_limit", "Entity storage limit reached", "the projected committed entity storage exceeds its configured byte limit")
		}
		problem = problem.WithLimit(limit.Limit, limit.Observed).WithDocs("https://gregale.dev/docs")
	}
	api.WriteProblem(w, problem)
}
