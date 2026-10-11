package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state"
)

type OperationRoute struct {
	Definition state.OperationDefinition
	ReleaseID  string
}

type OperationRouteEnqueuer interface {
	ResolveOperationRoute(context.Context, App, *http.Request) (OperationRoute, error)
	EnqueueOperationRoute(context.Context, OperationRoute, string, string, []byte) (state.Operation, error)
}

type OperationRouteStore interface {
	state.OperationStore
	AppByID(context.Context, string) (state.App, error)
	DeploymentByID(context.Context, string) (state.Deployment, error)
	LiveDeploymentForScope(context.Context, string, string) (state.Deployment, error)
	ResolveRevisionPin(context.Context, string, string, string) (state.Deployment, error)
	ResolveProjectRelease(context.Context, string, string, string) (string, string, error)
}

type DurableOperationRoutes struct {
	Store     OperationRouteStore
	Admission *operations.PreviewAdmission
}

func (d DurableOperationRoutes) ResolveOperationRoute(ctx context.Context, app App, r *http.Request) (OperationRoute, error) {
	owned, err := d.Store.AppByID(ctx, app.ID)
	if err != nil || owned.AccountID != app.AccountID {
		return OperationRoute{}, state.ErrNotFound
	}
	scope := app.Scope
	if app.PinnedDeploymentScope != "" {
		scope = app.PinnedDeploymentScope
	}
	if scope == "" {
		scope = state.DefaultEnvScope
		if owned.ProjectID != "" && owned.PreviewOfSlug == "" {
			scope = "production"
		}
	}
	deployment, release := app.PinnedDeploymentID, ""
	if values := r.Header.Values(api.RevisionHeader); len(values) > 1 {
		return OperationRoute{}, state.ErrInvalidArgument
	}
	if values := r.Header.Values(api.ReleaseHeader); len(values) > 1 {
		return OperationRoute{}, state.ErrInvalidArgument
	}
	revision, requestedRelease := r.Header.Get(api.RevisionHeader), r.Header.Get(api.ReleaseHeader)
	if revision != "" && requestedRelease != "" {
		return OperationRoute{}, state.ErrConflict
	}
	if deployment == "" {
		if revision != "" {
			dep, err := d.Store.ResolveRevisionPin(ctx, app.ID, scope, revision)
			if err != nil {
				return OperationRoute{}, err
			}
			deployment = dep.ID
		} else if owned.ProjectID != "" && owned.PreviewOfSlug == "" {
			release, deployment, err = d.Store.ResolveProjectRelease(ctx, app.ID, scope, requestedRelease)
			if err != nil {
				return OperationRoute{}, err
			}
		} else {
			if requestedRelease != "" {
				return OperationRoute{}, state.ErrNotFound
			}
			dep, err := d.Store.LiveDeploymentForScope(ctx, app.ID, scope)
			if err != nil {
				return OperationRoute{}, err
			}
			deployment = dep.ID
		}
	} else if revision != "" && revision != deployment {
		return OperationRoute{}, state.ErrConflict
	} else if requestedRelease != "" {
		return OperationRoute{}, state.ErrConflict
	}
	def, err := d.Store.OperationDefinitionForRoute(ctx, app.AccountID, app.ID, deployment, r.Method, r.URL.Path)
	if err != nil {
		return OperationRoute{}, err
	}
	if def.Scope != scope {
		return OperationRoute{}, state.ErrNotFound
	}
	return OperationRoute{Definition: def, ReleaseID: release}, nil
}

func (d DurableOperationRoutes) EnqueueOperationRoute(ctx context.Context, route OperationRoute, tenant, key string, input []byte) (state.Operation, error) {
	if !d.Admission.AllowsTenant(route.Definition.AccountID, route.Definition.AppID, route.Definition.Scope, tenant) {
		return state.Operation{}, operations.ErrAdmissionClosed
	}
	op, _, err := d.Store.AdmitOperation(ctx, state.OperationAdmission{AccountID: route.Definition.AccountID, DefinitionID: route.Definition.ID,
		PlatformTenantID: tenant, IdempotencyKey: key, Input: input, ReleaseID: route.ReleaseID})
	return op, err
}

func (h *Handler) WithOperationRoutes(routes OperationRouteEnqueuer) *Handler {
	h.operations = routes
	return h
}

// Authentication and body admission have completed. Exact operation matches
// enter the durable ledger before burst admission or a VM wake.
func (h *Handler) applyOperationRoute(w http.ResponseWriter, r *http.Request, app App, sidecar string) bool {
	if h.operations == nil || sidecar != "" || isSyntheticInvocation(r.Context()) {
		return false
	}
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return false
	}
	route, err := h.operations.ResolveOperationRoute(r.Context(), app, r)
	if errors.Is(err, state.ErrNotFound) {
		return false
	}
	if err != nil {
		writeOperationRouteError(w, err)
		return true
	}
	if isUpgradeRequest(r) {
		api.WriteProblem(w, api.ErrValidation("operation HTTP handlers do not support protocol upgrades"))
		return true
	}
	if !app.RequestInvocationsEnabled {
		api.WriteProblem(w, api.ErrValidation("operation HTTP handlers require a request listener"))
		return true
	}
	owner := authenticatedFrom(r.Context()).PlatformTenantID
	if owner == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Customer identity required", "operation ownership requires an authenticated platform tenant"))
		return true
	}
	payload, problem := readAsyncRoutePayload(r, api.OperationSubmissionMaxBytes)
	if problem != nil {
		api.WriteProblem(w, problem)
		return true
	}
	op, err := h.operations.EnqueueOperationRoute(r.Context(), route, owner, r.Header.Get("Idempotency-Key"), payload)
	if err != nil {
		writeOperationRouteError(w, err)
		return true
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(api.OperationIDHeader, op.ID)
	if op.ReleaseID != "" {
		w.Header().Set(api.ReleaseHeader, op.ReleaseID)
	}
	w.Header().Set(api.RevisionHeader, op.DeploymentID)
	writeOperationRouteReceipt(w, op.ID)
	return true
}

func writeOperationRouteError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, api.CodeCapacity
	switch {
	case errors.Is(err, state.ErrOperationInputConflict):
		status, code = http.StatusConflict, "operation_input_conflict"
	case errors.Is(err, state.ErrOperationExpired):
		status, code = http.StatusGone, "operation_expired"
	case errors.Is(err, state.ErrOperationQuota):
		api.WriteProblem(w, api.OperationLimitProblem(err))
		return
	case errors.Is(err, state.ErrPlatformTenantSuspended):
		status, code = http.StatusForbidden, api.CodeForbidden
	case errors.Is(err, state.ErrInvalidArgument):
		status, code = http.StatusUnprocessableEntity, api.CodeValidation
	case errors.Is(err, state.ErrConflict):
		status, code = http.StatusConflict, api.CodeConflict
	case errors.Is(err, state.ErrNotFound):
		status, code = http.StatusNotFound, api.CodeNotFound
	}
	api.WriteProblem(w, api.NewProblem(status, code, "Operation admission failed", "check the input contract, customer identity, idempotency key, and retained operation state"))
}

func writeOperationRouteReceipt(w http.ResponseWriter, id string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(api.OperationAcceptedResponse{ID: id, StatusURL: "/v1/platform-tenant-self/customer-operations/" + id, EventsURL: "/v1/platform-tenant-self/customer-operations/" + id + "/events"})
}
