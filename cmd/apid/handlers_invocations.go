package main

// Move 2: customer-facing event-driven surface. This file holds the
// nine handlers that put the Move 1 backend (state.Invocation table +
// pkg/sched drain) in front of customers. The shape mirrors the
// existing apid handlers (loadApp → plan cap check → writeJSON or
// ErrPlan*) and reuses every helper from cmd/apid/server.go.
//
// Why a separate file: keeps the diff on handlers_ext.go focused on the
// deleteApp GC rewrite; reviewers can scan this file's nine handlers
// as one logical group.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// --- decodeJSONLimit --------------------------------------------------------

// defaultInvokeMethod is the fallback method for async / sync
// invoke requests that omit the body's method field. Lifted to a
// constant so goconst stops flagging the repeated literal across
// the three handlers that default it (issue #315 / tier-2 DX
// added the third occurrence in replayInvocation).
const defaultInvokeMethod = "POST"

// decodeJSONLimit is a MaxBytesReader-wrapped variant of decodeJSON.
// The plan's MaxSourceBytesPerInvocation caps each event-driven payload
// (Hobby 64 KB, Pro 256 KB, Scale 1 MB); anything larger is a 413, not
// a 422 — the size limit is a plan cap, not a malformed-body problem.
//
// Returns false if the read or decode fails; the helper writes the
// appropriate Problem response in that case so the caller can simply
// `return`.
func decodeJSONLimit(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) bool {
	if maxBytes <= 0 {
		maxBytes = 1 << 20 // 1 MB hard fallback — defensive only
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			api.WriteProblem(w, api.ErrValidation("empty request body"))
			return false
		}
		// MaxBytesReader returns a *http.MaxBytesError on overflow;
		// surface as a 413 with the source-bytes cap code.
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			api.WriteProblem(w, api.ErrPlanSourceBytes(int(maxBytes), int64(mbe.Limit)))
			return false
		}
		api.WriteProblem(w, api.ErrValidation("malformed JSON: "+err.Error()))
		return false
	}
	return true
}

// --- async invoke -----------------------------------------------------------

// invokeAppAsync is the 202-side of the synchronous invoke. Enqueues a
// row, returns the id + status URL; the customer polls
// /v1/invocations/{id} (or stream SSE in a follow-up) for completion.
func (s *server) invokeAppAsync(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.AsyncInvokeAllowed {
		api.WriteProblem(w, api.ErrPlanFeatureGated("async_invoke", acct.Plan))
		return
	}
	var req invokeRequest
	if !decodeJSONLimit(w, r, &req, int64(limits.MaxSourceBytesPerInvocation)) {
		return
	}
	if req.Method == "" {
		req.Method = defaultInvokeMethod
	}
	if req.Path == "" {
		req.Path = "/"
	}
	inv, err := s.store.EnqueueInvocation(r.Context(), state.Invocation{
		AppID:     app.ID,
		AccountID: acct.ID,
		Source:    state.InvocationAsyncInvoke,
		Method:    req.Method,
		Path:      req.Path,
		Payload:   req.Payload,
		Headers:   req.Headers,
		DueAt:     time.Now().UTC(),
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("enqueue async invoke"))
		return
	}
	writeJSON(w, http.StatusAccepted, api.AsyncInvokeResponse{
		ID:        inv.ID,
		StatusURL: "/v1/invocations/" + inv.ID,
	})
}

