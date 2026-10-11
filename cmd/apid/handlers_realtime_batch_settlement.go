package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) realtimeConsumerGroupBatchSettle(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, _, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	var req state.RealtimeConsumerGroupBatchSettlementRequest
	if decodeJSONSized(r, &req, api.RealtimeConsumerGroupSettlementBodyMaxBytes) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid batch settlement"))
		return
	}
	s.writeRealtimeBatchSettlement(w, r, ep.ID, r.PathValue("channel"), r.PathValue("group"), req)
}
func (s *server) realtimeWorkerBatchSettle(w http.ResponseWriter, r *http.Request) {
	if _, err := s.authenticateRealtimeWorker(r); err != nil {
		s.writeRealtimeWorkerOperationError(w, r, err)
		return
	}
	var req state.RealtimeConsumerGroupBatchSettlementRequest
	if decodeJSONSized(r, &req, api.RealtimeConsumerGroupSettlementBodyMaxBytes) != nil || len(req.Settlements) < 1 || len(req.Settlements) > api.RealtimeConsumerGroupSettlementBatchMax {
		api.WriteProblem(w, api.ErrRealtimeInvalid("batch requires 1 to 32 settlements"))
		return
	}
	action := req.Settlements[0].Action
	if action != "complete" && action != "release" && action != "retry" && action != "renew" {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid settlement action"))
		return
	}
	r, g, _, ok := s.loadRealtimeWorker(w, r, action)
	if !ok {
		return
	}
	s.writeRealtimeBatchSettlement(w, r, g.EndpointID, g.Channel, g.Group, req)
}
func (s *server) writeRealtimeBatchSettlement(w http.ResponseWriter, r *http.Request, ep, ch, group string, req state.RealtimeConsumerGroupBatchSettlementRequest) {
	store, ok := s.store.(state.RealtimeConsumerGroupBatchSettlementStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("batch settlement unavailable"))
		return
	}
	result, err := store.SettleRealtimeConsumerGroupBatch(r.Context(), ep, ch, group, req)
	if err != nil {
		s.writeRealtimeWorkerOperationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
