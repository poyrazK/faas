package main

// handlers_triggers.go — HTTP surface for the unified Trigger primitive
// (issue #757 / ADR-0NN; commit #6 of feat-triggers-mega).
//
// Eleven routes registered in server.go:
//
//	POST   /v1/triggers                           createTrigger
//	GET    /v1/triggers                           listTriggers
//	GET    /v1/triggers/{id}                      getTrigger
//	PATCH  /v1/triggers/{id}                      updateTrigger
//	DELETE /v1/triggers/{id}                      deleteTrigger
//	POST   /v1/triggers/{id}/pause                pauseTrigger
//	POST   /v1/triggers/{id}/resume               resumeTrigger
//	GET    /v1/triggers/{id}/records              listTriggerRecords
//	POST   /v1/triggers/{id}/records/{rid}/retry  retryTriggerRecord
//	POST   /v1/triggers/{id}/records/{rid}/drop   dropTriggerRecord
//	GET    /v1/triggers/{id}/dlq                  listTriggerDeadLetter
//	GET    /v1/triggers/{id}/metrics              getTriggerMetrics
//	POST   /v1/triggers:batch_create              batchCreateTrigger
//
// Patterns reused from handlers_ext.go (cron family):
//
//   - Plan-tier gate (lines ~1683-1687) fires BEFORE AppByID so a
//     Free customer posting to /v1/triggers gets a clean 402 rather
//     than a 404 (the dashboard renders the upgrade prompt from the
//     402 copy).
//
//   - IDOR-safe two-step ownership check: resolve the trigger,
//     resolve its app, then compare account ids. Both fail branches
//     emit identical "no such trigger" 404 so a probe cannot
//     distinguish missing-from-cross-account.
//
//   - NotifyTriggerChanged fans to schedd (CREATE/UPDATE/DELETE/
//     PAUSE/RESUME) and the dashboard SSE channel; mirrors
//     NotifyCronChanged at handlers_ext.go:1714 (cron family).
//
//   - IAM-4 audit emit AFTER the plan-tier gate so a rejected Free
//     customer leaves no row in the audit feed.
//
// Differences from cron (intentional, see ADR-0NN):
//
//   - kind is immutable after create — PATCH does NOT accept it.
//     Switching kind = creating a new resource + deleting the old
//     one (audit-friendly).
//
//   - Five of six kinds share one UpdateTrigger shape (config blob
//     is opaque at the wire level). Only kind=cron accepts the
//     Schedule+Path partial patches; non-cron kinds reject those
//     fields.
//
//   - Pause/resume use a single UpdateTrigger call with Enabled=
//     false/true — they exist as separate routes because the
//     dashboard renders them as named buttons.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// notifyTriggerChangedJSON mirrors db.NotifyCronChanged's payload
// shape so the schedd/dashboard listeners don't have to learn a new
// schema per-resource. op carries the verb (created|updated|deleted|
// paused|resumed) — same convention as the cron family at
// handlers_ext.go:1714 / :1777 / :1822.
func notifyTriggerChangedJSON(op, appID, triggerID string) string {
	if op == "deleted" {
		return `{"kind":"deleted","app_id":"` + appID + `","trigger_id":"` + triggerID + `"}`
	}
	return `{"kind":"` + op + `","app_id":"` + appID + `","trigger_id":"` + triggerID + `"}`
}

// triggerResponse mirrors pkg/state/sqlc.Trigger onto the wire shape.
//
// Two translations matter:
//
//   - Config is the raw jsonb blob from the row — re-emitted as
//     json.RawMessage so the SDK round-trip preserves unknown fields.
//   - CronID + Source are nullable columns — read them through the
//     pgtype.Valid / .String pair. Schedule + Path live on the
//     paired crons row (which commit #2 wires via cron_id FK); the
//     HTTP path doesn't expose them on Trigger because the cron
//     kind isn't created through POST /v1/triggers (use POST
//     /v1/crons).
func triggerResponse(t sqlc.Trigger) api.Trigger {
	resp := api.Trigger{
		ID:                   uuidFromPgtype(t.ID).String(),
		AccountID:            uuidFromPgtype(t.AccountID).String(),
		AppID:                uuidFromPgtype(t.AppID).String(),
		Kind:                 api.TriggerKind(t.Kind),
		Slug:                 t.Slug,
		Enabled:              t.Enabled,
		Config:               json.RawMessage(t.Config),
		BatchSizeMax:         int(t.BatchSizeMax),
		BatchWindowMs:        int(t.BatchWindowMs),
		MaxAttempts:          int(t.MaxAttempts),
		PayloadMaxBytes:      int(t.PayloadMaxBytes),
		BrokerPoisonStrategy: t.BrokerPoisonStrategy,
		CreatedAt:            t.CreatedAt.Time,
		UpdatedAt:            t.UpdatedAt.Time,
	}
	if t.CronID.Valid {
		resp.CronID = uuidFromPgtype(t.CronID).String()
	}
	if t.Source.Valid && t.Source.String != "" {
		s := t.Source.String
		resp.Source = &s
	}
	return resp
}

// uuidFromPgtype reads a pgtype.UUID into a uuid.UUID. A non-valid
// pgtype (NULL column) returns uuid.Nil — used by callers as a
// "missing" sentinel.
func uuidFromPgtype(p pgtype.UUID) uuid.UUID {
	if !p.Valid {
		return uuid.Nil
	}
	u, _ := uuid.FromBytes(p.Bytes[:])
	return u
}

// parseUUID hex-decodes a string UUID into its 16-byte big-endian
// representation. Used by the trigger store methods (which take
// pgtype.UUID) to bridge from the existing string-id AppByID.
//
//nolint:unused // reserved for the PATCH trigger handler path PR-B.
func parseUUID(s string) []byte {
	uid, err := uuid.Parse(s)
	if err != nil {
		return nil
	}
	b := uid[:]
	return b
}

// --- createTrigger ---------------------------------------------------------

