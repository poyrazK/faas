package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type obsDeploymentProbeFailure struct {
	Node        string                         `json:"node"`
	URLID       string                         `json:"url_id"`
	State       string                         `json:"state"`
	Reason      string                         `json:"reason"`
	EvaluatedAt time.Time                      `json:"evaluated_at"`
	LastRunAt   *time.Time                     `json:"last_run_at"`
	Diagnostics api.ObsServingProbeDiagnostics `json:"diagnostics"`
}

type obsDeploymentProbeFailureSnapshot struct {
	CauseSHA256 string                      `json:"cause_sha256"`
	Total       int                         `json:"total"`
	Omitted     int                         `json:"omitted"`
	Truncated   bool                        `json:"truncated"`
	Items       []obsDeploymentProbeFailure `json:"items"`
}

type obsDeploymentProbeEvidenceRetention struct {
	PerTransitionLimit       int  `json:"per_transition_limit"`
	ItemLimit                int  `json:"item_limit"`
	ByteLimit                int  `json:"byte_limit"`
	RetainedItems            int  `json:"retained_items"`
	RetainedBytes            int  `json:"retained_bytes"`
	DroppedItems             int  `json:"dropped_items"`
	DroppedSnapshots         int  `json:"dropped_snapshots"`
	Truncated                bool `json:"truncated"`
	EnvelopeOmittedItems     int  `json:"envelope_omitted_items,omitempty"`
	EnvelopeOmittedSnapshots int  `json:"envelope_omitted_snapshots,omitempty"`
}

type obsDeploymentProbeFailureSet struct {
	total       int
	fingerprint string
	preview     []obsDeploymentProbeFailure
}

func obsDeploymentProbeFailures(checks []obsDeploymentCheck, serving *api.ObsServingHealth, after, now time.Time) obsDeploymentProbeFailureSet {
	confirmed := slices.ContainsFunc(checks, func(check obsDeploymentCheck) bool {
		return check.Name == "serving_probes" && check.State == "degraded"
	})
	if !confirmed || serving == nil {
		return obsDeploymentProbeFailureSet{}
	}
	out := obsDeploymentProbeFailureSet{}
	digest := sha256.New()
	for _, probe := range serving.Probes {
		if !slices.Contains([]string{"missing", "stale", "invalid_telemetry", "content_disabled", "budget_mismatch", "failing"}, probe.State) || !validServingProbe(probe, now) || !obsDeploymentFresh(probe.EvaluatedAt, after, now) {
			continue
		}
		failure := projectObsDeploymentProbeFailure(probe)
		key := failure
		// A newer observation/run clock alone is not a new cause. Include all
		// pairs in the fingerprint, even those outside the retained preview.
		key.EvaluatedAt, key.LastRunAt, key.Diagnostics.EvaluatedAt = time.Time{}, nil, nil
		// These fixed fields contain no floats or custom source encoders.
		body, _ := json.Marshal(key)
		_, _ = digest.Write(body)
		_, _ = digest.Write([]byte{'\n'})
		out.total++
		if len(out.preview) < api.ObsPostDeployProbeEvidencePerTransitionMax {
			out.preview = append(out.preview, failure)
		}
	}
	if out.total > 0 {
		out.fingerprint = fmt.Sprintf("%x", digest.Sum(nil))
	}
	return out
}

func projectObsDeploymentProbeFailure(probe api.ObsServingProbe) obsDeploymentProbeFailure {
	diagnostic := api.ObsServingProbeDiagnostics{State: "unknown", Reason: "diagnostics_missing"}
	if d := probe.Diagnostics; d != nil {
		// Project fixed reviewed fields and own all pointers. Later API reads
		// cannot rewrite an earlier captured cause, status or source clock.
		diagnostic = api.ObsServingProbeDiagnostics{State: d.State, Reason: d.Reason, EvaluatedAt: obsDeploymentEvidencePtr(d.EvaluatedAt), FailureCode: d.FailureCode, FirstFailureCode: d.FirstFailureCode, Attempts: obsDeploymentEvidencePtr(d.Attempts), HTTPStatus: obsDeploymentEvidencePtr(d.HTTPStatus), RecoveredOnRetry: obsDeploymentEvidencePtr(d.RecoveredOnRetry)}
	}
	return obsDeploymentProbeFailure{Node: probe.Node, URLID: probe.URLID, State: probe.State, Reason: probe.Reason, EvaluatedAt: probe.EvaluatedAt.UTC(), LastRunAt: obsDeploymentEvidencePtr(probe.LastRunAt), Diagnostics: diagnostic}
}

