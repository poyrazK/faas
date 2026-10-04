package state

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// InvocationVersion is the deployment selected for a durable or synthetic
// invocation. A nonempty ReleaseID is also stamped into the guest request so
// managed service calls continue inside the same project graph.
type InvocationVersion struct {
	DeploymentID string
	ReleaseID    string
	Scope        string
}

type invocationVersionStore interface {
	ResolveProjectRelease(context.Context, string, string, string) (string, string, error)
	ResolveRevisionPin(context.Context, string, string, string) (Deployment, error)
}

type invocationAppReader interface {
	AppByID(context.Context, string) (App, error)
}

type invocationPinScopeStore interface {
	ResolveInvocationPinScope(context.Context, string, string, string) (string, error)
}

type invocationEnvironmentStore interface {
	ProjectEnvironmentBySlug(context.Context, string, string, string) (ProjectEnvironment, error)
	DeploymentByID(context.Context, string) (Deployment, error)
}

// A stage requires owned work admission. Queue consumers and completion
// destinations remain unavailable until their environment ownership exists.
var ErrInvocationEnvironmentWorkIsolation = fmt.Errorf("%w: invocation environment work isolation is unavailable", ErrConflict)

// ResolveInvocationVersion validates untrusted pin headers at delivery time.
// It recovers the environment from a pin's owned identity, and selects the
// active production release for project invocations without a pin.
// The returned invocation carries the canonical release header; persisting an
// explicit pin at enqueue and checking it again here prevents a delayed task
// from silently moving to a newer graph after the old one expires.
func ResolveInvocationVersion(ctx context.Context, store invocationAppReader, inv Invocation) (Invocation, InvocationVersion, error) {
	return resolveInvocationVersion(ctx, store, inv, "", false)
}

// ResolveInvocationVersionForEnvironment accepts the environment selected by a
// trusted ingress router. Delivery recovers that scope from the persisted pin;
// customer headers cannot select a different environment on an ingress host.
func ResolveInvocationVersionForEnvironment(ctx context.Context, store invocationAppReader, inv Invocation, environment string) (Invocation, InvocationVersion, error) {
	return resolveInvocationVersion(ctx, store, inv, environment, true)
}

