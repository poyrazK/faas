package api

import "math"

// CommitRecoveryDiagnosticEstimate is advisory performance from the current watch.
// Remaining estimates describe initial sampling, never delivery or fresh settlement.
type CommitRecoveryDiagnosticEstimate struct {
	State                    string   `json:"state"`
	TargetCount              int      `json:"target_count"`
	Remaining                int      `json:"remaining"`
	Deferred                 int      `json:"deferred"`
	DiagnosticPasses         int      `json:"diagnostic_passes"`
	SuccessfulSamples        int      `json:"successful_samples"`
	SamplesPerSecond         float64  `json:"samples_per_second"`
	SamplesPerPass           float64  `json:"samples_per_pass"`
	EstimatedRemainingPasses *int     `json:"estimated_remaining_passes,omitempty"`
	EstimatedRemainingMS     *float64 `json:"estimated_remaining_ms,omitempty"`
	EstimatedRefreshMS       *float64 `json:"estimated_refresh_ms,omitempty"`
	RefreshExceedsMaxAge     bool     `json:"refresh_exceeds_max_age"`
}

type commitRecoveryDiagnosticEstimator struct {
	passes, samples int
	readMS          float64
}

func (e *commitRecoveryDiagnosticEstimator) observe(out CommitRecoveryStatusResult, readMS float64) {
	if out.DiagnosticsProgress == nil || out.DiagnosticsProgress.Reads == 0 {
		return
	}
	e.passes++
	e.readMS += readMS
	for _, row := range out.Items {
		if diagnosticTarget(row) && validCommitRecoveryDiagnostic(row.Diagnostics, row) && !row.Diagnostics.Retained {
			e.samples++
		}
	}
}

func (e *commitRecoveryDiagnosticEstimator) estimate(out CommitRecoveryStatusResult, elapsedMS, delayMS float64) *CommitRecoveryDiagnosticEstimate {
	progress := out.DiagnosticsProgress
	if progress == nil {
		return nil
	}
	result := &CommitRecoveryDiagnosticEstimate{State: "insufficient_data", DiagnosticPasses: e.passes, SuccessfulSamples: e.samples}
	for _, row := range out.Items {
		if !diagnosticTarget(row) {
			continue
		}
		result.TargetCount++
		if !validCommitRecoveryDiagnostic(row.Diagnostics, row) {
			result.Remaining++
			if !diagnosticQueryable(row) {
				result.Deferred++
			}
		}
	}
	if elapsedMS > 0 {
		result.SamplesPerSecond = float64(e.samples) * 1000 / elapsedMS
	}
	if e.passes > 0 {
		result.SamplesPerPass = float64(e.samples) / float64(e.passes)
	}
	if result.Remaining == 0 {
		result.State = "covered"
		zeroPasses, zeroMS := 0, float64(0)
		result.EstimatedRemainingPasses, result.EstimatedRemainingMS = &zeroPasses, &zeroMS
	} else if result.Deferred > 0 {
		result.State = "awaiting_receipts"
	}
	if result.Deferred > 0 || e.passes == 0 || e.samples == 0 || elapsedMS <= 0 {
		return result
	}
	cycleMS := (elapsedMS + delayMS) / float64(e.passes)
	refreshPasses := math.Ceil(float64(result.TargetCount) / result.SamplesPerPass)
	refreshMS := float64(0)
	if refreshPasses > 0 {
		refreshMS = math.Ceil((refreshPasses-1)*cycleMS + e.readMS/float64(e.passes))
	}
	result.EstimatedRefreshMS = &refreshMS
	result.RefreshExceedsMaxAge = progress.MaxDiagnosticAgeMS > 0 && refreshMS > float64(progress.MaxDiagnosticAgeMS)
	if result.Remaining > 0 && result.Deferred == 0 {
		result.State = "estimated"
		passes := int(math.Ceil(float64(result.Remaining) / result.SamplesPerPass))
		remainingMS := math.Ceil(float64(passes) * cycleMS)
		result.EstimatedRemainingPasses, result.EstimatedRemainingMS = &passes, &remainingMS
	}
	return result
}
