// Package projectaccess authorizes project operations against persisted
// organization ownership while preserving human authentication and resource IDs.
package projectaccess

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type Store interface {
	state.OrgAccessStore
	state.ProjectOwnershipStore
	state.AppOwnerReader
	ProjectBySlug(context.Context, string, string) (state.Project, error)
	OrgBySlug(context.Context, string) (state.Org, error)
	OrgMemberByAccount(context.Context, string, string) (state.OrgMembership, error)
}

type grantKey struct{}
type Grant struct {
	Project      state.Project
	Org          state.Org
	Actor, Owner state.Account
}

func From(ctx context.Context) (Grant, bool) {
	grant, ok := ctx.Value(grantKey{}).(Grant)
	return grant, ok
}

func Wrap(store Store, audit authz.AuditEmitter, next authmw.AccountHandler) authmw.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, actor state.Account) {
		projectRequest := strings.HasPrefix(r.URL.Path, "/v1/projects") || strings.HasPrefix(r.URL.Path, "/dashboard/projects/") ||
			r.PathValue("organization") != "" && strings.Contains(r.URL.Path, "/projects")
		if !projectRequest {
			next(w, r, actor)
			return
		}
		if store == nil {
			api.WriteProblem(w, api.ErrCapacity("project store unavailable"))
			return
		}
		project, org, addressed, err := resource(r, store, actor)
		if err != nil {
			api.WriteProblem(w, lookupProblem(err))
			return
		}
		if !addressed {
			if problem := personalCollection(r, store, actor); problem != nil {
				api.WriteProblem(w, problem)
				return
			}
			next(w, r, actor)
			return
		}
		if org.Personal {
			_, key, _ := authmw.AccountFromContext(r)
			if key != nil && (key.AppID != "" || key.OrgID != "" && key.OrgID != org.ID) {
				api.WriteProblem(w, lookupProblem(state.ErrNotFound))
				return
			}
			next(w, r, actor)
			return
		}
		owner, member, fence, problem := authorize(r, store, audit, actor, project, org)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		_, key, _ := authmw.AccountFromContext(r)
		ctx := authmw.WithPrincipal(r.Context(), actor, key, &member)
		ctx = context.WithValue(ctx, grantKey{}, Grant{Project: project, Org: org, Actor: actor, Owner: owner})
		ctx = managedpostgres.WithOwnership(ctx, org.ID, owner.ResourceOwnerID())
		r = r.WithContext(ctx)
		r = r.WithContext(authz.WithRequestOnContext(ctx, r))
		appaccess.ServeFenced(store, next, w, r, owner, fence)
	}
}

func personalCollection(r *http.Request, store Store, actor state.Account) *api.Problem {
	if r.Method != http.MethodPost && r.PathValue("slug") == "" {
		return nil
	}
	_, key, _ := authmw.AccountFromContext(r)
	if key == nil {
		return nil
	}
	if key.AppID != "" {
		return lookupProblem(state.ErrNotFound)
	}
	if key.OrgID == "" {
		return nil
	}
	org, err := store.OrgByID(r.Context(), key.OrgID)
	if err != nil {
		return lookupProblem(err)
	}
	if !org.Personal || org.PersonalOwnerAccountID == nil || *org.PersonalOwnerAccountID != actor.ID {
		if r.PathValue("slug") != "" {
			return lookupProblem(state.ErrNotFound)
		}
		return api.NewProblem(http.StatusConflict, api.CodeOrgAPIKeyRequiresOrg, "Organization project path required", "use the organization's project scan and deployment URLs")
	}
	return nil
}

func resource(r *http.Request, store Store, actor state.Account) (state.Project, state.Org, bool, error) {
	if organization := r.PathValue("organization"); organization != "" {
		org, err := store.OrgBySlug(r.Context(), organization)
		if err != nil {
			return state.Project{}, org, true, err
		}
		if org.Personal {
			return state.Project{}, org, true, state.ErrNotFound
		}
		if slug := r.PathValue("slug"); slug != "" {
			p, err := store.ProjectByOrgSlug(r.Context(), org.ID, slug)
			return p, org, true, err
		}
		namespace := org.ID
		if !org.ResourceOwnershipEnabled {
			namespace = actor.ID
		}
		return state.Project{AccountID: namespace, OrgID: org.ID}, org, true, nil
	}
	if r.PathValue("slug") == "" {
		return state.Project{}, state.Org{}, false, nil
	}
	p, err := store.ProjectBySlug(r.Context(), actor.ID, r.PathValue("slug"))
	if errors.Is(err, state.ErrNotFound) && preCreationRoutes[r.Pattern] {
		// The project does not exist yet; the route reads only the actor's namespace.
		return state.Project{}, state.Org{}, false, nil
	}
	if err != nil {
		return p, state.Org{}, true, err
	}
	if p.OrgID == "" {
		return p, state.Org{}, false, nil
	}
	org, err := store.OrgByID(r.Context(), p.OrgID)
	return p, org, true, err
}

