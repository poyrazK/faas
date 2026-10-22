// adr: 20261021090000
package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// RehearsalPlatformAPIStore names the contained caller's run and captures its
// platform work as run evidence.
type RehearsalPlatformAPIStore interface {
	state.ScenarioRehearsalNetworkPolicyStore
	state.ScenarioQueueCaptureStore
	state.ScenarioEventCaptureStore
	state.ScenarioPlatformCaptureStore
	state.ScenarioCapturedReadStore
	state.ScenarioRealtimeGroupStore
	state.EventSchemaStore
}

// rehearsalPlatformCall is one request from a contained caller.
type rehearsalPlatformCall struct {
	store  RehearsalPlatformAPIStore
	caller string
	policy state.ScenarioRehearsalNetworkPolicy
	now    time.Time
}

type rehearsalPlatformRoute func(http.ResponseWriter, *http.Request, rehearsalPlatformCall)

// WithRehearsalPlatformAPI serves a contained rehearsal guest's platform API
// calls at api.svc.gregale on the guest service listener. Each call is
// validated like the public API and captured as run evidence, never
// performed. Every other request, including one from a caller outside a
// contained run, reaches next unchanged.
func WithRehearsalPlatformAPI(next http.Handler, resolve ServiceProxyCallerResolver, store RehearsalPlatformAPIStore) http.Handler {
	if resolve == nil || store == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route, ok := rehearsalPlatformAPIRoute(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		caller, err := resolve(r.Context(), r.RemoteAddr)
		if err != nil || caller == "" {
			next.ServeHTTP(w, r)
			return
		}
		policy, contained, err := store.ScenarioRehearsalNetworkPolicy(r.Context(), caller)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("rehearsal platform API unavailable"))
			return
		}
		if !contained {
			next.ServeHTTP(w, r)
			return
		}
		route(w, r, rehearsalPlatformCall{store: store, caller: caller, policy: policy, now: time.Now().UTC()})
	})
}

// rehearsalPlatformAPIRoute matches the public API paths the run-local API
// serves, on the reserved platform host only.
func rehearsalPlatformAPIRoute(r *http.Request) (rehearsalPlatformRoute, bool) {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if !strings.EqualFold(strings.TrimSuffix(host, "."), api.ScenarioPlatformAPIHost) {
		return nil, false
	}
	switch r.Method {
	case http.MethodGet:
		if route, ok := rehearsalReadRoute(r.URL.Path); ok {
			return route, true
		}
		if route, ok := realtimeWorkerGroupRoute(r.Method, r.URL.Path); ok {
			return route, true
		}
		return realtimeGroupStatusRoute(r.URL.Path)
	case http.MethodDelete:
		if id, ok := strings.CutPrefix(r.URL.Path, "/v1/executions/"); ok && id != "" && !strings.Contains(id, "/") {
			return executionCancelRoute(id), true
		}
		return nil, false
	case http.MethodPost:
	default:
		return nil, false
	}
	if route, ok := realtimeWorkerGroupRoute(r.Method, r.URL.Path); ok {
		return route, true
	}
	if route, ok := realtimePublisherRoute(r.URL.Path); ok {
		return route, true
	}
	switch r.URL.Path {
	case "/v1/events/recipient-blockers":
		return serveRehearsalRecipientBlockers, true
	case "/v1/executions":
		return serveRehearsalExecutionStart, true
	case "/v1/events:publish":
		return serveRehearsalEventPublish, true
	case "/v1/events:publish-batch":
		return serveRehearsalEventBatch, true
	}
	if rest, found := strings.CutPrefix(r.URL.Path, "/v1/workflows/runs/"); found {
		runID, tail, _ := strings.Cut(rest, "/")
		if runID != "" && tail == "events" {
			return workflowEventRoute(runID), true
		}
		return nil, false
	}
	path := r.URL.Path
	// An organization-scoped queue send addresses the same app; the run, not
	// the organization, bounds what it may reach.
	if rest, found := strings.CutPrefix(path, "/v1/orgs/"); found {
		org, tail, _ := strings.Cut(rest, "/")
		if org == "" || !strings.HasPrefix(tail, "apps/") || !strings.HasSuffix(tail, "/queues/send") {
			return nil, false
		}
		path = "/v1/" + tail
	}
	rest, found := strings.CutPrefix(path, "/v1/apps/")
	if !found {
		return nil, false
	}
	slug, route, found := strings.Cut(rest, "/")
	if !found || slug == "" {
		return nil, false
	}
	switch route {
	case "queues/send":
		return queueCaptureRoute(api.ScenarioQueueKindQueue, slug), true
	case "delayed-tasks":
		return queueCaptureRoute(api.ScenarioQueueKindDelayedTask, slug), true
	case "invoke/async":
		return queueCaptureRoute(api.ScenarioQueueKindAsyncInvoke, slug), true
	case "inbox":
		return queueCaptureRoute(rehearsalInboxKind, slug), true
	case "entities/invoke", "entities/retry":
		return entitiesUnavailableRoute, true
	}
	if rest, ok := strings.CutPrefix(route, "realtime/"); ok {
		return realtimeRoute(slug, rest)
	}
	if workflow, ok := strings.CutPrefix(route, "workflows/"); ok {
		if name, ok := strings.CutSuffix(workflow, "/runs"); ok && name != "" && !strings.Contains(name, "/") {
			return workflowStartRoute(slug, name), true
		}
	}
	return nil, false
}