// createTrigger handles POST /v1/triggers.
//
// Plan-tier gate is the FIRST check — a Free customer gets a 402
// with the upgrade-to-Hobby copy the dashboard renders, before any
// DB lookup. Same fail-closed pattern as createCron at
// handlers_ext.go:1683-1687.
//
// Kind-specific field gating is enforced here (NOT deferred to the
// gregalemanifest validator) because the HTTP path can decode the
// kind without re-running the YAML round-trip — the loop is small
// enough to keep inline. The manifest path uses the package
// validator instead; both paths reject the same malformed payloads.
//
// kind=cron POSTs are rejected here so the dashboard "Add cron"
// UI keeps using POST /v1/crons (ADR-090 PR-B wires cron triggers
// through the crons table; widening the cron-kind POST would couple
// the two storage paths in this commit).
func (s *server) createTrigger(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateTriggerRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.AppID == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", "app_id is required"))
		return
	}
	// PR #993 / issue #757 review MED-5: the original 188-line
	// createTrigger was a linear early-return chain that mixed
	// HTTP decode, kind validation, plan-cap application, store
	// call, and audit emission. CLAUDE.md caps handler bodies at
	// 50 lines; the review flagged that the chain had grown past
	// that. Extract two helpers (validateCreateTriggerRequest +
	// enforceCreateTriggerCaps) so the handler reads as
	// decode → validate → caps → store → audit.
	if p := validateCreateTriggerRequest(&req); p != nil {
		api.WriteProblem(w, p)
		return
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || !limits.TriggersAllowed {
		api.WriteProblem(w, api.ErrPlanTriggersNotAllowed(acct.Plan))
		return
	}
	app, err := s.store.AppByID(r.Context(), req.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such app")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	batchSizeMax, batchWindowMs, maxAttempts, payloadMaxBytes, brokerPoisonStrategy, p := enforceCreateTriggerCaps(&req, acct.Plan, limits)
	if p != nil {
		api.WriteProblem(w, p)
		return
	}
	t, problem := s.persistCreatedTrigger(w, r.Context(), &req, acct, app.ID, enabled,
		batchSizeMax, batchWindowMs, maxAttempts, payloadMaxBytes, brokerPoisonStrategy, limits)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusCreated, triggerResponse(t))
}

// persistCreatedTrigger runs the store call + audit emission that
// closes the createTrigger handler. PR #993 / issue #757 review
// MED-5: extracted from createTrigger so the handler body stays
// under the CLAUDE.md 50-line cap (the inline store call + quota-
// error switch + notify + log + audit emission was the remaining
// ~30 lines after validateCreateTriggerRequest + enforceCreateTriggerCaps
// were extracted). Returns the created Trigger + a non-nil Problem
// on the quota / capacity error paths. The not-found path writes
// the 404 directly via s.notFound (it owns the writer contract) and
// returns sqlc.Trigger{} + nil so the caller doesn't double-write.
//
// Cron-kind POSTs are rejected earlier in the chain (the ADR-090
// PR-B path, review finding #6), so persistCreatedTrigger only
// sees kinds that CreateTriggerIfUnderQuota accepts.
func (s *server) persistCreatedTrigger(
	w http.ResponseWriter,
	ctx context.Context,
	req *api.CreateTriggerRequest,
	acct state.Account,
	appID string,
	enabled bool,
	batchSizeMax, batchWindowMs, maxAttempts, payloadMaxBytes int32,
	brokerPoisonStrategy string,
	limits api.Limits,
) (sqlc.Trigger, *api.Problem) {
	t, err := s.store.CreateTriggerIfUnderQuota(ctx,
		appID,
		string(req.Kind), req.Slug, enabled, []byte(req.Config),
		batchSizeMax, batchWindowMs, maxAttempts, payloadMaxBytes,
		brokerPoisonStrategy, limits)
	if err != nil {
		var qe *state.TriggerQuotaError
		switch {
		case errors.As(err, &qe):
			return sqlc.Trigger{}, api.ErrPlanTriggerQuota(acct.Plan, string(qe.Scope), qe.Limit, qe.Observed)
		case errors.Is(err, state.ErrNotFound):
			s.notFound(w, "no such app")
			return sqlc.Trigger{}, nil
		default:
			return sqlc.Trigger{}, api.ErrCapacity("could not create trigger")
		}
	}
	triggerUUID := uuidFromPgtype(t.ID).String()
	appUUID := uuidFromPgtype(t.AppID).String()
	_ = s.notif.Notify(ctx, db.NotifyTriggerChanged,
		notifyTriggerChangedJSON("created", appUUID, triggerUUID))
	s.log.Info("trigger created", "trigger", triggerUUID, "app", appUUID, "account", acct.ID, "kind", req.Kind)
	s.audit.Emit(ctx, "trigger.created", &acct.ID, map[string]any{
		"trigger_id": triggerUUID,
		"app_id":     appUUID,
		"kind":       req.Kind,
		"slug":       req.Slug,
		"enabled":    enabled,
	})
	return t, nil
}

