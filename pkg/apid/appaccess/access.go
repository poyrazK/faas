package appaccess

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Store is only the resource and membership lookup surface. There is no
// membership cache: role changes and removal apply to the next request.
type Store interface {
	state.OrgAccessStore
	AppBySlug(context.Context, string) (state.App, error)
	AppBySlugIncludingDeleted(context.Context, string) (state.App, error)
	AppByID(context.Context, string) (state.App, error)
	DeploymentByID(context.Context, string) (state.Deployment, error)
	BuildByID(context.Context, string) (state.Build, error)
	DomainByName(context.Context, string) (state.CustomDomain, error)
	CronByID(context.Context, string) (state.Cron, error)
	GetEdgeRuleByID(context.Context, string) (state.EdgeRule, error)
	InvocationByID(context.Context, string) (state.Invocation, error)
	TriggerByID(context.Context, string) (sqlc.Trigger, error)
	GetWorkflowRun(context.Context, string) (*state.WorkflowRun, error)
	OrgByID(context.Context, string) (state.Org, error)
	OrgMemberByAccount(context.Context, string, string) (state.OrgMembership, error)
	AccountByID(context.Context, string) (state.Account, error)
}

type grantKey struct{}

// Grant separates the actor from the legacy storage/quota subject. It grants
// access to exactly one addressed app, never to the resource account itself.
type Grant struct {
	App   state.App
	Actor state.Account
	Owner state.Account
	Fence state.OrgAccessFence
}

func From(r *http.Request) (Grant, bool) {
	return FromContext(r.Context())
}

func FromContext(ctx context.Context) (Grant, bool) {
	g, ok := ctx.Value(grantKey{}).(Grant)
	return g, ok
}

// IdempotencyKey keeps receipts and activity deduplication in the same
// caller/credential namespace when storage still belongs to another account.
func IdempotencyKey(r *http.Request, key string) string {
	g, ok := From(r)
	if !ok || g.Actor.ID == g.Owner.ID {
		// Existing creator receipts must survive the authorization rollout.
		// New collaborators cannot reach this legacy account namespace.
		return key
	}
	_, credential, _ := authmw.AccountFromContext(r)
	credentialID := "session"
	if credential != nil {
		credentialID = credential.ID
	}
	return "actor " + g.Actor.ID + " credential " + credentialID + "\n" + key
}

// AuditData preserves the real actor while existing event subjects continue
// to identify the account whose resource was changed.
func AuditData(ctx context.Context, data map[string]any) map[string]any {
	g, ok := FromContext(ctx)
	if !ok {
		return data
	}
	result := maps.Clone(data)
	if result == nil {
		result = make(map[string]any)
	}
	result["actor_account_id"] = g.Actor.ID
	result["org_id"] = g.App.OrgID
	return result
}

// Wrap runs outside route-specific scope, verification and idempotency
// handlers. Those handlers continue to inspect the original principal.
func Wrap(store Store, audit authz.AuditEmitter, next authmw.AccountHandler) authmw.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, actor state.Account) {
		if principal, _, ok := authmw.AccountFromContext(r); ok {
			actor = principal
		}
		app, addressed, err := addressedApp(r, store, actor.ID)
		if !addressed {
			next(w, r, actor)
			return
		}
		if err != nil {
			writeLookupError(w, r, err)
			return
		}
		owner, problem := authorize(r, store, audit, actor, app)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		serveAuthorized(store, next, w, r, owner)
	}
}

func addressedApp(r *http.Request, store Store, actorID string) (state.App, bool, error) {
	// No app lookup is needed on account, authentication or organization
	// management endpoints, including degraded-server health tests.
	if r.PathValue("organization") != "" && strings.Contains(r.Pattern, "/apps/{slug}") {
		app, err := store.AppBySlug(r.Context(), r.PathValue("slug"))
		if err != nil {
			return state.App{}, true, err
		}
		org, err := store.OrgByID(r.Context(), app.OrgID)
		if err != nil || org.Personal || org.Slug != r.PathValue("organization") {
			return state.App{}, true, state.ErrNotFound
		}
		return app, true, nil
	}
	if !strings.HasPrefix(r.URL.Path, "/v1/apps/") && !strings.HasPrefix(r.URL.Path, "/v1/builds/") && !strings.HasPrefix(r.URL.Path, "/v1/deployments/") {
		return indirectlyAddressedApp(r, store, actorID)
	}
	if store == nil {
		return state.App{}, true, errors.New("application store unavailable")
	}
	if strings.HasPrefix(r.URL.Path, "/v1/apps/") && r.PathValue("slug") != "" {
		if r.Pattern == "POST /v1/apps/{slug}/restore" {
			app, err := store.AppBySlugIncludingDeleted(r.Context(), r.PathValue("slug"))
			return app, true, err
		}
		app, err := store.AppBySlug(r.Context(), r.PathValue("slug"))
		if errors.Is(err, state.ErrNotFound) && r.Pattern == "POST /v1/apps/{slug}/diff" {
			// Diffing an unused slug previews creating the app; nothing exists
			// to authorize, and the handler scopes the lookup to the caller.
			return state.App{}, false, nil
		}
		return app, true, err
	}
	if r.PathValue("id") == "" {
		return state.App{}, false, nil
	}
	var deploymentID string
	switch {
	case strings.HasPrefix(r.URL.Path, "/v1/builds/"):
		build, err := store.BuildByID(r.Context(), r.PathValue("id"))
		if err != nil {
			return state.App{}, true, err
		}
		deploymentID = build.DeploymentID
	case strings.HasPrefix(r.URL.Path, "/v1/deployments/"):
		deploymentID = r.PathValue("id")
	default:
		return state.App{}, false, nil
	}
	dep, err := store.DeploymentByID(r.Context(), deploymentID)
	if err != nil {
		return state.App{}, true, err
	}
	app, err := store.AppByID(r.Context(), dep.AppID)
	return app, true, err
}

