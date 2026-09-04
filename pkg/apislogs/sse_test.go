package apislogs

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestLogAppLogFrameStructuredJournalRecord(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	f := scheddgrpc.LogFrame{
		InstanceID: "instance-1\nspoofed",
		Seq:        42,
		Stream:     "stdout",
		Line:       "hello\r\nworld",
		WrittenAt:  time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
	}
	LogAppLogFrame(logger, f, "acct-1\nspoofed", "app-1", "deploy-1")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("decode structured log: %v; output=%q", err, buf.String())
	}
	if record["msg"] != "app_log" {
		t.Fatalf("msg=%v, want app_log", record["msg"])
	}
	for key, want := range map[string]string{
		"account_id":    "acct-1·spoofed",
		"app_id":        "app-1",
		"instance_id":   "instance-1·spoofed",
		"deployment_id": "deploy-1",
		"line":          "hello··world",
	} {
		if got := record[key]; got != want {
			t.Errorf("%s=%v, want %q", key, got, want)
		}
	}
	// JSON records necessarily end in a newline; inspect decoded string
	// values for injection rather than rejecting the framing newline.
	for _, key := range []string{"account_id", "app_id", "instance_id", "deployment_id", "line"} {
		if strings.ContainsAny(record[key].(string), "\r\n") {
			t.Errorf("%s contains CR/LF: %q", key, record[key])
		}
	}
}

func TestLogAppLogFrameGapUsesWarningRecord(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	LogAppLogFrame(logger, scheddgrpc.LogFrame{
		IsGap:          true,
		GapReason:      "seq_below_retained",
		GapToWrittenAt: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		WrittenAt:      time.Date(2026, 7, 29, 12, 0, 1, 0, time.UTC),
	}, "acct-1", "app-1", "")
	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("decode structured gap log: %v; output=%q", err, buf.String())
	}
	if record["msg"] != "app_log_gap" {
		t.Fatalf("msg=%v, want app_log_gap", record["msg"])
	}
	if record["gap_reason"] != "seq_below_retained" {
		t.Errorf("gap_reason=%v", record["gap_reason"])
	}
}

// TestRenderAppLogEvent_PayloadShape pins the Move 4 acceptance
// #5 wire shape. The {seq, instance, stream, line, written_at}
// field set is what the SDK decoder parses; an accidental
// rename or field drop would break the dashboard's live log
// view.
func TestRenderAppLogEvent_PayloadShape(t *testing.T) {
	rec := httptest.NewRecorder()
	f := scheddgrpc.LogFrame{
		InstanceID: "i-1",
		Seq:        42,
		Stream:     "stdout",
		Line:       "hello\n",
		WrittenAt:  time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
	}
	RenderAppLogEvent(rec, rec, f, "app-1", nil)
	out := rec.Body.String()
	for _, want := range []string{
		`event: log`,
		`"seq":42`,
		`"instance":"i-1"`,
		`"stream":"stdout"`,
		`"line":"hello\n"`,
		`"written_at":"2026-07-29T12:00:00Z"`,
		"\n\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("body missing %q\nbody=%s", want, out)
		}
	}
}

// TestRenderAppLogsError_NotFound pins the parked-app path:
// codes.NotFound → degraded event with `"code":"not_found"` and
// terminal end with `"reason":"not_found"`. The SDK decoder
// branches on these fields; renaming any of them is a breaking
// change.
func TestRenderAppLogsError_NotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	RenderAppLogsError(rec, rec, notFoundErrorStub())
	out := rec.Body.String()
	if !strings.Contains(out, `event: degraded`) {
		t.Errorf("missing degraded event: %s", out)
	}
	if !strings.Contains(out, `"code":"not_found"`) {
		t.Errorf("missing not_found code: %s", out)
	}
	if !strings.Contains(out, `"reason":"not_found"`) {
		t.Errorf("missing end reason: %s", out)
	}
}

// TestRenderAppLogsError_Generic pins the schedd-unreachable
// path: any non-NotFound error → degraded event + end reason
// "schedd_unreachable". Pino-style log-tailing clients treat
// this reason as "reconnect with backoff".
func TestRenderAppLogsError_Generic(t *testing.T) {
	rec := httptest.NewRecorder()
	RenderAppLogsError(rec, rec, genericErrorStub())
	out := rec.Body.String()
	if !strings.Contains(out, `event: degraded`) {
		t.Errorf("missing degraded event: %s", out)
	}
	if !strings.Contains(out, `"reason":"schedd_unreachable"`) {
		t.Errorf("missing schedd_unreachable reason: %s", out)
	}
}

