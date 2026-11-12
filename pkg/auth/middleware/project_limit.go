package middleware

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// KeyProjectLimiter is the optional store seam that loads the projects an
// agent or mcp-login key is limited to (ADR-20261104085712).
type KeyProjectLimiter interface {
	APIKeyProjectLimit(ctx context.Context, keyID string) ([]string, bool, error)
}

// ProjectResolver is the optional store seam that resolves a project route's
// {slug} for a project-limited key.
type ProjectResolver interface {
	ProjectBySlug(ctx context.Context, accountID, slug string) (state.Project, error)
}

// OrgProjectResolver is the optional store seam that resolves an
// organization project route's {organization} and {slug} for a
// project-limited key: project slugs are unique only within an owner.
type OrgProjectResolver interface {
	OrgBySlug(ctx context.Context, slug string) (state.Org, error)
	ProjectByOrgSlug(ctx context.Context, orgID, slug string) (state.Project, error)
}

// ProjectLimitable reports whether a key may carry a project limit: agent
// keys and hosted MCP sign-ins' mcp-login keys.
func ProjectLimitable(key state.APIKey) bool {
	return key.ProjectLimitable()
}

// AppOutsideKeyProjects reports whether a project-limited key may not reach
// app: an app outside every project is outside every limit.
func AppOutsideKeyProjects(key *state.APIKey, app state.App) bool {
	return key != nil && key.ProjectLimited && (app.ProjectID == "" || !slices.Contains(key.ProjectIDs, app.ProjectID))
}

// projectLimitedOpenRoutes are the routes a project-limited key reaches
// besides /v1/apps/{slug} and /v1/projects/{slug}: lists the handlers narrow
// to the key's projects, routes whose handlers authorize the app they name in
// the body or a stored session, and the key's own identity. Every other route
// is closed to it until someone adds it here deliberately.
var projectLimitedOpenRoutes = map[string]bool{
	"GET /v1/apps":                         true,
	"GET /v1/orgs/{slug}/apps":             true,
	"GET /v1/projects":                     true,
	"GET /v1/orgs/{organization}/projects": true,
	"POST /v1/domains":                     true,
	"POST /v1/crons":                       true,
	"GET /v1/account":                      true,
	"GET /v1/capabilities":                 true,
	"PUT /v1/agent-connections/self":       true,
	"DELETE /v1/agent-connections/self":    true,
}

// AppByIDResolver is the optional store seam that loads a deployment read's
// app for a project-limited key.
type AppByIDResolver interface {
	AppByID(ctx context.Context, id string) (state.App, error)
}

// limitKeyToProjects loads key's project limit onto it and confines a limited
// key to its projects' routes. It reports whether it has answered the request.
func (m *Middleware) limitKeyToProjects(w http.ResponseWriter, r *http.Request, acct state.Account, key *state.APIKey) bool {
	limiter, ok := m.Authn.(KeyProjectLimiter)
	if !ok || !ProjectLimitable(*key) {
		return false
	}
	ids, limited, err := limiter.APIKeyProjectLimit(r.Context(), key.ID)
	if err != nil {
		m.credentialStoreUnavailable(w, r, err)
		return true
	}
	if !limited {
		return false
	}
	key.ProjectLimited, key.ProjectIDs = true, ids
	switch {
	case IsUploadSessionPath(r.URL.Path), projectLimitedOpenRoutes[r.Pattern]:
		// The handler authorizes the app (appaccess refuses one outside
		// the key's projects) or narrows the list.
		return false
	case appRoutePattern(r.Pattern), orgAppRoutePattern(r.Pattern):
		// App slugs are unique across accounts, so an organization's app
		// route resolves the app the same way.
		return m.refuseAppOutsideProjects(w, r, key)
	case projectRoutePattern(r.Pattern) && OrganizationSlug(r) != "":
		// apid's org project mirror dispatched an organization path to the
		// personal handler; the project is the organization's.
		return m.refuseOrgProjectOutsideLimit(w, r, key)
	case projectRoutePattern(r.Pattern):
		return m.refuseProjectOutsideLimit(w, r, acct, key)
	case orgProjectRoutePattern(r.Pattern):
		return m.refuseOrgProjectOutsideLimit(w, r, key)
	case strings.HasPrefix(r.Pattern, "GET /v1/deployments/{id}"):
		return m.refuseDeploymentOutsideProjects(w, r, key)
	}
	api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Key limited to projects",
		"this key may only reach the apps and projects it is limited to; mint an unlimited key for account-wide routes"))
	return true
}

