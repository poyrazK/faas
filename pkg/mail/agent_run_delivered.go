package mail

import (
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/redact"
)

// AgentRunDelivered is an agent run's summary, mailed to the account's own
// address (ADR-20261027090010 §8). Summary is the run's chosen result.json string, cut
// to the outbox's bound; the full outputs stay behind the authenticated API.
type AgentRunDelivered struct {
	Agent      string
	RunID      string
	Summary    string
	Truncated  bool
	FinishedAt time.Time
}

// AgentRunSummaryText is the summary as mailed: secrets redacted (ADR-096)
// and each line's control characters neutralized, keeping line breaks.
func AgentRunSummaryText(v string) string {
	v, _ = redact.New(8192).Apply(v)
	lines := strings.Split(strings.ReplaceAll(v, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = logsanitize.Field(line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// AgentRunDeliveredBodyV1 freezes the durable notification's rendering.
func AgentRunDeliveredBodyV1(d AgentRunDelivered) (subject, body string) {
	agent := logsanitize.Field(d.Agent)
	subject = fmt.Sprintf("Agent %s finished a run", agent)
	var b strings.Builder
	fmt.Fprintf(&b, "Hi,\n\nYour agent %q finished run %s at %s.\n\n", agent, logsanitize.Field(d.RunID),
		d.FinishedAt.UTC().Format("2006-01-02 15:04 UTC"))
	if summary := AgentRunSummaryText(d.Summary); summary != "" {
		b.WriteString(summary)
		b.WriteString("\n\n")
	}
	if d.Truncated {
		b.WriteString("The summary was shortened for email.\n")
	}
	fmt.Fprintf(&b, "The run's full outputs: GET /v1/agent-runs/%s/outputs\n\n— Gregale\n", logsanitize.Field(d.RunID))
	return subject, b.String()
}
