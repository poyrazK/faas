package operatorobs

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

const RuntimeAppTaskOperationsScope = "process_local_counter_observations"

func runtimeAppTaskOperationNames() []string {
	return []string{"restore", "renew", "execute", "destroy", "complete"}
}
func runtimeAppTaskOperationOutcomes() []string {
	return []string{"success", "error", "canceled", "lease_lost", "empty", "cancellation_pending", "capacity"}
}
func runtimeAppTaskOperationPairs() int {
	return len(runtimeAppTaskOperationNames()) * len(runtimeAppTaskOperationOutcomes())
}

type RuntimeAppTaskOperationCounts struct {
	Expected    int `json:"expected"`
	Observed    int `json:"observed"`
	Increased   int `json:"increased"`
	Unchanged   int `json:"unchanged"`
	Baseline    int `json:"baseline"`
	Missing     int `json:"missing"`
	Stale       int `json:"stale"`
	Conflicting int `json:"conflicting"`
	Unavailable int `json:"unavailable"`
	Unexpected  int `json:"unexpected"`
}
type RuntimeAppTaskOperationSummary struct {
	Operation        string `json:"operation"`
	Increased        int    `json:"increased"`
	Unchanged        int    `json:"unchanged"`
	Baseline         int    `json:"baseline"`
	Unknown          int    `json:"unknown"`
	ErrorIncreases   int    `json:"error_increases"`
	Cancellations    int    `json:"cancellations"`
	LeaseLosses      int    `json:"lease_losses"`
	CapacityRefusals int    `json:"capacity_refusals"`
}
type RuntimeAppTaskOperationsSummary struct {
	Scope      string                           `json:"scope"`
	State      string                           `json:"state"`
	Reason     string                           `json:"reason,omitempty"`
	Counts     RuntimeAppTaskOperationCounts    `json:"counts"`
	Operations []RuntimeAppTaskOperationSummary `json:"operations"`
	Incomplete bool                             `json:"incomplete"`
}
type RuntimeAppTaskOperations struct {
	RuntimeAppTaskOperationsSummary
	ComparisonAt *time.Time                    `json:"comparison_at,omitempty"`
	Items        []RuntimeAppTaskOperationItem `json:"items"`
	OmittedItems int                           `json:"omitted_items"`
}
type RuntimeAppTaskOperationPoint struct {
	Value      int64     `json:"value"`
	ObservedAt time.Time `json:"observed_at"`
}
type RuntimeAppTaskOperationSignal struct {
	Operation string                        `json:"operation"`
	Outcome   string                        `json:"outcome"`
	State     string                        `json:"state"`
	Reason    string                        `json:"reason,omitempty"`
	Current   *RuntimeAppTaskOperationPoint `json:"current,omitempty"`
	Previous  *RuntimeAppTaskOperationPoint `json:"previous,omitempty"`
	Delta     *int64                        `json:"delta,omitempty"`
}
type RuntimeAppTaskOperationItem struct {
	Job              string                          `json:"job"`
	Node             string                          `json:"node,omitempty"`
	TargetID         string                          `json:"target_id,omitempty"`
	Unexpected       bool                            `json:"unexpected,omitempty"`
	ScrapeObservedAt *time.Time                      `json:"scrape_observed_at,omitempty"`
	Process          *api.ObsRuntimeProcessItem      `json:"process,omitempty"`
	Signals          []RuntimeAppTaskOperationSignal `json:"signals"`
}

func newRuntimeAppTaskOperationsSummary() RuntimeAppTaskOperationsSummary {
	h := RuntimeAppTaskOperationsSummary{Scope: RuntimeAppTaskOperationsScope}
	for _, name := range runtimeAppTaskOperationNames() {
		h.Operations = append(h.Operations, RuntimeAppTaskOperationSummary{Operation: name})
	}
	return h
}
func CloneRuntimeAppTaskOperationsSummary(in *RuntimeAppTaskOperationsSummary) *RuntimeAppTaskOperationsSummary {
	if in == nil {
		return nil
	}
	out := *in
	out.Operations = append([]RuntimeAppTaskOperationSummary{}, in.Operations...)
	return &out
}
func CloneRuntimeAppTaskOperations(in *RuntimeAppTaskOperations) *RuntimeAppTaskOperations {
	if in == nil {
		return nil
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out RuntimeAppTaskOperations
	if json.Unmarshal(body, &out) != nil {
		return nil
	}
	return &out
}
