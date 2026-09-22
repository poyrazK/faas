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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	pkgtrace "github.com/onebox-faas/faas/pkg/trace"
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
	if r.ContentLength > maxBytes {
		api.WriteProblem(w, api.ErrPlanSourceBytes(int(maxBytes), r.ContentLength))
		return false
	}
	if r.Body == nil {
		api.WriteProblem(w, api.ErrValidation("empty request body"))
		return false
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
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.ErrPlanSourceBytes(int(maxBytes), int64(maxBytes)))
			return false
		}
		if err == nil {
			err = errors.New("request body must contain a single JSON value")
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
	if !app.AcceptsRequestInvocations() {
		api.WriteProblem(w, api.ErrInvocationWorkloadClass(string(app.WorkloadClass), app.Manifest.ExecutionMode))
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
	if problem := validateInvokeRequest(req); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if req.Method == "" {
		req.Method = defaultInvokeMethod
	}
	if req.Path == "" {
		req.Path = "/"
	}
	onSuccessDestination, onFailureDestination, destinationProblem := s.resolveInvocationDestinations(r.Context(), app.ID, acct.ID, req.Destinations)
	if destinationProblem != nil {
		api.WriteProblem(w, destinationProblem)
		return
	}
	invocationHeaders, err := pkgtrace.MergeHeaders(r.Context(), req.Headers)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("headers must be a JSON object of string values"))
		return
	}
	inv, err := s.store.EnqueueInvocation(r.Context(), state.Invocation{
		AppID:                  app.ID,
		AccountID:              acct.ID,
		Source:                 state.InvocationAsyncInvoke,
		Method:                 req.Method,
		Path:                   req.Path,
		Payload:                req.Payload,
		Headers:                invocationHeaders,
		DueAt:                  time.Now().UTC(),
		RetryPolicyJSON:        effectiveInvocationRetryPolicy(app, req.RetryPolicy),
		DeadlineAt:             deadlineForRequest(req.DeadlineAt, acct),
		ResultRetentionUntil:   retentionForRequest(req.RetentionSeconds, acct),
		OnSuccessDestinationID: onSuccessDestination,
		OnFailureDestinationID: onFailureDestination,
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
	if !app.AcceptsRequestInvocations() {
		api.WriteProblem(w, api.ErrInvocationWorkloadClass(string(app.WorkloadClass), app.Manifest.ExecutionMode))
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
	if problem := validateInvokeRequest(req); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if req.Method == "" {
		req.Method = defaultInvokeMethod
	}
	if req.Path == "" {
		req.Path = "/"
	}
	onSuccessDestination, onFailureDestination, destinationProblem := s.resolveInvocationDestinations(r.Context(), app.ID, acct.ID, req.Destinations)
	if destinationProblem != nil {
		api.WriteProblem(w, destinationProblem)
		return
	}
	timeout := 30 * time.Second
	if acct.Plan == api.PlanFree {
		timeout = 5 * time.Second
	}
	invocationHeaders, err := pkgtrace.MergeHeaders(r.Context(), req.Headers)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("headers must be a JSON object of string values"))
		return
	}
	inv, err := s.store.EnqueueInvocation(r.Context(), state.Invocation{
		AppID:     app.ID,
		AccountID: acct.ID,
		Source:    state.InvocationAsyncInvoke, // sync reuses the async source; the long-poll is what makes it sync
		Method:    req.Method,
		Path:      req.Path,
		Payload:   req.Payload,
		Headers:   invocationHeaders,
		DueAt:     time.Now().UTC(),
		// PR-B fixup (code-review #1185 findings #7 + #8): wire the
		// customer's deadline / retry-policy / retention overrides
		// into the row. deadlineForRequest / retentionForRequest
		// clamp each field to the plan's ceiling so a customer
		// cannot bypass MaxAsyncInvocationDeadlineSeconds /
		// MaxAsyncResultRetentionSeconds by sending an out-of-range
		// value. RetryPolicy is the typed DTO; marshal to JSON for
		// the JSONB column.
		DeadlineAt:             deadlineForRequest(req.DeadlineAt, acct),
		RetryPolicyJSON:        effectiveInvocationRetryPolicy(app, req.RetryPolicy),
		ResultRetentionUntil:   retentionForRequest(req.RetentionSeconds, acct),
		OnSuccessDestinationID: onSuccessDestination,
		OnFailureDestinationID: onFailureDestination,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("enqueue sync invoke"))
		return
	}
	var waitErr error
	if s.invocationCompletion != nil {
		waitErr = s.invocationCompletion.Wait(r.Context(), inv.ID, timeout)
	} else {
		var payload string
		payload, waitErr = s.notif.WaitFor(r.Context(), db.NotifyInvocationDone,
			func(p string) bool {
				// Canonical match — parse the JSON and compare by id, not
				// by substring. A 32-char id suffix can otherwise match
				// the tail of an unrelated id (review finding on PR #191).
				got, _ := extractNotifyFields(p)
				return got == inv.ID
			},
			timeout)
		_ = payload // payload is the pg_notify JSON; we re-read by id below
	}
	if errors.Is(waitErr, db.ErrWaitTimeout) {
		api.WriteProblem(w, api.ErrLongPollTimeout())
		return
	}
	if waitErr != nil {
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
		Error:  final.LastError,
	})
}

