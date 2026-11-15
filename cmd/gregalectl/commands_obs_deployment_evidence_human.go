package main

import (
	"fmt"
	"io"
	"time"
)

func writeObsDeploymentEvidence(w io.Writer, evidence *obsDeploymentEvidence) {
	if evidence == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "qualification_evidence observed_at=%s\n", servingTime(evidence.ObservedAt))
	for _, check := range evidence.Checks {
		_, _ = fmt.Fprintf(w, "  evidence_check name=%s state=%s reason=%s\n", check.Name, check.State, check.Reason)
		if progress := check.Progress; progress != nil {
			_, _ = fmt.Fprintf(w, "    evidence_progress first_observed_at=%s first_blocked_at=%s first_passed_at=%s state_since=%s blockers_since=%s observed_blocked_seconds=%g observed_unavailable_seconds=%g\n", servingTime(progress.FirstObservedAt), servingTime(progress.FirstBlockedAt), servingTime(progress.FirstPassedAt), servingTime(progress.StateSince), servingTime(progress.BlockersSince), progress.ObservedBlockedSeconds, progress.ObservedUnavailableSeconds)
		}
		writeObsDeploymentEvidencePreview(w, "evidence_blockers", check.Blockers)
		if previous := check.LastBlocked; previous != nil {
			_, _ = fmt.Fprintf(w, "  last_evidence_blockers check=%s at=%s historical=true\n", check.Name, previous.At.Format(time.RFC3339Nano))
			writeObsDeploymentEvidencePreview(w, "last_evidence_preview", previous.Blockers)
		}
	}
	writeObsDeploymentEvidenceHistory(w, evidence.History)
}

func writeObsDeploymentEvidenceHistory(w io.Writer, history *obsDeploymentEvidenceHistory) {
	if history == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "qualification_history historical=true total_events=%d retained_events=%d dropped_events=%d envelope_omitted_events=%d truncated=%t event_limit=%d retained_bytes=%d byte_limit=%d\n", history.TotalEvents, history.RetainedEvents, history.DroppedEvents, history.EnvelopeOmittedEvents, history.Truncated, history.EventLimit, history.RetainedBytes, history.ByteLimit)
	for _, event := range history.Events {
		_, _ = fmt.Fprintf(w, "  evidence_change at=%s check=%s state=%s reason=%s cause_sha256=%s", event.At.Format(time.RFC3339Nano), event.Check, event.State, event.Reason, event.CauseSHA256)
		if event.SourceHTTPStatus != nil {
			_, _ = fmt.Fprintf(w, " source_http_status=%d", *event.SourceHTTPStatus)
		}
		_, _ = fmt.Fprintln(w)
		writeObsDeploymentEvidencePreview(w, "historical_evidence_blockers", event.Blockers)
	}
}

func writeObsDeploymentEvidencePreview(w io.Writer, label string, preview *obsDeploymentEvidencePreview) {
	if preview == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "    %s total=%d retained=%d omitted=%d truncated=%t item_limit=%d retained_bytes=%d byte_limit=%d\n", label, preview.Total, len(preview.Items), preview.Omitted, preview.Truncated, preview.ItemLimit, preview.RetainedBytes, preview.ByteLimit)
	for _, item := range preview.Items {
		_, _ = fmt.Fprintf(w, "      kind=%s name=%s node=%s url_id=%s receiver=%s integration=%s reason=%s source_state=%s source_reason=%s observed_at=%s", item.Kind, item.Name, item.Node, item.URLID, item.Receiver, item.Integration, item.Reason, item.SourceState, item.SourceReason, servingTime(item.ObservedAt))
		if item.AgeSeconds != nil {
			_, _ = fmt.Fprintf(w, " age_seconds=%g", *item.AgeSeconds)
		}
		if item.MaxAgeSeconds != 0 {
			_, _ = fmt.Fprintf(w, " max_age_seconds=%g allowed_clock_skew_seconds=%g", item.MaxAgeSeconds, item.AllowedClockSkewSeconds)
		}
		_, _ = fmt.Fprintln(w)
	}
}
