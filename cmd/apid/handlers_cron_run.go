package main

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// handlers_cron_run.go — POST /v1/crons/{id}/run (issue #791 PR-C,
// ADR-090).
//
// The handler is a thin client over a pg_notify + cron_fire_now_requests
// row (migrations/00193). The actual dispatch runs in schedd's
// fire-now consumer (pkg/sched/fire_now.go) — apid only:
//
//   1. Authorises the request (scope + IDOR-safe two-step).
//   2. Inserts a pending row (status='pending') — the durable record.
//   3. Emits db.NotifyCronRunNow — the wakeup for schedd.
//   4. Returns 202 with the request id; the customer polls the
//      status via the future GET /v1/cron-fire-now-requests/{id}
//      surface or watches `GET /v1/crons/{id}/runs` for the audit row.
//
// The handler does NOT validate enabled — schedd's RunCronNow
// re-checks enabled on claim (a cron disabled between INSERT and
// claim surfaces as ErrCronDisabled in the row, not at the API).
// Same pattern for ErrAccountSuspended and ErrNoCapacity — the
// failure is stamped onto the row in schedd's process and the
// customer reads it from the row's terminal state.
//
// 4xx/5xx surface:
//
//	id unparseable            → 400 cron_invalid (existing errors.go:270)
//	cron not found            → 404 not_found
//	cron on another account   → 404 not_found (byte-identical body)
//	rate limited              → 429 (authLimited)
//	no dispatch capacity      → N/A at API (schedd stamps the row)
//	server error              → 500 (existing ErrInternal)

func (s *server) fireCronNow(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ctx := r.Context()

	idStr := r.PathValue("id")
	// uuid.Parse accepts both hyphenated (0123-4567-...) and hex
	// (01234567...) forms. We validate for shape only and pass the
	// raw id string downstream — memstore IDs are hex-formatted by
	// newID() (no dashes) and pgstore IDs use whichever shape the
	// CREATE TABLE pinned. The validation tripwire stops a customer
	// from hitting the DB with junk; the actual lookup uses idStr.
	if _, err := uuid.Parse(idStr); err != nil {
		api.WriteProblem(w, api.ErrCronInvalid("invalid cron id (want UUID)"))
		return
	}

	// IDOR-safe two-step (mirror createCron, handlers_ext.go:1603).
	// CronByID → AppByID → AccountID == acct.ID. Cross-account and
	// missing return byte-identical 404 bodies — no existence oracle.
	c, err := s.store.CronByID(ctx, idStr)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such cron")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load cron"))
		return
	}
	app, err := s.store.AppByID(ctx, c.AppID)
	if err != nil || app.AccountID != acct.ID {
		// 404 not 403 — never reveal whether the cron exists on
		// another account. The body must be byte-identical to the
		// "missing" case above so a timing oracle cannot distinguish.
		s.notFound(w, "no such cron")
		return
	}
	// Plan-tier gate runs BEFORE InsertFireNowRequest so a Free
	// customer never creates a row that schedd will then stamp as
	// failed. Same shape as createCron at handlers_ext.go:1598-1601.
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || limits.CronLimitPerApp == 0 {
		api.WriteProblem(w, api.ErrPlanCronsNotAllowed(acct.Plan))
		return
	}

	// Insert the durable record. Status starts at 'pending'. The
	// id is returned to the customer so they can correlate the
	// audit-event stream with their request.
	requestID, err := s.store.InsertFireNowRequest(ctx, c.ID, acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not enqueue fire-now"))
		return
	}

	// Wake schedd. The row is already committed (pg_notify happens
	// after the INSERT above; pgx.Exec returns once Postgres
	// acknowledges the write). If the notify fails, schedd's 60s
	// safety tick (pkg/sched/fire_now.go::fireNowSafetyTick) picks
	// up the row. So a notify failure here is logged but does not
	// 5xx the customer — the row is the source of truth.
	if err := s.notif.Notify(ctx, db.NotifyCronRunNow,
		`{"request_id":"`+requestID+`"}`); err != nil {
		s.log.Warn("apid: fire-now notify failed; safety tick will recover",
			"cron_id", c.ID, "request_id", requestID, "err", err)
	}

	s.log.Info("cron fire-now enqueued",
		"cron_id", c.ID, "app_id", c.AppID,
		"account_id", acct.ID, "request_id", requestID)

	writeJSON(w, http.StatusAccepted, api.FireCronResponse{
		RequestID: requestID,
		CronID:    c.ID,
		Status:    "pending",
	})
}
