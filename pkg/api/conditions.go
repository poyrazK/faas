package api

import "time"

// ResourceStatus is a resource's status subresource (ADR-20261030151810):
// its conditions, derived when read from evidence the API already holds.
// ObservedAt is when that evidence was read, so a reader knows how fresh
// the conditions are.
type ResourceStatus struct {
	Kind        string                            `json:"kind"`
	Name        string                            `json:"name"`
	ObservedAt  string                            `json:"observed_at"`
	Conditions  []Condition                       `json:"conditions"`
	Storage     *ManagedPostgresStorageStatus     `json:"storage,omitempty"`
	Maintenance *ManagedPostgresMaintenanceStatus `json:"maintenance,omitempty"`
	Backup      *ManagedPostgresBackupStatus      `json:"backup,omitempty"`
	Replica     *ManagedPostgresReplicaStatus     `json:"replica,omitempty"`
}

// ManagedPostgresReplicaStatus is the running standby's sampled health.
type ManagedPostgresReplicaStatus struct {
	Available       bool       `json:"available"`
	ReplayLagBytes  *int64     `json:"replay_lag_bytes,omitempty"`
	ObservedAt      *time.Time `json:"observed_at,omitempty"`
	SampleAgeMillis *int64     `json:"sample_age_ms,omitempty"`
}

// ManagedPostgresStorageStatus is the allocated size and last filesystem
// observations for a native PostgreSQL database.
type ManagedPostgresStorageStatus struct {
	AllocatedBytes  int64                          `json:"allocated_bytes"`
	StandbyExpected bool                           `json:"standby_expected"`
	Primary         *ManagedPostgresVolumeStatus   `json:"primary,omitempty"`
	Standby         *ManagedPostgresVolumeStatus   `json:"standby,omitempty"`
	AutoGrow        *ManagedPostgresAutoGrowStatus `json:"auto_grow,omitempty"`
	Alerts          []ManagedPostgresStorageAlert  `json:"alerts,omitempty"`
}

// ManagedPostgresStorageAlert is a recorded transition of a primary storage incident.
type ManagedPostgresStorageAlert struct {
	IncidentID   string    `json:"incident_id"`
	State        string    `json:"state"`
	SampledAt    time.Time `json:"sampled_at"`
	TotalBytes   int64     `json:"total_bytes"`
	FreeBytes    int64     `json:"free_bytes"`
	GrowthReason string    `json:"growth_reason,omitempty"`
}

func (a ManagedPostgresStorageAlert) Title() string {
	switch a.State {
	case "low_space":
		return "Low storage"
	case "read_only":
		return "Storage guard triggered read-only mode"
	case "recovered":
		return "Storage recovered"
	default:
		return a.State
	}
}

// ManagedPostgresVolumeStatus describes the filesystem at SampledAt. Fresh
// is false for parked, restarted or otherwise out-of-date observations.
type ManagedPostgresVolumeStatus struct {
	TotalBytes int64     `json:"total_bytes"`
	FreeBytes  int64     `json:"free_bytes"`
	Low        bool      `json:"low"`
	ReadOnly   bool      `json:"read_only"`
	SampledAt  time.Time `json:"sampled_at"`
	Fresh      bool      `json:"fresh"`
}

// ManagedPostgresBackupStatus reports configured retention and recorded
// backup and restore evidence for a native database.
type ManagedPostgresBackupStatus struct {
	Recovery             *ManagedPostgresRecoveryStatus     `json:"recovery,omitempty"`
	RestoreWindowSeconds int64                              `json:"restore_window_seconds"`
	LastBaseBackupAt     *time.Time                         `json:"last_base_backup_at,omitempty"`
	LatestRestoreDrill   *ManagedPostgresRestoreDrillStatus `json:"latest_restore_drill,omitempty"`
	LastVerifiedRestore  *ManagedPostgresRestoreDrillStatus `json:"last_verified_restore,omitempty"`
}

// ManagedPostgresRecoveryStatus reports the latest point proven in the
// recovery region's own archive and its age when checked.
type ManagedPostgresRecoveryStatus struct {
	Region                 string     `json:"region"`
	Outcome                string     `json:"outcome"`
	Reason                 string     `json:"reason"`
	RecoveryPoint          *time.Time `json:"recovery_point,omitempty"`
	RecoveryLagSeconds     int64      `json:"recovery_lag_seconds,omitempty"`
	MissingWALSegment      string     `json:"missing_wal_segment,omitempty"`
	RecoveryTimeline       uint32     `json:"recovery_timeline,omitempty"`
	MissingTimelineHistory string     `json:"missing_timeline_history,omitempty"`
}

