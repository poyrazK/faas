package mcpmanagement

import (
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type profileCoverageView struct {
	Status       string                       `json:"status"`
	Scope        string                       `json:"scope"`
	Measurements *profileCoverageMeasurements `json:"measurements,omitempty"`
}
type profileCoverageMeasurements struct {
	ReceivedProfiles int64   `json:"received_profiles"`
	WindowSeconds    float64 `json:"window_seconds"`
	CoveredSeconds   float64 `json:"recorded_capture_seconds"`
	GapSeconds       float64 `json:"gap_seconds"`
	LastReceivedAt   string  `json:"last_received_at,omitempty"`
	FailedUploads    int64   `json:"recorded_failed_uploads_lower_bound"`
	FailuresComplete bool    `json:"failures_complete"`
}
type profileAttributionView struct {
	Status       string                          `json:"status"`
	Scope        string                          `json:"scope"`
	Measurements *profileAttributionMeasurements `json:"measurements,omitempty"`
}
type profileAttributionMeasurements struct {
	TotalCPUSeconds        float64             `json:"total_cpu_seconds"`
	AttributedCPUSeconds   float64             `json:"attributed_cpu_seconds"`
	UnattributedCPUSeconds float64             `json:"unattributed_cpu_seconds"`
	AttributedPercent      float64             `json:"attributed_percent"`
	UnattributedPercent    float64             `json:"unattributed_percent"`
	DiagnosticsComplete    bool                `json:"native_diagnostics_complete"`
	Reasons                []profileReasonView `json:"reasons"`
}
type profileReasonView struct {
	Code       string  `json:"code"`
	CPUSeconds float64 `json:"cpu_seconds"`
}

func projectProfileCoverage(q api.ProfileQuery, n *api.ProfileCoverage) (profileCoverageView, error) {
	v := profileCoverageView{Status: "not_reported", Scope: "deployment_runtime_window"}
	if n == nil {
		return v, nil
	}
	v.Status = "unavailable"
	if !n.Available {
		return v, nil
	}
	fail := invalid("invalid_response", "native profile coverage measurements are inconsistent")
	if n.ReceivedProfiles < 0 || n.ContributingCollectors < 0 || n.RecordedFailedUploads < 0 || !profileNumber(n.WindowSeconds) || n.WindowSeconds == 0 || !profileNumber(n.CoveredSeconds) || !profileNumber(n.GapSeconds) || !profileClose(n.WindowSeconds, q.End.Sub(q.Start).Seconds()) || n.CoveredSeconds > n.WindowSeconds && !profileClose(n.CoveredSeconds, n.WindowSeconds) || !profileClose(n.CoveredSeconds+n.GapSeconds, n.WindowSeconds) || n.ReceivedProfiles == 0 && (n.LastReceivedAt != nil || n.CoveredSeconds != 0) {
		return v, fail
	}
	m := &profileCoverageMeasurements{ReceivedProfiles: n.ReceivedProfiles, WindowSeconds: n.WindowSeconds, CoveredSeconds: n.CoveredSeconds, GapSeconds: n.GapSeconds, FailedUploads: n.RecordedFailedUploads, FailuresComplete: n.FailuresComplete}
	if n.LastReceivedAt != nil {
		if n.LastReceivedAt.IsZero() {
			return v, fail
		}
		m.LastReceivedAt = n.LastReceivedAt.UTC().Format(time.RFC3339Nano)
	}
	v.Status, v.Measurements = "observed", m
	return v, nil
}

func projectProfileAttribution(q api.ProfileQuery, n api.ProfileResponse) (profileAttributionView, error) {
	v := profileAttributionView{Status: "not_reported", Scope: "deployment_runtime_window"}
	a := n.Attribution
	if a == nil {
		return v, nil
	}
	v.Status = "unavailable"
	if !a.Available {
		return v, nil
	}
	fail := invalid("invalid_response", "native profile attribution measurements are inconsistent")
	if !profileNumber(a.TotalCPUSeconds) || a.TotalCPUSeconds == 0 || !profileNumber(a.AttributedCPUSeconds) || !profileNumber(a.UnattributedCPUSeconds) || !profileClose(a.AttributedCPUSeconds+a.UnattributedCPUSeconds, a.TotalCPUSeconds) || a.AttributedPercent == nil || a.UnattributedPercent == nil || !performanceRate(*a.AttributedPercent) || !performanceRate(*a.UnattributedPercent) || !profileClose(*a.AttributedPercent, a.AttributedCPUSeconds/a.TotalCPUSeconds*100) || !profileClose(*a.UnattributedPercent, a.UnattributedCPUSeconds/a.TotalCPUSeconds*100) || len(a.Reasons) > api.ProfileAttributionMaxReasons || a.TotalCPUSeconds < n.CPUSeconds && !profileClose(a.TotalCPUSeconds, n.CPUSeconds) || q.Route == "" && !profileClose(a.TotalCPUSeconds, n.CPUSeconds) {
		return v, fail
	}
	m := &profileAttributionMeasurements{TotalCPUSeconds: a.TotalCPUSeconds, AttributedCPUSeconds: a.AttributedCPUSeconds, UnattributedCPUSeconds: a.UnattributedCPUSeconds, AttributedPercent: *a.AttributedPercent, UnattributedPercent: *a.UnattributedPercent, DiagnosticsComplete: a.DiagnosticsComplete, Reasons: []profileReasonView{}}
	total := 0.0
	for _, r := range a.Reasons {
		if !profileNumber(r.CPUSeconds) {
			return v, fail
		}
		total += r.CPUSeconds
		code := r.Reason
		switch code {
		case "unlabeled", "invalid_label", "route_not_admitted", "encoding_limit", "unknown":
		default:
			code = "unrecognized"
		}
		m.Reasons = append(m.Reasons, profileReasonView{Code: code, CPUSeconds: r.CPUSeconds})
	}
	// Native reason reconciliation uses total window CPU as its tolerance
	// scale, including when a tiny unlabeled residual is omitted.
	tolerance := math.Max(1e-9, a.TotalCPUSeconds*1e-6)
	if !profileNumber(total) || total-a.UnattributedCPUSeconds > tolerance || a.DiagnosticsComplete && math.Abs(total-a.UnattributedCPUSeconds) > tolerance {
		return v, fail
	}
	v.Status, v.Measurements = "observed", m
	return v, nil
}
