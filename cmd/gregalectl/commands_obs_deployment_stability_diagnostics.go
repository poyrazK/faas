package main

import (
	"cmp"
	"encoding/json"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type obsDeploymentStabilityDiagnostics struct {
	State                       string                         `json:"state"`
	ObservationRemainingSeconds float64                        `json:"observation_remaining_seconds"`
	Checks                      []obsDeploymentStabilityCheck  `json:"checks,omitempty"`
	Blockers                    *obsDeploymentStabilityPreview `json:"blockers,omitempty"`
	LastReset                   *obsDeploymentStabilityReset   `json:"last_reset,omitempty"`
}

type obsDeploymentStabilityCheck struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Reason string `json:"reason"`
}

type obsDeploymentStabilityPreview struct {
	Total         int                            `json:"total"`
	Omitted       int                            `json:"omitted"`
	Truncated     bool                           `json:"truncated"`
	ItemLimit     int                            `json:"item_limit"`
	ByteLimit     int                            `json:"byte_limit"`
	RetainedBytes int                            `json:"retained_bytes"`
	Items         []obsDeploymentStabilityDetail `json:"items"`
}

type obsDeploymentStabilityDetail struct {
	Kind                             string     `json:"kind"`
	Name                             string     `json:"name,omitempty"`
	Node                             string     `json:"node,omitempty"`
	URLID                            string     `json:"url_id,omitempty"`
	Receiver                         string     `json:"receiver,omitempty"`
	Integration                      string     `json:"integration,omitempty"`
	ExpectedFirstByteSeconds         float64    `json:"expected_first_byte_seconds,omitempty"`
	MaxResultAgeSeconds              float64    `json:"max_result_age_seconds,omitempty"`
	Reason                           string     `json:"reason"`
	BaselineAt                       *time.Time `json:"baseline_at"`
	PreviousAt                       *time.Time `json:"previous_at"`
	LatestAt                         *time.Time `json:"latest_at"`
	AdvancedSeconds                  *float64   `json:"advanced_seconds,omitempty"`
	RemainingSeconds                 *float64   `json:"remaining_seconds,omitempty"`
	PreviousExpectedFirstByteSeconds *float64   `json:"previous_expected_first_byte_seconds,omitempty"`
	PreviousMaxResultAgeSeconds      *float64   `json:"previous_max_result_age_seconds,omitempty"`
}

type obsDeploymentStabilityReset struct {
	At                    time.Time                      `json:"at"`
	Reason                string                         `json:"reason"`
	PreviousObservedAt    *time.Time                     `json:"previous_observed_at"`
	ObservationGapSeconds float64                        `json:"observation_gap_seconds,omitempty"`
	Checks                []obsDeploymentStabilityCheck  `json:"checks,omitempty"`
	SourceContext         string                         `json:"source_context,omitempty"`
	Sources               *obsDeploymentStabilityPreview `json:"sources,omitempty"`
}

func (r *obsDeploymentStabilityRecorder) observeChecks(checks []obsDeploymentCheck, serving *api.ObsServingHealth, notifications *obsDeploymentNotifications, at time.Time) bool {
	state, _ := obsDeploymentOverall(checks)
	resets := r.status.Resets
	passed := r.observe(state, serving, notifications, at)
	for _, check := range checks {
		if check.State != "passed" {
			r.status.Diagnostics.Checks = append(r.status.Diagnostics.Checks, obsDeploymentStabilityCheck{check.Name, check.State, check.Reason})
		}
	}
	if r.status.Resets != resets && (state == "degraded" || state == "unknown") {
		r.status.Diagnostics.LastReset.Checks = slices.Clone(r.status.Diagnostics.Checks)
	}
	return passed
}

