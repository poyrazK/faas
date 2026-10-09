// Package api is the onebox FaaS platform's wire surface. It is the
// public Go SDK shape: typed DTOs, the RFC 7807 problem envelope, the
// streaming SSE decoder, and the bearer/idempotency-key/pagination
// conventions every Client method honours. The daemon's pkg/api/*
// package is the server-side counterpart; see ADR-038 (issue #266) for
// the split contract this module enforces.
package api

import "time"

// File-level note: this file is the SDK copy of pkg/api/limits.go,
// trimmed to the wire types only (Plan enum, Plans slice, Limits
// struct). The authoritative planLimits table, ConntrackCapProbe, and
// platform constants stay in the daemon-only pkg/api/limits.go (the
// spec documents limits.go as the single source of truth for admission
// — spec §15 conventions; "never inline a limit at its point of use").
// See pkg/api/limits.go in the daemon for the test surface; this copy
// has no LimitsFor / MustLimitsFor / BillableRAMMB helpers because the
// values are server-side constants, not wire data.

// Plan is a customer subscription tier. The zero value is intentionally invalid
// so an unset plan never silently reads as Free.
type Plan string

const (
	PlanFree  Plan = "free"
	PlanHobby Plan = "hobby"
	PlanPro   Plan = "pro"
	PlanScale Plan = "scale"
)

// Plans lists every plan low-to-high. Order matters for upgrade/downgrade logic
// and for deterministic tests — do not reorder.
var Plans = []Plan{PlanFree, PlanHobby, PlanPro, PlanScale}

// Limits is the full quota/limit set for one plan. Every field has a spec
// reference. Add a field here (never a literal elsewhere) when a new limit
// appears. The server-side planLimits table (pkg/api/limits.go in the daemon)
// is authoritative; this struct shape is the wire form the customer sees on
// /v1/account.
type Limits struct {
	Profiling ProfilingLimits `json:"profiling"`
	Plan      Plan

	// Deploy-time quotas (enforced by apid before work happens, spec §4.2).
	DeployedApps       int // max apps in state active|evicted_cold
	MaxConcurrency     int // max instances of one app in {WAKING,COLD_BOOTING,RUNNING}
	RAMMB              int // max ram_mb per app (memory.max = RAMMB + PerVMOverheadMB)
	AppLayerMaxMB      int // drive1 ext4 cap (spec §4.6)
	SourceTarballMaxMB int // upload cap; >cap => 413 (spec §4.2)

	// Runtime shape.
	VCPU         int // firecracker vcpu_count (spec §4.4)
	IdleTimeoutS int // default idle-reaper timeout (spec §4.3)

	// Metering (spec §1, §10). Money in millicents.
	IncludedGBHours int   // included GB-RAM-hours per calendar month
	PriceMillicents int64 // monthly subscription price

	// Edge (gatewayd-internal, spec §4.1).
	RateLimitRPS   int // token-bucket refill rate
	RateLimitBurst int // token-bucket burst

	// Networking (spec §7).
	EgressMbit int // per-instance egress bandwidth cap via tc

	// Secrets (spec §11/G2). Ciphertext quota per app; per-value byte cap.
	SecretCountMax      int // max secrets per app (Free 8, Hobby 25, Pro 50, Scale 100)
	SecretValueMaxBytes int // per-secret value byte cap (Free 4K, Hobby 8K, Pro 16K, Scale 32K)

	// MinInstancesAllowed toggles the per-app cold-wake floor (ux_spec
	// §6.5). Pro + Scale opt in; Free + Hobby keep the default
	// scale-to-zero behaviour.
	MinInstancesAllowed bool

	// ScaleUpTargetRPSAllowed toggles `autoscale_target_rps` per plan
	// (issue #169 / #172). Hobby + Pro + Scale opt in; Free does not.
	ScaleUpTargetRPSAllowed bool

	// ScaleUpTargetCPUAllowed toggles `autoscale_target_cpu_pct` per
	// plan. Pro + Scale only — CPU-driven scaling on cheaper tiers is
	// unbounded.
	ScaleUpTargetCPUAllowed bool

	// Move 1 event-shaped surfaces (spec §4.4, §4.9).
	MaxQueueDepth               int
	MaxDelayedTasksPerApp       int
	MaxSourceBytesPerInvocation int
	AsyncInvokeAllowed          bool

	// EgressAllowlistAllowed toggles the per-app outbound IP allowlist
	// (ADR-031, tier-2 of the network roadmap). Pro + Scale opt in.
	EgressAllowlistAllowed bool
	// EgressAllowlistMaxSize is the per-app count cap on CIDR entries.
	// 0 with Allowed=false (Free/Hobby); non-zero with Allowed=true
	// (Pro: 16; Scale: 64).
	EgressAllowlistMaxSize int
}