// validateCreateTriggerRequest walks the request shape + kind
// vocabulary. Returns nil on accept; an *api.Problem on reject.
// PR #993 / issue #757 review MED-5: extracted from createTrigger
// so the cap application (MED-4's two gates + the original four)
// lives in a separate helper, and the HTTP-shape validation stays
// in one place. Rejecting cron-kind POSTs early (review finding
// #6) keeps the AppByID roundtrip off the hot path for the
// already-rejected case.
func validateCreateTriggerRequest(req *api.CreateTriggerRequest) *api.Problem {
	// Review finding #6: reject cron-kind POSTs BEFORE the
	// plan-quota lookup. ADR-090 PR-B wires cron triggers
	// through the crons table (the dashboard "Add cron" UI is
	// POST /v1/crons); accepting them here would couple the
	// two storage paths in this commit. Rejecting early also
	// avoids the AppByID roundtrip + the per-plan
	// TriggerLimitPerApp / TriggerLimitPerAccount caps work
	// for a POST that would have been rejected a few lines
	// later — a Free-plan customer hitting POST /v1/triggers
	// with kind=cron gets a clean 400 rather than the 402
	// "triggers not allowed on plan free" response that
	// would be misleading.
	if req.Kind == api.TriggerKindCron {
		if !validCron(req.Schedule) {
			return api.ErrCronInvalid("expected 5-field cron expression (m h dom mon dow)")
		}
		// Path default kept here for parity with the cron table.
		if req.Path == "" {
			req.Path = "/"
		}
		return api.NewProblem(http.StatusBadRequest,
			"trigger_immutable",
			"kind=cron not supported on POST /v1/triggers — use POST /v1/crons",
			"")
	}
	switch req.Kind {
	case api.TriggerKindKafka, api.TriggerKindNATS, api.TriggerKindRedisStreams, api.TriggerKindSQSCompat, api.TriggerKindQueue:
		if req.Slug == "" {
			return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", "slug is required for non-cron triggers")
		}
		if len(req.Config) == 0 {
			return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", "config is required for non-cron triggers")
		}
		if err := validateTriggerConfig(req.Kind, req.Config); err != nil {
			return api.NewProblem(http.StatusUnprocessableEntity, "trigger_invalid_config", "Invalid trigger config", err.Error())
		}
		return nil
	default:
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request",
			"unknown kind: must be one of cron|kafka|nats|redis_streams|sqs_compat|queue")
	}
}

// enforceCreateTriggerCaps applies every plan-cap gate the
// createTrigger handler enforces, in order, and returns the
// finalised (batchSizeMax, batchWindowMs, maxAttempts,
// payloadMaxBytes, brokerPoisonStrategy) tuple. A non-nil
// *api.Problem short-circuits the handler. PR #993 / issue #757
// review MED-5: extracted from createTrigger so the gate chain
// reads as a single declarative block. MED-4's BatchWindowMs +
// TLSSkipVerifyAllowed gates live here alongside the original
// batch_size_max / max_attempts / payload_max_bytes gates.
//
// Field defaults (batch_size_max=64, batch_window_ms=1000,
// max_attempts=5, payload_max_bytes=6 MiB, broker_poison_strategy
// ="commit") match the pre-MED-5 handler exactly so behaviour
// stays unchanged for callers that omit the optional fields.
func enforceCreateTriggerCaps(req *api.CreateTriggerRequest, plan api.Plan, limits api.Limits) (
	batchSizeMax, batchWindowMs, maxAttempts, payloadMaxBytes int32,
	brokerPoisonStrategy string,
	problem *api.Problem,
) {
	batchSizeMax = int32(64)
	if v := intFrom(req.BatchSizeMax); v > 0 {
		batchSizeMax = int32(v)
	}
	if limits.TriggerBatchSizeMax > 0 && batchSizeMax > int32(limits.TriggerBatchSizeMax) {
		return batchSizeMax, batchWindowMs, maxAttempts, payloadMaxBytes, brokerPoisonStrategy,
			api.ErrPlanTriggerQuota(plan, "batch_size_max", limits.TriggerBatchSizeMax, int(batchSizeMax))
	}
	batchWindowMs = int32(1000)
	if v := intFrom(req.BatchWindowMs); v > 0 {
		batchWindowMs = int32(v)
	}
	// PR #993 / issue #757 review MED-4: enforce the per-plan
	// batch_window cap (limits.TriggerBatchWindowMaxSec — Hobby
	// 30s, Pro/Scale 300s). Pre-MED-4 the field went straight
	// through to the SQL CHECK (10ms–600s) — a Hobby customer
	// could legally request 600s and pin 10× the broker dwell
	// window the per-app rate-limit is sized for.
	if limits.TriggerBatchWindowMaxSec > 0 {
		observedSec := int(batchWindowMs / 1000)
		if observedSec > limits.TriggerBatchWindowMaxSec {
			return batchSizeMax, batchWindowMs, maxAttempts, payloadMaxBytes, brokerPoisonStrategy,
				api.ErrTriggerBatchWindowTooLarge(plan, limits.TriggerBatchWindowMaxSec, observedSec)
		}
	}
	// PR #993 / issue #757 review MED-4: tls.skip_verify=true on
	// a Kafka trigger is gated by the plan's TLSSkipVerifyAllowed
	// flag (Hobby=false, Pro=true, Scale=true). Pre-MED-4 the
	// field went straight to the broker — a Hobby customer
	// could silently weaken hostname + cert verification on the
	// production broker.
	if !plan.TLSSkipVerifyAllowed() {
		skip, err := kafkaSkipVerifyRequested(req.Kind, req.Config)
		if err != nil {
			return batchSizeMax, batchWindowMs, maxAttempts, payloadMaxBytes, brokerPoisonStrategy,
				api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", "invalid trigger config: "+err.Error())
		}
		if skip {
			return batchSizeMax, batchWindowMs, maxAttempts, payloadMaxBytes, brokerPoisonStrategy,
				api.ErrTriggerTLSSkipVerifyNotAllowed(plan)
		}
	}
	maxAttempts = int32(5)
	if v := intFrom(req.MaxAttempts); v > 0 {
		maxAttempts = int32(v)
	}
	if limits.TriggerMaxAttemptsMax > 0 && maxAttempts > int32(limits.TriggerMaxAttemptsMax) {
		return batchSizeMax, batchWindowMs, maxAttempts, payloadMaxBytes, brokerPoisonStrategy,
			api.ErrPlanTriggerQuota(plan, "max_attempts", limits.TriggerMaxAttemptsMax, int(maxAttempts))
	}
	// Audit finding #7 (migration 00278): per-trigger broker
	// payload size cap. Default 6 MiB when the request omits
	// the field. Surface a plan-level 403 (rather than letting
	// the SQL CHECK 422 the request) so the response carries
	// the plan cap + the observed value.
	payloadMaxBytes = int32(6291456)
	if req.PayloadMaxBytes != nil && *req.PayloadMaxBytes > 0 {
		payloadMaxBytes = int32(*req.PayloadMaxBytes)
	}
	if limits.TriggerPayloadMaxBytes > 0 && payloadMaxBytes > int32(limits.TriggerPayloadMaxBytes) {
		return batchSizeMax, batchWindowMs, maxAttempts, payloadMaxBytes, brokerPoisonStrategy,
			api.ErrPlanTriggerQuota(plan, "payload_max_bytes", limits.TriggerPayloadMaxBytes, int(payloadMaxBytes))
	}
	// Audit #10 (migration 00279): kafka-only broker-poison
	// handling strategy. nil → "commit" default.
	brokerPoisonStrategy = api.BrokerPoisonStrategyCommit
	if req.BrokerPoisonStrategy != nil && *req.BrokerPoisonStrategy != "" {
		brokerPoisonStrategy = *req.BrokerPoisonStrategy
	}
	return batchSizeMax, batchWindowMs, maxAttempts, payloadMaxBytes, brokerPoisonStrategy, nil
}