// invokeApp is the sync-side. Enqueues a row then long-polls on the
// invocation_done channel scoped to its id; returns the final row
// when the drain drives it to a terminal state, or 504 on timeout.
//
// Free-plan timeout is 5s (a customer on the free tier shouldn't hold a
// connection for the full SLO); paid plans get 30s.
func (s *server) invokeApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.AsyncInvokeAllowed {
		api.WriteProblem(w, api.ErrPlanFeatureGated("sync_invoke", acct.Plan))
		return
	}
	var req invokeRequest
	if !decodeJSONLimit(w, r, &req, int64(limits.MaxSourceBytesPerInvocation)) {
		return
	}
	if req.Method == "" {
		req.Method = defaultInvokeMethod
	}
	if req.Path == "" {
		req.Path = "/"
	}
	timeout := 30 * time.Second
	if acct.Plan == api.PlanFree {
		timeout = 5 * time.Second
	}
	inv, err := s.store.EnqueueInvocation(r.Context(), state.Invocation{
		AppID:     app.ID,
		AccountID: acct.ID,
		Source:    state.InvocationAsyncInvoke, // sync reuses the async source; the long-poll is what makes it sync
		Method:    req.Method,
		Path:      req.Path,
		Payload:   req.Payload,
		Headers:   req.Headers,
		DueAt:     time.Now().UTC(),
		// PR-B fixup (code-review #1185 findings #7 + #8): wire the
		// customer's deadline / retry-policy / retention overrides
		// into the row. deadlineForRequest / retentionForRequest
		// clamp each field to the plan's ceiling so a customer
		// cannot bypass MaxAsyncInvocationDeadlineSeconds /
		// MaxAsyncResultRetentionSeconds by sending an out-of-range
		// value. RetryPolicy is the typed DTO; marshal to JSON for
		// the JSONB column.
		DeadlineAt:           deadlineForRequest(req.DeadlineAt, acct),
		RetryPolicyJSON:      marshalRetryPolicy(req.RetryPolicy),
		ResultRetentionUntil: retentionForRequest(req.RetentionSeconds, acct),
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("enqueue sync invoke"))
		return
	}
	payload, err := s.notif.WaitFor(r.Context(), db.NotifyInvocationDone,
		func(p string) bool {
			// Canonical match — parse the JSON and compare by id, not
			// by substring. A 32-char id suffix can otherwise match
			// the tail of an unrelated id (review finding on PR #191).
			got, _ := extractNotifyFields(p)
			return got == inv.ID
		},
		timeout)
	_ = payload // payload is the pg_notify JSON; we re-read by id below
	if errors.Is(err, db.ErrWaitTimeout) {
		api.WriteProblem(w, api.ErrLongPollTimeout())
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("long-poll"))
		return
	}
	// Pull the post-state row. Drain stamps InstanceID + Result
	// before emitting invocation_done.
	final, ferr := s.store.InvocationByID(r.Context(), inv.ID)
	if ferr != nil {
		api.WriteProblem(w, api.ErrInvocationNotFound(inv.ID))
		return
	}
	writeJSON(w, http.StatusOK, api.InvokeResponse{
		ID:     final.ID,
		Status: string(final.State),
		Result: final.Result,
	})
}

// invokeRequest is the shared body for sync + async invoke (uses
// api.InvokeRequest so the spec compliance test sees a DTO).
type invokeRequest = api.InvokeRequest

// --- queues -----------------------------------------------------------------

// queueSend enqueues a single FIFO row on the per-app queue. The
// per-app MaxQueueDepth cap is re-checked here (the apid gate; the
// drain re-checks at dispatch tick).
func (s *server) queueSend(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if limits.MaxQueueDepth == 0 {
		api.WriteProblem(w, api.ErrPlanFeatureGated("queues", acct.Plan))
		return
	}
	n, err := s.store.CountPendingInvocations(r.Context(), app.ID, state.InvocationQueue)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("count queue"))
		return
	}
	if n >= limits.MaxQueueDepth {
		api.WriteProblem(w, api.ErrPlanQueueDepth(limits.MaxQueueDepth, n))
		return
	}
	var req queueSendRequest
	if !decodeJSONLimit(w, r, &req, int64(limits.MaxSourceBytesPerInvocation)) {
		return
	}
	inv, err := s.store.EnqueueInvocation(r.Context(), state.Invocation{
		AppID:     app.ID,
		AccountID: acct.ID,
		Source:    state.InvocationQueue,
		Payload:   req.Payload,
		DueAt:     time.Now().UTC(),
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("enqueue queue send"))
		return
	}
	writeJSON(w, http.StatusCreated, api.QueueSendResponse{ID: inv.ID})
}

// queueReceive long-polls on invocation_done scoped to this app; when
// the drain completes a row, returns the row's id + payload. 204 on
// timeout (no event during the wait window — the client retries).
func (s *server) queueReceive(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if limits.MaxQueueDepth == 0 {
		api.WriteProblem(w, api.ErrPlanFeatureGated("queues", acct.Plan))
		return
	}
	// ADR-093 / PR-D: 30s long-poll becomes child of inbound budget
	// when one is attached. min(parentRemaining, 30s). No-budget
	// path keeps the legacy 30s WaitFor ceiling.
	waitCtx, cancel := budgetCtx(r.Context(), 30*time.Second)
	defer cancel()
	payload, err := s.notif.WaitFor(waitCtx, db.NotifyInvocationDone,
		func(p string) bool {
			// Canonical match on app_id — substring tests would let a
			// 32-char id tail collide with an unrelated id (review
			// finding on PR #191).
			_, got := extractNotifyFields(p)
			return got == app.ID
		},
		30*time.Second)
	if errors.Is(err, db.ErrWaitTimeout) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("queue receive"))
		return
	}
	invID := extractInvocationID(payload)
	inv, ferr := s.store.InvocationByID(r.Context(), invID)
	if ferr != nil || inv.AccountID != acct.ID || inv.AppID != app.ID {
		// Don't leak ownership — the predicate matches on app_id, but
		// cross-account reads must surface 404, not 200 with a foreign
		// row.
		api.WriteProblem(w, api.ErrInvocationNotFound(invID))
		return
	}
	writeJSON(w, http.StatusOK, api.QueueReceiveResponse{
		ID:      inv.ID,
		Payload: inv.Payload,
		Result:  inv.Result,
	})
}

