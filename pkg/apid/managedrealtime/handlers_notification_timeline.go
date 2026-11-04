package managedrealtime

import (
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ManagedRealtimeNotificationTimeline(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.previewUnavailable(w, acct.ID, s.HistoryPreviewEnabled, "notification timeline preview unavailable") {
		return
	}
	ep, _, ok := s.loadManagedRealtimeEndpoint(w, r, acct)
	if !ok {
		return
	}
	principal := r.URL.Query().Get("principal")
	if api.ValidateRealtimePrincipal(principal) != nil {
		api.WriteProblem(w, api.ErrRealtimeInvalid("invalid principal"))
		return
	}
	before := int64(0)
	if value := r.URL.Query().Get("before"); value != "" {
		var err error
		before, err = strconv.ParseInt(value, 10, 64)
		if err != nil || before < 0 {
			api.WriteProblem(w, api.ErrRealtimeInvalid("before must be a nonnegative timeline ID"))
			return
		}
	}
	store, ok := s.Host.Store().(state.ManagedRealtimeNotificationTimelineStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("notification timeline unavailable"))
		return
	}
	events, err := store.ListManagedRealtimeNotificationTimeline(r.Context(), ep.ID, principal, r.PathValue("message_id"), before)
	if err != nil {
		s.writeRealtimeInboxError(w, r, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, events)
}
