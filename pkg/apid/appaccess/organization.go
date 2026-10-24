package appaccess

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// WrapOrganization runs after LoadOrg. It refreshes the role atomically with
// its revision before the handler checks an organization action.
func WrapOrganization(store Store, next authmw.AccountHandler) authmw.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, actor state.Account) {
		mem, ok := authz.MembershipFrom(r)
		if !ok {
			next(w, r, actor)
			return
		}
		org, err := store.OrgByID(r.Context(), mem.OrgID)
		if err != nil {
			writeLookupError(w, r, err)
			return
		}
		if _, key, _ := authmw.AccountFromContext(r); key != nil && !keyMayActOnOrg(r, store, key, org, actor.ID) {
			api.WriteProblem(w, api.ErrOrgRoleForbidden("use this API key outside the application or organization it is bound to"))
			return
		}
		if org.Personal {
			next(w, r, actor)
			return
		}
		snapshot, err := store.OrgAccessSnapshot(r.Context(), org.ID, actor.ID)
		if err != nil {
			api.WriteProblem(w, api.ErrOrgRoleForbidden("use an organization after membership changed"))
			return
		}
		member := *mem
		member.Role = snapshot.Role
		_, key, _ := authmw.AccountFromContext(r)
		ctx := authmw.WithPrincipal(r.Context(), actor, key, &member)
		r = r.WithContext(ctx)
		ctx = authz.WithRequestOnContext(ctx, r)
		r = r.WithContext(ctx)
		fence := state.OrgAccessFence{OrgID: org.ID, AccountID: actor.ID, Revision: snapshot.Revision}
		if key != nil {
			fence.CredentialID, fence.CredentialOrgID, fence.AppID = key.ID, key.OrgID, key.AppID
		}
		ServeFenced(store, next, w, r, actor, fence)
	}
}

// keyMayActOnOrg confines a bound API key to the organization routes it was
// minted for, as authorize does for app routes. An app-bound key never
// administers an organization. A key bound to a company organization acts
// only on that organization. Account keys (no organization) and keys bound to
// the holder's personal organization, which the CLI mints at login, keep
// acting on the organizations their holder belongs to; the route's scope
// and the holder's role still decide what they may do there.
func keyMayActOnOrg(r *http.Request, store Store, key *state.APIKey, org state.Org, actorID string) bool {
	switch {
	case key.AppID != "":
		return false
	case key.OrgID == "" || key.OrgID == org.ID:
		return true
	default:
		return personalKey(r.Context(), store, key.OrgID, actorID)
	}
}
