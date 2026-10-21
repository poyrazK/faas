// adr: 20261021090000
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// RehearsalPlatformAPIStore names the contained caller's run and captures its
// queue work as run evidence.
type RehearsalPlatformAPIStore interface {
	ScenarioRehearsalNetworkRun(ctx context.Context, appID string) (string, error)
	state.ScenarioQueueCaptureStore
}

// WithRehearsalPlatformAPI serves a contained rehearsal guest's platform API
// calls at api.svc.gregale on the guest service listener. Queue sends and
// delayed tasks are validated like the public API and captured, never
// enqueued. Every other request, including one from a caller outside a
// contained run, reaches next unchanged.
func WithRehearsalPlatformAPI(next http.Handler, resolve ServiceProxyCallerResolver, store RehearsalPlatformAPIStore) http.Handler {
	if resolve == nil || store == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind, slug, ok := rehearsalPlatformAPIRoute(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		caller, err := resolve(r.Context(), r.RemoteAddr)
		if err != nil || caller == "" {
			next.ServeHTTP(w, r)
			return
		}
		run, err := store.ScenarioRehearsalNetworkRun(r.Context(), caller)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("rehearsal platform API unavailable"))
			return
		}
		if run == "" {
			next.ServeHTTP(w, r)
			return
		}
		serveRehearsalQueueCapture(w, r, store, caller, kind, slug, time.Now().UTC())
	})
}

// rehearsalPlatformAPIRoute matches the public API's queue and delayed-task
// paths on the reserved platform host only.
func rehearsalPlatformAPIRoute(r *http.Request) (kind, slug string, ok bool) {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if r.Method != http.MethodPost || !strings.EqualFold(strings.TrimSuffix(host, "."), api.ScenarioPlatformAPIHost) {
		return "", "", false
	}
	rest, found := strings.CutPrefix(r.URL.Path, "/v1/apps/")
	if !found {
		return "", "", false
	}
	slug, route, found := strings.Cut(rest, "/")
	if !found || slug == "" {
		return "", "", false
	}
	switch route {
	case "queues/send":
		return api.ScenarioQueueKindQueue, slug, true
	case "delayed-tasks":
		return api.ScenarioQueueKindDelayedTask, slug, true
	}
	return "", "", false
}

func serveRehearsalQueueCapture(w http.ResponseWriter, r *http.Request, store state.ScenarioQueueCaptureStore, caller, kind, slug string, now time.Time) {
	msg, scheduledAt, problem := decodeRehearsalQueueCapture(w, r, kind, slug, now)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	id, captured, err := store.CaptureScenarioQueueMessage(r.Context(), caller, msg)
	switch {
	case !captured || errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such app"))
		return
	case errors.Is(err, state.ErrScenarioActionsSealed):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "rehearsal_actions_sealed", "Rehearsal actions sealed", "the rehearsal run no longer accepts queue work"))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("rehearsal queue capture unavailable"))
		return
	}
	w.Header().Set("X-Gregale-Rehearsal", "captured")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if kind == api.ScenarioQueueKindQueue {
		_ = json.NewEncoder(w).Encode(api.QueueSendResponse{ID: id})
		return
	}
	_ = json.NewEncoder(w).Encode(api.DelayedTaskResponse{ID: id, ScheduledAt: scheduledAt, State: string(state.InvocationPending), CreatedAt: now})
}

// decodeRehearsalQueueCapture applies the public API's request contract. A
// body over the capture bound is kept only as an incomplete marker, so the run
// reports incomplete evidence rather than a different outcome.
func decodeRehearsalQueueCapture(w http.ResponseWriter, r *http.Request, kind, slug string, now time.Time) (state.ScenarioQueueCapture, time.Time, *api.Problem) {
	msg := state.ScenarioQueueCapture{Kind: kind, Slug: slug}
	if r.Body == nil {
		return msg, time.Time{}, api.ErrValidation("empty request body")
	}
	_ = http.NewResponseController(w).SetReadDeadline(now.Add(5 * time.Second))
	body, err := io.ReadAll(io.LimitReader(r.Body, api.MaxScenarioCapturedQueueMessageBytes+1))
	_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
	if err != nil {
		return msg, time.Time{}, api.ErrValidation("read request body")
	}
	if len(body) > api.MaxScenarioCapturedQueueMessageBytes {
		msg.Oversized = true
		return msg, now, nil
	}
	if kind == api.ScenarioQueueKindQueue {
		var req api.QueueSendRequest
		if problem := decodeRehearsalStrictJSON(body, &req); problem != nil {
			return msg, time.Time{}, problem
		}
		msg.QueueName = req.QueueName
		msg.Message, _ = json.Marshal(req)
		return msg, time.Time{}, nil
	}
	var req api.DelayedTaskRequest
	if problem := decodeRehearsalStrictJSON(body, &req); problem != nil {
		return msg, time.Time{}, problem
	}
	scheduledAt, schedule, problem := rehearsalDelayedTaskSchedule(now, req)
	if problem != nil {
		return msg, time.Time{}, problem
	}
	// The instant is wall-clock; only the requested delay or the fact of an
	// absolute instant is compared.
	req.ScheduledAt, req.DelaySeconds = time.Time{}, 0
	msg.Schedule = schedule
	msg.Message, _ = json.Marshal(req)
	return msg, scheduledAt, nil
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