func obsDeploymentEvidencePtr[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (r *obsDeploymentTimelineRecorder) retainProbeFailures(transition obsDeploymentTransition, failures obsDeploymentProbeFailureSet) *obsDeploymentProbeFailureSnapshot {
	if failures.total == 0 {
		return nil
	}
	count := min(len(failures.preview), api.ObsPostDeployProbeEvidenceItemsMax-r.evidence.RetainedItems)
	for count >= 0 {
		capture := &obsDeploymentProbeFailureSnapshot{CauseSHA256: failures.fingerprint, Total: failures.total, Omitted: failures.total - count, Truncated: failures.total > count, Items: slices.Clone(failures.preview[:count])}
		if capture.Items == nil {
			capture.Items = []obsDeploymentProbeFailure{}
		}
		bytes := obsDeploymentProbeEvidenceBytes(transition, capture)
		if bytes <= api.ObsPostDeployProbeEvidenceBytesMax-r.evidence.RetainedBytes {
			r.evidence.RetainedItems += count
			r.evidence.RetainedBytes += bytes
			r.evidence.DroppedItems += capture.Omitted
			return capture
		}
		count--
	}
	r.evidence.DroppedItems += failures.total
	r.evidence.DroppedSnapshots++
	return nil
}

func obsDeploymentProbeEvidenceBytes(transition obsDeploymentTransition, evidence *obsDeploymentProbeFailureSnapshot) int {
	transition.ProbeFailures = evidence
	// Match --report's pretty JSON: a transition sits six spaces into the
	// report. Charge the entire added property, including counts and commas.
	with, err := json.MarshalIndent(transition, "      ", "  ")
	if err != nil {
		return api.ObsPostDeployProbeEvidenceBytesMax + 1
	}
	transition.ProbeFailures = nil
	without, err := json.MarshalIndent(transition, "      ", "  ")
	if err != nil {
		return api.ObsPostDeployProbeEvidenceBytesMax + 1
	}
	return len(with) - len(without)
}

func writeObsDeploymentProbeEvidence(w io.Writer, history *obsDeploymentTimeline) {
	if retention := history.ProbeEvidence; retention != nil {
		_, _ = fmt.Fprintf(w, "probe_evidence retained_items=%d retained_bytes=%d dropped_items=%d dropped_snapshots=%d truncated=%t\n", retention.RetainedItems, retention.RetainedBytes, retention.DroppedItems, retention.DroppedSnapshots, retention.Truncated)
		writeObsDeploymentEnvelopeOmissions(w, "probe_evidence", retention)
	}
	for _, transition := range history.Transitions {
		capture := transition.ProbeFailures
		if capture == nil {
			continue
		}
		_, _ = fmt.Fprintf(w, "  probe_failures at=%s attempt=%d total=%d retained=%d omitted=%d truncated=%t cause_sha256=%s\n", transition.At.Format(time.RFC3339Nano), transition.Attempt, capture.Total, len(capture.Items), capture.Omitted, capture.Truncated, capture.CauseSHA256)
		for _, item := range capture.Items {
			_, _ = fmt.Fprintf(w, "    node=%s url_id=%s state=%s reason=%s evaluated_at=%s last_run_at=%s\n", item.Node, item.URLID, item.State, item.Reason, item.EvaluatedAt.Format(time.RFC3339Nano), servingTime(item.LastRunAt))
			writeServingProbeDiagnostics(w, &item.Diagnostics)
		}
	}
}
