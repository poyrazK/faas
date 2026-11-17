package appaccess

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// Customer-granted support access (ADR-20261116163508). apid verifies the
// operator session and the grant named by the Support-Grant header, then
// marks the request with WithSupport. authorize answers such a request from
// the grant instead of an organization membership, and only for the routes
// below: deploying the app's connected repository, retrying, cancelling,
// rolling back and restarting, plus the reads those need. Secrets,
// configuration, members, keys, billing, deletion, runtime logs and data are
// never reachable this way. A route joins this list only after review.
var supportRoutes = map[string]bool{
	"GET /v1/apps/{slug}":                          true,
	"GET /v1/apps/{slug}/deployments":              true,
	"GET /v1/apps/{slug}/deployments/latest":       true,
	"GET /v1/apps/{slug}/deployments/{id}/summary": true,
	"GET /v1/deployments/{id}":                     true,
	"GET /v1/deployments/{id}/logs":                true,
	"GET /v1/deployments/{id}/stages":              true,
	"GET /v1/deployments/{id}/timeline":            true,
	"GET /v1/builds/{id}":                          true,

	"POST /v1/apps/{slug}/deployments/source-ref":  true,
	"POST /v1/deployments/{id}/retry":              true,
	"POST /v1/apps/{slug}/deployments/{id}/cancel": true,
	"POST /v1/apps/{slug}/rollback":                true,
	"POST /v1/apps/{slug}/restart":                 true,
}

// SupportRoute reports whether a support grant may reach the route pattern.
func SupportRoute(pattern string) bool { return supportRoutes[pattern] }

// SupportRoutes lists the reviewed routes, for tests and documentation.
func SupportRoutes() []string {
	out := make([]string, 0, len(supportRoutes))
	for pattern := range supportRoutes {
		out = append(out, pattern)
	}
	return out
}

// Support is a grant apid verified for this request's operator.
type Support struct {
	Grant      state.SupportGrant
	OperatorID string
}

type supportKey struct{}

// WithSupport marks ctx as acting under a verified support grant.
func WithSupport(ctx context.Context, support Support) context.Context {
	return context.WithValue(ctx, supportKey{}, support)
}

// SupportFrom returns the support grant the request acts under.
func SupportFrom(ctx context.Context) (Support, bool) {
	support, ok := ctx.Value(supportKey{}).(Support)
	return support, ok
}

// supportRole is the role a support request carries into handlers that
// re-check the organization role. Developer is the least role that deploys;
// the route list above, not the role, bounds what support can do.
const supportRole = state.OrgRoleDeveloper

func authorizeSupport(r *http.Request, store Store, actor state.Account, key *state.APIKey, app state.App, support Support) (state.Account, *api.Problem) {
	if key != nil || actor.ID != support.OperatorID {
		return state.Account{}, api.NewProblem(http.StatusForbidden, api.CodeForbidden,
			"Operator session required", "support access is used from a signed-in operator session, never an API key")
	}
	if !SupportRoute(r.Pattern) {
		return state.Account{}, api.NewProblem(http.StatusForbidden, api.CodeForbidden,
			"Not covered by support access", "a support grant covers deploying, retrying, cancelling, rolling back and restarting only")
	}
	grant := support.Grant
	if app.OrgID == "" || app.OrgID != grant.OrgID || !grant.Covers(app.ID) {
		return state.Account{}, notFound(r)
	}
	org, err := store.OrgByID(r.Context(), app.OrgID)
	if err != nil {
		return state.Account{}, lookupProblem(r, err)
	}
	if org.DeletedPending || org.Status == state.OrgStatusDeletedPending {
		return state.Account{}, notFound(r)
	}
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	if org.Status == state.OrgStatusSuspended && !read {
		return state.Account{}, api.ErrOrgRoleForbidden("change applications in an inactive organization")
	}
	owner, err := state.AppResourceAccount(r.Context(), store, app)
	if err != nil {
		return state.Account{}, lookupProblem(r, err)
	}
	if !owner.Active() && !authmw.InactiveAccountMayReach(owner, r.Method, r.URL.Path) {
		return state.Account{}, api.ErrAccountSuspended()
	}
	mem := state.OrgMembership{OrgID: org.ID, AccountID: actor.ID, Role: supportRole}
	ctx := authmw.WithPrincipal(r.Context(), actor, nil, &mem)
	*r = *r.WithContext(ctx)
	fence := state.OrgAccessFence{OrgID: org.ID, AccountID: actor.ID, SupportGrantID: grant.ID}
	*r = *r.WithContext(context.WithValue(r.Context(), grantKey{}, Grant{App: app, Actor: actor, Owner: owner, Fence: fence}))
	return owner, nil
}
