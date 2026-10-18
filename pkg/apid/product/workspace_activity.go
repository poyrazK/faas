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

func (s *Service) workspaceActivityStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ProductWorkspaceActivityStore, state.ProductWorkspaceActivityScope, bool) {
	id, ok := authmw.ProductCustomerFromContext(r.Context())
	if !ok {
		customerError(w, state.ErrProductAccess)
		return nil, state.ProductWorkspaceActivityScope{}, false
	}
	_, app, ok := s.owned(w, r, acct)
	if !ok {
		return nil, state.ProductWorkspaceActivityScope{}, false
	}
	store, ok := s.Host.Store().(state.ProductWorkspaceActivityStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Workspace activity is unavailable"))
		return nil, state.ProductWorkspaceActivityScope{}, false
	}
	scope := state.ProductWorkspaceActivityScope{ProductPlanScope: state.ProductPlanScope{AccountID: acct.ID, AppID: app.ID, TenantID: id.TenantID, AuthApp: id.AuthApp, AuthAppID: id.AuthAppID, OrganizationID: id.OrganizationID, Role: id.Access.Role}}
	return store, scope, true
}

func (s *Service) CustomerWorkspaceActivity(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.workspaceActivityStore(w, r, acct)
	if !ok {
		return
	}
	q, err := ownerQuery(r, "filter", "cursor")
	if err != nil {
		respond(w, nil, err)
		return
	}
	filter := q.Get("filter")
	if filter == "" {
		filter = "all"
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ProductControlWorkspaceActivityMaxDuration)
	defer cancel()
	page, err := store.ListProductWorkspaceActivity(ctx, scope, filter, q.Get("cursor"))
	if err == nil {
		err = workspaceActivityScoped(r, scope, filter, q.Get("cursor"), page)
	}
	if err == nil {
		err = ctx.Err()
	}
	workspaceActivityRespond(w, page, err)
}

func workspaceActivityScoped(r *http.Request, s state.ProductWorkspaceActivityScope, filter, cursor string, p api.ProductWorkspaceActivityPage) error {
	id, ok := authmw.ProductCustomerFromContext(r.Context())
	if !ok || p.AccountID != s.AccountID || p.AppID != s.AppID || p.TenantID != s.TenantID || p.Filter != filter {
		return state.ErrConflict
	}
	scope := productcontrol.WorkspaceActivityScope(s.AccountID, s.AppID, s.TenantID, s.AuthAppID, s.OrganizationID, s.Role, filter, id.Access.Permissions)
	if productcontrol.ValidateWorkspaceActivityPage(p, id.Access.Permissions, scope, cursor) != nil {
		return state.ErrConflict
	}
	return nil
}

func workspaceActivityRespond(w http.ResponseWriter, value api.ProductWorkspaceActivityPage, err error) {
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		respond(w, nil, err)
		return
	}
	raw, err := json.Marshal(value)
	if err == nil && len(raw)+1 > api.ProductControlWorkspaceActivityMaxResponseBytes {
		err = state.NewOperationLimitError("product_workspace_activity_response_bytes", api.ProductControlWorkspaceActivityMaxResponseBytes, int64(len(raw)+1))
	}
	if err != nil {
		respond(w, nil, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(raw, '\n'))
}
