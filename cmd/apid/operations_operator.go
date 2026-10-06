package main

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Operator selectors never supply authority: loadApp binds the URL to the
// authenticated account, while tenant-self continues to reject tenant filters.
func accountOperationHistoryOptions(r *http.Request, app string) (api.OperationListOptions, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return api.OperationListOptions{}, state.ErrInvalidArgument
	}
	tenant := ""
	if values, ok := query["tenant_id"]; ok {
		if len(values) != 1 || values[0] == "" {
			return api.OperationListOptions{}, state.ErrInvalidArgument
		}
		tenant = values[0]
		query.Del("tenant_id")
	}
	if query.Has("app_id") {
		return api.OperationListOptions{}, state.ErrInvalidArgument
	}
	clone := r.Clone(r.Context())
	u := *r.URL
	clone.URL = &u
	clone.URL.RawQuery = query.Encode()
	opts, err := operationHistoryOptions(clone)
	opts.AppID, opts.TenantID = app, tenant
	return opts, err
}

func (s *server) listAccountOperations(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.operationStore(w)
	if !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	opts, err := accountOperationHistoryOptions(r, app.ID)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	page, err := store.ListAccountOperations(r.Context(), acct.ID, opts)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *server) getAccountOperationEvents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, store, ok := s.ownedOperation(w, r, acct)
	if !ok {
		return
	}
	if r.URL.Query().Has("limit") {
		writeOperationError(w, state.ErrInvalidArgument)
		return
	}
	after, err := operatorOperationPageNumber(r, "after", 0)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	page, err := store.OperationEvents(r.Context(), acct.ID, "", op.ID, int64(after), api.OperationEventsPageMax)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func operatorOperationPageNumber(r *http.Request, key string, fallback int) (int, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return 0, state.ErrInvalidArgument
	}
	for name, values := range query {
		if (name != "after" && name != "limit") || len(values) != 1 || values[0] == "" {
			return 0, state.ErrInvalidArgument
		}
	}
	if !query.Has(key) {
		return fallback, nil
	}
	n, err := strconv.Atoi(query.Get(key))
	if err != nil || n < 0 {
		return 0, state.ErrInvalidArgument
	}
	return n, nil
}

func (s *server) getOperationExecutions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, store, ok := s.ownedOperation(w, r, acct)
	if !ok {
		return
	}
	after, err := operatorOperationPageNumber(r, "after", 0)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	limit, err := operatorOperationPageNumber(r, "limit", api.OperationHistoryPageDefault)
	if err != nil || limit == 0 {
		writeOperationError(w, state.ErrInvalidArgument)
		return
	}
	page, err := store.OperationExecutions(r.Context(), acct.ID, op.ID, after, limit)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *server) retryOperationDelivery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, store, ok := s.ownedOperation(w, r, acct)
	if !ok {
		return
	}
	def, err := store.OperationDefinitionByID(r.Context(), acct.ID, op.DefinitionID)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	id := op.CompletionDelivery.DeliveryID
	if id == "" || def.Spec.CompletionWebhookID == "" {
		writeOperationError(w, state.ErrNotFound)
		return
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || limits.WebhookPerApp == 0 {
		api.WriteProblem(w, api.ErrPlanWebhooksNotAllowed(acct.Plan))
		return
	}
	if err := s.store.ResetAppWebhookDeliveryFromDead(r.Context(), id, def.Spec.CompletionWebhookID, acct.ID, timeNow()); err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.ErrAppWebhookInvalid("delivery is not in 'dead' state; only dead deliveries can be retried"))
			return
		}
		writeOperationError(w, err)
		return
	}
	// Only the delivery ledger changes. Refresh its projection without creating
	// an invocation or modifying the already settled business result.
	current, err := store.OperationByID(r.Context(), acct.ID, "", op.ID)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "app.webhook_delivery_retried", &acct.ID, map[string]any{"operation_id": op.ID, "webhook_id": def.Spec.CompletionWebhookID, "delivery_id": id, "app_id": op.AppID})
	writeJSON(w, http.StatusOK, current.OperationResponse)
}
