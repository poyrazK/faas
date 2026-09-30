// adr: 375
package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// Keep the resolver's dependency read-only, including when a test supplies a
// legacy Store. Optional environment/alias/tenant seams remain read-only too.
type publicHostAppStore interface {
	AppByID(context.Context, string) (state.App, error)
	AppBySlug(context.Context, string) (state.App, error)
	AccountByID(context.Context, string) (state.Account, error)
	DeploymentByID(context.Context, string) (state.Deployment, error)
	DeploymentByRevision(context.Context, string, int) (state.Deployment, error)
	LiveDeploymentForScope(context.Context, string, string) (state.Deployment, error)
	DomainByName(context.Context, string) (state.CustomDomain, error)
	GetProjectEnvironmentEdgePolicy(context.Context, string, string, string) (state.ProjectEnvironmentEdgePolicy, error)
	TenantSurfaceByHostname(context.Context, string) (state.TenantSurface, error)
	GetTenantHostnameByName(context.Context, string) (state.TenantHostname, error)
}

func (r pgRouter) policySource(host, slug string) *gateway.PublicAppPolicySource {
	return &gateway.PublicAppPolicySource{Host: host, Slug: slug, AppsSuffix: r.appsSuffix,
		DeploySuffix: r.deploySuffix, TenantSurfaces: r.tenantSurfacesOn()}
}

func (r pgRouter) resolveHostSnapshot(ctx context.Context, store state.PublicHostPolicySnapshotStore, host string) (gateway.App, bool, error) {
	bounded, cancel := context.WithTimeout(ctx, api.TrafficPublicHostReadTimeout)
	defer cancel()
	var app gateway.App
	var found bool
	source := r.policySource(host, "")
	err := store.WithPublicHostPolicySnapshot(bounded, func(reader state.PublicHostPolicyReader) error {
		var err error
		app, found, err = resolvePublicPolicySource(bounded, reader, source)
		if err == nil && found {
			source.Revision = reader.PublicHostPolicyRevision()
			app.PublicPolicySource = source
		}
		return err
	})
	if err == nil {
		err = bounded.Err()
	}
	return app, found && err == nil, err
}

func (r pgRouter) resolvePublicAppSlug(ctx context.Context, slug string) (gateway.App, bool, error) {
	store, ok := r.store.(state.PublicHostPolicySnapshotStore)
	if !ok {
		return r.appBySlug(ctx, slug)
	}
	bounded, cancel := context.WithTimeout(ctx, api.TrafficPublicHostReadTimeout)
	defer cancel()
	var app gateway.App
	var found bool
	source := r.policySource("", slug)
	err := store.WithPublicHostPolicySnapshot(bounded, func(reader state.PublicHostPolicyReader) error {
		var err error
		app, found, err = resolvePublicPolicySource(bounded, reader, source)
		if err == nil && found {
			source.Revision = reader.PublicHostPolicyRevision()
			app.PublicPolicySource = source
		}
		return err
	})
	if err == nil {
		err = bounded.Err()
	}
	return app, found && err == nil, err
}

func resolvePublicPolicySource(ctx context.Context, reader state.PublicHostPolicyReader, source *gateway.PublicAppPolicySource) (gateway.App, bool, error) {
	if source == nil || (source.Host == "") == (source.Slug == "") {
		return gateway.App{}, false, errors.New("public host policy source is invalid")
	}
	router := pgRouter{store: reader, appsSuffix: source.AppsSuffix, deploySuffix: source.DeploySuffix,
		tenantSurfacesEnabled: func() bool { return source.TenantSurfaces }}
	if source.Slug != "" {
		return router.appBySlug(ctx, source.Slug)
	}
	return router.resolveHost(ctx, source.Host)
}

func verifyPublicHostPolicy(ctx context.Context, reader state.PublicRoutingPolicyReader, app gateway.App) error {
	if app.PublicPolicySource == nil {
		return errors.New("public host policy source is unavailable")
	}
	view, ok := reader.(state.PublicRoutingHostPolicyReader)
	if !ok || app.PublicPolicySource.Revision == "" {
		return errors.New("public host policy verifier is unavailable")
	}
	host := view.HostPolicyReader()
	resolved, found, err := resolvePublicPolicySource(ctx, host, app.PublicPolicySource)
	if err != nil {
		return err
	}
	if !found || resolved.ID != app.ID || resolved.AccountID != app.AccountID ||
		host.PublicHostPolicyRevision() != app.PublicPolicySource.Revision {
		return errors.New("public host policy changed before dispatch")
	}
	return nil
}

func publicAppDeploymentScope(app state.App) string {
	if app.ProjectID != "" && app.PreviewOfSlug == "" {
		return "production"
	}
	return state.DefaultEnvScope
}

func (r pgRouter) livePublicRoutingDeployments(ctx context.Context, app state.App) ([]state.Deployment, error) {
	scope := publicAppDeploymentScope(app)
	if scoped, ok := r.store.(interface {
		LiveDeploymentsForScope(context.Context, string, string) ([]state.Deployment, error)
	}); ok {
		return scoped.LiveDeploymentsForScope(ctx, app.ID, scope)
	}
	legacy, ok := r.store.(interface {
		LiveDeployments(context.Context, string) ([]state.Deployment, error)
	})
	if !ok {
		return nil, errors.New("public deployment ingress reader is unavailable")
	}
	rows, err := legacy.LiveDeployments(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	result := make([]state.Deployment, 0, len(rows))
	for _, row := range rows {
		if (row.Scope == scope || row.Scope == "" && scope == state.DefaultEnvScope) && row.DeletedAt == nil && row.TrafficPercent > 0 {
			result = append(result, row)
		}
	}
	if len(result) > api.TrafficPolicyMaxDeployments {
		return nil, errors.New("public host deployment limit exceeded")
	}
	return result, nil
}
