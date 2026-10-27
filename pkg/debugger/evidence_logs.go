package debugger

import (
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/redact"
)

// Request evidence carries the log lines a request emitted, matched by its
// trace ID, so the evidence answers "what did the app say" without a
// separate log search.
const (
	// EvidenceLogMaxLines bounds the lines attached to one request's evidence.
	EvidenceLogMaxLines = 20
	// EvidenceLogMaxMessageBytes bounds each line's message.
	EvidenceLogMaxMessageBytes = 512
	// EvidenceLogWindowBefore and EvidenceLogWindowAfter bound the log read
	// around the request's receive time (plus its latency), keeping it on
	// the time-partitioned log index.
	EvidenceLogWindowBefore = time.Minute
	EvidenceLogWindowAfter  = 5 * time.Minute
)

// EvidenceLogInput is the subset of a stored log event the evidence uses.
// Structured fields are deliberately excluded.
type EvidenceLogInput struct {
	OccurredAt time.Time
	Source     string
	Level      string
	Stream     string
	Message    string
}

// EvidenceLogs redacts and bounds log events for request evidence and returns
// them in chronological order. Emails, card numbers, tokens, keys and other
// credentials in a message are replaced with "[REDACTED:kind]" by the ADR-096
// redactor that also guards app-error sample messages, before the message is
// bounded, so evidence stays a redacted envelope. When a request logged more
// than the limit the evidence keeps its last lines, the ones nearest a
// failure.
func EvidenceLogs(events []EvidenceLogInput) []api.DebugEvidenceLogLine {
	events = append([]EvidenceLogInput(nil), events...)
	sort.SliceStable(events, func(i, j int) bool { return events[i].OccurredAt.After(events[j].OccurredAt) })
	if len(events) > EvidenceLogMaxLines {
		events = events[:EvidenceLogMaxLines]
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].OccurredAt.Before(events[j].OccurredAt) })
	out := make([]api.DebugEvidenceLogLine, 0, len(events))
	redactor := redact.New(evidenceLogRedactorCap)
	for _, event := range events {
		out = append(out, api.DebugEvidenceLogLine{
			OccurredAt: event.OccurredAt.UTC().Format(time.RFC3339Nano),
			Source:     boundDebugEvidenceText(strings.TrimSpace(event.Source), 32),
			Level:      boundDebugEvidenceText(strings.ToLower(strings.TrimSpace(event.Level)), 16),
			Stream:     boundDebugEvidenceText(strings.TrimSpace(event.Stream), 16),
			Message:    boundDebugEvidenceText(redactEvidenceLogMessage(redactor, event.Message), EvidenceLogMaxMessageBytes),
		})
	}
	return out
}

// evidenceLogRedactorCap bounds the text the redactor scans; the result is
// then cut to EvidenceLogMaxMessageBytes.
const evidenceLogRedactorCap = 4096

func redactEvidenceLogMessage(redactor *redact.Redactor, message string) string {
	redacted, _ := redactor.Apply(strings.TrimRight(message, "\r\n"))
	return redacted
}

// synthesisLogs keeps only what the rules read from log lines. ADR-127 §7.1:
// no raw log text reaches the synthesis layer, even though evidence carries
// the request's redacted lines for the customer.
func synthesisLogs(lines []api.DebugEvidenceLogLine) []api.DebugEvidenceLogLine {
	out := make([]api.DebugEvidenceLogLine, 0, len(lines))
	for _, line := range lines {
		out = append(out, api.DebugEvidenceLogLine{OccurredAt: line.OccurredAt, Level: line.Level})
	}
	return out
}

// errorLogLevel reports whether a log level marks an error.
func errorLogLevel(level string) bool {
	switch strings.ToLower(level) {
	case "error", "err", "fatal", "critical", "crit", "panic", "alert", "emerg", "emergency":
		return true
	default:
		return false
	}
}
