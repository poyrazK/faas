package profiling

import (
	"math"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

func attributionQuality(total, attributed float64) *api.ProfileAttributionQuality {
	out := &api.ProfileAttributionQuality{TotalCPUSeconds: total, AttributedCPUSeconds: attributed, UnattributedCPUSeconds: max(0, total-attributed), Reasons: []api.ProfileAttributionReason{}}
	if total <= 0 {
		return out
	}
	out.Available = true
	labeled := min(100, max(0, 100*attributed/total))
	unlabeled := 100 - labeled
	out.AttributedPercent, out.UnattributedPercent = &labeled, &unlabeled
	if out.UnattributedCPUSeconds > 0 {
		out.Reasons = append(out.Reasons, api.ProfileAttributionReason{Reason: "unknown", CPUSeconds: out.UnattributedCPUSeconds})
	}
	return out
}

// AttachAttributionReasons reconciles optional host diagnostics with merged CPU.
// Legacy/mixed windows keep residual CPU unknown. Inconsistent totals never
// report complete diagnostics or replace CPU-derived labeled shares.
func AttachAttributionReasons(out *api.ProfileResponse) {
	q := out.Attribution
	if q == nil || !q.Available || out.Coverage == nil {
		return
	}
	reasons := out.Coverage.AttributionReasons
	total, attributed := 0.0, 0.0
	for _, r := range reasons {
		total += r.CPUSeconds
		if r.Reason == "attributed" {
			attributed += r.CPUSeconds
		}
	}
	tolerance := math.Max(1e-9, q.TotalCPUSeconds*1e-6)
	if len(reasons) == 0 || total > q.TotalCPUSeconds+tolerance || attributed > q.AttributedCPUSeconds+tolerance {
		return
	}
	unattributed := total - attributed
	if unattributed > q.UnattributedCPUSeconds+tolerance {
		return
	}
	q.Reasons = []api.ProfileAttributionReason{}
	for _, r := range reasons {
		if r.Reason != "attributed" && r.CPUSeconds > 0 {
			q.Reasons = append(q.Reasons, r)
		}
	}
	unknown := q.UnattributedCPUSeconds - unattributed
	if unknown > tolerance {
		q.Reasons = append(q.Reasons, api.ProfileAttributionReason{Reason: "unknown", CPUSeconds: unknown})
	}
	q.DiagnosticsComplete = math.Abs(total-q.TotalCPUSeconds) <= tolerance && math.Abs(attributed-q.AttributedCPUSeconds) <= tolerance
	sort.Slice(q.Reasons, func(i, j int) bool { return q.Reasons[i].Reason < q.Reasons[j].Reason })
}

func CompareAttribution(a, b api.ProfileResponse) *api.ProfileAttributionComparison {
	out := &api.ProfileAttributionComparison{Baseline: a.Attribution, Candidate: b.Attribution, MaximumChangePercentagePoints: api.ProfileAttributionMaxChangePercentagePoints, Warnings: []string{}}
	if a.Attribution == nil || b.Attribution == nil || !a.Attribution.Available || !b.Attribution.Available || a.Attribution.AttributedPercent == nil || b.Attribution.AttributedPercent == nil {
		out.Warnings = append(out.Warnings, "Attribution quality is unavailable for one or both capture windows.")
		return out
	}
	out.Available = true
	delta := *b.Attribution.AttributedPercent - *a.Attribution.AttributedPercent
	out.DeltaPercentagePoints = &delta
	out.SubstantialChange = math.Abs(delta) >= out.MaximumChangePercentagePoints
	if delta < 0 {
		out.Warnings = append(out.Warnings, "The candidate has a lower labeled share of sampled CPU; instrumentation changes or background work may affect route comparisons.")
	}
	if out.SubstantialChange {
		out.Warnings = append(out.Warnings, "The labeled CPU share changed substantially; advisory route checks require more consistent attribution.")
	}
	if !a.Attribution.DiagnosticsComplete || !b.Attribution.DiagnosticsComplete {
		out.Warnings = append(out.Warnings, "Some label-discard diagnostics are unavailable, including for legacy captures.")
	}
	return out
}
