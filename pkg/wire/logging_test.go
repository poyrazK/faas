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
