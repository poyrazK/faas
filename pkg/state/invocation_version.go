package state

import (
	"context"
	"encoding/json"
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

// ResolveInvocationVersion validates untrusted pin headers at delivery time.
// It also selects the active release for project invocations without a pin.
// The returned invocation carries the canonical release header; persisting an
// explicit pin at enqueue and checking it again here prevents a delayed task
// from silently moving to a newer graph after the old one expires.
func ResolveInvocationVersion(ctx context.Context, store invocationAppReader, inv Invocation) (Invocation, InvocationVersion, error) {
	if snapshot, ok := store.(InvocationVersionSnapshotStore); ok {
		prepared, version, _, err := resolveInvocationDispatch(ctx, snapshot, inv, nil)
		return prepared, version, err
	}
	// Compatibility for minimal unpinned adapters. Hide optional pool resolvers:
	// a pin requires a committed snapshot and cannot use independent reads.
	return resolveInvocationVersion(ctx, struct{ invocationAppReader }{store}, inv)
}

// ResolveInvocationDispatch reads owner, version and optional delivery target in
// one committed view. No selection or resolved owner escapes a failed snapshot.
// Passing nil checks before wake; passing a target checks again before delivery.
func ResolveInvocationDispatch(ctx context.Context, store invocationAppReader, inv Invocation, target *InvocationTarget) (Invocation, InvocationVersion, string, error) {
	snapshot, ok := store.(InvocationVersionSnapshotStore)
	if !ok {
		return inv, InvocationVersion{}, "", ErrConflict
	}
	return resolveInvocationDispatch(ctx, snapshot, inv, target)
}

func resolveInvocationDispatch(ctx context.Context, snapshot InvocationVersionSnapshotStore, inv Invocation, target *InvocationTarget) (Invocation, InvocationVersion, string, error) {
	var prepared Invocation
	var version InvocationVersion
	var owner string
	err := snapshot.WithInvocationVersionSnapshot(ctx, func(reader InvocationVersionReader) error {
		app, err := reader.AppByID(ctx, inv.AppID)
		if err != nil {
			return err
		}
		prepared, version, err = resolveInvocationVersionForApp(ctx, reader, inv, app)
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

func resolveInvocationVersion(ctx context.Context, store invocationAppReader, inv Invocation) (Invocation, InvocationVersion, error) {
	app, err := store.AppByID(ctx, inv.AppID)
	if err != nil {
		return inv, InvocationVersion{}, err
	}
	return resolveInvocationVersionForApp(ctx, store, inv, app)
}

func resolveInvocationVersionForApp(ctx context.Context, store invocationAppReader, inv Invocation, app App) (Invocation, InvocationVersion, error) {
	if app.Status == AppDeleted || app.DeletedAt != nil || inv.AccountID != "" && inv.AccountID != app.AccountID {
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
	projectApp := app.ProjectID != "" && app.PreviewOfSlug == ""
	scope, err := invocationDeploymentScope(app, inv.DeploymentScope)
	if err != nil {
		return inv, InvocationVersion{}, err
	}
	inv.DeploymentScope = scope
	if !projectApp && release != "" {
		return inv, InvocationVersion{}, ErrNotFound
	}
	version := InvocationVersion{Scope: scope}
	if revision == "" && !projectApp {
		return inv, version, nil
	}
	resolver, ok := store.(InvocationVersionReader)
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
		var err error
		version.ReleaseID, version.DeploymentID, err = resolver.ResolveProjectRelease(ctx, app.ID, scope, release)
		if err != nil {
			return inv, InvocationVersion{}, err
		}
	}
	if version.DeploymentID == "" {
		return inv, version, nil
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
