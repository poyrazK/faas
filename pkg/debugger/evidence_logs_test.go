package debugger

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestEvidenceLogsRedactsMessages(t *testing.T) {
	got := EvidenceLogs([]EvidenceLogInput{{OccurredAt: time.Now(), Source: "runtime",
		Message: "charge failed for alice@example.com card 4111 1111 1111 1111"}})
	if strings.Contains(got[0].Message, "alice@example.com") || strings.Contains(got[0].Message, "4111 1111") {
		t.Fatalf("message not redacted: %q", got[0].Message)
	}
	if !strings.Contains(got[0].Message, "[REDACTED:email]") || !strings.Contains(got[0].Message, "charge failed") {
		t.Fatalf("redaction removed context: %q", got[0].Message)
	}
}

// ADR-127 §7.1: the synthesis layer never sees log text.
func TestSynthesisLogsDropsMessages(t *testing.T) {
	for _, line := range synthesisLogs([]api.DebugEvidenceLogLine{{Level: "error", Source: "runtime", Message: "secret detail"}}) {
		if line.Message != "" || line.Source != "" || line.Level != "error" {
			t.Fatalf("synthesis input = %+v, want level only", line)
		}
	}
}
func TestEvidenceLogsKeepsLastLinesInOrder(t *testing.T) {
	base := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	var events []EvidenceLogInput
	// The store returns newest first; 25 lines, two with sub-second times.
	for i := 24; i >= 0; i-- {
		events = append(events, EvidenceLogInput{OccurredAt: base.Add(time.Duration(i) * 100 * time.Millisecond), Source: "runtime", Level: "INFO", Message: "line " + string(rune('a'+i)) + "\n"})
	}
	got := EvidenceLogs(events)
	if len(got) != EvidenceLogMaxLines {
		t.Fatalf("lines = %d, want %d", len(got), EvidenceLogMaxLines)
	}
	if got[0].Message != "line f" || got[len(got)-1].Message != "line y" {
		t.Fatalf("kept %q..%q, want the last 20 lines in order", got[0].Message, got[len(got)-1].Message)
	}
	for i := 1; i < len(got); i++ {
		prev, _ := time.Parse(time.RFC3339Nano, got[i-1].OccurredAt)
		cur, _ := time.Parse(time.RFC3339Nano, got[i].OccurredAt)
		if cur.Before(prev) {
			t.Fatalf("lines out of order at %d: %s before %s", i, got[i].OccurredAt, got[i-1].OccurredAt)
		}
	}
	if got[0].Level != "info" {
		t.Fatalf("level = %q, want lower-cased", got[0].Level)
	}
}

func TestEvidenceLogsBoundsMessages(t *testing.T) {
	got := EvidenceLogs([]EvidenceLogInput{{OccurredAt: time.Now(), Source: "runtime", Message: strings.Repeat("x", 4096)}})
	if len(got) != 1 || len(got[0].Message) > EvidenceLogMaxMessageBytes {
		t.Fatalf("message length = %d, want ≤ %d", len(got[0].Message), EvidenceLogMaxMessageBytes)
	}
	if empty := EvidenceLogs(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("no events must encode as an empty array, got %#v", empty)
	}
}

func TestSynthesizeReportsErrorLogs(t *testing.T) {
	evidence := api.DebugRequestEvidenceResponse{
		Request: api.DebugTelemetryRequestItem{Status: 200},
		Logs: []api.DebugEvidenceLogLine{
			{Level: "info", Message: "starting"},
			{Level: "error", Message: "payment declined"},
			{Level: "FATAL", Message: "giving up"},
		},
	}
	got := Synthesize(evidence)
	var detail string
	for _, finding := range got.Findings {
		if finding.Code == "error_logs" {
			detail = finding.Detail
		}
	}
	if !strings.Contains(detail, "2 error-level") {
		t.Fatalf("error_logs finding = %q, want 2 error-level lines", detail)
	}
	if strings.Contains(detail, "payment declined") {
		t.Fatalf("finding prose must not quote log messages: %q", detail)
	}
	if hasFinding(Synthesize(api.DebugRequestEvidenceResponse{Logs: []api.DebugEvidenceLogLine{{Level: "warn"}}}), "error_logs") {
		t.Fatal("warnings are not error lines")
	}
}