// invokeRequest is the shared body for sync + async invoke (uses
// api.InvokeRequest so the spec compliance test sees a DTO).
type invokeRequest = api.InvokeRequest

// resolveInvocationDestinations validates the webhook subscriptions named by
// an invocation request before the durable row is created. The IDs are
// intentionally app-scoped: accepting an arbitrary webhook here would make
// a valid app invocation a cross-tenant delivery primitive.
func (s *server) resolveInvocationDestinations(ctx context.Context, appID, accountID string, destinations *api.InvocationDestinations) (string, string, *api.Problem) {
	if destinations == nil {
		return "", "", nil
	}
	resolve := func(field, id string) (string, *api.Problem) {
		if id == "" {
			return "", nil
		}
		hook, err := s.store.AppWebhookByID(ctx, id)
		if err != nil || hook.AppID != appID || hook.AccountID != accountID {
			return "", api.ErrValidation(fmt.Sprintf("destinations.%s must reference a webhook owned by this app", field))
		}
		return hook.ID, nil
	}
	success, prob := resolve("on_success", destinations.OnSuccess)
	if prob != nil {
		return "", "", prob
	}
	failure, prob := resolve("on_failure", destinations.OnFailure)
	if prob != nil {
		return "", "", prob
	}
	return success, failure, nil
}

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
	if problem := validateInvocationRetryPolicy(req.RetryPolicy); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	queueName, problem := s.resolveQueueSendName(r.Context(), acct, app, req.QueueName)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	traceHeaders, err := pkgtrace.MergeHeaders(r.Context(), nil)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("encode queue trace context"))
		return
	}
	inv, err := s.store.EnqueueInvocation(r.Context(), state.Invocation{
		AppID:           app.ID,
		AccountID:       acct.ID,
		Source:          state.InvocationQueue,
		QueueName:       queueName,
		Payload:         req.Payload,
		Headers:         traceHeaders,
		DueAt:           time.Now().UTC(),
		RetryPolicyJSON: effectiveInvocationRetryPolicy(app, req.RetryPolicy),
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("enqueue queue send"))
		return
	}
	var traceHeaderValues map[string]string
	_ = json.Unmarshal(traceHeaders, &traceHeaderValues)
	writeJSON(w, http.StatusCreated, api.QueueSendResponse{
		ID:      inv.ID,
		TraceID: traceHeaderValues[api.TraceIDHeader],
	})
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
			invocationID, got := extractNotifyFields(p)
			if got != app.ID || invocationID == "" {
				return false
			}
			inv, err := s.store.InvocationByID(waitCtx, invocationID)
			return err == nil && inv.AccountID == acct.ID && inv.AppID == app.ID && inv.Source == state.InvocationQueue
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
	if ferr != nil || inv.AccountID != acct.ID || inv.AppID != app.ID || inv.Source != state.InvocationQueue {
		// Don't leak ownership — the predicate matches on app_id, but
		// cross-account reads must surface 404, not 200 with a foreign
		// row.
		api.WriteProblem(w, api.ErrInvocationNotFound(invID))
		return
	}
	var traceHeaders map[string]string
	if len(inv.Headers) > 0 {
		_ = json.Unmarshal(inv.Headers, &traceHeaders)
	}
	writeJSON(w, http.StatusOK, api.QueueReceiveResponse{
		ID:          inv.ID,
		Payload:     inv.Payload,
		Result:      inv.Result,
		TraceID:     traceHeaders[api.TraceIDHeader],
		Traceparent: traceHeaders["traceparent"],
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
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	id := r.PathValue("id")
	inv, err := s.store.InvocationByID(r.Context(), id)
	if err != nil || inv.AccountID != acct.ID || inv.AppID != app.ID || inv.Source != state.InvocationQueue {
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

// resolveQueueSendName keeps the legacy single per-app queue ergonomic while
// making named queues deterministic once an app has more than one binding.
// An explicit queue_name is always preferred; an omitted name adopts the
// only active binding/trigger when there is one, otherwise it retains the
// legacy empty queue name.
func (s *server) resolveQueueSendName(ctx context.Context, acct state.Account, app state.App, requested string) (string, *api.Problem) {
	if requested != "" {
		if prob := validateQueueBindingName("queue_name", requested); prob != nil {
			return "", prob
		}
		if bindings, err := s.store.ListQueueBindingsForApp(ctx, acct.ID, app.ID); err == nil {
			active := 0
			for _, binding := range bindings {
				if !binding.Enabled {
					continue
				}
				active++
				if binding.QueueName == requested {
					return requested, nil
				}
			}
			if active > 0 {
				return "", queueBindingProblem(fmt.Sprintf("queue_name %q is not an enabled binding for this app", requested))
			}
		}
		if triggers, err := s.store.ListTriggersForApp(ctx, app.ID); err == nil {
			active := 0
			for _, trigger := range triggers {
				if trigger.Kind != string(api.TriggerKindQueue) || !trigger.Enabled || !trigger.Source.Valid || trigger.Source.String != string(state.InvocationQueue) {
					continue
				}
				active++
				if trigger.Slug == requested {
					return requested, nil
				}
			}
			if active > 0 {
				return "", queueBindingProblem(fmt.Sprintf("queue_name %q is not an enabled queue consumer for this app", requested))
			}
		}
		return requested, nil
	}
	bindings, err := s.store.ListQueueBindingsForApp(ctx, acct.ID, app.ID)
	if err == nil {
		active := make([]state.QueueBinding, 0, len(bindings))
		for _, binding := range bindings {
			if binding.Enabled {
				active = append(active, binding)
			}
		}
		if len(active) == 1 {
			return active[0].QueueName, nil
		}
		if len(active) > 1 {
			return "", queueBindingProblem("queue_name is required when an app has multiple enabled queue bindings")
		}
	}
	// Compatibility for pre-binding queue triggers. The old API exposed one
	// app-scoped queue and used the trigger slug only as a label.
	triggers, err := s.store.ListTriggersForApp(ctx, app.ID)
	if err == nil {
		var queueName string
		for _, trigger := range triggers {
			if trigger.Kind != string(api.TriggerKindQueue) || !trigger.Enabled || !trigger.Source.Valid || trigger.Source.String != string(state.InvocationQueue) {
				continue
			}
			if queueName != "" {
				return "", queueBindingProblem("queue_name is required when an app has multiple enabled queue consumers")
			}
			queueName = trigger.Slug
		}
		if queueName != "" {
			return queueName, nil
		}
	}
	return "", nil
}

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
	if !app.AcceptsRequestInvocations() {
		api.WriteProblem(w, api.ErrInvocationWorkloadClass(string(app.WorkloadClass), app.Manifest.ExecutionMode))
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
	sched, problem := delayedTaskSchedule(now, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if problem := validateInvocationRetryPolicy(req.RetryPolicy); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if req.RetentionSeconds != nil && *req.RetentionSeconds < 0 {
		api.WriteProblem(w, api.ErrValidation("retention_seconds must be non-negative"))
		return
	}
	if req.Method == "" {
		req.Method = defaultInvokeMethod
	}
	if req.Path == "" {
		req.Path = "/"
	}
	onSuccessDestination, onFailureDestination, destinationProblem := s.resolveInvocationDestinations(r.Context(), app.ID, acct.ID, req.Destinations)
	if destinationProblem != nil {
		api.WriteProblem(w, destinationProblem)
		return
	}
	invocationHeaders, err := pkgtrace.MergeHeaders(r.Context(), req.Headers)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("encode delayed task trace context"))
		return
	}
	inv, err := s.store.EnqueueInvocation(r.Context(), state.Invocation{
		AppID:                  app.ID,
		AccountID:              acct.ID,
		Source:                 state.InvocationDelayedTask,
		Method:                 req.Method,
		Path:                   req.Path,
		Payload:                req.Payload,
		Headers:                invocationHeaders,
		DueAt:                  sched,
		ScheduledAt:            &sched,
		DeadlineAt:             deadlineForRequestAt(sched, nil, acct),
		RetryPolicyJSON:        effectiveInvocationRetryPolicy(app, req.RetryPolicy),
		ResultRetentionUntil:   retentionForRequestAt(sched, req.RetentionSeconds, acct),
		OnSuccessDestinationID: onSuccessDestination,
		OnFailureDestinationID: onFailureDestination,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("enqueue delayed task"))
		return
	}
	writeJSON(w, http.StatusCreated, delayedTaskResponse(inv))
}

// delayedTaskSchedule validates the mutually-exclusive absolute and relative
// scheduling forms and applies the platform's bounded scheduling horizon.
func delayedTaskSchedule(now time.Time, req delayedTaskRequest) (time.Time, *api.Problem) {
	hasAbsolute := !req.ScheduledAt.IsZero()
	hasRelative := req.DelaySeconds != 0
	if hasAbsolute == hasRelative {
		return time.Time{}, api.ErrInvalidScheduledAt("exactly one of scheduled_at or delay_seconds is required")
	}
	if req.DelaySeconds < 0 || req.DelaySeconds > int64(api.MaxDelayedTaskDelaySeconds) {
		return time.Time{}, api.ErrInvalidScheduledAt(fmt.Sprintf("delay_seconds must be between 1 and %d", api.MaxDelayedTaskDelaySeconds))
	}
	sched := req.ScheduledAt.UTC()
	if hasRelative {
		sched = now.Add(time.Duration(req.DelaySeconds) * time.Second)
	}
	if !sched.After(now) {
		return time.Time{}, api.ErrInvalidScheduledAt()
	}
	if sched.After(now.Add(time.Duration(api.MaxDelayedTaskDelaySeconds) * time.Second)) {
		return time.Time{}, api.ErrInvalidScheduledAt(fmt.Sprintf("scheduled_at cannot be more than %d days in the future", api.MaxDelayedTaskDelaySeconds/(24*60*60)))
	}
	return sched, nil
}

// delayedTaskList returns delayed tasks for one app, newest first.
func (s *server) delayedTaskList(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limit := 20
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	rows, err := s.store.ListDelayedTasksForApp(r.Context(), app.ID, limit, r.URL.Query().Get("before"))
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("list delayed tasks"))
		return
	}
	response := api.ListDelayedTasksResponse{Tasks: make([]api.DelayedTaskResponse, 0, len(rows))}
	for _, inv := range rows {
		response.Tasks = append(response.Tasks, delayedTaskResponse(inv))
	}
	if len(rows) == limit && len(rows) > 0 {
		response.NextBefore = rows[len(rows)-1].ID
	}
	writeJSON(w, http.StatusOK, response)
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
	writeJSON(w, http.StatusOK, delayedTaskResponse(inv))
}

