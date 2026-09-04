// Package apislogs holds the SSE envelope helpers for the customer-
// facing app-logs stream (issue #254 / Move 4 landing). Both the
// cmd/apid tail (PR-A wiring, kept for the e2e harness direct-hit
// path) and the cmd/gatewayd-internal/inline handler (PR-2 production
// wiring) render the same `text/event-stream` envelope shape, so
// the helpers live here rather than being duplicated in each
// daemon.
//
// Wire shape (Move 4 acceptance #5, issue #517 / PR-B): the SSE
// response is `text/event-stream` with the following frames:
//
//	event: log\ndata: {"seq":<i>,"instance":<s>,"stream":<s>,"line":<s>,"written_at":<rfc3339>}\n\n
//	event: gap\ndata: {"reason":<s>,"gap_to_written_at":<rfc3339>,"replay_advised":<b>}\n\n
//	event: ping\ndata: {}\n\n   (heartbeat; sigil form `:\n\n`)
//	event: degraded\ndata: {...}\n\n
//	event: error\ndata: {"code":<s>,"message":<s>}\n\n
//	event: end\ndata: {"reason":<s>|"timeout"|"not_found"|"schedd_unreachable"}\n\n
//
// The six-event vocabulary is the contract the SDK decoder
// (pkg/api/sse.go) matches against. New frames MUST add a new
// sentinel rather than overload an existing one — a downstream
// SDK filtering on `event: log` would silently misconsume a
// not-yet-catalogued event.
//
// The `event: gap` frame is NOT terminal; the stream continues
// after a gap with the surviving replay and the live tail (the
// ring's lowest retained seq was below the cursor). `replay_advised`
// is a hint the SDK may surface as a banner; the server itself
// never reaches back into history.
package apislogs

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RenderAppLogEvent writes a single `event: log` SSE frame for
// the given schedd frame. The payload matches Move 4 acceptance
// #5 (the `instance` field is the additive per ADR-016). The
// flusher.Flush after each frame is what the htmx-ext-sse
// auto-reconnect logic relies on — silent frames look like a
// dead connection. ops is nil-safe (a nil receiver no-ops;
// tests that don't wire metrics keep working).
func RenderAppLogEvent(w http.ResponseWriter, flusher http.Flusher, f scheddgrpc.LogFrame, appID string, ops *wire.OpsMetrics) {
	payload, _ := json.Marshal(map[string]any{
		"seq":        f.Seq,
		"instance":   f.InstanceID,
		"stream":     f.Stream,
		"line":       f.Line,
		"written_at": f.WrittenAt.UTC().Format(time.RFC3339Nano),
	})
	_, _ = fmt.Fprintf(w, "event: log\ndata: %s\n\n", payload)
	if flusher != nil {
		flusher.Flush()
	}
	ops.ObserveLogEmitted(appID)
}

// LogAppLogFrame writes the customer log frame to the daemon's structured
// journal stream so Promtail can ship it to Loki even when the client only
// consumes the live SSE path. accountID and appID come from the authenticated
// account/app lookup; they are the only tenant identity fields that the Loki
// pipeline promotes to labels. The raw log line remains a field, never a
// label, and is sanitized to keep one journal record per frame.
//
// A nil logger is a no-op for the lightweight handler tests and for callers
// that intentionally disable off-box customer-log emission.
func LogAppLogFrame(log *slog.Logger, f scheddgrpc.LogFrame, accountID, appID, deploymentID string) {
	if log == nil {
		return
	}
	attrs := []any{
		"account_id", logsanitize.Field(accountID),
		"app_id", logsanitize.Field(appID),
		"instance_id", logsanitize.Field(f.InstanceID),
		"stream", logsanitize.Field(f.Stream),
		"seq", f.Seq,
		"written_at", f.WrittenAt.UTC().Format(time.RFC3339Nano),
	}
	if deploymentID != "" {
		attrs = append(attrs, "deployment_id", logsanitize.Field(deploymentID))
	}
	if f.IsGap {
		attrs = append(attrs,
			"gap_reason", logsanitize.Field(f.GapReason),
			"gap_to_written_at", f.GapToWrittenAt.UTC().Format(time.RFC3339Nano),
		)
		log.Warn("app_log_gap", attrs...)
		return
	}
	attrs = append(attrs, "line", logsanitize.Field(f.Line))
	log.Info("app_log", attrs...)
}

