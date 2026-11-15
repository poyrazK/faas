package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type obsDeploymentEvidenceEvent struct {
	At               time.Time                     `json:"at"`
	Check            string                        `json:"check"`
	State            string                        `json:"state"`
	Reason           string                        `json:"reason,omitempty"`
	SourceHTTPStatus *int                          `json:"source_http_status,omitempty"`
	CauseSHA256      string                        `json:"cause_sha256,omitempty"`
	Blockers         *obsDeploymentEvidencePreview `json:"blockers,omitempty"`
}

type obsDeploymentEvidenceHistory struct {
	EventLimit            int                          `json:"event_limit"`
	ByteLimit             int                          `json:"byte_limit"`
	TotalEvents           int                          `json:"total_events"`
	RetainedEvents        int                          `json:"retained_events"`
	RetainedBytes         int                          `json:"retained_bytes"`
	DroppedEvents         int                          `json:"dropped_events"`
	EnvelopeOmittedEvents int                          `json:"envelope_omitted_events"`
	Truncated             bool                         `json:"truncated"`
	Events                []obsDeploymentEvidenceEvent `json:"events"`
}

func newObsDeploymentEvidenceHistory() obsDeploymentEvidenceHistory {
	out := obsDeploymentEvidenceHistory{EventLimit: api.ObsPostDeployEvidenceEventsMax, ByteLimit: api.ObsPostDeployEvidenceHistoryBytesMax, Events: []obsDeploymentEvidenceEvent{}}
	out.account()
	return out
}

func (h *obsDeploymentEvidenceHistory) append(event obsDeploymentEvidenceEvent) {
	h.TotalEvents++
	h.Events = append(h.Events, event)
	for len(h.Events) > h.EventLimit || obsDeploymentEvidenceHistoryBytes(h.Events) > h.ByteLimit {
		h.Events = h.Events[1:]
		h.DroppedEvents++
	}
	h.account()
}

func (h *obsDeploymentEvidenceHistory) account() {
	h.RetainedEvents = len(h.Events)
	h.RetainedBytes = obsDeploymentEvidenceHistoryBytes(h.Events)
	h.Truncated = h.DroppedEvents > 0 || h.EnvelopeOmittedEvents > 0
}

func obsDeploymentEvidenceHistoryBytes(events []obsDeploymentEvidenceEvent) int {
	// Include the indented events array at qualification_evidence.history.events,
	// its leading indentation and newline, not just compact event JSON.
	body, err := json.MarshalIndent(events, "      ", "  ")
	if err != nil {
		return api.ObsPostDeployEvidenceHistoryBytesMax + 1
	}
	return len(body) + len("      ") + 1
}

func cloneObsDeploymentEvidenceHistory(in *obsDeploymentEvidenceHistory) *obsDeploymentEvidenceHistory {
	if in == nil {
		return nil
	}
	out := *in
	out.Events = slices.Clone(in.Events)
	for i := range out.Events {
		out.Events[i].SourceHTTPStatus = obsDeploymentEvidencePtr(out.Events[i].SourceHTTPStatus)
		out.Events[i].Blockers = cloneObsDeploymentEvidencePreview(out.Events[i].Blockers)
	}
	return &out
}

func obsDeploymentEvidenceFingerprint(items []obsDeploymentEvidenceDetail) string {
	// retainObsDeploymentEvidence sorted the complete inspected set, including
	// omitted items. Only identities, reasons and policy define a new cause.
	digest := sha256.New()
	for _, item := range items {
		item.ObservedAt, item.AgeSeconds = nil, nil
		body, _ := json.Marshal(item)
		_, _ = digest.Write(body)
		_, _ = digest.Write([]byte{'\n'})
	}
	return fmt.Sprintf("%x", digest.Sum(nil))
}
