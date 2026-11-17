package operatorobs

import (
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Result dimensions separate acknowledged returned rows from recovery sweep changes.
type RuntimeAppTaskResultDimension struct {
	Source  string `json:"source"`
	Kind    string `json:"kind,omitempty"`
	Outcome string `json:"outcome"`
}

func runtimeAppTaskResultDimensions() []RuntimeAppTaskResultDimension {
	out := []RuntimeAppTaskResultDimension{}
	for _, kind := range []string{"release", "manual", "cron"} {
		for _, status := range []string{"succeeded", "failed", "timed_out", "cancelled", "queued"} {
			out = append(out, RuntimeAppTaskResultDimension{Source: "completion", Kind: kind, Outcome: status})
		}
	}
	for _, outcome := range []string{"requeued_restores", "failed_runs", "cancelled"} {
		out = append(out, RuntimeAppTaskResultDimension{Source: "recovery", Outcome: outcome})
	}
	return out
}

type RuntimeAppTaskResultOutcome struct {
	RuntimeAppTaskResultDimension
	Increased int `json:"increased"`
	Unchanged int `json:"unchanged"`
	Baseline  int `json:"baseline"`
	Unknown   int `json:"unknown"`
}

type RuntimeAppTaskResultsSummary struct {
	Scope      string                        `json:"scope"`
	State      string                        `json:"state"`
	Reason     string                        `json:"reason,omitempty"`
	Counts     RuntimeAppTaskOperationCounts `json:"counts"`
	Outcomes   []RuntimeAppTaskResultOutcome `json:"outcomes"`
	Incomplete bool                          `json:"incomplete"`
}

type RuntimeAppTaskResults struct {
	RuntimeAppTaskResultsSummary
	ComparisonAt *time.Time                 `json:"comparison_at,omitempty"`
	Items        []RuntimeAppTaskResultItem `json:"items"`
	OmittedItems int                        `json:"omitted_items"`
}

type RuntimeAppTaskResultSignal struct {
	RuntimeAppTaskResultDimension
	State    string                        `json:"state"`
	Reason   string                        `json:"reason,omitempty"`
	Current  *RuntimeAppTaskOperationPoint `json:"current,omitempty"`
	Previous *RuntimeAppTaskOperationPoint `json:"previous,omitempty"`
	Delta    *int64                        `json:"delta,omitempty"`
}

type RuntimeAppTaskResultItem struct {
	Job              string                       `json:"job"`
	Node             string                       `json:"node,omitempty"`
	TargetID         string                       `json:"target_id,omitempty"`
	Unexpected       bool                         `json:"unexpected,omitempty"`
	ScrapeObservedAt *time.Time                   `json:"scrape_observed_at,omitempty"`
	Process          *api.ObsRuntimeProcessItem   `json:"process,omitempty"`
	Signals          []RuntimeAppTaskResultSignal `json:"signals"`
}

func newRuntimeAppTaskResultsSummary() RuntimeAppTaskResultsSummary {
	h := RuntimeAppTaskResultsSummary{Scope: RuntimeAppTaskOperationsScope}
	for _, d := range runtimeAppTaskResultDimensions() {
		h.Outcomes = append(h.Outcomes, RuntimeAppTaskResultOutcome{RuntimeAppTaskResultDimension: d})
	}
	return h
}

func CloneRuntimeAppTaskResultsSummary(in *RuntimeAppTaskResultsSummary) *RuntimeAppTaskResultsSummary {
	if in == nil {
		return nil
	}
	out := *in
	out.Outcomes = append([]RuntimeAppTaskResultOutcome{}, in.Outcomes...)
	return &out
}

func CloneRuntimeAppTaskResults(in *RuntimeAppTaskResults) *RuntimeAppTaskResults {
	if in == nil {
		return nil
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out RuntimeAppTaskResults
	if json.Unmarshal(body, &out) != nil {
		return nil
	}
	return &out
}

func runtimeAppTaskResultsState(c RuntimeAppTaskOperationCounts, incomplete bool) (string, string) {
	s, r := runtimeAppTaskOperationsState(c, incomplete, c.Baseline == 0)
	switch r {
	case "operation_increases_observed":
		r = "result_increases_observed"
	case "incomplete_operation_evidence":
		r = "incomplete_result_evidence"
	case "operation_evidence_unavailable":
		r = "result_evidence_unavailable"
	}
	return s, r
}

func accountRuntimeAppTaskResults(h *RuntimeAppTaskResultsSummary, item RuntimeAppTaskResultItem) {
	for i, s := range item.Signals {
		c, o := &h.Counts, &h.Outcomes[i]
		if s.Current != nil {
			c.Observed++
		}
		if item.Unexpected {
			c.Unexpected++
		}
		switch s.State {
		case "increased":
			c.Increased++
			o.Increased++
		case "unchanged":
			c.Unchanged++
			o.Unchanged++
		case "baseline":
			c.Baseline++
			o.Baseline++
		default:
			o.Unknown++
			switch s.State {
			case "missing":
				c.Missing++
			case "stale":
				c.Stale++
			case "conflicting":
				c.Conflicting++
			default:
				c.Unavailable++
			}
		}
	}
}