// EphemeralDiskMaxMB returns the maximum writable drive1 capacity represented
// by the plan's app-layer cap. The server keeps AppLayerMaxMB as the canonical
// field for compatibility; this name makes the storage meaning explicit to
// SDK callers.
func (l Limits) EphemeralDiskMaxMB() int {
	return l.AppLayerMaxMB
}

// EphemeralDiskMaxBytes returns the ephemeral disk ceiling in bytes.
func (l Limits) EphemeralDiskMaxBytes() int64 {
	if l.EphemeralDiskMaxMB() <= 0 {
		return 0
	}
	return int64(l.EphemeralDiskMaxMB()) * 1024 * 1024
}

// Notification configuration is bounded independently of object upload bodies.
const (
	MaxObjectNotificationRules                = 1000
	MaxObjectNotificationIDRunes              = 255
	MaxObjectNotificationEvents               = 10
	MaxObjectNotificationFilterBytes          = 1024
	MaxObjectNotificationQueueNameBytes       = 63
	MaxObjectNotificationBodyBytes      int64 = 1 << 20
	MaxObjectNotificationXMLDepth             = 6
	MaxObjectNotificationXMLNodes             = MaxObjectNotificationRules*24 + 1
)

// Object Lock duration bounds mirror the server contract.
const (
	MaxObjectLockRetentionDays  int32 = 36500
	MaxObjectLockRetentionYears int32 = 100
)

const (
	BindingReleasePolicyMaxRevision    int64 = 1<<53 - 1
	BindingReleasePolicyMaxAge               = 24 * time.Hour
	BindingReleasePolicyReasonMaxBytes       = 256
)