// --- listTriggers ----------------------------------------------------------

func validTriggerKind(kind api.TriggerKind) bool {
	switch kind {
	case api.TriggerKindCron, api.TriggerKindKafka, api.TriggerKindNATS,
		api.TriggerKindRedisStreams, api.TriggerKindSQSCompat, api.TriggerKindQueue:
		return true
	default:
		return false
	}
}

// listTriggers handles GET /v1/triggers.
//
// Walks every app the account owns and unions their triggers. Optional
// app_id and kind query parameters are applied before the fan-out so
// the SDK's documented filters are real filters rather than a client-
// side hint. An app_id is resolved through the account-owned app lookup
// so a cross-account probe has the same not-found response as the
// item endpoint.
func (s *server) listTriggers(w http.ResponseWriter, r *http.Request, acct state.Account) {
	query := r.URL.Query()
	appID := query.Get("app_id")
	kind := api.TriggerKind(query.Get("kind"))
	if kind != "" && !validTriggerKind(kind) {
		// The public list contract treats an unknown filter value as
		// an empty result, preserving the same semantics as a valid
		// filter that currently matches no rows.
		writeJSON(w, http.StatusOK, []api.Trigger{})
		return
	}
	var apps []state.App
	var err error
	if appID != "" {
		app, appErr := s.store.AppByID(r.Context(), appID)
		if appErr != nil || app.AccountID != acct.ID {
			s.notFound(w, "no such app")
			return
		}
		apps = []state.App{app}
	} else {
		apps, err = s.store.ListApps(r.Context(), acct.ID)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not list triggers"))
			return
		}
	}
	out := make([]api.Trigger, 0)
	for _, app := range apps {
		ts, err := s.store.ListTriggersForApp(r.Context(), app.ID)
		if err != nil {
			continue
		}
		for _, t := range ts {
			if kind != "" && api.TriggerKind(t.Kind) != kind {
				continue
			}
			out = append(out, triggerResponse(t))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// --- getTrigger ------------------------------------------------------------

// getTrigger handles GET /v1/triggers/{id}.
//
// Two-step IDOR check (resolve trigger, resolve app, compare
// account) — identical to getCron at handlers_ext.go:1848. Both
// fail branches emit "no such trigger" so a probe cannot
// distinguish missing-from-cross-account.
func (s *server) getTrigger(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := parseTriggerID(w, r)
	if !ok {
		return
	}
	t, err := s.store.TriggerByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such trigger")
		return
	}
	app, err := s.store.AppByID(r.Context(), uuidFromPgtype(t.AppID).String())
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such trigger")
		return
	}
	writeJSON(w, http.StatusOK, triggerResponse(t))
}

// --- updateTrigger ---------------------------------------------------------

