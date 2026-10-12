package appevents

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) GetEventRecoveryPreflight(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, r, cancel, ok := s.eventRecoveryStore(w, r)
	if !ok {
		return
	}
	defer cancel()
	retry := r.URL.Query().Get("retry") == "true"
	if len(r.URL.Query()) != 0 && (len(r.URL.Query()) != 1 || !retry || len(r.URL.Query()["retry"]) != 1) {
		s.writeEventRecovery(w, r, 0, nil, state.ErrEventRecoveryQuery)
		return
	}
	store, ok := s.Host.Store().(state.EventRecoveryPreflightStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("recovery preflight"))
		return
	}
	var out api.EventRecoveryPreflight
	var err error
	if retry {
		retryStore, ok := s.Host.Store().(state.WorkflowRecoveryRetryPreflightStore)
		if !ok {
			api.WriteProblem(w, api.ErrInternal("workflow retry preflight"))
			return
		}
		out, err = retryStore.GetWorkflowRecoveryRetryPreflight(r.Context(), acct.ID, r.PathValue("jobID"), time.Now().UTC())
	} else {
		out, err = store.GetEventRecoveryPreflight(r.Context(), acct.ID, r.PathValue("jobID"), time.Now().UTC())
	}
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
