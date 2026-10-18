package mcpmanagement

import (
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type coverageSignalView struct {
	Rows     int64   `json:"rows"`
	Requests int64   `json:"requests"`
	RatePct  float64 `json:"rate_pct"`
}

type coverageView struct {
	performanceWindow
	PlanRetentionDays      int                `json:"plan_retention_days"`
	TelemetryRows          int64              `json:"telemetry_rows"`
	RepresentedRequests    int64              `json:"represented_requests"`
	ErrorRequests          int64              `json:"error_requests"`
	TraceLinked            coverageSignalView `json:"trace_linked"`
	SpanEvidence           coverageSignalView `json:"span_evidence"`
	WakeEvidence           coverageSignalView `json:"wake_evidence"`
	GuestEvidence          coverageSignalView `json:"guest_evidence"`
	OldestTelemetryAt      string             `json:"oldest_telemetry_at,omitempty"`
	LatestTelemetryAt      string             `json:"latest_telemetry_at,omitempty"`
	Status                 string             `json:"status"`
	CoverageBasis          string             `json:"coverage_basis"`
	CollectionCompleteness string             `json:"collection_completeness"`
	Limitations            string             `json:"limitations"`
}

func projectDebugCoverage(window performanceWindow, n api.DebugCoverageResponse) (coverageView, error) {
	view := coverageView{performanceWindow: window, PlanRetentionDays: n.PlanRetentionDays, TelemetryRows: n.TelemetryRows, RepresentedRequests: n.RepresentedRequests, ErrorRequests: n.ErrorRequests,
		Status: "observed", CoverageBasis: "retained_telemetry_only", CollectionCompleteness: "unknown",
		OldestTelemetryAt: n.OldestTelemetryAt, LatestTelemetryAt: n.LatestTelemetryAt,
		Limitations: "Percentages describe evidence within retained represented requests only. Requests dropped before storage have no known denominator; collection completeness is unknown. Rows may represent multiple requests. Empty or missing evidence does not prove health. This app-wide sequential read does not grant environment/log or write access; native plan and retention limits apply."}
	if n.PlanRetentionDays <= 0 || n.TelemetryRows < 0 || n.RepresentedRequests < n.TelemetryRows || (n.TelemetryRows == 0) != (n.RepresentedRequests == 0) || n.ErrorRequests < 0 || n.ErrorRequests > n.RepresentedRequests {
		return view, invalid("invalid_response", "native coverage counts or retention are inconsistent")
	}
	effective, _ := requestSince(window.Since)
	wholeDays := effective / (24 * time.Hour)
	if wholeDays > time.Duration(n.PlanRetentionDays) || wholeDays == time.Duration(n.PlanRetentionDays) && effective%(24*time.Hour) != 0 {
		return view, invalid("invalid_response", "native coverage exceeds its plan retention")
	}
	for _, at := range []string{n.OldestTelemetryAt, n.LatestTelemetryAt} {
		if at == "" {
			continue
		}
		stamp, err := time.Parse(time.RFC3339Nano, at)
		start, _ := time.Parse(time.RFC3339Nano, window.WindowStart)
		end, _ := time.Parse(time.RFC3339Nano, window.WindowEnd)
		if err != nil || stamp.Before(start) || !stamp.Before(end) || n.TelemetryRows == 0 {
			return view, invalid("invalid_response", "native coverage observation is outside its window")
		}
	}
	signals := []api.DebugCoverageSignal{n.TraceLinked, n.SpanEvidence, n.WakeEvidence, n.GuestEvidence}
	projected := make([]coverageSignalView, 0, len(signals))
	for _, s := range signals {
		expected := float64(0)
		if n.RepresentedRequests > 0 {
			expected = float64(s.Requests) * 100 / float64(n.RepresentedRequests)
		}
		if s.Rows < 0 || s.Rows > n.TelemetryRows || s.Requests < s.Rows || (s.Rows == 0) != (s.Requests == 0) || s.Requests > n.RepresentedRequests || !performanceRate(s.RatePct) || math.Abs(s.RatePct-expected) > 0.000001 {
			return view, invalid("invalid_response", "native coverage signal has an invalid retained denominator")
		}
		projected = append(projected, coverageSignalView{Rows: s.Rows, Requests: s.Requests, RatePct: s.RatePct})
	}
	view.TraceLinked, view.SpanEvidence, view.WakeEvidence, view.GuestEvidence = projected[0], projected[1], projected[2], projected[3]
	if n.TelemetryRows == 0 {
		view.Status = "no_retained_telemetry"
	}
	return view, nil
}

func performanceRate(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 100
}
