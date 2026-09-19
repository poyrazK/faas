package main

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const deadLetterEventsMaxLimit = 200

func deadLetterEventResponse(ev state.DeadLetterEvent) api.DeadLetterEvent {
	return api.DeadLetterEvent{
		ID:            ev.ID,
		AppID:         ev.AppID,
		Source:        ev.Source,
		SourceID:      ev.SourceID,
		Origin:        ev.Origin,
		TriggerID:     ev.TriggerID,
		Payload:       ev.Payload,
		Headers:       ev.Headers,
		ErrorKind:     ev.ErrorKind,
		ErrorDetail:   ev.ErrorDetail,
		RetryCount:    ev.RetryCount,
		FirstFailedAt: ev.FirstFailedAt,
		LastFailedAt:  ev.LastFailedAt,
		ReplayedAt:    ev.ReplayedAt,
		CreatedAt:     ev.CreatedAt,
	}
}

func deadLetterEventResponseForApp(ev state.DeadLetterEvent, appSlug string) api.DeadLetterEvent {
	out := deadLetterEventResponse(ev)
	out.AppSlug = appSlug
	return out
}

func deadLetterNotFound(id string) *api.Problem {
	return api.NewProblem(http.StatusNotFound, api.CodeNotFound,
		"Dead-letter event not found", "no dead-letter event with id "+strconv.Quote(id)+" belongs to this app.")
}

func (s *server) listDeadLetterEvents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= deadLetterEventsMaxLimit {
			limit = n
		}
	}
	events, err := s.store.ListDeadLetterEvents(r.Context(), app.ID, limit, r.URL.Query().Get("before"))
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("dead-letter events"))
		return
	}
	out := api.DeadLetterEventsResponse{AppSlug: app.Slug, Events: make([]api.DeadLetterEvent, 0, len(events))}
	for _, ev := range events {
		out.Events = append(out.Events, deadLetterEventResponseForApp(ev, app.Slug))
	}
	if len(events) == limit && len(events) > 0 {
		out.NextBefore = events[len(events)-1].ID
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getDeadLetterEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	ev, err := s.store.DeadLetterEventByID(r.Context(), app.ID, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, deadLetterNotFound(r.PathValue("id")))
			return
		}
		api.WriteProblem(w, api.ErrInternal("dead-letter event"))
		return
	}
	writeJSON(w, http.StatusOK, deadLetterEventResponseForApp(ev, app.Slug))
}

func (s *server) replayDeadLetterEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	ev, err := s.store.ReplayDeadLetterEvent(r.Context(), acct.ID, app.ID, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.ops.ObserveDLQReplay(app.Slug, "not_found")
			api.WriteProblem(w, deadLetterNotFound(r.PathValue("id")))
			return
		}
		s.ops.ObserveDLQReplay(app.Slug, "error")
		api.WriteProblem(w, api.ErrInternal("dead-letter replay"))
		return
	}
	s.ops.ObserveDLQReplay(app.Slug, "success")
	s.audit.Emit(r.Context(), "app.dlq.event_replayed", &acct.ID, map[string]any{
		"app_id": app.ID, "event_id": ev.ID, "source": ev.Source, "source_id": ev.SourceID,
	})
	writeJSON(w, http.StatusAccepted, deadLetterEventResponseForApp(ev, app.Slug))
}

func (s *server) replayAllDeadLetterEvents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limit := deadLetterEventsMaxLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 || n > deadLetterEventsMaxLimit {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeBadRequest,
				"Invalid dead-letter replay limit", "limit must be between 1 and 200"))
			return
		}
		limit = n
	}
	replayed, err := s.store.ReplayDeadLetterEvents(r.Context(), acct.ID, app.ID, limit)
	if err != nil {
		s.ops.ObserveDLQReplay(app.Slug, "error")
		api.WriteProblem(w, api.ErrInternal("dead-letter replay"))
		return
	}
	for i := 0; i < replayed; i++ {
		s.ops.ObserveDLQReplay(app.Slug, "success")
	}
	if replayed > 0 {
		s.audit.Emit(r.Context(), "app.dlq.event_replayed", &acct.ID, map[string]any{
			"app_id": app.ID, "count": replayed, "operation": "batch",
		})
	}
	writeJSON(w, http.StatusAccepted, api.DeadLetterReplayAllResponse{AppSlug: app.Slug, Replayed: replayed})
}

func (s *server) deleteDeadLetterEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if err := s.store.DeleteDeadLetterEvent(r.Context(), acct.ID, app.ID, r.PathValue("id")); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.ops.ObserveDLQPurge(app.Slug, "not_found")
			api.WriteProblem(w, deadLetterNotFound(r.PathValue("id")))
			return
		}
		s.ops.ObserveDLQPurge(app.Slug, "error")
		api.WriteProblem(w, api.ErrInternal("dead-letter purge"))
		return
	}
	s.ops.ObserveDLQPurge(app.Slug, "success")
	s.audit.Emit(r.Context(), "app.dlq.purged", &acct.ID, map[string]any{
		"app_id": app.ID, "event_id": r.PathValue("id"), "count": 1,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) purgeDeadLetterEvents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limit := deadLetterEventsMaxLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 || n > deadLetterEventsMaxLimit {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeBadRequest,
				"Invalid dead-letter purge limit", "limit must be between 1 and 200"))
			return
		}
		limit = n
	}
	purged, err := s.store.DeleteDeadLetterEvents(r.Context(), acct.ID, app.ID, limit)
	if err != nil {
		s.ops.ObserveDLQPurge(app.Slug, "error")
		api.WriteProblem(w, api.ErrInternal("dead-letter purge"))
		return
	}
	for i := 0; i < purged; i++ {
		s.ops.ObserveDLQPurge(app.Slug, "success")
	}
	if purged > 0 {
		s.audit.Emit(r.Context(), "app.dlq.purged", &acct.ID, map[string]any{
			"app_id": app.ID, "count": purged, "operation": "batch",
		})
	}
	writeJSON(w, http.StatusOK, api.DeadLetterPurgeResponse{AppSlug: app.Slug, Purged: purged})
}