// ManagedPostgresRestoreDrillStatus reports a single attempt. RecoveredTo
// is the last replayed commit reported by PostgreSQL, when available.
type ManagedPostgresRestoreDrillStatus struct {
	DrillID         string     `json:"drill_id,omitempty"`
	Outcome         string     `json:"outcome"`
	StartedAt       time.Time  `json:"started_at"`
	TargetAt        time.Time  `json:"target_at,omitzero"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	RecoveredTo     *time.Time `json:"recovered_to,omitempty"`
	ErrorCode       string     `json:"error_code,omitempty"`
	DurationSeconds *int64     `json:"duration_seconds,omitempty"`
}

// Condition is one named aspect of a resource's state, in the shape of a
// Kubernetes status condition. Reason is a stable code from the closed list
// below, safe to branch on; Message is for people. Since is set only from a
// recorded timestamp. Terminal marks a condition that will not change
// without a new request, so a wait can stop on it.
type Condition struct {
	Type     string `json:"type"`
	Status   string `json:"status"`
	Reason   string `json:"reason"`
	Message  string `json:"message"`
	Since    string `json:"since,omitempty"`
	Terminal bool   `json:"terminal,omitempty"`
}

// Condition statuses.
const (
	ConditionTrue    = "True"
	ConditionFalse   = "False"
	ConditionUnknown = "Unknown"
)

// Resource kinds with a status subresource.
const (
	ResourceKindApp        = "app"
	ResourceKindDeployment = "deployment"
	ResourceKindDatabase   = "database"
	ResourceKindDomain     = "domain"
	ResourceKindCron       = "cron"
	ResourceKindJobRun     = "job_run"
)

// Condition types.
const (
	ConditionReady       = "Ready"
	ConditionProgressing = "Progressing"
	ConditionDegraded    = "Degraded"
	ConditionRolledOut   = "RolledOut"

	ConditionVerified         = "Verified"
	ConditionCertificateReady = "CertificateReady"
	ConditionLastRunSucceeded = "LastRunSucceeded"
	ConditionSucceeded        = "Succeeded"
)

// ConditionTypes are the condition types each kind reports, in the order
// they are returned.
var ConditionTypes = map[string][]string{
	ResourceKindApp:        {ConditionReady, ConditionProgressing, ConditionDegraded},
	ResourceKindDeployment: {ConditionReady, ConditionProgressing, ConditionRolledOut},
	ResourceKindDatabase:   {ConditionReady, ConditionProgressing, ConditionDegraded},
	ResourceKindDomain:     {ConditionReady, ConditionVerified, ConditionCertificateReady},
	ResourceKindCron:       {ConditionReady, ConditionLastRunSucceeded},
	ResourceKindJobRun:     {ConditionSucceeded, ConditionProgressing},
}

// Condition reasons that are not an app health phase, an app health check
// code, a deployment, database, certificate or job run status, a cron
// suspension or a cron run outcome, which are reasons as they are.
const (
	ConditionReasonNotAssessed            = "not_assessed"
	ConditionReasonDeploying              = "deploying"
	ConditionReasonComplete               = "complete"
	ConditionReasonLatestDeploymentFailed = "latest_deployment_failed"
	ConditionReasonHealthy                = "healthy"
	ConditionReasonVerifying              = "verifying"
	ConditionReasonRollingOut             = "rolling_out"
	ConditionReasonRolledOut              = "rolled_out"
	ConditionReasonRolloutAborted         = "rollout_aborted"
	ConditionReasonStaged                 = "staged" // live with no production traffic (deploy --no-traffic)
	ConditionReasonSuspended              = "suspended"
	ConditionReasonStale                  = "stale"
	ConditionReasonStorageLow             = "storage_low"
	ConditionReasonStorageReadOnly        = "storage_read_only"
	ConditionReasonStandbyStorageLow      = "standby_storage_low"
	ConditionReasonVerified               = "verified"
	ConditionReasonDNSPending             = "dns_pending"
	ConditionReasonServing                = "serving"
	ConditionReasonScheduled              = "scheduled"
	ConditionReasonDisabled               = "disabled"
	ConditionReasonNeverRun               = "never_run"
)