// Authorize applies Wrap's decision to an app the URL does not address, for a
// handler that resolves it from the request body or a stored session (the
// resumable upload routes). The route's action comes from routeActions; it
// returns the account that owns the app's resources.
func Authorize(r *http.Request, store Store, audit authz.AuditEmitter, actor state.Account, app state.App) (state.Account, *api.Problem) {
	return authorize(r, store, audit, actor, app)
}

// AuthorizeRoute is Authorize for a request that finishes another route's
// work on a different surface: the browser secret-entry page completes a
// secret request, so it gets the decision that request's route pattern gets.
func AuthorizeRoute(r *http.Request, store Store, audit authz.AuditEmitter, actor state.Account, app state.App, pattern string) (state.Account, *api.Problem) {
	served := r.Pattern
	r.Pattern = pattern
	defer func() { r.Pattern = served }()
	return authorize(r, store, audit, actor, app)
}

func authorize(r *http.Request, store Store, audit authz.AuditEmitter, actor state.Account, app state.App) (state.Account, *api.Problem) {
	_, key, _, ok := authmw.PrincipalFrom(r)
	if !ok {
		return state.Account{}, api.ErrCapacity("application authorization requires an authenticated principal")
	}
	if key != nil && (key.AppID != "" && key.AppID != app.ID || key.OrgID != "" && app.OrgID != "" && key.OrgID != app.OrgID) {
		return state.Account{}, notFound(r)
	}
	if app.OrgID == "" {
		// Unattributed legacy apps never become shared by guessing a tenant.
		if app.AccountID != actor.ID || key != nil && key.OrgID != "" && !personalKey(r.Context(), store, key.OrgID, actor.ID) {
			return state.Account{}, notFound(r)
		}
		return actor, nil
	}
	org, err := store.OrgByID(r.Context(), app.OrgID)
	if err != nil {
		return state.Account{}, lookupProblem(r, err)
	}
	if org.Personal {
		if app.AccountID != actor.ID || org.PersonalOwnerAccountID == nil || *org.PersonalOwnerAccountID != actor.ID {
			return state.Account{}, notFound(r)
		}
		return actor, nil
	}
	return authorizeShared(r, store, audit, actor, key, app, org)
}