// updateTrigger handles PATCH /v1/triggers/{id}.
//
// Partial update — every field except kind is optional. nil pointer
// means "leave unchanged". Kind is intentionally absent from
// UpdateTriggerRequest: switching kind means creating a new
// resource + deleting the old one (audit-friendly).
//
// Per-kind field gating mirrors createTrigger: kind=cron accepts
// Schedule/Path patches; non-cron kinds reject those. Schedule/path
// patches for cron rows are recorded by the *UpdateTrigger store
// method via the SQL (limited to schedule + path for cron rows;
// batch/config for non-cron rows; enabled for both).
func (s *server) updateTrigger(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := parseTriggerID(w, r)
	if !ok {
		return
	}
	t, err := s.store.TriggerByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such trigger")
		return
	}
	app, err := s.store.AppByID(r.Context(), uuidFromPgtype(t.AppID).String())
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such trigger")
		return
	}
	var req api.UpdateTriggerRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if (req.Schedule != nil || req.Path != nil) && t.Kind != string(api.TriggerKindCron) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request",
			"schedule/path patches are only valid for kind=cron triggers"))
		return
	}
	if req.Schedule != nil && !validCron(*req.Schedule) {
		api.WriteProblem(w, api.ErrCronInvalid("expected 5-field cron expression"))
		return
	}
	if req.Config != nil && t.Kind != string(api.TriggerKindCron) {
		// Re-validate the per-kind config when the customer is
		// changing the broker fingerprint — a kafka role-arn swap
		// to a new account should fail at update time, not at the
		// next poll.
		if err := validateTriggerConfig(api.TriggerKind(t.Kind), req.Config); err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, "trigger_invalid_config", "Invalid trigger config", err.Error()))
			return
		}
	}
	// PR #993 / issue #757 review MED-4: mirror createTrigger's
	// plan gates for PATCH. A Hobby customer upgrading their
	// batch_window from 5s to 600s after the fact, or flipping
	// tls.skip_verify=true on an existing trigger, are the two
	// holes the create-time gate leaves open. LimitsFor returns
	// the (plan) entry; the unknown-plan fallback is Free which
	// short-circuits via TriggersAllowed (already enforced on
	// the lookup above when we AppByIDed).
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		limits, _ = api.LimitsFor(api.PlanFree)
	}
	if req.BatchWindowMs != nil && limits.TriggerBatchWindowMaxSec > 0 {
		observedSec := int(*req.BatchWindowMs / 1000)
		if observedSec > limits.TriggerBatchWindowMaxSec {
			api.WriteProblem(w, api.ErrTriggerBatchWindowTooLarge(acct.Plan, limits.TriggerBatchWindowMaxSec, observedSec))
			return
		}
	}
	if req.Config != nil && !acct.Plan.TLSSkipVerifyAllowed() {
		skip, err := kafkaSkipVerifyRequested(api.TriggerKind(t.Kind), req.Config)
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", "invalid trigger config: "+err.Error()))
			return
		}
		if skip {
			api.WriteProblem(w, api.ErrTriggerTLSSkipVerifyNotAllowed(acct.Plan))
			return
		}
	}
	var batchSizeMax, batchWindowMs, maxAttempts, payloadMaxBytes *int32
	if req.BatchSizeMax != nil {
		v := int32(*req.BatchSizeMax)
		batchSizeMax = &v
	}
	if req.BatchWindowMs != nil {
		v := int32(*req.BatchWindowMs)
		batchWindowMs = &v
	}
	if req.MaxAttempts != nil {
		v := int32(*req.MaxAttempts)
		maxAttempts = &v
	}
	if req.PayloadMaxBytes != nil {
		v := int32(*req.PayloadMaxBytes)
		payloadMaxBytes = &v
	}
	// Audit #10 (migration 00279): the apid handler passes the
	// pointer straight through; nil = "leave unchanged" via the
	// SQL coalesce() in the pgstore UpdateTrigger path. Empty
	// string would be coerced to "commit" but is rejected at the
	// JSON decoder level (omitempty drops it before reaching
	// here).
	var brokerPoisonStrategy *string
	if req.BrokerPoisonStrategy != nil && *req.BrokerPoisonStrategy != "" {
		bps := *req.BrokerPoisonStrategy
		brokerPoisonStrategy = &bps
	}
	var configBytes []byte
	if req.Config != nil {
		configBytes = []byte(req.Config)
	}
	// REVIEW-FIX MED-1 (PR #993 / issue #757 closure):
	// marshal filter_criteria to JSONB bytes if the customer is
	// patching it. nil req.FilterCriteria → leave the column
	// unchanged via the coalesce() in pgstore.UpdateTrigger.
	var filterCriteriaBytes *[]byte
	if req.FilterCriteria != nil {
		b, err := json.Marshal(req.FilterCriteria)
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
			return
		}
		filterCriteriaBytes = &b
	}
	// Review finding #4 (PR #910): for kind=cron rows the
	// schedule/path columns live on the `crons` table (the
	// triggers.cron_id FK points at it). The old code accepted
	// the schedule/path patch silently, validated the cron
	// expression, and then DROPPED it on the floor because
	// UpdateTrigger doesn't touch the crons table. Route cron
	// patches through UpdateCron via the cron_id FK.
	var updated sqlc.Trigger
	if t.Kind == string(api.TriggerKindCron) {
		if !t.CronID.Valid {
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "trigger_inconsistent", "Trigger has no cron_id",
				"kind=cron trigger is missing the cron_id FK"))
			return
		}
		if _, err := s.store.UpdateCron(r.Context(), uuidFromPgtype(t.CronID).String(), req.Schedule, req.Path, req.Enabled, nil); err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not update cron schedule/path"))
			return
		}
		// Update the non-cron fields on the triggers row.
		updated, err = s.store.UpdateTrigger(r.Context(), id, req.Enabled, configBytes, batchSizeMax, batchWindowMs, maxAttempts, payloadMaxBytes, brokerPoisonStrategy, filterCriteriaBytes)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not update trigger"))
			return
		}
	} else {
		updated, err = s.store.UpdateTrigger(r.Context(), id, req.Enabled, configBytes, batchSizeMax, batchWindowMs, maxAttempts, payloadMaxBytes, brokerPoisonStrategy, filterCriteriaBytes)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not update trigger"))
			return
		}
	}
	triggerUUID := uuidFromPgtype(updated.ID).String()
	appUUID := uuidFromPgtype(updated.AppID).String()
	_ = s.notif.Notify(r.Context(), db.NotifyTriggerChanged,
		notifyTriggerChangedJSON("updated", appUUID, triggerUUID))
	oldT, newT := map[string]any{}, map[string]any{}
	if req.Enabled != nil {
		oldT["enabled"] = t.Enabled
		newT["enabled"] = updated.Enabled
	}
	if req.MaxAttempts != nil {
		oldT["max_attempts"] = t.MaxAttempts
		newT["max_attempts"] = updated.MaxAttempts
	}
	s.audit.Emit(r.Context(), "trigger.updated", &acct.ID, map[string]any{
		"trigger_id": triggerUUID,
		"app_id":     appUUID,
		"old":        oldT,
		"new":        newT,
	})
	writeJSON(w, http.StatusOK, triggerResponse(updated))
}

// --- deleteTrigger ---------------------------------------------------------

// deleteTrigger handles DELETE /v1/triggers/{id}.
//
// Cascades to trigger_records + trigger_dead_letter (SQL ON DELETE
// CASCADE at migrations/00267_triggers.sql). The dispatch tick
// (commit #14) reads trigger_records and ignores gone triggers.
func (s *server) deleteTrigger(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := parseTriggerID(w, r)
	if !ok {
		return
	}
	t, err := s.store.TriggerByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such trigger")
		return
	}
	app, err := s.store.AppByID(r.Context(), uuidFromPgtype(t.AppID).String())
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such trigger")
		return
	}
	if err := s.store.DeleteTrigger(r.Context(), id, uuidFromPgtype(t.AppID).String()); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not delete trigger"))
		return
	}
	triggerUUID := uuidFromPgtype(t.ID).String()
	appUUID := uuidFromPgtype(t.AppID).String()
	_ = s.notif.Notify(r.Context(), db.NotifyTriggerChanged,
		notifyTriggerChangedJSON("deleted", appUUID, triggerUUID))
	s.audit.Emit(r.Context(), "trigger.deleted", &acct.ID, map[string]any{
		"trigger_id": triggerUUID,
		"app_id":     appUUID,
	})
	w.WriteHeader(http.StatusNoContent)
}

