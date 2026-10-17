package operation

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// OperationTimelineCursor is the wire form of every source's position
// (ADR-1195). It is bound to one Operation and must round-trip canonically.
type OperationTimelineCursor struct {
	OperationID string                      `json:"operation_id"`
	Event       int64                       `json:"event"`
	Attempt     [2]int                      `json:"attempt"`
	Step        OperationTimelineStepCursor `json:"step"`
	Delivery    [2]int                      `json:"delivery"`
}

type OperationTimelineStepCursor struct {
	StartedAt time.Time `json:"started_at"`
	Name      string    `json:"name"`
	Attempt   int       `json:"attempt"`
}

func encodeOperationTimelineCursor(id string, c state.OperationTimelineCursor) string {
	raw, _ := json.Marshal(OperationTimelineCursor{OperationID: id, Event: c.Event,
		Attempt:  [2]int{c.Attempt.Generation, c.Attempt.Attempt},
		Step:     OperationTimelineStepCursor{c.Step.StartedAt.UTC(), c.Step.Step, c.Step.Attempt},
		Delivery: [2]int{c.Delivery.Generation, c.Delivery.Attempt}})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeOperationTimelineCursor(id, cursor string) (state.OperationTimelineCursor, error) {
	if len(cursor) > api.OperationHistoryCursorMaxBytes {
		return state.OperationTimelineCursor{}, state.ErrInvalidArgument
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return state.OperationTimelineCursor{}, state.ErrInvalidArgument
	}
	var c OperationTimelineCursor
	if json.Unmarshal(raw, &c) != nil || c.OperationID != id {
		return state.OperationTimelineCursor{}, state.ErrInvalidArgument
	}
	out := state.OperationTimelineCursor{Event: c.Event,
		Attempt:  state.OperationTimelineAttemptKey{Generation: c.Attempt[0], Attempt: c.Attempt[1]},
		Step:     state.OperationTimelineStepKey{StartedAt: c.Step.StartedAt, Step: c.Step.Name, Attempt: c.Step.Attempt},
		Delivery: state.OperationTimelineAttemptKey{Generation: c.Delivery[0], Attempt: c.Delivery[1]}}
	if encodeOperationTimelineCursor(id, out) != cursor {
		return state.OperationTimelineCursor{}, state.ErrInvalidArgument
	}
	return out, nil
}

func operationTimelineQuery(r *http.Request, id string) (int, state.OperationTimelineCursor, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return 0, state.OperationTimelineCursor{}, state.ErrInvalidArgument
	}
	for key, values := range q {
		if (key != "limit" && key != "cursor") || len(values) != 1 || values[0] == "" {
			return 0, state.OperationTimelineCursor{}, state.ErrInvalidArgument
		}
	}
	limit := api.OperationHistoryPageDefault
	if q.Has("limit") {
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || limit < 1 || limit > api.OperationHistoryPageMax {
			return 0, state.OperationTimelineCursor{}, state.ErrInvalidArgument
		}
	}
	if !q.Has("cursor") {
		return limit, state.OperationTimelineCursor{}, nil
	}
	cursor, err := decodeOperationTimelineCursor(id, q.Get("cursor"))
	return limit, cursor, err
}

// GetOperationTimeline merges an Operation's events, attempts and completion
// deliveries into one chronological page (ADR-1195). Account-only: attempts,
// errors and log windows are owner diagnostics.
func (s *Service) GetOperationTimeline(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, store, ok := s.ownedOperation(w, r, acct)
	if !ok {
		return
	}
	timeline, ok := store.(state.OperationTimelineStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation timeline storage is unavailable"))
		return
	}
	limit, cursor, err := operationTimelineQuery(r, op.ID)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	page, err := timeline.OperationTimeline(r.Context(), acct.ID, op.ID, cursor, limit)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	if page.Next != nil {
		page.Response.NextCursor = encodeOperationTimelineCursor(op.ID, *page.Next)
	}
	httpkit.WriteJSON(w, http.StatusOK, page.Response)
}
