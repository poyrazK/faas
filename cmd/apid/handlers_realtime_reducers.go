package main

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) managedRealtimeReducer(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.realtimeHistoryPreviewEnabled {
		s.notFound(w, "channel reducers preview unavailable")
		return
	}
	ep, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	ch := r.PathValue("channel")
	if problem := validateManagedRealtimeChannel(ch); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	store, ok := s.store.(state.ManagedRealtimeReducerStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("channel reducers unavailable"))
		return
	}
	var row state.ManagedRealtimeReducerState
	var err error
	switch r.Method {
	case http.MethodPut:
		var req api.ManagedRealtimeReducerRequest
		if decodeJSONSized(r, &req, 70<<10) != nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid("reducer requires sequence and bounded object entities"))
			return
		}
		row, err = store.PutManagedRealtimeReducer(r.Context(), state.ManagedRealtimeReducerState{EndpointID: ep.ID, Channel: ch, Sequence: req.Sequence, Entities: req.Entities})
	case http.MethodDelete:
		err = store.DeleteManagedRealtimeReducer(r.Context(), ep.ID, ch)
		if err == nil {
			s.audit.Emit(r.Context(), "realtime.reducer_disabled", &acct.ID, map[string]any{"endpoint_id": ep.ID, "channel": ch})
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
			return
		}
	default:
		row, err = store.GetManagedRealtimeReducer(r.Context(), ep.ID, ch)
	}
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "channel reducer not found")
		return
	}
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Reducer baseline conflict", "seed sequence must equal the current channel head; an active reducer cannot be reseeded"))
		return
	}
	if err != nil {
		s.writeManagedRealtimeHistoryError(w, r, err)
		return
	}
	if r.Method == http.MethodPut {
		s.audit.Emit(r.Context(), "realtime.reducer_enabled", &acct.ID, map[string]any{"endpoint_id": ep.ID, "channel": ch, "sequence": row.Sequence})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, api.ManagedRealtimeReducerResponse{Channel: ch, Sequence: row.Sequence, Entities: row.Entities, EntityVersions: row.EntityVersions, EntityExpirations: row.EntityExpirations, UpdatedAt: row.UpdatedAt})
}
