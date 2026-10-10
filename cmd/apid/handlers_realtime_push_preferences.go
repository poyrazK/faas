package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) managedRealtimePushPreferences(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.realtimeHistoryPreviewEnabled {
		s.notFound(w, "notification preferences preview unavailable")
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
	store, ok := s.store.(state.ManagedRealtimePushPreferencesStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("notification preferences unavailable"))
		return
	}
	if r.Method == http.MethodPut {
		var raw json.RawMessage
		if decodeJSONSized(r, &raw, 4096) != nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid("invalid notification preferences"))
			return
		}
		p, err := api.DecodeRealtimeNotificationPreferences(raw)
		if err != nil {
			api.WriteProblem(w, api.ErrRealtimeInvalid("invalid notification preferences, categories, devices or quiet hours"))
			return
		}

		if err := store.PutManagedRealtimeNotificationPreferences(r.Context(), ep.ID, principal, p); err != nil {
			if errors.Is(err, state.ErrManagedRealtimeDurableCursorLimit) {
				api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, api.CodeCapacity, "Notification preference limit reached", "the endpoint has reached its 256 user preference document limit"))
			} else {
				s.writeRealtimeInboxError(w, r, err)
			}
			return
		}
		s.audit.Emit(r.Context(), "realtime.notification_preferences_updated", &acct.ID, map[string]any{"endpoint_id": ep.ID})
		writeJSON(w, http.StatusOK, p)
		return
	}
	p, err := store.GetManagedRealtimeNotificationPreferences(r.Context(), ep.ID, principal)
	if err != nil {
		s.writeRealtimeInboxError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}
