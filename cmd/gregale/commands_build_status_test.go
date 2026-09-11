package main

// Test file for issue #741 / DEPLOY-PROV-6 — pins the
// `gregale build status <id>` rendering. Mirrors the existing
// printProvenance test pattern (whitebox, package main) so
// silent reordering of the status/lifecycle rows surfaces here.
// The minimum viable test set covers: (a) terminal "succeeded"
// rendering with all fields populated, (b) pre-Phase-3 queued
// rendering with empty optional fields, (c) failure rendering
// with failure_class populated, (d) row-order pin (status
// before duration_seconds; enqueued_at before the two started/
// finished rows).

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestPrintBuildStatus_TerminalSucceeded pins a fully-populated
// succeeded build row. Duration_seconds is server-computed
// (ADR-089 §3); here we pin the formatted output as "82" so a
// future refactor that loses the strconv.Itoa path trips here.
func TestPrintBuildStatus_TerminalSucceeded(t *testing.T) {
	var buf bytes.Buffer
	printBuildStatus(&buf, api.BuildResponse{
		ID:              "00000000000000000000000000000000",
		DeploymentID:    "00000000000000000000000000000001",
		Kind:            "railpack",
		Status:          "succeeded",
		CacheStatus:     "hit",
		CacheKeySHA256:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceBytes:     12345,
		EnqueuedAt:      "2026-08-10T12:34:56Z",
		StartedAt:       "2026-08-10T12:34:58Z",
		FinishedAt:      "2026-08-10T12:36:20Z",
		DurationSeconds: 82,
	})
	out := buf.String()
	for _, want := range []string{
		"id:                    00000000000000000000000000000000",
		"deployment_id:         00000000000000000000000000000001",
		"kind:                  railpack",
		"status:                succeeded",
		"cache_status:          hit",
		"cache_key_sha256:      aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"source_bytes:          12345",
		"enqueued_at:           2026-08-10T12:34:56Z",
		"started_at:            2026-08-10T12:34:58Z",
		"finished_at:           2026-08-10T12:36:20Z",
		"duration_seconds:      82",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected line %q in output; got:\n%s", want, out)
		}
	}
}

// TestFormatBuildCacheSummary pins the human deploy receipt wording while
// keeping the full digest available to operators. Empty status is omitted so
// pre-cache rows remain compatible with older API responses.
func TestFormatBuildCacheSummary(t *testing.T) {
	const key = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	tests := []struct {
		name, status, key, want string
	}{
		{name: "hit with key", status: "hit", key: key, want: "hit (sha256:" + key + ")"},
		{name: "miss without key", status: "miss", want: "miss"},
		{name: "invalidated with key", status: "invalidated", key: key, want: "invalidated (sha256:" + key + ")"},
		{name: "empty status", key: key, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatBuildCacheSummary(tt.status, tt.key); got != tt.want {
				t.Fatalf("formatBuildCacheSummary(%q, %q) = %q, want %q", tt.status, tt.key, got, tt.want)
			}
		})
	}
}

// TestPrintBuildStatus_PreRunning pins a queued build (no
// started_at, no finished_at, no duration_seconds). failure_class
// must be absent — the source DTO uses omitempty so empty values
// render as blank rows (line consistency for auditors), but
// optional fields like failure_class only appear when populated.
// Reads as: the customer shouldn't see "failure_class:" at all
// when status=queued and no failure has been recorded.
func TestPrintBuildStatus_PreRunning(t *testing.T) {
	var buf bytes.Buffer
	printBuildStatus(&buf, api.BuildResponse{
		ID:           "11111111111111111111111111111111",
		DeploymentID: "22222222222222222222222222222222",
		Kind:         "tarball",
		Status:       "queued",
		SourceBytes:  0,
		EnqueuedAt:   "2026-08-10T12:00:00Z",
	})
	out := buf.String()
	if !strings.Contains(out, "status:                queued") {
		t.Errorf("expected status: queued row; got:\n%s", out)
	}
	if !strings.Contains(out, "enqueued_at:           2026-08-10T12:00:00Z") {
		t.Errorf("expected enqueued_at row; got:\n%s", out)
	}
	// Optional rows must render as blank lines, not be skipped —
	// mirrors the empty-buildkit_version convention.
	if !strings.Contains(out, "started_at:") {
		t.Errorf("expected started_at row even when empty; got:\n%s", out)
	}
	if !strings.Contains(out, "finished_at:") {
		t.Errorf("expected finished_at row even when empty; got:\n%s", out)
	}
	if !strings.Contains(out, "duration_seconds:") {
		t.Errorf("expected duration_seconds row even when 0; got:\n%s", out)
	}
}

