package main

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestBuildDebugDependencyLatencyHistoryDetectsRecentRegression(t *testing.T) {
	start := time.Date(2026, 9, 19, 8, 0, 0, 0, time.UTC)
	rows := make([]sqlc.ListRequestTelemetryDependencySpansRow, 0, 20)
	for i := 0; i < 10; i++ {
		rows = append(rows, dependencyHistoryTestRow(start.Add(time.Duration(i)*time.Minute), 100, "ok"))
	}
	for i := 0; i < 10; i++ {
		rows = append(rows, dependencyHistoryTestRow(start.Add(time.Hour+time.Duration(i)*time.Minute), 300, "error"))
	}

	got, truncated, represented, samples := buildDebugDependencyLatencyHistory(rows, start, start.Add(2*time.Hour))
	if truncated {
		t.Fatal("unexpected truncation")
	}
	if represented != 20 || samples != 20 {
		t.Fatalf("coverage = (represented=%d samples=%d), want (20, 20)", represented, samples)
	}
	if len(got) != 1 {
		t.Fatalf("got %d dependency groups, want one: %+v", len(got), got)
	}
	item := got[0]
	if item.Type != "managed_binding" || item.Kind != "managed_postgres" {
		t.Fatalf("dependency identity = %+v", item)
	}
	if item.P50MS != 100 || item.P95MS != 300 || item.P99MS != 300 {
		t.Fatalf("full-window percentiles = %+v", item)
	}
	if item.BaselineP95MS != 100 || item.CurrentP95MS != 300 || item.P95DeltaMS != 200 {
		t.Fatalf("split-window percentiles = %+v", item)
	}
	if item.RegressionFactor != 3 || !item.Regression {
		t.Fatalf("regression = %+v", item)
	}
	if item.ErrorRatePct != 50 || item.BaselineErrorRatePct != 0 || item.CurrentErrorRatePct != 100 || item.ErrorRateDeltaPct != 100 {
		t.Fatalf("error rates = %+v", item)
	}
}

func TestBuildDebugDependencyLatencyHistoryCapsGroupsAndUsesApplicationFallback(t *testing.T) {
	start := time.Date(2026, 9, 19, 8, 0, 0, 0, time.UTC)
	rows := make([]sqlc.ListRequestTelemetryDependencySpansRow, 0, debugDependencyHistoryMaxGroups+1)
	for i := 0; i < debugDependencyHistoryMaxGroups+1; i++ {
		rows = append(rows, dependencyHistoryTestRowWithName(start.Add(time.Minute), uint64(i+1), "ok", fmt.Sprintf("unique-%d", i)))
	}
	got, truncated, _, _ := buildDebugDependencyLatencyHistory(rows, start, start.Add(2*time.Hour))
	if !truncated || len(got) != debugDependencyHistoryMaxOutput {
		t.Fatalf("cap = (groups=%d truncated=%v), want (%d, true)", len(got), truncated, debugDependencyHistoryMaxOutput)
	}
	for _, item := range got {
		if item.Type != "application" {
			t.Fatalf("unclassified dependency type = %q, want application", item.Type)
		}
	}
}