// TestStartSSE_Headers confirms the four mandatory SSE headers
// are set on a 200 response. httptest.NewRecorder's Header()
// already returns a fresh map; the assertion is byte-exact.
func TestStartSSE_Headers(t *testing.T) {
	rec := httptest.NewRecorder()
	StartSSE(rec)
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
	if got := rec.Header().Get("Connection"); got != "keep-alive" {
		t.Errorf("Connection = %q, want keep-alive", got)
	}
	if got := rec.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", got)
	}
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestValidateLogFilters_Level rejects an unknown level value.
// The wire frame asks for the enum to traverse api.IsValidLogLevel
// so the CLI and the server share the same source of truth.
func TestValidateLogFilters_Level(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/apps/foo/logs?level=debug", nil)
	_, _, _, _, reason, ok := ValidateLogFilters(r)
	if ok {
		t.Error("ValidateLogFilters accepted level=debug; should reject")
	}
	if reason != InvalidLevelCode {
		t.Errorf("reason = %q, want %q", reason, InvalidLevelCode)
	}
}

// TestValidateLogFilters_Grep rejects an embedded newline.
// httptest.NewRequest strips CR/LF from the URL, so we mutate
// r.URL.RawQuery after construction (same trick as the URL
// log-injection tests in pkg/auth/middleware).
func TestValidateLogFilters_Grep(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/apps/foo/logs?grep=foo", nil)
	r.URL.RawQuery = "grep=foo\nbar"
	_, _, _, _, reason, ok := ValidateLogFilters(r)
	if ok {
		t.Error("ValidateLogFilters accepted grep with newline; should reject")
	}
	if reason != InvalidGrepCode {
		t.Errorf("reason = %q, want %q", reason, InvalidGrepCode)
	}
}

// TestValidateLogFilters_HappyPath confirms a valid request passes.
func TestValidateLogFilters_HappyPath(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/apps/foo/logs?level=info&grep=hello", nil)
	level, grep, sinceWrittenAt, deploymentID, reason, ok := ValidateLogFilters(r)
	if !ok {
		t.Fatal("ValidateLogFilters rejected a valid request")
	}
	if reason != "" {
		t.Errorf("reason = %q, want empty on happy path", reason)
	}
	if level != "info" || grep != "hello" {
		t.Errorf("level=%q grep=%q, want info/hello", level, grep)
	}
	if !sinceWrittenAt.IsZero() {
		t.Errorf("sinceWrittenAt = %v, want zero on default request", sinceWrittenAt)
	}
	if deploymentID != "" {
		t.Errorf("deploymentID = %q, want empty on default request", deploymentID)
	}
}

// TestValidateLogFilters_SinceAccepted (issue #517 / PR-B, AC3)
// confirms a well-formed RFC3339 `since=` parses to the
// corresponding time.Time. Empty → zero time.
func TestValidateLogFilters_SinceAccepted(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/apps/foo/logs?since=2026-08-01T12:00:00Z", nil)
	_, _, sinceWrittenAt, _, reason, ok := ValidateLogFilters(r)
	if !ok {
		t.Fatalf("ValidateLogFilters rejected a valid since= RFC3339; reason=%q", reason)
	}
	want := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	if !sinceWrittenAt.Equal(want) {
		t.Errorf("sinceWrittenAt = %v, want %v", sinceWrittenAt, want)
	}
}

// TestValidateLogFilters_SinceMalformed (issue #517 / PR-B, AC3)
// confirms a non-RFC3339 `since=` is rejected with the
// InvalidSinceCode so the handler renders an `event: error` frame.
func TestValidateLogFilters_SinceMalformed(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/apps/foo/logs?since=yesterday", nil)
	_, _, _, _, reason, ok := ValidateLogFilters(r)
	if ok {
		t.Fatal("ValidateLogFilters accepted since=yesterday; should reject")
	}
	if reason != InvalidSinceCode {
		t.Errorf("reason = %q, want %q", reason, InvalidSinceCode)
	}
}

