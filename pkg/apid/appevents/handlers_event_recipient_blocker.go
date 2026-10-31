package appevents

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) GetEventRecipientBlocker(w http.ResponseWriter, r *http.Request, acct state.Account) {
	query, err := parseEventRecipientBlocker(r)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	store, ok := s.Host.Store().(state.EventRecipientBlockerStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event recipient blocker store"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.EventRecipientBlockerReadTimeout)
	defer cancel()
	out, err := s.eventRecipientBlocker(ctx, store, acct, nil, query)
	if err != nil {
		s.Host.Logger().ErrorContext(ctx, "read event recipient blocker", "err", err)
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "event_recipient_blocker_read_timeout", "Recipient blocker read timed out", "Retry the exact-recipient lookup."))
		} else {
			api.WriteProblem(w, api.ErrCapacity("failed to read recipient blocker"))
		}
		return
	}
	if out.Recipient != nil {
		page := eventBacklogResponse(state.EventBacklog{Recipients: []state.EventBacklogEntry{{EventBacklogRecipient: *out.Recipient}}}, state.EventBacklogQuery{}, acct.ID)
		out.Recipient = &page.Recipients[0]
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, out)
}

func parseEventRecipientBlocker(r *http.Request) (api.EventRecipientBlockerQuery, error) {
	values := r.URL.Query()
	guard, err := api.ParseAppEventAcceptanceGuard(values.Get("expected_accepted_at"))
	if err != nil {
		return api.EventRecipientBlockerQuery{}, err
	}
	query := api.EventRecipientBlockerQuery{Source: values.Get("source"), ID: values.Get("id"), SubscriptionID: values.Get("subscription_id")}
	if guard.ExpectedAcceptedAt != nil {
		query.ExpectedAcceptedAt = *guard.ExpectedAcceptedAt
	}
	return query, query.Validate()
}

// A batch is read-only, with independent snapshots and an ordered result per query.
func (s *Service) GetEventRecipientBlockers(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var request api.EventRecipientBlockerBatchRequest
	if err := httpkit.DecodeJSONSized(r, &request, api.EventRecipientBlockerBatchBodyMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if err := request.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	store, ok := s.Host.Store().(state.EventRecipientBlockerStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("event recipient blocker store"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.EventRecipientBlockerReadTimeout)
	defer cancel()
	access, err := s.eventAccess(ctx, acct)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("event company access"))
		return
	}
	out := api.EventRecipientBlockerBatchResponse{Items: make([]api.EventRecipientBlockerBatchItem, len(request.Queries))}
	for i, query := range request.Queries {
		out.Items[i] = s.readEventRecipientBlockerBatchItem(ctx, store, acct, access, query)
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, out)
}
func (s *Service) readEventRecipientBlockerBatchItem(ctx context.Context, store state.EventRecipientBlockerStore, acct state.Account, access *eventAccess, query api.EventRecipientBlockerQuery) api.EventRecipientBlockerBatchItem {
	accountID := acct.ID
	item := api.EventRecipientBlockerBatchItem{Query: query}
	if ctx.Err() != nil {
		item.Error = "read_timeout"
		return item
	}
	result, err := s.eventRecipientBlocker(ctx, store, acct, access, query)
	if err != nil {
		item.Error = "read_error"
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			item.Error = "read_timeout"
		}
		s.Host.Logger().ErrorContext(ctx, "read event recipient blocker batch item", "err", err)
		return item
	}
	if result.Recipient != nil {
		page := eventBacklogResponse(state.EventBacklog{Recipients: []state.EventBacklogEntry{{EventBacklogRecipient: *result.Recipient}}}, state.EventBacklogQuery{}, accountID)
		result.Recipient = &page.Recipients[0]
	}
	item.Result = &result
	return item
}

// eventRecipientBlocker answers for an event or recipient the caller may not
// see (ADR-20261025211616) exactly as for a receipt that is not retained.
func (s *Service) eventRecipientBlocker(ctx context.Context, store state.EventRecipientBlockerStore, acct state.Account, access *eventAccess, query api.EventRecipientBlockerQuery) (api.EventRecipientBlockerResponse, error) {
	var err error
	if access == nil {
		if access, err = s.eventAccess(ctx, acct); err != nil {
			return api.EventRecipientBlockerResponse{}, err
		}
	}
	_, err = access.event(acct.ID, query.Source, query.ID, query.SubscriptionID)
	if errors.Is(err, state.ErrNotFound) {
		return api.EventRecipientBlockerResponse{Query: query, ObservedAt: time.Now().UTC(), Status: "unavailable", Reason: "receipt_unavailable"}, nil
	}
	if err != nil {
		return api.EventRecipientBlockerResponse{}, err
	}
	return store.EventRecipientBlocker(ctx, acct.ID, query)
}