func queueCaptureRoute(kind, slug string) rehearsalPlatformRoute {
	return func(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall) {
		serveRehearsalQueueCapture(w, r, call, kind, slug)
	}
}

// rehearsalInboxKind routes POST .../inbox, the application-inbox facade over
// a queue send; it is captured as kind queue.
const rehearsalInboxKind = "inbox"

func serveRehearsalQueueCapture(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall, kind, slug string) {
	now := call.now
	msg, scheduledAt, eventID, problem := decodeRehearsalQueueCapture(w, r, call.policy.AccountID, kind, slug, now)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if !scheduledAt.IsZero() {
		msg.ScheduledAt = &scheduledAt
	}
	id, captured, err := call.store.CaptureScenarioQueueMessage(r.Context(), call.caller, msg)
	switch {
	case !captured || errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such app"))
		return
	case errors.Is(err, state.ErrScenarioActionsSealed):
		api.WriteProblem(w, rehearsalSealedProblem())
		return
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("rehearsal queue capture unavailable"))
		return
	}
	statusURL := "/v1/invocations/" + id
	switch kind {
	case api.ScenarioQueueKindQueue:
		writeRehearsalCaptured(w, http.StatusCreated, api.QueueSendResponse{ID: id})
	case rehearsalInboxKind:
		writeRehearsalCaptured(w, http.StatusAccepted, api.SendAppMessageResponse{ID: id, EventID: eventID, TargetApp: slug,
			Status: string(state.InvocationPending), StatusURL: statusURL})
	case api.ScenarioQueueKindAsyncInvoke:
		writeRehearsalCaptured(w, http.StatusAccepted, api.AsyncInvokeResponse{ID: id, StatusURL: statusURL})
	default:
		writeRehearsalCaptured(w, http.StatusCreated, api.DelayedTaskResponse{ID: id, ScheduledAt: scheduledAt, State: string(state.InvocationPending), CreatedAt: now})
	}
}

