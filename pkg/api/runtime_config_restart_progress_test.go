package api

import (
	"strings"
	"testing"
)

func TestRestartProgressSeparatesFailurePendingAndHealth(t *testing.T) {
	for _, status := range []string{"retrying", "running", "failed", "completed", "unknown"} {
		row := RuntimeConfigRestartStatusResponse{Status: status, FailureReason: "requests_active"}
		message, next := row.ProgressMessage(), row.ProgressNextStep()
		if message == "" || next == "" {
			t.Fatalf("missing explanation: %s", status)
		}
		if status == "retrying" && !strings.Contains(message, "Waiting for active requests") {
			t.Fatal("pending drain has no useful explanation")
		}
		if status != "retrying" && strings.HasPrefix(message, "Waiting") {
			t.Fatalf("stale failure mistaken for pending work: %s %s", status, message)
		}
		if status == "completed" && !strings.Contains(message, "health has not been verified") {
			t.Fatal("processing completion declared application health")
		}
		if status == "failed" && !strings.Contains(next, "before requesting another restart") {
			t.Fatal("failed request encouraged blind replay")
		}
	}
}

func TestRestartProgressNamesAStartupCrash(t *testing.T) {
	for _, status := range []string{"retrying", "failed"} {
		row := RuntimeConfigRestartStatusResponse{Status: status, FailureReason: CodeAppStartupCrash}
		if message := row.ProgressMessage(); !strings.Contains(message, "crashed during startup") {
			t.Fatalf("%s: message %q does not name the startup crash", status, message)
		}
		if next := row.ProgressNextStep(); !strings.Contains(next, "gregale env history") {
			t.Fatalf("%s: next step %q does not point at config history", status, next)
		}
	}
}
