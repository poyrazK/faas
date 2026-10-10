package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAutomationHealthRunHintsPreserveWindowAndFailureScope(t *testing.T) {
	start, _ := time.Parse(time.RFC3339Nano, "2026-10-01T03:00:00.123456789+03:00")
	end, _ := time.Parse(time.RFC3339Nano, "2026-10-05T03:00:00+03:00")
	health := api.AutomationHealthResponse{AppSlug: "billing", AutomationName: "approval-flow", WindowStart: start, WindowEnd: end, StatusCounts: map[string]int64{"failed": 2, "dead": 1}, LastFailure: &api.AutomationHealthRun{ID: inspectorRunID, Status: "dead"}}
	var output bytes.Buffer
	if err := writeAutomationHealthRunHints(&output, health); err != nil {
		t.Fatal(err)
	}
	base := "gregale automations runs --app billing --name approval-flow --created-after 2026-10-01T00:00:00.123456789Z --created-before 2026-10-05T00:00:00Z"
	for _, command := range []string{base, base + " --status failed", base + " --status dead", "gregale automations diagnose --app billing --name approval-flow --run " + inspectorRunID} {
		if !strings.Contains(output.String(), command) {
			t.Fatalf("missing %q in %s", command, output.String())
		}
	}
	health.StatusCounts, health.LastFailure = nil, nil
	output.Reset()
	_ = writeAutomationHealthRunHints(&output, health)
	if !strings.Contains(output.String(), base) || strings.Contains(output.String(), "--status") || strings.Contains(output.String(), " diagnose ") {
		t.Fatalf("healthy hints=%s", output.String())
	}
	for _, mode := range []string{"bad-name", "missing-window", "reversed-window"} {
		copy := health
		switch mode {
		case "bad-name":
			copy.AutomationName = "../other"
		case "missing-window":
			copy.WindowStart = time.Time{}
		case "reversed-window":
			copy.WindowStart = copy.WindowEnd.Add(time.Hour)
		}
		output.Reset()
		_ = writeAutomationHealthRunHints(&output, copy)
		if output.Len() != 0 {
			t.Fatalf("invalid %s offered commands: %s", mode, output.String())
		}
	}
}

func TestAutomationCommandArgumentsAreQuoted(t *testing.T) {
	for value, want := range map[string]string{"billing": "billing", "approval-flow": "approval-flow", "name with spaces": "'name with spaces'", "name'quoted": "'name'\"'\"'quoted'", "$(touch /tmp/bad)": "'$(touch /tmp/bad)'"} {
		if got := automationCommandArgument(value); got != want {
			t.Fatalf("quote %q=%q want=%q", value, got, want)
		}
	}
}