// decodeRehearsalQueueCapture applies the public API's request contract. A
// body over the capture bound is kept only as an incomplete marker, so the run
// reports incomplete evidence rather than a different outcome. eventID is an
// inbox message's envelope ID.
func decodeRehearsalQueueCapture(w http.ResponseWriter, r *http.Request, accountID, kind, slug string, now time.Time) (state.ScenarioQueueCapture, time.Time, string, *api.Problem) {
	msg := state.ScenarioQueueCapture{Kind: kind, Slug: slug}
	if kind == rehearsalInboxKind {
		msg.Kind = api.ScenarioQueueKindQueue
	}
	if r.Body == nil {
		return msg, time.Time{}, "", api.ErrValidation("empty request body")
	}
	_ = http.NewResponseController(w).SetReadDeadline(now.Add(5 * time.Second))
	body, err := io.ReadAll(io.LimitReader(r.Body, api.MaxScenarioCapturedQueueMessageBytes+1))
	_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
	if err != nil {
		return msg, time.Time{}, "", api.ErrValidation("read request body")
	}
	if len(body) > api.MaxScenarioCapturedQueueMessageBytes {
		msg.Oversized = true
		if kind == api.ScenarioQueueKindDelayedTask {
			return msg, now, "", nil
		}
		return msg, time.Time{}, uuid.NewString(), nil
	}
	switch kind {
	case api.ScenarioQueueKindQueue:
		var req api.QueueSendRequest
		if problem := decodeRehearsalStrictJSON(body, &req); problem != nil {
			return msg, time.Time{}, "", problem
		}
		if problem := req.RetryPolicy.Validate(); problem != nil {
			return msg, time.Time{}, "", problem
		}
		msg.QueueName = req.QueueName
		msg.Message, _ = json.Marshal(req)
		return msg, time.Time{}, "", nil
	case rehearsalInboxKind:
		return decodeRehearsalInbox(body, msg, accountID, now)
	case api.ScenarioQueueKindAsyncInvoke:
		return decodeRehearsalAsyncInvoke(body, msg, now)
	}
	var req api.DelayedTaskRequest
	if problem := decodeRehearsalStrictJSON(body, &req); problem != nil {
		return msg, time.Time{}, "", problem
	}
	scheduledAt, schedule, problem := rehearsalDelayedTaskSchedule(now, req)
	if problem == nil {
		problem = req.RetryPolicy.Validate()
	}
	if problem == nil && req.RetentionSeconds != nil && *req.RetentionSeconds < 0 {
		problem = api.ErrValidation("retention_seconds must be non-negative")
	}
	if problem != nil {
		return msg, time.Time{}, "", problem
	}
	// The instant is wall-clock; only the requested delay or the fact of an
	// absolute instant is compared.
	req.ScheduledAt, req.DelaySeconds = time.Time{}, 0
	msg.Schedule = schedule
	msg.Message, _ = json.Marshal(req)
	return msg, scheduledAt, "", nil
}

// decodeRehearsalInbox normalizes an application-inbox message as the public
// API does. The envelope ID and time are platform defaults or wall-clock, so
// only the business fields are compared.
func decodeRehearsalInbox(body []byte, msg state.ScenarioQueueCapture, accountID string, now time.Time) (state.ScenarioQueueCapture, time.Time, string, *api.Problem) {
	var req api.SendAppMessageRequest
	if problem := decodeRehearsalStrictJSON(body, &req); problem != nil {
		return msg, time.Time{}, "", problem
	}
	if req.ID == "" {
		req.ID = uuid.NewString()
	}
	if req.Source == "" {
		req.Source = "gregale.send"
	}
	occurredAt := time.Time{}
	if req.Time != nil {
		occurredAt = *req.Time
	}
	envelope, err := (events.Envelope{ID: req.ID, Source: req.Source, Type: req.Type, Time: occurredAt, DataContentType: req.DataContentType,
		Data: req.Data}).Normalize(accountID, now)
	if err != nil {
		return msg, time.Time{}, "", api.ErrValidation(err.Error())
	}
	if problem := req.RetryPolicy.Validate(); problem != nil {
		return msg, time.Time{}, "", problem
	}
	msg.QueueName = req.QueueName
	msg.Message, _ = json.Marshal(map[string]any{"inbox": map[string]any{"source": envelope.Source, "type": envelope.Type,
		"datacontenttype": envelope.DataContentType, "data": envelope.Data}, "queue_name": req.QueueName, "environment": req.Environment,
		"flag_context": req.FlagContext, "retry_policy": req.RetryPolicy, "work": req.Work})
	return msg, time.Time{}, envelope.ID, nil
}