// queueAck is a no-op state change (the row is already completed when
// invocation_done fires). The handler exists for symmetry with the
// SDK surface and to give a customer a stable place to instrument
// "received + handled" — we stamp completed_at+1ns so a subsequent
// attempt can see the ack.
//
// Idempotent: a re-ack is a 204.
func (s *server) queueAck(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	inv, err := s.store.InvocationByID(r.Context(), id)
	if err != nil || inv.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrInvocationNotFound(id))
		return
	}
	// Ack is informational only. The drain has already stamped
	// completed_at; we just no-op. A future slice can introduce an
	// `acked_at` column if a billing model needs it.
	w.WriteHeader(http.StatusNoContent)
}

type queueSendRequest = api.QueueSendRequest

type delayedTaskRequest = api.DelayedTaskRequest

// extractInvocationID parses {"invocation_id":"<uuid>"} out of a
// pg_notify payload. Defensive against partial / extra-key payloads;
// returns "" if no id is present, in which case the caller surfaces
// ErrInvocationNotFound (which is the right response to a malformed
// notify).
func extractInvocationID(payload string) string {
	// Path-of-least-resistance: pg_notify payloads for invocation_done
	// are produced by drain.emitDone via json.Marshal — guaranteed
	// JSON with `invocation_id`. Use the raw json.Unmarshal path so we
	// don't depend on key order or any extra context.
	var p struct {
		InvocationID string `json:"invocation_id"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return ""
	}
	return p.InvocationID
}

// extractNotifyFields parses {"invocation_id":"...","app_id":"..."}
// out of a pg_notify payload. Same shape as extractInvocationID but
// surfaces both fields — the long-poll predicates need canonical
// equality (not substring) so a 32-char id can never collide against
// the tail of an unrelated id (review finding on PR #191).
func extractNotifyFields(payload string) (invID, appID string) {
	var p struct {
		InvocationID string `json:"invocation_id"`
		AppID        string `json:"app_id"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return "", ""
	}
	return p.InvocationID, p.AppID
}

// --- delayed-tasks ----------------------------------------------------------

// delayedTaskCreate enqueues a delayed_task row. Cap-checked against
// the plan's MaxDelayedTasksPerApp; the drain re-checks at dispatch.
func (s *server) delayedTaskCreate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if limits.MaxDelayedTasksPerApp == 0 {
		api.WriteProblem(w, api.ErrPlanFeatureGated("delayed_tasks", acct.Plan))
		return
	}
	n, err := s.store.CountPendingInvocations(r.Context(), app.ID, state.InvocationDelayedTask)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("count delayed tasks"))
		return
	}
	if n >= limits.MaxDelayedTasksPerApp {
		api.WriteProblem(w, api.ErrPlanDelayedTasksCap(limits.MaxDelayedTasksPerApp, n))
		return
	}
	var req delayedTaskRequest
	if !decodeJSONLimit(w, r, &req, int64(limits.MaxSourceBytesPerInvocation)) {
		return
	}
	now := time.Now().UTC()
	if req.ScheduledAt.Before(now) {
		api.WriteProblem(w, api.ErrInvalidScheduledAt())
		return
	}
	sched := req.ScheduledAt.UTC()
	inv, err := s.store.EnqueueInvocation(r.Context(), state.Invocation{
		AppID:       app.ID,
		AccountID:   acct.ID,
		Source:      state.InvocationDelayedTask,
		Payload:     req.Payload,
		DueAt:       sched,
		ScheduledAt: &sched,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("enqueue delayed task"))
		return
	}
	writeJSON(w, http.StatusCreated, api.DelayedTaskResponse{
		ID:          inv.ID,
		ScheduledAt: sched,
	})
}