// TestPrintBuildStatus_Failed pins the failure rendering: status
// flips to "failed" and failure_class populates from the
// deployments.error_message classification
// (oom|timeout|user_error|infra). The full failure string is
// kept on deployments (ADR-089 §4) and surfaced via
// GetDeployment(deployment_id) — not here.
func TestPrintBuildStatus_Failed(t *testing.T) {
	var buf bytes.Buffer
	printBuildStatus(&buf, api.BuildResponse{
		ID:              "33333333333333333333333333333333",
		DeploymentID:    "44444444444444444444444444444444",
		Kind:            "dockerfile",
		Status:          "failed",
		FailureClass:    "timeout",
		SourceBytes:     67890,
		EnqueuedAt:      "2026-08-10T13:00:00Z",
		StartedAt:       "2026-08-10T13:00:05Z",
		FinishedAt:      "2026-08-10T13:10:05Z",
		DurationSeconds: 600,
	})
	out := buf.String()
	for _, want := range []string{
		"status:                failed",
		"failure_class:         timeout",
		"kind:                  dockerfile",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected line %q in output; got:\n%s", want, out)
		}
	}
}

// TestPrintBuildStatus_RowOrder pins the load-bearing field order:
// enqueued_at must come before started_at, which must come
// before finished_at, which must come before duration_seconds.
// A silent re-shuffle of printBuildStatus surfaces here.
func TestPrintBuildStatus_RowOrder(t *testing.T) {
	var buf bytes.Buffer
	printBuildStatus(&buf, api.BuildResponse{
		ID:         "id",
		Status:     "succeeded",
		EnqueuedAt: "2026-08-10T12:00:00Z",
		StartedAt:  "2026-08-10T12:00:05Z",
		FinishedAt: "2026-08-10T12:00:10Z",
	})
	out := buf.String()
	idxEnq := strings.Index(out, "enqueued_at:")
	idxStart := strings.Index(out, "started_at:")
	idxEnd := strings.Index(out, "finished_at:")
	idxDur := strings.Index(out, "duration_seconds:")
	if !(idxEnq < idxStart && idxStart < idxEnd && idxEnd < idxDur) {
		t.Errorf("row order: enq=%d, started=%d, finished=%d, duration=%d; want enq < started < finished < duration",
			idxEnq, idxStart, idxEnd, idxDur)
	}
}

// TestPrintBuildStatus_BlankDurationForNonTerminal pins that a
// queued or running build renders an empty duration_seconds
// value, NOT a literal `0`. Review finding #1: the server's
// omitempty JSON tag drops the field on a non-terminal build,
// leaving the Go int zero. Stringifying that zero produced a
// misleading `duration_seconds: 0` that CI scripts parsed as
// "this build took zero seconds" rather than "this build
// hasn't finished yet".
func TestPrintBuildStatus_BlankDurationForNonTerminal(t *testing.T) {
	for _, status := range []string{"queued", "running"} {
		t.Run(status, func(t *testing.T) {
			var buf bytes.Buffer
			printBuildStatus(&buf, api.BuildResponse{
				ID:         "id",
				Status:     status,
				EnqueuedAt: "2026-08-10T12:00:00Z",
				StartedAt:  "", // omitempty drops; zero string
				FinishedAt: "",
				// DurationSeconds is 0 (server omits via omitempty).
			})
			out := buf.String()
			// The row label must be present (auditor consistency,
			// mirrors empty-buildkit_version convention) but the
			// value column must be blank — not "0".
			if !strings.Contains(out, "duration_seconds:") {
				t.Errorf("missing duration_seconds row label; got:\n%s", out)
			}
			// Find the duration_seconds line and assert the
			// value column (after the label + gutter) is empty.
			idx := strings.Index(out, "duration_seconds:")
			line := out[idx:]
			if nl := strings.Index(line, "\n"); nl >= 0 {
				line = line[:nl]
			}
			// The gutter is 22 chars then a literal space then
			// the value. We expect just the gutter + label + ":"
			// + spaces with NO trailing digit.
			if strings.HasSuffix(strings.TrimSpace(line), "0") {
				t.Errorf("duration_seconds row ends with 0 (status=%s): %q", status, line)
			}
		})
	}
}

// TestPrintBuildStatus_NumericDurationForTerminal pins the
// inverse: succeeded + failed builds DO render their integer
// duration_seconds value. Without this test, the previous
// regression fix could over-correct into "always blank".
func TestPrintBuildStatus_NumericDurationForTerminal(t *testing.T) {
	for _, status := range []string{"succeeded", "failed"} {
		t.Run(status, func(t *testing.T) {
			var buf bytes.Buffer
			printBuildStatus(&buf, api.BuildResponse{
				ID:              "id",
				Status:          status,
				EnqueuedAt:      "2026-08-10T12:00:00Z",
				StartedAt:       "2026-08-10T12:00:05Z",
				FinishedAt:      "2026-08-10T12:00:10Z",
				DurationSeconds: 82,
			})
			out := buf.String()
			if !strings.Contains(out, "duration_seconds:      82") {
				t.Errorf("expected duration_seconds: 82 in output (status=%s); got:\n%s", status, out)
			}
		})
	}
}
