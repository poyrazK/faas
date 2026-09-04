package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestRenderWakeTimelinePage_TriggersAndContext pins the ADR-123
// human output shape: every wake.boot_started / wake.boot_completed
// row gains a trailing `trigger=… q=N c=N` line; the page header
// gains a `triggers: foo=N bar=M` histogram. Stable ordering across
// runs (sort.Strings) keeps the golden-file check simple.
func TestRenderWakeTimelinePage_TriggersAndContext(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	resp := api.WakeTimelineResponse{
		WakeID: "wake-1",
		AppID:  "app-1",
		Limit:  10,
		Events: []api.WakeTimelineEvent{
			{At: now, Kind: "wake.boot_started", Actor: "schedd",
				Data: map[string]any{
					"trigger":              "gateway",
					"queued_count":         float64(3),
					"concurrency_at_admit": float64(2),
				}},
			{At: now, Kind: "wake.boot_completed", Actor: "schedd",
				Data: map[string]any{
					"trigger":              "gateway",
					"queued_count":         float64(3),
					"concurrency_at_admit": float64(2),
				}},
		},
	}
	var buf bytes.Buffer
	renderWakeTimelinePage(&buf, resp)
	out := buf.String()
	for _, want := range []string{
		"wake wake-1 app app-1 limit 10:",
		"triggers: gateway=1", // summary aggregates by wake.boot_started
		"trigger=gateway q=3 c=2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// TestRenderSummaryHeader_SortedByKey pins the deterministic key
// order (sort.Strings) so the CLI output is grep-able / golden-friendly.
func TestRenderSummaryHeader_SortedByKey(t *testing.T) {
	events := []api.WakeTimelineEvent{
		{Kind: "wake.boot_started", Data: map[string]any{"trigger": "scaleup"}},
		{Kind: "wake.boot_started", Data: map[string]any{"trigger": "gateway"}},
		{Kind: "wake.boot_started", Data: map[string]any{"trigger": "cron.schedule"}},
		{Kind: "wake.boot_completed", Data: map[string]any{"trigger": "ignored"}},
	}
	var buf bytes.Buffer
	renderSummaryHeader(&buf, events)
	got := buf.String()
	want := "  triggers: cron.schedule=1 gateway=1 scaleup=1\n"
	if got != want {
		t.Errorf("renderSummaryHeader = %q, want %q", got, want)
	}
}

// TestRenderSummaryHeader_AbsentNoop verifies the histogram is
// suppressed when no wake.boot_started event carries a trigger
// (pre-ADR-123 fleet or events emitted by an older schedd).
func TestRenderSummaryHeader_AbsentNoop(t *testing.T) {
	events := []api.WakeTimelineEvent{
		{Kind: "wake.boot_completed", Data: map[string]any{}},
	}
	var buf bytes.Buffer
	renderSummaryHeader(&buf, events)
	if buf.Len() != 0 {
		t.Errorf("expected no header line, got %q", buf.String())
	}
}

// TestRenderContextSuffix_LegacyEvent pins the legacy event shape:
// a wake.boot_started without ADR-123 fields renders no trailing
// context line (so the human output stays byte-identical for
// pre-ADR-123 events).
func TestRenderContextSuffix_LegacyEvent(t *testing.T) {
	ev := api.WakeTimelineEvent{Kind: "wake.boot_started", Data: map[string]any{}}
	if got := renderContextSuffix(ev); got != "" {
		t.Errorf("legacy event rendered context %q, want \"\"", got)
	}
}

// TestRenderContextSuffix_TriggerOnly covers the cron branch:
// trigger stamped, but queue/concurrency both zero (cron-driven
// cold boot has no waiting-requests and no siblings).
func TestRenderContextSuffix_TriggerOnly(t *testing.T) {
	ev := api.WakeTimelineEvent{
		Kind: "wake.boot_started",
		Data: map[string]any{"trigger": "cron.schedule"},
	}
	got := renderContextSuffix(ev)
	if got != "trigger=cron.schedule" {
		t.Errorf("renderContextSuffix = %q, want %q", got, "trigger=cron.schedule")
	}
}

func TestRenderRestoreBreakdown_ExactTotalAndPhases(t *testing.T) {
	ev := api.WakeTimelineEvent{
		Kind: "wake.restore_breakdown",
		Data: map[string]any{
			"total_ms":           float64(596),
			"load_snapshot_ms":   float64(400),
			"wait_ready_ms":      float64(131),
			"materialize_mem_ms": float64(3),
		},
	}
	got := renderRestoreBreakdown(ev)
	for _, want := range []string{
		"restore total=596ms",
		"materialize_mem=3ms",
		"load_snapshot=400ms",
		"wait_ready=131ms",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("renderRestoreBreakdown missing %q in %q", want, got)
		}
	}
}

func TestRenderWakeTimelinePage_DefaultSummaryHidesInternalPhases(t *testing.T) {
	resp := api.WakeTimelineResponse{
		WakeID: "wake-restore", AppID: "app-restore", Limit: 50,
		Events: []api.WakeTimelineEvent{
			{At: "2026-08-31T12:13:57.696394Z", Kind: "wake.queue_accepted", Actor: "schedd"},
			{At: "2026-08-31T12:13:57.699179Z", Kind: "wake.admitted", Actor: "schedd",
				Data: map[string]any{"admitted_at": "2026-08-31T12:13:57.699179Z"}},
			{At: "2026-08-31T12:13:58.302741Z", Kind: "wake.restore_breakdown", Actor: "vmmd",
				Data: map[string]any{"total_ms": float64(596), "materialize_mem_ms": float64(3)}},
			{At: "2026-08-31T12:13:58.302741Z", Kind: "wake.readiness_200", Actor: "vmmd",
				Data: map[string]any{"elapsed_ms": float64(131)}},
			{At: "2026-08-31T12:13:58.302741Z", Kind: "wake.boot_completed", Actor: "schedd",
				Data: map[string]any{
					"started_at": "2026-08-31T12:13:57.696394Z", "completed_at": "2026-08-31T12:13:58.302741Z",
				}},
		},
	}
	var buf bytes.Buffer
	renderWakeTimelinePage(&buf, resp)
	out := buf.String()
	if !strings.Contains(out, "cold start: queue_to_admit=2ms restore=596ms readiness=131ms wake_to_running=606ms") {
		t.Errorf("default output missing compact summary:\n%s", out)
	}
	if strings.Contains(out, "materialize_mem=3ms") {
		t.Errorf("default output exposed internal restore phase:\n%s", out)
	}

	buf.Reset()
	renderWakeTimelinePageWithOptions(&buf, resp, true)
	if !strings.Contains(buf.String(), "materialize_mem=3ms") {
		t.Errorf("verbose output missing internal restore phase:\n%s", buf.String())
	}
}
