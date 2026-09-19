package main

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// accountDeadLetterAppSlugs resolves the optional app identity carried by an
// account-wide event. Account-owned job runs intentionally have no app_id, so
// those rows simply retain an empty app_slug in the wire response.
func (s *server) accountDeadLetterAppSlugs(r *http.Request, acct state.Account) (map[string]string, error) {
	apps, err := s.store.ListApps(r.Context(), acct.ID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(apps))
	for _, app := range apps {
		out[app.ID] = app.Slug
	}
	return out, nil
}

func accountDeadLetterEventResponse(ev state.DeadLetterEvent, appSlugs map[string]string) api.DeadLetterEvent {
	out := deadLetterEventResponse(ev)
	if ev.AppID != "" {
		out.AppSlug = appSlugs[ev.AppID]
	}
	return out
}

func accountDeadLetterNotFound(id string) *api.Problem {
	return api.NewProblem(http.StatusNotFound, api.CodeNotFound,
		"Dead-letter event not found", "no dead-letter event with id "+strconv.Quote(id)+" belongs to this account.")
}

func parseAccountDeadLetterLimit(w http.ResponseWriter, r *http.Request, operation string) (int, bool) {
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 || n > deadLetterEventsMaxLimit {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest,
				api.CodeBadRequest, "Invalid dead-letter "+operation+" limit",
				"limit must be between 1 and 200"))
			return 0, false
		}
		limit = n
	}
	return limit, true
}

func (s *server) listAccountDeadLetterEvents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limit, ok := parseAccountDeadLetterLimit(w, r, "list")
	if !ok {
		return
	}
	events, err := s.store.ListDeadLetterEventsForAccount(r.Context(), acct.ID, limit, r.URL.Query().Get("before"))
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("account dead-letter events"))
		return
	}
	appSlugs, err := s.accountDeadLetterAppSlugs(r, acct)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("account dead-letter apps"))
		return
	}
	out := api.AccountDeadLetterEventsResponse{Events: make([]api.DeadLetterEvent, 0, len(events))}
	for _, ev := range events {
		out.Events = append(out.Events, accountDeadLetterEventResponse(ev, appSlugs))
	}
	if len(events) == limit && len(events) > 0 {
		out.NextBefore = events[len(events)-1].ID
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getAccountDeadLetterEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	eventID := r.PathValue("id")
	ev, err := s.store.DeadLetterEventByAccountID(r.Context(), acct.ID, eventID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, accountDeadLetterNotFound(eventID))
			return
		}
		api.WriteProblem(w, api.ErrInternal("account dead-letter event"))
		return
	}
	appSlugs, err := s.accountDeadLetterAppSlugs(r, acct)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("account dead-letter apps"))
		return
	}
	writeJSON(w, http.StatusOK, accountDeadLetterEventResponse(ev, appSlugs))
}

func (s *server) replayAccountDeadLetterEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	eventID := r.PathValue("id")
	ev, err := s.store.ReplayDeadLetterEventForAccount(r.Context(), acct.ID, eventID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.ops.ObserveDLQReplay("account", "not_found")
			api.WriteProblem(w, accountDeadLetterNotFound(eventID))
			return
		}
		s.ops.ObserveDLQReplay("account", "error")
		api.WriteProblem(w, api.ErrInternal("account dead-letter replay"))
		return
	}
	s.ops.ObserveDLQReplay("account", "success")
	s.audit.Emit(r.Context(), "account.dlq.event_replayed", &acct.ID, map[string]any{
		"event_id": ev.ID, "source": ev.Source, "source_id": ev.SourceID,
		"app_id": ev.AppID, "account_scope": true,
	})
	appSlugs, _ := s.accountDeadLetterAppSlugs(r, acct)
	writeJSON(w, http.StatusAccepted, accountDeadLetterEventResponse(ev, appSlugs))
}

func (s *server) replayAllAccountDeadLetterEvents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limit, ok := parseAccountDeadLetterLimit(w, r, "replay")
	if !ok {
		return
	}
	replayed, err := s.store.ReplayDeadLetterEventsForAccount(r.Context(), acct.ID, limit)
	if err != nil {
		s.ops.ObserveDLQReplay("account", "error")
		api.WriteProblem(w, api.ErrInternal("account dead-letter replay"))
		return
	}
	for i := 0; i < replayed; i++ {
		s.ops.ObserveDLQReplay("account", "success")
	}
	if replayed > 0 {
		s.audit.Emit(r.Context(), "account.dlq.event_replayed", &acct.ID, map[string]any{
			"count": replayed, "operation": "batch", "account_scope": true,
		})
	}
	writeJSON(w, http.StatusAccepted, api.AccountDeadLetterReplayAllResponse{Replayed: replayed})
}

func (s *server) deleteAccountDeadLetterEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	eventID := r.PathValue("id")
	if err := s.store.DeleteDeadLetterEventForAccount(r.Context(), acct.ID, eventID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.ops.ObserveDLQPurge("account", "not_found")
			api.WriteProblem(w, accountDeadLetterNotFound(eventID))
			return
		}
		s.ops.ObserveDLQPurge("account", "error")
		api.WriteProblem(w, api.ErrInternal("account dead-letter purge"))
		return
	}
	s.ops.ObserveDLQPurge("account", "success")
	s.audit.Emit(r.Context(), "account.dlq.purged", &acct.ID, map[string]any{
		"event_id": eventID, "count": 1, "account_scope": true,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) purgeAccountDeadLetterEvents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limit, ok := parseAccountDeadLetterLimit(w, r, "purge")
	if !ok {
		return
	}
	purged, err := s.store.DeleteDeadLetterEventsForAccount(r.Context(), acct.ID, limit)
	if err != nil {
		s.ops.ObserveDLQPurge("account", "error")
		api.WriteProblem(w, api.ErrInternal("account dead-letter purge"))
		return
	}
	for i := 0; i < purged; i++ {
		s.ops.ObserveDLQPurge("account", "success")
	}
	if purged > 0 {
		s.audit.Emit(r.Context(), "account.dlq.purged", &acct.ID, map[string]any{
			"count": purged, "operation": "batch", "account_scope": true,
		})
	}
	writeJSON(w, http.StatusOK, api.AccountDeadLetterPurgeResponse{Purged: purged})
}