func authorizeShared(r *http.Request, store Store, audit authz.AuditEmitter, actor state.Account, key *state.APIKey, app state.App, org state.Org) (state.Account, *api.Problem) {
	mem, err := store.OrgMemberByAccount(r.Context(), org.ID, actor.ID)
	if err != nil {
		return state.Account{}, lookupProblem(r, err)
	}
	if mem.RemovedAt != nil || mem.OrgID != org.ID || mem.AccountID != actor.ID || org.DeletedPending || org.Status == state.OrgStatusDeletedPending {
		return state.Account{}, notFound(r)
	}
	snapshot, err := store.OrgAccessSnapshot(r.Context(), org.ID, actor.ID)
	if err != nil {
		return state.Account{}, lookupProblem(r, err)
	}
	mem.Role = snapshot.Role
	// Account keys retain their existing creator-owned boundary. Sharing an
	// app across creators requires a session or a key explicitly bound to its org.
	if key != nil && key.OrgID == "" && app.AccountID != actor.ID {
		return state.Account{}, api.NewProblem(http.StatusConflict, api.CodeOrgAPIKeyRequiresOrg,
			"Organization key required", "mint an API key bound to the application's organization")
	}
	ctx := authmw.WithPrincipal(r.Context(), actor, key, &mem)
	*r = *r.WithContext(ctx)
	*r = *r.WithContext(authz.WithRequestOnContext(r.Context(), r))
	action, supported := routeActions[r.Pattern]
	if !supported && dashboardRead(r) {
		action, supported = authz.OrgActionReadApp, true
	}
	if authz.QueuePattern(r.Pattern) {
		action, supported = authz.QueueAction(r.Pattern)
		if !supported {
			return state.Account{}, api.ErrOrgRoleForbidden("use a queue operation without a reviewed organization permission")
		}
		if !org.ResourceOwnershipEnabled && r.Method != http.MethodGet {
			return state.Account{}, api.NewProblem(http.StatusConflict, api.CodeConflict, "Organization billing adoption required", "complete organization billing adoption before changing company queues")
		}
	}
	if authz.ObjectStoragePattern(r.Pattern) {
		action, supported = authz.ObjectStorageAction(r.Pattern)
		if !supported {
			return state.Account{}, api.ErrOrgRoleForbidden("use a storage operation without a reviewed organization permission")
		}
		if !org.ResourceOwnershipEnabled && r.Method != http.MethodGet {
			return state.Account{}, api.NewProblem(http.StatusConflict, api.CodeConflict, "Organization billing adoption required", "complete organization billing adoption before changing company object storage")
		}
	}
	if !supported {
		// Retain pre-existing creator access only for current owners/admins.
		// An unreviewed endpoint cannot switch to a different resource account.
		if app.AccountID != actor.ID {
			return state.Account{}, api.ErrOrgRoleForbidden("use an application endpoint without a reviewed organization permission")
		}
		action = authz.OrgActionManageAppSecurity
	}
	// A past-due company keeps running and loses only deploys (spec §4.7), as
	// a past-due account does: the owner account below carries past_due, so
	// deploy admission refuses while members still revoke and reconfigure.
	if org.Status == state.OrgStatusSuspended && action != authz.OrgActionReadApp && action != authz.OrgActionReadAppLogs && action != authz.ReadObjectStorage && action != authz.OrgActionReadQueues {
		return state.Account{}, api.ErrOrgRoleForbidden("change applications in an inactive organization")
	}
	if p := authz.AuthorizeOrgAction(r.Context(), action, audit); p != nil {
		return state.Account{}, p
	}
	owner, err := state.AppResourceAccount(r.Context(), store, app)
	if err != nil {
		return state.Account{}, lookupProblem(r, err)
	}
	if !owner.Active() && !authmw.InactiveAccountMayReach(owner, r.Method, r.URL.Path) {
		return state.Account{}, api.ErrAccountSuspended()
	}
	fence := state.OrgAccessFence{OrgID: org.ID, AccountID: actor.ID, Revision: snapshot.Revision}
	if key != nil {
		fence.CredentialID, fence.CredentialOrgID, fence.AppID = key.ID, key.OrgID, key.AppID
	}
	*r = *r.WithContext(managedpostgres.WithOwnership(context.WithValue(r.Context(), grantKey{}, Grant{App: app, Actor: actor, Owner: owner, Fence: fence}), org.ID, owner.ResourceOwnerID()))
	return owner, nil
}

func personalKey(ctx context.Context, store Store, orgID, actorID string) bool {
	org, err := store.OrgByID(ctx, orgID)
	return err == nil && org.Personal && org.PersonalOwnerAccountID != nil && *org.PersonalOwnerAccountID == actorID
}

// notFound answers absent and foreign resources alike. Each route family
// keeps the 404 its handlers document, so authorization neither enumerates
// resources nor changes a published error (build_not_found, "no such cron").
func notFound(r *http.Request) *api.Problem {
	path := r.URL.Path
	what := "no such application resource"
	switch {
	case r.Pattern == "GET /v1/builds/{id}":
		return api.ErrBuildNotFound()
	case strings.HasPrefix(path, "/v1/builds/"):
		what = "no such build" // provenance and SBOM
	case strings.HasPrefix(path, "/v1/crons/") && r.PathValue("run_id") != "":
		what = "no such cron run"
	case strings.HasPrefix(path, "/v1/crons/"):
		what = "no such cron"
	case strings.HasPrefix(path, "/v1/deployments/"):
		what = "no such deployment"
	case strings.HasPrefix(path, "/v1/domains/"):
		what = "no such domain"
	case strings.HasPrefix(path, "/v1/workflows/runs/"):
		return api.ErrWorkflowRunNotFound()
	case strings.HasPrefix(path, "/v1/delayed-tasks/"), strings.HasPrefix(path, "/v1/invocations/"):
		return api.ErrInvocationNotFound(r.PathValue("id"))
	case strings.HasPrefix(path, "/v1/edge-rules/"):
		return api.ErrEdgeRuleNotFound(r.PathValue("id"))
	case strings.HasPrefix(path, "/v1/triggers/"):
		what = "no such trigger"
	case strings.HasPrefix(path, "/v1/deleted-apps/"):
		what = "no such deleted app"
	}
	return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", what)
}

func lookupProblem(r *http.Request, err error) *api.Problem {
	// A path ID that is no UUID names no resource either (Hunt 14 H14-65).
	if errors.Is(err, state.ErrNotFound) || state.IsMalformedID(err) {
		return notFound(r)
	}
	return api.ErrCapacity("could not authorize application access")
}

func writeLookupError(w http.ResponseWriter, r *http.Request, err error) {
	api.WriteProblem(w, lookupProblem(r, err))
}
