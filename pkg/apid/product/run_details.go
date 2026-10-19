package product

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) runDetailsStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ProductRunDetailsStore, state.ProductRunDetailsScope, bool) {
	id, ok := authmw.ProductCustomerFromContext(r.Context())
	if !ok || !id.Access.Permissions.ViewWork {
		customerError(w, state.ErrProductAccess)
		return nil, state.ProductRunDetailsScope{}, false
	}
	_, app, ok := s.owned(w, r, acct)
	if !ok {
		return nil, state.ProductRunDetailsScope{}, false
	}
	store, ok := s.Host.Store().(state.ProductRunDetailsStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Run details are unavailable"))
		return nil, state.ProductRunDetailsScope{}, false
	}
	return store, state.ProductRunDetailsScope{ProductPlanScope: state.ProductPlanScope{AccountID: acct.ID, AppID: app.ID, TenantID: id.TenantID, AuthApp: id.AuthApp, AuthAppID: id.AuthAppID, OrganizationID: id.OrganizationID, Role: id.Access.Role}}, true
}

func (s *Service) CustomerRunDetails(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	store, scope, ok := s.runDetailsStore(w, r, acct)
	if !ok {
		return
	}
	if _, err := ownerQuery(r); err != nil {
		respond(w, nil, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ProductControlRunDetailsMaxDuration)
	defer cancel()
	d, err := store.GetProductRunDetails(ctx, scope, r.PathValue("operation"))
	if err == nil {
		err = runDetailsScoped(r, scope, d)
	}
	if err == nil {
		err = ctx.Err()
	}
	runDetailsRespond(w, d, err)
}

func runDetailsScoped(r *http.Request, scope state.ProductRunDetailsScope, d api.ProductRunDetails) error {
	id, ok := authmw.ProductCustomerFromContext(r.Context())
	if !ok || d.AccountID != scope.AccountID || d.AppID != scope.AppID || d.TenantID != scope.TenantID || d.OperationID != r.PathValue("operation") || productcontrol.ValidateRunDetails(d, id.Access.Permissions) != nil {
		return state.ErrConflict
	}
	return nil
}

func runDetailsRespond(w http.ResponseWriter, d api.ProductRunDetails, err error) {
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		respond(w, nil, err)
		return
	}
	raw, err := json.Marshal(d)
	if err == nil && len(raw)+1 > api.ProductControlRunDetailsMaxResponseBytes {
		err = state.NewOperationLimitError("product_run_details_response_bytes", api.ProductControlRunDetailsMaxResponseBytes, int64(len(raw)+1))
	}
	if err != nil {
		respond(w, nil, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(raw, '\n'))
}
