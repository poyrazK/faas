package main

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Failure diagnostics contain bounded platform metadata. They never include
// request bodies, headers, app secrets, consumer keys, or the operator token.
type testWorkloadDiagnostics struct {
	Wakes           []api.WakeTimelineJSONRow `json:"wakes,omitempty"`
	Requests        []testDiagnosticRequest   `json:"requests,omitempty"`
	CollectionError string                    `json:"collection_error,omitempty"`
}

type testDiagnosticRequest struct {
	RequestID  string `json:"request_id,omitempty"`
	TraceID    string `json:"trace_id,omitempty"`
	ReceivedAt string `json:"received_at,omitempty"`
	WakeID     string `json:"wake_id,omitempty"`
	InstanceID string `json:"instance_id,omitempty"`
	Method     string `json:"method,omitempty"`
	Route      string `json:"route,omitempty"`
	Status     int    `json:"status,omitempty"`
}

type testDiagnosticsClient interface {
	GetAppWakeTimeline(context.Context, string, api.AppWakeTimelineOptions) (api.AppWakeTimelineResponse, error)
	ListAppDebugRequestsWithOptions(context.Context, string, api.DebugTelemetryListOptions) (api.DebugTelemetryListResponse, error)
}

func collectTestDiagnostics(ctx context.Context, client testDiagnosticsClient, slugs map[string]string, redactions ...[]string) map[string]testWorkloadDiagnostics {
	var secrets []string
	if len(redactions) > 0 {
		secrets = testDiagnosticRedactions(redactions[0])
	}
	results := make(map[string]testWorkloadDiagnostics, len(slugs))
	for _, name := range testEndpointRecoveryNames(slugs) {
		if len(results) >= testFailureDiagnosticMaxWorkloads {
			break
		}
		slug := slugs[name]
		result := testWorkloadDiagnostics{}
		wakes, err := client.GetAppWakeTimeline(ctx, slug, api.AppWakeTimelineOptions{})
		if err != nil {
			result.CollectionError = "wake timeline unavailable"
		} else {
			result.Wakes = wakes.Rows
			if len(result.Wakes) > 20 {
				result.Wakes = result.Wakes[:20]
			}
		}
		requests, err := client.ListAppDebugRequestsWithOptions(ctx, slug, api.DebugTelemetryListOptions{Since: "1h", Limit: 30})
		if err != nil {
			if result.CollectionError != "" {
				result.CollectionError += "; "
			}
			result.CollectionError += "request telemetry unavailable"
		} else {
			for i, request := range requests.Requests {
				if i >= 30 {
					break
				}
				traceID := ""
				if request.TraceID != nil {
					traceID = *request.TraceID
				}
				result.Requests = append(result.Requests, testDiagnosticRequest{
					RequestID: request.RequestID, TraceID: traceID, ReceivedAt: request.ReceivedAt,
					WakeID: request.WakeID, InstanceID: request.InstanceID, Method: request.Method,
					Route: request.Route, Status: request.Status,
				})
			}
		}
		for i := range result.Wakes {
			row := &result.Wakes[i]
			row.WakeID = redactTestDiagnosticText(row.WakeID, secrets, 128)
			row.Kind = redactTestDiagnosticText(row.Kind, secrets, 128)
			row.State = redactTestDiagnosticText(row.State, secrets, 128)
			row.At = redactTestDiagnosticText(row.At, secrets, 128)
			row.Trigger = ""
			row.TriggerClass = redactTestDiagnosticText(row.TriggerClass, secrets, 128)
			row.Method = redactTestDiagnosticText(row.Method, secrets, 128)
			row.Tier = redactTestDiagnosticText(row.Tier, secrets, 128)
		}
		for i := range result.Requests {
			row := &result.Requests[i]
			row.RequestID = redactTestDiagnosticText(row.RequestID, secrets, 128)
			row.TraceID = redactTestDiagnosticText(row.TraceID, secrets, 128)
			row.ReceivedAt = redactTestDiagnosticText(row.ReceivedAt, secrets, 128)
			row.WakeID = redactTestDiagnosticText(row.WakeID, secrets, 128)
			row.InstanceID = redactTestDiagnosticText(row.InstanceID, secrets, 128)
			row.Method = redactTestDiagnosticText(row.Method, secrets, 16)
			row.Route = redactTestDiagnosticText(strings.SplitN(strings.SplitN(row.Route, "?", 2)[0], "#", 2)[0], secrets, 128)
		}
		results[name] = result
	}
	return results
}
