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

import (
	"fmt"
	"time"
)

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

	// Secrets (spec §11/G2). Ciphertext quota per app; per-value byte cap.
	// SecretCountMax bounds the (app_id, key) row count. SecretValueMaxBytes
	// bounds the plaintext value the customer may PUT — apid rejects larger
	// values with 413 CodeSecretValueTooLarge before sealing.
	SecretCountMax      int // max secrets per app (Free 3, Hobby 25, Pro 50, Scale 100)
	SecretValueMaxBytes int // per-secret value byte cap (Free 4K, Hobby 8K, Pro 16K, Scale 32K)

	// MinInstancesAllowed toggles the per-app cold-wake floor (ux_spec
	// §6.5). Free + Hobby keep the default scale-to-zero behaviour
	// because `min_instances = N` keeps N × RAMMB resident at all times,
	// which is the cost shape of the always-on tier. Pro + Scale opt in.
	// apid's updateApp handler gates the PATCH body on this flag.
	MinInstancesAllowed bool
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
		Plan:                PlanFree,
		DeployedApps:        1,
		MaxConcurrency:      1,
		RAMMB:               128,
		AppLayerMaxMB:       256,
		SourceTarballMaxMB:  100,
		VCPU:                2,
		IdleTimeoutS:        30,
		IncludedGBHours:     5,
		PriceMillicents:     0,
		RateLimitRPS:        5,
		RateLimitBurst:      20,
		EgressMbit:          10,
		SecretCountMax:      3,
		SecretValueMaxBytes: 4 * 1024,
	},
	PlanHobby: {
		Plan:                PlanHobby,
		DeployedApps:        5,
		MaxConcurrency:      2,
		RAMMB:               256,
		AppLayerMaxMB:       512,
		SourceTarballMaxMB:  100,
		VCPU:                2,
		IdleTimeoutS:        60,
		IncludedGBHours:     50,
		PriceMillicents:     900_000, // €9.00
		RateLimitRPS:        20,
		RateLimitBurst:      100,
		EgressMbit:          25,
		SecretCountMax:      25,
		SecretValueMaxBytes: 8 * 1024,
	},
	PlanPro: {
		Plan:                PlanPro,
		DeployedApps:        25,
		MaxConcurrency:      5,
		RAMMB:               512,
		AppLayerMaxMB:       1024,
		SourceTarballMaxMB:  250,
		VCPU:                2,
		IdleTimeoutS:        300,
		IncludedGBHours:     250,
		PriceMillicents:     2_900_000, // €29.00
		RateLimitRPS:        100,
		RateLimitBurst:      500,
		EgressMbit:          100,
		SecretCountMax:      50,
		SecretValueMaxBytes: 16 * 1024,
		MinInstancesAllowed: true,
	},
	PlanScale: {
		Plan:                PlanScale,
		DeployedApps:        100,
		MaxConcurrency:      20,
		RAMMB:               1024,
		AppLayerMaxMB:       2048,
		SourceTarballMaxMB:  250,
		VCPU:                4,
		IdleTimeoutS:        600,
		IncludedGBHours:     1500,
		PriceMillicents:     9_900_000, // €99.00
		RateLimitRPS:        500,
		RateLimitBurst:      2000,
		EgressMbit:          250,
		SecretCountMax:      100,
		SecretValueMaxBytes: 32 * 1024,
		MinInstancesAllowed: true,
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
	// LvFcName is the LVM logical volume apps + snapshots live on (spec §8).
	// Schedd's dashboard gauge shells out to `lvs -o data_percent <LvFcName>`
	// to populate `fcvm_lv_fc_used_pct`. Empty on dev/macOS — the
	// DefaultLvFcUsedPct closure returns 0 and the gauge degrades to "no data".
	LvFcName = "lv-fc"

	// Build artifact export (M6): vmmd loopback-mounts the chroot-local drive1
	// on Destroy to copy out /build/out/image.tar (and friends). 4 GiB is
	// well above the §14 target (~130 MB) so it's not the limiting factor; it's
	// the ceiling we refuse to copy past. See pkg/fcvm/vmm.go::exportBuildArtifacts.
	MaxExportedLayerBytes int64 = 4 << 30

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

	// Instance retention (spec §17 follow-up, PR #74): STOPPED/FAILED
	// rows are DELETED by pkg/sched.Retention this long after entering
	// the terminal state. Tunable in cmd/schedd config; this default is
	// the spec baseline (30 days). Retention only touches terminal
	// instances — it never affects quota/RAM/concurrency counts because
	// those only sum non-terminal rows (state/machine.go CountsFor*).
	DefaultInstanceRetention = 30 * 24 * time.Hour
	// DefaultRetentionInterval is how often the retention sweep actually
	// runs. Once per hour is plenty — the sweep itself reads now-30d, so
	// hourly cadence means a row that just crossed 30d is deleted within
	// the next hour.
	DefaultRetentionInterval = 1 * time.Hour
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

// PlanIncludedGBHours returns the included GB-RAM-hours per calendar month
// for the plan. Returns 0 for unknown plans so callers default to "no
// quota band" rather than treating unknown as Free. The meter aggregator
// (pkg/meter.CheckQuota) compares monthly usage against this number.
func (p Plan) PlanIncludedGBHours() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.IncludedGBHours
}

// Valid reports whether p is a known plan.
func (p Plan) Valid() bool {
	_, ok := planLimits[p]
	return ok
}

// MinInstancesAllowed reports whether the plan may set the per-app
// cold-wake floor (ux_spec §6.5). Pro + Scale opt in; Free + Hobby
// stay scale-to-zero by default. apid's updateApp handler gates
// `req.MinInstances` on this; the CLI surfaces the rejection with
// CodePlanMinInstancesNotAllowed.
func (p Plan) MinInstancesAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.MinInstancesAllowed
}

// AdmissionMB is the RAM an instance charges against the admission ceiling and
// tenant slice: its plan RAM plus the fixed per-VM overhead (spec §4.3, §6.2-2).
func (l Limits) AdmissionMB() int {
	return BillableRAMMB(l.RAMMB)
}

// BillableRAMMB returns the RAM one instance charges against both the admission
// ceiling (schedd's ledger, invariant §6.2-2) and the metering ledger (meterd's
// sampler, spec §4.7): the customer's configured ram_mb plus the fixed per-VM
// overhead. Single source of truth — every site that previously inlined
// `ram_mb + PerVMOverheadMB` now goes through this helper so a future change
// to the overhead constant updates exactly one place.
func BillableRAMMB(ramMB int) int {
	return ramMB + PerVMOverheadMB
}

// IdleTimeoutBounds returns the [floor, ceiling] seconds a customer may configure
// their idle timeout to for this plan (spec §4.3).
func (l Limits) IdleTimeoutBounds() (floor, ceiling int) {
	return IdleTimeoutFloorSeconds, l.IdleTimeoutS * IdleTimeoutMaxMultiple
}
