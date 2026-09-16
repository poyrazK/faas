package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apislogs"
	"github.com/onebox-faas/faas/pkg/state"
)

// streamExecutionEvents handles GET /v1/executions/{id}/events. The event
// log is account-scoped and replayable by Last-Event-ID, so an agent can
// reconnect without a guest workspace or a long-lived VM.
func (s *server) streamExecutionEvents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !requireExecutionEntitlement(w, acct) {
		return
	}
	if !s.requireExecutionAPI(w) {
		return
	}
	eventStore, ok := s.store.(state.ExecutionEventStore)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusNotImplemented, api.CodeNotImplemented,
			"Execution events unavailable", "this control-plane store does not support resumable execution events"))
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		s.notFound(w, "no such execution")
		return
	}
	row, err := s.store.ExecutionByID(r.Context(), acct.ID, id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such execution")
			return
		}
		api.WriteProblem(w, api.ErrInternal("could not load execution"))
		return
	}
	after, problem := executionEventCursor(r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	limitProblem, limit := api.ParseLimit(r.URL.Query().Get("limit"), 100, 1000, "execution events")
	if limitProblem != nil {
		api.WriteProblem(w, limitProblem)
		return
	}

	apislogs.StartSSE(w)
	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		events, listErr := eventStore.ListExecutionEvents(r.Context(), acct.ID, id, after, limit)
		if listErr != nil {
			writeExecutionSSEError(w, flush, "could not read execution events")
			return
		}
		for _, event := range events {
			writeExecutionSSEEvent(w, flush, event)
			after = event.Sequence
			if event.Type == state.ExecutionEventTerminal {
				return
			}
		}
		if row.Status.Terminal() && len(events) == 0 {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			fresh, readErr := s.store.ExecutionByID(r.Context(), acct.ID, id)
			if readErr == nil {
				row = fresh
			}
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ":\n\n")
			flush()
		}
	}
}

func executionEventCursor(r *http.Request) (int64, *api.Problem) {
	raw := strings.TrimSpace(r.URL.Query().Get("after"))
	if raw == "" {
		raw = strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	}
	if raw == "" {
		return 0, nil
	}
	cursor, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || cursor < 0 {
		return 0, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad execution event cursor", "after and Last-Event-ID must be a non-negative integer")
	}
	return cursor, nil
}

func writeExecutionSSEEvent(w http.ResponseWriter, flush func(), event state.ExecutionEvent) {
	_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, event.Payload)
	flush()
}

func writeExecutionSSEError(w http.ResponseWriter, flush func(), message string) {
	payload, _ := json.Marshal(struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Code: "execution_events_unavailable", Message: message})
	_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", payload)
	flush()
}