// delayedTaskCancel moves a pending delayed_task row to cancelled and
// returns the resulting state. Dispatching and terminal rows are left
// unchanged so clients can distinguish prevention from observation.
func (s *server) delayedTaskCancel(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	inv, err := s.store.InvocationByID(r.Context(), id)
	if err != nil || inv.AccountID != acct.ID || inv.Source != state.InvocationDelayedTask {
		api.WriteProblem(w, api.ErrInvocationNotFound(id))
		return
	}
	result, err := s.store.CancelPendingInvocation(r.Context(), id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrInvocationNotFound(id))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("cancel delayed task"))
		return
	}
	inv.State = result
	writeJSON(w, http.StatusOK, delayedTaskResponse(inv))
}

func delayedTaskResponse(inv state.Invocation) api.DelayedTaskResponse {
	return api.DelayedTaskResponse{
		ID:          inv.ID,
		AppID:       inv.AppID,
		ScheduledAt: ptrTime(inv.ScheduledAt),
		State:       string(inv.State),
		Method:      inv.Method,
		Path:        inv.Path,
		Attempts:    inv.Attempts,
		LastError:   inv.LastError,
		Result:      inv.Result,
		CreatedAt:   inv.CreatedAt,
		CompletedAt: inv.CompletedAt,
	}
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
	return retentionForRequestAt(time.Now().UTC(), reqRetentionSeconds, acct)
}

