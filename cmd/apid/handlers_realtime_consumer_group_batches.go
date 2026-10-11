package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) claimRealtimeConsumerGroupBatch(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, _, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	var req state.RealtimeConsumerGroupBatchRequest
	if decodeJSONSized(r, &req, api.RealtimeConsumerGroupBodyMaxBytes) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid batch claim request"))
		return
	}
	s.writeRealtimeGroupBatch(w, r, ep.ID, r.PathValue("channel"), r.PathValue("group"), req)
}
func (s *server) realtimeWorkerClaimBatch(w http.ResponseWriter, r *http.Request) {
	r, g, _, ok := s.loadRealtimeWorker(w, r, "claim")
	if !ok {
		return
	}
	var req state.RealtimeConsumerGroupBatchRequest
	if decodeJSONSized(r, &req, api.RealtimeConsumerGroupBodyMaxBytes) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid worker batch claim request"))
		return
	}
	if req.Worker != "" && req.Worker != g.Info.Name {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Worker identity denied", "worker identity is fixed by the credential"))
		return
	}
	req.Worker = g.Info.Name
	s.writeRealtimeGroupBatch(w, r, g.EndpointID, g.Channel, g.Group, req)
}
func (s *server) writeRealtimeGroupBatch(w http.ResponseWriter, r *http.Request, ep, ch, group string, req state.RealtimeConsumerGroupBatchRequest) {
	_, ok := s.store.(state.RealtimeConsumerGroupBatchStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("batch claims unavailable"))
		return
	}
	batch, err := s.waitRealtimeGroupBatch(r, ep, ch, group, req.Worker, req.Limit, req.WaitSeconds)
	if err != nil {
		s.writeRealtimeWorkerOperationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, batch)
}
