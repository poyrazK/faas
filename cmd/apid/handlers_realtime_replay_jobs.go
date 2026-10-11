package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) realtimeReplayJobs(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, _, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.RealtimeReplayJobStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("replay jobs unavailable"))
		return
	}
	if r.Method == http.MethodGet {
		jobs, err := store.ListRealtimeReplayJobs(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"))
		if err != nil {
			s.writeRealtimeConsumerGroupError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, jobs)
		return
	}
	var result state.RealtimeReplayJob
	var err error
	if r.PathValue("job") == "" {
		var req state.RealtimeReplayJobRequest
		if decodeJSONSized(r, &req, api.RealtimeConsumerGroupBulkReplayBodyMaxBytes) != nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid("invalid replay job filters or rate"))
			return
		}
		req.DryRun = r.PathValue("mode") == "dry_run"
		result, err = store.CreateRealtimeReplayJob(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"), req)
	} else {
		result, err = store.ControlRealtimeReplayJob(r.Context(), ep.ID, r.PathValue("channel"), r.PathValue("group"), r.PathValue("job"), r.PathValue("action"))
	}
	if err != nil {
		s.writeRealtimeConsumerGroupError(w, r, err)
		return
	}
	s.audit.Emit(r.Context(), "realtime.replay_job_changed", &acct.ID, map[string]any{"endpoint_id": ep.ID, "group_id": result.GroupID, "job_id": result.ID, "state": result.State, "selected": result.Selected, "rate": result.Rate, "dry_run": result.DryRun, "reviewed_job_id": result.ReviewedJobID})
	code := http.StatusOK
	if r.PathValue("job") == "" {
		code = http.StatusCreated
	}
	writeJSON(w, code, result)
}

func (s *server) createRealtimeReplayDryRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	req := r.Clone(r.Context())
	req.SetPathValue("mode", "dry_run")
	s.realtimeReplayJobs(w, req, acct)
}
