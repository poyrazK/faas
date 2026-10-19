package product

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) helpStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ProductHelpStore, state.App, bool) {
	_, app, ok := s.owned(w, r, acct)
	if !ok {
		return nil, app, false
	}
	store, ok := s.Host.Store().(state.ProductHelpStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Customer Help is unavailable"))
		return nil, app, false
	}
	return store, app, true
}

func (s *Service) customerHelpStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ProductHelpStore, state.ProductHelpScope, bool) {
	id, ok := authmw.ProductCustomerFromContext(r.Context())
	if !ok {
		helpRespond(w, nil, state.ErrProductAccess)
		return nil, state.ProductHelpScope{}, false
	}
	store, app, ok := s.helpStore(w, r, acct)
	return store, state.ProductHelpScope{ProductPlanScope: state.ProductPlanScope{AccountID: acct.ID, AppID: app.ID, TenantID: id.TenantID, AuthApp: id.AuthApp, AuthAppID: id.AuthAppID, OrganizationID: id.OrganizationID, Role: id.Access.Role}}, ok
}

func helpReadContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), api.ProductControlHelpMaxDuration)
}

func (s *Service) CustomerHelpCreate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.customerHelpStore(w, r, acct)
	if !ok {
		return
	}
	if _, err := ownerQuery(r); err != nil {
		helpRespond(w, nil, err)
		return
	}
	var req api.ProductHelpCreate
	if !operation.DecodeOperationBody(w, r, &req, api.ProductControlHelpBodyMaxBytes) {
		return
	}
	ctx, cancel := helpReadContext(r)
	defer cancel()
	value, err := store.CreateProductHelp(ctx, scope, req)
	helpRespond(w, value, helpScoped(r, value, acct.ID, scope.AppID, scope.TenantID, err))
}

func (s *Service) CustomerHelpGet(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.customerHelpStore(w, r, acct)
	if !ok {
		return
	}
	if _, err := ownerQuery(r); err != nil {
		helpRespond(w, nil, err)
		return
	}
	ctx, cancel := helpReadContext(r)
	defer cancel()
	value, err := store.GetProductHelp(ctx, scope, r.PathValue("request"))
	helpRespond(w, value, helpScoped(r, value, acct.ID, scope.AppID, scope.TenantID, err))
}

func (s *Service) CustomerHelpList(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.customerHelpStore(w, r, acct)
	if !ok {
		return
	}
	q, err := ownerQuery(r, "status", "cursor")
	if err != nil {
		helpRespond(w, nil, err)
		return
	}
	ctx, cancel := helpReadContext(r)
	defer cancel()
	value, err := store.ListProductHelp(ctx, scope, helpStatusFilter(q.Get("status"), "all"), q.Get("cursor"))
	helpRespond(w, value, helpScoped(r, value, acct.ID, scope.AppID, scope.TenantID, err))
}

func (s *Service) HelpList(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.helpStore(w, r, acct)
	if !ok {
		return
	}
	q, err := ownerQuery(r, "tenant", "status", "cursor")
	if err != nil {
		helpRespond(w, nil, err)
		return
	}
	ctx, cancel := helpReadContext(r)
	defer cancel()
	value, err := store.ListProductOwnerHelp(ctx, acct.ID, app.ID, q.Get("tenant"), helpStatusFilter(q.Get("status"), "active"), q.Get("cursor"))
	helpRespond(w, value, helpScoped(r, value, acct.ID, app.ID, q.Get("tenant"), err))
}

func (s *Service) HelpGet(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.helpStore(w, r, acct)
	if !ok {
		return
	}
	if _, err := ownerQuery(r); err != nil {
		helpRespond(w, nil, err)
		return
	}
	ctx, cancel := helpReadContext(r)
	defer cancel()
	value, err := store.GetProductOwnerHelp(ctx, acct.ID, app.ID, r.PathValue("request"))
	helpRespond(w, value, helpScoped(r, value, acct.ID, app.ID, "", err))
}

func (s *Service) HelpUpdate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.helpStore(w, r, acct)
	if !ok {
		return
	}
	if _, err := ownerQuery(r); err != nil {
		helpRespond(w, nil, err)
		return
	}
	var update api.ProductHelpUpdate
	if !operation.DecodeOperationBody(w, r, &update, api.ProductControlHelpBodyMaxBytes) {
		return
	}
	ctx, cancel := helpReadContext(r)
	defer cancel()
	value, err := store.UpdateProductHelp(ctx, acct.ID, app.ID, r.PathValue("request"), update)
	helpRespond(w, value, helpScoped(r, value, acct.ID, app.ID, "", err))
}

func helpStatusFilter(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func helpScoped(r *http.Request, value any, account, app, tenant string, err error) error {
	if err != nil {
		return err
	}
	var values []api.ProductHelpRequest
	switch v := value.(type) {
	case api.ProductHelpRequest:
		values = []api.ProductHelpRequest{v}
	case api.ProductHelpPage:
		if len(v.Requests) > api.ProductControlHelpPageSize || v.Requests == nil || v.ObservedAt.IsZero() {
			return state.ErrConflict
		}
		values = v.Requests
	}
	for _, v := range values {
		if v.AccountID != account || v.AppID != app || tenant != "" && v.TenantID != tenant || productcontrol.ValidateHelpCreate(v.Request) != nil || !productcontrol.HelpStatus(v.Status) || v.Version < 1 || v.Version > api.ProductControlHelpMaxUpdates || v.Context.ObservedAt.IsZero() || v.CreatedAt.IsZero() || v.UpdatedAt.Before(v.CreatedAt) {
			return state.ErrConflict
		}
		if id, customer := authmw.ProductCustomerFromContext(r.Context()); customer && !productcontrol.HelpVisible(v.Request.Kind, id.Access.Permissions) {
			return state.ErrConflict
		}
	}
	return r.Context().Err()
}

func helpRespond(w http.ResponseWriter, value any, err error) {
	if err == nil {
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			err = marshalErr
		} else if len(raw)+1 > api.ProductControlHelpResponseMaxBytes {
			err = state.NewOperationLimitError("product_help_response_bytes", api.ProductControlHelpResponseMaxBytes, int64(len(raw)+1))
		}
	}
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Help request unavailable", "this request is unavailable for your current workspace and permissions"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("use a valid help request, bounded message, current version and supported status"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "product_help_conflict", "Help request changed", "reload the current request; a saved request ID must keep its original details"))
	default:
		respond(w, value, err)
	}
}
