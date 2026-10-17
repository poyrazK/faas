package ci

import (
	"context"
	"time"
)

// Health contains account-scoped work and quota counts plus a fixed, anonymous
// fleet worker summary. It is operational evidence, never compute qualification.
type Health struct {
	ObservedAt time.Time      `json:"observed_at"`
	Queues     []QueueHealth  `json:"queues"`
	Quotas     []QuotaHealth  `json:"quotas"`
	Workers    []WorkerHealth `json:"workers"`
}

type QueueHealth struct {
	Name                 string `json:"name"`
	Pending              int64  `json:"pending"`
	InFlight             int64  `json:"in_flight"`
	Dead                 int64  `json:"dead"`
	Due                  int64  `json:"due"`
	StaleLeases          int64  `json:"stale_leases"`
	OldestItemAgeSeconds int64  `json:"oldest_item_age_seconds"`
}

type QuotaHealth struct {
	Resource string `json:"resource"`
	Used     int64  `json:"used"`
	Limit    *int64 `json:"limit,omitempty"`
	Headroom *int64 `json:"headroom,omitempty"`
}

type HealthReader interface {
	CIHealth(context.Context, string) (Health, error)
}
type FleetHealthReader interface {
	CIFleetHealth(context.Context) (Health, error)
}

// WorkerKind is a closed set. Neither process identity nor account identity is
// returned or exported as a metric label.
type WorkerKind string

const (
	WorkerDispatcher          WorkerKind = "dispatcher"
	WorkerMetadataAdmission   WorkerKind = "metadata_admission"
	WorkerAutomationAdmission WorkerKind = "automation_admission"
	WorkerMetadataChecks      WorkerKind = "metadata_checks"
	WorkerExecutionChecks     WorkerKind = "execution_checks"
	WorkerRetention           WorkerKind = "retention"
)

func workerKinds() []WorkerKind {
	return []WorkerKind{WorkerDispatcher, WorkerMetadataAdmission, WorkerAutomationAdmission, WorkerMetadataChecks, WorkerExecutionChecks, WorkerRetention}
}
func validWorker(kind WorkerKind) bool {
	for _, known := range workerKinds() {
		if kind == known {
			return true
		}
	}
	return false
}

func workerConditions() []string {
	return []string{"disabled", "blocked", "starting", "healthy", "degraded", "stalled", "stale"}
}

type WorkerHealth struct {
	Name            WorkerKind       `json:"name"`
	Status          string           `json:"status"`
	Replicas        map[string]int64 `json:"replicas"`
	LastHeartbeatAt *time.Time       `json:"last_heartbeat_at,omitempty"`
	LastCycleAt     *time.Time       `json:"last_cycle_at,omitempty"`
}
