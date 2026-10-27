// adr: 20261027090010
package mail

import (
	"strings"
	"testing"
	"time"
)

func TestAgentRunDeliveredBodyKeepsLinesAndRedacts(t *testing.T) {
	subject, body := AgentRunDeliveredBodyV1(AgentRunDelivered{Agent: "triage", RunID: "0b6a3c2e-8f47-4d3b-9a1e-2c5d7e9f1a3b",
		Summary: "Two PRs need review.\r\n- #41\n- #42\x1b[31m\nAuthorization: Bearer abc.def.ghi", Truncated: true,
		FinishedAt: time.Date(2026, 10, 21, 9, 30, 0, 0, time.UTC)})
	if subject != "Agent triage finished a run" {
		t.Fatal(subject)
	}
	for _, want := range []string{"Two PRs need review.\n- #41\n- #42", "2026-10-21 09:30 UTC", "shortened",
		"GET /v1/agent-runs/0b6a3c2e-8f47-4d3b-9a1e-2c5d7e9f1a3b/outputs"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "abc.def.ghi") || strings.ContainsAny(body, "\r\x1b") {
		t.Fatalf("body kept a secret or a control character:\n%q", body)
	}
}