// CPU profiling transport and admission bounds (ADR-819). These limits are
// independent of request telemetry and never change billing dimensions.
const (
	ProfileDefaultWindowSeconds                         = 10
	ProfileMinWindowSeconds                             = 1
	ProfileMaxWindowSeconds                             = 60
	ProfileMaxCaptureDuration                           = time.Duration(ProfileMaxWindowSeconds)*time.Second + ProfileTransportTimeout
	ProfileMaxCompressedBytes                           = 1 << 20
	ProfileMaxExpandedBytes                             = 8 << 20
	ProfileMaxFrameBytes                                = (ProfileMaxCompressedBytes * 2) + (32 << 10)
	ProfileControlPollInterval                          = 100 * time.Millisecond
	ProfileMaxStackDepth                                = 256
	ProfileMaxProcesses                                 = 64
	ProfileMaxDeploymentChoices                         = 64
	ProfileAttributionMaxReasons                        = 6
	ProfileAttributionMaxWarnings                       = 3
	ProfileAttributionMaxChangePercentagePoints         = 20
	ProfileRouteRegressionMaxRoutes                     = 10
	ProfileRouteRequestTimestampTolerance               = time.Second
	ProfileRouteRequestReportMaxBytes                   = 16 << 10
	ProfileRouteRequestMetadataMaxBytes                 = 2500
	ProfileRouteMaxLabeledRequests                      = int64(1000000000)
	ProfileRouteMinimumLabelCoveragePercent             = 80.0
	ProfileRouteLabelMaxChangePercentagePoints          = 20.0
	ProfileRouteMaxLabels                               = 50
	ProfileRouteMaxLabelBytes                           = 256
	ProfileRequestMixMaxRoutes                          = 50
	ProfileRequestMixSnapshotMaxRoutes                  = 20
	ProfileRequestMixSnapshotMaxBytes                   = 16 << 10
	ProfileRequestMixDifferencePercent                  = 20.0
	ProfileInvestigationMaxPerApp                       = 50
	ProfileInvestigationMaxBytes                        = 64 << 10
	ProfileInvestigationMaxStoredBytes                  = 2 * ProfileInvestigationMaxBytes
	ProfileInvestigationMaxFormBytes                    = 3*ProfileInvestigationMaxBytes + (8 << 10)
	ProfileInvestigationMaxTitleBytes                   = 160
	ProfileInvestigationMaxTextBytes                    = 8 << 10
	ProfileInvestigationMaxPathBytes                    = 16 << 10
	ProfileInvestigationMaxRevision               int64 = 9007199254740991
	ProfileRegressionDefaultRelativePercent             = 20.0
	ProfileRegressionDefaultAbsoluteCPU                 = 0.01
	ProfileRegressionDefaultAbsoluteCPUPerRequest       = 0.0001
	ProfileRegressionDefaultMinimumProfiles       int64 = 3
	ProfileRegressionDefaultCoverageRatio               = 0.8
	ProfileRegressionDefaultMinimumRequests       int64 = 20
	ProfileRegressionMaxAbsoluteCPUPerRequest           = 3600.0
	ProfileRegressionMaxMinimumRequests           int64 = 100000000
	ProfileRegressionMaxRelativePercent                 = 10000.0
	ProfileRegressionMaxAbsoluteCPU                     = 1e6
	ProfileRegressionMinimumCoverageRatio               = 0.1
	ProfileRegressionMaxEvidence                        = 10
	ProfileRegressionMaxEvidenceBytes                   = 32 << 10
	ProfileRouteCodeMaxEvidenceBytes                    = 8 << 10
	ProfileRegressionMaxAssessmentBytes                 = 64 << 10
	ProfileRegressionMaxStoredBytes                     = 2 * ProfileRegressionMaxAssessmentBytes
	ProfilePeriodicMinIntervalSeconds                   = 60
	ProfilePeriodicMaxIntervalSeconds                   = 86400
	ProfilePeriodicDefaultIntervalSeconds               = 900
	ProfilePeriodicMaxConfirmations                     = 5
	ProfilePeriodicDefaultConfirmations                 = 2
	ProfilePeriodicMaxHistory                           = 10
	ProfilePeriodicMaxRows                              = 50
	ProfilePeriodicMaxDataBytes                         = 262144

	ProfileAutoDefaultWindowSeconds = 300
	ProfileAutoDefaultWarmupSeconds = 120
	ProfileAutoMinWindowSeconds     = 60
	ProfileAutoMaxWindowSeconds     = 1800
	ProfileAutoMaxWarmupSeconds     = 3600
	ProfileAutoMaxAttempts          = 5
	ProfileAutoBatchSize            = 10
	ProfileAutoMaxResults           = 50
	ProfileAlertMaxFrames           = 5
	ProfileAlertMaxSymbolBytes      = 256
	ProfileAutoTickInterval         = 30 * time.Second
	ProfileAutoIngestionGrace       = 30 * time.Second
	ProfileAutoRetryInterval        = time.Minute
	ProfileAutoLeaseDuration        = 2 * time.Minute
	ProfileAutoDiscoveryLookback    = 24 * time.Hour
	ProfileAutoReceiptRetention     = 30 * 24 * time.Hour
	ProfileCanaryHistoryPageSize    = 5
	ProfileCanaryHistoryMaxPage     = 10
	ProfileMaxGenerationBytes       = 128
	ProfileMaxRuntimeBytes          = 64
	ProfileRetryCacheTTL            = 2 * time.Minute
	ProfileRPCOverheadBytes         = 1024
	ProfileControlMaxBytes          = 4096
	ProfileProcessStaleAfter        = 2 * time.Second
	ProfileDrainTimeout             = 500 * time.Millisecond
	ProfileNodeBootstrapPath        = "/opt/gregale/profiling/node.cjs"
	ProfilePythonBootstrapDir       = "/opt/gregale/profiling/python"
	ProfileMaxSymbolBytes           = 4096
	ProfileMaxTotalFrames           = 200000
	ProfileMaxNodes                 = 20000
	ProfileMaxViewNodes             = 5000
	ProfileMaxViewSymbolBytes       = 256 << 10
	ProfileMaxCoverageEntries       = 5000
	ProfileFailureRecordInterval    = time.Minute
	ProfileMaxChartPoints           = 120
	ProfileChartMinStep             = time.Minute
	ProfileMaxConcurrentUploads     = 4
	ProfileMaxConcurrentQueries     = 4
	ProfileMaxTrackedAccounts       = 10000
	ProfileTransportTimeout         = 2 * time.Second
	ProfileQueryTimeout             = 15 * time.Second
	ProfileVsockPort                = 1040
	ProfileLocalEndpoint            = "http://127.0.0.1:9191"
	ProfileHealthListen             = "127.0.0.1:9160"
	ProfileDefaultSocket            = "/run/faas/profiled.sock"
)

type ProfilingLimits struct {
	Enabled          bool
	RetentionDays    int
	UploadsPerMinute int
}

const (
	ProfileGateMaxConfirmations       = 5
	ProfileGateMaxTimeoutSeconds      = 86400
	ProfileGateDefaultTimeoutSeconds  = 1800
	ProfileGateDefaultConfirmations   = 2
	ProfileGateOverrideMaxReasonBytes = 1024
)

// AppEventPublishKeyMaxBytes mirrors pkg/api/limits.go for the standalone SDK.
const AppEventPublishKeyMaxBytes = 256
