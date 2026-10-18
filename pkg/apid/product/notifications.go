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

func (s *Service) notificationStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ProductNotificationStore, state.ProductNotificationScope, bool) {
	id, ok := authmw.ProductCustomerFromContext(r.Context())
	if !ok {
		customerError(w, state.ErrProductAccess)
		return nil, state.ProductNotificationScope{}, false
	}
	_, app, ok := s.owned(w, r, acct)
	if !ok {
		return nil, state.ProductNotificationScope{}, false
	}
	store, ok := s.Host.Store().(state.ProductNotificationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Notifications are unavailable"))
		return nil, state.ProductNotificationScope{}, false
	}
	return store, state.ProductNotificationScope{ProductPlanScope: state.ProductPlanScope{AccountID: acct.ID, AppID: app.ID, TenantID: id.TenantID, AuthApp: id.AuthApp, AuthAppID: id.AuthAppID, OrganizationID: id.OrganizationID, Role: id.Access.Role}}, true
}

func (s *Service) CustomerNotifications(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.notificationStore(w, r, acct)
	if !ok {
		return
	}
	query, err := ownerQuery(r, "filter", "cursor")
	if err != nil {
		notificationRespond(w, nil, err)
		return
	}
	filter := query.Get("filter")
	if filter == "" {
		filter = "all"
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ProductControlNotificationMaxDuration)
	defer cancel()
	page, err := store.ListProductNotifications(ctx, scope, filter, query.Get("cursor"))
	if err == nil && (page.AccountID != scope.AccountID || page.AppID != scope.AppID || page.TenantID != scope.TenantID || page.Items == nil || len(page.Items) > api.ProductControlNotificationPageSize || page.UnreadCount < 0 || page.ReadStateScope != "workspace" || page.ObservedAt.IsZero() || page.WindowStart.IsZero()) {
		err = state.ErrConflict
	}
	if err == nil {
		for _, item := range page.Items {
			if !notificationVisible(r, item) || !item.OccurredAt.After(page.WindowStart) {
				err = state.ErrConflict
				break
			}
		}
	}
	notificationRespond(w, page, err)
}

func (s *Service) CustomerNotificationRead(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.notificationStore(w, r, acct)
	if !ok {
		return
	}
	if _, err := ownerQuery(r); err != nil {
		notificationRespond(w, nil, err)
		return
	}
	var req api.ProductNotificationUpdate
	if !operation.DecodeOperationBody(w, r, &req, api.ProductControlNotificationBodyMaxBytes) {
		return
	}
	if productcontrol.ValidateNotificationUpdate(req) != nil {
		notificationRespond(w, nil, state.ErrInvalidArgument)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ProductControlNotificationMaxDuration)
	defer cancel()
	value, err := store.UpdateProductNotification(ctx, scope, req)
	if err == nil && (value.ID != req.ID || value.Revision != req.Revision || value.Read != *req.Read || !notificationVisible(r, value)) {
		err = state.ErrConflict
	}
	notificationRespond(w, value, err)
}

func notificationVisible(r *http.Request, n api.ProductNotification) bool {
	id, ok := authmw.ProductCustomerFromContext(r.Context())
	kind, reference, valid := productcontrol.NotificationReference(n.ID)
	if !ok || !valid || reference != n.ReferenceID || !accessRevision(n.Revision) || n.OccurredAt.IsZero() || n.ReadVersion < 0 || n.ReadVersion > api.ProductControlNotificationMaxUpdates {
		return false
	}
	if kind == "work" {
		if !id.Access.Permissions.ViewWork || n.HelpKind != "" {
			return false
		}
		return n.Kind == "work_succeeded" || n.Kind == "work_failed" || n.Kind == "work_cancelled" || n.Kind == "work_attention"
	}
	return n.Feature == "" && productcontrol.HelpVisible(n.HelpKind, id.Access.Permissions) && (n.Kind == "help_replied" || n.Kind == "help_resolved")
}

func notificationRespond(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Cache-Control", "no-store")
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "product_notification_conflict", "Update changed", "Refresh notifications before changing read state."))
		return
	}
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Notification unavailable", "this update is unavailable for your current workspace and permissions"))
		return
	}
	if errors.Is(err, state.ErrInvalidArgument) {
		api.WriteProblem(w, api.ErrValidation("use the current notification reference, revision, read state and version"))
		return
	}
	if err != nil {
		respond(w, nil, err)
		return
	}
	raw, err := json.Marshal(value)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if len(raw)+1 > api.ProductControlNotificationResponseMaxBytes {
		respond(w, nil, state.NewOperationLimitError("product_notification_response_bytes", api.ProductControlNotificationResponseMaxBytes, int64(len(raw)+1)))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(raw, '\n'))
}
