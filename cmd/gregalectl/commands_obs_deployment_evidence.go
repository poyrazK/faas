package main

import (
	"cmp"
	"encoding/json"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type obsDeploymentEvidence struct {
	ObservedAt *time.Time                    `json:"observed_at"`
	Checks     []obsDeploymentEvidenceCheck  `json:"checks"`
	History    *obsDeploymentEvidenceHistory `json:"history,omitempty"`
}

type obsDeploymentEvidenceCheck struct {
	Name        string                            `json:"name"`
	State       string                            `json:"state"`
	Reason      string                            `json:"reason,omitempty"`
	Blockers    *obsDeploymentEvidencePreview     `json:"blockers,omitempty"`
	LastBlocked *obsDeploymentEvidenceObservation `json:"last_blocked,omitempty"`
	Progress    *obsDeploymentEvidenceProgress    `json:"progress,omitempty"`
}

type obsDeploymentEvidenceObservation struct {
	At       time.Time                     `json:"at"`
	Blockers *obsDeploymentEvidencePreview `json:"blockers"`
}

type obsDeploymentEvidencePreview struct {
	Total         int                           `json:"total"`
	Omitted       int                           `json:"omitted"`
	Truncated     bool                          `json:"truncated"`
	ItemLimit     int                           `json:"item_limit"`
	ByteLimit     int                           `json:"byte_limit"`
	RetainedBytes int                           `json:"retained_bytes"`
	Items         []obsDeploymentEvidenceDetail `json:"items"`
}

type obsDeploymentEvidenceDetail struct {
	Kind                    string     `json:"kind"`
	Name                    string     `json:"name,omitempty"`
	Node                    string     `json:"node,omitempty"`
	URLID                   string     `json:"url_id,omitempty"`
	Receiver                string     `json:"receiver,omitempty"`
	Integration             string     `json:"integration,omitempty"`
	Reason                  string     `json:"reason"`
	SourceState             string     `json:"source_state,omitempty"`
	SourceReason            string     `json:"source_reason,omitempty"`
	ObservedAt              *time.Time `json:"observed_at"`
	AgeSeconds              *float64   `json:"age_seconds"`
	MaxAgeSeconds           float64    `json:"max_age_seconds,omitempty"`
	AllowedClockSkewSeconds float64    `json:"allowed_clock_skew_seconds,omitempty"`
}

type obsDeploymentEvidenceRecorder struct {
	checks   []obsDeploymentEvidenceCheck
	last     map[string]*obsDeploymentEvidenceObservation
	progress []obsDeploymentEvidenceProgressObservation
	history  obsDeploymentEvidenceHistory
}

func newObsDeploymentEvidence(checks []obsDeploymentCheck) *obsDeploymentEvidenceRecorder {
	r := &obsDeploymentEvidenceRecorder{last: map[string]*obsDeploymentEvidenceObservation{}, history: newObsDeploymentEvidenceHistory()}
	for _, check := range checks {
		r.checks = append(r.checks, obsDeploymentEvidenceCheck{Name: check.Name, State: "not_observed", Progress: &obsDeploymentEvidenceProgress{}})
		r.progress = append(r.progress, obsDeploymentEvidenceProgressObservation{})
	}
	return r
}

func (r *obsDeploymentEvidenceRecorder) observe(checks []obsDeploymentCheck, snapshot obsDeploymentSnapshot, serving *api.ObsServingHealth, notifications *obsDeploymentNotifications, after, at time.Time) *obsDeploymentEvidence {
	for i, check := range checks {
		item := obsDeploymentEvidenceCheck{Name: check.Name, Reason: check.Reason}
		cause := ""
		switch check.State {
		case "passed":
			item.State = "passed"
		case "degraded":
			item.State = "faulted"
		default:
			item.State = "unavailable"
			var details []obsDeploymentEvidenceDetail
			switch check.Name {
			case "serving_probes":
				if serving != nil {
					details = obsDeploymentServingEvidence(*serving, after, at)
				}
			case "notification_pipeline":
				if notifications != nil {
					details = obsDeploymentPipelineEvidence(snapshot.notifications, *notifications, after, at)
				}
			case "notification_receivers":
				if notifications != nil {
					details = obsDeploymentReceiverEvidence(snapshot.notifications, *notifications, after, at)
				}
			}
			if details != nil {
				if len(details) == 0 {
					details = []obsDeploymentEvidenceDetail{{Kind: "check_metadata", Name: check.Name, Reason: "incomplete_evidence"}}
				}
				item.State, item.Blockers = "blocked", retainObsDeploymentEvidence(details)
				cause = obsDeploymentEvidenceFingerprint(details)
				r.last[check.Name] = &obsDeploymentEvidenceObservation{At: at.UTC(), Blockers: item.Blockers}
			}
		}
		if item.State != "blocked" {
			item.LastBlocked = r.last[check.Name]
		}
		r.observeProgress(i, &item, cause, check.SourceHTTPStatus, at)
		r.checks[i] = item
	}
	observed := at.UTC()
	return r.snapshot(&observed)
}

func retainObsDeploymentEvidence(items []obsDeploymentEvidenceDetail) *obsDeploymentEvidencePreview {
	slices.SortFunc(items, func(a, b obsDeploymentEvidenceDetail) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name), cmp.Compare(a.Node, b.Node), cmp.Compare(a.URLID, b.URLID), cmp.Compare(a.Receiver, b.Receiver), cmp.Compare(a.Integration, b.Integration), cmp.Compare(a.Reason, b.Reason))
	})
	out := &obsDeploymentEvidencePreview{Total: len(items), ItemLimit: api.ObsPostDeployEvidenceItemsMax, ByteLimit: api.ObsPostDeployEvidenceBytesMax, Items: []obsDeploymentEvidenceDetail{}}
	out.RetainedBytes = obsDeploymentEvidenceBytes(out.Items)
	for _, item := range items {
		if len(out.Items) == out.ItemLimit {
			break
		}
		candidate := append(slices.Clone(out.Items), item)
		bytes := obsDeploymentEvidenceBytes(candidate)
		if bytes > out.ByteLimit {
			break
		}
		out.Items, out.RetainedBytes = candidate, bytes
	}
	out.Omitted, out.Truncated = out.Total-len(out.Items), out.Total > len(out.Items)
	return out
}

