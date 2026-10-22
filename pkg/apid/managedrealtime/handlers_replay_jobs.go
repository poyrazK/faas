package managedrealtime

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *Service) RealtimeReplayJobs(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ep, _, ok := s.loadRealtimeConsumerGroups(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.RealtimeReplayJobStore)
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
		httpkit.WriteJSON(w, http.StatusOK, jobs)
		return
	}
	var result state.RealtimeReplayJob
	var err error
	if r.PathValue("job") == "" {
		var req state.RealtimeReplayJobRequest
		if httpkit.DecodeJSONSized(r, &req, api.RealtimeConsumerGroupBulkReplayBodyMaxBytes) != nil {
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
	s.Host.EmitAudit(r.Context(), "realtime.replay_job_changed", &acct.ID, map[string]any{"endpoint_id": ep.ID, "group_id": result.GroupID, "job_id": result.ID, "state": result.State, "selected": result.Selected, "rate": result.Rate, "dry_run": result.DryRun, "reviewed_job_id": result.ReviewedJobID})
	code := http.StatusOK
	if r.PathValue("job") == "" {
		code = http.StatusCreated
	}
	httpkit.WriteJSON(w, code, result)
}

func (s *Service) CreateRealtimeReplayDryRun(w http.ResponseWriter, r *http.Request, acct state.Account) {
	req := r.Clone(r.Context())
	req.SetPathValue("mode", "dry_run")
	s.RealtimeReplayJobs(w, req, acct)
}
