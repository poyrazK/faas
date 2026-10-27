package state

import (
	"context"
	"encoding/json"
	"errors"
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

// InvocationTarget is a scheduler delivery claim, verified against its running
// instance and scoped live deployment before forwarding.
type InvocationTarget struct {
	InstanceID, NodeID, DeploymentID string
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
	if snapshot, ok := store.(InvocationVersionSnapshotStore); ok {
		prepared, version, _, err := resolveInvocationDispatch(ctx, snapshot, inv, nil, "", false)
		return prepared, version, err
	}
	// Compatibility for minimal unpinned adapters. Hide optional pool resolvers:
	// a pin requires a committed snapshot and cannot use independent reads.
	return resolveInvocationVersion(ctx, struct{ invocationAppReader }{store}, inv, "", false)
}

// ResolveInvocationDispatch reads owner, version and optional delivery target in
// one committed view. No selection or resolved owner escapes a failed snapshot.
// Passing nil checks before wake; passing a target checks again before delivery.
func ResolveInvocationDispatch(ctx context.Context, store invocationAppReader, inv Invocation, target *InvocationTarget) (Invocation, InvocationVersion, string, error) {
	snapshot, ok := store.(InvocationVersionSnapshotStore)
	if !ok {
		return inv, InvocationVersion{}, "", ErrConflict
	}
	return resolveInvocationDispatch(ctx, snapshot, inv, target, "", false)
}

func resolveInvocationDispatch(ctx context.Context, snapshot InvocationVersionSnapshotStore, inv Invocation, target *InvocationTarget, environment string, ingress bool) (Invocation, InvocationVersion, string, error) {
	var prepared Invocation
	var version InvocationVersion
	var owner string
	err := snapshot.WithInvocationVersionSnapshot(ctx, func(reader InvocationVersionReader) error {
		app, err := reader.AppByID(ctx, inv.AppID)
		if err != nil {
			return err
		}
		prepared, version, err = resolveInvocationVersionForApp(ctx, reader, inv, app, environment, ingress)
		if err != nil {
			return err
		}
		if target != nil {
			if target.InstanceID == "" || target.NodeID == "" || target.DeploymentID == "" || version.DeploymentID != "" && target.DeploymentID != version.DeploymentID {
				return ErrNotFound
			}
			allowed, err := reader.InvocationTargetAllowed(ctx, app.ID, version.Scope, *target)
			if err != nil {
				return err
			}
			if !allowed {
				return ErrNotFound
			}
		}
		owner = app.AccountID
		return nil
	})
	if err != nil {
		return inv, InvocationVersion{}, "", err
	}
	return prepared, version, owner, nil
}

// ResolveInvocationVersionForEnvironment authenticates a trusted ingress scope
// in the same committed view as owner, release and private work admission.
func ResolveInvocationVersionForEnvironment(ctx context.Context, store invocationAppReader, inv Invocation, environment string) (Invocation, InvocationVersion, error) {
	if snapshot, ok := store.(InvocationVersionSnapshotStore); ok {
		prepared, version, _, err := resolveInvocationDispatch(ctx, snapshot, inv, nil, environment, true)
		return prepared, version, err
	}
	return resolveInvocationVersion(ctx, struct{ invocationAppReader }{store}, inv, environment, true)
}

func resolveInvocationVersion(ctx context.Context, store invocationAppReader, inv Invocation, environment string, ingress bool) (Invocation, InvocationVersion, error) {
	// Only the workflow resolver below may assert a step's Operation identity.
	inv.WorkflowOperation = nil
	app, err := store.AppByID(ctx, inv.AppID)
	if err != nil {
		return inv, InvocationVersion{}, err
	}
	return resolveInvocationVersionForApp(ctx, store, inv, app, environment, ingress)
}

func resolveInvocationVersionForApp(ctx context.Context, store invocationAppReader, inv Invocation, app App, environment string, ingress bool) (Invocation, InvocationVersion, error) {
	if app.Status == AppDeleted || app.DeletedAt != nil || inv.AccountID != "" && inv.AccountID != app.AccountID {
		return inv, InvocationVersion{}, ErrNotFound
	}
	// PgStore and MemStore combine persisted canary membership with the owner
	// read already required for durable dispatch. Never trust inv.Source or a
	// payload/header marker as a negative membership assertion.
	ownerRead := false
	if reader, ok := store.(invocationDispatchAuthorityReader); ok && canonicalStateUUID(inv.ID) {
		authority, readErr := reader.invocationDispatchAuthority(ctx, inv.ID)
		if readErr != nil && !errors.Is(readErr, ErrNotFound) {
			return inv, InvocationVersion{}, readErr
		}
		ownerRead = true
		if readErr == nil {
			if authority.EnvironmentID != inv.EnvironmentID {
				return inv, InvocationVersion{}, ErrInvocationEnvironmentWorkIsolation
			}
			if authority.QueueCanary {
				// The gateway authenticates ESM claims against the durable row.
				// Its transport omits QueueName; recover that private identity
				// before applying bound-queue environment and release checks.
				if inv.Source == InvocationSource("esm") && inv.QueueName == "" {
					inv.QueueName = authority.QueueName
					inv.Source = InvocationQueue
				}
				if authority.AppID != app.ID || authority.AccountID != app.AccountID || ingress || inv.Source != InvocationQueue || authority.QueueBindingID != inv.QueueBindingID || authority.QueueName != inv.QueueName || authority.Scope != inv.DeploymentScope {
					return inv, InvocationVersion{}, ErrConflict
				}
				if err := validateQueueCanaryDispatch(ctx, store, app, inv, true); err != nil {
					return inv, InvocationVersion{}, err
				}
			}
		}
	} else if !ingress {
		// Older store adapters retain the conservative persisted lookup.
		if err := validateQueueCanaryDispatch(ctx, store, app, inv, false); err != nil {
			return inv, InvocationVersion{}, err
		}
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
	// Workflow calls have synthetic correlation IDs, not invocation UUIDs.
	// Validate their separate durable run authority before reading an HTTP
	// invocation's environment owner. Unsupported workflow calls keep the
	// ordinary resolution path and receive no private operation authority.
	if inv.Source == InvocationSource("workflow") && !ingress {
		resolved, version, linked, err := resolveWorkflowOperationInvocationVersion(ctx, store, app, inv, headers)
		if err != nil || linked {
			return resolved, version, err
		}
	}
	// Durable private work must retain its original marker and explicit pin.
	// Never repair a damaged envelope by resolving today's active release.
	if reader, ok := store.(InvocationEnvironmentOwnerReader); ok && !ownerRead && inv.ID != "" && !syntheticTriggerInvocation(inv) {
		stored, readErr := reader.InvocationEnvironmentID(ctx, inv.ID)
		if readErr != nil && !errors.Is(readErr, ErrNotFound) {
			return inv, InvocationVersion{}, readErr
		}
		if readErr == nil && stored != inv.EnvironmentID {
			return inv, InvocationVersion{}, ErrInvocationEnvironmentWorkIsolation
		}
	}
	if inv.EnvironmentID != "" && revision == "" && release == "" {
		return inv, InvocationVersion{}, ErrInvocationEnvironmentWorkIsolation
	}
	if InvocationHasOperation(inv) {
		return resolveOperationInvocationVersion(ctx, store, app, inv)
	}
	if inv.WorkflowRunID != "" {
		pinned, version, handled, err := resolveWorkflowInvocationVersion(ctx, store, app, inv, revision, release)
		if handled || err != nil {
			return pinned, version, err
		}
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
				if inv.EnvironmentID != "" {
					return inv, InvocationVersion{}, ErrInvocationEnvironmentWorkIsolation
				}
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
	if ingress && !projectApp && scope != DefaultEnvScope {
		return inv, InvocationVersion{}, ErrNotFound
	}
	boundQueue, err := validateBoundQueueEnvironment(ctx, store, inv, scope)
	if err != nil {
		return inv, InvocationVersion{}, err
	}
	if projectApp && invocationStageScope(scope) {
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
	resolver, ok := store.(InvocationVersionReader)
	if !ok {
		return inv, InvocationVersion{}, ErrConflict
	}
	jobPinned := false
	if !ingress && projectApp && scope == "production" && inv.QueueBindingID != "" && inv.Attempts > 0 && app.Manifest.ExecutionMode == api.ExecutionModeWorker && (inv.Source == InvocationQueue || inv.Source == InvocationReplay || inv.Source == InvocationSource("esm")) {
		if pins, supported := store.(projectWorkerReleasePinStore); supported {
			claimed := inv
			claimed.AccountID = app.AccountID
			version, jobPinned, err = pins.ResolveProjectWorkerJobRelease(ctx, claimed, revision, release)
			if err != nil {
				return inv, InvocationVersion{}, err
			}
		}
	}
	if jobPinned {
		// Durable lineage authority has already resolved and validated the graph.
	} else if revision != "" {
		dep, err := resolver.ResolveRevisionPin(ctx, app.ID, scope, revision)
		if err != nil {
			return inv, InvocationVersion{}, err
		}
		version.DeploymentID = dep.ID
	} else {
		var err error
		if ingress && release == "" && projectApp && app.Manifest.ExecutionMode != api.ExecutionModeWorker {
			if canary, ok := store.(interface {
				ResolveProjectCanary(context.Context, string, string, string) (string, string, error)
			}); ok {
				version.ReleaseID, version.DeploymentID, err = canary.ResolveProjectCanary(ctx, app.ID, scope, uuid.NewString())
			} else {
				version.ReleaseID, version.DeploymentID, err = resolver.ResolveProjectRelease(ctx, app.ID, scope, release)
			}
		} else {
			version.ReleaseID, version.DeploymentID, err = resolver.ResolveProjectRelease(ctx, app.ID, scope, release)
		}
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
		if projectApp && scope == "production" {
			if version.Scope, err = projectProductionDispatchScope(ctx, store, app.ID); err != nil {
				return inv, InvocationVersion{}, err
			}
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
	encoded, err := json.Marshal(headers)
	if err != nil {
		return inv, InvocationVersion{}, err
	}
	inv.Headers = encoded
	return inv, version, nil
}

// projectProductionDispatchScope selects the deployment scope that serves a
// project app's production environment when no release graph pins one.
// Project deployments made without a named environment keep the legacy
// default scope, and the gateway routes that lane as production
// (ProductionRoutingDeployments). Durable work must wake the same lane:
// a wake in scope "production" finds no deployment there at all. The
// captured inv.DeploymentScope stays "production", so each attempt reselects
// and a release graph published later still takes precedence.
func projectProductionDispatchScope(ctx context.Context, store invocationAppReader, appID string) (string, error) {
	reader, ok := store.(interface {
		LiveDeployments(context.Context, string) ([]Deployment, error)
	})
	if !ok {
		return "production", nil
	}
	deployments, err := reader.LiveDeployments(ctx, appID)
	if err != nil {
		return "", err
	}
	for _, deployment := range ProductionRoutingDeployments(deployments) {
		if deployment.AppID == appID && deployment.Status == DeployLive && deployment.TrafficPercent > 0 {
			return normalizedDeploymentScope(deployment.Scope), nil
		}
	}
	return "production", nil
}

func invocationStageScope(scope string) bool {
	return scope != "production" && scope != DefaultEnvScope
}

// ProductionDeploymentScope reports whether a deployment scope (or a
// workload token's environment) is the production lane: "production", the
// default scope, or no scope. Every other scope is a stage, staging or a
// preview, whose code must not touch production work.
func ProductionDeploymentScope(scope string) bool {
	return !invocationStageScope(normalizedDeploymentScope(scope))
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

// Durable operation pins are trusted admission metadata. They outlive public
// revision-pin TTLs, and cannot move to a newer deployment when dispatch waits.
func resolveOperationInvocationVersion(ctx context.Context, store invocationAppReader, app App, inv Invocation) (Invocation, InvocationVersion, error) {
	operations, ok := store.(interface {
		OperationByID(context.Context, string, string, string) (Operation, error)
	})
	if !ok {
		return inv, InvocationVersion{}, ErrConflict
	}
	op, err := operations.OperationByID(ctx, inv.AccountID, inv.PlatformTenantID, inv.OperationID)
	if err != nil {
		return inv, InvocationVersion{}, err
	}
	if app.AccountID != op.AccountID || app.ID != op.AppID || app.Status == AppDeleted || op.CurrentInvocationID != inv.ID || (op.State != api.OperationAccepted && op.State != api.OperationRunning) {
		return inv, InvocationVersion{}, ErrOperationStaleAttempt
	}
	reader, ok := store.(interface {
		DeploymentByID(context.Context, string) (Deployment, error)
	})
	if !ok {
		return inv, InvocationVersion{}, ErrConflict
	}
	dep, err := reader.DeploymentByID(ctx, op.DeploymentID)
	if err != nil {
		return inv, InvocationVersion{}, err
	}
	if dep.AppID != app.ID || dep.Scope != op.Scope || dep.Status != DeployLive || dep.DeletedAt != nil {
		return inv, InvocationVersion{}, ErrConflict
	}
	return inv, InvocationVersion{DeploymentID: op.DeploymentID, ReleaseID: op.ReleaseID, Scope: op.Scope}, nil
}
