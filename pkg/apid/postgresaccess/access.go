// Package postgresaccess authorizes managed PostgreSQL against its persisted
// organization while keeping authentication attached to the human principal.
package postgresaccess

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
	state.AppOwnerReader
	OrgBySlug(context.Context, string) (state.Org, error)
	OrgByPersonalAccount(context.Context, string) (state.Org, error)
	OrgMemberByAccount(context.Context, string, string) (state.OrgMembership, error)
}
type grantKey struct{}
type Grant struct {
	Org          state.Org
	Actor, Owner state.Account
	Database     managedpostgres.Database
	Binding      managedpostgres.Binding
}

func From(ctx context.Context) (Grant, bool) { g, ok := ctx.Value(grantKey{}).(Grant); return g, ok }

func Wrap(store Store, audit authz.AuditEmitter, service func() *managedpostgres.Service, next authmw.AccountHandler) authmw.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, actor state.Account) {
		if !strings.HasPrefix(r.URL.Path, "/v1/postgres/") && (r.PathValue("organization") == "" || !strings.Contains(r.URL.Path, "/postgres/")) && r.URL.Path != "/v1/account/managed-postgres-usage" {
			next(w, r, actor)
			return
		}
		g, err := resource(r, store, service(), actor)
		if err != nil {
			api.WriteProblem(w, lookupProblem(err))
			return
		}
		_, key, _ := authmw.AccountFromContext(r)
		if g.Org.Personal || g.Org.ID == "" {
			if key != nil && (key.AppID != "" || key.OrgID != "" && key.OrgID != g.Org.ID) {
				api.WriteProblem(w, lookupProblem(state.ErrNotFound))
				return
			}
			next(w, r.WithContext(managedpostgres.WithOwnership(r.Context(), g.Org.ID, actor.ID)), actor)
			return
		}
		owner, member, fence, problem := authorize(r, store, audit, actor, g)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		g.Actor, g.Owner = actor, owner
		ctx := authmw.WithPrincipal(r.Context(), actor, key, &member)
		ctx = context.WithValue(ctx, grantKey{}, g)
		ctx = managedpostgres.WithOwnership(ctx, g.Org.ID, owner.ResourceOwnerID())
		r = r.WithContext(ctx)
		r = r.WithContext(authz.WithRequestOnContext(ctx, r))
		appaccess.ServeFenced(store, next, w, r, owner, fence)
	}
}

func resource(r *http.Request, store Store, service *managedpostgres.Service, actor state.Account) (Grant, error) {
	g := Grant{Owner: actor}
	if name := r.PathValue("organization"); name != "" {
		org, err := store.OrgBySlug(r.Context(), name)
		if err != nil {
			return g, err
		}
		if org.Personal {
			return g, state.ErrNotFound
		}
		g.Org = org
		g.Owner.ID = org.ID
	}
	if id := r.PathValue("id"); id != "" && (strings.Contains(r.URL.Path, "/databases/") || strings.Contains(r.URL.Path, "/bindings/")) && service != nil {
		var err error
		if strings.Contains(r.URL.Path, "/postgres/bindings/") {
			g.Binding, err = service.BindingOwnership(r.Context(), id)
			if err != nil {
				return g, err
			}
			g.Database, err = service.DatabaseOwnership(r.Context(), g.Binding.DatabaseID)
			g.Owner.ID = g.Binding.AccountID
		} else {
			g.Database, err = service.DatabaseOwnership(r.Context(), id)
			g.Owner.ID = g.Database.AccountID
		}
		if err != nil {
			return g, err
		}
		if g.Org.ID != "" && g.Org.ID != g.Database.OrgID {
			return g, state.ErrNotFound
		}
		if g.Database.OrgID != "" {
			g.Org, err = store.OrgByID(r.Context(), g.Database.OrgID)
			if err != nil {
				return g, err
			}
		}
		if (g.Org.Personal || g.Org.ID == "") && g.Owner.ID != actor.ID {
			return g, state.ErrNotFound
		}
	}
	if g.Org.ID == "" {
		org, err := store.OrgByPersonalAccount(r.Context(), actor.ID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return g, err
		}
		g.Org = org
	}
	return g, nil
}

