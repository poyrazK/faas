package main

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func decodeNotificationRetryBacklog(r *http.Request) (api.EventRecoveryNotificationRetryBacklogQuery, error) {
	var out api.EventRecoveryNotificationRetryBacklogQuery
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return out, state.ErrEventRecoveryQuery
	}
	for key, value := range values {
		if len(value) != 1 || value[0] == "" {
			return out, state.ErrEventRecoveryQuery
		}
		switch key {
		case "status":
			out.Status = value[0]
		case "cursor":
			out.Cursor = value[0]
		case "page_size":
			size, err := strconv.Atoi(value[0])
			if err != nil || size < 1 {
				return out, state.ErrEventRecoveryQuery
			}
			out.PageSize = size
		default:
			return out, state.ErrEventRecoveryQuery
		}
	}
	if err := out.Normalize(); err != nil {
		return out, state.ErrEventRecoveryQuery
	}
	return out, nil
}
func (s *server) listEventRecoveryNotificationRetryBacklog(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, r, cancel, ok := s.eventRecoveryStore(w, r)
	if !ok {
		return
	}
	defer cancel()
	query, err := decodeNotificationRetryBacklog(r)
	if err != nil {
		s.writeEventRecovery(w, r, 0, nil, err)
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.EventRecoveryNotificationRetryBacklogStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("notification retry backlog"))
		return
	}
	out, err := store.ListEventRecoveryNotificationRetryBacklog(r.Context(), acct.ID, app.ID, query, time.Now().UTC())
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
