package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func decodeRecoveryList(r *http.Request) (api.EventRecoveryListQuery, error) {
	v := r.URL.Query()
	q := api.EventRecoveryListQuery{State: v.Get("state"), Mode: v.Get("mode"), SubscriptionID: v.Get("subscription_id"), Cursor: v.Get("cursor")}
	for key, entries := range v {
		if len(entries) != 1 || entries[0] == "" {
			return q, state.ErrEventRecoveryQuery
		}
		switch key {
		case "state", "mode", "subscription_id", "cursor", "limit", "created_after", "created_before":
		default:
			return q, state.ErrEventRecoveryQuery
		}
	}
	if v.Has("limit") {
		n, err := strconv.Atoi(v.Get("limit"))
		if err != nil || n < 1 {
			return q, state.ErrEventRecoveryQuery
		}
		q.Limit = n
	}
	for key, target := range map[string]**time.Time{"created_after": &q.CreatedAfter, "created_before": &q.CreatedBefore} {
		if v.Has(key) {
			t, err := time.Parse(time.RFC3339Nano, v.Get(key))
			if err != nil {
				return q, state.ErrEventRecoveryQuery
			}
			t = t.UTC()
			*target = &t
		}
	}
	if err := q.Validate(); err != nil {
		return q, state.ErrEventRecoveryQuery
	}
	return q, nil
}
func (s *server) listEventRecoveries(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, r, cancel, ok := s.eventRecoveryStore(w, r)
	if !ok {
		return
	}
	defer cancel()
	query, err := decodeRecoveryList(r)
	if err != nil {
		s.writeEventRecovery(w, r, 0, nil, err)
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	out, err := store.ListEventRecoveries(r.Context(), acct.ID, app.ID, query)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
