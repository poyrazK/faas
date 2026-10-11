package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type eventReplayBackfillItemsHandlerStore struct {
	state.Store
	state.EventReplayBackfillStore
	query    api.EventReplayBackfillItemsQuery
	called   bool
	response api.EventReplayBackfillItemsResponse
	err      error
}

func (s *eventReplayBackfillItemsHandlerStore) ListEventReplayBackfillItems(_ context.Context, _ string, _ string, query api.EventReplayBackfillItemsQuery) (api.EventReplayBackfillItemsResponse, error) {
	s.called = true
	s.query = query
	return s.response, s.err
}

func TestEventReplayBackfillItemsAPIReadOnlyMetadataAndValidation(t *testing.T) {
	e := setup(t, api.PlanPro)
	jobID := uuid.NewString()
	acceptedAt := time.Now().UTC().Truncate(time.Microsecond)
	store := &eventReplayBackfillItemsHandlerStore{Store: e.store, response: api.EventReplayBackfillItemsResponse{
		JobID:     jobID,
		Items:     []api.EventReplayBackfillItem{{EventSource: "orders.eu", EventID: "event-1", EventType: "invoice.paid", AcceptedAt: acceptedAt, State: "failed", Attempts: 2, FailureCode: "enqueue_failed", LastError: "bounded diagnostic", DetailsTruncated: true, Retryable: true, UpdatedAt: acceptedAt}},
		NextAfter: "erbi1.cursor",
	}}
	e.s.store = store
	path := "/v1/event-replays/" + jobID + "/items?state=failed&after=erbi1.cursor&limit=7"
	rec := e.do(t, http.MethodGet, path, nil, nil)
	var response api.EventReplayBackfillItemsResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil || rec.Header().Get("Cache-Control") != "no-store" || !store.called {
		t.Fatalf("response=%d %s", rec.Code, rec.Body)
	}
	if store.query.State != "failed" || store.query.After != "erbi1.cursor" || store.query.Limit != 7 || len(response.Items) != 1 || response.Items[0].EventID != "event-1" || !response.Items[0].DetailsTruncated {
		t.Fatalf("query=%+v response=%+v", store.query, response)
	}
	for _, bad := range []string{
		"/v1/event-replays/" + jobID + "/items?state=unknown",
		"/v1/event-replays/" + jobID + "/items?limit=1&limit=2",
		"/v1/event-replays/" + jobID + "/items?unknown=x",
		"/v1/event-replays/" + jobID + "/items?after=",
	} {
		store.called = false
		invalid := e.do(t, http.MethodGet, bad, nil, nil)
		if invalid.Code != http.StatusBadRequest || store.called {
			t.Fatalf("invalid query=%s status=%d body=%s called=%t", bad, invalid.Code, invalid.Body, store.called)
		}
	}
}

func TestEventReplayBackfillItemsAPIReadScope(t *testing.T) {
	e := setupWithScopes(t, []string{api.ScopeEventsPublish})
	store := &eventReplayBackfillItemsHandlerStore{Store: e.store}
	e.s.store = store
	rec := e.do(t, http.MethodGet, "/v1/event-replays/"+uuid.NewString()+"/items", nil, nil)
	if rec.Code != http.StatusForbidden || store.called {
		t.Fatalf("status=%d body=%s called=%t", rec.Code, rec.Body, store.called)
	}
}
