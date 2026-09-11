package main

import (
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var obsTraceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// obsTraceLookup handles the bounded, exact-index lookup used during an
// operator incident. It intentionally joins in the handler rather than SQL so
// each table can use its existing partial trace_id index independently.
func (s *server) obsTraceLookup(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	traceID := r.PathValue("trace_id")
	if !obsTraceIDPattern.MatchString(traceID) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid trace_id", "trace_id must match ^[0-9a-f]{32}$"))
		return
	}
	limit, prob := parseObsTraceLimit(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	intents, err := s.store.ListOperatorIntentsByTraceID(r.Context(), traceID, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list operator intents for trace"))
		return
	}
	events, err := s.store.ListEventsByTraceID(r.Context(), traceID, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list events for trace"))
		return
	}
	if len(intents) == 0 && len(events) == 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"trace_not_found", "no operator intent or event has that trace id"))
		return
	}
	intentRows := make([]api.OperatorIntentResponse, 0, len(intents))
	for _, intent := range intents {
		intentRows = append(intentRows, toOperatorIntentResponse(intent))
	}
	writeJSON(w, http.StatusOK, api.ObsTraceLookupResponse{
		TraceID:     traceID,
		GeneratedAt: time.Now().UTC(),
		Limit:       limit,
		Intents:     intentRows,
		Events:      toObsEventRows(events),
	})
}

func parseObsTraceLimit(r *http.Request) (int, *api.Problem) {
	limit := api.ObsAdminEventsLimitDefault
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			return 0, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid limit", "limit must be a positive integer")
		}
		if value > api.ObsAdminEventsLimitMax {
			value = api.ObsAdminEventsLimitMax
		}
		limit = value
	}
	return limit, nil
}
