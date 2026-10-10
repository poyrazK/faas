package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestHostingDoctorFailsClosedWithoutLocalEvidence(t *testing.T) {
	oldOut, oldJSON := osStdout, jsonOutput
	var output bytes.Buffer
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	result := mcpTasksStatusResult{AppSlug: "worker", Configured: true, ScaleToZeroConfigured: true, Metrics: []mcpTasksMetricStatus{{Name: "mcp_tasks_observer_heartbeat", Present: true, Value: 0}}}
	result.diagnose()
	if code := printMCPHostingDoctor(result, ""); code != 1 {
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