func resolveInvocationVersion(ctx context.Context, store invocationAppReader, inv Invocation, environment string, ingress bool) (Invocation, InvocationVersion, error) {
	app, err := store.AppByID(ctx, inv.AppID)
	if err != nil {
		return inv, InvocationVersion{}, err
	}
	if inv.AccountID != "" && inv.AccountID != app.AccountID {
		return inv, InvocationVersion{}, ErrNotFound
	}
	headers := map[string]string{}
	if len(inv.Headers) > 0 {
		if err := json.Unmarshal(inv.Headers, &headers); err != nil {
			return inv, InvocationVersion{}, ErrInvalidArgument
		}
		if headers == nil {
			// Older async route producers persisted a nil map as JSON null.
			// Treat it as an empty envelope so existing work remains deliverable.
			headers = map[string]string{}
		}
	}
	revision, release, err := invocationPinHeaders(headers)
	if err != nil {
		return inv, InvocationVersion{}, err
	}
	capturedScope := inv.DeploymentScope != ""
	projectApp := app.ProjectID != "" && app.PreviewOfSlug == ""
	scope, err := invocationDeploymentScope(app, inv.DeploymentScope)
	if err != nil {
		return inv, InvocationVersion{}, err
	}
	inv.DeploymentScope = scope
	if !projectApp && release != "" {
		return inv, InvocationVersion{}, ErrNotFound
	}
	if ingress {
		if environment != "" && api.ValidateScope(environment) != nil {
			return inv, InvocationVersion{}, ErrInvalidArgument
		}
		scope = normalizedDeploymentScope(environment)
		if projectApp {
			scope = workloadEnvironmentSlug(scope)
		}
	}
	if revision != "" || release != "" {
		if reader, ok := store.(invocationPinScopeStore); ok {
			pinScope, scopeErr := reader.ResolveInvocationPinScope(ctx, app.ID, revision, release)
			if scopeErr != nil {
				return inv, InvocationVersion{}, scopeErr
			}
			if (ingress || capturedScope) && !invocationScopesMatch(projectApp, scope, pinScope) {
				return inv, InvocationVersion{}, ErrNotFound
			}
			scope = pinScope
		} else if invocationStageScope(scope) {
			return inv, InvocationVersion{}, ErrConflict
		}
	}
	if api.ValidateScope(scope) != nil {
		return inv, InvocationVersion{}, ErrConflict
	}
	if !projectApp && scope != DefaultEnvScope {
		return inv, InvocationVersion{}, ErrNotFound
	}
	boundQueue, err := validateBoundQueueEnvironment(ctx, store, inv, scope)
	if err != nil {
		return inv, InvocationVersion{}, err
	}
	if invocationStageScope(scope) {
		reader, ok := store.(invocationEnvironmentStore)
		if !ok {
			return inv, InvocationVersion{}, ErrConflict
		}
		env, lookupErr := reader.ProjectEnvironmentBySlug(ctx, app.AccountID, app.ProjectID, scope)
		if lookupErr != nil {
			return inv, InvocationVersion{}, lookupErr
		}
		if env.AccountID != app.AccountID || env.ProjectID != app.ProjectID || env.Slug != scope {
			return inv, InvocationVersion{}, ErrConflict
		}
		if (!boundQueue && invocationHasSharedWorkProducer(inv) && (inv.ID == "" || inv.EnvironmentID == "" || !stageQueueInvocationShape(inv))) ||
			inv.OnSuccessDestinationID != "" || inv.OnFailureDestinationID != "" {
			return inv, InvocationVersion{}, ErrInvocationEnvironmentWorkIsolation
		}
	}
	inv.DeploymentScope = scope
	version := InvocationVersion{Scope: scope}
	if revision == "" && !projectApp {
		if err := validateInvocationWorkEnvironmentAdmission(ctx, store, inv, version); err != nil {
			return inv, InvocationVersion{}, err
		}
		return inv, version, nil
	}
	resolver, ok := store.(invocationVersionStore)
	if !ok {
		return inv, InvocationVersion{}, ErrConflict
	}
	if revision != "" {
		dep, err := resolver.ResolveRevisionPin(ctx, app.ID, scope, revision)
		if err != nil {
			return inv, InvocationVersion{}, err
		}
		version.DeploymentID = dep.ID
	} else {
		version.ReleaseID, version.DeploymentID, err = resolver.ResolveProjectRelease(ctx, app.ID, scope, release)
		if err != nil {
			return inv, InvocationVersion{}, err
		}
	}
	if err := validateInvocationWorkEnvironmentAdmission(ctx, store, inv, version); err != nil {
		return inv, InvocationVersion{}, err
	}
	if version.DeploymentID == "" {
		if invocationStageScope(scope) && !boundQueue {
			return inv, InvocationVersion{}, ErrNotFound
		}
		return inv, version, nil
	}
	if invocationStageScope(scope) {
		dep, lookupErr := store.(invocationEnvironmentStore).DeploymentByID(ctx, version.DeploymentID)
		if lookupErr != nil {
			return inv, InvocationVersion{}, lookupErr
		}
		if dep.AppID != app.ID || normalizedDeploymentScope(dep.Scope) != scope || dep.Status != DeployLive {
			return inv, InvocationVersion{}, ErrConflict
		}
	}
	for key := range headers {
		if strings.EqualFold(key, api.RevisionHeader) || strings.EqualFold(key, api.ReleaseHeader) {
			delete(headers, key)
		}
	}
	if version.ReleaseID != "" {
		headers[api.ReleaseHeader] = version.ReleaseID
	} else {
		headers[api.RevisionHeader] = version.DeploymentID
	}
	inv.Headers, err = json.Marshal(headers)
	if err != nil {
		return inv, InvocationVersion{}, err
	}
	return inv, version, nil
}

func invocationStageScope(scope string) bool {
	return scope != "production" && scope != DefaultEnvScope
}

func invocationScopesMatch(projectApp bool, expected, actual string) bool {
	if projectApp {
		return workloadEnvironmentSlug(expected) == workloadEnvironmentSlug(actual)
	}
	return normalizedDeploymentScope(expected) == normalizedDeploymentScope(actual)
}

// DefaultInvocationDeploymentScope is the admission scope for producers that
// have no explicit environment. It must never reinterpret already stored work.
func DefaultInvocationDeploymentScope(app App) string {
	if app.ProjectID != "" && app.PreviewOfSlug == "" {
		return "production"
	}
	return DefaultEnvScope
}

func invocationDeploymentScope(app App, scope string) (string, error) {
	if scope == "" {
		scope = DefaultInvocationDeploymentScope(app)
	}
	if err := api.ValidateScope(scope); err != nil {
		return "", ErrInvalidArgument
	}
	return scope, nil
}

func invocationPinHeaders(headers map[string]string) (revision, release string, err error) {
	var revisionSeen, releaseSeen bool
	for key, value := range headers {
		switch {
		case strings.EqualFold(key, api.RevisionHeader):
			if revisionSeen {
				return "", "", ErrInvalidArgument
			}
			revisionSeen, revision = true, strings.TrimSpace(value)
		case strings.EqualFold(key, api.ReleaseHeader):
			if releaseSeen {
				return "", "", ErrInvalidArgument
			}
			releaseSeen, release = true, strings.TrimSpace(value)
		}
	}
	if revisionSeen && releaseSeen {
		return "", "", ErrConflict
	}
	if revisionSeen {
		if _, parseErr := uuid.Parse(revision); parseErr != nil {
			return "", "", ErrInvalidArgument
		}
	}
	if releaseSeen {
		if _, parseErr := uuid.Parse(release); parseErr != nil {
			return "", "", ErrInvalidArgument
		}
	}
	return revision, release, nil
}