// RenderAppLogsError renders a schedd-side dial error as either a
// 404 (no live instances) or a `degraded` event. Mirrors the
// legacy cmd/apid/handlers_ext.go::renderAppLogsError — the wire
// shape is the source of truth for the SDK decoder.
//
// The discriminator is the gRPC status code, not the lifted
// *api.Problem — schedd returns raw status.Error(codes.NotFound,
// ...) when the app is parked, and that error has no
// *api.Problem payload. Inspecting codes.NotFound directly is
// the load-bearing branch.
//
// Wire shape: the `data:` line is SSE text, NOT JSON. The error
// string is embedded with `%q` (Go-string escaping) so embedded
// quotes / backslashes / control bytes are safe to round-trip
// through the SSE stream — but the resulting line is NOT valid
// JSON, and a future consumer that parses the data: line as JSON
// will see `"error":"...\\n..."` style escapes. The SDK decoder
// matches on the SSE event name + the literal `"code":"not_found"`
// substring, so the escaping is benign there. New helpers that
// emit structured data should use json.Marshal instead.
func RenderAppLogsError(w http.ResponseWriter, flusher http.Flusher, err error) {
	if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
		// Already past StartSSE — write the degraded event
		// instead of trying to overwrite the 200 (the consumer
		// reads the SSE body, not the status).
		_, _ = fmt.Fprintf(w, "event: degraded\ndata: {\"error\":%q,\"code\":\"not_found\"}\n\n", err.Error())
		_, _ = fmt.Fprint(w, "event: end\ndata: {\"reason\":\"not_found\"}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		return
	}
	_, _ = fmt.Fprintf(w, "event: degraded\ndata: {\"error\":%q}\n\n", err.Error())
	_, _ = fmt.Fprint(w, "event: end\ndata: {\"reason\":\"schedd_unreachable\"}\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// StartSSE sets the SSE response headers and disables write
// timeouts for the lifetime of the request. The w.(http.Flusher)
// type assertion is intentional — every http.ResponseWriter we
// accept in production satisfies Flusher (the stdlib
// *http.response is itself a Flusher; httptest.NewRecorder also
// returns one). A missing Flusher would silently break the
// htmx-ext-sse auto-reconnect contract.
func StartSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	w.(http.Flusher).Flush()
}

// RenderAppLogGap writes a single `event: gap` SSE frame for the
// given schedd gap frame (issue #517 / PR-B, AC4). The frame is
// NOT terminal — the stream continues with the surviving replay
// and the live tail. ops is nil-safe.
//
// Reason is one of a small taxonomy: "seq_below_retained" (the
// ring evicted older lines between attach and our first read),
// "since_below_retained" (the since-time predates the oldest
// surviving line). vmmdgrpc.Logs labels the frame at the producer
// and schedd propagates the label verbatim through StreamAppLogs;
// this renderer surfaces it as the wire payload's "reason" key.
// The SDK surface (pkg/api/sse.go::LogGapEvent) mirrors these
// names verbatim.
func RenderAppLogGap(w http.ResponseWriter, flusher http.Flusher, f scheddgrpc.LogFrame, appID string, ops *wire.OpsMetrics) {
	reason := f.GapReason
	if reason == "" {
		// Defensive default — a gap frame without a label means
		// a pre-PR-B vmmd / schedd is upstream. Surface the
		// broader "seq_below_retained" so the consumer still
		// gets a meaningful, non-empty reason.
		reason = "seq_below_retained"
	}
	payload, _ := json.Marshal(map[string]any{
		"reason":            reason,
		"gap_to_written_at": f.GapToWrittenAt.UTC().Format(time.RFC3339Nano),
		"replay_advised":    true,
	})
	_, _ = fmt.Fprintf(w, "event: gap\ndata: %s\n\n", payload)
	if flusher != nil {
		flusher.Flush()
	}
	ops.ObserveLogEmitted(appID)
}

// MaxGrepPatternBytes caps the customer-supplied --grep regex
// length (issue #309 / tier-2 DX, peer review of PR #728).
// Two concerns:
//   - Compile cost: regexp.Compile is O(pattern length) but the
//     compiler allocates intermediate NFA states that grow with
//     pattern complexity, not just length. A 100KB pattern can
//     pressure the vmmd heap on every wake.
//   - Execution cost: Go's regexp engine is RE2-flavoured (no
//     catastrophic backtracking in the worst case), but a
//     pattern like `(a|a)*` can still match linearly against
//     a long input line. The line length is capped by
//     logbuf.MaxPartialLineBytes (1 MiB) on the producer side,
//     so the matcher can't be pushed past that.
//
// 256 bytes is generous for any reasonable log filter
// ("timeout|deadline", "request_id=[a-f0-9]+", etc.) while
// foreclosing the pathological case. The CLI's --grep is
// typically 8-32 bytes; 256 leaves headroom for power users.
const MaxGrepPatternBytes = 256

// ValidateLogFilters enforces the issue #309 filter contract on
// the `--level`, `--grep`, `--since`, and `--deployment` query
// params. ok=false on rejection; the handler must then emit the
// SSE error frame (WriteInvalidLevelError / WriteInvalidGrepError
// / WriteInvalidSinceError / WritePlanDeploymentFilterNotAllowedError)
// and return.
//
// `reason` is the SSE error code that pinpoints the rejection
// ("invalid_level", "invalid_grep", "invalid_since", or
// "plan_deployment_filter_not_allowed") — exported as a const
// string so the SDK decoder can branch without a second
// package-level import.
//
// The return signature is positional rather than a struct so the
// handler's call site stays compact; the new sinceWrittenAt and
// deploymentID slots pair with the existing level/grep pair the
// PR-A wiring introduced.
func ValidateLogFilters(r *http.Request) (level string, grep string, sinceWrittenAt time.Time, deploymentID string, reason string, ok bool) {
	q := r.URL.Query()
	// --level: enum match against api.IsValidLogLevel so the CLI
	// and the server share the same source of truth.
	if l := q.Get("level"); l != "" && !api.IsValidLogLevel(l) {
		return "", "", time.Time{}, "", "invalid_level", false
	}
	// --grep: reject embedded newlines so Move 4's substring
	// matcher can never match across log line boundaries (same
	// log-injection precedent as `CodeQL go/log-injection
	// sanitisers`). ALSO reject patterns longer than
	// MaxGrepPatternBytes to foreclose the regex-DoS surface
	// (peer review of PR #728): a pathological pattern can
	// pressure the vmmd heap during Compile + MatchString.
	if g := q.Get("grep"); g != "" {
		if strings.ContainsAny(g, "\n\r") {
			return "", "", time.Time{}, "", "invalid_grep", false
		}
		if len(g) > MaxGrepPatternBytes {
			return "", "", time.Time{}, "", "invalid_grep", false
		}
	}
	// --since (issue #517 / PR-B, AC3): RFC3339 lower bound on
	// log written_at. Empty = no time bound. Malformed = reject
	// with invalid_since. The schedd/vmmd layer treats the
	// bound as inclusive (>= sinceWrittenAt), matching the
	// existing >= sinceSeq semantics.
	sinceWrittenAt = time.Time{}
	if s := q.Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return "", "", time.Time{}, "", "invalid_since", false
		}
		sinceWrittenAt = t
	}
	deploymentID = q.Get("deployment")
	return q.Get("level"), q.Get("grep"), sinceWrittenAt, deploymentID, "", true
}