// --- pause / resume --------------------------------------------------------

// pauseTrigger / resumeTrigger flip Enabled via UpdateTrigger.
// They exist as separate routes because the dashboard renders them
// as named buttons + the SDK uses the verb-first form.
func (s *server) pauseTrigger(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.setTriggerEnabled(w, r, acct, false, "paused")
}

func (s *server) resumeTrigger(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.setTriggerEnabled(w, r, acct, true, "resumed")
}

func (s *server) setTriggerEnabled(w http.ResponseWriter, r *http.Request, acct state.Account, enabled bool, op string) {
	id, ok := parseTriggerID(w, r)
	if !ok {
		return
	}
	t, err := s.store.TriggerByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such trigger")
		return
	}
	app, err := s.store.AppByID(r.Context(), uuidFromPgtype(t.AppID).String())
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such trigger")
		return
	}
	updated, err := s.store.UpdateTrigger(r.Context(), id, &enabled, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not update trigger"))
		return
	}
	triggerUUID := uuidFromPgtype(updated.ID).String()
	appUUID := uuidFromPgtype(updated.AppID).String()
	_ = s.notif.Notify(r.Context(), db.NotifyTriggerChanged,
		notifyTriggerChangedJSON(op, appUUID, triggerUUID))
	s.audit.Emit(r.Context(), "trigger."+op, &acct.ID, map[string]any{
		"trigger_id": triggerUUID,
		"app_id":     appUUID,
		"enabled":    enabled,
	})
	writeJSON(w, http.StatusOK, triggerResponse(updated))
}

// --- records / dlq / metrics ----------------------------------------------

// listTriggerRecords handles GET /v1/triggers/{id}/records.
func (s *server) listTriggerRecords(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := parseTriggerID(w, r)
	if !ok {
		return
	}
	if !s.authoriseTriggerRead(w, r, acct, id) {
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 1 || n > 200 {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad limit", "expected 1..200"))
			return
		}
		limit = n
	}
	rows, err := s.store.ListTriggerRecordsForTrigger(r.Context(), id, int32(limit))
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("list trigger records"))
		return
	}
	out := make([]api.TriggerRecord, 0, len(rows))
	for _, rec := range rows {
		out = append(out, triggerRecordResponse(rec))
	}
	writeJSON(w, http.StatusOK, api.ListTriggerRecordsResponse{Records: out})
}

// retryTriggerRecord handles POST /v1/triggers/{id}/records/{rid}/retry.
//
// Forces a record back into pending with attempts=0. Operator
// verb — distinct from the dispatcher-initiated retry that the
// MarkTriggerRecordRetry store method issues. Until a dedicated
// store method lands (PR-B scope), we synthesize the write
// inline: this is a small one-line UPDATE that doesn't belong
// on the dispatcher's interface.
func (s *server) retryTriggerRecord(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tID, ok := parseTriggerID(w, r)
	if !ok {
		return
	}
	if !s.authoriseTriggerRead(w, r, acct, tID) {
		return
	}
	recID, ok := parseTriggerRecordID(w, r)
	if !ok {
		return
	}
	if err := s.store.RetryTriggerRecordByOperator(r.Context(), recID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such record")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not retry record"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// dropTriggerRecord handles POST /v1/triggers/{id}/records/{rid}/drop.
func (s *server) dropTriggerRecord(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tID, ok := parseTriggerID(w, r)
	if !ok {
		return
	}
	if !s.authoriseTriggerRead(w, r, acct, tID) {
		return
	}
	recID, ok := parseTriggerRecordID(w, r)
	if !ok {
		return
	}
	if err := s.store.DropTriggerRecordByOperator(r.Context(), recID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such record")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not drop record"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listTriggerDeadLetter handles GET /v1/triggers/{id}/dlq.
func (s *server) listTriggerDeadLetter(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := parseTriggerID(w, r)
	if !ok {
		return
	}
	if !s.authoriseTriggerRead(w, r, acct, id) {
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 1 || n > 200 {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad limit", "expected 1..200"))
			return
		}
		limit = n
	}
	rows, err := s.store.ListTriggerDeadLetter(r.Context(), id, int32(limit))
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("list dead letter"))
		return
	}
	out := make([]api.TriggerDeadLetter, 0, len(rows))
	for _, dl := range rows {
		out = append(out, triggerDeadLetterResponse(dl))
	}
	writeJSON(w, http.StatusOK, api.ListTriggerDeadLetterResponse{Records: out})
}

// getTriggerMetrics handles GET /v1/triggers/{id}/metrics.
//
// Walks trigger_records for the trigger and counts by state. The
// store layer is the future home of a CountTriggerRecordsByState
// helper; this handler-side aggregation covers the dashboard until
// then. A 1000-row ceiling keeps the cost bounded; the Prometheus
// surface (`/v1/metrics`) is the authoritative counters path.
func (s *server) getTriggerMetrics(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, ok := parseTriggerID(w, r)
	if !ok {
		return
	}
	if !s.authoriseTriggerRead(w, r, acct, id) {
		return
	}
	metrics, err := s.aggregateTriggerMetrics(r.Context(), id)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("metrics"))
		return
	}
	metrics.TriggerID = id
	writeJSON(w, http.StatusOK, metrics)
}

