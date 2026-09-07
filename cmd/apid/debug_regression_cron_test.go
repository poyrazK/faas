package main

import (
	"math"
	"testing"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestCountAffectedRequestsWeightsCollapsedRows(t *testing.T) {
	rows := []sqlc.RequestTelemetryByDeploymentRow{
		{Route: "GET /slow", LatencyMs: 201, Count: 100},
		{Route: "GET /slow", LatencyMs: 200, Count: 2},
		{Route: "GET /slow", LatencyMs: 200, Count: 0}, // legacy row → one request
		{Route: "GET /slow", LatencyMs: 100, Count: 999},
		{Route: "GET /other", LatencyMs: 500, Count: 1000},
	}

	if got := countAffectedRequests(rows, "GET /slow", 200); got != 100 {
		t.Fatalf("countAffectedRequests = %d, want 100", got)
	}
}

func TestCountAffectedRequestsSaturatesInt32(t *testing.T) {
	rows := []sqlc.RequestTelemetryByDeploymentRow{
		{Route: "GET /slow", LatencyMs: 201, Count: math.MaxInt32},
		{Route: "GET /slow", LatencyMs: 202, Count: math.MaxInt32},
	}

	if got := countAffectedRequests(rows, "GET /slow", 200); got != math.MaxInt32 {
		t.Fatalf("countAffectedRequests = %d, want %d", got, math.MaxInt32)
	}
}
