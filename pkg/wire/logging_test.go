// Tests for the standard log envelope helpers added in PR-A (issue #517).
// The shape is intentionally small — we only assert the contract that downstream
// dashboards depend on (canonical field names, empty-drop semantics, context
// round-trip). Behavioural tests for the engine/vmmd wire lives elsewhere.

package wire_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// decodeLines splits captured slog JSON output into per-record decoded maps.
// slog's JSON handler emits one object per line; we read line by line so a
// single buffer holding multiple records is testable.
func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode line %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

func TestNewCorrelationLogger_EmitsCanonicalFields(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	log := wire.NewCorrelationLogger(base, wire.CorrelationFields{
		RequestID:    "req-1",
		WakeID:       "wake-1",
		AppID:        "app-1",
		DeploymentID: "dep-1",
		InstanceID:   "ins-1",
		InvocationID: "inv-1",
		// OTel span context (issue #555 PR-1 envelope extension).
		TraceID: "00000000000000000000000000000001",
		SpanID:  "0000000000000001",
	}, "schedd")
	log.Info("hello", "k", "v")

	recs := decodeLines(t, &buf)
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	rec := recs[0]
	want := map[string]string{
		"request_id":    "req-1",
		"wake_id":       "wake-1",
		"app_id":        "app-1",
		"deployment_id": "dep-1",
		"instance_id":   "ins-1",
		"invocation_id": "inv-1",
		"trace_id":      "00000000000000000000000000000001",
		"span_id":       "0000000000000001",
		"daemon":        "schedd",
		"msg":           "hello",
		"k":             "v",
	}
	for k, v := range want {
		if got, _ := rec[k].(string); got != v {
			t.Errorf("field %q = %q, want %q", k, got, v)
		}
	}
}

func TestPlatformIdentityLogger_EmitsDeploymentFields(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, nil))
	log := wire.WithPlatformIdentityLogger(base, api.PlatformIdentity{
		RequestID:           "req-identity",
		AppID:               "app-1",
		DeploymentID:        "dep-1",
		TenantID:            "tenant-1",
		InstanceID:          "instance-1",
		NodeID:              "node-1",
		Region:              "eu-west",
		CommitSHA:           "abc123",
		DeploymentTag:       "canary",
		DeploymentCreatedAt: "2026-09-19T19:00:00Z",
		ImageDigest:         "sha256:digest",
	})
	log.Info("identity")
	recs := decodeLines(t, &buf)
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	for key, want := range map[string]string{
		"request_id": "req-identity", "app_id": "app-1", "deployment_id": "dep-1",
		"tenant_id": "tenant-1", "instance_id": "instance-1", "node_id": "node-1",
		"region": "eu-west", "commit_sha": "abc123", "deployment_tag": "canary",
		"deployment_created_at": "2026-09-19T19:00:00Z", "image_digest": "sha256:digest",
	} {
		if got, _ := recs[0][key].(string); got != want {
			t.Errorf("field %q = %q, want %q", key, got, want)
		}
	}
}

func TestWithPlatformIdentity_PreservesLifecycleFields(t *testing.T) {
	ctx := wire.WithContext(context.Background(), wire.CorrelationFields{
		RequestID: "req-1", WakeID: "wake-1", InvocationID: "inv-1", Trigger: "gateway",
	})
	ctx = wire.WithPlatformIdentity(ctx, api.PlatformIdentity{
		AppID: "app-1", DeploymentID: "dep-1", TenantID: "tenant-1", Region: "eu-west",
	})
	got, ok := wire.FromContext(ctx)
	if !ok {
		t.Fatal("identity context did not round-trip")
	}
	if got.WakeID != "wake-1" || got.InvocationID != "inv-1" || got.Trigger != "gateway" {
		t.Fatalf("lifecycle fields changed: %+v", got)
	}
	if got.AppID != "app-1" || got.DeploymentID != "dep-1" || got.TenantID != "tenant-1" || got.Region != "eu-west" {
		t.Fatalf("identity fields missing: %+v", got)
	}
}