// appRoutePattern reports a route addressed to one app by its {slug}.
func appRoutePattern(pattern string) bool {
	_, path, _ := strings.Cut(pattern, " ")
	return strings.HasPrefix(path, "/v1/apps/{slug}")
}

// projectRoutePattern reports a route addressed to one project by its {slug}.
func projectRoutePattern(pattern string) bool {
	_, path, _ := strings.Cut(pattern, " ")
	return strings.HasPrefix(path, "/v1/projects/{slug}")
}

// orgAppRoutePattern reports an organization route addressed to one app.
func orgAppRoutePattern(pattern string) bool {
	_, path, _ := strings.Cut(pattern, " ")
	return strings.HasPrefix(path, "/v1/orgs/{organization}/apps/{slug}")
}

// orgProjectRoutePattern reports an organization route addressed to one
// project by its {slug}.
func orgProjectRoutePattern(pattern string) bool {
	_, path, _ := strings.Cut(pattern, " ")
	return strings.HasPrefix(path, "/v1/orgs/{organization}/projects/{slug}")
}

// refuseOrgProjectOutsideLimit answers 404, as for another organization's
// project, when the route's project is not one the key is limited to.
func (m *Middleware) refuseOrgProjectOutsideLimit(w http.ResponseWriter, r *http.Request, key *state.APIKey) bool {
	notFound := func() bool {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such project"))
		return true
	}
	resolver, ok := m.Authn.(OrgProjectResolver)
	if !ok {
		return notFound()
	}
	org, err := resolver.OrgBySlug(r.Context(), OrganizationSlug(r))
	if err == nil {
		var project state.Project
		if project, err = resolver.ProjectByOrgSlug(r.Context(), org.ID, r.PathValue("slug")); err == nil {
			if !slices.Contains(key.ProjectIDs, project.ID) {
				return notFound()
			}
			return false
		}
	}
	if errors.Is(err, state.ErrNotFound) {
		return notFound()
	}
	m.storeUnavailable(w, r, err, "could not load the project; retry shortly")
	return true
}

// refuseAppOutsideProjects answers 404, as for another account's app, when
// the route's app is outside the key's projects. It is enforced here and not
// only where handlers load the app, because some /v1/apps/{slug} handlers
// look the app up directly.
func (m *Middleware) refuseAppOutsideProjects(w http.ResponseWriter, r *http.Request, key *state.APIKey) bool {
	app, err := m.Authn.AppBySlug(r.Context(), r.PathValue("slug"))
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		m.storeUnavailable(w, r, err, "could not load the app; retry shortly")
		return true
	}
	if err != nil || AppOutsideKeyProjects(key, app) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such app"))
		return true
	}
	return false
}

// refuseDeploymentOutsideProjects answers 404 when a deployment read names a
// deployment of an app outside the key's projects.
func (m *Middleware) refuseDeploymentOutsideProjects(w http.ResponseWriter, r *http.Request, key *state.APIKey) bool {
	deployments, okDeployments := m.Authn.(DeploymentAppResolver)
	apps, okApps := m.Authn.(AppByIDResolver)
	notFound := func() bool {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such deployment"))
		return true
	}
	// A malformed id names no deployment: 404, never a store error.
	if !okDeployments || !okApps || uuid.Validate(r.PathValue("id")) != nil {
		return notFound()
	}
	appID, err := deployments.DeploymentAppID(r.Context(), r.PathValue("id"))
	if err == nil {
		var app state.App
		if app, err = apps.AppByID(r.Context(), appID); err == nil {
			if AppOutsideKeyProjects(key, app) {
				return notFound()
			}
			return false
		}
	}
	if errors.Is(err, state.ErrNotFound) {
		return notFound()
	}
	m.storeUnavailable(w, r, err, "could not load the deployment; retry shortly")
	return true
}

func (m *Middleware) refuseProjectOutsideLimit(w http.ResponseWriter, r *http.Request, acct state.Account, key *state.APIKey) bool {
	resolver, ok := m.Authn.(ProjectResolver)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such project"))
		return true
	}
	project, err := resolver.ProjectBySlug(r.Context(), acct.ID, r.PathValue("slug"))
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		m.storeUnavailable(w, r, err, "could not load the project; retry shortly")
		return true
	}
	if err != nil || !slices.Contains(key.ProjectIDs, project.ID) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such project"))
		return true
	}
	return false
}
