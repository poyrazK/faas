// Package orgbilling selects an authorized organization financial subject while
// preserving the authenticated human principal and its revocation fence.
package orgbilling

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

func Scope(store state.AppOwnerReader, audit authz.AuditEmitter, action authz.OrgAction, next authmw.AccountHandler) authmw.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, actor state.Account) {
		if problem := authz.AuthorizeOrgAction(r.Context(), action, audit); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		_, _, membership, ok := authmw.PrincipalFrom(r)
		if !ok || membership == nil {
			api.WriteProblem(w, api.ErrOrgNotFound(r.PathValue("slug")))
			return
		}
		org, err := store.OrgByID(r.Context(), membership.OrgID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrCapacity("could not resolve organization billing"))
			return
		}
		if err != nil || org.Personal {
			api.WriteProblem(w, api.ErrOrgNotFound(r.PathValue("slug")))
			return
		}
		target, err := state.BillingResourceAccount(r.Context(), store, org.ID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not resolve organization billing"))
			return
		}
		next(w, r, target)
	}
}

// BillingContact snapshots the current authorized billing contact for first
// checkout. It is organization billing data and does not authorize app access.
func BillingContact(ctx context.Context, store any, target, actor state.Account) (state.Account, error) {
	if target.Email != "" {
		return target, nil
	}
	writer, ok := store.(interface {
		UpdateOrgBillingEmail(context.Context, string, string) error
	})
	if !ok {
		return state.Account{}, state.ErrInvalidArgument
	}
	if err := writer.UpdateOrgBillingEmail(ctx, target.ResourceOrgID, actor.Email); err != nil {
		return state.Account{}, err
	}
	target.Email = actor.Email
	return target, nil
}
