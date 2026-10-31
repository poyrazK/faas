package bindings

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/servicebinding"
	"github.com/onebox-faas/faas/pkg/state"
)

const serviceExplanationLimitations = "Policy and routing observation from separate reads; assumes an authenticated caller from the selected deployment and canonical transport. Does not verify live caller credentials, guest adoption, connectivity, endpoint selection, or application authorization. Preview, qualification and development bridge contexts are not evaluated."

type serviceExplanation struct {
	api.ServiceBindingExplanation
}

type ServiceExplanationStore interface {
	servicebinding.AuthorizationStore
	DeploymentByID(context.Context, string) (state.Deployment, error)
}

func (e *serviceExplanation) check(rule, result, reason, setting string) {
	e.Checks = append(e.Checks, api.ServiceAuthorizationCheck{Rule: rule, Result: result, Reason: reason, Setting: setting})
}

func (e *serviceExplanation) stop(rule, result, reason string) {
	e.Result = result
	e.check(rule, result, reason, "")
}

// ExplainServiceBinding performs metadata reads only. The deployment selector
// is an observation context, never a substitute for the gateway's live identity.
func ExplainServiceBinding(ctx context.Context, store ServiceExplanationStore, caller state.App, service string, in api.ExplainServiceBindingOptions) (api.ServiceBindingExplanation, *api.Problem) {
	method, path, problem := validateServiceExplanation(service, in)
	if problem != nil {
		return api.ServiceBindingExplanation{}, problem
	}
	e := serviceExplanation{api.ServiceBindingExplanation{
		Caller: caller.Slug, Target: service, Method: method, Path: path,
		Result: "indeterminate", Scope: "app", CheckedAt: time.Now().UTC(),
		Checks: []api.ServiceAuthorizationCheck{}, Limitations: serviceExplanationLimitations,
		CallerFingerprint: state.ServiceBindingFingerprint(caller),
	}}
	deployment, problem := serviceExplanationDeployment(ctx, store, caller, in.DeploymentID)
	if problem != nil {
		return e.ServiceBindingExplanation, problem
	}
	e.CallerDeploymentID, e.Scope = deployment.ID, explanationScope(deployment.Scope)
	switch {
	case in.Scope != "" && in.Scope != e.Scope:
		e.stop("context", "denied", "deployment_scope_mismatch")
	case deployment.Status != state.DeployLive:
		e.stop("lifecycle", "denied", "caller_deployment_not_live")
	case caller.PreviewOfSlug != "":
		e.stop("context", "indeterminate", "preview_context_unsupported")
	case deployment.EnvironmentWorkloadHeld():
		e.stop("context", "indeterminate", "qualification_context_unsupported")
	default:
		e.resolve(ctx, store, caller, deployment, service)
	}
	e.CheckedAt = time.Now().UTC()
	return e.ServiceBindingExplanation, nil
}

func validateServiceExplanation(service string, in api.ExplainServiceBindingOptions) (string, string, *api.Problem) {
	id, err := uuid.Parse(in.DeploymentID)
	if err != nil || id == uuid.Nil || !api.ValidAppSlug(service) {
		return "", "", api.ErrValidation("a service name and nonzero deployment UUID are required")
	}
	if in.Scope != "" {
		if problem := api.ValidateScope(in.Scope); problem != nil {
			return "", "", problem
		}
	}
	method, path := in.Method, in.Path
	if method == "" {
		method = "GET"
	}
	if path == "" {
		path = "/"
	}
	method, path, err = api.NormalizeServiceExplanationRequest(method, path)
	if err != nil {
		return "", "", api.ErrValidation(err.Error())
	}
	return method, path, nil
}