func authorize(r *http.Request, store Store, audit authz.AuditEmitter, actor state.Account, g Grant) (state.Account, state.OrgMembership, state.OrgAccessFence, *api.Problem) {
	var zero state.Account
	var member state.OrgMembership
	var fence state.OrgAccessFence
	_, key, ok := authmw.AccountFromContext(r)
	if !ok {
		return zero, member, fence, api.ErrCapacity("database access requires an authenticated principal")
	}
	org := g.Org
	if org.DeletedPending || org.Status == state.OrgStatusDeletedPending || key != nil && (key.AppID != "" || key.OrgID != "" && key.OrgID != org.ID) {
		return zero, member, fence, lookupProblem(state.ErrNotFound)
	}
	member, err := store.OrgMemberByAccount(r.Context(), org.ID, actor.ID)
	if err != nil || member.RemovedAt != nil {
		if err == nil {
			err = state.ErrNotFound
		}
		return zero, member, fence, lookupProblem(err)
	}
	snapshot, err := store.OrgAccessSnapshot(r.Context(), org.ID, actor.ID)
	if err != nil {
		return zero, member, fence, lookupProblem(err)
	}
	if key != nil && key.OrgID == "" && actor.ID != g.Owner.ID {
		return zero, member, fence, api.NewProblem(http.StatusConflict, api.CodeOrgAPIKeyRequiresOrg, "Organization key required", "use a key bound to the database's organization")
	}
	member.Role = snapshot.Role
	action, supported := Action(r.Pattern)
	if !supported {
		return zero, member, fence, api.ErrOrgRoleForbidden("use a database operation without a reviewed organization permission")
	}
	// Past due loses only deploys (spec §4.7); the owner account carries it.
	if org.Status != state.OrgStatusActive && org.Status != state.OrgStatusPastDue && action != authz.OrgActionReadPostgres && action != authz.OrgActionReadPostgresUsage {
		return zero, member, fence, api.ErrOrgRoleForbidden("change a database in an inactive organization")
	}
	ctx := authmw.WithPrincipal(r.Context(), actor, key, &member)
	ctx = authz.WithRequestOnContext(ctx, r.WithContext(ctx))
	if p := authz.AuthorizeOrgAction(ctx, action, audit); p != nil {
		return zero, member, fence, p
	}
	if g.Database.ID == "" && !org.ResourceOwnershipEnabled {
		return zero, member, fence, api.NewProblem(http.StatusConflict, api.CodeConflict, "Organization billing adoption required", "complete organization billing adoption before creating or listing managed databases")
	}
	owner, err := state.AppResourceAccount(r.Context(), store, state.App{AccountID: g.Owner.ID, OrgID: org.ID})
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

// lookupProblem answers absent and foreign databases alike with the documented
// managed PostgreSQL 404 (code managed_postgres_not_found, as the handlers
// write it), so resolving access first neither enumerates nor changes the code.
func lookupProblem(err error) *api.Problem {
	if errors.Is(err, state.ErrNotFound) || errors.Is(err, managedpostgres.ErrNotFound) {
		return api.NewProblem(http.StatusNotFound, "managed_postgres_not_found", "Managed PostgreSQL resource not found", "no such managed PostgreSQL resource")
	}
	return api.ErrCapacity("could not authorize managed PostgreSQL access")
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
	return result
}
func IdempotencyKey(r *http.Request, key string) string {
	g, ok := From(r.Context())
	if !ok {
		return key
	}
	_, credential, _ := authmw.AccountFromContext(r)
	id := "session"
	if credential != nil {
		id = credential.ID
	}
	return "org " + g.Org.ID + " postgres actor " + g.Actor.ID + " credential " + id + "\n" + key
}

// CanBind prevents both personal and shared database endpoints from attaching a
// database to an app owned by a different organization in the same namespace.
func CanBind(ctx context.Context, app state.App, account state.Account) bool {
	if g, ok := From(ctx); ok {
		return app.OrgID == g.Org.ID
	}
	return app.AccountID == account.ID && (managedpostgres.Organization(ctx) == "" && app.OrgID == "" || app.OrgID == managedpostgres.Organization(ctx))
}

// PersonalContext scopes browser overviews and personal deployment helpers.
func PersonalContext(ctx context.Context, store interface {
	OrgByPersonalAccount(context.Context, string) (state.Org, error)
}, accountID string) (context.Context, error) {
	if managedpostgres.Organization(ctx) != "" {
		return ctx, nil
	}
	org, err := store.OrgByPersonalAccount(ctx, accountID)
	if errors.Is(err, state.ErrNotFound) {
		return ctx, nil
	}
	if err != nil {
		return ctx, err
	}
	return managedpostgres.WithOwnership(ctx, org.ID, accountID), nil
}

// DeleteOrganization closes the organization only after provider cleanup. The
// production trigger serializes this check with database reservations.
// beforeDelete, when set, runs after the database check and immediately before
// the organization closes; its problem keeps the organization open.
func DeleteOrganization(ctx context.Context, store interface {
	SoftDeleteOrg(context.Context, string) error
}, service *managedpostgres.Service, org state.Org, afterDelete func(context.Context) *api.Problem) *api.Problem {
	if service != nil {
		rows, err := service.List(managedpostgres.WithOwnership(ctx, org.ID, org.ID), org.ID)
		if err != nil {
			return api.ErrCapacity("could not check organization databases")
		}
		if len(rows) > 0 {
			return organizationHasDatabases()
		}
	}
	if err := store.SoftDeleteOrg(ctx, org.ID); err != nil {
		if errors.Is(err, state.ErrConflict) {
			return api.NewProblem(http.StatusConflict, api.CodeConflict, "Organization has data resources", "delete the organization's object buckets and managed databases before deleting the organization")
		}
		return api.ErrCapacity("could not delete organization")
	}
	// afterDelete (ending the subscription) runs only once the deletion
	// is recorded: run first, a refusal above left a paid plan cancelled
	// on an organization that stays in use. Soft delete is idempotent, so
	// a retried DELETE retries afterDelete.
	if afterDelete != nil {
		return afterDelete(ctx)
	}
	return nil
}
func organizationHasDatabases() *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeConflict, "Organization has managed databases", "delete the organization's managed PostgreSQL databases before deleting the organization")
}

// AuthorizeManifestBindings applies database permission and credential scopes
// before a shared source upload resolves dependencies or creates project rows.
func AuthorizeManifestBindings(ctx context.Context, audit authz.AuditEmitter) *api.Problem {
	if problem := authz.AuthorizeOrgAction(ctx, authz.OrgActionBindPostgres, audit); problem != nil {
		return problem
	}
	request := (&http.Request{}).WithContext(ctx)
	if !authmw.HasScope(request, api.ScopesManagedPostgresManageSurface...) {
		return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "PostgreSQL scope required", "binding organization databases from a manifest requires postgres:manage or admin")
	}
	return nil
}
