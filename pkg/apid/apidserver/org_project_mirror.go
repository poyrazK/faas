package apidserver

import (
	"net/http"
	"strings"

	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
)

// orgProjectMirrorPattern catches /v1/orgs/{organization}/projects/{slug}/...
// paths that no organization route registers explicitly. ServeMux prefers
// every more specific registration, so the explicit organization routes keep
// their own handlers.
const orgProjectMirrorPattern = "/v1/orgs/{organization}/projects/{slug}/"

// orgProjectMirror serves a company project's operations that exist only as
// /v1/projects/{slug}/... routes: secrets, release-set activation and
// rollback, staging, verification runs, flags, release secrets and
// deployment readiness (apps/projects review 2026-11-10, P1). Those routes
// resolved {slug} in the caller's own namespace, so no member could reach a
// project an adopted company owns. The mirror dispatches the request to the
// matching /v1/projects/{slug}/... route with the organization recorded on the
// context; projectaccess then resolves the project in that organization and
// authorizes the member's role for that route (projectaccess.routeActions).
// Routes without a reviewed organization permission stay refused there.
//
// next is the handler chain below the request-wide layers, so capability
// gates, hidden routes and the API contract apply to the dispatched route as
// they do to the personal path.
func orgProjectMirror(next func() http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		organization, slug := r.PathValue("organization"), r.PathValue("slug")
		rest, ok := orgProjectRest(r.URL.Path, organization, slug)
		if !ok {
			http.NotFound(w, r)
			return
		}
		dispatched := r.Clone(authmw.WithOrganizationProjectPath(r.Context(), organization))
		dispatched.URL.Path = "/v1/projects/" + slug + rest
		dispatched.URL.RawPath = ""
		dispatched.RequestURI = dispatched.URL.RequestURI()
		next().ServeHTTP(w, dispatched)
	})
}

// orgProjectRest returns the part of path after
// /v1/orgs/{organization}/projects/{slug}, which starts with "/".
func orgProjectRest(path, organization, slug string) (string, bool) {
	prefix := "/v1/orgs/" + organization + "/projects/" + slug
	rest, ok := strings.CutPrefix(path, prefix)
	if !ok || !strings.HasPrefix(rest, "/") || organization == "" || slug == "" {
		return "", false
	}
	return rest, true
}
