package api

import "time"

// Operator metadata only. Source values, SQL, snapshots, tokens and native
// producer/lease frames are deliberately absent from this projection.
type DataRepairHealth struct {
	ObservedAt        time.Time               `json:"observed_at"`
	Phases            []DataRepairPhaseHealth `json:"phases"`
	HeldSlots         int64                   `json:"held_slots"`
	HeldMemoryMB      int64                   `json:"held_memory_mb"`
	HeldVCPUs         int64                   `json:"held_vcpus"`
	HeldCPUMillicores int64                   `json:"held_cpu_millicores"`
	UnavailableTasks  int64                   `json:"unavailable_tasks"`
	Items             []DataRepairHealthItem  `json:"items"`
	NextCursor        string                  `json:"next_cursor,omitempty"`
	Limit             int                     `json:"limit"`
}

type DataRepairPhaseHealth struct {
	Phase                string     `json:"phase"`
	Count                int64      `json:"count"`
	OldestPhaseStartedAt *time.Time `json:"oldest_phase_started_at,omitempty"`
	OldestAgeSeconds     int64      `json:"oldest_age_seconds"`
	WarnAfterSeconds     int64      `json:"warn_after_seconds"`
}

type DataRepairHealthItem struct {
	CursorKey        string    `json:"cursor_key"`
	OperationID      string    `json:"operation_id"`
	TaskID           string    `json:"task_id,omitempty"`
	AccountID        string    `json:"account_id"`
	AppID            string    `json:"app_id"`
	NodeID           string    `json:"node_id,omitempty"`
	Phase            string    `json:"phase"`
	PhaseStartedAt   time.Time `json:"phase_started_at"`
	AgeSeconds       int64     `json:"age_seconds"`
	NodeStatus       string    `json:"node_status"`
	CapacityHeld     bool      `json:"capacity_held"`
	HeldMemoryMB     int       `json:"held_memory_mb"`
	HeldVCPUs        int       `json:"held_vcpus"`
	CancelRequested  bool      `json:"cancel_requested"`
	FailureCode      string    `json:"failure_code,omitempty"`
	NeedsAttention   bool      `json:"needs_attention"`
	RecoveryGuidance string    `json:"recovery_guidance"`
	RunbookURL       string    `json:"runbook_url"`
}

func DataRepairHealthPhases() []string {
	return []string{"queued", "running", "cleanup", "publishing", DataRepairApplying, DataRepairUnknown, DataRepairReplacing, DataRepairResolving}
}

func DataRepairHealthWarnSeconds(phase string) int64 {
	switch phase {
	case "queued":
		return DataRepairHealthQueueWarnSeconds
	case "running":
		return DataRepairTimeoutSeconds + DataRepairRehearsalCleanupSeconds
	case "cleanup":
		return DataRepairHealthCleanupWarnSeconds
	case "publishing":
		return DataRepairHealthPublishWarnSeconds
	case DataRepairApplying, DataRepairReplacing, DataRepairResolving:
		return DataRepairTimeoutSeconds
	default:
		return 0
	}
}