func retentionForRequestAt(base time.Time, reqRetentionSeconds *int, acct state.Account) *time.Time {
	limits := api.MustLimitsFor(acct.Plan)
	if limits.MaxAsyncResultRetentionSeconds <= 0 {
		return nil
	}
	seconds := limits.MaxAsyncResultRetentionSeconds
	if reqRetentionSeconds != nil && *reqRetentionSeconds > 0 {
		if *reqRetentionSeconds < seconds {
			seconds = *reqRetentionSeconds
		}
	}
	t := base.UTC().Add(time.Duration(seconds) * time.Second)
	return &t
}

// deadlineForRequest (ADR-134 PR-B / ADR-135) clamps the customer's
// requested deadline to the plan's MaxAsyncInvocationDeadlineSeconds.
// An omitted deadline receives the plan default; a requested deadline beyond
// the plan max is clamped to now + plan max.
func deadlineForRequest(reqDeadline *time.Time, acct state.Account) *time.Time {
	return deadlineForRequestAt(time.Now().UTC(), reqDeadline, acct)
}

func deadlineForRequestAt(base time.Time, reqDeadline *time.Time, acct state.Account) *time.Time {
	limits := api.MustLimitsFor(acct.Plan)
	if limits.MaxAsyncInvocationDeadlineSeconds <= 0 {
		return nil
	}
	maxDeadline := base.UTC().Add(time.Duration(limits.MaxAsyncInvocationDeadlineSeconds) * time.Second)
	if reqDeadline == nil {
		return &maxDeadline
	}
	if reqDeadline.After(maxDeadline) {
		return &maxDeadline
	}
	return reqDeadline
}

