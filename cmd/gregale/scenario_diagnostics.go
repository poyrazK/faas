package main

import (
	"context"
	"fmt"

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

func collectTestDiagnostics(ctx context.Context, client testDiagnosticsClient, slugs map[string]string) map[string]testWorkloadDiagnostics {
	results := make(map[string]testWorkloadDiagnostics, len(slugs))
	for name, slug := range slugs {
		result := testWorkloadDiagnostics{}
		wakes, err := client.GetAppWakeTimeline(ctx, slug, api.AppWakeTimelineOptions{})
		if err != nil {
			result.CollectionError = fmt.Sprintf("wake timeline: %v", err)
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
			result.CollectionError += fmt.Sprintf("request telemetry: %v", err)
		} else {
			for _, request := range requests.Requests {
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
		results[name] = result
	}
	return results
}