func TestBuildDebugCriticalPathHistoryDetectsRecentRegression(t *testing.T) {
	start := time.Date(2026, 9, 19, 8, 0, 0, 0, time.UTC)
	rows := make([]sqlc.ListRequestTelemetryDependencySpansRow, 0, 20)
	for i := 0; i < 10; i++ {
		rows = append(rows, criticalPathHistoryTestRow(start.Add(time.Duration(i)*time.Minute), 100, false))
	}
	for i := 0; i < 10; i++ {
		rows = append(rows, criticalPathHistoryTestRow(start.Add(time.Hour+time.Duration(i)*time.Minute), 400, true))
	}

	got, truncated, complete, represented, samples := buildDebugCriticalPathHistory(rows, start, start.Add(2*time.Hour))
	if truncated || !complete {
		t.Fatalf("history state = (truncated=%v complete=%v), want (false, true)", truncated, complete)
	}
	if represented != 20 || samples != 20 {
		t.Fatalf("coverage = (represented=%d samples=%d), want (20, 20)", represented, samples)
	}
	if len(got) != 1 {
		t.Fatalf("got %d critical paths, want one: %+v", len(got), got)
	}
	path := got[0]
	if len(path.Segments) != 2 || path.Segments[0].Name != "handler" || path.Segments[1].Name != "db.query" {
		t.Fatalf("path segments = %+v", path.Segments)
	}
	if path.P50MS != 100 || path.P95MS != 400 || path.P99MS != 400 {
		t.Fatalf("full-window percentiles = %+v", path)
	}
	if path.BaselineCalls != 10 || path.CurrentCalls != 10 || path.BaselineP95MS != 100 || path.CurrentP95MS != 400 || path.P95DeltaMS != 300 {
		t.Fatalf("split-window path percentiles = %+v", path)
	}
	if path.RegressionFactor != 4 || !path.Regression {
		t.Fatalf("regression = %+v", path)
	}
	if path.ErrorRatePct != 50 || path.BaselineErrorRatePct != 0 || path.CurrentErrorRatePct != 100 || path.ErrorRateDeltaPct != 100 {
		t.Fatalf("error rates = %+v", path)
	}
}

func criticalPathHistoryTestRow(at time.Time, durationMS uint64, errorStatus bool) sqlc.ListRequestTelemetryDependencySpansRow {
	start := at.UnixNano()
	end := at.Add(time.Duration(durationMS) * time.Millisecond).UnixNano()
	status := "ok"
	if errorStatus {
		status = "error"
	}
	raw, _ := json.Marshal([]debugEvidenceSpan{
		{
			SpanID:            "child",
			ParentSpanID:      "root",
			Name:              "db.query",
			StartTimeUnixNano: uint64(start),
			EndTimeUnixNano:   uint64(end),
			DurationNanos:     durationMS * uint64(time.Millisecond),
			Status:            status,
			Attributes: map[string]string{
				"gregale.dependency.type": "managed_binding",
				"gregale.dependency.kind": "managed_postgres",
			},
		},
		{
			SpanID:            "root",
			Name:              "handler",
			StartTimeUnixNano: uint64(start),
			EndTimeUnixNano:   uint64(end),
			DurationNanos:     durationMS * uint64(time.Millisecond),
		},
	})
	return sqlc.ListRequestTelemetryDependencySpansRow{
		Count:        1,
		ReceivedAt:   pgtype.Timestamptz{Time: at, Valid: true},
		SpansSummary: raw,
	}
}

func dependencyHistoryTestRow(at time.Time, durationMS uint64, status string) sqlc.ListRequestTelemetryDependencySpansRow {
	return dependencyHistoryTestRowWithName(at, durationMS, status, "db.query")
}

func dependencyHistoryTestRowWithName(at time.Time, durationMS uint64, status, name string) sqlc.ListRequestTelemetryDependencySpansRow {
	raw, _ := json.Marshal([]debugEvidenceSpan{{
		Name:          name,
		DurationNanos: durationMS * uint64(time.Millisecond),
		Status:        status,
		Attributes: map[string]string{
			"gregale.dependency.type": "managed_binding",
			"gregale.dependency.kind": "managed_postgres",
		},
	}})
	if name != "db.query" {
		// Deliberately leave the classification absent for the cardinality test.
		var spans []debugEvidenceSpan
		_ = json.Unmarshal(raw, &spans)
		spans[0].Attributes = nil
		raw, _ = json.Marshal(spans)
	}
	return sqlc.ListRequestTelemetryDependencySpansRow{
		Count:        1,
		ReceivedAt:   pgtype.Timestamptz{Time: at, Valid: true},
		SpansSummary: raw,
	}
}
