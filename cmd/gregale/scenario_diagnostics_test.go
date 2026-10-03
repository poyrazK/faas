package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type testDiagnosticsFake struct{}

func (testDiagnosticsFake) GetAppWakeTimeline(_ context.Context, _ string, _ api.AppWakeTimelineOptions) (api.AppWakeTimelineResponse, error) {
	return api.AppWakeTimelineResponse{Rows: []api.WakeTimelineJSONRow{{WakeID: "wake-1", Kind: "boot", State: "ready", Method: "restore"}}}, nil
}

func (testDiagnosticsFake) ListAppDebugRequestsWithOptions(_ context.Context, _ string, _ api.DebugTelemetryListOptions) (api.DebugTelemetryListResponse, error) {
	traceID := "trace-1"
	return api.DebugTelemetryListResponse{Requests: []api.DebugTelemetryRequestItem{{
		RequestID: "request-1", TraceID: &traceID, Method: "POST", Route: "/exports", Status: 503,
	}}}, nil
}

func TestScenarioFailureDiagnosticsKeepBoundedMetadata(t *testing.T) {
	results := collectTestDiagnostics(t.Context(), testDiagnosticsFake{}, map[string]string{"worker": "isolated-worker"})
	worker := results["worker"]
	if len(worker.Wakes) != 1 || worker.Wakes[0].Method != "restore" ||
		len(worker.Requests) != 1 || worker.Requests[0].TraceID != "trace-1" {
		t.Fatalf("diagnostics = %+v", results)
	}
	body, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "headers") || strings.Contains(string(body), "payload") {
		t.Fatalf("diagnostics expose request data: %s", body)
	}
}
