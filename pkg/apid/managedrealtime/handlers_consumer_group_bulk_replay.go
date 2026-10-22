package managedrealtime

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *Service) PreviewRealtimeConsumerGroupDeadLetters(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, _, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.RealtimeConsumerGroupBulkReplayStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("bulk replay unavailable"))
		return
	}
	var req state.RealtimeDeadLetterPreviewRequest
	if httpkit.DecodeJSONSized(r, &req, api.RealtimeConsumerGroupBulkReplayBodyMaxBytes) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid dead-letter preview filters"))
		return
	}
	result, err := store.PreviewRealtimeConsumerGroupDeadLetters(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"), req)
	if err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, result)
}
func (s *Service) BulkReplayRealtimeConsumerGroupDeadLetters(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, _, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.RealtimeConsumerGroupBulkReplayStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("bulk replay unavailable"))
		return
	}
	var req state.RealtimeDeadLetterBulkReplayRequest
	if httpkit.DecodeJSONSized(r, &req, api.RealtimeConsumerGroupBulkReplayBodyMaxBytes) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid dead-letter replay selection"))
		return
	}
	result, err := store.BulkReplayRealtimeConsumerGroupDeadLetters(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"), req)
	if err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "realtime.consumer_group_bulk_replayed", &acct.ID, map[string]any{"endpoint_id": ep.ID, "channel": r.PathValue("channel"), "group_id": result.GroupID, "selected": len(req.Items), "replayed": result.Replayed})
	httpkit.WriteJSON(w, http.StatusOK, result)
}