func serviceExplanationDeployment(ctx context.Context, store ServiceExplanationStore, caller state.App, id string) (state.Deployment, *api.Problem) {
	parsed, _ := uuid.Parse(id) // validated before any store reads
	deployment, err := store.DeploymentByID(ctx, parsed.String())
	if errors.Is(err, state.ErrNotFound) {
		deployment, err = store.DeploymentByID(ctx, strings.ReplaceAll(parsed.String(), "-", ""))
	}
	if errors.Is(err, state.ErrNotFound) || err == nil && deployment.AppID != caller.ID {
		return state.Deployment{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Deployment not found", "No such deployment for this app.")
	}
	if err != nil {
		return state.Deployment{}, api.ErrCapacity("service explanation deployment could not be read")
	}
	return deployment, nil
}

func explanationScope(scope string) string {
	if scope == "" {
		return state.DefaultEnvScope
	}
	return scope
}

func (e *serviceExplanation) resolve(ctx context.Context, store ServiceExplanationStore, caller state.App, deployment state.Deployment, service string) {
	var route state.EnvironmentGitOpsServiceBindingRoute
	managed := deployment.EnvironmentWorkloadManaged()
	if managed {
		e.PolicySource = "environment_release"
		if !e.managedContext(ctx, store, caller, deployment, service) {
			return
		}
		resolver, ok := store.(state.EnvironmentGitOpsServiceBindingStore)
		if !ok {
			e.stop("routing", "indeterminate", "managed_resolver_unavailable")
			return
		}
		resolved, isManaged, found, err := resolver.ResolveEnvironmentGitOpsServiceBinding(ctx, caller.ID, deployment.ID, service)
		switch {
		case errors.Is(err, state.ErrConflict), errors.Is(err, state.ErrInvalidArgument):
			e.stop("routing", "denied", "managed_route_denied")
		case err != nil:
			e.stop("routing", "indeterminate", "managed_route_unavailable")
		case !isManaged:
			e.stop("routing", "indeterminate", "managed_context_changed")
		case !found:
			e.stop("binding", "denied", "service_not_bound")
		default:
			route = resolved
		}
		if route.TargetAppID == "" {
			return // A managed miss must never fall back to the global slug.
		}
	}
	target, err := explanationTarget(ctx, store, service, route.TargetAppID)
	if err != nil || target.AccountID != caller.AccountID || target.AccountID == "" ||
		managed && target.ID != route.TargetAppID || !managed && target.Slug != service {
		e.stop("target_read", "indeterminate", "target_unreadable")
		return
	}
	if target.PreviewOfSlug != "" {
		e.stop("context", "indeterminate", "preview_context_unsupported")
		return
	}
	e.TargetAppID, e.TargetFingerprint = target.ID, state.ServiceBindingFingerprint(target)
	policy, err := servicebinding.NewAuthorizer(store)(ctx, caller.ID, target.ID)
	if err != nil {
		e.authorizationError(err)
		return
	}
	e.check("app_authorization", "allowed", "app_policy_granted", "")
	if managed {
		if route.AccountID != caller.AccountID || route.CallerAppID != caller.ID || route.CallerDeploymentID != deployment.ID || route.ReleaseSetID == "" || route.TargetDeploymentID == "" {
			e.stop("routing", "indeterminate", "managed_context_changed")
			return
		}
		e.ReleaseID, e.TargetDeploymentID = route.ReleaseSetID, route.TargetDeploymentID
		policy.RequireHTTPS, policy.CallScope = route.RequireHTTPS, route.CallScope
		e.check("routing", "allowed", "active_reviewed_release", "")
	} else {
		e.PolicySource = "app"
		if !e.releaseRoute(ctx, store, caller, deployment, target) {
			return
		}
	}
	e.Transport = api.ServiceBindingTransportHTTP
	if policy.RequireHTTPS {
		e.Transport = api.ServiceBindingTransportHTTPS
		if !managed {
			allowed, err := servicebinding.NewAliasAllowed(store)(ctx, caller.ID, service)
			if err != nil {
				e.stop("binding", "indeterminate", "alias_authorization_unavailable")
				return
			}
			if !allowed {
				e.stop("binding", "denied", "canonical_alias_not_declared")
				return
			}
		}
	}
	if policy.CallScope != nil && !policy.CallScope.Allows(e.Method, e.Path) {
		e.stop("inbound_scopes", "denied", "method_path_not_granted")
		return
	}
	e.check("inbound_scopes", "allowed", "method_path_granted", "allowed_service_call_scopes")
	e.Result = "allowed"
}

func explanationTarget(ctx context.Context, store ServiceExplanationStore, service, id string) (state.App, error) {
	if id != "" {
		return store.AppByID(ctx, id)
	}
	return store.AppBySlug(ctx, service)
}

func (e *serviceExplanation) authorizationError(err error) {
	switch {
	case errors.Is(err, gateway.ErrServiceProxyBindingDenied):
		e.stop("app_authorization", "denied", "outbound_binding_denied")
	case errors.Is(err, gateway.ErrServiceProxyCallerDenied):
		e.stop("app_authorization", "denied", "inbound_caller_denied")
	case errors.Is(err, gateway.ErrServiceProxyDenied):
		e.stop("app_authorization", "denied", "app_identity_denied")
	default:
		e.stop("app_authorization", "indeterminate", "app_authorization_unavailable")
	}
}

func (e *serviceExplanation) managedContext(ctx context.Context, store ServiceExplanationStore, caller state.App, deployment state.Deployment, service string) bool {
	frozen, err := deployment.ScopedWorkloadRuntime()
	if err != nil || frozen == nil {
		e.stop("context", "indeterminate", "managed_policy_unreadable")
		return false
	}
	if explanationScope(frozen.Scope) != e.Scope {
		e.stop("context", "denied", "managed_environment_mismatch")
		return false
	}
	reader, ok := store.(interface {
		ActiveProjectReleaseSet(context.Context, string, string, string) (state.ProjectReleaseSet, error)
	})
	if !ok {
		e.stop("routing", "indeterminate", "active_release_unavailable")
		return false
	}
	release, err := reader.ActiveProjectReleaseSet(ctx, caller.AccountID, caller.ProjectID, e.Scope)
	if errors.Is(err, state.ErrNotFound) {
		e.stop("routing", "denied", "active_release_missing")
		return false
	}
	if err != nil {
		e.stop("routing", "indeterminate", "active_release_unavailable")
		return false
	}
	member := false
	for _, m := range release.Members {
		member = member || m.AppID == caller.ID && m.DeploymentID == deployment.ID
	}
	if !member {
		e.stop("routing", "denied", "caller_not_in_active_release")
		return false
	}
	for _, binding := range frozen.ServiceBindings {
		if strings.EqualFold(binding.Workload, service) {
			return true
		}
	}
	e.stop("binding", "denied", "service_not_bound")
	return false
}

func (e *serviceExplanation) releaseRoute(ctx context.Context, store ServiceExplanationStore, caller state.App, deployment state.Deployment, target state.App) bool {
	resolver, ok := store.(interface {
		ResolveServiceRelease(context.Context, string, string, string, string) (string, string, error)
	})
	if !ok {
		e.stop("routing", "indeterminate", "release_resolver_unavailable")
		return false
	}
	release, targetDeployment, err := resolver.ResolveServiceRelease(ctx, caller.ID, deployment.ID, target.ID, "")
	if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
		e.stop("routing", "denied", "release_route_denied")
		return false
	}
	if err != nil {
		e.stop("routing", "indeterminate", "release_route_unavailable")
		return false
	}
	e.ReleaseID, e.TargetDeploymentID = release, targetDeployment
	reason := "project_release_route"
	if release == "" {
		reason = "unpinned_service_route"
	}
	e.check("routing", "allowed", reason, "")
	return true
}