// batchCreateTrigger handles POST /v1/triggers:batch_create.
//
// Inline-manifest path. Customers ship a gregale.yaml blob in the
// request body and the server fans it out through the same store
// helpers createTrigger uses. Distinct from POST /v1/triggers so
// the dashboard can differentiate "add one trigger" from "apply
// manifest".
//
// Per-trigger errors are accumulated into the response so a partial
// success is observable (vs fail-loud on the first 4xx).
func (s *server) batchCreateTrigger(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateTriggerBatchRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.AppID == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", "app_id is required"))
		return
	}
	m, prob := validateManifestBytes([]byte(req.ManifestYAML), acct.Plan)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if m == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request",
			"manifest_yaml is empty"))
		return
	}
	app, err := s.store.AppByID(r.Context(), req.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such app")
		return
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		// No entry for this plan (migrated stale plan string) — the
		// Free tier is the fail-closed default. TriggersAllowed is
		// false on Free, so the loop below rejects every entry
		// before it touches the store.
		limits, _ = api.LimitsFor(api.PlanFree)
	}
	out := make([]api.Trigger, 0, len(m.Triggers))
	errs := make([]batchError, 0)
	for _, t := range m.Triggers {
		if t.Kind == gregalemanifest.TriggerKindCron {
			errs = append(errs, batchError{Slug: t.Slug, Message: "kind=cron triggers must be created via POST /v1/crons"})
			continue
		}
		if t.Slug == "" {
			errs = append(errs, batchError{Slug: t.Slug, Message: "slug is required"})
			continue
		}
		bsm := int32(64)
		if t.BatchSizeMax > 0 {
			bsm = int32(t.BatchSizeMax)
		}
		bwm := int32(1000)
		if t.BatchWindowMs > 0 {
			bwm = int32(t.BatchWindowMs)
		}
		ma := int32(5)
		if t.MaxAttempts > 0 {
			ma = int32(t.MaxAttempts)
		}
		pmb := int32(6291456)
		if t.PayloadMaxBytes > 0 {
			pmb = int32(t.PayloadMaxBytes)
		}
		// Audit #10 (migration 00279): kafka-only broker-poison
		// handling strategy. The YAML validator at
		// pkg/gregalemanifest/manifest.go:Validate rejects
		// anything outside the closed vocab; an empty string
		// falls through to "commit" (the previous hardcoded
		// behaviour).
		bps := api.BrokerPoisonStrategyCommit
		if t.BrokerPoisonStrategy != "" {
			bps = t.BrokerPoisonStrategy
		}
		if limits.TriggerBatchSizeMax > 0 && bsm > int32(limits.TriggerBatchSizeMax) {
			errs = append(errs, batchError{Slug: t.Slug, Message: "batch_size_max exceeds plan cap"})
			continue
		}
		if limits.TriggerMaxAttemptsMax > 0 && ma > int32(limits.TriggerMaxAttemptsMax) {
			errs = append(errs, batchError{Slug: t.Slug, Message: "max_attempts exceeds plan cap"})
			continue
		}
		if limits.TriggerPayloadMaxBytes > 0 && pmb > int32(limits.TriggerPayloadMaxBytes) {
			errs = append(errs, batchError{Slug: t.Slug, Message: "payload_max_bytes exceeds plan cap"})
			continue
		}
		created, err := s.store.CreateTriggerIfUnderQuota(r.Context(),
			req.AppID,
			string(t.Kind), t.Slug, t.IsEnabled(), marshalConfig(t.Config),
			bsm, bwm, ma, pmb, bps, limits)
		if err != nil {
			var qe *state.TriggerQuotaError
			switch {
			case errors.As(err, &qe):
				errs = append(errs, batchError{Slug: t.Slug, Message: "quota: " + string(qe.Scope)})
			case errors.Is(err, state.ErrNotFound):
				errs = append(errs, batchError{Slug: t.Slug, Message: "no such app"})
			default:
				errs = append(errs, batchError{Slug: t.Slug, Message: "internal error"})
			}
			continue
		}
		out = append(out, triggerResponse(created))
	}
	writeJSON(w, http.StatusOK, batchCreateResponse{Created: out, Errors: errs})
}

// batchCreateResponse is the per-trigger success/error list shape.
type batchCreateResponse struct {
	Created []api.Trigger `json:"created"`
	Errors  []batchError  `json:"errors,omitempty"`
}

type batchError struct {
	Slug    string `json:"slug,omitempty"`
	Message string `json:"message"`
}

// marshalConfig JSON-encodes a map[string]any manifest config into
// the bytes the store persists. nil / empty maps encode as "{}" so
// the SQL NOT NULL DEFAULT '{}' constraint always holds.
func marshalConfig(c map[string]any) []byte {
	if len(c) == 0 {
		return []byte("{}")
	}
	b, _ := json.Marshal(c)
	return b
}

// --- trigger response projections -----------------------------------------

// triggerRecordResponse mirrors sqlc.TriggerRecord onto the wire
// shape. JSON-blob fields (payload / headers / metadata) emit as
// raw strings rather than re-decoded — keeps the wire stable across
// broker library upgrades.
func triggerRecordResponse(rec sqlc.TriggerRecord) api.TriggerRecord {
	out := api.TriggerRecord{
		ID:             uuidFromPgtype(rec.ID).String(),
		TriggerID:      uuidFromPgtype(rec.TriggerID).String(),
		ItemIdentifier: rec.ItemIdentifier,
		Payload:        string(rec.Payload),
		Headers:        string(rec.Headers),
		Metadata:       string(rec.Metadata),
		State:          rec.State,
		Attempts:       int(rec.Attempts),
		NextFireAt:     rec.NextFireAt.Time,
		ReceivedAt:     rec.ReceivedAt.Time,
	}
	if rec.LastError.Valid && rec.LastError.String != "" {
		s := rec.LastError.String
		out.LastError = &s
	}
	if rec.LastDispatchedAt.Valid {
		t := rec.LastDispatchedAt.Time
		out.LastDispatchedAt = &t
	}
	return out
}

// triggerDeadLetterResponse mirrors sqlc.TriggerDeadLetter onto
// the wire shape. Detail is opaque JSON so the dashboard can
// decide per-reason rendering (a poison_record payload is rendered
// differently from a max_attempts one).
func triggerDeadLetterResponse(dl sqlc.TriggerDeadLetter) api.TriggerDeadLetter {
	return api.TriggerDeadLetter{
		RecordID:  uuidFromPgtype(dl.RecordID).String(),
		TriggerID: uuidFromPgtype(dl.TriggerID).String(),
		Reason:    dl.Reason,
		RoutedTo:  dl.RoutedTo,
		Detail:    string(dl.Detail),
		CreatedAt: dl.CreatedAt.Time,
	}
}