// TestParseInt64Query pins the default + clamp behaviour.
func TestParseInt64Query(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/apps/foo/logs", nil)
	if got := ParseInt64Query(r, "since_seq", 7); got != 7 {
		t.Errorf("missing param: ParseInt64Query = %d, want default 7", got)
	}
	r = httptest.NewRequest("GET", "/v1/apps/foo/logs?since_seq=42", nil)
	if got := ParseInt64Query(r, "since_seq", 7); got != 42 {
		t.Errorf("present param: ParseInt64Query = %d, want 42", got)
	}
	r = httptest.NewRequest("GET", "/v1/apps/foo/logs?since_seq=-5", nil)
	if got := ParseInt64Query(r, "since_seq", 7); got != 0 {
		t.Errorf("negative param: ParseInt64Query = %d, want 0 (clamped)", got)
	}
	r = httptest.NewRequest("GET", "/v1/apps/foo/logs?since_seq=abc", nil)
	if got := ParseInt64Query(r, "since_seq", 7); got != 7 {
		t.Errorf("unparseable param: ParseInt64Query = %d, want default 7", got)
	}
}

// TestIsTerminalFrame pins the six-event vocabulary contract.
// `gap` (issue #517 / PR-B) is NOT terminal — the stream
// continues with the surviving replay and the live tail.
func TestIsTerminalFrame(t *testing.T) {
	for _, ev := range []string{"end", "error", "degraded"} {
		if !IsTerminalFrame(ev) {
			t.Errorf("IsTerminalFrame(%q) = false, want true", ev)
		}
	}
	for _, ev := range []string{"log", "gap", "ping", ""} {
		if IsTerminalFrame(ev) {
			t.Errorf("IsTerminalFrame(%q) = true, want false", ev)
		}
	}
}

// TestRenderAppLogGap (issue #517 / PR-B, AC4) pins the `event:
// gap` wire shape: {reason, gap_to_written_at, replay_advised},
// non-terminal (no event:end follow-up). ops is nil-safe.
func TestRenderAppLogGap(t *testing.T) {
	rec := httptest.NewRecorder()
	f := scheddgrpc.LogFrame{
		InstanceID:     "i-1",
		GapToWrittenAt: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
	}
	RenderAppLogGap(rec, rec, f, "app-1", nil)
	out := rec.Body.String()
	for _, want := range []string{
		`event: gap`,
		`"reason":`,
		`"gap_to_written_at":"2026-07-29T12:00:00Z"`,
		`"replay_advised":true`,
		"\n\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("body missing %q\nbody=%s", want, out)
		}
	}
	// Must NOT include a terminal end frame — gap is mid-stream.
	if strings.Contains(out, `event: end`) {
		t.Errorf("gap frame must not be followed by an end frame; body=%s", out)
	}
}

// TestWriteInvalidSinceError confirms the SSE error-frame wire
// shape for an invalid `since=` value (issue #517 / PR-B, AC3).
func TestWriteInvalidSinceError(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteInvalidSinceError(rec, rec)
	out := rec.Body.String()
	if !strings.Contains(out, `"code":"invalid_since"`) {
		t.Errorf("missing invalid_since code: %s", out)
	}
	if !strings.Contains(out, `event: end`) {
		t.Errorf("missing end sentinel: %s", out)
	}
}

// TestWritePlanDeploymentFilterNotAllowedError confirms the
// SSE error-frame wire shape for a Free-plan customer sending
// `?deployment=...` (issue #517 / PR-B, AC3). The `max` arg
// surfaces in the message so the SDK can hint the upgrade path.
func TestWritePlanDeploymentFilterNotAllowedError(t *testing.T) {
	rec := httptest.NewRecorder()
	WritePlanDeploymentFilterNotAllowedError(rec, rec, 0)
	out := rec.Body.String()
	if !strings.Contains(out, `"code":"plan_deployment_filter_not_allowed"`) {
		t.Errorf("missing plan_deployment_filter_not_allowed code: %s", out)
	}
	if !strings.Contains(out, `max=0`) {
		t.Errorf("missing max=0 hint in message: %s", out)
	}
	if !strings.Contains(out, `event: end`) {
		t.Errorf("missing end sentinel: %s", out)
	}
}

// --- helpers ---

// notFoundErrorStub returns a real gRPC status with code NotFound
// so grpcerr.FromStatus can decode it. The render path keys on
// the gRPC code, not the message — so a plain errors.New is not
// enough to exercise the not-found branch.
func notFoundErrorStub() error { return status.Error(codes.NotFound, "no live instances") }

// genericErrorStub is a non-NotFound error for the generic-error
// render path. A plain error string is enough — the render
// function checks the gRPC code only when it can lift one.
func genericErrorStub() error { return status.Error(codes.Unavailable, "dial: connection refused") }
