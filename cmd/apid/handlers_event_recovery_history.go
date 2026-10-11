package main

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func recoveryActorRequest(r *http.Request) *http.Request {
	account, key, ok := authmw.AccountFromContext(r)
	if !ok {
		return r
	}
	kind, id := "account", account.ID
	if key != nil && key.ID != "" {
		kind, id = "api_key", key.ID
	}
	return r.WithContext(state.WithEventRecoveryActor(r.Context(), kind, id))
}
func recoveryControlRequest(r *http.Request) (*http.Request, error) {
	var req api.EventRecoveryControlRequest
	if r.Body != nil {
		if err := decodeJSONSized(r, &req, 4<<10); err != nil && !errors.Is(err, io.EOF) {
			return r, errors.Join(state.ErrEventRecoveryQuery, err)
		}
	}
	ctx, err := state.WithEventRecoveryReason(r.Context(), req.Reason)
	return r.WithContext(ctx), err
}
func (s *server) listEventRecoveryHistory(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, r, cancel, ok := s.eventRecoveryStore(w, r)
	if !ok {
		return
	}
	defer cancel()
	v := r.URL.Query()
	after := int64(0)
	limit := api.EventRecoveryHistoryPageMax
	for name, entries := range v {
		if len(entries) != 1 || entries[0] == "" || name != "after" && name != "limit" {
			s.writeEventRecovery(w, r, 0, nil, state.ErrEventRecoveryQuery)
			return
		}
	}
	if v.Has("after") {
		n, err := strconv.ParseInt(v.Get("after"), 10, 64)
		if err != nil || n < 0 {
			s.writeEventRecovery(w, r, 0, nil, state.ErrEventRecoveryQuery)
			return
		}
		after = n
	}
	if v.Has("limit") {
		n, err := strconv.Atoi(v.Get("limit"))
		if err != nil || n < 1 || n > api.EventRecoveryHistoryPageMax {
			s.writeEventRecovery(w, r, 0, nil, state.ErrEventRecoveryQuery)
			return
		}
		limit = n
	}
	out, err := store.ListEventRecoveryHistory(r.Context(), acct.ID, r.PathValue("jobID"), after, limit)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