// SSE error codes emitted by ValidateLogFilters. Stable strings
// that the SDK decoder branches on (`pkg/api/sse.go`); renaming
// any of these is a breaking change.
const (
	InvalidLevelCode                   = "invalid_level"
	InvalidGrepCode                    = "invalid_grep"
	InvalidSinceCode                   = "invalid_since"
	PlanDeploymentFilterNotAllowedCode = "plan_deployment_filter_not_allowed"
)

// WriteInvalidLevelError writes the `event: error` +
// `event: end` terminal for `level` validation failures. The
// caller has already started SSE; the helper just renders the
// two frames and flushes.
func WriteInvalidLevelError(w http.ResponseWriter, flusher http.Flusher) {
	_, _ = fmt.Fprint(w, "event: error\ndata: {\"code\":\"invalid_level\",\"message\":\"level must be one of: info, warn, error\"}\n\n")
	_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// WriteInvalidGrepError mirrors WriteInvalidLevelError for the
// `--grep` validation failure path.
func WriteInvalidGrepError(w http.ResponseWriter, flusher http.Flusher) {
	_, _ = fmt.Fprint(w, "event: error\ndata: {\"code\":\"invalid_grep\",\"message\":\"grep must not contain newline or carriage return\"}\n\n")
	_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// WriteInvalidSinceError mirrors WriteInvalidLevelError for the
// `--since` validation failure path (issue #517 / PR-B, AC3).
// Triggered when the caller sends a value that is not a valid
// RFC3339 timestamp; the SDK sees `code: invalid_since` and can
// surface a "since must be RFC3339 (e.g. 2026-08-01T12:00:00Z)"
// hint.
func WriteInvalidSinceError(w http.ResponseWriter, flusher http.Flusher) {
	_, _ = fmt.Fprint(w, "event: error\ndata: {\"code\":\"invalid_since\",\"message\":\"since must be RFC3339 (e.g. 2026-08-01T12:00:00Z)\"}\n\n")
	_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// WritePlanDeploymentFilterNotAllowedError mirrors
// WriteInvalidLevelError for the `--deployment` plan-gate
// failure path (issue #517 / PR-B, AC3). Triggered when a Free
// plan customer sends `?deployment=...` (LogDeploymentFilterMax
// == 0 for Free). The `max` arg is the per-plan cap (0 for
// Free); surfaced in the message so the SDK can show the user
// what to upgrade to.
func WritePlanDeploymentFilterNotAllowedError(w http.ResponseWriter, flusher http.Flusher, max int) {
	_, _ = fmt.Fprintf(w, "event: error\ndata: {\"code\":\"plan_deployment_filter_not_allowed\",\"message\":\"your plan does not allow the ?deployment= filter (max=%d); upgrade to Hobby or above\"}\n\n", max)
	_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// ParseInt64Query parses an int64 query param with a default
// value. Returns the default if the param is missing or
// unparseable. Negative values are clamped to 0 — the schedd
// gRPC stream lifts them to 0 anyway (defence in depth).
func ParseInt64Query(r *http.Request, name string, def int64) int64 {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	if n < 0 {
		return 0
	}
	return n
}

// IsTerminalFrame is the small predicate the SDK uses to decide
// when a frames stream is done. Event-name lookup is the
// cheapest check; the SDK reads the full line anyway. Exposed
// here so the apid tail (PR-A) and the gatewayd-internal handler (PR-2)
// agree on which event names end the stream.
//
// Post-condition: every terminal frame in the wire shape is
// followed by an `event: end` sentinel (renderAppLogsError,
// WriteInvalidLevelError, WriteInvalidGrepError all emit end
// after their terminal frame). The SDK decoder matches on the
// event name; the post-condition is what guarantees a
// structured-frame loop exits after exactly one terminal frame
// rather than spinning through a residual "degraded" + "end"
// pair twice.
func IsTerminalFrame(event string) bool {
	switch event {
	case "end", "error", "degraded":
		return true
	}
	return false
}
