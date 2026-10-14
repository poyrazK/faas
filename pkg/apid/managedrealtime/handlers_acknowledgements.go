package managedrealtime

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *Service) ManagedRealtimeAcknowledgement(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.HistoryPreviewEnabled {
		httpkit.NotFound(w, "acknowledgement preview unavailable")
		return
	}
	ep, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.RealtimeAcknowledgementStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("acknowledgement status unavailable"))
		return
	}
	result, err := store.ReadRealtimeAcknowledgement(r.Context(), ep.ID, r.URL.Query().Get("principal"), r.URL.Query().Get("subscription"), r.PathValue("channel"))
	if err != nil {
		s.writeRealtimeInboxError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, result)
}
