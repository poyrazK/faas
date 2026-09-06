// Package api is the onebox FaaS platform's wire surface. It is the
// public Go SDK shape: typed DTOs, the RFC 7807 problem envelope, the
// streaming SSE decoder, and the bearer/idempotency-key/pagination
// conventions every Client method honours. The daemon's pkg/api/*
// package is the server-side counterpart; see ADR-038 (issue #266) for
// the split contract this module enforces.
package api

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
	Plan Plan

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
	SecretCountMax      int // max secrets per app (Free 3, Hobby 25, Pro 50, Scale 100)
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
