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
