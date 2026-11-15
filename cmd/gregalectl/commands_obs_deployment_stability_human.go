package main

import (
	"fmt"
	"io"
	"time"
)

func writeObsDeploymentStabilityDiagnostics(w io.Writer, diagnostics *obsDeploymentStabilityDiagnostics) {
	if diagnostics == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "  stability_evidence state=%s observation_remaining_seconds=%g\n", diagnostics.State, diagnostics.ObservationRemainingSeconds)
	writeObsDeploymentStabilityChecks(w, "stability_wait", diagnostics.Checks)
	writeObsDeploymentStabilityPreview(w, "stability_blockers", diagnostics.Blockers)
	if reset := diagnostics.LastReset; reset != nil {
		_, _ = fmt.Fprintf(w, "  stability_reset at=%s reason=%s previous_observed_at=%s observation_gap_seconds=%g source_context=%s\n", reset.At.Format(time.RFC3339Nano), reset.Reason, servingTime(reset.PreviousObservedAt), reset.ObservationGapSeconds, reset.SourceContext)
		writeObsDeploymentStabilityChecks(w, "stability_reset_check", reset.Checks)
		writeObsDeploymentStabilityPreview(w, "stability_reset_sources", reset.Sources)
	}
}

func writeObsDeploymentStabilityChecks(w io.Writer, label string, checks []obsDeploymentStabilityCheck) {
	for _, check := range checks {
		_, _ = fmt.Fprintf(w, "    %s check=%s state=%s reason=%s\n", label, check.Name, check.State, check.Reason)
	}
}

func writeObsDeploymentStabilityPreview(w io.Writer, label string, preview *obsDeploymentStabilityPreview) {
	if preview == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "  %s total=%d retained=%d omitted=%d truncated=%t item_limit=%d retained_bytes=%d byte_limit=%d\n", label, preview.Total, len(preview.Items), preview.Omitted, preview.Truncated, preview.ItemLimit, preview.RetainedBytes, preview.ByteLimit)
	for _, item := range preview.Items {
		_, _ = fmt.Fprintf(w, "    kind=%s name=%s node=%s url_id=%s receiver=%s integration=%s reason=%s baseline_at=%s previous_at=%s latest_at=%s", item.Kind, item.Name, item.Node, item.URLID, item.Receiver, item.Integration, item.Reason, servingTime(item.BaselineAt), servingTime(item.PreviousAt), servingTime(item.LatestAt))
		if item.AdvancedSeconds != nil && item.RemainingSeconds != nil {
			_, _ = fmt.Fprintf(w, " advanced_seconds=%g remaining_seconds=%g", *item.AdvancedSeconds, *item.RemainingSeconds)
		}
		if item.ExpectedFirstByteSeconds != 0 {
			_, _ = fmt.Fprintf(w, " expected_first_byte_seconds=%g max_result_age_seconds=%g", item.ExpectedFirstByteSeconds, item.MaxResultAgeSeconds)
		}
		if item.PreviousExpectedFirstByteSeconds != nil && item.PreviousMaxResultAgeSeconds != nil {
			_, _ = fmt.Fprintf(w, " previous_expected_first_byte_seconds=%g previous_max_result_age_seconds=%g", *item.PreviousExpectedFirstByteSeconds, *item.PreviousMaxResultAgeSeconds)
		}
		_, _ = fmt.Fprintln(w)
	}
}
