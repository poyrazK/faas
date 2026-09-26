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

type invocationVersionStore interface {
	ResolveProjectRelease(context.Context, string, string, string) (string, string, error)
	ResolveRevisionPin(context.Context, string, string, string) (Deployment, error)
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
	app, err := store.AppByID(ctx, inv.AppID)
	if err != nil {
		return inv, InvocationVersion{}, err
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
	scope := DefaultEnvScope
	projectApp := app.ProjectID != "" && app.PreviewOfSlug == ""
	if projectApp {
		scope = "production"
	} else if release != "" {
		return inv, InvocationVersion{}, ErrNotFound
	}
	version := InvocationVersion{Scope: scope}
	if revision == "" && !projectApp {
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
	inv.Headers, err = json.Marshal(headers)
	if err != nil {
		return inv, InvocationVersion{}, err
	}
	return inv, version, nil
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