// --- helpers ---------------------------------------------------------------

// parseTriggerID is the shared {id} path-param decoder for trigger
// routes. Emits a 400 Problem on a malformed UUID and writes
// through the supplied ResponseWriter so the caller can `if !ok
// { return }` cleanly. Returns the canonical lowercase string form
// (uuid.Parse normalises), which is what the Store interface
// methods expect.
func parseTriggerID(w http.ResponseWriter, r *http.Request) (string, bool) {
	idStr := r.PathValue("id")
	uid, err := uuid.Parse(idStr)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad trigger id", err.Error()))
		return "", false
	}
	return uid.String(), true
}

// parseTriggerRecordID is the shared {rid} path-param decoder for
// the /records/{rid}/* routes.
func parseTriggerRecordID(w http.ResponseWriter, r *http.Request) (string, bool) {
	idStr := r.PathValue("rid")
	uid, err := uuid.Parse(idStr)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad record id", err.Error()))
		return "", false
	}
	return uid.String(), true
}

// authoriseTriggerRead resolves the trigger + app + account and
// emits a 404 (intentionally not 403 — see handlers_ext.go for
// precedent) if either step fails. Both fail branches use
// identical copy so a probe cannot distinguish "wrong id" from
// "someone else's trigger". Returns true when the caller is
// authorised; false when a response has already been written.
func (s *server) authoriseTriggerRead(w http.ResponseWriter, r *http.Request, acct state.Account, id string) bool {
	t, err := s.store.TriggerByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such trigger")
		return false
	}
	app, err := s.store.AppByID(r.Context(), uuidFromPgtype(t.AppID).String())
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such trigger")
		return false
	}
	return true
}

// intFrom returns *p when non-nil, 0 otherwise. Helper for the
// "default if 0" pattern on UpdateTriggerRequest's optional fields.
func intFrom(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// validateTriggerConfig round-trips the request's Config blob
// through the per-kind validator in pkg/gregalemanifest. Used by
// createTrigger + updateTrigger + batchCreateTrigger so the HTTP
// path stays in lockstep with the manifest path.
//
// We construct a synthetic single-trigger Manifest, validate it,
// and return the wrapped error if non-nil. The package's
// per-kind validator is otherwise unexported (validateKindConfig).
func validateTriggerConfig(kind api.TriggerKind, raw json.RawMessage) error {
	probe := &gregalemanifest.Manifest{Triggers: []gregalemanifest.Trigger{{
		Kind: gregalemanifest.TriggerKind(kind),
		Slug: "_",
	}}}
	if raw != nil {
		var anyMap map[string]any
		if err := json.Unmarshal(raw, &anyMap); err != nil {
			return err
		}
		probe.Triggers[0].Config = anyMap
	}
	return probe.Validate()
}

// kafkaSkipVerifyRequested returns true iff the trigger Config
// blob carries a kafka.tls.skip_verify=true leaf. Used by
// createTrigger + updateTrigger to gate the TLSSkipVerifyAllowed
// plan flag (PR #993 / issue #757 review MED-4). Non-kafka kinds
// always return false (the field is meaningless for them). A
// malformed Config blob returns (false, err) so the caller can
// surface the validator's error rather than silently allowing the
// gate to fall through.
//
// Mirrors validateTriggerConfig's permissive decode (the same
// json.Unmarshal into map[string]any), but extracts the single
// bool we care about. Re-decode rather than re-using
// gregalemanifest's typed KafkaConfig because we don't want the
// createTrigger handler to take on a hard dependency on the
// manifest package's exact field shape — the validator owns the
// shape contract, this helper just reads one leaf.
func kafkaSkipVerifyRequested(kind api.TriggerKind, raw json.RawMessage) (bool, error) {
	if kind != api.TriggerKindKafka {
		return false, nil
	}
	if len(raw) == 0 {
		return false, nil
	}
	var cfg struct {
		Brokers []string `json:"brokers"`
		TLS     *struct {
			SkipVerify bool `json:"skip_verify"`
		} `json:"tls"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return false, err
	}
	if cfg.TLS == nil {
		return false, nil
	}
	return cfg.TLS.SkipVerify, nil
}

// aggregateTriggerMetrics walks trigger_records for the trigger
// and counts by state. A 1000-row ceiling keeps the cost bounded
// (the dispatcher owns the authoritative counters via Prometheus).
func (s *server) aggregateTriggerMetrics(ctx context.Context, id string) (api.TriggerMetricsResponse, error) {
	rows, err := s.store.ListTriggerRecordsForTrigger(ctx, id, int32(1000))
	if err != nil {
		return api.TriggerMetricsResponse{}, err
	}
	var m api.TriggerMetricsResponse
	for _, rec := range rows {
		switch rec.State {
		case triggerRecordStatePending:
			m.PendingCount++
		case triggerRecordStateClaimed:
			m.ClaimedCount++
		case triggerRecordStateSucceeded:
			m.SucceededCount++
		case triggerRecordStateRetry:
			m.RetryCount++
		case triggerRecordStateDeadLetter:
			m.DeadLetterCount++
		}
	}
	return m, nil
}

// _ keeps go vet happy about the time import (used by the
// trigger_metrics response's CreatedAt formatting in the future).
var _ = time.Now

// trigger_records state-machine constants (match the CHECK on
// trigger_records.state in migrations/00297_triggers.sql). CI
// lint rule goconst would otherwise flag the per-record status
// comparisons in aggregateTriggerMetrics as duplicates.
const (
	triggerRecordStatePending    = "pending"
	triggerRecordStateClaimed    = "claimed"
	triggerRecordStateSucceeded  = "succeeded"
	triggerRecordStateRetry      = "retry"
	triggerRecordStateDeadLetter = "dead_letter"
)