// TestCorrelationLogger_StampsVersion (issue #586 / ADR-129 /
// cluster C commit 10 of the platform-observability mega-PR) pins
// the version propagation contract: every slog record emitted
// through a NewCorrelationLogger constructed on top of a
// base.With("version", ...) MUST carry both "daemon" and
// "version" attributes exactly once per record. wire.Daemon()
// relies on this to surface the binary identity from journalctl —
// a regression that drops version from the With chain would mean
// operators can't tell which commit SHA produced a panic stack
// trace.
//
// The test mimics Daemon()'s post-#852-fix pattern: base.With
// only stamps "version" (NOT "daemon", since NewCorrelationLogger
// already injects FieldDaemon). The output JSON must carry
// "version" on every record, including ones emitted from a child
// logger derived via WithCorrelationFields.
//
// Issue #852 fix: assert via strings.Count instead of
// json.Unmarshal-into-map. The map decoder silently collapses
// duplicate JSON keys (the previous tests all passed while
// emitting daemon twice per record), so the string-count check
// is the only regression test that catches the dedup-violating
// shape. Exactly one `"daemon":` and one `"version":` per line
// is the contract.
func TestCorrelationLogger_StampsVersion(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	daemonName := "vmmd"
	version := "1.2.3-test"

	// Daemon() (post #852 fix) constructs the base with version
	// only — daemon is injected by NewCorrelationLogger.
	log := wire.NewCorrelationLogger(
		base.With("version", version),
		wire.CorrelationFields{RequestID: "req-1"},
		daemonName,
	)
	log.Info("starting")

	// Child logger (e.g. per-app handler logs) must still carry version.
	child := wire.WithCorrelationFields(log, wire.CorrelationFields{AppID: "app-1"})
	child.Info("wake admit", "wake_id", "wake-1")

	// Raw-line assertion: every emitted line carries exactly one
	// `"daemon":` and exactly one `"version":`. Pre-#852 fix this
	// was 2 each because base.With stamped daemon AND
	// NewCorrelationLogger stamped daemon a second time. Map
	// decoders silently collapse duplicates, so this is the only
	// test shape that catches the regression.
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	for i, line := range lines {
		if c := strings.Count(line, `"daemon":`); c != 1 {
			t.Errorf("record[%d] has %d %q keys, want 1 (#852 fix: each line must carry daemon exactly once). line=%q",
				i, c, "daemon", line)
		}
		if c := strings.Count(line, `"version":`); c != 1 {
			t.Errorf("record[%d] has %d %q keys, want 1 (Daemon()'s With chain must stamp version exactly once). line=%q",
				i, c, "version", line)
		}
	}

	// Decoded-receiver checks for the human-readable shape.
	recs := decodeLines(t, &buf)
	for i, rec := range recs {
		if got, _ := rec["version"].(string); got != version {
			t.Errorf("record[%d] version = %q, want %q", i, got, version)
		}
		if got, _ := rec["daemon"].(string); got != daemonName {
			t.Errorf("record[%d] daemon = %q, want %q", i, got, daemonName)
		}
	}
	// Child logger inherits the envelope.
	if got, _ := recs[1]["app_id"].(string); got != "app-1" {
		t.Errorf("child record missing app_id = %q, want 'app-1'", got)
	}
	if got, _ := recs[1]["version"].(string); got != version {
		t.Errorf("child record version = %q, want %q (child logger must inherit envelope version)", got, version)
	}
}