func obsDeploymentEvidenceBytes(items []obsDeploymentEvidenceDetail) int {
	// Charge the deeper last_blocked.blockers.items path, including brackets,
	// leading indentation and a newline. Current previews cost less.
	body, err := json.MarshalIndent(items, "            ", "  ")
	if err != nil {
		return api.ObsPostDeployEvidenceBytesMax + 1
	}
	return len(body) + len("            ") + 1
}

func (r *obsDeploymentEvidenceRecorder) snapshot(at *time.Time) *obsDeploymentEvidence {
	out := &obsDeploymentEvidence{ObservedAt: obsDeploymentEvidencePtr(at), Checks: slices.Clone(r.checks)}
	out.History = cloneObsDeploymentEvidenceHistory(&r.history)
	for i := range out.Checks {
		out.Checks[i].Progress = cloneObsDeploymentEvidenceProgress(out.Checks[i].Progress)
		out.Checks[i].Blockers = cloneObsDeploymentEvidencePreview(out.Checks[i].Blockers)
		if observation := out.Checks[i].LastBlocked; observation != nil {
			copy := *observation
			copy.Blockers = cloneObsDeploymentEvidencePreview(copy.Blockers)
			out.Checks[i].LastBlocked = &copy
		}
	}
	return out
}

func cloneObsDeploymentEvidencePreview(in *obsDeploymentEvidencePreview) *obsDeploymentEvidencePreview {
	if in == nil {
		return nil
	}
	out := *in
	out.Items = slices.Clone(in.Items)
	for i := range out.Items {
		out.Items[i].ObservedAt = obsDeploymentEvidencePtr(out.Items[i].ObservedAt)
		out.Items[i].AgeSeconds = obsDeploymentEvidencePtr(out.Items[i].AgeSeconds)
	}
	return &out
}