func authorize(r *http.Request, store Store, audit authz.AuditEmitter, actor state.Account, project state.Project, org state.Org) (state.Account, state.OrgMembership, state.OrgAccessFence, *api.Problem) {
	var zero state.Account
	var member state.OrgMembership
	var fence state.OrgAccessFence
	_, key, ok := authmw.AccountFromContext(r)
	if !ok {
		return zero, member, fence, api.ErrCapacity("project access requires an authenticated principal")
	}
	if org.DeletedPending || org.Status == state.OrgStatusDeletedPending || key != nil && (key.AppID != "" || key.OrgID != "" && key.OrgID != org.ID) {
		return zero, member, fence, lookupProblem(state.ErrNotFound)
	}
	member, err := store.OrgMemberByAccount(r.Context(), org.ID, actor.ID)
	if err != nil {
		return zero, member, fence, lookupProblem(err)
	}
	snapshot, err := store.OrgAccessSnapshot(r.Context(), org.ID, actor.ID)
	if err != nil {
		return zero, member, fence, lookupProblem(err)
	}
	if member.RemovedAt != nil {
		return zero, member, fence, lookupProblem(state.ErrNotFound)
	}
	if key != nil && key.OrgID == "" && actor.ID != project.AccountID {
		return zero, member, fence, api.NewProblem(http.StatusConflict, api.CodeOrgAPIKeyRequiresOrg, "Organization key required", "use an API key bound to the project's organization")
	}
	member.Role = snapshot.Role
	ctx := authmw.WithPrincipal(r.Context(), actor, key, &member)
	ctx = authz.WithRequestOnContext(ctx, r.WithContext(ctx))
	action, supported := Action(r.Pattern)
	if !supported {
		return zero, member, fence, api.ErrOrgRoleForbidden("use a project operation without a reviewed organization permission")
	}
	// Past due loses only deploys (spec §4.7); the owner account carries it.
	if org.Status != state.OrgStatusActive && org.Status != state.OrgStatusPastDue && action != authz.OrgActionReadProject && action != authz.OrgActionReadQueues {
		return zero, member, fence, api.ErrOrgRoleForbidden("change a project in an inactive organization")
	}
	if p := authz.AuthorizeOrgAction(ctx, action, audit); p != nil {
		return zero, member, fence, p
	}
	if action == authz.OrgActionConfigureQueues && !org.ResourceOwnershipEnabled {
		return zero, member, fence, api.NewProblem(http.StatusConflict, api.CodeConflict, "Organization billing adoption required", "complete organization billing adoption before changing company queues")
	}
	owner, err := state.ProjectResourceAccount(r.Context(), store, project)
	if err != nil {
		return zero, member, fence, lookupProblem(err)
	}
	if !owner.Active() && r.Method != http.MethodGet && r.Method != http.MethodHead {
		return zero, member, fence, api.ErrAccountSuspended()
	}
	fence = state.OrgAccessFence{OrgID: org.ID, AccountID: actor.ID, Revision: snapshot.Revision}
	if key != nil {
		fence.CredentialID, fence.CredentialOrgID = key.ID, key.OrgID
	}
	return owner, member, fence, nil
}

func lookupProblem(err error) *api.Problem {
	if errors.Is(err, state.ErrNotFound) {
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such project resource")
	}
	return api.ErrCapacity("could not authorize project access")
}

func AuditData(ctx context.Context, data map[string]any) map[string]any {
	g, ok := From(ctx)
	if !ok {
		return data
	}
	result := maps.Clone(data)
	if result == nil {
		result = make(map[string]any)
	}
	result["org_id"], result["actor_account_id"] = g.Org.ID, g.Actor.ID
	if g.Project.ID != "" {
		result["project_id"] = g.Project.ID
	}
	return result
}

func IdempotencyKey(r *http.Request, key string) string {
	if g, ok := From(r.Context()); ok {
		_, credential, _ := authmw.AccountFromContext(r)
		id := "session"
		if credential != nil {
			id = credential.ID
		}
		return "org " + g.Org.ID + " project " + g.Project.ID + " actor " + g.Actor.ID + " credential " + id + "\n" + key
	}
	if r.Pattern == "POST /v1/projects" {
		return "personal project v2\n" + key
	}
	return key
}