// TestCorrelationLogger_DaemonEmittedOnce_ParentLogger pins issue
// #852 directly: a logger built via Daemon()'s exact
// post-fix construction (base.With("version", ...), then
// NewCorrelationLogger(..., daemonName)) MUST emit "daemon" exactly
// once per record. The pre-fix pattern (base.With("daemon", ...,
// "version", ...), then NewCorrelationLogger(..., daemonName))
// emitted "daemon" twice on every record — caught by nothing
// because the existing tests decoded JSON into map[string]any,
// which silently collapses duplicate keys.
func TestCorrelationLogger_DaemonEmittedOnce_ParentLogger(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	log := wire.NewCorrelationLogger(
		base.With("version", "1.2.3"),
		wire.CorrelationFields{RequestID: "req-1"},
		"schedd",
	)
	log.Info("boot")

	line := strings.TrimSpace(buf.String())
	if c := strings.Count(line, `"daemon":`); c != 1 {
		t.Fatalf("got %d \"daemon\": keys, want 1 (issue #852 fix). line=%q", c, line)
	}
	if got, _ := decodeLines(t, &buf)[0]["daemon"].(string); got != "schedd" {
		t.Errorf("daemon = %q, want schedd", got)
	}
}

// TestCorrelationLogger_DaemonEmittedOnce_ChildLogger: even a
// child logger derived via WithCorrelationFields must carry
// exactly one "daemon" — the parent's daemon envelope survives
// the With chain and NewCorrelationLogger must not stamp a second
// copy.
func TestCorrelationLogger_DaemonEmittedOnce_ChildLogger(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	parent := wire.NewCorrelationLogger(
		base.With("version", "1.2.3"),
		wire.CorrelationFields{},
		"vmmd",
	)
	child := wire.WithCorrelationFields(parent, wire.CorrelationFields{AppID: "app-1"})
	child.Info("wake admit", "wake_id", "wake-1")

	line := strings.TrimSpace(buf.String())
	if c := strings.Count(line, `"daemon":`); c != 1 {
		t.Fatalf("got %d \"daemon\": keys on child record, want 1 (issue #852 fix). line=%q", c, line)
	}
	if got, _ := decodeLines(t, &buf)[0]["daemon"].(string); got != "vmmd" {
		t.Errorf("daemon = %q, want vmmd", got)
	}
}

// TestCorrelationLogger_DaemonNotOnEmptyName: the daemon field
// is OPTIONAL. Passing "" to NewCorrelationLogger (a legitimate
// call shape for non-daemon libraries that use the helper) must
// emit zero "daemon" keys — neither an empty string nor a duplicate.
func TestCorrelationLogger_DaemonNotOnEmptyName(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	log := wire.NewCorrelationLogger(
		base.With("version", "1.2.3"),
		wire.CorrelationFields{RequestID: "req-1"},
		"", // no daemon — e.g. a test harness wrapping the helper
	)
	log.Info("hello")

	line := strings.TrimSpace(buf.String())
	if c := strings.Count(line, `"daemon":`); c != 0 {
		t.Fatalf("got %d \"daemon\": keys, want 0 when daemon=\"\". line=%q", c, line)
	}
	if _, ok := decodeLines(t, &buf)[0]["daemon"]; ok {
		t.Errorf("daemon field present on record when caller passed empty daemon name")
	}
}

func TestNewCorrelationLogger_DropsEmptyFields(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Only RequestID is set. WakeID/AppID/etc. are zero-valued and must not
	// appear in the emitted record.
	log := wire.NewCorrelationLogger(base, wire.CorrelationFields{RequestID: "req-1"}, "vmmd")
	log.Info("hi")

	recs := decodeLines(t, &buf)
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	rec := recs[0]
	for _, absent := range []string{"wake_id", "app_id", "deployment_id", "instance_id", "invocation_id", "trace_id", "span_id"} {
		if _, ok := rec[absent]; ok {
			t.Errorf("expected field %q to be dropped, got %v", absent, rec[absent])
		}
	}
	if rec["request_id"] != "req-1" {
		t.Errorf("request_id = %v, want req-1", rec["request_id"])
	}
	if rec["daemon"] != "vmmd" {
		t.Errorf("daemon = %v, want vmmd", rec["daemon"])
	}
}

