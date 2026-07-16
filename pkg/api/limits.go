// Package api holds cross-component API types shared by every daemon.
//
// limits.go is the ONE place every plan quota, ceiling, and hard limit lives.
// The financial model (ex44_faas_financial_model.xlsx) is the source of these
// numbers; the implementation spec §1/§4/§13 encodes them here. Never inline a
// limit at its point of use — read it from this table so a single edit moves the
// whole platform (spec §15 conventions).
//
// Money is integer millicents (1 cent = 1000 millicents). Floats near money fail
// review (spec §Conventions).
package api

import "fmt"

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
// appears, and cover it in limits_test.go.
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

	// Edge (gatewayd, spec §4.1).
	RateLimitRPS   int // token-bucket refill rate
	RateLimitBurst int // token-bucket burst

	// Networking (spec §7).
	EgressMbit int // per-instance egress bandwidth cap via tc
}

// planLimits is the authoritative table. Values: spec §1 quota row, §4.1 rate
// limits, §4.3 idle timeouts, §4.6 app-layer caps, §7 egress, §10 prices.
//
// Plans (deployed/concurrent/RAM MB/GB-h):
//
//	Free  1 / 1  / 128  / 5
//	Hobby 5 / 2  / 256  / 50
//	Pro   25/ 5  / 512  / 250
//	Scale 100/20 / 1024 / 1500
var planLimits = map[Plan]Limits{
	PlanFree: {
		Plan:               PlanFree,
		DeployedApps:       1,
		MaxConcurrency:     1,
		RAMMB:              128,
		AppLayerMaxMB:      256,
		SourceTarballMaxMB: 100,
		VCPU:               2,
		IdleTimeoutS:       30,
		IncludedGBHours:    5,
		PriceMillicents:    0,
		RateLimitRPS:       5,
		RateLimitBurst:     20,
		EgressMbit:         10,
	},
	PlanHobby: {
		Plan:               PlanHobby,
		DeployedApps:       5,
		MaxConcurrency:     2,
		RAMMB:              256,
		AppLayerMaxMB:      512,
		SourceTarballMaxMB: 100,
		VCPU:               2,
		IdleTimeoutS:       60,
		IncludedGBHours:    50,
		PriceMillicents:    900_000, // €9.00
		RateLimitRPS:       20,
		RateLimitBurst:     100,
		EgressMbit:         25,
	},
	PlanPro: {
		Plan:               PlanPro,
		DeployedApps:       25,
		MaxConcurrency:     5,
		RAMMB:              512,
		AppLayerMaxMB:      1024,
		SourceTarballMaxMB: 250,
		VCPU:               2,
		IdleTimeoutS:       300,
		IncludedGBHours:    250,
		PriceMillicents:    2_900_000, // €29.00
		RateLimitRPS:       100,
		RateLimitBurst:     500,
		EgressMbit:         100,
	},
	PlanScale: {
		Plan:               PlanScale,
		DeployedApps:       100,
		MaxConcurrency:     20,
		RAMMB:              1024,
		AppLayerMaxMB:      2048,
		SourceTarballMaxMB: 250,
		VCPU:               4,
		IdleTimeoutS:       600,
		IncludedGBHours:    1500,
		PriceMillicents:    9_900_000, // €99.00
		RateLimitRPS:       500,
		RateLimitBurst:     2000,
		EgressMbit:         250,
	},
}

// Global platform constants (spec §1, §13). These are the physics of the one
// box; code enforces them, telemetry verifies them.
const (
	// RAM ledger (megabytes).
	HostOSReserveMB       = 2_048  // system.slice
	ControlPlaneReserveMB = 6_144  // faas-cp.slice
	TenantRAMBudgetMB     = 56_000 // tenant budget
	TenantSliceMaxMB      = 57_344 // faas-tenant.slice memory.max hard fence
	// RAMAdmissionCeilingMB is 85% of the tenant budget — schedd admits only up
	// to this (spec §1, §4.3, invariant §6.2-2).
	RAMAdmissionCeilingMB = 47_600
	// PerVMOverheadMB is added to every instance's ram_mb for admission and
	// billing (VMM + jailer + TAP slack, spec §1, §4.7).
	PerVMOverheadMB = 8

	// CPU (spec §1).
	CPUOvercommit = 8
	VCPUSlots     = 160

	// Metering (spec §1, §10).
	OverageMillicentsPerGBHour = 1_000 // €0.01 per GB-RAM-hour

	// Builder VM (spec §4.5, §1). Builds live in the control-plane slice, never
	// tenant RAM.
	BuildVMRAMMB           = 2_048
	BuildVMVCPU            = 2
	BuildTimeoutSeconds    = 600 // 10 min build
	BuildE2ETimeoutSeconds = 900 // 15 min end-to-end

	// Snapshots / disk (spec §1, §8).
	FleetSnapshotAvgTargetMB = 130 // business metric; alert >160 warn, >200 page
	SnapshotBudgetGB         = 452

	// Edge request caps (spec §4.1).
	MaxRequestBodyBytes = 25 * 1024 * 1024 // 25 MB either direction
	WakeQueueCap        = 512              // per-app wake queue
	WakeQueueTTLSeconds = 30

	// Idle timeout tuning (spec §4.3): app-configurable down to this floor, and
	// no higher than plan default × this multiplier.
	IdleTimeoutFloorSeconds = 10
	IdleTimeoutMaxMultiple  = 2

	// Free-tier disk reaper (spec §4.3): zero requests this long => EVICTED_COLD.
	FreeTierColdEvictDays = 14
)

// LimitsFor returns the limits for a plan and whether the plan is known. Callers
// that already trust the plan (e.g. read from a CHECK-constrained column) can use
// MustLimitsFor.
func LimitsFor(p Plan) (Limits, bool) {
	l, ok := planLimits[p]
	return l, ok
}

// MustLimitsFor returns the limits for a plan and panics on an unknown plan.
// Use only where the plan is already validated (DB CHECK constraint upstream).
func MustLimitsFor(p Plan) Limits {
	l, ok := planLimits[p]
	if !ok {
		panic(fmt.Sprintf("api: unknown plan %q", p))
	}
	return l
}

// Valid reports whether p is a known plan.
func (p Plan) Valid() bool {
	_, ok := planLimits[p]
	return ok
}

// AdmissionMB is the RAM an instance charges against the admission ceiling and
// tenant slice: its plan RAM plus the fixed per-VM overhead (spec §4.3, §6.2-2).
func (l Limits) AdmissionMB() int {
	return l.RAMMB + PerVMOverheadMB
}

// IdleTimeoutBounds returns the [floor, ceiling] seconds a customer may configure
// their idle timeout to for this plan (spec §4.3).
func (l Limits) IdleTimeoutBounds() (floor, ceiling int) {
	return IdleTimeoutFloorSeconds, l.IdleTimeoutS * IdleTimeoutMaxMultiple
}
