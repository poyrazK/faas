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