// delayedTaskGet is the read-only counterpart. Restricted to
// delayed_task source so a customer's regular invocation id never
// surfaces here.
func (s *server) delayedTaskGet(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	inv, err := s.store.InvocationByID(r.Context(), id)
	if err != nil || inv.AccountID != acct.ID || inv.Source != state.InvocationDelayedTask {
		api.WriteProblem(w, api.ErrInvocationNotFound(id))
		return
	}
	writeJSON(w, http.StatusOK, api.DelayedTaskResponse{
		ID:          inv.ID,
		ScheduledAt: ptrTime(inv.ScheduledAt),
		State:       string(inv.State),
	})
}

// delayedTaskCancel moves a pending delayed_task row to cancelled.
// The drain ignores cancelled rows. Idempotent: a re-cancel is 204
// (the row may have already fired — that's a "we did the work", not
// an error).
func (s *server) delayedTaskCancel(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	inv, err := s.store.InvocationByID(r.Context(), id)
	if err != nil || inv.AccountID != acct.ID || inv.Source != state.InvocationDelayedTask {
		api.WriteProblem(w, api.ErrInvocationNotFound(id))
		return
	}
	if err := s.store.CancelInvocation(r.Context(), id); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrInvocationNotFound(id))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("cancel delayed task"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ptrTime is a tiny adapter so delayedTaskGet can format *time.Time
// without a nil-check at the call site.
// retentionForRequest (ADR-134 PR-B / ADR-135) clamps the customer's
// requested retention to the plan's MaxAsyncResultRetentionSeconds and
// stamps ResultRetentionUntil for EnqueueInvocation.
//
// PR-B fixup (code-review #1185 finding #7): without this helper, the
// per-plan Limits field is unreachable from production code — the
// spec §17 G-Async-Retention gap closure requires the reaper to
// actually have rows to delete. The "opt-down, never up" rule is
// enforced by clamping: a customer asking for N seconds > plan max
// gets plan max; N seconds <= plan max is honored verbatim; nil
// (no override) defaults to the plan max.
//
// Returns nil only if the customer's plan has zero retention (a
// configuration error — every plan row in limits.go has a positive
// value, so nil is a defensive guard for a future PR that adds a
// no-retention plan).
func retentionForRequest(reqRetentionSeconds *int, acct state.Account) *time.Time {
	limits := api.MustLimitsFor(acct.Plan)
	if limits.MaxAsyncResultRetentionSeconds <= 0 {
		return nil
	}
	seconds := limits.MaxAsyncResultRetentionSeconds
	if reqRetentionSeconds != nil {
		if *reqRetentionSeconds < 0 {
			*reqRetentionSeconds = 0
		}
		if *reqRetentionSeconds < seconds {
			seconds = *reqRetentionSeconds
		}
	}
	t := time.Now().UTC().Add(time.Duration(seconds) * time.Second)
	return &t
}

// deadlineForRequest (ADR-134 PR-B / ADR-135) clamps the customer's
// requested deadline to the plan's MaxAsyncInvocationDeadlineSeconds.
// nil or past-now deadlines return nil (no enforcement); a future
// deadline beyond the plan max is clamped to now + plan max.
func deadlineForRequest(reqDeadline *time.Time, acct state.Account) *time.Time {
	if reqDeadline == nil {
		return nil
	}
	limits := api.MustLimitsFor(acct.Plan)
	if limits.MaxAsyncInvocationDeadlineSeconds <= 0 {
		return nil
	}
	maxDeadline := time.Now().UTC().Add(time.Duration(limits.MaxAsyncInvocationDeadlineSeconds) * time.Second)
	if reqDeadline.After(maxDeadline) {
		return &maxDeadline
	}
	return reqDeadline
}

// marshalRetryPolicy (ADR-134 PR-B) converts the wire DTO into
// the JSONB blob pgstore stores verbatim. Returns nil when the
// customer didn't override — EnqueueInvocation's nullable column
// then leaves retry_policy NULL and the drain falls back to the
// plan default.
func marshalRetryPolicy(p *api.RetryPolicyDTO) json.RawMessage {
	if p == nil {
		return nil
	}
	raw, err := json.Marshal(p)
	if err != nil {
		// json.Marshal on a typed struct cannot fail at runtime;
		// the closure-form MarshalJSON could but RetryPolicyDTO
		// has none. Defensive nil-return keeps the row consistent.
		return nil
	}
	return raw
}

func ptrTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// --- invocation history -----------------------------------------------------

// listInvocations is the unified history read for /v1/invocations.
// Pagination is by `?before=<id>` (an Invocation.ID); defaults to 20,
// capped at 200.
func (s *server) listInvocations(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limit := 20
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	before := r.URL.Query().Get("before")
	rows, err := s.store.ListInvocationsForAccount(r.Context(), acct.ID, limit, before)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("list invocations"))
		return
	}
	if rows == nil {
		rows = []state.Invocation{}
	}
	writeJSON(w, http.StatusOK, invocationListResponse{Invocations: rows})
}

