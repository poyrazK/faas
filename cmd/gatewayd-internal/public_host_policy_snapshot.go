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
	var app gateway.App
	var found bool
	var err error
	projection := *source
	if projection.Slug != "" {
		app, found, err = router.appBySlug(ctx, projection.Slug)
	} else {
		app, found, err = router.resolveHost(ctx, projection.Host)
		if err == nil {
			projection.CanSubstitute, err = router.publicHostSubstitutionAllowed(ctx, reader, projection.Host, app, found)
		}
	}
	if err == nil && found {
		err = resolvePublicDeclaredRoutePolicy(ctx, reader, &app)
	}
	if err == nil {
		err = resolvePublicCompiledPolicy(ctx, reader, &app, found, &projection)
	}
	if err == nil {
		projection.Revision = reader.PublicHostPolicyRevision()
		app.PublicPolicySource = &projection
		if _, pinned := ctx.Value(publicRouteGraphsKey{}).(publicRouteGraphs); pinned && projection.Slug != "" && found {
			app.PublicRouteSource = gateway.PublicRouteSourceClaim(ctx)
			err = verifyPublicRouteSourceClaim(ctx, reader.NewProjectionReader(), app)
		}
	}
	return app, found, err
}

func (r pgRouter) publicHostSubstitutionAllowed(ctx context.Context, reader state.PublicHostPolicyReader, host string, app gateway.App, found bool) (bool, error) {
	if _, _, matched := gateway.EnvironmentIDsFromHost(r.deploySuffix, host); matched {
		return false, nil
	}
	if _, _, matched := gateway.DeploymentScopeFromHost(r.deploySuffix, host); matched {
		return false, nil
	}
	if _, matched := r.deploymentAliasLabelForHost(host); matched {
		return false, nil // The reserved tag- namespace survives alias removal.
	}
	if found {
		return app.PinnedDeploymentID == "", nil
	}
	var reserved bool
	var err error
	if slug, platform := r.slugFor(host); platform {
		reserved, err = reader.PublicHostReserved(ctx, slug, "")
	} else {
		reserved, err = reader.PublicHostReserved(ctx, "", host)
	}
	return !reserved, err
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
	return verifyPublicRouteSourcePolicy(ctx, view, app)
}

func verifyPublicRouteSourcePolicy(ctx context.Context, reader state.PublicRoutingHostPolicyReader, app gateway.App) error {
	return verifyPublicRouteSourceClaim(ctx, reader.HostPolicyReader(), app)
}

func verifyPublicRouteSourceClaim(ctx context.Context, host state.PublicHostPolicyReader, app gateway.App) error {
	claim := app.PublicRouteSource
	if app.PublicPolicySource.Slug == "" {
		if claim != nil {
			return errors.New("ordinary public host has an unexpected route source")
		}
		return nil
	}
	if claim == nil || claim.Source == nil || claim.Source.Host == "" || claim.Source.Slug != "" || claim.Source.Revision == "" {
		return errors.New("public route source policy is unavailable")
	}
	if !claim.Source.CanSubstitute {
		return errors.New("public route source does not permit substitution")
	}
	if claim.Found && (claim.AppID == "" || claim.AccountID != app.AccountID) ||
		!claim.Found && (claim.AppID != "" || claim.AccountID != "") {
		return errors.New("public route source owner is inconsistent")
	}
	resolved, found, err := resolvePublicPolicySource(ctx, host, claim.Source)
	if err != nil {
		return err
	}
	if found != claim.Found || resolved.ID != claim.AppID || resolved.AccountID != claim.AccountID ||
		resolved.PublicPolicySource == nil || !resolved.PublicPolicySource.CanSubstitute ||
		host.PublicHostPolicyRevision() != claim.Source.Revision {
		return errors.New("public route source policy changed before dispatch")
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
