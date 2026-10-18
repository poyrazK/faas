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

func (s *Service) workHistoryStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ProductWorkHistoryStore, state.ProductWorkHistoryScope, bool) {
	id, ok := authmw.ProductCustomerFromContext(r.Context())
	if !ok || !id.Access.Permissions.ViewWork {
		customerError(w, state.ErrProductAccess)
		return nil, state.ProductWorkHistoryScope{}, false
	}
	_, app, ok := s.owned(w, r, acct)
	if !ok {
		return nil, state.ProductWorkHistoryScope{}, false
	}
	store, ok := s.Host.Store().(state.ProductWorkHistoryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Work history is unavailable"))
		return nil, state.ProductWorkHistoryScope{}, false
	}
	return store, state.ProductWorkHistoryScope{ProductPlanScope: state.ProductPlanScope{AccountID: acct.ID, AppID: app.ID, TenantID: id.TenantID, AuthApp: id.AuthApp, AuthAppID: id.AuthAppID, OrganizationID: id.OrganizationID, Role: id.Access.Role}}, true
}

func (s *Service) CustomerWorkHistory(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	store, scope, ok := s.workHistoryStore(w, r, acct)
	if !ok {
		return
	}
	q, err := ownerQuery(r, "operation_id", "feature", "state", "from", "through", "cursor")
	if err != nil {
		respond(w, nil, err)
		return
	}
	f, err := productcontrol.NormalizeWorkHistoryFilter(api.ProductWorkHistoryFilter{OperationID: q.Get("operation_id"), Feature: q.Get("feature"), State: q.Get("state"), From: q.Get("from"), Through: q.Get("through")})
	if err != nil {
		respond(w, nil, state.ErrInvalidArgument)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ProductControlWorkHistoryMaxDuration)
	defer cancel()
	page, err := store.ListProductWorkHistory(ctx, scope, f, q.Get("cursor"))
	if err == nil {
		err = workHistoryScoped(r, scope, f, q.Get("cursor"), page)
	}
	if err == nil {
		err = ctx.Err()
	}
	workHistoryRespond(w, page, err)
}

func workHistoryScoped(r *http.Request, s state.ProductWorkHistoryScope, f api.ProductWorkHistoryFilter, cursor string, p api.ProductWorkHistoryPage) error {
	id, ok := authmw.ProductCustomerFromContext(r.Context())
	if !ok || p.AccountID != s.AccountID || p.AppID != s.AppID || p.TenantID != s.TenantID || p.Filter != f {
		return state.ErrConflict
	}
	scope := productcontrol.WorkHistoryScope(s.AccountID, s.AppID, s.TenantID, s.AuthAppID, s.OrganizationID, s.Role, id.Access.Permissions, f)
	if productcontrol.ValidateWorkHistoryPage(p, id.Access.Permissions, scope, cursor) != nil {
		return state.ErrConflict
	}
	return nil
}

func workHistoryRespond(w http.ResponseWriter, p api.ProductWorkHistoryPage, err error) {
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		respond(w, nil, err)
		return
	}
	raw, err := json.Marshal(p)
	if err == nil && len(raw)+1 > api.ProductControlWorkHistoryMaxResponseBytes {
		err = state.NewOperationLimitError("product_work_history_response_bytes", api.ProductControlWorkHistoryMaxResponseBytes, int64(len(raw)+1))
	}
	if err != nil {
		respond(w, nil, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(raw, '\n'))
}
