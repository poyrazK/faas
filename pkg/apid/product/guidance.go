package product

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

// CustomerGuidance shares the portal projection without reading work history,
// observing onboarding access, or returning financial/binding details.
func (s *Service) CustomerGuidance(w http.ResponseWriter, r *http.Request, acct state.Account) {
	identity, ok := authmw.ProductCustomerFromContext(r.Context())
	if !ok {
		respond(w, nil, state.ErrProductAccess)
		return
	}
	if _, err := ownerQuery(r); err != nil {
		respond(w, nil, err)
		return
	}
	store, app, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	view, err := customerViewWithHistory(r.Context(), store, acct.ID, app.ID, identity.TenantID, productcontrol.Period(time.Now()), &identity.Access, false)
	if err == nil && view.Guidance == nil {
		err = state.ErrConflict
	}
	respond(w, view.Guidance, err)
}

func populateCustomerGuidance(ctx context.Context, store state.ProductControlStore, view *api.ProductPortal, permissions api.ProductPermissions, usage *api.ProductUsage, observed time.Time) error {
	head, err := store.GetProductCatalog(ctx, view.AccountID, view.AppID, "")
	if err != nil {
		return err
	}
	if id, ok := authmw.ProductCustomerFromContext(ctx); ok {
		if head.Spec.AuthApp != id.AuthApp || view.Customer.AuthOrganizationID != id.OrganizationID {
			return state.ErrProductAccess
		}
		if head.Revision != id.Access.PolicyRevision {
			return state.ErrConflict
		}
	}
	guidance, err := productcontrol.UsageGuidance(view.AccountID, view.AppID, view.Customer, view.Catalog, head, permissions, usage, view.Bindings, observed)
	if err != nil {
		return err
	}
	current, err := store.GetProductCustomer(ctx, view.AccountID, view.AppID, view.Customer.TenantID)
	if err != nil {
		return err
	}
	latest, err := store.GetProductCatalog(ctx, view.AccountID, view.AppID, "")
	if err != nil {
		return err
	}
	if current != view.Customer || latest.Revision != head.Revision || productcontrol.Period(time.Now()) != guidance.Period {
		return state.ErrConflict
	}
	view.Guidance = &guidance
	return nil
}
