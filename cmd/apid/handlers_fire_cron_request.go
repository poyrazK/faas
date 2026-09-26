package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// handlers_fire_cron_request.go — GET /v1/cron-fire-now-requests/{request_id}
// (issue #791 PR-D / ADR-090 §Sub-decision 7).
//
// Read surface for the cron_fire_now_requests row that
// `POST /v1/crons/{id}/run` inserts (PR-C). Customers poll this
// endpoint with the request_id returned by the 202 acknowledgement
// until Status reaches a terminal value (succeeded | failed |
// cancelled).
//
// IDOR contract — same byte-identical-404 rule as PR-C POST:
//   - request_id unparseable        → 404 not_found
//   - row not found                 → 404 not_found
//   - row on another account        → 404 not_found (no existence oracle)
//   - rate limited                  → 429 (authLimited)
//   - server error                  → 500 (ErrInternal)
//
// Note on path collisions: the {request_id} is a UUID, but the
// handler ALSO has to NOT distinguish "invalid UUID" from
// "wrong account" — a probing customer must not be able to use
// the response shape to tell whether their id was correctly formed.
// We therefore use a single 404 path for both missing-row and
// bad-format (rather than 400 for bad-uuid), keeping the existence
// oracle closed.

func (s *server) getFireCronRequest(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ctx := r.Context()
	requestID := r.PathValue("request_id")

	// UUID parse check: accept format-only, not existence. A
	// non-UUID-shape request is treated as 404 (not 400) — see the
	// note above. Without this, a probing customer could submit
	// arbitrary strings to see whether the path matches; treating
	// bad shape as missing-id keeps the surface uniform with the
	// "wrong account" branch below.
	if _, err := uuid.Parse(requestID); err != nil {
		s.notFound(w, "no such fire-now request")
		return
	}

	req, err := s.store.GetFireNowRequest(ctx, requestID)
	if errors.Is(err, state.ErrFireNowRequestNotFound) {
		s.notFound(w, "no such fire-now request")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load fire-now"))
		return
	}

	// IDOR-safe single-step: the row's account_id is denormalised
	// (migrations/00194_cron_fire_now_requests.sql:51), so we don't
	// need the CronByID → AppByID hop the two-step handlers use.
	// Cross-account rows are 404-not-403, and the body MUST be
	// byte-identical to the missing case above.
	if req.AccountID != acct.ID {
		s.notFound(w, "no such fire-now request")
		return
	}

	writeJSON(w, http.StatusOK, api.FireCronRequestResponse{
		RequestID:    req.ID,
		CronID:       req.CronID,
		Status:       string(req.Status),
		RequestedAt:  req.RequestedAt.UTC().Format(time.RFC3339Nano),
		FinishedAt:   finishedAtOrNil(req.FinishedAt),
		InvocationID: req.InvocationID,
		TaskID:       req.TaskID,
		Error:        req.Error,
		AccountID:    req.AccountID,
	})
}

// finishedAtOrNil converts a non-nil *time.Time to its RFC3339Nano
// UTC string; nil → nil pointer so the JSON shape uses omitempty
// to drop the field (FireCronRequestResponse declares `json:"finished_at,omitempty"`).
func finishedAtOrNil(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339Nano)
	return &s
}