// decodeRehearsalAsyncInvoke validates an asynchronous invoke as the public
// API does. A deadline is an instant, so only its presence is compared.
func decodeRehearsalAsyncInvoke(body []byte, msg state.ScenarioQueueCapture, now time.Time) (state.ScenarioQueueCapture, time.Time, string, *api.Problem) {
	var req api.InvokeRequest
	if problem := decodeRehearsalStrictJSON(body, &req); problem != nil {
		return msg, time.Time{}, "", problem
	}
	if req.DeadlineAt != nil && !req.DeadlineAt.After(now) {
		return msg, time.Time{}, "", api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid invocation deadline", "deadline_at must be in the future")
	}
	if req.RetentionSeconds != nil && *req.RetentionSeconds < 0 {
		return msg, time.Time{}, "", api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid invocation retention", "retention_seconds must be non-negative")
	}
	if problem := req.RetryPolicy.Validate(); problem != nil {
		return msg, time.Time{}, "", problem
	}
	if req.Method == "" {
		req.Method = http.MethodPost
	}
	if req.Path == "" {
		req.Path = "/"
	}
	deadline := ""
	if req.DeadlineAt != nil {
		deadline = "absolute"
	}
	req.DeadlineAt = nil
	invoke, _ := json.Marshal(req)
	msg.Message, _ = json.Marshal(map[string]json.RawMessage{"invoke": invoke, "deadline": json.RawMessage(fmt.Sprintf("%q", deadline))})
	return msg, time.Time{}, "", nil
}

func decodeRehearsalStrictJSON(body []byte, dst any) *api.Problem {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return api.ErrValidation("empty request body")
		}
		return api.ErrValidation("malformed JSON: " + err.Error())
	}
	if dec.More() {
		return api.ErrValidation("malformed JSON: trailing data after the request object")
	}
	return nil
}

// rehearsalDelayedTaskSchedule mirrors the public API's schedule validation.
func rehearsalDelayedTaskSchedule(now time.Time, req api.DelayedTaskRequest) (time.Time, string, *api.Problem) {
	hasAbsolute, hasRelative := !req.ScheduledAt.IsZero(), req.DelaySeconds != 0
	if hasAbsolute == hasRelative {
		return time.Time{}, "", api.ErrInvalidScheduledAt("exactly one of scheduled_at or delay_seconds is required")
	}
	if req.DelaySeconds < 0 || req.DelaySeconds > int64(api.MaxDelayedTaskDelaySeconds) {
		return time.Time{}, "", api.ErrInvalidScheduledAt(fmt.Sprintf("delay_seconds must be between 1 and %d", api.MaxDelayedTaskDelaySeconds))
	}
	scheduledAt, schedule := req.ScheduledAt.UTC(), "absolute"
	if hasRelative {
		scheduledAt, schedule = now.Add(time.Duration(req.DelaySeconds)*time.Second), fmt.Sprintf("delay:%d", req.DelaySeconds)
	}
	if !scheduledAt.After(now) {
		return time.Time{}, "", api.ErrInvalidScheduledAt()
	}
	if scheduledAt.After(now.Add(time.Duration(api.MaxDelayedTaskDelaySeconds) * time.Second)) {
		return time.Time{}, "", api.ErrInvalidScheduledAt(fmt.Sprintf("scheduled_at cannot be more than %d days in the future", api.MaxDelayedTaskDelaySeconds/(24*60*60)))
	}
	return scheduledAt, schedule, nil
}