func validateInvokeRequest(req invokeRequest) *api.Problem {
	if req.DeadlineAt != nil && !req.DeadlineAt.After(time.Now()) {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid invocation deadline", "deadline_at must be in the future")
	}
	if req.RetentionSeconds != nil && *req.RetentionSeconds < 0 {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid invocation retention", "retention_seconds must be non-negative")
	}
	return validateInvocationRetryPolicy(req.RetryPolicy)
}

func validateInvocationRetryPolicy(policy *api.RetryPolicyDTO) *api.Problem {
	if policy == nil {
		return nil
	}
	if policy.MaxAttempts < 0 || policy.MaxAttempts > 25 {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid invocation retry policy", "max_attempts must be between 0 and 25")
	}
	if policy.BaseSeconds < 0 || math.IsNaN(policy.BaseSeconds) || math.IsInf(policy.BaseSeconds, 0) {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid invocation retry policy", "base_seconds must be finite and non-negative")
	}
	if policy.MaxSeconds < 0 || math.IsNaN(policy.MaxSeconds) || math.IsInf(policy.MaxSeconds, 0) {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid invocation retry policy", "max_seconds must be finite and non-negative")
	}
	if policy.BaseSeconds > 0 && policy.MaxSeconds > 0 && policy.MaxSeconds < policy.BaseSeconds {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid invocation retry policy", "max_seconds must be at least base_seconds")
	}
	if policy.JitterSeconds < 0 || policy.JitterSeconds > 1 || math.IsNaN(policy.JitterSeconds) || math.IsInf(policy.JitterSeconds, 0) {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid invocation retry policy", "jitter_seconds must be between 0 and 1")
	}
	return nil
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

// effectiveInvocationRetryPolicy applies deterministic precedence for an
// invocation row: an explicit request override wins; otherwise the app-level
// default is copied into the row. Queue binding consumers can replace this
// value with their binding policy before dispatch.
func effectiveInvocationRetryPolicy(app state.App, override *api.RetryPolicyDTO) json.RawMessage {
	if override != nil {
		return marshalRetryPolicy(override)
	}
	if len(app.RetryPolicyJSON) == 0 || string(app.RetryPolicyJSON) == "{}" {
		return nil
	}
	return append(json.RawMessage(nil), app.RetryPolicyJSON...)
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
	var nextBefore string
	if len(rows) == limit && len(rows) > 0 {
		// The store orders newest first and applies the same cursor to
		// the next query, so the last row is the stable continuation
		// token. An extra request at the end of an exact-size history is
		// harmless and keeps the store interface backwards-compatible.
		nextBefore = rows[len(rows)-1].ID
	}
	writeJSON(w, http.StatusOK, invocationListResponse{Invocations: rows, NextBefore: nextBefore})
}

// invocationListResponse is the handler-local wire shape for GET
// /v1/invocations. Lives here (not pkg/api/dto.go) because pkg/api
// cannot import pkg/state — the cycle would deadlock compilation. The
// wire mirror is 1:1 with what pkg/api would expose; customers see
// the same JSON regardless of where the type lives.
type invocationListResponse struct {
	Invocations []state.Invocation `json:"invocations"`
	NextBefore  string             `json:"next_before,omitempty"`
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
	if !app.AcceptsRequestInvocations() {
		api.WriteProblem(w, api.ErrInvocationWorkloadClass(string(app.WorkloadClass), app.Manifest.ExecutionMode))
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
	invocationHeaders, err := pkgtrace.MergeHeaders(r.Context(), orig.Headers)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("original invocation headers must be a JSON object of string values"))
		return
	}
	inv, err := s.store.EnqueueInvocation(r.Context(), state.Invocation{
		AppID:                orig.AppID,
		AccountID:            acct.ID,
		Source:               state.InvocationReplay,
		Method:               orig.Method,
		Path:                 orig.Path,
		Payload:              orig.Payload,
		Headers:              invocationHeaders,
		DueAt:                time.Now().UTC(),
		DeadlineAt:           deadlineForRequest(nil, acct),
		ResultRetentionUntil: retentionForRequest(nil, acct),
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
