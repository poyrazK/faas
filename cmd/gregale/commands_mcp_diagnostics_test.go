package main_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPDiagnosticsCountsAndPrivacy(t *testing.T) {
	page := api.DebugTelemetryListResponse{Complete: false, NextCursor: "next", Requests: []api.DebugTelemetryRequestItem{
		{ID: "row1", RequestID: "public-request", Status: 401, Count: 3, LatencyMS: 12, Guest: &api.DebugGuestExecutionEvidence{ErrorClass: "PRIVATE_APP_DATA"}},
		{ID: "row2", Status: 200, Count: 8},
		{ID: "row3", Status: 504, Count: 1},
	}}
	report := main.SummarizeMCPDiagnostics("app", "/mcp", page)
	if report.RepresentedRequests != 12 || report.Outcomes["unauthorized"] != 3 || len(report.Failures) != 2 || report.Complete || report.NextCursor != "next" {
		t.Fatalf("%+v", report)
	}
	if report.Failures[0].InspectPath != "/v1/apps/app/debug/requests/public-request/evidence" || report.Failures[1].InspectPath != "/v1/apps/app/debug/requests/row3/evidence" {
		t.Fatal("invalid inspection paths")
	}
	raw, _ := json.Marshal(report)
	if strings.Contains(string(raw), "PRIVATE_APP_DATA") {
		t.Fatal("private guest details included")
	}
}