func obsDeploymentStabilityBlockers(first, previous, current obsDeploymentStabilityEvidence, required time.Duration) *obsDeploymentStabilityPreview {
	var items []obsDeploymentStabilityDetail
	for key, latest := range current {
		advance := max(time.Duration(0), latest.Sub(first[key]))
		if advance >= required {
			continue
		}
		item := obsDeploymentStabilitySourceDetail(key, "source_window_incomplete", first[key], previous[key], latest)
		advanced, remaining := advance.Seconds(), (required - advance).Seconds()
		item.AdvancedSeconds, item.RemainingSeconds = &advanced, &remaining
		items = append(items, item)
	}
	slices.SortFunc(items, func(a, b obsDeploymentStabilityDetail) int {
		return cmp.Or(cmp.Compare(*a.AdvancedSeconds, *b.AdvancedSeconds), compareObsDeploymentStabilityDetails(a, b))
	})
	return retainObsDeploymentStabilityPreview(items)
}

// These keys only come from passing, validated health projections. Project
// fixed identity fields; never copy raw URLs, delivery values or error text.
func obsDeploymentStabilitySourceDetail(key obsDeploymentStabilitySource, reason string, baseline, previous, latest time.Time) obsDeploymentStabilityDetail {
	return obsDeploymentStabilityDetail{Kind: key.kind, Name: key.name, Node: key.node, URLID: key.urlID, Receiver: key.receiver, Integration: key.integration,
		ExpectedFirstByteSeconds: key.budget, MaxResultAgeSeconds: key.maxAge, Reason: reason,
		BaselineAt: obsDeploymentStabilityClock(baseline), PreviousAt: obsDeploymentStabilityClock(previous), LatestAt: obsDeploymentStabilityClock(latest)}
}

func obsDeploymentStabilityClock(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	utc := at.UTC()
	return &utc
}

func compareObsDeploymentStabilityDetails(a, b obsDeploymentStabilityDetail) int {
	return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Node, b.Node), cmp.Compare(a.URLID, b.URLID), cmp.Compare(a.Name, b.Name), cmp.Compare(a.Receiver, b.Receiver), cmp.Compare(a.Integration, b.Integration), cmp.Compare(a.Reason, b.Reason))
}

func retainObsDeploymentStabilityPreview(items []obsDeploymentStabilityDetail) *obsDeploymentStabilityPreview {
	out := &obsDeploymentStabilityPreview{Total: len(items), ItemLimit: api.ObsPostDeployStabilityItemsMax, ByteLimit: api.ObsPostDeployStabilityBytesMax, Items: []obsDeploymentStabilityDetail{}}
	out.RetainedBytes = obsDeploymentStabilityPreviewBytes(out.Items)
	for _, item := range items {
		if len(out.Items) == out.ItemLimit {
			break
		}
		// Reserve indentation for the deeper reset-sources path, plus an
		// array separator. Blocker arrays have less overhead than this charge.
		candidate := append(slices.Clone(out.Items), item)
		bytes := obsDeploymentStabilityPreviewBytes(candidate)
		if bytes > out.ByteLimit {
			break
		}
		out.Items, out.RetainedBytes = candidate, bytes
	}
	out.Omitted = out.Total - len(out.Items)
	out.Truncated = out.Omitted > 0
	return out
}

func obsDeploymentStabilityPreviewBytes(items []obsDeploymentStabilityDetail) int {
	body, err := json.MarshalIndent(items, "          ", "  ")
	if err != nil {
		return api.ObsPostDeployStabilityBytesMax + 1
	}
	return len(body) + len("          ") + 1
}

func cloneObsDeploymentStabilityDiagnostics(in *obsDeploymentStabilityDiagnostics) *obsDeploymentStabilityDiagnostics {
	if in == nil {
		return nil
	}
	out := *in
	out.Checks = slices.Clone(in.Checks)
	out.Blockers = cloneObsDeploymentStabilityPreview(in.Blockers)
	if in.LastReset != nil {
		reset := *in.LastReset
		reset.Checks = slices.Clone(reset.Checks)
		reset.Sources = cloneObsDeploymentStabilityPreview(reset.Sources)
		out.LastReset = &reset
	}
	return &out
}

func cloneObsDeploymentStabilityPreview(in *obsDeploymentStabilityPreview) *obsDeploymentStabilityPreview {
	if in == nil {
		return nil
	}
	out := *in
	out.Items = slices.Clone(in.Items)
	return &out
}
