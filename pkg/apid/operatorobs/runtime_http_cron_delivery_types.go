package operatorobs

import (
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Delivery outcomes describe settled process-local HTTP attempts, including
// scheduled fires and manual runs. Retries do not establish a final result.
type RuntimeHTTPCronDeliveryDimension struct {
	Outcome string `json:"outcome"`
}

func runtimeHTTPCronDeliveryDimensions() []RuntimeHTTPCronDeliveryDimension {
	out := []RuntimeHTTPCronDeliveryDimension{}
	for _, outcome := range []string{"success", "retry", "app_error", "failed", "dead_letter"} {
		out = append(out, RuntimeHTTPCronDeliveryDimension{Outcome: outcome})
	}
	return out
}

type RuntimeHTTPCronDeliveryOutcomeSummary struct {
	RuntimeHTTPCronDeliveryDimension
	Increased int `json:"increased"`
	Unchanged int `json:"unchanged"`
	Baseline  int `json:"baseline"`
	Unknown   int `json:"unknown"`
}

type RuntimeHTTPCronDeliverySummary struct {
	Scope      string                                  `json:"scope"`
	State      string                                  `json:"state"`
	Reason     string                                  `json:"reason,omitempty"`
	Counts     RuntimeAppTaskOperationCounts           `json:"counts"`
	Outcomes   []RuntimeHTTPCronDeliveryOutcomeSummary `json:"outcomes"`
	Incomplete bool                                    `json:"incomplete"`
}

type RuntimeHTTPCronDelivery struct {
	RuntimeHTTPCronDeliverySummary
	ComparisonAt *time.Time                    `json:"comparison_at,omitempty"`
	Items        []RuntimeHTTPCronDeliveryItem `json:"items"`
	OmittedItems int                           `json:"omitted_items"`
}

type RuntimeHTTPCronDeliverySignal struct {
	RuntimeHTTPCronDeliveryDimension
	State    string                        `json:"state"`
	Reason   string                        `json:"reason,omitempty"`
	Current  *RuntimeAppTaskOperationPoint `json:"current,omitempty"`
	Previous *RuntimeAppTaskOperationPoint `json:"previous,omitempty"`
	Delta    *int64                        `json:"delta,omitempty"`
}

type RuntimeHTTPCronDeliveryItem struct {
	Job              string                          `json:"job"`
	Node             string                          `json:"node,omitempty"`
	TargetID         string                          `json:"target_id,omitempty"`
	Unexpected       bool                            `json:"unexpected,omitempty"`
	ScrapeObservedAt *time.Time                      `json:"scrape_observed_at,omitempty"`
	Process          *api.ObsRuntimeProcessItem      `json:"process,omitempty"`
	Signals          []RuntimeHTTPCronDeliverySignal `json:"signals"`
}

func newRuntimeHTTPCronDeliverySummary() RuntimeHTTPCronDeliverySummary {
	h := RuntimeHTTPCronDeliverySummary{Scope: RuntimeAppTaskOperationsScope}
	for _, d := range runtimeHTTPCronDeliveryDimensions() {
		h.Outcomes = append(h.Outcomes, RuntimeHTTPCronDeliveryOutcomeSummary{RuntimeHTTPCronDeliveryDimension: d})
	}
	return h
}

func CloneRuntimeHTTPCronDeliverySummary(in *RuntimeHTTPCronDeliverySummary) *RuntimeHTTPCronDeliverySummary {
	if in == nil {
		return nil
	}
	out := *in
	out.Outcomes = append([]RuntimeHTTPCronDeliveryOutcomeSummary{}, in.Outcomes...)
	return &out
}

func CloneRuntimeHTTPCronDelivery(in *RuntimeHTTPCronDelivery) *RuntimeHTTPCronDelivery {
	if in == nil {
		return nil
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out RuntimeHTTPCronDelivery
	if json.Unmarshal(body, &out) != nil {
		return nil
	}
	return &out
}

func runtimeHTTPCronDeliveryState(c RuntimeAppTaskOperationCounts, incomplete bool) (string, string) {
	s, r := runtimeAppTaskOperationsState(c, incomplete, c.Baseline == 0)
	switch r {
	case "operation_increases_observed":
		r = "http_cron_delivery_increases_observed"
	case "incomplete_operation_evidence":
		r = "incomplete_http_cron_delivery_evidence"
	case "operation_evidence_unavailable":
		r = "http_cron_delivery_evidence_unavailable"
	}
	return s, r
}

func accountRuntimeHTTPCronDelivery(h *RuntimeHTTPCronDeliverySummary, item RuntimeHTTPCronDeliveryItem) {
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
