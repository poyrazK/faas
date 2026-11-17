package operatorobs

import (
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Outcome dimensions separate scan results, scheduling decisions and admission returns.
type RuntimeCommandCronOutcomeDimension struct {
	Source  string `json:"source"`
	Outcome string `json:"outcome"`
}

func runtimeCommandCronOutcomeDimensions() []RuntimeCommandCronOutcomeDimension {
	out := []RuntimeCommandCronOutcomeDimension{}
	for _, group := range []struct {
		source   string
		outcomes []string
	}{
		{"sweep", []string{"success", "error", "canceled"}},
		{"decision", []string{"disabled", "not_due", "account_held", "plan_limit", "replacement_wait", "not_owned", "invalid_schedule", "lookup_error", "store_unavailable", "no_live_deployment", "canceled", "legacy_queued", "legacy_skipped", "legacy_stale", "legacy_error"}},
		{"admission", []string{"queued", "exclusive_pending", "coalesced", "skipped_overlap", "missed_deadline", "stale", "deployment_unavailable", "rejected", "error", "canceled", "unknown"}},
	} {
		for _, outcome := range group.outcomes {
			out = append(out, RuntimeCommandCronOutcomeDimension{Source: group.source, Outcome: outcome})
		}
	}
	return out
}

type RuntimeCommandCronOutcomeSummary struct {
	RuntimeCommandCronOutcomeDimension
	Increased int `json:"increased"`
	Unchanged int `json:"unchanged"`
	Baseline  int `json:"baseline"`
	Unknown   int `json:"unknown"`
}

type RuntimeCommandCronOutcomesSummary struct {
	Scope      string                             `json:"scope"`
	State      string                             `json:"state"`
	Reason     string                             `json:"reason,omitempty"`
	Counts     RuntimeAppTaskOperationCounts      `json:"counts"`
	Outcomes   []RuntimeCommandCronOutcomeSummary `json:"outcomes"`
	Incomplete bool                               `json:"incomplete"`
}

type RuntimeCommandCronOutcomes struct {
	RuntimeCommandCronOutcomesSummary
	ComparisonAt *time.Time                      `json:"comparison_at,omitempty"`
	Items        []RuntimeCommandCronOutcomeItem `json:"items"`
	OmittedItems int                             `json:"omitted_items"`
}

type RuntimeCommandCronOutcomeSignal struct {
	RuntimeCommandCronOutcomeDimension
	State    string                        `json:"state"`
	Reason   string                        `json:"reason,omitempty"`
	Current  *RuntimeAppTaskOperationPoint `json:"current,omitempty"`
	Previous *RuntimeAppTaskOperationPoint `json:"previous,omitempty"`
	Delta    *int64                        `json:"delta,omitempty"`
}

type RuntimeCommandCronOutcomeItem struct {
	Job              string                            `json:"job"`
	Node             string                            `json:"node,omitempty"`
	TargetID         string                            `json:"target_id,omitempty"`
	Unexpected       bool                              `json:"unexpected,omitempty"`
	ScrapeObservedAt *time.Time                        `json:"scrape_observed_at,omitempty"`
	Process          *api.ObsRuntimeProcessItem        `json:"process,omitempty"`
	Signals          []RuntimeCommandCronOutcomeSignal `json:"signals"`
}

func newRuntimeCommandCronOutcomesSummary() RuntimeCommandCronOutcomesSummary {
	h := RuntimeCommandCronOutcomesSummary{Scope: RuntimeAppTaskOperationsScope}
	for _, d := range runtimeCommandCronOutcomeDimensions() {
		h.Outcomes = append(h.Outcomes, RuntimeCommandCronOutcomeSummary{RuntimeCommandCronOutcomeDimension: d})
	}
	return h
}

func CloneRuntimeCommandCronOutcomesSummary(in *RuntimeCommandCronOutcomesSummary) *RuntimeCommandCronOutcomesSummary {
	if in == nil {
		return nil
	}
	out := *in
	out.Outcomes = append([]RuntimeCommandCronOutcomeSummary{}, in.Outcomes...)
	return &out
}

func CloneRuntimeCommandCronOutcomes(in *RuntimeCommandCronOutcomes) *RuntimeCommandCronOutcomes {
	if in == nil {
		return nil
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out RuntimeCommandCronOutcomes
	if json.Unmarshal(body, &out) != nil {
		return nil
	}
	return &out
}

func runtimeCommandCronOutcomesState(c RuntimeAppTaskOperationCounts, incomplete bool) (string, string) {
	s, r := runtimeAppTaskOperationsState(c, incomplete, c.Baseline == 0)
	switch r {
	case "operation_increases_observed":
		r = "cron_outcome_increases_observed"
	case "incomplete_operation_evidence":
		r = "incomplete_cron_outcome_evidence"
	case "operation_evidence_unavailable":
		r = "cron_outcome_evidence_unavailable"
	}
	return s, r
}

func accountRuntimeCommandCronOutcomes(h *RuntimeCommandCronOutcomesSummary, item RuntimeCommandCronOutcomeItem) {
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
