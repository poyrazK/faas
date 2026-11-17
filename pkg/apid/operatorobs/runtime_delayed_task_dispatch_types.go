package operatorobs

import (
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Dispatch outcomes describe settled process-local delayed-task attempts.
// Failed includes application errors; retries do not establish a final result.
type RuntimeDelayedTaskDispatchDimension struct {
	Outcome string `json:"outcome"`
}

func runtimeDelayedTaskDispatchDimensions() []RuntimeDelayedTaskDispatchDimension {
	out := []RuntimeDelayedTaskDispatchDimension{}
	for _, outcome := range []string{"success", "retry", "failed", "dead_letter"} {
		out = append(out, RuntimeDelayedTaskDispatchDimension{Outcome: outcome})
	}
	return out
}

type RuntimeDelayedTaskDispatchOutcomeSummary struct {
	RuntimeDelayedTaskDispatchDimension
	Increased int `json:"increased"`
	Unchanged int `json:"unchanged"`
	Baseline  int `json:"baseline"`
	Unknown   int `json:"unknown"`
}

type RuntimeDelayedTaskDispatchSummary struct {
	Scope      string                                     `json:"scope"`
	State      string                                     `json:"state"`
	Reason     string                                     `json:"reason,omitempty"`
	Counts     RuntimeAppTaskOperationCounts              `json:"counts"`
	Outcomes   []RuntimeDelayedTaskDispatchOutcomeSummary `json:"outcomes"`
	Incomplete bool                                       `json:"incomplete"`
}

type RuntimeDelayedTaskDispatch struct {
	RuntimeDelayedTaskDispatchSummary
	ComparisonAt *time.Time                       `json:"comparison_at,omitempty"`
	Items        []RuntimeDelayedTaskDispatchItem `json:"items"`
	OmittedItems int                              `json:"omitted_items"`
}

type RuntimeDelayedTaskDispatchSignal struct {
	RuntimeDelayedTaskDispatchDimension
	State    string                        `json:"state"`
	Reason   string                        `json:"reason,omitempty"`
	Current  *RuntimeAppTaskOperationPoint `json:"current,omitempty"`
	Previous *RuntimeAppTaskOperationPoint `json:"previous,omitempty"`
	Delta    *int64                        `json:"delta,omitempty"`
}

type RuntimeDelayedTaskDispatchItem struct {
	Job              string                             `json:"job"`
	Node             string                             `json:"node,omitempty"`
	TargetID         string                             `json:"target_id,omitempty"`
	Unexpected       bool                               `json:"unexpected,omitempty"`
	ScrapeObservedAt *time.Time                         `json:"scrape_observed_at,omitempty"`
	Process          *api.ObsRuntimeProcessItem         `json:"process,omitempty"`
	Signals          []RuntimeDelayedTaskDispatchSignal `json:"signals"`
}

func newRuntimeDelayedTaskDispatchSummary() RuntimeDelayedTaskDispatchSummary {
	h := RuntimeDelayedTaskDispatchSummary{Scope: RuntimeAppTaskOperationsScope}
	for _, d := range runtimeDelayedTaskDispatchDimensions() {
		h.Outcomes = append(h.Outcomes, RuntimeDelayedTaskDispatchOutcomeSummary{RuntimeDelayedTaskDispatchDimension: d})
	}
	return h
}

func CloneRuntimeDelayedTaskDispatchSummary(in *RuntimeDelayedTaskDispatchSummary) *RuntimeDelayedTaskDispatchSummary {
	if in == nil {
		return nil
	}
	out := *in
	out.Outcomes = append([]RuntimeDelayedTaskDispatchOutcomeSummary{}, in.Outcomes...)
	return &out
}

func CloneRuntimeDelayedTaskDispatch(in *RuntimeDelayedTaskDispatch) *RuntimeDelayedTaskDispatch {
	if in == nil {
		return nil
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out RuntimeDelayedTaskDispatch
	if json.Unmarshal(body, &out) != nil {
		return nil
	}
	return &out
}

func runtimeDelayedTaskDispatchState(c RuntimeAppTaskOperationCounts, incomplete bool) (string, string) {
	s, r := runtimeAppTaskOperationsState(c, incomplete, c.Baseline == 0)
	switch r {
	case "operation_increases_observed":
		r = "delayed_task_dispatch_increases_observed"
	case "incomplete_operation_evidence":
		r = "incomplete_delayed_task_dispatch_evidence"
	case "operation_evidence_unavailable":
		r = "delayed_task_dispatch_evidence_unavailable"
	}
	return s, r
}

func accountRuntimeDelayedTaskDispatch(h *RuntimeDelayedTaskDispatchSummary, item RuntimeDelayedTaskDispatchItem) {
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
