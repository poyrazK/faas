package managedrealtime

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ManagedRealtimeNotificationControl(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.previewUnavailable(w, acct.ID, s.HistoryPreviewEnabled, "notification control preview unavailable") {
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
	store, ok := s.Host.Store().(state.ManagedRealtimeNotificationControlStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("notification control unavailable"))
		return
	}
	var at *time.Time
	if r.Method == http.MethodPut {
		var req api.ManagedRealtimeNotificationRescheduleRequest
		if httpkit.DecodeJSONSized(r, &req, 4096) != nil || req.NotificationNotBefore == "" {
			api.WriteProblem(w, api.ErrRealtimeInvalid("notification_not_before is required"))
			return
		}
		parsed, err := api.ParseRealtimeNotificationNotBefore(req.NotificationNotBefore, time.Now().UTC())
		if err != nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid(err.Error()))
			return
		}
		at = &parsed
	}
	result, err := store.ControlManagedRealtimeNotification(r.Context(), ep.ID, principal, r.PathValue("message_id"), at)
	if err != nil {
		s.writeRealtimeInboxError(w, r, err)
		return
	}
	s.Host.EmitAudit(r.Context(), "realtime.notification_controlled", &acct.ID, map[string]any{"endpoint_id": ep.ID, "cancelled": at == nil, "fallbacks": result.Fallbacks, "deliveries": result.Deliveries})
	httpkit.WriteJSON(w, http.StatusOK, result)
}