// invocationListResponse is the handler-local wire shape for GET
// /v1/invocations. Lives here (not pkg/api/dto.go) because pkg/api
// cannot import pkg/state — the cycle would deadlock compilation. The
// wire mirror is 1:1 with what pkg/api would expose; customers see
// the same JSON regardless of where the type lives.
type invocationListResponse struct {
	Invocations []state.Invocation `json:"invocations"`
}

// getInvocation is the single-row read. Account-scoped so a customer
// never sees another tenant's id.
func (s *server) getInvocation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	inv, err := s.store.InvocationByID(r.Context(), id)
	if err != nil || inv.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrInvocationNotFound(id))
		return
	}
	writeJSON(w, http.StatusOK, inv)
}

// replayInvocation re-issues a failed or dead_letter invocation
// against the original's app + instance (issue #315 / tier-2 DX).
// The replayed row is a fresh async invocation: Source='replay'
// (stamped via the new migration 00159), the original payload +
// headers + method + path are carried verbatim, and the customer
// polls the new id via /v1/invocations/{newID}.
//
// The state allow-list is {failed, dead_letter}. Anything else
// (pending, dispatching, completed, cancelled) returns 409
// ErrInvocationNotReplayable — re-running a successful invocation
// is a customer bug, not a flow we want to enable by accident.
//
// Account-scoped: cross-tenant access returns 404 (same IDOR-safe
// path as getInvocation). Replay carries the customer's auth
// context; the original's AccountID is never reused — we always
// stamp acct.ID on the new row.
//
// IDOR defense (peer review of PR #733, finding F1): the handler
// verifies BOTH that the original invocation belongs to the replayer's
// account AND that the app the original ran against still does. If
// the app has been transferred to a different account (orgs move
// apps, accounts get re-assigned), an old failed invocation retained
// under the original account must NOT be replayable against the
// now-foreign app. The check is `app.AccountID == acct.ID` mirroring
// loadAppAndPreflight (handlers.go:312); any mismatch surfaces 404
// ErrInvocationNotFound, indistinguishable from a missing
// invocation (no information leak about whether the app or the
// invocation was the foreign object).
func (s *server) replayInvocation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	orig, err := s.store.InvocationByID(r.Context(), id)
	if err != nil || orig.AccountID != acct.ID {
		// Same 404 path as getInvocation — IDOR-safe. Don't
		// surface 403 on a cross-tenant attempt; that would
		// leak the existence of the row.
		api.WriteProblem(w, api.ErrInvocationNotFound(id))
		return
	}
	// Re-verify the original's app still belongs to the replayer's
	// account. The original's AccountID may match the replayer's
	// while the app has been transferred to a different account;
	// without this check, the replay would land on a foreign app.
	app, err := s.store.AppByID(r.Context(), orig.AppID)
	if err != nil || app.AccountID != acct.ID {
		// Same 404 surface as the invocation check — never 403.
		api.WriteProblem(w, api.ErrInvocationNotFound(id))
		return
	}
	if orig.State != state.InvocationFailed && orig.State != state.InvocationDeadLetter {
		api.WriteProblem(w, api.ErrInvocationNotReplayable(string(orig.State)))
		return
	}
	// Re-issue the original against the same app; DueAt is "now"
	// (the customer is replaying interactively, not on a schedule).
	// Attempts is reset to 0 — the drain increments it on the new
	// lifecycle. LeaseExpiresAt / ReceivedAt / CompletedAt / Result /
	// LastError / AckURL are nil on a fresh INSERT; the drain
	// populates them as the row flows through dispatch.
	inv, err := s.store.EnqueueInvocation(r.Context(), state.Invocation{
		AppID:     orig.AppID,
		AccountID: acct.ID,
		Source:    state.InvocationReplay,
		Method:    orig.Method,
		Path:      orig.Path,
		Payload:   orig.Payload,
		Headers:   orig.Headers,
		DueAt:     time.Now().UTC(),
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("enqueue replay invocation"))
		return
	}
	writeJSON(w, http.StatusAccepted, api.AsyncInvokeResponse{
		ID:        inv.ID,
		StatusURL: "/v1/invocations/" + inv.ID,
	})
}
