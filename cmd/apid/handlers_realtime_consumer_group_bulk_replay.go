package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) previewRealtimeConsumerGroupDeadLetters(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, _, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.RealtimeConsumerGroupBulkReplayStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("bulk replay unavailable"))
		return
	}
	var req state.RealtimeDeadLetterPreviewRequest
	if decodeJSONSized(r, &req, api.RealtimeConsumerGroupBulkReplayBodyMaxBytes) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid dead-letter preview filters"))
		return
	}
	result, err := store.PreviewRealtimeConsumerGroupDeadLetters(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"), req)
	if err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (s *server) bulkReplayRealtimeConsumerGroupDeadLetters(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, _, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.RealtimeConsumerGroupBulkReplayStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("bulk replay unavailable"))
		return
	}
	var req state.RealtimeDeadLetterBulkReplayRequest
	if decodeJSONSized(r, &req, api.RealtimeConsumerGroupBulkReplayBodyMaxBytes) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid dead-letter replay selection"))
		return
	}
	result, err := store.BulkReplayRealtimeConsumerGroupDeadLetters(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"), req)
	if err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	s.audit.Emit(r.Context(), "realtime.consumer_group_bulk_replayed", &acct.ID, map[string]any{"endpoint_id": ep.ID, "channel": r.PathValue("channel"), "group_id": result.GroupID, "selected": len(req.Items), "replayed": result.Replayed})
	writeJSON(w, http.StatusOK, result)
}
