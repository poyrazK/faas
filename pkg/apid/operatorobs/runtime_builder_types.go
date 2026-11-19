package operatorobs

import (
	"encoding/json"
	"time"
)

// Builder diagnostics describe sampled process-local work, not build success.
type RuntimeBuilderSummary struct {
	State      string               `json:"state"`
	Reason     string               `json:"reason,omitempty"`
	Counts     RuntimeBuilderCounts `json:"counts"`
	Incomplete bool                 `json:"incomplete"`
}

type RuntimeBuilderHealth struct {
	RuntimeBuilderSummary
	Items        []RuntimeBuilderItem `json:"items"`
	OmittedItems int                  `json:"omitted_items"`
}

type RuntimeBuilderCounts struct {
	Expected        int `json:"expected"`
	Observed        int `json:"observed"`
	Complete        int `json:"complete"`
	Missing         int `json:"missing"`
	Stale           int `json:"stale"`
	Conflicting     int `json:"conflicting"`
	Unavailable     int `json:"unavailable"`
	Unexpected      int `json:"unexpected"`
	OverdueWorkers  int `json:"overdue_workers"`
	WaitingWorkers  int `json:"waiting_workers"`
	WorkingWorkers  int `json:"working_workers"`
	DrainingWorkers int `json:"draining_workers"`
	WaitingQueues   int `json:"waiting_queues"`
	SaturatedSlots  int `json:"saturated_slots"`
}

type RuntimeBuilderSignal struct {
	State      string     `json:"state"`
	Reason     string     `json:"reason,omitempty"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
}

type RuntimeBuilderWorker struct {
	RuntimeBuilderSignal
	Values *RuntimeBuilderWorkerValues `json:"values,omitempty"`
}

type RuntimeBuilderWorkerValues struct {
	Enabled                  bool       `json:"enabled"`
	Inflight                 bool       `json:"inflight"`
	StartedAt                time.Time  `json:"started_at"`
	LastAttemptAt            *time.Time `json:"last_attempt_at,omitempty"`
	LastCompletedAt          *time.Time `json:"last_completed_at,omitempty"`
	IntervalSeconds          float64    `json:"interval_seconds"`
	BusyAllowanceSeconds     float64    `json:"busy_allowance_seconds"`
	ProgressAgeSeconds       float64    `json:"progress_age_seconds"`
	ProgressAllowanceSeconds float64    `json:"progress_allowance_seconds"`
}

type RuntimeBuilderSlots struct {
	RuntimeBuilderSignal
	Values *RuntimeBuilderSlotValues `json:"values,omitempty"`
}

type RuntimeBuilderSlotValues struct {
	Used  int `json:"used"`
	Limit int `json:"limit"`
}

type RuntimeBuilderQueue struct {
	RuntimeBuilderSignal
	Queue  string                     `json:"queue"`
	Values *RuntimeBuilderQueueValues `json:"values,omitempty"`
}

type RuntimeBuilderQueueValues struct {
	Enabled           bool       `json:"enabled"`
	Waiting           int        `json:"waiting"`
	Active            int        `json:"active"`
	Capacity          int        `json:"capacity"`
	Workers           int        `json:"workers"`
	OldestEnqueuedAt  *time.Time `json:"oldest_enqueued_at,omitempty"`
	OldestWaitSeconds float64    `json:"oldest_wait_seconds"`
}

type RuntimeBuilderItem struct {
	Node             string                `json:"node,omitempty"`
	TargetID         string                `json:"target_id,omitempty"`
	Unexpected       bool                  `json:"unexpected,omitempty"`
	ScrapeObservedAt *time.Time            `json:"scrape_observed_at,omitempty"`
	State            string                `json:"state"`
	Worker           RuntimeBuilderWorker  `json:"worker"`
	Slots            RuntimeBuilderSlots   `json:"slots"`
	Queues           []RuntimeBuilderQueue `json:"queues"`
}

func CloneRuntimeBuilderHealth(in *RuntimeBuilderHealth) *RuntimeBuilderHealth {
	if in == nil {
		return nil
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out RuntimeBuilderHealth
	if json.Unmarshal(body, &out) != nil {
		return nil
	}
	return &out
}
