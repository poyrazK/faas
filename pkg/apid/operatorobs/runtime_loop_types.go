package operatorobs

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// RuntimeBuildsLoopResponse adds private operator evidence without changing the
// identity DTO consumed by older readers. All optional loop data is advisory.
type RuntimeBuildsLoopResponse struct {
	Diagnostics         *RuntimeDiagnostics         `json:"diagnostics,omitempty"`
	CommandCronOutcomes *RuntimeCommandCronOutcomes `json:"command_cron_outcomes,omitempty"`
	HTTPCronDelivery    *RuntimeHTTPCronDelivery    `json:"http_cron_delivery,omitempty"`
	DelayedTaskDispatch *RuntimeDelayedTaskDispatch `json:"delayed_task_dispatch,omitempty"`
	CommandCronTiming   *RuntimeCommandCronTiming   `json:"command_cron_timing,omitempty"`
	AppTaskResults      *RuntimeAppTaskResults      `json:"app_task_results,omitempty"`
	BuildRecoveryHealth *RuntimeBuildRecoveryHealth `json:"build_recovery_health,omitempty"`
	AppTaskHealth       *RuntimeAppTaskHealth       `json:"app_task_health,omitempty"`
	CommandCronHealth   *RuntimeCommandCronHealth   `json:"command_cron_health,omitempty"`
	AppTaskOperations   *RuntimeAppTaskOperations   `json:"app_task_operations,omitempty"`
	PostCaptureHealth   *RuntimePostCaptureHealth   `json:"post_capture_health,omitempty"`
	SnapshotPrimeHealth *RuntimeSnapshotPrimeHealth `json:"snapshot_prime_health,omitempty"`
	api.ObsRuntimeBuilds
	LoopHealth        *RuntimeLoopHealth        `json:"loop_health,omitempty"`
	BuilderHealth     *RuntimeBuilderHealth     `json:"builder_health,omitempty"`
	HandoffHealth     *RuntimeHandoffHealth     `json:"handoff_health,omitempty"`
	PreparationHealth *RuntimePreparationHealth `json:"preparation_health,omitempty"`
}

type RuntimeLoopSummary struct {
	State      string            `json:"state"`
	Reason     string            `json:"reason,omitempty"`
	Counts     RuntimeLoopCounts `json:"counts"`
	Incomplete bool              `json:"incomplete"`
}

type RuntimeLoopHealth struct {
	RuntimeLoopSummary
	Items        []RuntimeLoopItem `json:"items"`
	OmittedItems int               `json:"omitted_items"`
}

type RuntimeLoopCounts struct {
	Expected        int `json:"expected"`
	Observed        int `json:"observed"`
	NoReportedStall int `json:"no_reported_stall"`
	Stalled         int `json:"stalled"`
	Missing         int `json:"missing"`
	Stale           int `json:"stale"`
	Conflicting     int `json:"conflicting"`
	Unavailable     int `json:"unavailable"`
	ReportedStalls  int `json:"reported_stalls"`
	Unexpected      int `json:"unexpected"`
}

type RuntimeLoopItem struct {
	Job              string     `json:"job"`
	Daemon           string     `json:"daemon"`
	Loop             string     `json:"loop"`
	Node             string     `json:"node,omitempty"`
	TargetID         string     `json:"target_id,omitempty"`
	Unexpected       bool       `json:"unexpected,omitempty"`
	State            string     `json:"state"`
	Reason           string     `json:"reason,omitempty"`
	ReportedStalled  *bool      `json:"reported_stalled,omitempty"`
	AgeSeconds       *float64   `json:"age_seconds,omitempty"`
	ScrapeObservedAt *time.Time `json:"scrape_observed_at,omitempty"`
	StallObservedAt  *time.Time `json:"stall_observed_at,omitempty"`
	AgeObservedAt    *time.Time `json:"age_observed_at,omitempty"`
}

func runtimeLoopsForDaemon(daemon string) []string {
	switch daemon {
	case "schedd":
		return []string{"runtime", "main"}
	case "vmmd":
		return []string{"runtime", "sweep"}
	default:
		return []string{"runtime"}
	}
}

func CloneRuntimeLoopHealth(in *RuntimeLoopHealth) *RuntimeLoopHealth {
	if in == nil {
		return nil
	}
	out := *in
	out.Items = append([]RuntimeLoopItem{}, in.Items...)
	for i := range out.Items {
		item := &out.Items[i]
		item.ReportedStalled = cloneRuntimeLoopValue(item.ReportedStalled)
		item.AgeSeconds = cloneRuntimeLoopValue(item.AgeSeconds)
		item.ScrapeObservedAt = cloneRuntimeLoopValue(item.ScrapeObservedAt)
		item.StallObservedAt = cloneRuntimeLoopValue(item.StallObservedAt)
		item.AgeObservedAt = cloneRuntimeLoopValue(item.AgeObservedAt)
	}
	return &out
}

func cloneRuntimeLoopValue[T any](in *T) *T {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
