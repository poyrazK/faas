// Package objectaccess authorizes organization storage accounting collections.
// Individual bucket operations use their persisted application ownership grant.
package objectaccess

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

type Store interface {
	state.OrgAccessStore
	state.AppOwnerReader
	OrgBySlug(context.Context, string) (state.Org, error)
	OrgByPersonalAccount(context.Context, string) (state.Org, error)
	OrgMemberByAccount(context.Context, string, string) (state.OrgMembership, error)
}

func Wrap(store Store, audit authz.AuditEmitter, next authmw.AccountHandler) authmw.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, actor state.Account) {
		if r.Pattern != "GET /v1/orgs/{organization}/object-storage-usage" {
			if r.URL.Path == "/v1/account/object-storage-usage" && !personalCredential(r, store, actor) {
				api.WriteProblem(w, problem(state.ErrNotFound))
				return
			}
			next(w, r, actor)
			return
		}
		org, err := store.OrgBySlug(r.Context(), r.PathValue("organization"))
		if err != nil || org.Personal || org.DeletedPending {
			if err == nil {
				err = state.ErrNotFound
			}
			api.WriteProblem(w, problem(err))
			return
		}
		serve(store, audit, next, w, r, actor, org)
	}
}

func personalCredential(r *http.Request, store Store, actor state.Account) bool {
	_, key, _ := authmw.AccountFromContext(r)
	if key == nil {
		return true
	}
	if key.AppID != "" {
		return false
	}
	if key.OrgID == "" {
		return true
	}
	org, err := store.OrgByPersonalAccount(r.Context(), actor.ID)
	return err == nil && org.ID == key.OrgID
}

func serve(store Store, audit authz.AuditEmitter, next authmw.AccountHandler, w http.ResponseWriter, r *http.Request, actor state.Account, org state.Org) {
	_, key, _ := authmw.AccountFromContext(r)
	if key != nil && (key.AppID != "" || key.OrgID != org.ID) {
		api.WriteProblem(w, problem(state.ErrNotFound))
		return
	}
	member, err := store.OrgMemberByAccount(r.Context(), org.ID, actor.ID)
	if err != nil || member.RemovedAt != nil {
		api.WriteProblem(w, problem(state.ErrNotFound))
		return
	}
	snapshot, err := store.OrgAccessSnapshot(r.Context(), org.ID, actor.ID)
	if err != nil {
		api.WriteProblem(w, problem(err))
		return
	}
	member.Role = snapshot.Role
	ctx := authmw.WithPrincipal(r.Context(), actor, key, &member)
	ctx = authz.WithRequestOnContext(ctx, r.WithContext(ctx))
	if p := authz.AuthorizeOrgAction(ctx, authz.ReadObjectStorageUsage, audit); p != nil {
		api.WriteProblem(w, p)
		return
	}
	if !org.ResourceOwnershipEnabled {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Organization billing adoption required", "complete billing adoption before reading aggregate company storage usage"))
		return
	}
	owner, err := state.AppResourceAccount(ctx, store, state.App{AccountID: org.ID, OrgID: org.ID})
	if err != nil {
		api.WriteProblem(w, problem(err))
		return
	}
	fence := state.OrgAccessFence{OrgID: org.ID, AccountID: actor.ID, Revision: snapshot.Revision}
	if key != nil {
		fence.CredentialID, fence.CredentialOrgID = key.ID, key.OrgID
	}
	appaccess.ServeFenced(store, next, w, r.WithContext(ctx), owner, fence)
}

func problem(err error) *api.Problem {
	if errors.Is(err, state.ErrNotFound) {
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such organization storage resource")
	}
	return api.ErrCapacity("could not authorize organization storage usage")
}
