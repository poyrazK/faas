package operatorobs

import (
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Timing signals are cumulative histogram buckets, its count and a separate
// counter for committed returns with untrustworthy timestamps. No sum or
// percentile is inferred. The bucket order is the producer's fixed contract.
type RuntimeCommandCronTimingDimension struct {
	Signal string `json:"signal"`
}

func runtimeCommandCronTimingDimensions() []RuntimeCommandCronTimingDimension {
	out := []RuntimeCommandCronTimingDimension{}
	for _, name := range []string{"le_1_seconds", "le_5_seconds", "le_15_seconds", "le_30_seconds", "le_60_seconds", "le_120_seconds", "le_300_seconds", "le_600_seconds", "le_1800_seconds", "le_3600_seconds", "le_inf", "timed", "unknown_timestamps"} {
		out = append(out, RuntimeCommandCronTimingDimension{Signal: name})
	}
	return out
}

type RuntimeCommandCronTimingSignalSummary struct {
	RuntimeCommandCronTimingDimension
	Increased int `json:"increased"`
	Unchanged int `json:"unchanged"`
	Baseline  int `json:"baseline"`
	Unknown   int `json:"unknown"`
}

type RuntimeCommandCronTimingSummary struct {
	Scope      string                                  `json:"scope"`
	State      string                                  `json:"state"`
	Reason     string                                  `json:"reason,omitempty"`
	Counts     RuntimeAppTaskOperationCounts           `json:"counts"`
	Signals    []RuntimeCommandCronTimingSignalSummary `json:"signals"`
	Incomplete bool                                    `json:"incomplete"`
}

type RuntimeCommandCronTiming struct {
	RuntimeCommandCronTimingSummary
	ComparisonAt *time.Time                     `json:"comparison_at,omitempty"`
	Items        []RuntimeCommandCronTimingItem `json:"items"`
	OmittedItems int                            `json:"omitted_items"`
}

type RuntimeCommandCronTimingSignal struct {
	RuntimeCommandCronTimingDimension
	State    string                        `json:"state"`
	Reason   string                        `json:"reason,omitempty"`
	Current  *RuntimeAppTaskOperationPoint `json:"current,omitempty"`
	Previous *RuntimeAppTaskOperationPoint `json:"previous,omitempty"`
	Delta    *int64                        `json:"delta,omitempty"`
}

type RuntimeCommandCronTimingItem struct {
	Job              string                           `json:"job"`
	Node             string                           `json:"node,omitempty"`
	TargetID         string                           `json:"target_id,omitempty"`
	Unexpected       bool                             `json:"unexpected,omitempty"`
	ScrapeObservedAt *time.Time                       `json:"scrape_observed_at,omitempty"`
	Process          *api.ObsRuntimeProcessItem       `json:"process,omitempty"`
	Signals          []RuntimeCommandCronTimingSignal `json:"signals"`
}

func newRuntimeCommandCronTimingSummary() RuntimeCommandCronTimingSummary {
	h := RuntimeCommandCronTimingSummary{Scope: RuntimeAppTaskOperationsScope}
	for _, d := range runtimeCommandCronTimingDimensions() {
		h.Signals = append(h.Signals, RuntimeCommandCronTimingSignalSummary{RuntimeCommandCronTimingDimension: d})
	}
	return h
}

func CloneRuntimeCommandCronTimingSummary(in *RuntimeCommandCronTimingSummary) *RuntimeCommandCronTimingSummary {
	if in == nil {
		return nil
	}
	out := *in
	out.Signals = append([]RuntimeCommandCronTimingSignalSummary{}, in.Signals...)
	return &out
}

func CloneRuntimeCommandCronTiming(in *RuntimeCommandCronTiming) *RuntimeCommandCronTiming {
	if in == nil {
		return nil
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out RuntimeCommandCronTiming
	if json.Unmarshal(body, &out) != nil {
		return nil
	}
	return &out
}

func runtimeCommandCronTimingState(c RuntimeAppTaskOperationCounts, incomplete bool) (string, string) {
	s, r := runtimeAppTaskOperationsState(c, incomplete, c.Baseline == 0)
	switch r {
	case "operation_increases_observed":
		r = "cron_timing_increases_observed"
	case "incomplete_operation_evidence":
		r = "incomplete_cron_timing_evidence"
	case "operation_evidence_unavailable":
		r = "cron_timing_evidence_unavailable"
	}
	return s, r
}

func accountRuntimeCommandCronTiming(h *RuntimeCommandCronTimingSummary, item RuntimeCommandCronTimingItem) {
	for i, s := range item.Signals {
		c, o := &h.Counts, &h.Signals[i]
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
