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
		out.Events = append(out.Events, deadLetterEventResponse(ev))
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
	writeJSON(w, http.StatusOK, deadLetterEventResponse(ev))
}

func (s *server) replayDeadLetterEvent(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	ev, err := s.store.ReplayDeadLetterEvent(r.Context(), acct.ID, app.ID, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, deadLetterNotFound(r.PathValue("id")))
			return
		}
		api.WriteProblem(w, api.ErrInternal("dead-letter replay"))
		return
	}
	writeJSON(w, http.StatusAccepted, deadLetterEventResponse(ev))
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
		api.WriteProblem(w, api.ErrInternal("dead-letter replay"))
		return
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
			api.WriteProblem(w, deadLetterNotFound(r.PathValue("id")))
			return
		}
		api.WriteProblem(w, api.ErrInternal("dead-letter purge"))
		return
	}
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
		api.WriteProblem(w, api.ErrInternal("dead-letter purge"))
		return
	}
	writeJSON(w, http.StatusOK, api.DeadLetterPurgeResponse{AppSlug: app.Slug, Purged: purged})
}
