package main_test

import (
	"bytes"
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"testing"
)

func TestHostingDoctorFailsClosedWithoutLocalEvidence(t *testing.T) {
	oldOut, oldJSON := (*main.OsStdout), (*main.JsonOutput)
	var output bytes.Buffer
	(*main.OsStdout), (*main.JsonOutput) = &output, true
	t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = oldOut, oldJSON })
	result := main.McpTasksStatusResult{AppSlug: "worker", Configured: true, ScaleToZeroConfigured: true, Metrics: []main.McpTasksMetricStatus{{Name: "mcp_tasks_observer_heartbeat", Present: true, Value: 0}}}
	result.Diagnose()
	if code := main.PrintMCPHostingDoctor(result, ""); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	var report struct {
		OK     bool                            `json:"ok"`
		Checks []struct{ Name, Status string } `json:"checks"`
	}
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatal("unknown local bindings passed readiness")
	}
	foundDatabase, foundObserver := false, false
	for _, check := range report.Checks {
		if check.Name == "database_and_keys" && check.Status == "unknown" {
			foundDatabase = true
		}
		if check.Name == "scale_to_zero_observer" && check.Status == "failed" {
			foundObserver = true
		}
	}
	if !foundDatabase || !foundObserver {
		t.Fatalf("missing required failures: %s", output.String())
	}
}