func TestWithCorrelationFields_AddsFieldsToBase(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, nil))
	// Daemon-wide envelope has daemon + version already.
	envelope := base.With("daemon", "schedd", "version", "1.2.3")

	// Per-handler enrichment adds the correlation fields on top.
	log := wire.WithCorrelationFields(envelope, wire.CorrelationFields{
		AppID: "app-1", WakeID: "wake-1",
	})
	log.Info("wake admit")

	recs := decodeLines(t, &buf)
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	rec := recs[0]
	for k, v := range map[string]string{
		"daemon":  "schedd",
		"version": "1.2.3",
		"app_id":  "app-1",
		"wake_id": "wake-1",
	} {
		if got, _ := rec[k].(string); got != v {
			t.Errorf("field %q = %q, want %q", k, got, v)
		}
	}
}

func TestContext_RoundTrip(t *testing.T) {
	ctx := context.Background()
	if _, ok := wire.FromContext(ctx); ok {
		t.Fatal("FromContext on empty ctx returned ok=true")
	}

	fields := wire.CorrelationFields{
		RequestID: "req-1",
		WakeID:    "wake-1",
	}
	ctx = wire.WithContext(ctx, fields)
	got, ok := wire.FromContext(ctx)
	if !ok {
		t.Fatal("FromContext after WithContext returned ok=false")
	}
	if got != fields {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, fields)
	}
}

func TestFromContext_NilSafe(t *testing.T) {
	var nilCtx context.Context
	if _, ok := wire.FromContext(nilCtx); ok {
		t.Fatal("FromContext(nil) returned ok=true")
	}
	// WithContext on nil ctx returns a non-nil ctx (the helper
	// substitutes context.Background()).
	got := wire.WithContext(nilCtx, wire.CorrelationFields{RequestID: "x"})
	if got == nil {
		t.Fatal("WithContext(nil) returned nil ctx")
	}
}

func TestFromContext_EmptyStructIsMissing(t *testing.T) {
	// A struct stored with zero-value fields must NOT be reported as
	// present, otherwise downstream callers would log an empty envelope
	// as if it were a real correlation set.
	ctx := wire.WithContext(context.Background(), wire.CorrelationFields{})
	if _, ok := wire.FromContext(ctx); ok {
		t.Fatal("FromContext on zero-valued fields returned ok=true")
	}
}

// TestWithCorrelationFields_SanitizesControlChars pins the PR-A review
// feedback (item 7): correlation IDs cross protocol boundaries
// (HTTP headers, gRPC MD, proto fields) and any of those can carry
// control chars / newlines per CLAUDE.md §11. logsanitize.Field
// replaces 0x00-0x1F and 0x7F with U+00B7; plain ASCII passes through
// unchanged so this test uses an embedded newline to force the
// sanitize path.
func TestWithCorrelationFields_SanitizesControlChars(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	log := wire.WithCorrelationFields(base, wire.CorrelationFields{
		RequestID:    "req\nINJECT",
		WakeID:       "wake\x00null",
		AppID:        "app-1",
		InstanceID:   "ins\x7Fdel",
		InvocationID: "inv-1",
		DeploymentID: "dep-1",
	})
	log.Info("hello")

	recs := decodeLines(t, &buf)
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	rec := recs[0]
	// Embedded control chars become U+00B7 (middle dot) per
	// logsanitize.Field. The literal strings must NOT appear in the
	// emitted record.
	for k, raw := range map[string]string{
		"request_id":  "req\nINJECT",
		"wake_id":     "wake\x00null",
		"instance_id": "ins\x7Fdel",
	} {
		got, _ := rec[k].(string)
		if got == raw {
			t.Errorf("field %q emitted raw control bytes: %q", k, got)
		}
	}
	// Plain ASCII fields pass through unchanged.
	for k, want := range map[string]string{
		"app_id":        "app-1",
		"deployment_id": "dep-1",
		"invocation_id": "inv-1",
	} {
		if got, _ := rec[k].(string); got != want {
			t.Errorf("field %q = %q, want %q", k, got, want)
		}
	}
}
