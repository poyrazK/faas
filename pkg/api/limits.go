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
	"net/netip"
	"os"
	"os/exec"
	"testing"
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

// StreamingStatus is the per-request classification emitted via the
// Streaming-Status response header (ADR-102 D1/D2). String alias so
// the JSON encoder marshals the wire value verbatim and HTTP header
// Set/Get round-trips losslessly. The canonical wire values are the
// lower-case string forms of the StreamingStatus* constants declared
// in the streaming constants block below; see that block for per-value
// semantics.
//
// All non-streaming variants carry the plan-level buffered cap
// (MaxResponseBodyBytes); only the streaming variant can carry an
// endpoint-rule cap (max_body_bytes_streaming from a matched edge
// rule).
type StreamingStatus string

// Plans lists every plan low-to-high. Order matters for upgrade/downgrade logic
// and for deterministic tests — do not reorder.
var Plans = []Plan{PlanFree, PlanHobby, PlanPro, PlanScale}

// planRank maps a Plan to its rank (Free=0 … Scale=3). The lookup
// returns -1 for any unknown plan so a closed-set drift surfaces
// as a clean failure rather than a silent false-positive "you meet
// the minimum plan". Used by PlanMeetsMinimumPlan to compare tiers
// in O(1) without parsing Plans on every call.
var planRank = map[Plan]int{
	PlanFree:  0,
	PlanHobby: 1,
	PlanPro:   2,
	PlanScale: 3,
}

// PlanMeetsMinimumPlan returns true iff customer's plan rank is
// >= minimumPlan's rank. Used by enableAlertPreset to gate the
// catalog row's minimum_plan before loadApp (so a low-plan
// customer posting to a non-existent slug gets a 402, not a 404
// that would leak the slug's existence — same shape as the
// ErrPlanAlertRulesNotAllowed guard at handlers_alerts.go:158-162).
// Returns false for unknown plans (closed-set enforcement).
func PlanMeetsMinimumPlan(customer, minimumPlan Plan) bool {
	cRank, cOk := planRank[customer]
	mRank, mOk := planRank[minimumPlan]
	if !cOk || !mOk {
		return false
	}
	return cRank >= mRank
}

// GDPR self-service export rate limit (issue #755 / PR-5.1). Single
// global value (not per-plan) because the cost is per-bundle (one
// export scans every per-account table) and the abuse case is
// "customer hits the endpoint once a minute". A plan-tiered version
// would invite gaming — a Free customer hitting Pro's 5x window
// would cost the same as a Pro customer. 24h was chosen to match the
// DPA §7 30-day sub-processor notice window's "half-life" cadence —
// the export bundle contains sub-processor references, so the
// window should comfortably exceed a customer re-reading the
// sub-processor list inside one sub-processor-change cycle.
//
// ExportRateLimitWindow is the lookback window; ExportRateLimitWindowSeconds
// is the integer-seconds expression of the same value, used for the
// Retry-After header when no prior export is found in the ledger
// (the upper bound the wire will advertise).
const (
	ExportRateLimitWindow        = 24 * time.Hour
	ExportRateLimitWindowSeconds = int(24 * time.Hour / time.Second)
)

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

	// ConcurrencyPerVMBound (issue #559) is the platform-advertised
	// upper bound on concurrent in-flight requests one VM can handle
	// at the listener layer. Distinct from MaxConcurrency (the per-app
	// *instance* cap, spec §6.2-1) — this is per-VM, not per-app.
	// Concurrency above 1 is the customer's runner/process
	// responsibility: Node.js (single-event-loop), Python asyncio,
	// and Go net/http handlers process concurrent requests within
	// one process; a synchronous subprocess-per-request handler
	// (e.g. a Python reader-from-stdin script) does not. All five
	// current runners spawn one subprocess per request via cmd.Run()
	// (guest/runners/<runtime>/main.go), so the bound is the
	// listener's goroutine count, not a single process's request
	// queue. Surfaced on GET /v1/apps/{slug} as concurrency_per_vm
	// so dashboards + CLI can render the platform's per-VM bound
	// without reading limits.go. Spec §13 hard-limits table.
	ConcurrencyPerVMBound int // Free 1, Hobby 5, Pro 25, Scale 80

	// Runtime shape.
	VCPU         int // firecracker vcpu_count (spec §4.4)
	IdleTimeoutS int // default idle-reaper timeout (spec §4.3)

	// CertExpiryWarningDays (issue #961 / Mega-A PR-3) is the threshold
	// below which the cert engine emits a `cert.expiring_soon` audit
	// row and the dashboard renders the yellow banner. Same across
	// plans (no per-plan override); days, NOT hours, so the
	// customer-facing UI can render "30 days" once. Default applied
	// in Plan.LimitsFor via CertExpiryWarningDaysDefault.
	CertExpiryWarningDays int

	// End-to-end request budget (ADR-093). Per-plan overrides for
	// the platform's wall-clock deadline on every customer-facing
	// request. 0 falls back to RequestBudgetDefault /
	// RequestBudgetMax in the limits.go const block. Per-route
	// overrides via edge-rule kind=budget take precedence at
	// request time.
	RequestBudgetMs    int // 0 → RequestBudgetDefault; non-zero clamped to [1, RequestBudgetMaxMs]
	RequestBudgetMaxMs int // 0 → RequestBudgetMax; non-zero must be ≥ RequestBudgetMs

	// CPU fairness (issue #301 / ADR-044). The 3-level cgroup hierarchy
	// (faas-tenant.slice/tenant-<plan>.slice/<instance>) enforces these
	// per-plan via two complementary channels:
	//
	//   - CPUWeight is fed to the jailer --cgroup cpu.weight=N argv, so
	//     the kernel schedules bursts under the plan's share of the
	//     tenant slice. Ratio 2:4:8:16 (Free:Hobby:Pro:Scale) so
	//     Scale-customer bursts can preempt Free-customer bursts but
	//     never starve them out of their weight.
	//   - CPUQuotaUS / CPUPeriodUS are written directly to the
	//     instance's cpu.max file (jailer v1.7 has no --cgroup cpu.max
	//     arg). Together they cap per-instance compute so a Plan H
	//     hot-loop never burns the box's full multi-core share.
	//
	// The values are the issue #301 spec: Free 100ms/100ms, Hobby
	// 200ms/200ms, Pro 500ms/500ms, Scale 1000ms/1000ms.
	CPUWeight   int // kernel cpu.weight (1..10000); ratio per plan
	CPUQuotaUS  int // cpu.max quota half (microseconds)
	CPUPeriodUS int // cpu.max period half (microseconds)

	// Metering (spec §1, §10). Money in millicents.
	IncludedGBHours int   // included GB-RAM-hours per calendar month
	PriceMillicents int64 // monthly subscription price

	// Edge (gatewayd-internal, spec §4.1).
	RateLimitRPS   int // token-bucket refill rate
	RateLimitBurst int // token-bucket burst
	// RateLimitPerAccountRPM is the per-account requests/minute cap
	// (ADR-040 / issue #292). Distinct from RateLimitRPS/Burst which are
	// per-app. Bucket parameters consumed by pkg/gateway.Limiter.AllowAccount.
	// Bounds the cross-app botnet signature — a customer rotating across
	// many apps stays under per-app rps individually but cannot exceed
	// the per-minute sum across all their apps.
	RateLimitPerAccountRPM int

	// ThrottleMaxKeysPerRule (ADR-104 / issue #881 Phase 3) caps the
	// cardinality of the per-consumer bucket map for a single
	// kind=throttle rule. Distinct from EdgeRulesThrottlePerApp (the
	// per-app quota on how many throttle rules an app may hold) and
	// from RateLimitRPS/Burst (the per-rule rps/burst ceiling). The
	// value bounds the consumer-set size: when the set exceeds this
	// number, all over-cap callers collapse into a single
	// non-evicting __other__ bucket that still consumes tokens
	// (ADR-104 §"Consequences"). 0 means the plan does NOT expose
	// per-consumer throttling — the apid validator rejects any rule
	// that opts into a non-"none" KeyBy when this is 0.
	//
	// Per-plan: Free 100, Hobby 1000, Pro 5000, Scale 10000. The
	// doubling shape (100 → 1000 → 5000 → 10000) tracks plan headroom:
	// a Hobby customer can size per-key limits on a meaningful
	// fraction of their app's key space; a Scale customer can size
	// per-tenant limits in a multi-tenant deployment. Plans below
	// 100 (none today) would be trivially bypassable.
	ThrottleMaxKeysPerRule int

	// Wake-side admission (schedd layer). Throttles the rate at which
	// schedd will admit a *new* wake operation for an app or account.
	// Distinct from RateLimitRPS/Burst which throttle inbound HTTP
	// requests at the gateway edge — these cap the downstream
	// consequence (one wake per cold-boot, one wake per warm fan-out,
	// N wakes per cron tick). Consumed by
	// pkg/sched.WakeRateLimiter (ADR-099 PR-0 / ADR-080 Risk #1).
	//
	// Units: per-minute refill, with `WakeBurstPerApp` as the bucket
	// ceiling. The minute-scale (vs second-scale at the gateway) is
	// deliberate: wake admissions are burstier than HTTP requests (a
	// cron tick legitimately fires N wakes in the same second) and
	// the per-minute budget still bounds a runaway dispatch fan-out.
	//
	// WakeBurstPerAccount caps the per-account sum so a customer
	// fanning out across many apps in the same account cannot evade
	// the per-app ceiling by keeping each app under its own cap. Same
	// evasion shape as pkg/gateway.Limiter.AllowAccount.
	WakeBurstPerApp     int
	WakeBurstPerAccount int

	// Networking (spec §7).
	EgressMbit int // per-instance egress bandwidth cap via tc

	// Secrets (spec §11/G2). Ciphertext quota per app; per-value byte cap.
	// SecretCountMax bounds the (app_id, scope, key) row count across every
	// scope the customer has minted — ADR-090 D6 parallel posture for the
	// secret surface (ADR-092). A Free-tier customer with 2 prod secrets +
	// 2 staging secrets = 4 total exceeds the cap of 3 and gets 403
	// CodePlanLimitSecrets on the next PUT. SecretValueMaxBytes bounds the
	// plaintext value the customer may PUT — apid rejects larger values
	// with 413 CodeSecretValueTooLarge before sealing.
	SecretCountMax      int // max secrets per app across all scopes (Free 3, Hobby 25, Pro 50, Scale 100)
	SecretValueMaxBytes int // per-secret value byte cap (Free 4K, Hobby 8K, Pro 16K, Scale 32K)

	// Customer env vars (issue #395 / ADR-045). Plaintext per-app store
	// for non-sensitive runtime config (LOG_LEVEL, FEATURE_X, etc.). The
	// quota shape mirrors secrets minus the per-secret seal cost — values
	// are stored as-is, no ciphertext. EnvVarsMax bounds the (app_id,
	// scope, key) row count across every scope the customer has minted
	// (ADR-090 D6). EnvValueMaxBytes bounds the per-value byte cap.
	// Per-plan values are tuned to cover typical 12-factor config
	// surface without letting one app monopolise the table.
	EnvVarsMax       int // max env vars per app across all scopes (Free 8, Hobby 32, Pro 64, Scale 256)
	EnvValueMaxBytes int // per-value byte cap (Free 4K, Hobby 8K, Pro 16K, Scale 32K)

	// TrustedSignerCountMax bounds the (app_id, signer_name) row count
	// in app_trusted_signers (issue #472 / ADR-054). Mirrors
	// EnvVarsMax's posture — a config cap, not a credential one.
	// Per-plan values are tuned to cover the typical CI rotation
	// surface (3-5 publishers: GitHub Actions, GitLab CI, Jenkins,
	// Argo CD, custom in-house) without letting one app accumulate
	// an unbounded allowlist. Free plan is 0 — the open-deploy
	// posture for Free means customers on Free never need
	// require_signed=true and so never need signers either.
	// Spec §11: signature enforcement is a regulated-workload feature;
	// Free tier keeps its "ship any image" path.
	TrustedSignerCountMax int // Free 0, Hobby 4, Pro 8, Scale 16

	// RegistryCredentialMax (issue #461 / ADR-062) bounds the per-app
	// count of sealed Basic Auth credentials, one row per (app, host).
	// Free = 0 — Free cannot pull from private registries (the abuse
	// path of credentialed pulls on a single-concurrency plan is not
	// the product target). Hobby/Pro/Scale opt in with a small
	// fan-out budget: a Hobby customer's typical surface is one
	// staging + one prod registry (2); Pro/Scale absorb the
	// multi-region CI shape (5/20). Per-app, not per-account, because
	// the credential is app-scoped (different apps can target
	// different registries). apid's setRegistryCredential handler
	// gates 403 plan_registry_credentials_not_allowed when
	// RegistryCredentialMax == 0 and 413 plan_registry_credential_quota
	// when the count reaches the cap and the upsert is a fresh host.
	RegistryCredentialMax int

	// MinInstancesAllowed toggles the per-app cold-wake floor (ux_spec
	// §6.5). Free keeps the default scale-to-zero behaviour because
	// `min_instances = N` keeps N × RAMMB resident at all times, which
	// is the cost shape of the always-on tier. Hobby + Pro + Scale opt
	// in (issue #462 / ADR-058 PR-A tier-up: Hobby unlocked at PR-A
	// because the bill auto-counts via pkg/meter/sampler.go:238-239 and
	// the max_concurrency cap is bounded). apid's updateApp handler
	// gates the PATCH body on this flag.
	MinInstancesAllowed bool

	// MaxInstancesAllowed (issue #462 / ADR-058) toggles the per-app
	// ceiling on live instances. Mirrors MinInstancesAllowed: Hobby+
	// unlock, Free stays off. The customer-authored `max_instances`
	// is bounded above by the plan's MaxConcurrency (already a
	// hard cap on the wake path); the gate here is the plan-tier
	// lock, not the value-lock. The accessor
	// `Plan.MaxInstancesAllowed()` reads this field.
	MaxInstancesAllowed bool

	// MaxMinInstances (issue #557 / ADR-071) bounds the per-app
	// cold-wake floor independent of MaxConcurrency. ADR-071
	// §Decision 5: Hobby 1, Pro 3, Scale 10, Free 0. The cap is
	// tighter than today's implicit MaxConcurrency clamp (1/2/5/20)
	// because the floor is resident RAM against the §6.2-2
	// 47,600 MB ceiling — a Scale customer pinning the floor at
	// MaxConcurrency=20 would commit ~20.6 GB (~43% of ceiling)
	// from a single API call. Mirrors TrustedSignerCountMax's
	// posture: a per-plan value cap, not a plan-tier lock. Plan
	// lock is the existing MinInstancesAllowed bool. Free=0 here
	// means even if MinInstancesAllowed is unlocked, Free has no
	// floor — but Free's MinInstancesAllowed=false keeps it
	// locked off regardless.
	MaxMinInstances int // Free 0, Hobby 1, Pro 3, Scale 10

	// ScaleUpTargetRPSAllowed toggles `autoscale_target_rps` per plan
	// (issue #169 / #172). Hobby + Pro + Scale opt in; Free does not
	// (Free is single-concurrency and the per-request cost envelope
	// already covers any reasonable load). apid's updateApp handler
	// returns 403 CodePlanScaleUpNotAllowed when the plan lacks this
	// gate.
	ScaleUpTargetRPSAllowed bool

	// ScaleUpTargetCPUAllowed toggles `autoscale_target_cpu_pct` per
	// plan (issue #169 / #172). Pro + Scale only — "scale on CPU"
	// without `min_instances` set is unbounded on Hobby, where the cost
	// shape is too steep for the cheaper tier. apid's updateApp handler
	// returns 403 CodePlanScaleUpNotAllowed when the plan lacks this
	// gate.
	ScaleUpTargetCPUAllowed bool

	// Move 1 event-shaped surfaces (spec §4.4, §4.9, CLAUDE.md Hard
	// limits). The apid cap checks on POST .../queues/invocations:send
	// and POST /v1/apps/{slug}/delayed-tasks read these via
	// MustLimitsFor; schedd's drain re-checks delayed_tasks before
	// claiming a row in case the cap shifted between enqueue and tick.
	//
	// MaxQueueDepth bounds the per-app live (pending+dispatching)
	// 'queue' rows at any moment. The drain long-poll (queueReceive)
	// holds the cap stable; an empty queue stops draining.
	//
	// MaxDelayedTasksPerApp caps how many delayed_tasks an app may
	// have scheduled (pending+dispatching). Scale gets the cap-check
	// ceiling (1e6); per-task payload remains the binding
	// constraint via MaxSourceBytesPerInvocation.
	//
	// MaxSourceBytesPerInvocation is the body size cap the apid
	// enforces on every event-shaped POST (sync invoke, async
	// invoke, queue :send, delayed-task create). The matrix scales
	// sub-linearly with the source-tarball ratio so a customer can
	// send roughly the same fraction of their tier budget per
	// payload regardless of plan.
	//
	// AsyncInvokeAllowed gates the async-invoke surface: Free is
	// false (spec §4.4 reserves event-shaped primitives for paid
	// tiers). sync-invoke and queueReceive are still reachable; only
	// the 202 surface is plan-conditional.
	MaxQueueDepth               int
	MaxDelayedTasksPerApp       int
	MaxSourceBytesPerInvocation int
	AsyncInvokeAllowed          bool

	// MaxQueueAttempts (issue #394 / Move 1 dead-letter) is the
	// per-plan retry budget for queue messages that hit a transient
	// failure during drain (pkg/sched/drain.go). Once a row's
	// `attempts` reaches this value, the next transient failure
	// transitions it to state='dead_letter' (terminal) instead of
	// 'pending' (re-queued). Free = 0 means queues aren't entitled
	// anyway (MaxQueueDepth = 0); the drain keeps the legacy
	// infinite-retry behaviour because Free never queues. Hobby/Pro/
	// Scale follow the same shape as the other async-event caps:
	// small on the cheap tier, larger as we move up. The dead-letter
	// rows are readable via GET /v1/apps/{slug}/queues/dead_letter
	// (migrations/00060_invocations_dead_letter.sql lands the
	// 'dead_letter' state value + partial index).
	MaxQueueAttempts int

	// LogDeploymentFilterMax (issue #517 / PR-B, AC3) caps how
	// many concurrent `?deployment=` filters a customer may scope
	// their log stream to. The wire surface is single-valued today
	// (the SDK takes one deploymentID arg); this field is the
	// plan-tier gate the handler enforces before forwarding to
	// schedd. Free returns 0 so a Free customer's `?deployment=`
	// is rejected with `plan_deployment_filter_not_allowed`; Hobby
	// unlocks it for the typical Hobby customer's single-staging-
	// deployment workload; Pro/Scale get the higher caps the
	// per-tier multi-deployment fan-out needs.
	LogDeploymentFilterMax int

	// Cron limits (spec §4.4 / event-shaped surface). Two independent
	// caps; both populated for every plan, Free is 0/0 so the
	// per-store check returns QuotaError immediately. The handler
	// also gates Free to 402 via ErrPlanCronsNotAllowed before
	// reaching the store; the store still reads 0/0 from Limits
	// and refuses (defence in depth).
	//
	// CronLimitPerApp caps how many crons a single app may hold at
	// any moment.
	//
	// CronLimitPerAccount caps how many crons an account may hold
	// across all its apps. Independent of CronLimitPerApp — the
	// per-account cap defends against the N-apps-times-cap-per-app
	// bypass. Both enforced under the same apps-row lock in
	// pkg/state.PgStore.CreateCronIfUnderQuota.
	CronLimitPerApp     int
	CronLimitPerAccount int

	// EvictionPriorityReservedAllowed (issue #475) gates the per-app
	// reserved eviction tier. Free = false (no reserved apps on the
	// abuse-floor tier); Hobby+ = true. apid's updateApp handler
	// rejects a `reserved` PATCH on Free with 403
	// plan_eviction_priority_reserved_not_allowed. The field is
	// always set-able to 'best_effort' (the default); what the plan
	// gate controls is whether the customer may opt in to the
	// reserved tier.
	EvictionPriorityReservedAllowed bool
	// ReservedConcurrencyPerAccount caps how many apps an account
	// may park in the reserved tier simultaneously. Free = 0 (gate
	// off). Hobby = 1, Pro = 2, Scale = 4. Counts APPS (not live
	// instances) whose eviction_priority column equals 'reserved' — a
	// single reserved app with 5 concurrent instances counts as 1
	// against the cap. Enforced in apid's updateApp path under an
	// apps-row FOR UPDATE lock (mirrors CreateCronIfUnderQuota).
	ReservedConcurrencyPerAccount int

	// PublicAuthBearerAllowed (issue #477 / ADR-079) gates whether
	// the plan may opt apps into public_auth_mode='bearer'. Free =
	// false (Free apps stay public-by-default — no-signup friction);
	// Hobby+ = true. Enforced at the apid PATCH validator with 402
	// CodePlanPublicAuthBearerNotAllowed. The 'open' mode is always
	// allowed regardless of plan (default), and the 'basic' mode is
	// gated by PublicAuthBasicAllowed below.
	PublicAuthBearerAllowed bool
	// PublicAuthBasicAllowed (issue #477 / ADR-079) gates whether
	// the plan may opt apps into public_auth_mode='basic'. Free +
	// Hobby = false (basic adds a sealed-credential storage cost
	// and the Hobby customer shape doesn't typically need HTTP Basic
	// — bearer covers the dashboard/admin-endpoint use case); Pro+
	// = true. Enforced at the apid PATCH validator with 402
	// CodePlanPublicAuthBasicNotAllowed. Unknown plans fail closed
	// (return false) — same contract as the other accessors above.
	PublicAuthBasicAllowed bool

	// PublicAuthMembersOnlyAllowed (ADR-123) gates whether the
	// plan may opt apps into public_auth_mode='members_only'.
	// Free = false (Free personal-org has exactly 1 member, so
	// members_only on Free would collapse to bearer with the same
	// account — keep the abuse-floor posture clean and reject);
	// Hobby+ = true (the org/membership infrastructure is Hobby+
	// via the OrgMembersMax ladder; ADR-061 line 174). Enforced
	// at the apid PATCH validator with 402
	// CodePlanPublicAuthMembersOnlyNotAllowed. Unknown plans
	// fail closed (return false) — same contract as the other
	// accessors above.
	PublicAuthMembersOnlyAllowed bool

	// RequireAuthnDefault (issue #695 / ADR-080) is the default
	// value stamped onto a freshly created app's `require_authn`
	// column when the customer omitted `require_authn` from the
	// POST body. Per-plan truth table (Free/Hobby/Pro/Scale):
	// false / true / true / true. Free stays public-by-default;
	// Hobby+ ship secure-by-default. The companion column
	// `apps.auth_default_flipped_at` is stamped on every pre-flip
	// row by migration 00155 so pre-flip customers see no
	// behaviour change. Unknown plans fail closed (return false on
	// the accessor — the column default reverts to false).
	RequireAuthnDefault bool
	// PublicAuthModeDefault (issue #695 / ADR-080) is the default
	// mode stamped onto a freshly created app's `public_auth_mode`
	// column when the customer omitted `public_auth` from the POST
	// body. Closed enum: "open" / "bearer" / "basic". Per-plan
	// truth table: Free="open", Hobby="open" (no bearer scope on
	// Hobby), Pro="bearer", Scale="bearer". Hobby unlocks the
	// require_authn gate but not the bearer scope — defaulting to
	// "bearer" without an unlocked scope would strand the customer.
	// Unknown plans fail closed (return "open" on the accessor).
	PublicAuthModeDefault string

	// KeysMax (issue #189 / IAM-5) caps the per-account count of
	// active + grace API keys. Revoked keys are exempt — they
	// remain in the table for audit lineage but no longer count
	// against quoata. apid's createKey handler rejects with 409
	// api_key_limit_exceeded once the count reaches the cap;
	// rotateKey is quota-neutral (one new key replaces one old:
	// -1 +1 = 0 net) and is allowed at the cap. Per-plan values:
	// Free 3, Hobby 10, Pro 50, Scale 200 — tracks the typical
	// auth surface per tier (Free's 1-app customer might run 1 key
	// per deploy target; Scale's 100-app customer might run a
	// handful per team).
	KeysMax int

	// Organization limits (issue #190 / IAM-6 / ADR-061). Two
	// per-org caps; both populated for every plan, currently 0
	// until the financial model authorizes the per-plan values.
	// The same fail-closed contract as CronLimitPerApp applies:
	// 0 means the gate refuses all membership / invitation
	// operations, which is the safe default before the financial
	// model is updated. PR 1 ships the fields and the accessors
	// but does NOT invent per-plan values — those land in PR 2
	// alongside the schema.
	//
	// OrgMembersMax caps the count of active members
	// (`removed_at IS NULL`) on a non-personal org. The owner
	// counts toward the cap; the personal org is exempt (its
	// membership is exactly one). The PR 2 schema adds the
	// `removed_at` column so the cap reflects only live members.
	//
	// OrgPendingInvitationsMax caps the count of pending
	// invitations on a non-personal org. Independent of
	// OrgMembersMax — defends against the N-invites × fast-accept
	// botnet signature without blocking quiet growth.
	OrgMembersMax            int
	OrgPendingInvitationsMax int

	// AlertRuleLimitPerApp caps how many alert rules an account may pin
	// to a single app. Account-wide rules (Issue #396 / ADR-045,
	// AppID == "") count toward the per-app cap only when the rule pins
	// an app — they do not count against any per-app cap because they
	// have no app to bind against. The cap defends against a noisy
	// customer deploying N rules on a hot app.
	AlertRuleLimitPerApp int
	// AlertRuleLimitPerAccount caps the total alert rules an account
	// holds across every app plus account-wide. Independent of
	// AlertRuleLimitPerApp — the per-account cap defends against the
	// N-apps-times-cap-per-app bypass that the cron shape closed in
	// M7. Both enforced under the same apps-row lock + per-account
	// read in pkg/state.CreateAlertRuleIfUnderQuota.
	AlertRuleLimitPerAccount int
	// AlertPresetCatalogLimitPerAccount (issue #1233 / ADR-123) is
	// the informational count of catalog rows the customer may see
	// in the alert_presets catalog. NOT a per-app cap — instantiating
	// a preset counts toward the existing AlertRuleLimitPerApp /
	// AlertRuleLimitPerAccount. Default 0 (no catalog seeded); the
	// PR-A seed inserts 8 rows so every plan gets 8. Surfaced via
	// the GET /v1/alert-presets response so the CLI / dashboard can
	// render "8 presets available" without hardcoding the seed
	// count — a future ADR that re-seeds the catalog only has to
	// bump the seed + the per-plan values; consumers read the
	// accessor.
	AlertPresetCatalogLimitPerAccount int

	// EdgeRulesPerApp caps how many edge rules (ADR-089) an app may
	// hold. Per-app scope only — there is no account-wide edge rule
	// flavour. The cap defends against a noisy customer pinning
	// hundreds of rules on a hot app (the gateway's compiled-rules
	// slice iterates in priority order at request time, so the per-
	// request cost is O(rules_per_app) — bounded by this cap).
	EdgeRulesPerApp int
	// EdgeRulesJWTAllowed gates kind='jwt' rules on the plan.
	// Hobby/Pro/Scale opt in; Free stays off (the apid handler
	// returns 402 CodePlanEdgeRuleKindNotAllowed before insert).
	EdgeRulesJWTAllowed bool
	// EdgeRulesIPAllowed gates kind='ip' rules on the plan. Same
	// Hobby+ opt-in shape as EdgeRulesJWTAllowed.
	EdgeRulesIPAllowed bool
	// EdgeRulesGeoPerApp caps how many kind='geo' rules one app may
	// hold. Stricter than EdgeRulesPerApp because geo is a high-touch
	// abuse primitive — a customer with 10 geo rules on a Free app is
	// doing something exotic. The Free-tier guardrail (1 rule) covers
	// the abuse-desk customer persona ("block everything except DE")
	// before they have to upgrade. Enforced inside the per-app FOR
	// UPDATE lock in CreateEdgeRuleIfUnderQuota.
	//
	// Per-plan: Free 1, Hobby 5, Pro 25, Scale 100. Geo is available
	// on ALL plans including Free (ADR-091 D21 sub-decision hop —
	// the abuse-desk customer can't convert if they can't size geo
	// first). Plan gate is enforced via the EdgeRulesQuotaError.Kind
	// field; apid-Validate throws CodePlanEdgeRuleKindQuotaReached
	// when this trips.
	EdgeRulesGeoPerApp int
	// EdgeRulesThrottlePerApp caps how many kind='throttle' rules
	// one app may hold. ADR-091 D20.5 amendment (issue #881):
	// per-route per-method token-bucket rate limiting customers
	// attach to one (host, path, http_method) triple. Cardinality
	// is bounded by configured rules (bucket key is appID+"\x00"+
	// ruleID), so the per-app quota mirrors EdgeRulesGeoPerApp:
	// the same Free-allowed posture keeps the abuse-desk path open
	// and the same per-tier doubling shape (1/5/25/100) keeps the
	// upgrade curve predictable.
	//
	// The finer-grained sub-plan ceiling on the action itself
	// (rps ≤ plan.RateLimitRPS, burst ≤ plan.RateLimitBurst) is
	// enforced twice: at apid create/update by
	// api.EdgeRuleThrottleAction.Validate, and again at gateway
	// compile time by cmd/gatewayd-internal/edge_rules.go::
	// compileThrottleRules (defence-in-depth against a direct-DB
	// write that bypassed apid).
	//
	// Per-plan: Free 1, Hobby 5, Pro 25, Scale 100. Throttle is
	// available on ALL plans including Free (ADR-091 D20.5
	// sub-decision — same posture as Geo: a customer can size a
	// throttle on Free before upgrading). Plan gate is enforced
	// via the EdgeRulesQuotaError.Kind field; apid-Validate
	// throws CodePlanEdgeRuleKindQuotaReached when this trips.
	EdgeRulesThrottlePerApp int

	// EdgeRulesCachePerApp caps how many kind='cache' rules one
	// app may hold (ADR-122 §Decision). Mirrors the per-app
	// per-kind shape of EdgeRulesThrottlePerApp: per (host, path,
	// vary) cache rules can pin the in-process
	// pkg/gateway/response_cache.go byte ceiling, so a single
	// customer could otherwise dominate the gateway's route
	// cardinality. Cache is NOT in EdgeRuleKind.IsPaidOnly()
	// (geo/throttle precedent: gated by per-app count, not a
	// per-plan bool), so the per-plan shape lives entirely in
	// this field. The Free=0 default keeps the abuse-floor tier
	// on cold wake every time — the upsell is the wake-elision
	// guarantee.
	//
	// Per-plan: Free 0, Hobby 1, Pro 5, Scale 20. The
	// pgstore/memstore branch under CreateEdgeRuleIfUnderQuota
	// returns EdgeRuleQuotaError{PerKind: true, PerAppOnly: true}
	// when this trips; apid surfaces
	// CodePlanEdgeRuleKindQuotaReached.
	EdgeRulesCachePerApp int

	// CorsPresetsPerAccount caps how many cors_presets rows one
	// account may own in total (account-wide + app-scoped). The
	// cap defends against a customer pinning one preset per
	// partner / per customer-of-customer and inflating the
	// gateway's per-account preset map. Per-plan: Free 0, Hobby
	// 10, Pro 50, Scale 250. The Free=0 default keeps the
	// abuse-floor tier on inline CORS — the upsell is the
	// abstraction. apid-Validate throws
	// CodePlanCorsPresetQuotaReached when this trips (PR-B,
	// #979-c, slot 00295 wires the writer).
	CorsPresetsPerAccount int
	// CorsPresetsPerApp caps how many app-scoped cors_presets rows
	// one app may own. Independent of CorsPresetsPerAccount so a
	// customer with 5 apps under a Pro account can split 50
	// presets across the apps without tripping the per-account
	// cap. Per-plan: Free 0, Hobby 5, Pro 15, Scale 50. The
	// per-app shape mirrors EdgeRulesPerApp's escalation curve
	// so the upgrade ladder a customer reasons about is one
	// number per primitive.
	CorsPresetsPerApp int
	// CorsPresetMaxOrigins caps how many entries
	// cors_presets.allow_origins may hold. The cap defends
	// against a customer stuffing a 10k-entry wildcard
	// collection into a preset (the gateway's per-request
	// allowlist walk is O(AllowOrigins)). Per-plan: Free 0,
	// Hobby 25, Pro 100, Scale 500. Enforced at the apid write
	// boundary by api.CorsPreset.Validate in PR-B.
	CorsPresetMaxOrigins int
	// CorsPresetMaxAllowMethods caps how many entries
	// cors_presets.allow_methods may hold. Smaller ceiling
	// than MaxOrigins because the set is a closed enum
	// (CORS_ALLOWED_METHODS — GET/POST/PUT/PATCH/DELETE/HEAD/
	// OPTIONS in current ADR-091 D12). Per-plan: Free 0,
	// Hobby 8, Pro 8, Scale 8 (the closed-set ceiling is
	// constant across plans; the per-plan knob here is the
	// read-side cap a preset can ship, not the closed-set
	// size). Enforced at the apid write boundary.
	CorsPresetMaxAllowMethods int
	// CorsPresetMaxNameLength pins the upper bound on
	// cors_presets.name (the migration's CHECK in 00304
	// pins it to 64). Surfaced here so the apid writer
	// can reject before INSERT. Same value across plans.
	// NOT the consumer_keys name cap — that lives on
	// ConsumerKeysPerApp's creator-side apid validator.
	CorsPresetMaxNameLength int

	// ConsumerKeysPerApp caps how many consumer_keys rows
	// (ADR-120 / issue #975 item #5) one app may own. The cap
	// defends against a customer pinning one consumer key per
	// customer-of-customer and inflating the gateway-side
	// (app_id, prefix) hot-path lookup. Per-plan: Free 0, Hobby 100, Pro 100, Scale 1000. Free is 0 because the
	// abuse-floor tier cannot host multi-tenant consumer surfaces (the
	// apid CreateConsumerKey handler returns 402 CodeConsumerKeysNotAllowed
	// before the store is touched). Free switch to 0 mirrors the
	// CronLimitPerApp/OrgMembersMax 0/0 posture. Same 100-per-app floor
	// across the lower three tiers because the cardinality
	// budget per app is a tenant-stability concern, not a
	// tier-upgrade lever — the per-account cap scales with
	// the upgrade ladder. apid-Validate throws
	// CodePlanConsumerKeyQuotaReached when this trips (PR #5-B
	// wires the writer).
	ConsumerKeysPerApp int
	// ConsumerKeysPerAccount caps how many consumer_keys rows
	// one account may own in total across all its apps.
	// Independent of ConsumerKeysPerApp so a customer with N
	// apps under one account can split the per-account cap.
	// Per-plan: Free 0, Hobby 250, Pro 2500, Scale 25000. Free is 0 — same
	// abuse-floor gating as ConsumerKeysPerApp; the per-account ceiling is
	// reached before the per-app ceiling on a typical multi-app Free
	// customer, but Free is gated off entirely so neither trips.
	// Same per-app floor + 25× scale ceiling.
	ConsumerKeysPerAccount int

	// OpenAPIDocsPerDeployment caps how many captured OpenAPI
	// docs (ADR-122 / issue #975 item #1) one deployment may own.
	// The schema is 1 row per deployment (PRIMARY KEY on
	// deployment_id), so the per-deployment cap is effectively 1
	// — the field exists for forward-compatibility (issue #975
	// item #2 may want multiple doc formats per deployment).
	// Per-plan: Free 0, Hobby 1, Pro 1, Scale 1. Free is 0 — the
	// apid PATCH handler returns 402 CodePlanOpenAPIDocsNotAllowed
	// before the store is touched; the microVM still captures the
	// doc during cold boot but never serves it via apid.
	OpenAPIDocsPerDeployment int
	// OpenAPIDocMaxBytes caps the body size of one captured /
	// uploaded OpenAPI doc. Per-plan: Free 0 (irrelevant), Hobby
	// 131072, Pro 131072, Scale 131072. The cap layers on top of
	// the global constant state.OpenAPIDocMaxBytes (128 KiB) — the
	// guest-init probe hard-truncates at the global cap; the apid
	// PATCH handler validates against the per-plan cap and
	// returns 413 CodePlanOpenAPIDocTooLarge on overflow.
	OpenAPIDocMaxBytes int
	// OpenAPIDocsPerAccount caps how many captured OpenAPI docs
	// one account may own across all its deployments. Per-plan:
	// Free 0, Hobby 100, Pro 1000, Scale 10000. The cap is the
	// per-account quota that the apid PATCH handler enforces
	// via Store.CountOpenAPIDocsByAccount (the apid tail-call
	// arrives when the count == cap, before the INSERT). Scale
	// gives 10× Pro to mirror the consumer_keys 25× scale ceiling
	// scaled down by 2.5× (OpenAPI docs are larger + rarer).
	OpenAPIDocsPerAccount int
	// OpenAPIImportsPerAccount (ADR-126 / issue #975 item #2)
	// caps how many imported OpenAPI docs one account may own
	// across all its apps. Per-plan: Free 100, Hobby 1000, Pro
	// 10000, Scale 10000 (mirrors the OpenAPIDocsPerAccount
	// ladder so the two surfaces share the same per-account
	// ceiling shape — the import is overwrite-not-multi-version
	// per app, so the per-account cap is the load-bearing
	// defensive cap against throwaway rows). The apid POST
	// /v1/apps/{slug}/openapi handler enforces via
	// Store.CountOpenAPIImportsByAccount; 403 when the cap is
	// reached. Same fail-closed contract on unknown plans
	// (return 0 → caller treats as quota-exceeded).
	OpenAPIImportsPerAccount int

	// TenantSurfacesPerAccount caps how many `tenant_surfaces` rows
	// (ADR-099 / issue #879) a single account may own. The cap
	// defends against a SaaS customer pinning one surface per
	// end-customer and inflating the per-account cert inventory.
	// Per-plan: Free 0, Hobby 1, Pro 5, Scale 25. Surfacing tenant
	// hostnames beyond the cap returns 402
	// CodeTenantSurfaceQuotaReached; verified out-of-band (apid
	// doesn't dispatch cert mint). Plan gate is enforced via the
	// TenantSurfaceQuotaError.PerPlan field (TBD PR-A), apid-Validate
	// throws CodeTenantSurfaceQuotaReached when this trips.
	TenantSurfacesPerAccount int
	// TenantHostnamesPerSurface caps how many verified hostnames one
	// surface may hold. Independent of TenantSurfacesPerAccount — the
	// per-surface cap defends against the 1-surface-times-N-hostnames
	// bypass. Per-plan: Free 0 (irrelevant because Free cannot create
	// surfaces), Hobby 10, Pro 50, Scale 250. The cap is enforced in
	// pkg/state.AddTenantHostnameIfUnderQuota (TBD PR-A) under a
	// tenant_surfaces-row FOR UPDATE lock — same TOCTOU-defence
	// pattern as CreateEdgeRuleIfUnderQuota (pgstore.go:5914-5974).
	// 0 with TenantSurfacesAllowed=false (Free); non-zero with
	// TenantSurfacesAllowed=true (Hobby/Pro/Scale).
	TenantHostnamesPerSurface int
	// TenantSurfacesAllowed toggles the `tenant_surfaces` feature
	// (ADR-099 / issue #879) on the plan. Free stays off — the
	// legacy `custom_domains` path (one FQDN, one cert) carries the
	// single-tenant use case; the surface route is the upsell.
	// Hobby/Pro/Scale = true. apid's createTenantSurface handler
	// rejects a POST with 402 CodeTenantSurfaceQuotaReached when this
	// is false (a Free customer's POST hits the gate before the store
	// is touched).
	TenantSurfacesAllowed bool

	// DataPlacementHintsPerApp (ADR-098 §D5) caps how many
	// inferred/explicit data_upstreams rows one app may hold. The
	// per-app cap defends against a noisy customer pinning hundreds
	// of DB/cache hints on a hot app (the schedd wake-time chooser
	// iterates the per-app scores at placement time). Free = 0
	// (the customer can only see metadata via the dashboard; the
	// capture path is fail-closed at the handler level via
	// CodePlanLimitDataUpstreams). Per-plan: Free 0, Hobby 3, Pro
	// 10, Scale 50.
	DataPlacementHintsPerApp int

	// WebhookPerApp caps how many outbound webhook subscriptions a
	// single app may register (issue #476 / ADR-076). The plan gate
	// is enforced in pkg/state.CreateAppWebhookIfUnderQuota under an
	// apps-row FOR UPDATE lock — same TOCTOU-defence pattern as
	// CreateCronIfUnderQuota. The cap defends against a noisy
	// customer pinning every event to a single target.
	WebhookPerApp int
	// WebhookPerAccount caps the total outbound webhook subscriptions
	// an account may hold across every app. Independent of
	// WebhookPerApp — the per-account cap defends against the
	// N-apps-times-cap-per-app bypass. Both enforced in
	// pkg/state.CreateAppWebhookIfUnderQuota.
	WebhookPerAccount int

	// TriggersAllowed (issue #757 / ADR-0NN) gates the unified Trigger
	// primitive (cron + kafka + nats + redis_streams + sqs_compat +
	// in-platform queue/delayed_task). Free = false (the abuse-floor
	// tier doesn't get pull-from-broker primitives; the cron path is
	// the only synthetic-wake surface Free retains via the existing
	// CronLimitPerApp cap). Hobby/Pro/Scale = true. apid's
	// createTrigger / listTriggers handlers reject a POST on Free
	// with 402 CodePlanTriggersNotAllowed before the store is touched.
	// Independent of CronLimitPerApp — crons remain free-form on
	// Hobby with their own per-app budget; triggers cover the
	// broker-pull path with its own budget.
	TriggersAllowed bool
	// TriggerLimitPerApp caps how many triggers a single app may
	// hold at any moment. 0 for Free; positive for Hobby/Pro/Scale.
	// Both this and TriggerLimitPerAccount are enforced under the
	// same apps-row lock in pkg/state.PgStore.CreateTriggerIfUnderQuota,
	// mirroring CreateCronIfUnderQuota's TOCTOU defence
	// (pkg/state/pgstore.go:5431-5511).
	TriggerLimitPerApp int
	// TriggerLimitPerAccount caps how many triggers an account may
	// hold across all its apps. Independent of TriggerLimitPerApp —
	// the per-account cap defends against the N-apps-times-cap-per-app
	// bypass. Same fail-closed contract as TriggerLimitPerApp.
	TriggerLimitPerAccount int
	// TriggerBatchSizeMax caps the per-trigger `batch_size_max` the
	// customer may set. The migration 00267 SQL CHECK clamps to
	// [1, 5000]; this plan cap is the per-plan ceiling BELOW that
	// hard SQL ceiling. 0 for Free (triggers are gated off); Hobby
	// 50 / Pro 500 / Scale 5000. apid's createTrigger handler reads
	// both this and the SQL CHECK so a Hobby customer asking for 200
	// gets 403 trigger_batch_size_too_large with the Hobby cap in the
	// body, not a 422 from the SQL constraint.
	TriggerBatchSizeMax int
	// TriggerBatchWindowMaxSec caps the per-trigger `batch_window_ms`
	// / 1000 the customer may set. Hobby 30s / Pro 300s / Scale 300s.
	// The SQL CHECK (10ms–600s) is the hard ceiling; this plan cap
	// stops Hobby from holding 10-minute windows that the 50-size
	// cap doesn't justify. 0 with TriggersAllowed=false (Free).
	TriggerBatchWindowMaxSec int
	// TriggerMaxAttemptsMax caps the per-trigger `max_attempts`
	// setting. Hobby 3 / Pro 10 / Scale 25 — the upper bound mirrors
	// the queue-attempts pattern (per MaxQueueAttempts in
	// pkg/state/pgstore.go). The SQL CHECK [1, 25] is the hard
	// ceiling. 0 with TriggersAllowed=false (Free).
	TriggerMaxAttemptsMax int
	// TriggerRecordsPerSecondPerApp caps the per-app steady-state
	// dispatch rate schedd permits to a single trigger. The cap is
	// enforced by WakeRateLimiter (pkg/sched/rate_limit.go) — the
	// same bucket the wake path drains. Hobby 100 / Pro 1000 /
	// Scale 10000. Above the cap, records transition to dead_letter
	// with reason='rate_limited' rather than back-pressuring the
	// broker (back-pressure on Kafka consumer groups breaks the
	// group-rebalance contract). 0 with TriggersAllowed=false (Free).
	TriggerRecordsPerSecondPerApp int
	// TriggerPayloadMaxBytes (migration 00274 / audit #7) caps the
	// per-trigger `payload_max_bytes` the customer may set. SQL
	// CHECK admits [1024, 67108864] (1 KiB floor, 64 MiB ceiling);
	// this plan-level cap is below that ceiling so Hobby can't
	// park 64 MiB records that would balloon trigger_records rows.
	// Hobby 1 MiB / Pro 6 MiB / Scale 16 MiB. apid's createTrigger
	// handler rejects a value above the plan cap with
	// trigger_payload_too_large before the SQL CHECK fires.
	// MaxESMSourcesPerApp (ADR-118 / issue #757 closure, commit 3
	// of 11) is the operator-facing ALIAS for TriggerLimitPerApp
	// surfaced on GET /v1/plans/{slug}/limits under the
	// "max_esm_sources_per_app" key. Dual-emit per ADR-118
	// §"Audit vocabulary bridging": trigger.* is canonical,
	// esm.* is the operator alias. Values mirror
	// TriggerLimitPerApp exactly (0 / 2 / 10 / 50). The runtime
	// admission path reads TriggerLimitPerApp — MaxESMSourcesPerApp
	// exists only so the dashboard can render both labels without
	// a wire-shape diff.
	MaxESMSourcesPerApp int
	// MaxESMRecordsPerSecond (ADR-118) is the operator-facing
	// alias for TriggerRecordsPerSecondPerApp. Same dual-emit
	// rationale as MaxESMSourcesPerApp. Values mirror exactly
	// (0 / 100 / 1000 / 10000).
	MaxESMRecordsPerSecond int
	// BrokerEgressMbit (ADR-118 / commit 8 of 11) caps the
	// per-app broker egress bandwidth in megabits per second
	// the broker poll goroutines (pkg/sched/broker_egress.go)
	// may sustain. Enforced via the faas-brokerq.slice cgroup
	// + tc commands. Hobby 10 / Pro 50 / Scale 200. 0 for Free
	// (gated off via TriggersAllowed=false). Above the cap,
	// broker traffic is rate-limited at the host cgroup
	// boundary — the per-VM cgroup is unaffected.
	BrokerEgressMbit int
	// TLSSkipVerifyAllowed (ADR-118 / commit 2 of 11) gates the
	// `tls.skip_verify=true` flag on KafkaConfig. Hobby=false
	// (a Hobby customer's plaintext-TLS path doesn't justify
	// the weakened-verification posture). Pro / Scale = true.
	// 0 for Free (gated off). The apid createTrigger handler
	// rejects skip_verify=true on Hobby with
	// trigger_tls_skip_verify_not_allowed.
	TLSSkipVerifyAllowed   bool
	TriggerPayloadMaxBytes int

	// EgressAllowlistAllowed toggles the per-app outbound IP allowlist
	// (ADR-031, tier-2 of the network roadmap). Free + Hobby keep
	// allowlist opt-out because the abuse-desk use case is a
	// Pro+ concern (Scale customers are the ones with the budget to
	// care about egress hygiene). Pro/Scale cap their max entries
	// differently — Pro is 16, Scale 64 — the higher scale tier gets
	// a larger entry budget because SaaS-scale apps tend to integrate
	// with more upstream services. apid's updateApp handler rejects a
	// PATCH with 403 plan_egress_allowlist_not_allowed when this is
	// false.
	EgressAllowlistAllowed bool
	// EgressAllowlistMaxSize is the per-app count cap on CIDR entries.
	// 0 with Allowed=false (Free/Hobby); non-zero with Allowed=true
	// (Pro: 16; Scale: 64). apid's updateApp rejects with 400
	// egress_allowlist_too_long when the PATCH body has more entries.
	EgressAllowlistMaxSize int

	// StaticEgressIPAllowed (ADR-119) toggles the per-app static
	// outbound IP feature. Customer BYOIPs an IPv4 from their own
	// range; the host bridge aliases it and a per-host postrouting
	// MASQUERADE sibling rewrites matching tenant source traffic to
	// the customer's IP. Free/Hobby/Pro keep this off — the B2B
	// allowlist use case is a paid Scale concern, mirroring how
	// EgressAllowlistAllowed gates Pro+. apid's updateApp handler
	// rejects a PATCH with 402 plan_static_egress_ip_not_allowed
	// when this is false.
	StaticEgressIPAllowed bool
	// StaticEgressIPsPerApp is the per-app count cap on pinned
	// static egress IPs. v1 ships with 1 for Scale (the column is
	// a single inet, not a child table — see ADR-119 "Storage").
	// Bumping to N later is a per-plan int change with no schema
	// impact. 0 with Allowed=false (Free/Hobby/Pro).
	StaticEgressIPsPerApp int

	// PublicAuthIPAllowlistAllowed toggles the per-app ingress IP
	// allowlist (ADR-118; extends ADR-079's reserved 'ip_allowlist'
	// enum value). Pro/Scale only — Free/Hobby use edge rules
	// (kind='ip') for the abuse-floor posture; the per-app
	// allowlist is the Pro+ feature for SaaS-scale ingress
	// hygiene, where every CIDR is a deliberate policy decision.
	// apid's updateApp handler rejects a PATCH with 403
	// plan_public_auth_ip_allowlist_not_allowed when this is false.
	PublicAuthIPAllowlistAllowed bool
	// PublicAuthIPAllowlistMaxEntries is the per-app CIDR-entry
	// cap. 0 with Allowed=false (Free/Hobby); non-zero with
	// Allowed=true (Pro: 16; Scale: 64 — mirrors
	// EgressAllowlistMaxSize exactly). apid's updateApp rejects
	// with 400 public_auth_ip_allowlist_too_long when the PATCH
	// body has more entries.
	PublicAuthIPAllowlistMaxEntries int

	// WarmSnapshotEnabled (issue #470 / ADR-055) is the plan-gated
	// default for the per-app two-tier snapshot flag. Free/Hobby =
	// false (warm-tier apps keep both warm.snap + init.snap, which
	// is +130 MB per app on the parked disk budget — Hobby's pricing
	// tier is too cheap for that). Pro/Scale = true (the doubled
	// parked footprint is inside the 452 GB budget). Apid's
	// updateApp handler rejects Free/Hobby PATCH-true with
	// 403 plan_warm_snapshot_not_allowed; the default is applied
	// at CreateApp time so a Pro customer's brand-new app gets a
	// warm.snap without an extra PATCH.
	WarmSnapshotEnabled bool
	// WarmSnapshotMinRequestsDefault is the per-app request-count
	// threshold for warm-tier capture, applied at CreateApp when
	// the plan allows it. Free/Hobby = 0 (irrelevant because
	// WarmSnapshotEnabled = false there). Pro/Scale = 5. Range
	// [1, 100] (migration 00109 CHECK). The per-app PATCH may
	// override; both the SQL CHECK and the apid handler reject
	// out-of-range values.
	WarmSnapshotMinRequestsDefault int

	// RequireAuthn (issue #560) is the plan gate for the per-app
	// require_authn opt-in. Pro/Scale = true (Cloud Run analogue:
	// `--no-allow-unauthenticated`); Free/Hobby = false (the
	// opt-in is gated to paid tiers because every existing app
	// stays public-by-default — flipping it on is a security
	// posture change, not a feature toggle). Apid's updateApp
	// handler rejects Free/Hobby PATCH-true with 403
	// plan_require_authn_not_allowed; the column default
	// (migration 00135) is false so no existing customer is
	// affected. Cross-account tokens (caller's account_id !=
	// app.account_id) receive 403 from the gatewayd-internal
	// authz branch, not from this gate.
	RequireAuthn bool

	// TrafficSplit (issue #556 / traffic splitting across
	// deployments) is the plan gate for the per-deployment
	// traffic_percent opt-in. Pro/Scale = true; Free/Hobby =
	// false. Differs from RequireAuthn in the Hobby tier: Hobby
	// unlocks require_authn (issue #462 / ADR-058) but stays
	// locked on traffic_split because the audience is more
	// expensive — keeping N canary deployments warm is
	// RAM-billable per running second for every "extra" live
	// deployment, and Hobby's value-prop is "near-Free with a
	// floor", not "production canary rollout". Apid's create
	// + PATCH-traffic handlers reject Free/Hobby with 403
	// plan_traffic_split_not_allowed. Column default
	// (migration 00160) is 100, so every existing app routes
	// 100% to its single live row regardless of plan — the
	// gate only fires when a Free/Hobby customer tries to
	// opt-in to a non-100 traffic_percent (which is denied).
	TrafficSplit bool

	// MirrorRuleAllowed (issue #72 / ADR-125) is the plan gate
	// for the per-deployment traffic-mirroring opt-in. Pro/Scale
	// = true; Free/Hobby = false. Same Hobby-locked rationale
	// as TrafficSplit: a mirror VM wakes for every customer
	// request, billed per running second. Hobby is the
	// near-Free-with-a-floor tier where every additional wake
	// is cost-shaped against the cheap monthly bill; mirror's
	// 1:1 wake ratio is too expensive to unlock there. Apid's
	// createMirrorRule + PATCH-mirror handlers reject
	// Free/Hobby with 403 plan_mirror_not_allowed. Distinct
	// from TrafficSplit: even if the customer could unlock
	// traffic split on Hobby, mirror stays locked — the wake
	// cost shape is stricter than split's (1 wake per request
	// for mirror vs N-wakes-per-second-burst for split).
	MirrorRuleAllowed bool
	// MirrorTargetsPerApp (issue #72 / ADR-125) is the per-app
	// mirror-rule cap, enforced inside
	// CreateMirrorRuleIfUnderQuota's FOR UPDATE lock on apps
	// (mirrors CreateEdgeRuleIfUnderQuota's per-kind count
	// precedent). Free = 0 (gated off, see MirrorRuleAllowed);
	// Hobby = 0 (same gate); Pro = 1 (single canary target);
	// Scale = 3 (multi-shard rollout). The cap is per-app, not
	// per-account: a Scale customer running 10 apps can hold up
	// to 30 mirror rules total. QuotaErrorKindMirror carries
	// the Limit + Observed so the apid handler stamps both on
	// the 403 envelope via api.ErrMirrorRuleQuotaExceeded.
	MirrorTargetsPerApp int
	// WarmSnapshotMinMsDefault is the per-app time-since-first-ready
	// threshold for warm-tier capture, applied at CreateApp when
	// the plan allows it. Free/Hobby = 0 (irrelevant). Pro/Scale =
	// 2000 (matches Node.js Express / Flask framework startup).
	// AppProtocolGrpcAllowed (ADR-124 §Plan gating) is the plan
	// gate for the per-app app_protocol=grpc opt-in. Hobby/Pro/
	// Scale = true (Cloud Run analogue: gRPC framing is a paid-tier
	// feature). Free = false (no business case for gRPC traffic at
	// the free tier; the universal default 'http1' keeps every
	// pre-existing app on the buffered H1 path regardless). Apid's
	// createApp + updateApp handlers reject Free PATCH-grpc with
	// 403 plan_app_protocol_grpc_not_allowed. http1 and http2
	// are universally allowed (no per-plan gate) and validated
	// via the same accessor below.
	AppProtocolGrpcAllowed bool
	// Range [100, 60000] (migration 00109 CHECK).
	WarmSnapshotMinMsDefault int

	// StreamingEnabled (issue #471) gates the per-app streaming
	// response path through gatewayd-internal (Flusher + periodic 200 ms /
	// 256 KiB tx_bytes flush; ADR-047). Free defaults off — the
	// buffered path is the v1 contract and Free is the abuse-floor
	// tier where an unbounded stream would let one app monopolise
	// the gatewayd-internal process. Hobby/Pro/Scale default on; apid's updateApp handler
	// rejects Free PATCH with 403 plan_streaming_not_allowed (issue
	// #471 AC #3). The plan-level default is applied at CreateApp
	// time via buildApp so a Hobby customer's brand-new app is
	// streaming-ready without an extra PATCH round-trip.
	StreamingEnabled bool
	// WebSocketEnabled (issue #676 / ADR-080) gates the per-app
	// raw-bytes bridge path: when true, gatewayd-internal's Upgrade
	// detector routes inbound Connection: Upgrade + Upgrade: <token>
	// requests to the new rawStreamReverseProxy (which opens the
	// ForwardRawStream RPC and pumps raw bytes into the guest's netns
	// TCP socket). Same fail-closed shape as StreamingEnabled:
	// Free defaults off (the abuse-floor tier where a long-lived WS
	// would pin a wake past wake_idle_timeout), Hobby/Pro/Scale
	// default on. The plan-level default is applied at CreateApp
	// time in cmd/apid/handlers.go::buildApp using
	// Plan.WebSocketEnabled(); an existing app may still flip the
	// flag via PATCH (gated by Plan.WebSocketResponseAllowed so
	// Free stays off even when an admin backfills the column).
	WebSocketEnabled bool
	// RouteMetricsEnabled (ADR-093) gates the per-app per-route
	// observability surface: when true, gatewayd-internal emits three
	// additional Prometheus series keyed by an enumerated `route`
	// label (method + raw path, bounded per-app at 50 distinct entries
	// with __route_other__ as the non-evicting overflow bucket) and
	// serves the per-app reader at GET /v1/internal/apps/{slug}/routes.
	// Hobby/Pro/Scale default on; Free stays off (the abuse-floor tier
	// where per-route cardinality would not have a budget). The
	// plan-level default is applied at CreateApp time via buildApp
	// using Plan.RouteMetricsEnabled(); an existing app may still flip
	// the flag via PATCH (gated by Plan.RouteMetricsResponseAllowed
	// so Free stays off even when an admin backfills the column).
	RouteMetricsEnabled bool
	// MaxResponseBodyBytes is the per-response body cap (spec §4.1
	// for the legacy 25 MB bound; issue #471 raises the cap for
	// Hobby+ to 100 MB so LLM-style streams have headroom). 0 means
	// "fall back to api.MaxResponseBodyBytesDefault" so an unknown
	// plan fails closed rather than silently inheriting Free's cap.
	// gatewayd-internal wraps the response writer in http.MaxBytesWriter at
	// this number; PR-A leaves the writer unused on the buffered
	// path and PR-B activates it on the streaming path.
	MaxResponseBodyBytes int64
	// ResponseWriteTimeoutSeconds is the total-response-write window
	// for streaming responses (spec §4.1: 300 s; issue #471 raises
	// it to 900 s for Hobby+ so 30 s LLM streams + slow client reads
	// fit). The http.Server-level WriteTimeout is the safety net;
	// the per-flush deadline is enforced via http.ResponseController.
	// 0 means "fall back to api.ResponseWriteTimeoutDefault".
	ResponseWriteTimeoutSeconds int

	// TailEnabled (issue #667 / ADR-078) is the per-plan toggle for
	// the waitUntil(post-response tail) primitive. Free defaults ON
	// at the 5 s floor (spec §13 hard-limits table; the primitive
	// is the most-asked-for addition to the function surface, so we
	// ship it on every tier with a tight ceiling). The plan-level
	// default is applied at CreateApp time via buildApp so a
	// brand-new app on any plan is tail-ready without an extra
	// PATCH round-trip; an existing app may still disable the
	// primitive per-app via PATCH (gated by TailAllowed).
	TailEnabled bool

	// TailTimeoutS is the per-task wall-clock ceiling for a single
	// waitUntil(promise) registration (issue #667 §"Rules"). The
	// runner enforces this via context.WithTimeout(WaitUntilSec) per
	// task; on expiry, the task is cancelled and a
	// wake.tail_failed{reason=timeout} event is emitted. Plan
	// values: Free 5 s, Hobby 15 s, Pro 30 s, Scale 60 s — the
	// Pro value matches the issue's spec; Scale's 60 s ceiling is
	// the longest "send a confirmation email" latency budget that
	// still fits the reaper's G7 idle window. 0 means "feature
	// disabled" — a missing plan row fails closed rather than
	// silently inheriting a paid tier's relaxed ceiling.
	TailTimeoutS int

	// TailCapMax is the per-request structural cap on in-flight
	// waitUntil(promise) registrations (issue #667 §"Implementation
	// sketch"). Applied uniformly across plans — the issue pins
	// this as a structural constant in pkg/api/limits.go, not a
	// per-plan matrix. The runner enforces it before any
	// BumpInstanceTailCount call; over-cap attempts emit the
	// tailCapReached metric and log a wake.tail_failed{reason=cap_reached}.
	// See Plan.TailCapMax() below — the accessor returns the
	// structural value, NOT the field, so a missing plan row still
	// gets the cap.
	TailCapMax int

	// ConcurrentTailsPerInstance is the per-plan cap on in-flight
	// waitUntil(promise) registrations across all in-flight requests
	// for one instance (issue #667 §"Rules"). Distinct from
	// TailCapMax (per-request) — this is per-instance, not per-call.
	// Plan values: Free 4, Hobby 16, Pro 64, Scale 256 — designed
	// so a Pro customer's tail load can comfortably outpace their
	// MaxConcurrency=5 wake fleet, and a Scale customer's can
	// outpace MaxConcurrency=20. 0 means "feature disabled".
	ConcurrentTailsPerInstance int

	// LivenessPeriodSeconds (issue #554 / ADR-078) is the per-VM
	// liveness-probe interval (vmmd polls the guest every N seconds
	// via the new VsockLivenessPort=1028 STREAM channel). 0 means
	// "liveness is disabled for this plan" (Free defaults here). For
	// Hobby+ the field carries the plan's default probe period
	// (5 s across Hobby/Pro/Scale). Clamped to
	// [MinLivenessPeriodSeconds, MaxLivenessPeriodSeconds] at the
	// create-deployment handler. The probe survives a busy-loop
	// runner because it goes via the in-guest HTTP :8080 channel,
	// not via tap0 DNAT, so a wedged customer code can't drown the
	// probe in its own back-pressure.
	LivenessPeriodSeconds int
	// LivenessConsecutiveFailures (issue #554 / ADR-078) is N — the
	// number of consecutive failed probes that triggers
	// Engine.DestroyForLivenessFailure. After the destroy, the next
	// wake cold-boots (MarkSnapshotStale is called eagerly) and the
	// idle timer resets (TouchInstancesLastSeen). Default 3 across
	// Hobby/Pro/Scale (Free = 0 / disabled). Clamped to
	// [1, 10] at create-deployment.
	LivenessConsecutiveFailures int
	// LivenessCooldownSeconds (issue #554 / ADR-078) is the minimum
	// spacing between liveness-driven destroys on the same instance.
	// vmmd's poll goroutine refuses to fire onFail within this
	// window so a transient network blip doesn't cascade into a
	// tight restart loop. Default 60 s. Floor 10 s, ceiling 600 s.
	LivenessCooldownSeconds int
	// LivenessMaxRestarts (issue #554 / ADR-078) is the cap on the
	// number of liveness-driven restarts the system will tolerate
	// within LivenessWindowSeconds before parking the deployment
	// with parked_reason='liveness_exhausted'. Default 3. Clamped
	// to [1, 10].
	LivenessMaxRestarts int
	// LivenessWindowSeconds (issue #554 / ADR-078) is the sliding
	// window in which LivenessMaxRestarts restarts trigger the
	// deployment-park branch. Default 300 s (5 min). Floor 60 s,
	// ceiling 3600 s.
	LivenessWindowSeconds int

	// LogArchiveEnabled (issue #562) gates the per-plan log
	// archive + read-back surface (FAAS_LOG_ARCHIVE_*). Free is
	// off — the S3 backend + read-back path is a paid-tier
	// feature (the abuse-floor tier doesn't need cross-process
	// log persistence; the ring buffer is enough). Hobby/Pro/
	// Scale opt in. The plan-level gate is read by apid's
	// bgBefore wire-up (cmd/apid/main.go) and by the gatewayd-internal
	// bucket-proxy handler (issue #562 PR-B) so a Free-tier
	// customer's read-back request returns 402 immediately
	// without burning a bucket request.
	LogArchiveEnabled bool
	// LogArchiveRetentionDaysMax is the per-plan ceiling on
	// FAAS_LOG_ARCHIVE_RETENTION_DAYS. Hobby gets 7, Pro 30,
	// Scale 90 — matches the typical incident-window expectations
	// per tier (Hobby's "last week", Pro's "this month", Scale's
	// "this quarter"). 0 means "no archive on this plan" (Free).
	LogArchiveRetentionDaysMax int

	// AppErrorsRetentionDays (ADR-096 / customer-facing automatic
	// error grouping) is the per-plan retention cap on
	// app_errors / app_error_requests rows. The nightly purge
	// cron in cmd/apid/app_errors_purge.go deletes rows older
	// than this bound. MUST be <= LogArchiveRetentionDaysMax
	// for the same plan — the errors view is a stricter subset
	// of the log archive; if the archive retention widens in a
	// future release, the errors retention widens with it but
	// not faster. Free=1, Hobby=7, Pro=30, Scale=90.
	AppErrorsRetentionDays int
	// AppErrorsMaxFingerprintsPerApp (ADR-096) is the per-plan
	// ceiling on the number of distinct fingerprints the
	// gatewayd-internal recorder retains in its LRU for one
	// (account_id, app_id). Past the cap the recorder silently
	// drops + bumps faas_gateway_app_errors_recorded_total{
	// outcome="rate_limited"}. Free=50, Hobby=200, Pro=1000,
	// Scale=5000.
	AppErrorsMaxFingerprintsPerApp int
	// AppErrorsMaxRequestRowsPerFingerprint (ADR-096) is the
	// per-plan ceiling on the number of app_error_requests rows
	// retained per fingerprint for the drill-down view. Older
	// rows beyond the cap are deleted first on the retention
	// purge. Free=25, Hobby=100, Pro=500, Scale=1000.
	AppErrorsMaxRequestRowsPerFingerprint int

	// DebugTelemetryEnabled (ADR-127 / production debugger) gates
	// whether the per-request telemetry plane is on for an
	// account. Free=false (the abuse-floor tier carries no
	// debugger surface; the upsell is the wake-elision / rollback
	// guarantee). Hobby/Pro/Scale=true. Surfaced at
	// cmd/apid/handlers_debug_telemetry.go via
	// api.ErrPlanFeatureGated.
	DebugTelemetryEnabled bool
	// DebugTelemetryRetentionDays (ADR-127) is the per-plan cap
	// on how long a request_telemetry row is queryable. The
	// RetentionOnceRequestTelemetry sweep in
	// pkg/meter/retention.go drops the oldest monthly partition
	// whose max(received_at) is older than this bound. Free=0
	// (off), Hobby=3, Pro=7, Scale=14.
	DebugTelemetryRetentionDays int
	// DebugTelemetryRequestsPerMinute (ADR-127) is the per-account
	// rate cap on the IncrementRequestTelemetry ingest RPC. The
	// recorder publisher reads this at startup; per-record
	// overflow returns outcome {code: RATE_LIMITED, retry_after}
	// rather than dropping silently. Hobby=1000, Pro=10000,
	// Scale=50000.
	DebugTelemetryRequestsPerMinute int
	// DebugTelemetryDeploymentsPerApp (ADR-127 §Decision 4) is
	// the per-app ceiling on distinct deployment_id labels the
	// gateway_request_duration_seconds histogram admits. Past the
	// cap the deploymentLabelSet (pkg/gateway/deployment_label_set.go)
	// collapses the label to "__other__" — same discipline as
	// accountLabelSet (pkg/wire/metrics.go:256, 312). Hobby=10,
	// Pro=50, Scale=200.
	DebugTelemetryDeploymentsPerApp int
	// DebugTelemetrySpansPerTrace (ADR-127 §Decision 5) caps the
	// number of customer OTel spans the platform retains per
	// request_telemetry row's spans_summary jsonb. Past the cap
	// the slowest N are kept and the rest truncated. Hobby=50,
	// Pro=200, Scale=1000.
	DebugTelemetrySpansPerTrace int

	// PerAppMetricsAllowed (issue #TBD / ADR-TBD) gates whether
	// the customer-facing per-app observability surface is on for
	// an account. The surface covers
	// GET /v1/apps/{slug}/metrics (latency / error rate / cold-boot
	// ratio / wake count) and the JSON mirror of the wake-timeline
	// page (GET /v1/apps/{slug}/wake-timeline). Free=false (the
	// abuse-floor tier carries no per-app dashboard; the upsell is
	// the "see what you're paying for" expectation). Hobby/Pro/
	// Scale=true. Surfaced at cmd/apid/handlers_metrics.go and the
	// new cmd/apid/handlers_wake_timeline.go via
	// api.ErrPlanPerAppMetricsNotAllowed.
	PerAppMetricsAllowed bool

	// AppUsageSummaryAllowed (issue #TBD / ADR-TBD) gates whether
	// the customer-facing per-app billing-usage read is on. The
	// surface covers GET /v1/apps/{slug}/usage — the current-cycle
	// GB-hours + request rollup + plan-included vs overage split.
	// Free=false (the tier carries no usage dashboard; the upsell
	// is the §4.7 billing-transparency expectation). Hobby/Pro/
	// Scale=true. Surfaced at cmd/apid/handlers_usage.go via
	// api.ErrPlanAppUsageSummaryNotAllowed.
	AppUsageSummaryAllowed bool

	// AppErrorsAllowed (issue #TBD / ADR-TBD) gates whether the
	// per-app error-fingerprint read is on for an account. The
	// surface covers GET /v1/apps/{slug}/errors/summary (top
	// fingerprints + drill-down). Free=false (the tier carries no
	// grouped-error view; the upsell is the "see what failed"
	// expectation). Hobby/Pro/Scale=true. The retention ceiling is
	// AppErrorsRetentionDays (Free=1, Hobby=7, Pro=30, Scale=90),
	// so a downgraded customer sees the smaller of the two windows
	// automatically — the handler clamps the `since` window to
	// now().Add(-AppErrorsRetentionDays). Surfaced at
	// cmd/apid/handlers_app_errors.go via
	// api.ErrPlanAppErrorsNotAllowed.
	AppErrorsAllowed bool
}

// UpstreamProbeMaxConcurrent (ADR-098 §D2) is the global worker-pool
// cap on meterd's upstream-probe loop. Read by cmd/meterd at boot
// time and by pkg/meter/upstream_probe.go on each loop tick. NOT a
// per-plan field — the probe runs on the meterd daemon, not on the
// customer app, and the global cap defends the meterd node's fd /
// goroutine budget under burst. Lives as a top-level constant (not
// on the Limits struct) per ADR-098 §263.
const UpstreamProbeMaxConcurrent = 64

// UpstreamFitMinDeltaMs (ADR-098 §D3) is the global threshold below
// which schedd's chooser bias is suppressed (the legacy
// RAM/vCPU/region tie-break wins). Defends against flapping: a
// probe-sample delta of <5 ms is noise, not a signal. Lives as a
// top-level constant (not on the Limits struct) per ADR-098 §263.
const UpstreamFitMinDeltaMs = 5

// UpstreamAffinityTTL (ADR-098 §D2) is the staleness budget on
// schedd's in-process upstream-affinity cache. Matches the
// meterd probe cadence (pkg/meter/upstream_probe.go
// DefaultUpstreamProbeInterval = 30 s) so the cached preferred
// region is never more than one probe-cycle stale. Overridable
// via FAAS_UPSTREAM_AFFINITY_TTL on schedd startup; the engine
// constructor takes a duration so callers can stub it in tests.
// Lives as a top-level constant (not on the Limits struct) per
// ADR-098 §263.
const UpstreamAffinityTTL = 30 * time.Second

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
		Plan:           PlanFree,
		DeployedApps:   1,
		MaxConcurrency: 1,
		RAMMB:          128,
		// ConcurrencyPerVMBound (issue #559): Free is the
		// single-concurrency tier — one VM serves one request at a
		// time. Mirrors MaxConcurrency (= 1) because a Free customer
		// cannot have more than one VM per app anyway, so the
		// per-VM and per-app bounds collapse to the same number.
		ConcurrencyPerVMBound: 1,
		// AppLayerMaxMB 256 — Free is the lowest cap tier; spec §1 ("App-
		// layer build ... Free 256 MB") and the limits table both read 256
		// (PR #241 spec-drift audit, 2026-07-26). This is a no-op
		// alignment comment; the value was 256 before this audit too.
		AppLayerMaxMB:         256,
		SourceTarballMaxMB:    100,
		VCPU:                  2,
		IdleTimeoutS:          30,
		CertExpiryWarningDays: 30,
		IncludedGBHours:       5,
		PriceMillicents:       0,
		RateLimitRPS:          5,
		RateLimitBurst:        20,
		EgressMbit:            10,
		SecretCountMax:        3,
		SecretValueMaxBytes:   4 * 1024,
		EnvVarsMax:            8,
		EnvValueMaxBytes:      4 * 1024,
		// TrustedSignerCountMax: Free keeps the open-deploy posture;
		// signature enforcement is a regulated-workload feature that
		// Free never needs (issue #472 / ADR-054).
		TrustedSignerCountMax: 0,
		// Issue #461: Free has no private-registry credential surface.
		// Handler returns 403 plan_registry_credentials_not_allowed.
		RegistryCredentialMax: 0,
		// Move 1: async invoke and queues are paid-only (§4.4); Free
		// keeps HTTP-only. The tiny 1 KB payload cap is the binding
		// constraint should a Free customer spoof the gate.
		MaxQueueDepth:               0,
		MaxDelayedTasksPerApp:       0,
		MaxSourceBytesPerInvocation: 0,
		AsyncInvokeAllowed:          false,
		// Free: queues aren't entitled (MaxQueueDepth = 0). Value is
		// kept at 0 for symmetry with the rest of the async-event
		// caps. The drain falls back to legacy infinite-retry when
		// budget == 0, but Free customers never reach the queue
		// surface so the path is unreachable.
		MaxQueueAttempts: 0,
		// Autoscale (issue #169 / #172): Free stays off. The per-request
		// cost envelope already covers Free's load shape, and a "scale
		// up" trigger on a 1-concurrency plan is meaningless.
		ScaleUpTargetRPSAllowed: false,
		ScaleUpTargetCPUAllowed: false,
		// Cron: Free has no crons (spec §4.4 paid-only, like async
		// invoke). Handler returns 402 ErrPlanCronsNotAllowed before
		// the store is touched; the 0/0 here is a defence-in-depth
		// value the store still reads.
		CronLimitPerApp:     0,
		CronLimitPerAccount: 0,
		// Issue #475: Free stays off the reserved eviction tier. The
		// abuse-floor tier has no reserved-tier entitlement; per-account
		// cap is 0 so the gate fails closed.
		EvictionPriorityReservedAllowed: false,
		ReservedConcurrencyPerAccount:   0,
		// Issue #477 / ADR-079: Free stays on the no-signup-friction
		// path — public-by-default, no bearer/basic opt-in. The 'open'
		// default mode is always available regardless of plan, so
		// existing Free apps keep working with no migration work.
		PublicAuthBearerAllowed:      false,
		PublicAuthBasicAllowed:       false,
		PublicAuthMembersOnlyAllowed: false,
		// Issue #695 / ADR-080: Free stays public-by-default. The
		// token gate isn't unlocked on Free (RequireAuthn=false above);
		// matching the default literal avoids customers creating a
		// Free app and immediately seeing 401 from the gateway.
		RequireAuthnDefault:   false,
		PublicAuthModeDefault: "open",
		// IAM-5 (issue #189): Free gets 3 keys — one for the customer's
		// primary deploy target + one for a staging slot + one for
		// break-glass. The abuse-vector (scripted key rotation under
		// 1-concurrency) is bounded by the per-account rate limit.
		KeysMax: 3,
		// IAM-6 / ADR-061 PR-2 (issue #190): Free is the abuse-floor
		// tier and cannot host shared orgs — 0/0 stays by plan
		// policy, mirroring CronLimitPerApp and
		// EvictionPriorityReservedAllowed on Free. Personal orgs are
		// unaffected (they never read these caps). The financial
		// model is authoritative; reconciliation is a follow-up if
		// the workbook diverges from this fail-closed value.
		OrgMembersMax:            0,
		OrgPendingInvitationsMax: 0,
		// Alert rules (issue #396 / ADR-045): Free stays at 0/0.
		// Gates via CodePlanAlertRulesNotAllowed at the handler level
		// — the value is informational here for fail-closed accessors.
		AlertRuleLimitPerApp:              0,
		AlertRuleLimitPerAccount:          0,
		AlertPresetCatalogLimitPerAccount: 8,
		// Edge rules (ADR-089): Free gets 5/app — the 5 cheap
		// kinds (route, rewrite, redirect, headers, cors). JWT and
		// IP stay Hobby+ only (paid-only security primitives).
		EdgeRulesPerApp:     5,
		EdgeRulesJWTAllowed: false,
		EdgeRulesIPAllowed:  false,
		// Per-kind geo quota (ADR-091 D21/D22). Free gets exactly 1
		// geo rule — the abuse-desk customer ("block everything
		// except DE") is one rule. The upgrade path raises the cap
		// to 5/25/100.
		EdgeRulesGeoPerApp: 1,
		// kind='throttle' per-route rate limit cap (ADR-091 D20.5
		// amendment, issue #881). Mirrors EdgeRulesGeoPerApp so the
		// upgrade curve from Free → Scale is a single double/triple
		// progression a customer can predict.
		EdgeRulesThrottlePerApp: 1,
		// kind='cache' per-app quota (ADR-122 §Decision). Free=0:
		// the abuse-floor tier stays on cold wake every time; the
		// upsell is the wake-elision guarantee. Same posture as
		// tenant_surfaces / alert_rules / cors_presets on Free.
		EdgeRulesCachePerApp: 0,
		// CORS presets (issue #975 item #4 / Mega-Foundation #979-b,
		// slot 00294). Free=0 mirrors the tenant_surfaces / alert_rules
		// posture: the abstraction is the upsell, the abuse-floor tier
		// stays on inline kind=cors rules. PR-A ships the read path
		// and the limits; PR-B (#979-c, slot 00295) wires the apid
		// writer that trips the per-plan caps.
		CorsPresetsPerAccount:     0,
		CorsPresetsPerApp:         0,
		CorsPresetMaxOrigins:      0,
		CorsPresetMaxAllowMethods: 0,
		CorsPresetMaxNameLength:   64,
		// Consumer keys (ADR-120 / issue #975 item #5). Free
		// gets the 100/app floor — every plan does. The
		// per-account ceiling stays low (250) so an abuse-tier
		// customer can't pin one consumer key per customer-of-
		// customer indefinitely. PR-A ships the limits; PR-B
		// wires the writer.
		// Free is gated to 0 consumer keys — mirrors CronLimitPerApp/OrgMembersMax 0/0 posture.
		ConsumerKeysPerApp:     0,
		ConsumerKeysPerAccount: 0,
		// Open API docs (ADR-122 / issue #975 item #1). Free is
		// gated to 0 — the apid GET / PATCH handlers return 402
		// CodePlanOpenAPIDocsNotAllowed. The microVM still
		// captures the doc during cold boot but the row is never
		// served (the count is 0 so the per-account quota can't
		// trip either).
		OpenAPIDocsPerDeployment: 0,
		OpenAPIDocMaxBytes:       0,
		OpenAPIDocsPerAccount:    0,
		OpenAPIImportsPerAccount: 100,
		// Per-consumer throttle key cap (ADR-104, issue #881 Phase 3).
		// Free customers can size per-key throttles on a small slice
		// of their key space; large-cardinality per-key limits
		// require a paid plan.
		ThrottleMaxKeysPerRule: 100,
		// Tenant surfaces (ADR-099 / issue #879): Free is the
		// abuse-floor tier. The `tenant_surfaces` feature is the
		// upsell — Free customers carry the single-tenant case via
		// the legacy `custom_domains` path. apid's createTenantSurface
		// handler rejects 402 CodeTenantSurfaceQuotaReached before
		// the store is touched.
		TenantSurfacesPerAccount:  0,
		TenantHostnamesPerSurface: 0,
		TenantSurfacesAllowed:     false,
		// Data-placement hints (ADR-098 §D5): Free is gated off —
		// the handler returns 402 CodePlanLimitDataUpstreams before
		// any regex match. The 0 here is a defence-in-depth value
		// the handler still reads.
		DataPlacementHintsPerApp: 0,
		// Outbound webhook subscription caps (issue #476 / ADR-076).
		// Free has no webhooks — the handler returns 402
		// CodePlanWebhooksNotAllowed before the store is touched.
		WebhookPerApp:     0,
		WebhookPerAccount: 0,
		// Trigger primitive (issue #757 / ADR-0NN): Free is the
		// abuse-floor tier — TriggersAllowed=false so a POST on a
		// Free account gets 402 CodePlanTriggersNotAllowed before the
		// store is touched. The 0/0 cap pair is the fail-closed
		// defence-in-depth value the store still reads.
		TriggersAllowed:               false,
		TriggerLimitPerApp:            0,
		TriggerLimitPerAccount:        0,
		TriggerBatchSizeMax:           0,
		TriggerBatchWindowMaxSec:      0,
		TriggerMaxAttemptsMax:         0,
		TriggerRecordsPerSecondPerApp: 0,
		TriggerPayloadMaxBytes:        0,
		// ADR-118 / issue #757 closure: ESM-alias caps + broker
		// egress + TLS skip-verify gate. Free is the abuse-floor
		// tier — every value is 0 / false to fail-closed.
		MaxESMSourcesPerApp:    0,
		MaxESMRecordsPerSecond: 0,
		BrokerEgressMbit:       0,
		TLSSkipVerifyAllowed:   false,
		// Per-account rate limit (ADR-040): Free gets 50/min — enough for
		// the 1-concurrency plan's traffic envelope.
		RateLimitPerAccountRPM: 50,
		// Wake-side admission throttle (ADR-099 PR-0). Free caps
		// wake admissions at 1/min per app + 1/min per account — the
		// abuse-floor tier should never burst-wake. The apid-side
		// Free-plan gate is the primary block; this is the schedd
		// backstop for the path that bypasses apid (cron, jobs).
		WakeBurstPerApp:     1,
		WakeBurstPerAccount: 1,
		// Log deployment filter (issue #517 / PR-B): Free is the
		// abuse-floor tier — the filter is a paid feature.
		// Handler returns WritePlanDeploymentFilterNotAllowedError
		// before the store is touched; the 0 here is a
		// defence-in-depth value the handler still reads.
		LogDeploymentFilterMax: 0,
		// CPU fairness (issue #301 / ADR-044): Free gets the smallest
		// slice weight=2 and the tightest quota (100ms/100ms). 100 ms
		// is enough headroom for a Free-tier app to handle a handful of
		// requests without a throttle trip but stops a tight loop from
		// preempting other slice members.
		CPUWeight:   2,
		CPUQuotaUS:  100_000,
		CPUPeriodUS: 100_000,
		// Streaming (issue #471 / ADR-047): Free is the abuse-floor
		// tier — buffered path stays the contract, default off, no
		// cap lift (spec §4.1 baseline 25 MB / 300 s).
		StreamingEnabled:            false,
		MaxResponseBodyBytes:        MaxResponseBodyBytesDefault,
		ResponseWriteTimeoutSeconds: ResponseWriteTimeoutDefault,
		// WebSocket / Upgrade bridge (issue #676 / ADR-080): Free
		// is the abuse-floor tier — a long-lived WS would pin a
		// wake past wake_idle_timeout (the 30 s Free idle window).
		// Default off; apid PATCH rejects with 403
		// plan_websocket_not_allowed.
		WebSocketEnabled: false,
		// Per-route metrics (ADR-093): Free is the abuse-floor tier
		// — per-route cardinality would not have a budget (Free
		// apps share the §12 dashboard series set with paid apps).
		// Default off; apid PATCH rejects with 403
		// plan_route_metrics_not_allowed. Hobby+ customers opt in.
		RouteMetricsEnabled: false,
		// Warm-snapshot (issue #470 / ADR-055): Free is off by
		// plan. Warm-tier apps keep warm.snap + init.snap on the
		// parked disk budget; doubling the per-app snapshot
		// footprint is incompatible with the Free pricing tier.
		WarmSnapshotEnabled:            false,
		WarmSnapshotMinRequestsDefault: 0,
		WarmSnapshotMinMsDefault:       0,
		// Issue #560: Free is gated off — the opt-in is
		// a paid-tier feature (Cloud Run's
		// `--no-allow-unauthenticated` shape). The column
		// default (false) keeps every existing customer
		// public-by-default.
		RequireAuthn: false,
		// AppProtocolGrpcAllowed (ADR-124): Free does not
		// unlock gRPC framing at the customer edge. The
		// universal default 'http1' keeps every Free app on
		// the legacy H1 path regardless; the gate only fires
		// if a Free customer tries PATCH app_protocol=grpc.
		AppProtocolGrpcAllowed: false,
		// TrafficSplit (issue #556): Free does not unlock
		// per-deployment traffic splitting. The column
		// default (100) keeps today's behaviour — 100% to the
		// single live row — so no existing Free customer is
		// affected; the gate only fires when a Free customer
		// passes a non-100 traffic_percent on create (403
		// plan_traffic_split_not_allowed).
		TrafficSplit: false,
		// Mirror (issue #72 / ADR-125): Free stays locked — see
		// the Limits.MirrorRuleAllowed comment for the cost
		// rationale. MirrorTargetsPerApp = 0 keeps the field
		// shaped for the QuotaError tripwire (the per-app count
		// is what CreateMirrorRuleIfUnderQuota compares against).
		MirrorRuleAllowed:   false,
		MirrorTargetsPerApp: 0,
		// Tail primitive (issue #667 / ADR-078): Free enables with
		// the floor timeout (5 s) and the floor concurrency cap (4).
		// Customers on Free get the primitive, just tightly bounded —
		// the structural TailCapMax = 16 still applies, but the
		// per-instance concurrency is the binding constraint. A
		// pathological tail on Free is killed by the 5 s watchdog in
		// snapshotAndPark before it can hold a wake past the G7 idle
		// window.
		TailEnabled:                true,
		TailTimeoutS:               TailTimeoutFloorSeconds,
		TailCapMax:                 TailCapMax,
		ConcurrentTailsPerInstance: 4,
		// Liveness (issue #554 / ADR-078): Free is gated off — a
		// wedged Free VM is already bounded by the §13 M7
		// free-stop path at 5 GB-h, so the liveness probe adds no
		// safety on the abuse-floor tier. The zero-valued fields
		// (Period=0, Consecutive=0, Cooldown=0, MaxRestarts=0,
		// Window=0) cause `Plan.LivenessAllowed()` to return false
		// via the fail-closed default — see §Comment at
		// LivenessPeriodSeconds.
		// Log archive (issue #562): Free is the abuse-floor tier
		// and doesn't get the S3 archive + read-back surface —
		// the in-process ring buffer is the only log surface. The
		// shipper's bgBefore closure fails closed on this gate
		// (returns immediately on ctx.Done()).
		LogArchiveEnabled:          false,
		LogArchiveRetentionDaysMax: 0,
		// ADR-096 error grouping. Free = 1-day retention, 50
		// fingerprints, 25 request rows per fingerprint — the
		// abuse-floor tier. Retention MUST be <= the log-archive
		// retention above (which is 0 for Free; "no archive, no
		// grouped errors either" is the consistent posture).
		AppErrorsRetentionDays:                1,
		AppErrorsMaxFingerprintsPerApp:        50,
		AppErrorsMaxRequestRowsPerFingerprint: 25,
		// ADR-127 production debugger. Free is gated off: no
		// debugger surface, no rate budget, no retention. The
		// handler returns 402 ErrPlanFeatureGated before the store
		// is touched; the 0/0/0/0 here is the fail-closed
		// defence-in-depth value the store still reads.
		DebugTelemetryEnabled:           false,
		DebugTelemetryRetentionDays:     0,
		DebugTelemetryRequestsPerMinute: 0,
		DebugTelemetryDeploymentsPerApp: 0,
		DebugTelemetrySpansPerTrace:     0,
		// Per-app observability surface (Free = off). 0/0/0 is the
		// fail-closed defence-in-depth value the store still reads;
		// the handler-level ErrPlan*NotAllowed 402 is the primary
		// gate.
		PerAppMetricsAllowed:   false,
		AppUsageSummaryAllowed: false,
		AppErrorsAllowed:       false,
	},
	PlanHobby: {
		Plan:                  PlanHobby,
		DeployedApps:          5,
		MaxConcurrency:        2,
		RAMMB:                 256,
		AppLayerMaxMB:         512,
		SourceTarballMaxMB:    100,
		VCPU:                  2,
		IdleTimeoutS:          60,
		CertExpiryWarningDays: 30,
		IncludedGBHours:       50,
		PriceMillicents:       900_000, // €9.00
		// ConcurrencyPerVMBound (issue #559): Hobby = 5 — smallest
		// paid tier, matches Cloud Run's framing. Spec §4.9.1.
		ConcurrencyPerVMBound: 5,
		RateLimitRPS:          20,
		RateLimitBurst:        100,
		EgressMbit:            25,
		SecretCountMax:        25,
		SecretValueMaxBytes:   8 * 1024,
		EnvVarsMax:            32,
		EnvValueMaxBytes:      8 * 1024,
		// TrustedSignerCountMax: Hobby is the lowest paid tier; the
		// 4-publisher cap covers a hobbyist running a single CI
		// (GitHub Actions) + a backup CI (Codeberg) + a personal
		// signing key + an emergency break-glass. Anything beyond
		// that is "you're a Pro" territory.
		TrustedSignerCountMax: 4,
		// Issue #461: Hobby = 2 — staging + production.
		RegistryCredentialMax: 2,
		// 64 KB envelope = 0.25 % of Hobby's 25 MB tarball budget — small
		// enough to keep the drain tick bounded, large enough for typical
		// JSON event payloads.
		MaxQueueDepth:               5,
		MaxDelayedTasksPerApp:       5,
		MaxSourceBytesPerInvocation: 64 * 1024,
		AsyncInvokeAllowed:          true,
		// Hobby: 3 attempts. Tight on the cheap tier — a worker that
		// keeps re-trying a bad payload would otherwise burn the
		// per-app rps budget and starve the rest of the queue.
		MaxQueueAttempts: 3,
		// Autoscale: Hobby is gated on Pro+ for both RPS and CPU
		// (2026-07-28: ADR-037 amendment — Hobby→Pro re-tier on
		// ScaleUpTargetRPSAllowed). CPU-driven scaling is gated
		// on Pro+ because the cost shape of "scale on CPU without
		// a min_instances floor" is unbounded on Hobby.
		ScaleUpTargetRPSAllowed: false,
		ScaleUpTargetCPUAllowed: false,
		// Scaling policy (issue #462 / ADR-058, PR-A tier-up):
		// Hobby now unlocks `MinInstancesAllowed` (warm-floor
		// charge is bounded — Hobby's MaxConcurrency is 2 and
		// the bill auto-counts via pkg/meter/sampler.go:238-239).
		// MaxInstancesAllowed follows the same Hobby+ gate.
		// Hobby still does NOT unlock `ScaleUpTargetRPSAllowed`
		// nor `ScaleUpTargetCPUAllowed` — those remain Pro+ on
		// the existing cost-shape rationale. The doc copy on
		// the dashboard's "Plan" page names "Hobby+ unlocks
		// warm floor" so a Hobby customer opting in knows what
		// they're paying for.
		MinInstancesAllowed: true,
		MaxInstancesAllowed: true,
		// MaxMinInstances (ADR-071): Hobby gets 1 — one warm
		// instance is the minimum the floor feature exists to
		// deliver (the customer's "first request never pays the
		// §6.3 wake budget" expectation).
		MaxMinInstances: 1,
		// Cron: Hobby gets a small per-app budget (5) and a per-account
		// budget that absorbs ~2 Hobby-tier apps (10). Tracks the
		// Hobby apps cap (5) with headroom for the cron-example
		// template's tutorials.
		CronLimitPerApp:     5,
		CronLimitPerAccount: 10,
		// Issue #475: Hobby gets 1 reserved-tier app. One healthcheck-
		// critical service (status page, uptime probe) is the typical
		// Hobby workload that needs cross-account RAM-pressure
		// protection; Hobby's MaxConcurrency=2 already bounds the
		// resident instance count, so a single reserved app is
		// comfortable headroom for the tier's economics.
		EvictionPriorityReservedAllowed: true,
		ReservedConcurrencyPerAccount:   1,
		// Issue #477 / ADR-079: Hobby unlocks bearer (API-key-protected
		// private webhook receivers, dashboard admin endpoints) but
		// basic stays gated — basic adds sealed-credential storage cost
		// the Hobby customer shape doesn't typically need. The
		// 'open' mode is always available.
		PublicAuthBearerAllowed:      true,
		PublicAuthBasicAllowed:       false,
		PublicAuthMembersOnlyAllowed: true,
		// Issue #695 / ADR-080: Hobby unlocks the token gate as a
		// default (RequireAuthnAllowed is gated above so customers
		// can't PATCH-true, but defaults can stamp true). The mode
		// stays "open" because Hobby doesn't unlock the bearer scope
		// (PublicAuthBearerAllowed above is the gate for the PATCH;
		// a Hobby customer with mode='bearer' default would have no
		// way to authenticate). Customers who want the bearer
		// experience upgrade to Pro.
		RequireAuthnDefault:   true,
		PublicAuthModeDefault: "open",
		// IAM-5 (issue #189): Hobby gets 10 keys — 2 per app across
		// the Hobby app budget (5) keeps every deploy target
		// (CI / staging / prod / personal / monitoring) with a
		// dedicated key.
		KeysMax: 10,
		// IAM-6 / ADR-061 PR-2 (issue #190): Hobby gets 10 members /
		// 5 pending invitations — tracks the KeysMax ratio (IAM-5
		// shapes team headroom as 2× the per-account app budget).
		// Pending invitations stay at 1/2 of members because the
		// default invitation TTL is short (7d) and the typical Hobby
		// customer issues a handful at a time. Financial model is
		// authoritative — derived value, reconciliation follow-up.
		OrgMembersMax:            10,
		OrgPendingInvitationsMax: 5,
		// Alert rules (issue #396): Hobby gets 3 per-app and 10
		// per-account — a Hobby customer with 2 apps + 1 account-wide
		// rule lands inside both caps. The per-account floor tracks the
		// cron shape (10) because the typical Hobby customer configures
		// "one alert per app" and the spare capacity is for a couple of
		// account-wide rules.
		AlertRuleLimitPerApp:              3,
		AlertRuleLimitPerAccount:          10,
		AlertPresetCatalogLimitPerAccount: 8,
		// Edge rules (ADR-089): Hobby gets 25/app and unlocks the
		// JWT + IP kinds.
		EdgeRulesPerApp:     25,
		EdgeRulesJWTAllowed: true,
		EdgeRulesIPAllowed:  true,
		EdgeRulesGeoPerApp:  5,
		// kind='throttle' per-route rate limit cap (ADR-091 D20.5
		// amendment, issue #881). Mirrors EdgeRulesGeoPerApp.
		EdgeRulesThrottlePerApp: 5,
		// kind='cache' per-app quota (ADR-122 §Decision). Hobby=1
		// gives a single cached route per app (e.g. GET /catalog)
		// to demonstrate the wake-elision value before the
		// customer upgrades to Pro.
		EdgeRulesCachePerApp: 1,
		// CORS presets (issue #975 #4 / Mega-Foundation #979-b, slot
		// 00294). Hobby is the entry paid tier — 10 presets per
		// account, 5 per app. MaxOrigins 25 covers the typical
		// "partners + sub-domains" allowlist; 8 AllowMethods is the
		// closed-set ceiling.
		CorsPresetsPerAccount:     10,
		CorsPresetsPerApp:         5,
		CorsPresetMaxOrigins:      25,
		CorsPresetMaxAllowMethods: 8,
		CorsPresetMaxNameLength:   64,
		// Consumer keys (ADR-120 / issue #975 item #5). Hobby
		// keeps the 100/app floor; per-account cap stays at 250
		// (same as Free — Hobby is the entry paid tier, not a
		// big-leap on this primitive).
		ConsumerKeysPerApp:     100,
		ConsumerKeysPerAccount: 250,
		// Open API docs (ADR-122 / issue #975 item #1). Hobby
		// is the entry paid tier — 1 doc per deployment (the
		// schema's natural shape), 100 docs per account, 128
		// KiB per doc (the global cap).
		OpenAPIDocsPerDeployment: 1,
		OpenAPIDocMaxBytes:       131072,
		OpenAPIDocsPerAccount:    100,
		OpenAPIImportsPerAccount: 1000,
		// Per-consumer throttle key cap (ADR-104, issue #881 Phase 3).
		ThrottleMaxKeysPerRule: 1000,
		// Tenant surfaces (ADR-099 / issue #879): Hobby is the
		// entry paid tier — 1 surface with up to 10 verified
		// hostnames. The "single SaaS customer, a handful of
		// end-customer subdomains" use case is the hobby use case.
		TenantSurfacesPerAccount:  1,
		TenantHostnamesPerSurface: 10,
		TenantSurfacesAllowed:     true,
		// Data-placement hints (ADR-098 §D5): Hobby unlocks the
		// capture path with a 3-hint cap per app.
		DataPlacementHintsPerApp: 3,
		// Outbound webhook subscription caps (issue #476 / ADR-076).
		// Hobby gets 3/app, 10/account — mirrors the alert-rule ratio.
		WebhookPerApp:     3,
		WebhookPerAccount: 10,
		// Trigger primitive (issue #757 / ADR-0NN): Hobby is the
		// entry paid tier — unlocks the in-platform queue kind and
		// the sqs_compat kind (the two no-external-broker shapes).
		// Kafka/NATS/Redis-streams kinds land on Pro+ because their
		// egress policies require the allowlist opt-in. Batch /
		// window / attempts caps are tight (50 / 30 s / 3) so a
		// Hobby customer's fan-out can't saturate schedd's per-app
		// WakeRateLimiter bucket.
		TriggersAllowed:               true,
		TriggerLimitPerApp:            2,
		TriggerLimitPerAccount:        10,
		TriggerBatchSizeMax:           50,
		TriggerBatchWindowMaxSec:      30,
		TriggerMaxAttemptsMax:         3,
		TriggerRecordsPerSecondPerApp: 100,
		// Hobby: 10 Mbit broker egress is enough for a 2-source
		// Hobby fan-out without saturating the shared NIC.
		// Skip-verify disallowed: Hobby customers don't get a
		// weakened-TLS posture. ESM aliases mirror the trigger.* caps.
		MaxESMSourcesPerApp:    2,
		MaxESMRecordsPerSecond: 100,
		BrokerEgressMbit:       10,
		TLSSkipVerifyAllowed:   false,
		// Hobby payload cap: 1 MiB — keeps trigger_records rows
		// small enough that Hobby fan-out doesn't bloat Postgres.
		// The migration-00274 SQL ceiling is 64 MiB so there's
		// headroom for Pro+ below the hard limit.
		TriggerPayloadMaxBytes: 1048576,
		// Per-account rate limit (ADR-040): Hobby gets 200/min — ~10× the
		// Hobby per-app rps (20) so per-app trips first on a single hot
		// app, and the account limit catches the cross-app botnet.
		RateLimitPerAccountRPM: 200,
		// Wake-side admission throttle (ADR-099 PR-0). Hobby
		// permits a small wake burst — a cron tick on a Hobby
		// customer's job can legitimately want 5 wakes/min across
		// their apps. The per-account cap (10/min) is the ceiling
		// for a fan-out across many apps in the same account.
		WakeBurstPerApp:     5,
		WakeBurstPerAccount: 10,
		// Log deployment filter (issue #517 / PR-B): Hobby gets
		// 1 — the typical Hobby customer runs one staging
		// deployment alongside their prod slot, and the filter
		// scopes the log stream to it. Mirror shape of Hobby's
		// per-app cron cap (5).
		LogDeploymentFilterMax: 1,
		// CPU fairness (issue #301): Hobby weight=4, quota 200ms/200ms.
		// Doubles Free's quota — tracks the per-app concurrency bump
		// (1 → 2) and the per-app rps (5 → 20).
		CPUWeight:   4,
		CPUQuotaUS:  200_000,
		CPUPeriodUS: 200_000,
		// Streaming (issue #471 / ADR-047): Hobby is the first paid
		// tier — streaming is opt-in by default (the LLM use case is
		// the Hobby customer's entry point). Cap lifts to 100 MB / 900 s
		// to cover a 30–120 s chat completion plus headroom.
		StreamingEnabled:            true,
		MaxResponseBodyBytes:        100 * 1024 * 1024,
		ResponseWriteTimeoutSeconds: 900,
		// WebSocket / Upgrade bridge (issue #676 / ADR-080): Hobby
		// is the first paid tier — opt-in by default (the LLM/agent
		// use case is the Hobby customer's entry point, and many
		// agent SDKs speak WS over a thin HTTP boundary). The
		// 100 MB / 900 s caps above cover a long-poll or chat WS
		// session comfortably.
		WebSocketEnabled: true,
		// Per-route metrics (ADR-093): Hobby is the first paid tier
		// — opt-in by default (Hobby customers hosting APIs are the
		// core "which endpoint is slow?" use case). The per-app
		// route cap (50) + __route_other__ overflow bound the
		// cardinality regardless of the customer's traffic shape.
		RouteMetricsEnabled: true,
		// Warm-snapshot (issue #470 / ADR-055): Hobby is gated off
		// for the same cost-shape reason as Free — doubling the
		// parked per-app snapshot footprint doesn't fit the
		// €9/month Hobby price point. Pro/Scale customers pay
		// enough that the +130 MB per warm-tier app is comfortably
		// inside the 452 GB parked budget.
		WarmSnapshotEnabled:            false,
		WarmSnapshotMinRequestsDefault: 0,
		WarmSnapshotMinMsDefault:       0,
		// Issue #560: Hobby is gated off for the same
		// posture-change shape as Free — flipping a public
		// app to token-gated is a security decision, not a
		// feature toggle, and the issue pairs it with
		// internal-only ingress (Pro+).
		RequireAuthn: false,
		// AppProtocolGrpcAllowed (ADR-124): Hobby unlocks
		// gRPC framing — gRPC server-streaming is a paid-tier
		// feature consistent with Hobby's "near-Free with a
		// floor" value-prop. Customers on Hobby may PATCH
		// app_protocol=grpc freely.
		AppProtocolGrpcAllowed: true,
		// TrafficSplit (issue #556): Hobby does not unlock
		// per-deployment traffic splitting. Hobby's value-prop
		// is "near-Free with a floor" (MinInstancesAllowed
		// unlocked by issue #462 / ADR-058), not "production
		// canary rollout". The 2-3 live deployment bill shape
		// costs 2-3× the per-running-second RAM; Hobby's price
		// point doesn't cover it. Free/Hobby see 403
		// plan_traffic_split_not_allowed when they try to
		// pass a non-100 traffic_percent on create or PATCH.
		TrafficSplit: false,
		// Mirror (issue #72 / ADR-125): Free stays locked — see
		// the Limits.MirrorRuleAllowed comment for the cost
		// rationale. MirrorTargetsPerApp = 0 keeps the field
		// shaped for the QuotaError tripwire (the per-app count
		// is what CreateMirrorRuleIfUnderQuota compares against).
		MirrorRuleAllowed:   false,
		MirrorTargetsPerApp: 0,
		// Tail primitive (issue #667 / ADR-078): Hobby unlocks
		// the 15 s timeout + 16 per-instance concurrent tails.
		// Matches the issue's "send a confirmation email"
		// latency budget comfortably; over-cap attempts emit
		// the tailCapReached metric and log the failure.
		TailEnabled:                true,
		TailTimeoutS:               15,
		TailCapMax:                 TailCapMax,
		ConcurrentTailsPerInstance: 16,
		// Liveness (issue #554 / ADR-078): Hobby unlocks at the
		// first paid tier. Default §13 values: 5 s probe period,
		// 3 consecutive failures, 60 s cooldown, 3 restarts in 300 s
		// window before the deployment is parked. The customer can
		// tighten these per-deployment via DeploymentLivenessProbe
		// (clamped to [1, 60] / [1, 10] / [10, 600] / [1, 10] /
		// [60, 3600] respectively), but Hobby's plan defaults are
		// the conservative Cloud Run parity baseline — the same
		// `liveness_period × N + cold_boot_budget` envelope that
		// matches the issue AC #1 of ≤ 5 × N + 30 s.
		LivenessPeriodSeconds:       DefaultLivenessPeriodSeconds,
		LivenessConsecutiveFailures: DefaultLivenessConsecutiveFailures,
		LivenessCooldownSeconds:     DefaultLivenessCooldownSeconds,
		LivenessMaxRestarts:         DefaultLivenessMaxRestarts,
		LivenessWindowSeconds:       DefaultLivenessWindowSeconds,
		// Log archive (issue #562): Hobby unlocks the archive
		// + read-back surface at the first paid tier. 7-day
		// retention matches the "last week" incident-window
		// expectation of a Hobby customer; shipper cycle is the
		// 5-minute default so a Hobby customer's logs land in
		// S3 within the spec §4.1 latency budget.
		LogArchiveEnabled:          true,
		LogArchiveRetentionDaysMax: 7,
		// ADR-096 — Hobby = "last week" retention, 200 fingerprints,
		// 100 request rows per fingerprint. Retention equals the
		// log-archive retention.
		AppErrorsRetentionDays:                7,
		AppErrorsMaxFingerprintsPerApp:        200,
		AppErrorsMaxRequestRowsPerFingerprint: 100,
		// ADR-127 production debugger. Hobby gets the small-tier
		// debugger surface: 3-day retention, 1000 req/min, up to
		// 10 distinct deployments in the histogram, 50 spans per
		// trace. The Hobby customer is debugging a single Hobby
		// app — the 3-day cap matches the log-archive retention
		// (spec §4.7).
		DebugTelemetryEnabled:           true,
		DebugTelemetryRetentionDays:     3,
		DebugTelemetryRequestsPerMinute: 1000,
		DebugTelemetryDeploymentsPerApp: 10,
		DebugTelemetrySpansPerTrace:     50,
		// Per-app observability surface (Hobby = on). The Hobby
		// tier is the lowest paid plan; the upsell is "see what
		// you're paying for" rather than a debug-telemetry
		// capability.
		PerAppMetricsAllowed:   true,
		AppUsageSummaryAllowed: true,
		AppErrorsAllowed:       true,
	},
	PlanPro: {
		Plan:                  PlanPro,
		DeployedApps:          25,
		MaxConcurrency:        5,
		RAMMB:                 512,
		AppLayerMaxMB:         1024,
		SourceTarballMaxMB:    250,
		VCPU:                  2,
		IdleTimeoutS:          300,
		CertExpiryWarningDays: 30,
		IncludedGBHours:       250,
		PriceMillicents:       2_900_000, // €29.00
		// ConcurrencyPerVMBound (issue #559): Pro allows up to
		// 25 concurrent in-flight requests per VM. Matches the
		// typical SaaS-tier workload envelope (one Node/Python
		// service handling fan-out from a single client request).
		ConcurrencyPerVMBound: 25,
		RateLimitRPS:          100,
		RateLimitBurst:        500,
		EgressMbit:            100,
		SecretCountMax:        50,
		SecretValueMaxBytes:   16 * 1024,
		EnvVarsMax:            64,
		EnvValueMaxBytes:      16 * 1024,
		// Issue #461: Pro = 5 — multi-region + CI shapes.
		RegistryCredentialMax: 5,
		MinInstancesAllowed:   true,
		MaxInstancesAllowed:   true,
		// MaxMinInstances (ADR-071): Pro = 3 — covers a small
		// "always-warm fan-out for a customer-facing API" pattern
		// without letting one Pro app reserve a quarter of the
		// box's RAM ceiling.
		MaxMinInstances: 3,
		// TrustedSignerCountMax: Pro covers a small-team rotation
		// matrix (5-8 publishers). Enough for "every dev has their own
		// key" workflows without letting the table grow unbounded.
		TrustedSignerCountMax: 8,
		// 256 KB = 0.1 % of Pro's 250 MB tarball.
		MaxQueueDepth:               25,
		MaxDelayedTasksPerApp:       50,
		MaxSourceBytesPerInvocation: 256 * 1024,
		AsyncInvokeAllowed:          true,
		// Pro: 10 attempts. Trades tolerance against "a poisoned row
		// churns indefinitely". At 10 retries a transient downstream
		// flap has plenty of room, while a permanently-bad payload
		// exits the worker pool within ~50 s at the default retry
		// backoff (5 s).
		MaxQueueAttempts: 10,
		// ADR-031: Pro gets 16 CIDR entries — enough for "1 SaaS +
		// 1 webhook + 1 monitoring + ~10 partner integrations" which
		// is the typical Pro-tier reachability graph.
		EgressAllowlistAllowed: true,
		EgressAllowlistMaxSize: 16,
		// ADR-119: Pro does NOT unlock static egress IP in v1 —
		// this is a Scale-only feature. The Go zero values
		// (false/0) apply; no explicit assignment needed. The
		// accessor `Plan.StaticEgressIPAllowed()` fail-closes on
		// PlanPro, returning false.

		// PublicAuthIPAllowlist: same shape as egress — paid-only
		// abuse-desk primitive. 16 entries covers a Pro customer's
		// "1 office VPN + 1 CI runner + 1 partner API + ~10
		// regional allowlist ranges" reachability graph.
		PublicAuthIPAllowlistAllowed:    true,
		PublicAuthIPAllowlistMaxEntries: 16,
		// Autoscale: Pro gets both RPS and CPU targets. The CPU target
		// is gated on Pro+ to bound the "scale on CPU without a
		// min_instances floor" cost shape.
		ScaleUpTargetRPSAllowed: true,
		ScaleUpTargetCPUAllowed: true,
		// Cron: Pro gets 20 per-app and 50 per-account. The per-app
		// ceiling is 4× Hobby (5→20); the per-account ceiling is 5×
		// Hobby (10→50) — slightly steeper because Pro customers
		// run more apps (25) than Hobby (5).
		CronLimitPerApp:     20,
		CronLimitPerAccount: 50,
		// Issue #475: Pro gets 2 reserved-tier apps. Pro customers
		// run customer-facing APIs + background workers; the +1 vs
		// Hobby tracks the +5 Pro app budget. Reserved-tier RAM cost
		// is still bounded by MaxConcurrency (5) and the per-app
		// ram_mb cap (512 MB), so 2 reserved apps at full concurrency
		// is ~5.2 GB resident — well inside the 47.6 GB ceiling.
		EvictionPriorityReservedAllowed: true,
		ReservedConcurrencyPerAccount:   2,
		// Issue #477 / ADR-079: Pro unlocks both bearer and basic. Basic
		// is the right shape for Pro's typical webhook-receiver /
		// admin-endpoint use cases where HTTP Basic is the customer's
		// existing primitive. Sealed-credential storage cost is
		// negligible at Pro scale (~50 apps).
		PublicAuthBearerAllowed:      true,
		PublicAuthBasicAllowed:       true,
		PublicAuthMembersOnlyAllowed: true,
		// Issue #695 / ADR-080: Pro unlocks both the token gate
		// (RequireAuthn above) AND the bearer scope (PublicAuthBearerAllowed
		// above). Default new apps to (true, "bearer") so the customer
		// inherits secure-by-default. The opt-out path --no-require-authn
		// --public-auth=open is universal across all plans and gates.
		RequireAuthnDefault:   true,
		PublicAuthModeDefault: "bearer",
		// IAM-5 (issue #189): Pro gets 50 keys — 2 per app across the
		// Pro app budget (25) plus a per-team allowance (CI / staging
		// / prod / personal / monitoring / break-glass).
		KeysMax: 50,
		// IAM-6 / ADR-061 PR-2 (issue #190): Pro gets 50 members /
		// 25 pending invitations — tracks KeysMax (50) one-to-one so
		// every team member can hold a key for their own deploy
		// target. Financial model is authoritative — derived value,
		// reconciliation follow-up.
		OrgMembersMax:            50,
		OrgPendingInvitationsMax: 25,
		// Alert rules (issue #396): Pro gets 10 per-app and 30
		// per-account. ~2× the Hobby per-account budget tracks the
		// Pro app budget (25 apps vs Hobby's 5).
		AlertRuleLimitPerApp:              10,
		AlertRuleLimitPerAccount:          30,
		AlertPresetCatalogLimitPerAccount: 8,
		// Edge rules (ADR-089): Pro gets 100/app with JWT + IP.
		EdgeRulesPerApp:     100,
		EdgeRulesJWTAllowed: true,
		EdgeRulesIPAllowed:  true,
		EdgeRulesGeoPerApp:  25,
		// kind='throttle' per-route rate limit cap (ADR-091 D20.5
		// amendment, issue #881). Mirrors EdgeRulesGeoPerApp.
		EdgeRulesThrottlePerApp: 25,
		// kind='cache' per-app quota (ADR-122 §Decision). Pro=5 —
		// enough for a small catalogue: home, list, detail, search,
		// plus one wildcard. Same five-fold upgrade as throttle and
		// geo so the upsell curve is single-shape.
		EdgeRulesCachePerApp: 5,
		// CORS presets (issue #975 #4 / Mega-Foundation #979-b, slot
		// 00294). Pro is the typical SaaS tier — 50 presets per
		// account, 15 per app, 100 origins per preset.
		CorsPresetsPerAccount:     50,
		CorsPresetsPerApp:         15,
		CorsPresetMaxOrigins:      100,
		CorsPresetMaxAllowMethods: 8,
		CorsPresetMaxNameLength:   64,
		// Consumer keys (ADR-120 / issue #975 item #5). Pro
		// keeps the 100/app floor; per-account cap steps up to
		// 2500 — a typical SaaS customer with ~25 apps × 100
		// keys each fits comfortably with headroom.
		ConsumerKeysPerApp:     100,
		ConsumerKeysPerAccount: 2500,
		// Open API docs (ADR-122 / issue #975 item #1). Pro keeps
		// the 1/deployment shape; 1000 per account (10× Hobby to
		// match the consumer_keys 2500-vs-250 ratio).
		OpenAPIDocsPerDeployment: 1,
		OpenAPIDocMaxBytes:       131072,
		OpenAPIDocsPerAccount:    1000,
		OpenAPIImportsPerAccount: 10000,
		// Per-consumer throttle key cap (ADR-104, issue #881 Phase 3).
		ThrottleMaxKeysPerRule: 5000,
		// Tenant surfaces (ADR-099 / issue #879): Pro gets 5 surfaces
		// with up to 50 verified hostnames each — the growing-SaaS
		// tier. Each surface still binds to one app, so 5 surfaces
		// means 5 distinct customer-facing apps behind the same
		// account (the multi-app variant is the deferred footgun).
		TenantSurfacesPerAccount:  5,
		TenantHostnamesPerSurface: 50,
		TenantSurfacesAllowed:     true,
		// Data-placement hints (ADR-098 §D5): Pro unlocks the
		// capture path with a 10-hint cap per app.
		DataPlacementHintsPerApp: 10,
		// Outbound webhook subscription caps (issue #476 / ADR-076).
		// Pro gets 10/app, 30/account — mirrors the alert-rule ratio.
		WebhookPerApp:     10,
		WebhookPerAccount: 30,
		// Trigger primitive (issue #757 / ADR-0NN): Pro is the first
		// tier where the external-broker kinds unlock (Kafka, NATS,
		// Redis-streams) — the egress-allowlist tier (ADR-031) is
		// Pro+ and broker pulls require the allowlist. Batch caps
		// jump to 500 / 5 min / 10 attempts so a Pro customer's
		// 1k-msg/s Kafka consumer can be drained with one trigger.
		TriggersAllowed:               true,
		TriggerLimitPerApp:            10,
		TriggerLimitPerAccount:        50,
		TriggerBatchSizeMax:           500,
		TriggerBatchWindowMaxSec:      300,
		TriggerMaxAttemptsMax:         10,
		TriggerRecordsPerSecondPerApp: 1000,
		// Pro: 50 Mbit broker egress + ESM aliases + skip-verify OK.
		MaxESMSourcesPerApp:    10,
		MaxESMRecordsPerSecond: 1000,
		BrokerEgressMbit:       50,
		TLSSkipVerifyAllowed:   true,
		// Pro payload cap: 6 MiB — matches the previous
		// hardcoded closeBatch byte cap so Pro customers behave
		// identically pre/post migration 00274.
		TriggerPayloadMaxBytes: 6291456,
		// Per-account rate limit (ADR-040): Pro gets 1000/min — ~10× the
		// Pro per-app rps (100), same rationale as Hobby.
		RateLimitPerAccountRPM: 1000,
		// Wake-side admission throttle (ADR-099 PR-0). Pro is the
		// production tier — the per-app burst ceiling of 20/min is
		// calibrated against a customer running a cron fleet
		// (~1 cron tick per minute per app, plus a burst on
		// deploy-driven cold starts). The per-account ceiling of
		// 30/min bounds cross-app fan-out.
		WakeBurstPerApp:     20,
		WakeBurstPerAccount: 30,
		// Log deployment filter (issue #517 / PR-B): Pro gets 10
		// — covers the typical multi-staging fan-out (prod + 3-5
		// staging branches + a few ephemeral preview slots) without
		// letting one app monopolise the schedd's per-instance
		// goroutine fan-out.
		LogDeploymentFilterMax: 10,
		// CPU fairness (issue #301): Pro weight=8, quota 500ms/500ms.
		// Half-bandwidth of 2 cores — tracks the per-app concurrency
		// (5) and the per-app rps (100).
		CPUWeight:   8,
		CPUQuotaUS:  500_000,
		CPUPeriodUS: 500_000,
		// Streaming (issue #471 / ADR-047): Pro is paid-tier streaming
		// — same cap as Hobby. 100 MB / 900 s covers LLM chat
		// completions and JSON/CSV exports; SaaS-scale apps don't
		// need a higher cap because gatewayd-internal's per-instance egress
		// bandwidth ceiling (250 Mbit for Scale) is the binding
		// constraint long before 100 MB matters.
		StreamingEnabled:            true,
		MaxResponseBodyBytes:        100 * 1024 * 1024,
		ResponseWriteTimeoutSeconds: 900,
		// WebSocket / Upgrade bridge (issue #676 / ADR-080): Pro is
		// the first tier where production workloads sit — opt-in by
		// default for the same reason as Hobby (LLM / agent SDKs).
		WebSocketEnabled: true,
		// Per-route metrics (ADR-093): Pro is the first tier where
		// production workloads sit — opt-in by default for the same
		// reason as Hobby (production APIs want the per-route
		// breakdown by default).
		RouteMetricsEnabled: true,
		// Warm-snapshot (issue #470 / ADR-055): Pro is the first
		// tier where warm-snapshot is on by default. Per the issue
		// body's acceptance: "for a Pro+ app that has served ≥5
		// successful requests ≥2 s after first-ready, restore
		// from warm.snap should be ≤50 % of init.snap p50".
		WarmSnapshotEnabled:            true,
		WarmSnapshotMinRequestsDefault: 5,
		WarmSnapshotMinMsDefault:       2000,
		// Issue #560: Pro is the first tier where the
		// per-app require_authn opt-in unlocks. Pairs
		// with internal-only ingress (#5) per the issue's
		// recommendation. The column default is still
		// false — the customer must explicitly PATCH true.
		RequireAuthn: true,
		// AppProtocolGrpcAllowed (ADR-124): Pro unlocks gRPC
		// framing — paired with internal-only ingress + traffic
		// splitting + canary as the production-tier go-fast
		// stack. gRPC server-streaming is a paid-tier feature
		// consistently across Hobby/Pro/Scale.
		AppProtocolGrpcAllowed: true,
		// TrafficSplit (issue #556): Pro unlocks
		// per-deployment traffic splitting. The issue
		// title says "Pro+ canary"; the migration
		// (00160) and CreateDeployment handler stamp
		// traffic_percent=100 by default, so customers
		// who never opt-in see no behavioural change.
		TrafficSplit: true,
		// Mirror (issue #72 / ADR-125): Pro/Scale unlock the
		// per-deployment mirroring surface. MirrorTargetsPerApp
		// is 1 on Pro (single canary target — the canonical use
		// case) and 3 on Scale (multi-shard rollout). The
		// per-app cap is enforced inside
		// CreateMirrorRuleIfUnderQuota's FOR UPDATE lock; a
		// quota-exceeded attempt emits 403 mirror_rule_quota_exceeded
		// via QuotaErrorKindMirror.
		MirrorRuleAllowed:   true,
		MirrorTargetsPerApp: 1,
		// Tail primitive (issue #667 / ADR-078): Pro unlocks
		// the 30 s timeout + 64 per-instance concurrent tails.
		// Matches the issue's per-plan matrix value; covers
		// SaaS workloads where the webhook fan-out can take
		// 20–25 s under realistic network conditions.
		TailEnabled:                true,
		TailTimeoutS:               30,
		TailCapMax:                 TailCapMax,
		ConcurrentTailsPerInstance: 64,
		// Liveness (issue #554 / ADR-078): same defaults as Hobby —
		// the §13 baseline is plan-tier-independent (5 s / 3 /
		// 60 s / 3 / 300 s). The Pro tier is the unlock point for
		// the gRPC liveness flavor (Plan.GRPCLivenessAllowed,
		// v1 returns false because the runner shim only speaks
		// HTTP — see ADR-078 §Rejected alternatives).
		LivenessPeriodSeconds:       DefaultLivenessPeriodSeconds,
		LivenessConsecutiveFailures: DefaultLivenessConsecutiveFailures,
		LivenessCooldownSeconds:     DefaultLivenessCooldownSeconds,
		LivenessMaxRestarts:         DefaultLivenessMaxRestarts,
		LivenessWindowSeconds:       DefaultLivenessWindowSeconds,
		// Log archive (issue #562): Pro extends the retention
		// window to 30 days — covers a "this month" customer
		// post-mortem window. S3 storage cost scales linearly
		// with retention, so the per-plan matrix stays tight
		// rather than being a single shared cap.
		LogArchiveEnabled:          true,
		LogArchiveRetentionDaysMax: 30,
		// ADR-096 — Pro = "this month" retention, 1000 fingerprints,
		// 500 request rows per fingerprint.
		AppErrorsRetentionDays:                30,
		AppErrorsMaxFingerprintsPerApp:        1000,
		AppErrorsMaxRequestRowsPerFingerprint: 500,
		// ADR-127 production debugger. Pro = "this month"
		// retention, 10000 req/min, up to 50 distinct deployments
		// in the histogram, 200 spans per trace. The 7-day
		// retention matches the spec §4.7 "incident window"
		// for the Pro plan.
		DebugTelemetryEnabled:           true,
		DebugTelemetryRetentionDays:     7,
		DebugTelemetryRequestsPerMinute: 10000,
		DebugTelemetryDeploymentsPerApp: 50,
		DebugTelemetrySpansPerTrace:     200,
		// Per-app observability surface (Pro = on). Same posture
		// as Hobby; Pro gets larger retention ceilings via the
		// existing AppErrorsRetentionDays / usage_daily fields.
		PerAppMetricsAllowed:   true,
		AppUsageSummaryAllowed: true,
		AppErrorsAllowed:       true,
	},
	PlanScale: {
		Plan:                  PlanScale,
		DeployedApps:          100,
		MaxConcurrency:        20,
		RAMMB:                 1024,
		AppLayerMaxMB:         2048,
		SourceTarballMaxMB:    250,
		VCPU:                  4,
		IdleTimeoutS:          600,
		CertExpiryWarningDays: 30,
		IncludedGBHours:       1500,
		PriceMillicents:       9_900_000, // €99.00
		// ConcurrencyPerVMBound (issue #559): Scale = 80 — same
		// default as Cloud Run's `80 × vCPU` heuristic (the issue
		// body cites this number directly). 80 concurrent requests
		// per VM is comfortably reachable at Scale's 1024 MB RAM
		// for a typical Node.js / Go service; a sync-subprocess
		// Python customer would saturate before hitting this cap.
		ConcurrencyPerVMBound: 80,
		RateLimitRPS:          500,
		RateLimitBurst:        2000,
		EgressMbit:            250,
		SecretCountMax:        100,
		SecretValueMaxBytes:   32 * 1024,
		EnvVarsMax:            256,
		EnvValueMaxBytes:      32 * 1024,
		// Issue #461: Scale = 20 — broad fan-out for SaaS-scale apps.
		RegistryCredentialMax: 20,
		MinInstancesAllowed:   true,
		MaxInstancesAllowed:   true,
		// MaxMinInstances (ADR-071): Scale = 10 — half of
		// MaxConcurrency (20). At Scale's 1024 MB instance RAM
		// and 8 MB overhead, 10 instances resident = 10,320 MB
		// (~22% of the §6.2-2 47,600 MB ceiling), leaving
		// comfortable headroom for live wakes while still
		// delivering the "always-warm for traffic spikes" UX
		// the tier promises.
		MaxMinInstances: 10,
		// TrustedSignerCountMax: Scale is the regulated-workload
		// tier; 16 publishers covers "every platform team's CI
		// plus break-glass" without letting the table grow into
		// config-management territory. The byte-size cap on each
		// key (1024 bytes per migration 00083 CHECK) keeps the
		// table on-disk under ~16 KiB regardless.
		TrustedSignerCountMax: 16,
		// Soft ceiling: the binding constraint on Scale is the per-payload
		// byte cap (1 MiB), not the row count.
		MaxQueueDepth:               100,
		MaxDelayedTasksPerApp:       1_000_000,
		MaxSourceBytesPerInvocation: 1024 * 1024,
		AsyncInvokeAllowed:          true,
		// Scale: 25 attempts. The highest tier gets the most
		// tolerance so an upstream outage lasting a few minutes
		// doesn't dump the queue into dead_letter on a single bad
		// minute. Above 25 is irrational — that's 2 minutes at the
		// 5 s default backoff.
		MaxQueueAttempts: 25,
		// ADR-031: Scale gets 64 CIDR entries — broad enough for
		// SaaS-scale apps with many upstream integrations; doubling
		// the Pro budget tracks the doubling in DeployedApps (25 -> 100).
		EgressAllowlistAllowed: true,
		EgressAllowlistMaxSize: 64,
		// ADR-119: Scale unlocks the per-app static egress IP
		// surface — the B2B allowlist use case (Neon / Supabase /
		// partner APIs that whitelist source IPs). Per-app quota
		// of 1 in v1 — the column is a single inet, not a child
		// table. Bumping to N later is a per-plan int change with
		// no schema impact. IPv4-only in v1.
		StaticEgressIPAllowed: true,
		StaticEgressIPsPerApp: 1,

		// PublicAuthIPAllowlist: 4× Pro's budget tracks Scale's
		// 4× DeployedApps (25 → 100). SaaS-scale customers with
		// multi-region deployments routinely enumerate per-region
		// egress IPs in addition to their office VPN ranges.
		PublicAuthIPAllowlistAllowed:    true,
		PublicAuthIPAllowlistMaxEntries: 64,
		// Autoscale: Scale gets both targets; same rationale as Pro.
		ScaleUpTargetRPSAllowed: true,
		// Cron: Scale gets 100 per-app and 500 per-account. 5× Pro's
		// per-app ceiling (20→100) and 10× Pro's per-account ceiling
		// (50→500); the per-account figure absorbs 5× Scale-tier apps
		// at the per-app cap, the typical SaaS fan-out.
		CronLimitPerApp:     100,
		CronLimitPerAccount: 500,
		// Issue #475: Scale gets 4 reserved-tier apps. 2× Pro tracks
		// the doubling in DeployedApps (25 → 100) and the doubling in
		// MaxConcurrency (5 → 20). At Scale's 1024 MB instance RAM +
		// 8 MB overhead, 4 reserved apps at MaxConcurrency is ~8.3 GB
		// resident (~18% of the 47.6 GB ceiling) — leaves comfortable
		// headroom for live wakes.
		EvictionPriorityReservedAllowed: true,
		ReservedConcurrencyPerAccount:   4,
		// Issue #477 / ADR-079: Scale unlocks both bearer and basic —
		// the SaaS-scale customer shape has rotating-CI keys and
		// per-environment admin endpoints that benefit from both
		// auth modes.
		PublicAuthBearerAllowed:      true,
		PublicAuthBasicAllowed:       true,
		PublicAuthMembersOnlyAllowed: true,
		// Issue #695 / ADR-080: Scale mirrors Pro on the auth default
		// — the gate unlocks AND the bearer scope unlocks, so the
		// secure-by-default literal (true, "bearer") applies. A future
		// tier between Pro and Scale that unlocks mTLS would move
		// the literal here as part of the same PR.
		RequireAuthnDefault:   true,
		PublicAuthModeDefault: "bearer",
		// IAM-5 (issue #189): Scale gets 200 keys — 2 per app across
		// the Scale app budget (100) plus a per-team allowance, with
		// headroom for the rotating-CI shape of a SaaS-scale customer.
		KeysMax: 200,
		// IAM-6 / ADR-061 PR-2 (issue #190): Scale gets 200 members
		// / 100 pending invitations — tracks KeysMax (200) so a
		// SaaS-scale customer can run the typical multi-team +
		// rotating-CI shape. Financial model is authoritative —
		// derived value, reconciliation follow-up.
		OrgMembersMax:            200,
		OrgPendingInvitationsMax: 100,
		// Alert rules (issue #396): Scale gets 25 per-app and 100
		// per-account — 2.5× Pro's per-app (10→25) and ~3× the
		// per-account (30→100). Scale's app budget is 4× Pro's, so
		// the per-account figure absorbs the fan-out.
		AlertRuleLimitPerApp:              25,
		AlertRuleLimitPerAccount:          100,
		AlertPresetCatalogLimitPerAccount: 8,
		// Edge rules (ADR-089): Scale gets 500/app with JWT + IP.
		EdgeRulesPerApp:     500,
		EdgeRulesJWTAllowed: true,
		EdgeRulesIPAllowed:  true,
		EdgeRulesGeoPerApp:  100,
		// kind='throttle' per-route rate limit cap (ADR-091 D20.5
		// amendment, issue #881). Mirrors EdgeRulesGeoPerApp.
		EdgeRulesThrottlePerApp: 100,
		// kind='cache' per-app quota (ADR-122 §Decision). Scale=20
		// covers a full taxonomy of cached catalog endpoints under
		// one app (search/list/detail/filter/sort per locale,
		// category, etc.). Pin in limits_test.go so the per-plan
		// monotonic ladder Free < Hobby < Pro < Scale is enforced.
		EdgeRulesCachePerApp: 20,
		// CORS presets (issue #975 #4 / Mega-Foundation #979-b, slot
		// 00294). Scale is the large-fleet tier — 250 presets per
		// account, 50 per app, 500 origins per preset. Numbers
		// mirror the AlertRuleLimitPerApp progression
		// (1/5/25/100 * 5x) so the customer mental model is one
		// progression across primitives.
		CorsPresetsPerAccount:     250,
		CorsPresetsPerApp:         50,
		CorsPresetMaxOrigins:      500,
		CorsPresetMaxAllowMethods: 8,
		CorsPresetMaxNameLength:   64,
		// Consumer keys (ADR-120 / issue #975 item #5). Scale
		// gets the only per-app step-up — 1000 keys per app,
		// 25000 per account. The per-app step-up mirrors the
		// EdgeRulesPerApp 100-ceiling for Scale — both
		// primitives are about "per-customer-of-customer"
		// cardinality, so the upgrade ladder is one number.
		ConsumerKeysPerApp:     1000,
		ConsumerKeysPerAccount: 25000,
		// Open API docs (ADR-122 / issue #975 item #1). Scale keeps
		// the 1/deployment shape; 10000 per account (10× Pro, same
		// 10× ratio as Hobby→Pro). The byte cap stays at 128 KiB —
		// the global cap is the binding constraint, not the per-plan.
		OpenAPIDocsPerDeployment: 1,
		OpenAPIDocMaxBytes:       131072,
		OpenAPIDocsPerAccount:    10000,
		OpenAPIImportsPerAccount: 10000,
		// Per-consumer throttle key cap (ADR-104, issue #881 Phase 3).
		ThrottleMaxKeysPerRule: 10000,
		// Tenant surfaces (ADR-099 / issue #879): Scale gets 25
		// surfaces with up to 250 verified hostnames each — the
		// established-SaaS tier. The 250 cap is bounded by LE's
		// 100-SAN-per-cert limit (`per_host_san` falls back to
		// `per_host` above ~100 — surfaced via the cert engine, not
		// quota).
		TenantSurfacesPerAccount:  25,
		TenantHostnamesPerSurface: 250,
		TenantSurfacesAllowed:     true,
		// Data-placement hints (ADR-098 §D5): Scale unlocks the
		// capture path with a 50-hint cap per app — large enough
		// for a multi-DB SaaS (primary + replicas + read-only +
		// analytics + cache + queue).
		DataPlacementHintsPerApp: 50,
		// Outbound webhook subscription caps (issue #476 / ADR-076).
		// Scale gets 25/app, 100/account — mirrors the alert-rule ratio.
		WebhookPerApp:     25,
		WebhookPerAccount: 100,
		// Trigger primitive (issue #757 / ADR-0NN): Scale is the upper
		// tier — caps align with the SQL CHECK ceilings (5000 records
		// / 5 min window / 25 attempts) so a Scale customer's
		// SQS-compatible or Kafka consumer can be drained at full
		// throughput. Records/sec per app tracks the 10× rule that
		// the per-app rps tier already follows (500 → 10 000).
		TriggersAllowed:               true,
		TriggerLimitPerApp:            50,
		TriggerLimitPerAccount:        200,
		TriggerBatchSizeMax:           5000,
		TriggerBatchWindowMaxSec:      300,
		TriggerMaxAttemptsMax:         25,
		TriggerRecordsPerSecondPerApp: 10000,
		// Scale: 200 Mbit broker egress (the cap is host-NIC-shaped;
		// Scale customers run the largest fan-outs). ESM aliases
		// mirror the trigger.* caps; skip-verify OK (paid tier).
		MaxESMSourcesPerApp:    50,
		MaxESMRecordsPerSecond: 10000,
		BrokerEgressMbit:       200,
		TLSSkipVerifyAllowed:   true,
		// Scale payload cap: 16 MiB — covers the largest
		// realistic per-record broker payloads (SQS max 256 KiB,
		// Kafka default 1 MiB, NATS typically < 8 MiB). Below
		// the migration-00274 SQL ceiling of 64 MiB so the
		// column CHECK remains a safety net, not a binding
		// constraint.
		TriggerPayloadMaxBytes: 16777216,
		// Per-account rate limit (ADR-040): Scale gets 5000/min — ~10× the
		// Scale per-app rps (500). The fleet-summed alert at 100/min/5m
		// (FaasPerAccountRateLimitSpike) triggers well before any single
		// paid customer's bucket fills, which is the intended signal:
		// coordinated abuse, not baseline load.
		RateLimitPerAccountRPM: 5000,
		// Wake-side admission throttle (ADR-099 PR-0). Scale is
		// the upper tier — 100 wakes/min per app is enough to drain
		// a 1000-task parallel job run in 10 min wall-clock, which
		// matches ADR-099 §Acceptance. The per-account cap of
		// 150/min allows a customer to fan out across several apps
		// without exhausting the throttle.
		WakeBurstPerApp:     100,
		WakeBurstPerAccount: 150,
		// Log deployment filter (issue #517 / PR-B): Scale gets 50
		// — 5× Pro (10→50), tracks Scale's larger app budget
		// (100 apps vs Pro's 25) and the multi-region staging fan-out
		// SaaS-scale customers typically run.
		LogDeploymentFilterMax:  50,
		ScaleUpTargetCPUAllowed: true,
		// CPU fairness (issue #301): Scale weight=16, quota 1000ms/1000ms
		// — i.e. the full bandwidth of one core. Scale runs 20 concurrent
		// VMs by plan, so the slice's aggregate quota is 20× at burst.
		// The kernel cpu.weight ratio with the lower tiers keeps single
		// VM bursts from monopolising the parent slice.
		CPUWeight:   16,
		CPUQuotaUS:  1_000_000,
		CPUPeriodUS: 1_000_000,
		// Streaming (issue #471 / ADR-047): Scale is paid-tier
		// streaming — same cap as Hobby/Pro. 100 MB / 900 s is
		// already the LLM-token-stream ceiling; Scale customers who
		// need >100 MB are rare (large JSON exports are dwarfed by
		// the per-instance egress bandwidth cap of 250 Mbit/s). A
		// future PR can lift this if telemetry shows Scale customers
		// tripping the cap.
		StreamingEnabled:            true,
		MaxResponseBodyBytes:        100 * 1024 * 1024,
		ResponseWriteTimeoutSeconds: 900,
		// WebSocket / Upgrade bridge (issue #676 / ADR-080): Scale
		// stays on by default — production workloads at this tier
		// are expected to run agent / WS-backed services.
		WebSocketEnabled: true,
		// Per-route metrics (ADR-093): Scale stays on by default
		// for the same reason as Pro — production workloads want
		// the per-route breakdown without a PATCH round-trip.
		RouteMetricsEnabled: true,
		// Warm-snapshot (issue #470 / ADR-055): Scale stays on
		// by default — the per-app parked footprint cost fits
		// inside the 452 GB budget, and the customer's wake-p50
		// win is the largest dollar lever for SaaS workloads.
		WarmSnapshotEnabled:            true,
		WarmSnapshotMinRequestsDefault: 5,
		WarmSnapshotMinMsDefault:       2000,
		// Issue #560: Scale mirrors Pro — the opt-in is
		// available, but the column default stays false.
		// Customers on the largest plan who want
		// token-gating still set it per-deployment.
		RequireAuthn: true,
		// AppProtocolGrpcAllowed (ADR-124): Scale unlocks gRPC
		// framing — mirroring Pro as the production-tier
		// go-fast stack.
		AppProtocolGrpcAllowed: true,
		// TrafficSplit (issue #556): Scale unlocks
		// per-deployment traffic splitting — the
		// revenue-protecting feature for the Scale
		// tier (5/25/100% staged rollout to defend
		// against bad deploys on a checkout API).
		TrafficSplit: true,
		// Mirror (issue #72 / ADR-125): Pro/Scale unlock the
		// per-deployment mirroring surface. MirrorTargetsPerApp
		// is 1 on Pro (single canary target — the canonical use
		// case) and 3 on Scale (multi-shard rollout). The
		// per-app cap is enforced inside
		// CreateMirrorRuleIfUnderQuota's FOR UPDATE lock; a
		// quota-exceeded attempt emits 403 mirror_rule_quota_exceeded
		// via QuotaErrorKindMirror.
		MirrorRuleAllowed:   true,
		MirrorTargetsPerApp: 3,
		// Tail primitive (issue #667 / ADR-078): Scale unlocks
		// the 60 s timeout + 256 per-instance concurrent tails —
		// the ceiling per the issue's per-plan matrix. The 60 s
		// timeout is the longest the issue pins; longer timeouts
		// would let a runaway tail hold a wake past the G7 idle
		// window and is rejected by the runtime AdvisoryFloor
		// check.
		TailEnabled:                true,
		TailTimeoutS:               60,
		TailCapMax:                 TailCapMax,
		ConcurrentTailsPerInstance: 256,
		// Liveness (issue #554 / ADR-078): Scale inherits the
		// same defaults as Pro (5s / 3 consecutive / 60s cooldown
		// / 3 in 300s). The per-deployment sliding window is the
		// source of truth for the park-on-exhaustion path; the
		// in-memory tracker (pkg/sched/liveness_window.go) caps
		// operational cost and the per-deployment override column
		// (deployments.override_liveness_probe) lets a High-traffic
		// Scale customer lengthen the window without a code change.
		// Same v1 = HTTP-only caveat as Pro: gRPC health is deferred.
		LivenessPeriodSeconds:       DefaultLivenessPeriodSeconds,
		LivenessConsecutiveFailures: DefaultLivenessConsecutiveFailures,
		LivenessCooldownSeconds:     DefaultLivenessCooldownSeconds,
		LivenessMaxRestarts:         DefaultLivenessMaxRestarts,
		LivenessWindowSeconds:       DefaultLivenessWindowSeconds,
		// Log archive (issue #562): Scale gets 90-day retention
		// — covers a "this quarter" compliance window that
		// SaaS-scale customers typically need. Storage cost is
		// the customer's (separate billing path outside of the
		// free GB-h allowance), so the per-plan matrix stays
		// generous at the top tier.
		LogArchiveEnabled:          true,
		LogArchiveRetentionDaysMax: 90,
		// ADR-096 — Scale = "this quarter" retention, 5000
		// fingerprints, 1000 request rows per fingerprint.
		AppErrorsRetentionDays:                90,
		AppErrorsMaxFingerprintsPerApp:        5000,
		AppErrorsMaxRequestRowsPerFingerprint: 1000,
		// ADR-127 production debugger. Scale = "this quarter"
		// retention, 50000 req/min, up to 200 distinct deployments
		// in the histogram, 1000 spans per trace. The 14-day cap
		// matches the scale tier's incident-window expectations;
		// the deployment-label cap of 200 is what makes the
		// deploymentLabelSet discipline load-bearing (without the
		// cap a fleet with thousands of historical deployments
		// would blow up Prometheus cardinality).
		DebugTelemetryEnabled:           true,
		DebugTelemetryRetentionDays:     14,
		DebugTelemetryRequestsPerMinute: 50000,
		DebugTelemetryDeploymentsPerApp: 200,
		DebugTelemetrySpansPerTrace:     1000,
		// Per-app observability surface (Scale = on). Largest
		// retention ceiling via AppErrorsRetentionDays (90d).
		PerAppMetricsAllowed:   true,
		AppUsageSummaryAllowed: true,
		AppErrorsAllowed:       true,
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
	// SnapshotVMOverheadMB is temporary host-side headroom applied only while
	// Firecracker creates a full snapshot. The snapshot operation allocates
	// bookkeeping and copy-on-write pages in the host process in addition to
	// the guest RAM and ordinary VMM overhead. It is not part of admission or
	// billing; the tenant-slice ceiling remains the aggregate safety fence.
	SnapshotVMOverheadMB = 256

	// FloorDecisionIntervalSeconds (issue #557 / ADR-071 §Decision 1)
	// is the cadence at which the proactive floor trigger in
	// pkg/sched/floor wakes instances up to the per-app floor. 1 s
	// is the customer-facing promise: a Hobby customer who PATCHes
	// min_instances=1 must see one RUNNING instance within one
	// second. Tunable via FAAS_FLOOR_INTERVAL_SECONDS at schedd.
	FloorDecisionIntervalSeconds = 1

	// MaxFloorBackoffSeconds (ADR-071 §Decision 4) caps the per-app
	// exponential backoff the floor trigger applies on a non-nil
	// AdmitInstance error. 60 s bounds the FAILED-row hazard on a
	// RAM-saturated box: a stuck ceiling produces at most ~6 FAILED
	// rows per app per hour, not 3,600 (one per second).
	MaxFloorBackoffSeconds = 60

	// CPU (spec §1).
	CPUOvercommit = 8
	VCPUSlots     = 160

	// Metering (spec §1, §10).
	OverageMillicentsPerGBHour = 1_000 // €0.01 per GB-RAM-hour

	// Builder VM (spec §4.5, §1). Builds live in the control-plane slice, never
	// tenant RAM.
	BuildVMRAMMB = 2_048
	// BuildVMOverheadMB is host-side Firecracker/VMM RSS plus page-cache and
	// kernel-accounting headroom above the guest RAM allocation. The ordinary
	// 8 MiB per-VM billing overhead is intentionally not reused here: a builder
	// fills a 2 GiB guest and BuildKit snapshots keep several hundred MiB
	// charged to the Firecracker cgroup while a layer is being committed.
	BuildVMOverheadMB      = 768
	BuildVMVCPU            = 2
	BuildTimeoutSeconds    = 900 // 15 min build; cold rootless Railpack export needs headroom
	BuildE2ETimeoutSeconds = 900 // 15 min end-to-end

	// Snapshots / disk (spec §1, §8).
	FleetSnapshotAvgTargetMB = 130 // business metric; alert >160 warn, >200 page
	SnapshotBudgetGB         = 452
	// SnapshotBudgetAlarmPct is the lv-fc percentage at which the nightly
	// imaged GC switches from per-app retention (keep current+previous
	// deployments per app) to fleet budget pressure (evict from the
	// biggest-over-quota accounts first). Matches spec §12. NaN lv-fc
	// readings (lvs missing on dev/macOS) short-circuit the pressure branch.
	SnapshotBudgetAlarmPct = 90.0
	// SnapshotStaleRetention is how long a snapshot lives in stale state
	// after the F2 FC-version sweep marks it before imaged evicts it
	// (F-07). Spec §4.4 + ADR-005: stale snapshots must remain
	// restore-able for a brief window so an operator rollback across a
	// firecracker upgrade doesn't pay an extra cold boot. 7 days is the
	// v1 box's typical reset cycle.
	SnapshotStaleRetention = 7 * 24 * time.Hour
	// LvFcName is the LVM logical volume apps + snapshots live on (spec §8).
	// Schedd's dashboard gauge shells out to `lvs -o data_percent <LvFcName>`
	// to populate `fcvm_lv_fc_used_pct`. Empty on dev/macOS — the
	// DefaultLvFcUsedPct closure returns 0 and the gauge degrades to "no data".
	LvFcName = "lv-fc"

	// Characterization boot (ADR-051 §"Characterization window"). On the
	// first cold boot of a new deployment, guest-init observes what the
	// app binds, runs L7 probes, and ships a report over AF_VSOCK
	// STREAM (port 1026 / msgtype 3). Both bounds live here so a single
	// edit moves the whole observation window; the guest and the host
	// mirror against this single source.
	//
	// CharacterizationDeadline bounds the GUEST's observation window
	// (guest/init/characterize_linux.go::waitForBind). 10 s covers the
	// L7 probe budget (2 s) + shipReport's 4-attempt retry budget
	// (~1.85 s with backoff) + headroom for a slow customer app boot.
	// Never below the host's wait (CharacterizationHostDeadline) — the
	// guest gives up earlier than the host and both sides fall back to
	// the scan-hint class without failing the deploy (per
	// ADR-051 §"Failure messages become specific").
	CharacterizationDeadline = 10 * time.Second
	// CharacterizationHostDeadline bounds the HOST's
	// WaitCharacterizationReport dial+read inside Wake
	// (pkg/fcvm/manager.go::characterizationWait). 4 s gives margin
	// for the guest's 4 shipReport attempts + slow vsock proxies on
	// nested KVM (Lima caveat, spec §14).
	CharacterizationHostDeadline = 4 * time.Second

	// LogRingBufferBytes is the capacity of the Supervisor's
	// stdout/stderr ring buffer (ADR-051 Phase 4 Slice A PR-B).
	// 64 KiB covers the boot-time tail of any realistic customer
	// app (a Node cold start, a Python import chain) without
	// forcing a multi-page journal capture on every cold boot.
	// Characterized at plan time: a typical FastAPI app's
	// first-second log volume is ~4-8 KiB, so 64 KiB preserves the
	// entire boot window with margin. The wire-side truncation
	// (VsockCharacterizationMaxBody, 32 KiB) still clamps the
	// reported LogTail; this buffer is the over-budget source the
	// report reads from. A future bump to VsockCharacterizationMaxBody
	// must be matched here.
	LogRingBufferBytes = 64 * 1024

	// Build artifact export (M6): vmmd loopback-mounts the chroot-local drive1
	// on Destroy to copy out /build/out/image.tar (and friends). 4 GiB is
	// well above the §14 target (~130 MB) so it's not the limiting factor; it's
	// the ceiling we refuse to copy past. See pkg/fcvm/vmm.go::exportBuildArtifacts.
	MaxExportedLayerBytes int64 = 4 << 30

	// Edge request caps (spec §4.1).
	MaxRequestBodyBytes = 25 * 1024 * 1024 // 25 MB either direction
	WakeQueueCap        = 512              // per-app wake queue
	WakeQueueTTLSeconds = 30

	// MirrorMaxLifetimeSeconds (issue #72 / ADR-125) is the hard
	// upper bound on how long a single mirror goroutine can run.
	// The gateway derives a per-request context via
	// context.WithoutCancel(r.Context()) + WithTimeout(mirrorMaxLifetime)
	// so the mirror outlives a customer disconnect, but cannot run
	// forever. 5s is the empirical envelope: cold-boot
	// (~250ms) + serve (~50ms) + buffer drain (~100ms) + JCS
	// canonicalization (~50ms) + safety margin. A sustained
	// production request (p99 ~200ms) sits comfortably under this;
	// a wedged mirror VM is bounded at 5s before the goroutine
	// returns and the deferred ParkInstance runs. Larger values
	// waste wake-bill on a hung VM; smaller values truncate a
	// slow-but-correct response. Bumping this is a
	// spec-amendment-grade change (ADR-125 §Decision).
	MirrorMaxLifetimeSeconds = 5

	// Apid http.Server defaults (issue #995 Phase 1, ADR-121
	// companion). The customer-facing control plane binds loopback
	// (gatewayd-public reverse-proxies in front) so the same
	// shape gatewayd-internal uses (ResponseWriteTimeoutDefault =
	// 300s) carries over. ReadHeaderTimeout lives separately
	// (already 10s in cmd/apid/main.go) — slowloris defence is
	// split between ReadHeaderTimeout (header arrival) and
	// ReadTimeout (body arrival). IdleTimeout bounds the
	// keep-alive pool so a half-open client can't park a goroutine.
	// Values are int seconds to match ResponseWriteTimeoutDefault's
	// existing precedent at line 2201 — no `time` import added.
	APIDReadTimeoutSecondsDefault  = 60  // slowloris defence (body arrival)
	APIDWriteTimeoutSecondsDefault = 300 // matches gatewayd-internal
	APIDIdleTimeoutSecondsDefault  = 120 // keep-alive cap

	// Metrics-listener defaults (ADR-122 / post-issue-#995 follow-up).
	// PR #996 hardened apid's customer-facing listener (60/300/120
	// above) and apid's own /metrics listener (cmd/apid/main.go:1478-
	// 1481 — 10/10/60). The remaining six daemons (meterd, schedd,
	// vmmd, builderd, imaged, githubd) only set ReadHeaderTimeout at
	// the stdlib http.Server layer. These constants apply the apid
	// /metrics defaults to those daemons; per-daemon override is a
	// TOML field in each daemon's config.go.
	//
	// ReadTimeout=10s is the loopback-scrape cap — Prometheus scrapes
	// finish in milliseconds; 10s is a runaway-safety net, not a
	// normal-path knob. WriteTimeout mirrors. IdleTimeout=60s matches
	// apid's /metrics keep-alive cap. MaxHeaderBytes reused via
	// api.DefaultMaxHeaderBytes (1 MiB) above — the per-daemon TOML
	// field `metrics_max_header_bytes` falls back to that constant.
	//
	// ADR-122 records these as the canonical metrics-listener shape
	// for any new daemon added to the platform. Future listeners
	// should NOT reinvent — pick up the constants.
	MetricsReadTimeoutSecondsDefault  = 10 // loopback scrape cap
	MetricsWriteTimeoutSecondsDefault = 10 // mirror
	MetricsIdleTimeoutSecondsDefault  = 60 // matches apid /metrics keep-alive

	// Webhook-listener defaults (githubd only, ADR-122). Same
	// shape as Metrics but with body-cap-shaped timeouts — the
	// webhook handler accepts bodies up to 10 MiB (readBody cap at
	// pkg/githubd/server.go:323), so ReadTimeout=30s is the budget
	// for a slow webhook client to upload 10 MiB at the existing
	// body cap. WriteTimeout mirrors. IdleTimeout matches metrics.
	//
	// The existing readBody 10 MiB body cap is the body-size
	// contract; the new server-level MaxHeaderBytes is the
	// header cap (defence-in-depth against header smuggling).
	WebhookReadTimeoutSecondsDefault  = 30 // 10 MiB upload budget at the readBody cap
	WebhookWriteTimeoutSecondsDefault = 30 // mirror
	WebhookIdleTimeoutSecondsDefault  = 60 // matches metrics

	// Gatewayd-internal defaults (issue #995 Phase 3, ADR-121
	// companion). The public listener carries 60 s ReadTimeout
	// (matches the legacy default set at run.go:1989-1991), with
	// tighter caps on the control / unix-socket listener where
	// requests are smaller and shorter-lived.
	GatewaydInternalReadTimeoutSecondsDefault         = 60 // public listener slowloris defence
	GatewaydInternalControlReadTimeoutSecondsDefault  = 30 // control + unix-socket
	GatewaydInternalControlWriteTimeoutSecondsDefault = 30 // control + unix-socket
	GatewaydInternalControlIdleTimeoutSecondsDefault  = 60 // control + unix-socket keep-alive

	// MaxEdgeRuleLimitBodyBytesStreaming (ADR-091 D24 / kind=limit
	// streaming carve-out) is the upper bound on the optional
	// `max_body_bytes_streaming` field of a kind=limit edge rule.
	// The buffered-path field (`max_body_bytes`) is capped by
	// MaxRequestBodyBytes (25 MiB) above; the streaming opt-in
	// raises the cap to RawStreamMaxRequestBytes (100 MiB, ADR-080
	// raw-bridge parity) so an LLM-style streaming POST against a
	// /v1/chat/completions endpoint has the same headroom the
	// raw-bridge ForwardStream has. Runtime enforcement ships
	// alongside the field (D24 §6 amendment). The cap-selection
	// algorithm at pkg/gateway/handler.go::applyEdgeRuleLimit
	// consults this field only when the request is on the
	// streaming opt-in path (4-conjunct detection: h.streamingEnabled
	// && app.StreamingEnabled && !isAcceptJSON(Accept) && !isUpgradeRequest).
	// The DTO's `s ≥ b` invariant (pkg/api/dto.go) is the single
	// source of truth — the runtime trusts it without re-check.
	// Matches RawStreamMaxRequestBytes byte-for-byte so a
	// customer can set `max_body_bytes_streaming` = RawStreamMaxRequestBytes
	// and the value survives the apid-Validate round-trip without
	// surprise trimming.
	MaxEdgeRuleLimitBodyBytesStreaming int64 = 100 * 1024 * 1024

	// MaxEdgeRuleValidateSchemaBytes bounds the JSON Schema body of a
	// kind=validate edge rule at apid-create time (Cloudflare-style
	// 64 KiB). Mirrors the §11 JWKS-URL defence-in-depth posture on
	// pkg/api/dto.go::EdgeRuleValidateAction.Validate: a customer
	// cannot ship a schema so large that compiling + caching pushes
	// per-host memory through the roof, and the gateway never has
	// to defend against a multi-MiB document. Independent of
	// MaxRequestBodyBytes (which bounds inbound request bodies, not
	// schema documents).
	MaxEdgeRuleValidateSchemaBytes = 64 * 1024 // 64 KiB

	// EdgeRuleMaintenanceRetryAfterSeconds (ADR-091 amendment,
	// PR-A #??? / kind=maintenance) is the platform default
	// Retry-After for both the kind=maintenance edge rule and the
	// apps.maintenance_mode coarse gate. Override via
	// FAAS_EDGE_RULE_MAINTENANCE_RETRY_AFTER_SECONDS env var (parsed
	// at boot — see pkg/api/env.go for the env-loading helpers;
	// the constant here is the default when the env var is unset).
	// Applied in pkg/gateway.(*Handler).applyEdgeRuleMaintenance and
	// pkg/gateway.(*Handler).applyAppsMaintenanceMode via
	// api.WriteProblem + WithHeader, mirroring the existing
	// StandbyWriteRetryAfterSeconds pattern at line 2171.
	EdgeRuleMaintenanceRetryAfterSeconds = 60

	// MaxEdgeRuleMaintenanceRetryAfterSeconds caps the per-rule
	// RetryAfterSeconds field on a kind=maintenance edge rule at
	// 24 h. EdgeRuleMaintenanceAction.Validate rejects larger
	// values with 422 so a customer cannot ship a rule that asks
	// a client to back off for a week. Independent of
	// EdgeRuleMaintenanceRetryAfterSeconds (which is the default,
	// not the cap).
	MaxEdgeRuleMaintenanceRetryAfterSeconds = 24 * 60 * 60 // 86400 (24h)

	// API-key lifetime (issue #189 / IAM-5). New non-admin keys
	// minted by createKey get `expires_at = now + DefaultAPIKeyLifetimeDays`.
	// 365 days is the issue-189 spec: long enough to be
	// "set-and-forget" for a customer's CI rotation, short enough
	// that an exfiltrated key expires within a year of theft even
	// without rotation. Admin keys default to nil expiry (per
	// existing admin semantics — never expire, must be explicitly
	// revoked). Legacy admin keys (pre-IAM-5) keep null expiry
	// forever; rotation is the migration path for customers who
	// want a finite window on their admin keys.
	DefaultAPIKeyLifetimeDays = 365

	// DefaultAPIKeyGraceWindowDays (issue #189 / IAM-5) is the
	// plan-level default for the rotation grace window. 7 days
	// gives the customer's CI / staging / prod fleet one
	// rotation cycle to switch over without coordinated downtime.
	// The per-account override (accounts.key_grace_window_days)
	// takes precedence; 0 in the per-account column means
	// "atomic revocation" (no grace).
	DefaultAPIKeyGraceWindowDays = 7

	// Sidecar containers (issue #463 / ADR-070). The 2-sidecar
	// hard cap is a GLOBAL constant, not a per-plan matrix field.
	// Every plan inherits the same `SidecarCapMax = 2` (Free
	// included). The cap is structurally tight: 1 init + 1
	// sidecar is the smallest useful surface for a stateless
	// workload, and the schema CHECK on `deployments.sidecars`
	// (migration 00118) pins the cap at the second-line defence
	// layer (migrations/00118_deployments_sidecars.sql). A future
	// PR can grow this to a per-plan matrix if telemetry shows
	// demand — the constant is the single source of truth.
	SidecarCapMax = 2

	// Edge-rule JWT verify deadline (ADR-091 hardening PR-A). Caps
	// the wall-clock spent inside pkg/gateway.(*Handler).applyEdgeRuleJWT
	// on a single request — signature verify + claim parse + any
	// JWKS refresh that the verifier triggers mid-call. Sizing
	// matches pkg/edgejwks.DefaultFetchTimeout (5 s for the network
	// leg) so a hung IdP surfaces as a 401 inside the same window
	// the rest of the gateway considers a stalled upstream. The
	// deadline fires on top of r.Context(); if r.Context() already
	// carries a tighter cap (e.g. an upstream HTTP/1.1 server
	// ReadTimeout), the tighter deadline wins.
	EdgeRuleJWTVerifyTimeoutDefault = 5 * time.Second

	// Streaming response caps (issue #471 / ADR-047). Free stays on the
	// 25 MB / 300 s envelope (spec §4.1 baseline) so the abuse-floor
	// tier can't pin a long stream against the box. Hobby/Pro/Scale
	// raise the cap to 100 MB / 900 s so LLM token streams (typical
	// 30–120 s chat completions) and large JSON/CSV exports have
	// headroom. Limits.MaxResponseBodyBytes /
	// Limits.ResponseWriteTimeoutSeconds override these defaults per
	// plan; 0 in those fields falls back to the *Default constants
	// below so a missing plan row fails closed to the spec baseline
	// rather than inheriting a paid tier's relaxed cap.
	//
	// Enforced at two sites (issue #995 / ADR-121):
	//   - pkg/gateway/handler.go::setupStreamingWriter (streaming path)
	//     — 413 streaming_not_available on over-cap.
	//   - pkg/gateway/handler.go::setupBufferedCapWriter (buffered path)
	//     — 413 response_too_large on over-cap (or hardened connection
	//     reset if the upstream's headers already reached the wire).
	// Inbound body caps are enforced by http.MaxBytesReader at the
	// ServeHTTP entry (separate from the response cap).
	MaxResponseBodyBytesDefault   int64 = 25 * 1024 * 1024 // 25 MB (spec §4.1)
	ResponseWriteTimeoutDefault         = 300              // 300 s (spec §4.1)
	StreamingFlushBytesDefault          = 256 * 1024       // 256 KiB flush window (ADR-047)
	StreamingFlushIntervalDefault       = 200 * time.Millisecond

	// StreamingStatus is the per-request classification emitted via
	// the Streaming-Status response header (ADR-102 D1/D2). The
	// canonical wire values are the lower-case string forms of the
	// constants below; the Go type is a string alias so the JSON
	// encoder marshals the wire value verbatim and HTTP header
	// Set/Get round-trips losslessly. Six states, six wire values,
	// no aliases.
	//
	// StreamingStatusStreaming is the happy path — the platform's
	// four-conjunct streaming gate (operator opt-in + per-app flag
	// + non-JSON Accept + non-Upgrade request) all hold and the
	// capWriter / per-flush metering wrap is installed.
	//
	// StreamingStatusAcceptJSONDowngrade is the post-D3 advisory
	// variant: the request set Accept: application/json which
	// would have downgraded the gate pre-ADR-102 but no longer
	// does. Status is informational for one release cycle so
	// pinned-SDK customers (whose Accept defaults to JSON) can
	// self-diagnose via the header. The variant is deleted in
	// ADR-102-followup ~30 days post-merge.
	//
	// StreamingStatusFlagDisabled means apps.streaming_enabled=false.
	// StreamingStatusOperatorDisabled means FAAS_GATEWAY_STREAMING
	// env was not set on the gatewayd-internal process.
	// StreamingStatusPlanDisallows means the plan is Free (legacy
	// pre-D5 rows only — D5 closes CreateApp at apid).
	// StreamingStatusUpgradeBypass means Connection: Upgrade is
	// set — the raw-bytes bridge handles this path, not the
	// streaming path.
	//
	// All non-streaming variants carry the plan-level buffered cap
	// (MaxResponseBodyBytes); only the streaming variant can carry
	// an endpoint-rule cap (max_body_bytes_streaming from a
	// matched edge rule).
	StreamingStatusStreaming           StreamingStatus = "streaming"
	StreamingStatusAcceptJSONDowngrade StreamingStatus = "accept-json-downgrade"
	StreamingStatusFlagDisabled        StreamingStatus = "flag-disabled"
	StreamingStatusOperatorDisabled    StreamingStatus = "operator-disabled"
	StreamingStatusPlanDisallows       StreamingStatus = "plan-disallows"
	StreamingStatusUpgradeBypass       StreamingStatus = "upgrade-bypass"

	// StreamingStatusHeader is the canonical response header name
	// carrying the StreamingStatus enum (ADR-102 D2). Title-Case
	// per the IETF visible-header convention (Retry-After,
	// X-RateLimit-*); not the x-faas-* internal prefix because
	// this is customer-visible (and surfaceable to browser JS
	// via CORS Expose-Headers per ADR-102 D8).
	StreamingStatusHeader = "Streaming-Status"

	// StreamingStatusAcceptHintHeader is the one-cycle advisory
	// header (ADR-102 D3) stamped on the FIRST response after
	// upgrade when the request would have downgraded pre-D3
	// (i.e. Accept: application/json was set). Pinned-SDK
	// customers whose SDK defaults Accept to JSON see this
	// header and self-diagnose. Deleted in ADR-102-followup
	// when the accept-json-downgrade enum variant is retired.
	StreamingStatusAcceptHintHeader = "Streaming-Status-Accept-Hint"

	// StreamingStatusAcceptHintValue is the wire value of the
	// advisory header. Constant so a customer grepping for
	// "would-buffer-pre-D3" finds exactly the call site.
	StreamingStatusAcceptHintValue = "would-buffer-pre-D3"

	// Raw-bridge (issue #676 / ADR-080) inbound cap. The raw-bytes
	// bridge carries Upgrade / WebSocket / long-poll traffic from
	// gatewayd-internal into the guest's netns TCP socket. The cap
	// is per-request (the inbound body of one Upgrade handshake),
	// not per-session — Upgrade sessions are long-lived and
	// bytes-in is metered separately at the Prometheus layer. The
	// init frame's max_request_bytes is clamped DOWN to this value
	// on the vmmd side (callers cannot grow the cap; a Free-plan
	// gatewayd-internal cannot ask for math.MaxInt64 and disable the cap).
	// Mirrors ForwardStreamMaxBodyBytes in pkg/vmmdgrpc (100 MiB on
	// the same Hobby+ plans) so an LLM-style upgrade stream has
	// the same headroom.
	RawStreamMaxRequestBytes int64 = 100 * 1024 * 1024

	// RawStreamMaxResponseBytes (issue #676 / ADR-080 follow-up,
	// PR-C) bounds the per-session egress bytes on the raw-bytes
	// Upgrade bridge. Mirrors RawStreamMaxRequestBytes in shape
	// but is sized for a long-lived WS session — a 100 MiB cap on
	// a 24-h session would be pathologically tight for any chat /
	// agent workload. 1 GiB lets a 100 KB/s stream run for ~3 h
	// cleanly; above that, rawBridgePumpBody surfaces
	// ResourceExhausted so the gateway-side forwarder emits 502
	// to the customer (mirrors the inbound cap's behaviour at
	// rawBridgeBodyLoop). This is memory-safety, NOT billing —
	// the (plan_ram + 8) per-running-second cost already pays for
	// WS residency. The cap prevents a runaway guest from
	// ballooning the gateway's bidi goroutine pair past the
	// gateway process's RSS budget.
	RawStreamMaxResponseBytes int64 = 1 * 1024 * 1024 * 1024

	// Post-response tail (issue #667 / ADR-078).
	//
	// TailCapMax is a structural constant applied uniformly across
	// plans — the issue pins TailCapMax = 16 as a single source of
	// truth, not a per-plan matrix. The runner enforces it before any
	// BumpInstanceTailCount call so a customer cannot exceed the
	// structural cap even if a plan row's TailCapMax field is unset
	// or zero. The accessor Plan.TailCapMax() returns this constant
	// regardless of the field's value, matching the issue's
	// "structural constant in pkg/api/limits.go" framing.
	TailCapMax = 16

	// TailTimeoutFloorSeconds is the minimum wall-clock ceiling for
	// any plan that enables the waitUntil primitive. The per-plan
	// matrix values (Free 5 s / Hobby 15 s / Pro 30 s / Scale 60 s)
	// are all >= this floor; a buggy planLimits entry that drops
	// below the floor is clamped up by Plan.TailTimeoutSeconds()
	// so the reaper's 5 s park-watchdog always has at least a chance
	// to drain the tail before force-park (the watchdog is
	// ParkTailDrainTimeoutSeconds below).
	TailTimeoutFloorSeconds = 5

	// ParkTailDrainTimeoutSeconds is the watchdog ceiling for
	// snapshotAndPark when an instance's tail_count > 0 at park time
	// (ADR-078 §"Park gate"). The engine waits up to this many seconds
	// for the runner to drain its in-process tail host before
	// force-parking and emitting wake.tail_failed{reason=forced_at_park}
	// for any unfinished tails. Set to TailTimeoutFloorSeconds so the
	// watchdog can never be shorter than the shortest per-plan tail
	// timeout — otherwise the watchdog would fire mid-task and the
	// graceful-drain contract would be a lie.
	ParkTailDrainTimeoutSeconds = 5

	// OCI puller (spec §17 G1, ADR-021). Per-pull HTTP timeout for the
	// registry client. cmd/imaged passes this to oci.WithTimeout; the
	// daemon may override at boot via FAAS_OCI_PULL_TIMEOUT_SECONDS but
	// there is no per-deployment knob — every plan shares the same
	// ceiling so the cold-boot latency contract (§14, wake < 350 ms)
	// stays predictable. 60s is well above the largest manifest +
	// image-config GET and a generous safety margin over the
	// fail-fast PullImageConfig path.
	OCIPullTimeoutSeconds = 60

	// Idle timeout tuning (spec §4.3): app-configurable down to this floor, and
	// no higher than plan default × this multiplier.
	IdleTimeoutFloorSeconds = 10
	IdleTimeoutMaxMultiple  = 2

	// Liveness probe (issue #554 / ADR-078). The host (cmd/vmmd) polls
	// the guest's vsock 1028 STREAM on every Period; after N
	// consecutive non-2xx (or timeout/conn-refused) responses the
	// guest-init hasn't ACKed, vmmd destroys the VM and schedd
	// cold-boots it from rootfs per ADR-005 (no snapshot restore).
	// 3 restarts within a sliding Window trigger ParkDeployment
	// with reason='liveness_exhausted' (pkg/sched/liveness_window.go).
	//
	// Sizing rationale:
	//   PeriodSeconds       — 5 s base. Below the §13 idle reaper's
	//                         30 s floor on Free, so a Hobby+ app is
	//                         noticed and replaced before it could
	//                         survive on idle alone. High enough that
	//                         a busy HTTP workload on a healthy VM
	//                         doesn't burn vsock CPU.
	//   ConsecutiveFailures — 3. Below the wire-level transient
	//                         burst length (4-5 with vsock retries
	//                         in pkg/fcvm/vmm.go:2019-2119) so a
	//                         flake doesn't trigger a destroy, but
	//                         well below the customer-visible
	//                         "still 5xx" budget of ~15 s.
	//   CooldownSeconds     — 60 s. The minimum gap between two
	//                         destroys on the same instance so a
	//                         cold-boot + first probe doesn't
	//                         immediately re-destroy if the previous
	//                         failure was a network condition that's
	//                         slower to clear than the FC restart.
	//   MaxRestarts         — 3. Three strikes inside the window.
	//   WindowSeconds       — 300 s. Per the issue's acceptance #3,
	//                         "3 restarts in 5 min" — picked because
	//                         it's the longest a customer will
	//                         tolerate before they want their
	//                         service dead, not parked.
	//
	// Clamps (Limits.X validation in §13 mirror table):
	//   Period      ∈ [1, 60]   — sub-second probes burn vsock CPU
	//                              (each guest-init ack takes ≥2 ms
	//                              through the framed proto); >60 s
	//                              exceeds the customer-visible 5xx
	//                              budget.
	//   Consecutive ∈ [1, 10]   — 1 = hair-trigger shutdown on every
	//                              transient; 10 = effectively off.
	//   Cooldown    ∈ [10, 600] — <10 s on a hot loop; >10 min hides
	//                              a genuinely broken VM.
	//   MaxRestarts ∈ [1, 10]   — single restarts in window invalid
	//                              the "park" semantics; >10 = no
	//                              customer clarifies the cap.
	//   Window      ∈ [60, 3600] — <60 s collapses the window into
	//                              a single tick; >1 h loses the
	//                              "5 min" AC verbatim.
	//
	// gRPC liveness (Pro+) is deferred to v2 — the v1 path is HTTP
	// only, mirroring the existing readiness probe on `healthcheck_path`.
	// Plan.GRPCLivenessAllowed() returns false in v1 and exists in
	// the API surface so v2 can flip it without a DTO change.
	DefaultLivenessPeriodSeconds       = 5
	DefaultLivenessConsecutiveFailures = 3
	DefaultLivenessCooldownSeconds     = 60
	DefaultLivenessMaxRestarts         = 3
	DefaultLivenessWindowSeconds       = 300
	// MinLivenessCooldownSeconds / MaxLivenessCooldownSeconds
	// bound the per-deployment CooldownS override (issue #554 /
	// ADR-078). The window must be wide enough that a noisy
	// cold-boot doesn't get torn down (≥10s) and narrow enough
	// that a wedged app doesn't sit in grace forever (≤600s).
	MinLivenessCooldownSeconds = 10
	MaxLivenessCooldownSeconds = 600
	// ColdBootBudgetSeconds (issue #554 / ADR-079 follow-up, AC
	// #1) is the wall-clock budget the §14 metal acceptance
	// gate evaluates against when validating the liveness
	// cycle on a real Firecracker VM. The envelope is:
	//   LivenessPeriodSeconds * ConsecutiveFailures + ColdBootBudget
	//   = 5 * 3 + 30 = 45 s
	// After the 3 consecutive probe failures, vmmd has at most
	// ColdBootBudgetSeconds to tear the wedged VM down + bring
	// up a fresh one via cold boot (the load-bearing AC #1
	// invariant — a wedged snapshot must NOT be restored).
	ColdBootBudgetSeconds    = 30
	MinLivenessPeriodSeconds = 1
	MaxLivenessPeriodSeconds = 60

	// Autoscale (issue #169 / §17 G8). ScaleUpDecisionIntervalSeconds
	// is the trigger's tick rate — 1 s balances "admit the Nth
	// instance before the gateway wake queue builds" against "don't
	// hammer Postgres with a full app list on every tick". ScaleUpWindowSeconds
	// is the rolling RPS window — 5 s is the smallest window that
	// smooths a single-tick spike without lagging so much that a burst
	// is already over by the time the trigger fires.
	ScaleUpDecisionIntervalSeconds = 1
	ScaleUpWindowSeconds           = 5

	// Scaling policy cooldowns (issue #462 / ADR-058). The
	// customer-facing knobs are `scale_out_cooldown_s` /
	// `scale_in_cooldown_s` on the wire; the floor / ceiling
	// constants below are the admission time clamp apid uses to
	// validate the PATCH. The floors prevent a self-DoS via
	// `cooldown_s: 0` (the engine would otherwise admit every
	// request inside the same tick). The ceilings bound the
	// customer against accidentally making the engine inert
	// (24 h ceiling on scale-in is the maximum a customer
	// reasonably wants to dampen oscillation — anything longer
	// is a "stuck running" footgun).
	//
	// MinScaleOutCooldownS = 1 (1 s floor — the engine's tick is
	//   1 s, so 0 would always be honored as "now" and 1 is the
	//   smallest strictly-positive value).
	//
	// MaxScaleOutCooldownS = 3600 (1 h ceiling — any longer makes
	//   a legitimate burst unresponsive; 1 h is the practical
	//   upper bound for a "shock absorber" knob).
	//
	// MinScaleInCooldownS = 5 (5 s floor — matches the reaper's
	//   5 s floor on `ReapIdle`, so a manually-tuned scale-in
	//   cooldown cannot be tighter than the reaper's idle window).
	//
	// MaxScaleInCooldownS = 86400 (1 day ceiling — the customer
	//   who wants a "never scale-in" knob uses max_instances = 0
	//   via the legacy code path; values >= 1 day are degenerate
	//   but legal and the engine clamps to today+1d internally).
	MinScaleOutCooldownS = 1
	MaxScaleOutCooldownS = 3600
	MinScaleInCooldownS  = 5
	MaxScaleInCooldownS  = 86400

	// Tier A4 (cross-node app rebalance, ADR-064 follow-up to
	// ADR-062): pacing + per-tick cap on pkg/sched/rebalancer.go.
	//
	// RebalanceCooldownSeconds is the minimum gap between two
	// successful reassignments of the same app. A flap-loop
	// (operator toggles compute_nodes.active=false / true rapidly)
	// is suppressed by stamping apps.reassigned_at and filtering
	//   now() - reassigned_at < RebalanceCooldownSeconds.
	// Defaults to 60s; tunable via FAAS_REBALANCE_COOLDOWN_SECONDS
	// (cmd/schedd/config.go reads the env, the live watcher
	// stamps the value through Store.ListOrphanedApps's bound
	// parameter).
	//
	// RebalanceMaxPerTickPerNode caps the per-drain-event batch so
	// a 5,000-app orphaned node doesn't monopolise the schedd
	// worker pool. Excess apps stay pinned; the next
	// compute_node_changed event retries (heartbeat-staleness also
	// re-fires). Tunable via FAAS_REBALANCE_MAX_PER_TICK.
	RebalanceCooldownSeconds   = 60
	RebalanceMaxPerTickPerNode = 50

	// Tier A5 (cross-node live-instance migration, ADR-070
	// follow-up to ADR-064): pacing + lease window on
	// pkg/sched/migration_handoff.go.
	//
	// MigrateLiveMaxPerTick caps the per-drain-event batch so a
	// node with 500 RUNNING instances doesn't monopolise the
	// schedd worker pool. The rebalancer breaks each candidate
	// instance into a fresh four-phase handoff; excess candidates
	// stay on the dead node and retry on the next
	// compute_node_changed re-fire. Defaults to 10; tunable via
	// FAAS_MIGRATE_LIVE_MAX_PER_TICK (env-overridable, see
	// cmd/schedd/main.go::runWithDeps; propagated via
	// Engine.WithMigrateLiveConfig).
	//
	// MigrateLiveLeaseSeconds is the upper bound on the four-phase
	// handoff — Phase 1 mints a lease_token, Phase 3 commits or
	// the lease expires. The dying vmmd resumes the VM on lease
	// expiry (the snapshot stays). Tuned to comfortably exceed the
	// snapshot-upload + restore round-trip on the OCIRegistry
	// backend (latency dominated by the registry pull, not the
	// local VM lifecycle). Defaults to 90s; tunable via
	// FAAS_MIGRATE_LIVE_LEASE_SECONDS (env-overridable, see
	// cmd/schedd/main.go::runWithDeps; propagated via
	// Engine.WithMigrateLiveLeaseSeconds).
	//
	// Hard limits policy (CLAUDE.md): every limit is a constant
	// here, never inlined.
	MigrateLiveMaxPerTick   = 10
	MigrateLiveLeaseSeconds = 90

	// Tier A6 (migrating-instance watchdog, ADR-067 follow-up to
	// ADR-070): self-heal stuck state='migrating' rows that
	// never committed (the new owner vmmd died mid-handoff, the
	// network partition dropped the gRPC, the operator killed
	// the new owner before the commit). The watchdog is the
	// only writer that can move a row out of 'migrating' without
	// a peer commit — every Phase 4 path (CancelInstanceMigration)
	// requires a peer, and the peer is the very thing that's gone.
	//
	// MigratingWatchdogTickLimit is the per-tick cap on the
	// reconcile batch. A backlog of stuck rows past this cap is
	// itself a "you broke something" event (the metric fires
	// `outcome="cap_exceeded"`); a backlog over 50 means a peer
	// dropped tens of migrations in flight and the operator
	// should investigate before the next drain. Defaults to 50;
	// tunable via FAAS_MIGRATING_WATCHDOG_TICK_LIMIT (env-
	// overridable, see cmd/schedd/main.go::runWithDeps).
	//
	// MigratingWatchdogIntervalSeconds is the per-tick cadence
	// of the watchdog. Default 1s; matches the existing reaper /
	// cron tick. A 1s cadence is overkill for a 90s lease window
	// but matches the existing pattern (every other 1s tick in
	// pkg/sched/loop.go is the same shape). Tunable via
	// FAAS_MIGRATING_WATCHDOG_INTERVAL_SECONDS.
	//
	// Hard limits policy (CLAUDE.md): every limit is a constant
	// here, never inlined.
	MigratingWatchdogTickLimit       = 50
	MigratingWatchdogIntervalSeconds = 1

	// Dead-node billing reconciler. schedd's heartbeat sweep flips
	// compute_nodes.active = false when a node stops answering, but
	// that write deliberately does not touch `instances` — so a vmmd
	// that dies without transitioning its rows leaves them RUNNING
	// forever. meterd bills on State.CountsForRAM() with no
	// node-liveness cross-check (pkg/meter/sampler.go), which means
	// the customer keeps paying for a VM that no longer exists, and
	// the phantom rows keep consuming the §6.2-2 RAM ceiling.
	// Engine.ReconcileDeadNodeInstances closes both.
	//
	// DeadNodeReconcilerStalenessSeconds is how long a node's
	// last_heartbeat_at may age before its RUNNING instances are
	// considered orphaned. It MUST be ≥ the heartbeat staleness
	// window (state.DefaultHeartbeatStaleness, 90s) — reconciling
	// sooner than schedd itself declares a node dead would fail
	// instances on a node that is merely slow. 120s = 90s + one 30s
	// heartbeat interval of slack, so a single missed tick during a
	// GC pause or a schedd restart never fails a live instance.
	//
	// DeadNodeReconcilerTickLimit caps the per-tick write burst. A
	// whole node's instances can go dead at once, and this is a
	// terminal transition — bounding the batch keeps one tick's
	// write amplification predictable. The sweep re-runs on the next
	// tick until the set drains, ordered longest-dead-first so the
	// worst billing offenders are corrected first.
	DeadNodeReconcilerStalenessSeconds = 120
	DeadNodeReconcilerTickLimit        = 50

	// DeadNodeReconcilerIntervalSeconds is the sweep cadence. 30s,
	// not the §6.1 watchdog's 1s: the staleness window is 120s, so a
	// 1s tick would issue 120 identical no-op queries per node-death
	// before the first row is even eligible. 30s bounds the worst-case
	// extra billing exposure to one tick (a node dying just after a
	// sweep is corrected ≤150s later, well inside a single billed
	// minute) while keeping the query load negligible.
	DeadNodeReconcilerIntervalSeconds = 30

	// Tier A9 (capacity-pressure-triggered cross-node app rebalance,
	// ADR-087 — sibling to the dead-node rebalancer of ADR-064).
	// Today apps are durably pinned to a single compute_node via
	// apps.node_id (NOT NULL post-migration 00090). When the owner
	// node is at capacity but still healthy (active=true), the wake
	// path returns AtCapacity=true and the customer sees 503 even
	// though a peer schedd has headroom. Tier A9 closes the gap
	// with a sustained-pressure watcher that reassigns the app's
	// apps.node_id to a peer with admission headroom.
	//
	// PressureAtCapacityThresholdPerMin is the per-app
	// AtCapacity-event count over a 60s sliding window that marks
	// the app as "pressured" and eligible for cross-node reassign.
	// 5/min is high enough to ignore single-fail GC pauses or a
	// cold-boot burst, low enough that a sustained customer spike
	// reassigns within two reassessment sweeps (60s). Tunable via
	// FAAS_PRESSURE_THRESHOLD_PER_MIN (env-overridable, see
	// cmd/schedd/main.go::runWithDeps; propagated via
	// Engine.WithPressureConfig).
	//
	// PressureReassessmentIntervalSeconds is the sweep cadence at
	// which the watcher reconciles the aggregator. 30s matches the
	// existing rebalancer / router_watcher / dead-node-reconciler
	// family. A 1s tick would issue 60 identical no-op queries per
	// app before the threshold could fire. Tunable via
	// FAAS_PRESSURE_REASSESSMENT_SECONDS.
	//
	// PressureMigrationPolicy is the closed-set string that gates
	// the four-phase live-instance migration (ADR-066) on the
	// pressure path. Closed set ∈ {skip_live, migrate_after_1,
	// migrate_after_2}. Default migrate_after_2: cheap parked-only
	// reassign on the first sustained sweep, expensive live
	// migration on the second sustained sweep within the same
	// window. skip_live never migrates live instances (cheap path
	// only — apps with live instances stay pinned and the customer
	// sees 503 until the instances drain). migrate_after_1 fires
	// the live handoff on the first sustained sweep (highest
	// cost, lowest customer-facing latency). Closed-set validation
	// lives in cmd/schedd/main.go; bad values panic at startup.
	// Tunable via FAAS_PRESSURE_MIGRATION_POLICY.
	//
	// Hard limits policy (CLAUDE.md): every limit is a constant
	// here, never inlined.
	PressureAtCapacityThresholdPerMin   = 5
	PressureReassessmentIntervalSeconds = 30
	PressureMigrationPolicy             = "migrate_after_2"

	// Tier A7 (edge split — gatewayd-public / gatewayd-internal,
	// ADR-070): drain + replica registry + warm-hint-cache tunables.
	//
	// GatewayDrainGraceSeconds is the upper bound on the in-flight
	// request window after SIGTERM before http.Server.Shutdown
	// returns. Tuned to be 5s shorter than the systemd unit's
	// TimeoutStopSec (60s) so the daemon exits cleanly inside the
	// unit's grace. Tunable via FAAS_GATEWAY_DRAIN_GRACE_SECONDS.
	//
	// ReplicaHeartbeatIntervalSeconds is the cadence at which an
	// internal daemon re-asserts its presence to the public daemon
	// over the /run/faas/gatewayd-public.sock unix socket. The
	// public daemon marks a peer unready after 2× this interval
	// without a heartbeat (no extra constant — the 2x factor lives
	// in cmd/gatewayd-public/replicas.go::isStale). Tunable via
	// FAAS_REPLICA_HEARTBEAT_SECONDS.
	//
	// WarmHintCacheSize caps the per-daemon in-memory mirror of the
	// `warm_hint` table. Hot apps fit in the first 1000 entries; a
	// larger cache trades RAM for cold-miss latency. Tunable via
	// FAAS_WARM_HINT_CACHE_SIZE.
	//
	// CertSyncIntervalSeconds is the leader-side safety-net cron
	// cadence for the legacy certsync replicator (the fast-path is
	// the certmagic OnEvent callback). 30s is the worst-case lag
	// a follower replica carries a stale cert; in steady state
	// the OnEvent fast path keeps lag ≤1s. Tunable via
	// FAAS_CERT_SYNC_INTERVAL_SECONDS.
	//
	// Legacy daemon only (revised 2026-08-04): the certsync
	// replicator + this constant are owned by `cmd/gatewayd-public/` for
	// the migration window. PR #633 stripped certsync from
	// `gatewayd-public`; PR-C will sweep the constant + the
	// `pkg/gateway/certsync` package together once the legacy
	// daemon is retired.
	//
	// Hard limits policy (CLAUDE.md): every limit is a constant
	// here, never inlined.
	GatewayDrainGraceSeconds        = 25
	ReplicaHeartbeatIntervalSeconds = 5
	WarmHintCacheSize               = 1000
	CertSyncIntervalSeconds         = 30

	// Tenant-surface cert engine constants (ADR-100 amendment,
	// PR-D cert-engine-real-mint). The cap + renew-window + tick live
	// here per CLAUDE.md "Hard limits policy" — no inline numbers in
	// the issuer.
	//
	// MaxSANPerCert is LE's per-order SAN hard cap. Anything above this
	// must be split across multiple orders; PR-D fails closed (the
	// follow-up ADR-114 wires the split-aware `per_host` fallback).
	//
	// CertRenewBeforeNotAfterDays is the renew-window threshold: a
	// surface with cert_not_after < now+window is queued for re-mint
	// on the next renewer tick. LE's per-order rate limit exempts
	// renewals of existing certs, so this is the same shape for
	// fresh issuance and renewal — the renewer reuses the existing
	// certmagic.Renew path through the same surface-remint pipeline.
	// 30 days mirrors LE's expiry notification cadence (30/7/1 day
	// emails) so an operator reading the alert sees the same window.
	//
	// CertRenewTickSeconds is the renewer goroutine cadence — every
	// tick reads `tenant_surfaces_cert_expiry_idx` and re-mints
	// surfaces inside the window. 5 min keeps file I/O negligible
	// (one SELECT per tick) and the dashboard's "time-to-cert"
	// panel has sub-minute resolution.
	MaxSANPerCert               = 100
	CertRenewBeforeNotAfterDays = 30
	CertRenewTickSeconds        = 300
	// CertRenewTickBatchLimit bounds the renewer's per-tick
	// page size (PR-D code review Candidate 6). One SQL
	// query per tick returns up to this many due surfaces;
	// the next tick continues from the keyset cursor. The
	// 1k cap keeps a single tick's UPDATE + notify fan-out
	// bounded so a CA outage that lands N>1000 surfaces in
	// the renewal window does NOT spike IOPS into the
	// quadratic region.
	CertRenewTickBatchLimit = 1000

	// Tier A8 (active-passive HA topology, ADR-083 — closes the
	// §14 M8 "Gate-A runbook (2nd box active-passive)" gap left
	// by Tier A4 + A5 + A7). Lex-min leader election lives in
	// pkg/gateway/leader; standby warm-up + drain handoff lives
	// in cmd/gatewayd-public (PR-B).
	//
	// HAFailoverProbeTimeoutMS is the per-probe HTTP HEAD timeout
	// for the standby warm-up scraper in
	// cmd/gatewayd-public/standby_warmup.go. Each probe pre-warms
	// the per-app target-set cache so the new leader's first
	// request to an app hits a warm cache (no cold-boot penalty).
	// 500 ms is the worst-case round-trip from
	// `gatewayd-public` → `gatewayd-internal` → cache write —
	// much shorter than the existing wake-quiesce window. On
	// timeout the scraper logs Warn and skips the app; the
	// ADR-005 cold-boot safety net still serves the request.
	// Tunable via FAAS_HA_FAILOVER_PROBE_TIMEOUT_MS.
	//
	// HADNSRecordStaleSeconds bounds the drain protocol in
	// cmd/gatewayd-public/dns_handoff.go — the time between
	// `StandbyState → draining` and `dns.DeleteRecord`. 30 s
	// matches typical DNS TTL so the operator's resolver cache
	// stays honest, and bounds the operator's `kubectl drain`
	// analog (a stuck drain doesn't block the operator). On
	// expiry the leader increments
	// `gateway_active_passive_failovers_total{outcome="peer_unreachable"}`
	// and the runbook's manual drain command kicks in.
	// Tunable via FAAS_HA_DNS_RECORD_STALE_SECONDS.
	//
	// HAStandbyWarmupIntervalMS is the cadence at which a standby
	// re-scrapes cmd/gatewayd-internal on each known app's
	// hostname. The full per-app cache TTL is
	// HAStandbyWarmupIntervalMS × targetSetCacheTTL; tuned so the
	// standby's cache is always within one scrape of the leader's
	// cache. Tunable via FAAS_HA_STANDBY_WARMUP_INTERVAL_MS.
	//
	// Hard limits policy (CLAUDE.md): every limit is a constant
	// here, never inlined.
	HAFailoverProbeTimeoutMS  = 500
	HADNSRecordStaleSeconds   = 30
	HAStandbyWarmupIntervalMS = 500

	// Tier A9 (standby write-redirect, ADR-084 — closes ADR-083
	// §Open follow-up #2). The redirect lives in
	// cmd/gatewayd-internal/proxy.go (PR-B); the constants below
	// bound every timer that PR-B's writeGate consults so the
	// PR-A refactor can land them in advance.
	//
	// StandbyWriteRedirectTimeoutMS is the cross-box mTLS hop
	// budget for the transparent relay. A standby that receives
	// a bearer-authenticated mutation opens an outbound
	// https://<leaderURL>/ dial; on timeout the standby
	// degrades to 307 Temporary Redirect so the CLI's stdlib
	// http.Client follows the public edge instead. Tunable via
	// FAAS_STANDBY_WRITE_REDIRECT_TIMEOUT_MS.
	//
	// StandbyWriteRetryAfterSeconds is the Retry-After value
	// attached to the 503/307 responses the standby emits (cookie
	// writes on standbys, dial-failure fallback). 5 s is well
	// under the typical DNS TTL so the customer's next retry
	// usually lands on the new leader via DNS resolution.
	// Tunable via FAAS_STANDBY_WRITE_RETRY_AFTER_SECONDS.
	//
	// StandbyWriteLeaderURLCacheTTLSeconds bounds the lifetime
	// of the cached leader URL inside pkg/gateway/writegate's
	// LeaderResolver. The cache is invalidated promptly on
	// compute_node_changed (pg_notify subscriber); 5 s is the
	// upper bound when the subscriber misses an event (e.g.
	// network blip on the unix socket). Tunable via
	// FAAS_STANDBY_WRITE_LEADER_URL_CACHE_TTL_SECONDS.
	//
	// StandbyWriteNoLeaderRetryAfterSeconds is the Retry-After
	// for the 503 emitted when election returns no active peer
	// (all boxes drained, or pg outage). 60 s is the spec's
	// "standby state warming too long" alert threshold —
	// longer than DNS TTL but short enough that a customer
	// retry doesn't pile up. Tunable via
	// FAAS_STANDBY_WRITE_NO_LEADER_RETRY_AFTER_SECONDS.
	//
	// Hard limits policy (CLAUDE.md): every limit is a constant
	// here, never inlined.
	StandbyWriteRedirectTimeoutMS         = 5000
	StandbyWriteRetryAfterSeconds         = 5
	StandbyWriteLeaderURLCacheTTLSeconds  = 5
	StandbyWriteNoLeaderRetryAfterSeconds = 60

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

	// DefaultDiskDriftInterval is the cadence for the read-only
	// /srv/fc/snap vs DB size-tracking drift sweep (PR scale-out
	// readiness #3). Hourly matches DefaultRetentionInterval so the
	// two hourly tickers fire on aligned boundaries and don't drift
	// apart by minute-precision. The sweep never writes — it only
	// increments OpsMetrics.SnapshotDiskDrift when a disk-vs-DB
	// discrepancy is observed.
	DefaultDiskDriftInterval = 1 * time.Hour

	// WarmAffinityTTL is how long pkg/sched.WarmAffinity remembers the
	// last-warm compute node for an app (placement scheduler, ADR-025).
	// The chooser biases a wake toward the remembered node so a hot
	// app's snapshot + page cache stay warm (ADR-009). 30 minutes
	// matches the Pro plan idle-timeout default — a hot app on a
	// 30-minute TTL keeps the snapshot warm across one reaper cycle.
	// Overridable via FAAS_WARM_AFFINITY_TTL at the schedd daemon.
	// Sticky-warm is bias, never a gate (ADR-005: cold boot must
	// always work); an expired or missing hint falls through to
	// least-loaded RAM headroom.
	WarmAffinityTTL = 30 * time.Minute

	// DefaultConntrackCap is the spec §7 per-instance conntrack cap
	// (docs/faas_implementation_spec.md:344). One platform-wide number;
	// not per-plan tiered — every tenant sees the same cap because the
	// failure mode (host conntrack exhaustion) is a single shared
	// resource. ADR-018 deferred the enforcement to this PR; the value
	// is the spec literal. vmmd wires it into netns.Config at every
	// Wake (pkg/fcvm/manager.go:236) and the nft rule that consumes
	// it lives in pkg/netns/config.go::NftCommands.
	DefaultConntrackCap = 4096

	// ConntrackCap is the spec §7 per-instance conntrack cap value.
	// Use ConntrackCapProbe() at runtime to get the effective value,
	// which falls back to 0 on kernels without per-netns conntrack
	// support (CONFIG_NF_CONNTRACK_NET_NS=n). The egress tc cap is
	// unaffected.
	ConntrackCap = DefaultConntrackCap

	// DefaultMaxHeaderBytes caps the http.Server header size on the
	// gatewayd-public listeners (public + control). It mirrors stdlib's
	// historical 1 MiB default but pins it so a future stdlib default
	// change cannot widen the attack surface on this listener; a single
	// tenant-crafted 1 MiB header is fine, 64 MiB is not.
	DefaultMaxHeaderBytes = 1 << 20 // 1 MiB
)

// DefaultComputeNodeCeilingMB is the per-compute-node admission ceiling
// schedd hands out when no operator override is present. It mirrors
// RAMAdmissionCeilingMB (85% of the tenant budget) because a single
// compute node today owns the entire tenant slice on the one-box; when
// a future multi-node world splits tenant traffic across nodes, this
// helper is the single place to revisit (e.g. per-node share = ceiling
// / node count). Migrated from inline literals in
// pkg/state/memstore.go:seedDefaultLocalNodeLocked and
// cmd/vmmd/config.go:LoadConfig (PR scale-out readiness #4). The
// helper resolves to the same integer today — no behavior change.
func DefaultComputeNodeCeilingMB() int {
	return RAMAdmissionCeilingMB
}

// DefaultHostBridgeCIDR is the per-host bridge CIDR the vmmd tenant
// netns' veth host-side addresses live in (Mega-PR-B Commit 1;
// supersedes the former pkg/netns Go const HostBridgeCIDR). The /16
// default keeps single-host dev identical to the v1 behaviour; multi-
// host deployments override via ComputeNodeConfig.HostBridgeCIDR
// (cmd/vmmd/config.go) — the bridge IP is the .1 of whatever CIDR the
// operator ships. The MasqueradeCIDR default mirrors this value so the
// host forward chain's `ip saddr ... oifname ... masquerade` rule still
// covers the per-instance bridge.
func DefaultHostBridgeCIDR() netip.Prefix {
	return netip.MustParsePrefix("10.100.0.0/16")
}

// DefaultMasqueradeCIDR is the host postrouting nat chain MASQUERADE
// source CIDR (must equal the NETWORK form of DefaultHostBridgeCIDR —
// see pkg/netns/policy.go::Render panic gate). Exposed as a string
// for renderer/tests that don't need the netip.Prefix.
var DefaultMasqueradeCIDR = DefaultHostBridgeCIDR().String()

// DefaultOverlayCIDR is the per-host overlay subnet the vmmd overlay
// detector prefers when multiple IPv4 candidates come back from
// `tailscale ip -4` (Mega-PR-B Commit 3). The default matches the
// Tailscale CGNAT range (100.64.0.0/10); WireGuard deployments override
// via ComputeNodeConfig.OverlayCIDR. Mesh traffic between compute nodes
// lands here; the host forward chain's overlay-accept rules (Commit 2)
// unblock it past the §11 RFC1918 deny.
func DefaultOverlayCIDR() netip.Prefix {
	return netip.MustParsePrefix("100.64.0.0/10")
}

// ConntrackCapProbe returns the effective per-instance conntrack cap.
const (
	probeNS        = "faas-ct-probe"
	probeTable     = "faas_ct_probe"
	probeFamily    = "ip"
	probeChain     = "forward"
	probeNftCmd    = "nft"
	probeNftAdd    = "add"
	probeNetnsExec = "exec"
	probeNetnsCmd  = "netns"
)

// Returns DefaultConntrackCap when the kernel supports the ct expression
// inside network namespaces (CONFIG_NF_CONNTRACK_NET_NS=y); returns 0
// when it doesn't so the ct cap rules are silently omitted (egress tc
// cap is unaffected). Callers call this once at setup and cache the
// result — the kernel conntrack netns capability never changes at runtime.
func ConntrackCapProbe() int64 {
	// Skip probe in tests: tests that don't use metal don't need netns,
	// and metal tests create their own netns under leakcheck supervision.
	if testing.Testing() {
		return DefaultConntrackCap
	}
	bail := func() int64 { return 0 }

	// Clean up any stale probe namespace from a previous crash.
	if _, err := os.Stat("/run/netns/" + probeNS); err == nil {
		go func() { _, _ = execCmd("ip", probeNetnsCmd, "del", probeNS) }()
	}

	// Create a temporary netns for the probe.
	if _, err := execCmd("ip", probeNetnsCmd, "add", probeNS); err != nil {
		// Cannot create netns at all (e.g. Lima nested virt). Disable.
		return bail()
	}
	// Unconditional delete regardless of outcome.
	go func() { _, _ = execCmd("ip", probeNetnsCmd, "del", probeNS) }()

	// Quick probe: add a table + a rule using "ct state" (simpler than
	// "ct count over") inside the netns. If the kernel lacks conntrack
	// netns support, nft returns "No such file or directory".
	probe := func(expr string) bool {
		cmds := [][]string{
			{"ip", probeNetnsCmd, probeNetnsExec, probeNS, probeNftCmd, probeNftAdd, "table", probeFamily, probeTable},
			{"ip", probeNetnsCmd, probeNetnsExec, probeNS, probeNftCmd, probeNftAdd, "chain", probeFamily, probeTable, probeChain,
				"{", "type", "filter", "hook", probeChain, "priority", "filter", ";", "policy", "accept", ";", "}"},
			{"ip", probeNetnsCmd, probeNetnsExec, probeNS, probeNftCmd, probeNftAdd, "rule", probeFamily, probeTable, probeChain, expr},
		}
		for _, cmd := range cmds {
			if _, err := execCmd(cmd[0], cmd[1:]...); err != nil {
				return false
			}
		}
		return true
	}

	if probe("ct state established,related accept") && probe("ct count over 4096") {
		return DefaultConntrackCap
	}
	return bail()
}

// execCmd runs argv and returns combined output. Isolated here so
// limits.go stays a pure config package without external syscall
// imports polluting its API surface.
func execCmd(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

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

// IsPaid reports whether the plan is a paid tier (hobby/pro/scale).
// Free is the only non-paid plan; the changePlan handler (cmd/apid
// handlers_ext.go) uses this to decide whether an API-only upgrade
// requires a Stripe subscription item (issue #142).
func (p Plan) IsPaid() bool {
	return p == PlanHobby || p == PlanPro || p == PlanScale
}

// RequiresStripeUpgradeTo reports whether moving from p → next counts as
// a paid-upgrade that needs a Stripe subscription item. Downgrades
// (any → free) and same-tier moves return false; the customer can
// always downgrade without Stripe. The only free → paid direct path
// is free → hobby (the v0 upgrade); free → pro/scale and any
// hobby → pro/scale and pro → scale require a Stripe subscription item.
//
// The Stripe webhook is the legitimate path to set a paid plan — it
// stamps StripeSubscriptionItem on the account record before the plan
// change, so the same handler that rejects free → pro for an
// API-key-only call accepts free → pro when the Stripe item is set.
//
// Fail-closed on unknown plans: an unknown `from` (e.g. a future
// enterprise tier added without updating this switch) returns true so
// the 402 gate fires — a missing case must never silently let a
// customer upgrade without billing. Reviewers: keep this default in
// place if you extend the switch above.
func (p Plan) RequiresStripeUpgradeTo(next Plan) bool {
	if !next.Valid() {
		return false // caller's plan.Valid() check already covers this
	}
	switch p {
	case PlanFree:
		return next == PlanPro || next == PlanScale
	case PlanHobby:
		return next == PlanPro || next == PlanScale
	case PlanPro:
		return next == PlanScale
	case PlanScale:
		return false
	default:
		return true // unknown source plan: require Stripe, do not silently allow
	}
}

// MinInstancesAllowed reports whether the plan may set the per-app
// cold-wake floor (ux_spec §6.5). Hobby + Pro + Scale opt in; Free
// stays scale-to-zero by default. apid's updateApp handler gates
// `req.MinInstances` on this; the CLI surfaces the rejection with
// CodePlanMinInstancesNotAllowed. PR-A history (issue #462 / ADR-058):
// Hobby unlocked at PR-A. The pre-#462 contract was Pro + Scale only;
// the tier-up landed because the bill auto-counts via
// pkg/meter/sampler.go:238-239 and Hobby's MaxConcurrency is bounded
// (2) so the worst-case residency cost is 2 × RAMMB + 16 MB overhead.
func (p Plan) MinInstancesAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.MinInstancesAllowed
}

// MaxInstancesAllowed (issue #462 / ADR-058) reports whether the
// plan may set a per-app live-instances ceiling. Mirrors the
// MinInstancesAllowed tier-up: Hobby + Pro + Scale opt in; Free
// stays off. The value the customer passes is bounded above by
// the plan's MaxConcurrency (which is the existing hard cap on the
// wake path), so the gate here is the plan-tier lock, not the
// value-lock. apid's updateApp handler gates
// `req.ScalingPolicy.MaxInstances` on this; the CLI surfaces the
// rejection with CodePlanMaxInstancesNotAllowed.
func (p Plan) MaxInstancesAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.MaxInstancesAllowed
}

// SidecarAllowed (issue #463 / ADR-070 §Decision 1) reports whether
// the plan may attach sidecars to a deployment. PR-A's accessor
// returns true for every plan — the load-bearing gate is the GLOBAL
// `SidecarCapMax` constant, not a per-plan matrix. A future PR
// may grow this to a per-plan field (e.g. Free = 0, Hobby/Pro/Scale
// = 2) if telemetry shows Free-tier abuse; for PR-A the method
// exists so the apid handler can read a single source of truth
// without inlining the global cap. The companion
// `ErrSidecarNotAllowedOnPlan` constructor is reserved for that
// future per-plan gate.
func (p Plan) SidecarAllowed() bool {
	return true
}

// EgressAllowlistAllowed reports whether the plan may set a per-app
// outbound IP allowlist (ADR-031). Pro + Scale opt in; Free + Hobby
// stay off — the abuse-desk hygiene this surface gives is a paid
// concern. apid's updateApp handler gates `req.EgressAllowlist` on
// this; the CLI surfaces the rejection with
// CodePlanEgressAllowlistNotAllowed. Unknown plans fail closed
// (return false) so a missing row never silently unlocks a
// premium feature — same contract as MinInstancesAllowed above.
func (p Plan) EgressAllowlistAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.EgressAllowlistAllowed
}

// EgressAllowlistMaxSize returns the per-plan CIDR-entry cap for an
// allowlist (ADR-031). 0 for Free/Hobby (the gate above rejects
// before this matters); 16 for Pro; 64 for Scale. apid rejects a
// PATCH whose `req.EgressAllowlist` has more entries with 400
// egress_allowlist_too_long. Returning 0 on unknown plans makes a
// missing plan row a fail-closed denial, not a silent default.
func (p Plan) EgressAllowlistMaxSize() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.EgressAllowlistMaxSize
}

// StaticEgressIPAllowed (ADR-119) reports whether the plan may pin
// a static egress IP to an app. Scale-only in v1 — the B2B
// allowlist use case is a paid Scale concern, mirroring how
// EgressAllowlistAllowed gates Pro+. apid's updateApp handler
// gates `req.StaticEgressIP` on this; the CLI surfaces the
// rejection with CodePlanStaticEgressIPNotAllowed. Unknown plans
// fail closed (return false) so a missing row never silently
// unlocks a premium feature — same contract as
// EgressAllowlistAllowed above.
func (p Plan) StaticEgressIPAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.StaticEgressIPAllowed
}

// StaticEgressIPsPerApp returns the per-plan count cap on pinned
// static egress IPs (ADR-119). 0 for Free/Hobby/Pro (the gate
// above rejects before this matters); 1 for Scale in v1. apid
// rejects a PATCH whose `req.StaticEgressIP` is non-null on an
// account that already pins the same IP on a different app with
// 403 plan_static_egress_ip_quota (defends against alias-IP
// collision on br-tenants). Returning 0 on unknown plans makes a
// missing plan row a fail-closed denial, not a silent default.
func (p Plan) StaticEgressIPsPerApp() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.StaticEgressIPsPerApp
}

// PublicAuthIPAllowlistAllowed reports whether the plan may set a
// per-app ingress IP allowlist (ADR-118). Pro/Scale only — Free/Hobby
// use edge rules (kind='ip') for the abuse-floor posture. apid's
// updateApp handler gates `req.PublicAuthIPAllowlist` on the bool;
// the handler returns 403 CodePlanPublicAuthIPAllowlistNotAllowed.
// Unknown plans fail closed (return false) so a missing row never
// silently unlocks a premium feature — same contract as
// EgressAllowlistAllowed above.
func (p Plan) PublicAuthIPAllowlistAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.PublicAuthIPAllowlistAllowed
}

// PublicAuthMembersOnlyAllowed reports whether the plan may opt
// apps into public_auth_mode='members_only' (ADR-123). Hobby+
// only — Free is locked because Free personal-org has exactly 1
// member (the account itself), so members_only on Free would
// collapse to bearer with the same account. apid's updateApp
// handler gates `req.PublicAuth.Mode` on the bool; the handler
// returns 402 CodePlanPublicAuthMembersOnlyNotAllowed. Unknown
// plans fail closed (return false) — same contract as the other
// accessors above. The structural symmetry with
// PublicAuthBearerAllowed (Hobby+, the apps:read scope requires
// Hobby+) is deliberate: both gates enforce that the
// human-identity shape is paid-tier.
func (p Plan) PublicAuthMembersOnlyAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.PublicAuthMembersOnlyAllowed
}

// PublicAuthIPAllowlistMaxEntries returns the per-plan CIDR-entry
// cap for the ingress IP allowlist (ADR-118). 0 for Free/Hobby
// (the gate above rejects before this matters); 16 for Pro; 64
// for Scale. apid rejects a PATCH whose
// `req.PublicAuthIPAllowlist` has more entries with 400
// public_auth_ip_allowlist_too_long. Returning 0 on unknown plans
// makes a missing plan row a fail-closed denial, not a silent
// default — same contract as EgressAllowlistMaxSize above.
func (p Plan) PublicAuthIPAllowlistMaxEntries() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.PublicAuthIPAllowlistMaxEntries
}

// LivenessAllowed (issue #554 / ADR-078) reports whether the plan
// may opt-in to per-deployment liveness probes. Free stays off —
// the §13 M7 free-stop budget already handles abuse-floor paths,
// and Hobby/Pro/Scale need parity with Cloud Run's primitive.
// apid's createDeployment handler gates `OverrideLivenessProbe`
// on this; the CLI surfaces the rejection with
// CodePlanLivenessProbeNotAllowed. Unknown plans fail closed
// (return false) — same contract as MinInstancesAllowed /
// EgressAllowlistAllowed above. The bool is the trigger; the
// numerical fields below are the HOW. Note that the per-Plan
// period / consecutive / cooldown / max restarts / window are
// the DEFAULTS the customer inherits; an explicit
// `OverrideLivenessProbe` on the deployment overrides every
// one of them per-deployment (issue #554 §"Per-deployment overrides").
func (p Plan) LivenessAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.LivenessPeriodSeconds > 0
}

// LogArchiveEnabled (issue #562) reports whether the plan
// ships logs to S3. Free returns false (the abuse-floor tier
// has no archive + read-back surface). Unknown plans fail
// closed to false so a missing plan row never silently
// enables the shipper + bucket-proxy surface.
func (p Plan) LogArchiveEnabled() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.LogArchiveEnabled
}

// LogArchiveRetentionDaysMax (issue #562) returns the
// per-plan ceiling on FAAS_LOG_ARCHIVE_RETENTION_DAYS. 0
// for Free (no archive); the apid bgBefore closure uses
// this to clamp the configured value at boot so an operator
// can't set a higher retention than the plan allows.
func (p Plan) LogArchiveRetentionDaysMax() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.LogArchiveRetentionDaysMax
}

// AppErrorsRetentionDays (ADR-096) returns the per-plan
// retention cap on app_errors / app_error_requests rows. The
// nightly purge cron in cmd/apid/app_errors_purge.go reads this
// to decide which rows to DELETE. Returns 1 on unknown plans
// (fail-closed minimum) so a missing plan row never silently
// keeps errors forever.
func (p Plan) AppErrorsRetentionDays() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 1
	}
	return l.AppErrorsRetentionDays
}

// AppErrorsMaxFingerprintsPerApp (ADR-096) returns the per-plan
// ceiling on distinct fingerprints the gatewayd-internal recorder
// retains in its LRU. The recorder uses this as the
// CardinalityLimit backstop; past the cap, new fingerprints are
// silently dropped (outcome="rate_limited"). Returns 50 on
// unknown plans (Free-tier floor).
func (p Plan) AppErrorsMaxFingerprintsPerApp() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 50
	}
	return l.AppErrorsMaxFingerprintsPerApp
}

// AppErrorsMaxRequestRowsPerFingerprint (ADR-096) returns the
// per-plan ceiling on app_error_requests rows retained per
// fingerprint. The retention purge deletes oldest rows beyond
// the cap first. Returns 25 on unknown plans (Free-tier floor).
func (p Plan) AppErrorsMaxRequestRowsPerFingerprint() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 25
	}
	return l.AppErrorsMaxRequestRowsPerFingerprint
}

// DebugTelemetryEnabled (ADR-127) returns whether the per-request
// telemetry plane is on for the account. Used by the apid handler
// in cmd/apid/handlers_debug_telemetry.go to fail-closed before the
// store is touched (api.ErrPlanFeatureGated). Returns false on
// unknown plans (Free-tier floor — debug telemetry is a paid-only
// surface, same posture as the per-plan *Allowed fields above).
func (p Plan) DebugTelemetryEnabled() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.DebugTelemetryEnabled
}

// DebugTelemetryRetentionDays (ADR-127) returns the per-plan cap on
// how long a request_telemetry row is queryable. Read by
// RetentionOnceRequestTelemetry in pkg/meter/retention.go to decide
// which monthly partition to drop. Returns 0 on unknown plans (Free
// floor — debug telemetry is off, retention is 0).
func (p Plan) DebugTelemetryRetentionDays() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.DebugTelemetryRetentionDays
}

// DebugTelemetryRequestsPerMinute (ADR-127) returns the per-account
// rate cap on the IncrementRequestTelemetry ingest RPC. Read by the
// publisher at startup; per-record overflow returns outcome
// {code: RATE_LIMITED, retry_after}. Returns 0 on unknown plans.
func (p Plan) DebugTelemetryRequestsPerMinute() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.DebugTelemetryRequestsPerMinute
}

// DebugTelemetryDeploymentsPerApp (ADR-127 §Decision 4) returns the
// per-app ceiling on distinct deployment_id labels the
// gateway_request_duration_seconds histogram admits. Read by
// deploymentLabelSet (pkg/gateway/deployment_label_set.go) at
// histogram-emission time; overflow collapses to "__other__". Returns
// 0 on unknown plans — fail-closed, same posture as the deployment-cap
// fields above.
func (p Plan) DebugTelemetryDeploymentsPerApp() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.DebugTelemetryDeploymentsPerApp
}

// DebugTelemetrySpansPerTrace (ADR-127 §Decision 5) returns the cap
// on customer OTel spans retained per request_telemetry row's
// spans_summary jsonb. Read by the OTel ingest path; past the cap
// the slowest N are kept and the rest truncated. Returns 0 on unknown
// plans.
func (p Plan) DebugTelemetrySpansPerTrace() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.DebugTelemetrySpansPerTrace
}

// PerAppMetricsAllowed returns whether the customer-facing per-app
// observability surface is on for the account. The surface covers
// GET /v1/apps/{slug}/metrics (latency / error rate / cold-boot
// ratio / wake count) and the JSON mirror of the wake-timeline page
// (GET /v1/apps/{slug}/wake-timeline). Used by the apid handlers
// in cmd/apid/handlers_metrics.go and the new
// cmd/apid/handlers_wake_timeline.go to fail-closed before
// loadApp is touched (api.ErrPlanPerAppMetricsNotAllowed). Returns
// false on unknown plans (Free-tier floor — the surface is a
// paid-only capability, same posture as the per-plan *Allowed
// fields above).
func (p Plan) PerAppMetricsAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.PerAppMetricsAllowed
}

// AppUsageSummaryAllowed returns whether the customer-facing per-app
// billing-usage read is on. Used by the apid handler in
// cmd/apid/handlers_usage.go to fail-closed before loadApp is
// touched (api.ErrPlanAppUsageSummaryNotAllowed). Returns false on
// unknown plans (Free-tier floor — billing transparency is a
// paid-only capability).
func (p Plan) AppUsageSummaryAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.AppUsageSummaryAllowed
}

// AppErrorsAllowed returns whether the per-app error-fingerprint
// read is on for the account. Used by the apid handler in
// cmd/apid/handlers_app_errors.go to fail-closed before loadApp is
// touched (api.ErrPlanAppErrorsNotAllowed). Returns false on
// unknown plans (Free-tier floor — error grouping is a paid-only
// capability).
func (p Plan) AppErrorsAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.AppErrorsAllowed
}

// LivenessPeriodSeconds returns the per-plan default poll cadence
// for the liveness probe (issue #554). 0 for Free — coupled to
// LivenessAllowed() above; if the customer is on a plan where
// LivenessAllowed() is false, this returns 0 and the apid handler
// rejects BEFORE reading the value. Unknown plans fail closed to 0
// so a missing plan row never silently starts polling.
func (p Plan) LivenessPeriodSeconds() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.LivenessPeriodSeconds
}

// LivenessConsecutiveFailures returns the per-plan default N: the
// number of consecutive non-2xx (or timeout / conn-refused)
// responses before the probe declares the VM wedged and triggers
// DestroyForLivenessFailure. See the §13 mirror comment block above
// for the sizing rationale (3 = healthy transient budget).
func (p Plan) LivenessConsecutiveFailures() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.LivenessConsecutiveFailures
}

// LivenessCooldownSeconds returns the per-plan default cooldown
// between two destroys on the SAME instance. vmmd's liveness receiver
// skips a destroy if the last restart was within this window —
// protects against a cold-boot + first probe immediately
// re-destroying if the previous failure was a network condition that
// didn't clear by the time FC restarted. See §13 mirror constant.
func (p Plan) LivenessCooldownSeconds() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.LivenessCooldownSeconds
}

// LivenessMaxRestarts returns the per-plan default N: the maximum
// number of restarts allowed in one LivenessWindowSeconds before
// the deployment is parked with reason='liveness_exhausted'. The
// default 3 is the issue AC #3 ceiling.
func (p Plan) LivenessMaxRestarts() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.LivenessMaxRestarts
}

// LivenessWindowSeconds returns the per-plan default sliding window
// the LivenessMaxRestarts counter is summed over. The tracker
// (pkg/sched/liveness_window.go) trims timestamps older than
// now - window off the per-deployment counter on every
// RecordRestart call. Default 300 s = 5 min per issue AC #3.
func (p Plan) LivenessWindowSeconds() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.LivenessWindowSeconds
}

// GRPCLivenessAllowed (issue #554 / ADR-078 §"gRPC liveness") reports
// whether the plan may opt-in to gRPC health-check probes (the
// gRPC ServiceConfig.health_check protocol). v1 returns false across
// the board — the existing readiness probe on `healthcheck_path` is
// HTTP-only and vmmd's liveness receiver dials vsock 1028 STREAM
// which the runner exposes over HTTP GET semantics. The accessor
// exists in the API surface so a v2 PR can flip it without a
// DTO/SDK change. Pro + Scale are the unlock point when v2 lands
// (mirrors the GRPCAllowed gate). Free + Hobby stay off.
func (p Plan) GRPCLivenessAllowed() bool {
	return false
}

// StreamingEnabled reports whether the plan defaults the per-app
// streaming_enabled column to true (issue #471 / ADR-047). Hobby/Pro/
// Scale opt in; Free stays off (spec §4.1 baseline; Free is the
// abuse-floor tier where an unbounded stream would let one app pin
// gatewayd-internal). The plan-level default is applied at CreateApp time in
// cmd/apid/handlers.go::buildApp; an existing app may still flip the
// flag via PATCH (gated by StreamingResponseAllowed so Free stays off
// even when an admin backfills the column). Unknown plans fail closed
// (return false) — same contract as MinInstancesAllowed /
// EgressAllowlistAllowed.
func (p Plan) StreamingEnabled() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.StreamingEnabled
}

// StreamingResponseAllowed reports whether the plan permits a customer
// to set apps.streaming_enabled=true via PATCH. Hobby+ opt in; Free
// returns false so apid's updateApp handler can surface 403
// plan_streaming_not_allowed (issue #471 AC #3). Same fail-closed
// contract as StreamingEnabled above.
func (p Plan) StreamingResponseAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.StreamingEnabled
}

// WebSocketEnabled reports whether the plan defaults the per-app
// apps.websocket_enabled column to true (issue #676 / ADR-080).
// Hobby/Pro/Scale opt in; Free stays off (the abuse-floor tier where a
// long-lived WS would pin a wake past wake_idle_timeout). The plan-level
// default is applied at CreateApp time in cmd/apid/handlers.go::buildApp
// using the WebSocketEnabled() accessor. Unknown plans fail closed
// (return false) — same contract as StreamingEnabled / MinInstancesAllowed.
func (p Plan) WebSocketEnabled() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.WebSocketEnabled
}

// WebSocketResponseAllowed reports whether the plan permits a customer
// to set apps.websocket_enabled=true via PATCH. Hobby+ opt in; Free
// returns false so apid's updateApp handler can surface 403
// plan_websocket_not_allowed (issue #676 / ADR-080 AC #3). Same
// fail-closed contract as WebSocketEnabled above.
func (p Plan) WebSocketResponseAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.WebSocketEnabled
}

// RouteMetricsEnabled reports whether the plan defaults the per-app
// apps.route_metrics_enabled column to true (ADR-093). Hobby/Pro/Scale
// opt in; Free stays off (the abuse-floor tier where per-route
// cardinality would not have a budget). The plan-level default is
// applied at CreateApp time in cmd/apid/handlers.go::buildApp using
// the RouteMetricsEnabled() accessor. Unknown plans fail closed
// (return false) — same contract as WebSocketEnabled / StreamingEnabled.
func (p Plan) RouteMetricsEnabled() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.RouteMetricsEnabled
}

// RouteMetricsResponseAllowed reports whether the plan permits a customer
// to set apps.route_metrics_enabled=true via PATCH. Hobby+ opt in; Free
// returns false so apid's updateApp handler can surface 403
// plan_route_metrics_not_allowed (ADR-093 AC #2). Same fail-closed
// contract as RouteMetricsEnabled above.
func (p Plan) RouteMetricsResponseAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.RouteMetricsEnabled
}

// RouteMetricsPerAppCap is the per-app hard cap on the number of
// distinct routes admitted into the routeLabelSet (ADR-093 D2). When
// exceeded, all new routes collapse into the reserved __route_other__
// bucket. The cap is a constant — not per-plan — because the
// cardinality bound is the same regardless of plan: any single app
// exceeding 50 distinct routes is a wildcard-shape pattern that the
// __route_other__ signal was designed to surface. Halo constant: do
// not make this per-plan without a separate ADR (the §12 budget math
// is global, not per-tenant).
const RouteMetricsPerAppCap = 50

// WarmSnapshotEnabled reports whether the plan's default for the
// per-app two-tier snapshot flag is on. Pro/Scale return true; Free /
// Hobby return false. The accessor is fail-closed — an unknown plan
// reads as false, matching the Free default. Used by buildApp in
// cmd/apid/handlers.go to populate a brand-new app's flag.
//
// Issue #470 / ADR-055: the equivalent gate ("can the customer opt in
// to warm-snapshot?") lives on WarmSnapshotAllowed (separate method
// below) so Free + Hobby PATCH-true can be rejected cleanly without
// conflating the default and the gate.
func (p Plan) WarmSnapshotEnabled() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.WarmSnapshotEnabled
}

// WarmSnapshotAllowed reports whether the plan permits a customer to
// set apps.warm_snapshot_enabled=true via PATCH. Pro/Scale return true;
// Free / Hobby return false so apid's updateApp handler can surface
// 403 plan_warm_snapshot_not_allowed. Customers on any plan may PATCH
// true → false (opt-out per-app).
func (p Plan) WarmSnapshotAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.WarmSnapshotEnabled
}

// RequireAuthnAllowed reports whether the plan permits a customer to
// set apps.require_authn=true via PATCH (issue #560). Pro/Scale return
// true; Free/Hobby return false so apid's updateApp handler surfaces
// 403 plan_require_authn_not_allowed. The global column default is
// false, so every existing app keeps being public-by-default regardless
// of plan — the gate only fires when a Free/Hobby customer tries to
// opt-in (which is denied). Customers on any plan may PATCH true →
// false (opt-out per-app).
func (p Plan) RequireAuthnAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false // fail-closed
	}
	return l.RequireAuthn
}

// AppProtocolAllowed (ADR-124 §Plan gating) reports whether the
// plan admits the given protocol value. http1 and http2 are
// universally allowed (a customer on any plan may opt-in to H2
// framing). grpc is Hobby/Pro/Scale only — Free returns false so
// apid's createApp + updateApp handlers surface 403
// plan_app_protocol_grpc_not_allowed. Out-of-set values
// (anything other than http1|http2|grpc) return false so
// apid's validation branch surfaces 400 app_protocol_invalid
// rather than letting the value reach SQL. The migration
// (00360) default is 'http1' so every pre-existing app
// continues on the legacy H1 path regardless of plan.
func (p Plan) AppProtocolAllowed(protocol string) bool {
	switch protocol {
	case "http1", "http2":
		return true
	case "grpc":
		l, ok := LimitsFor(p)
		if !ok {
			return false // fail-closed
		}
		return l.AppProtocolGrpcAllowed
	default:
		return false
	}
}

// DefaultAppProtocol (ADR-124 §Decision 1) is the value apid
// writes when the customer omits AppProtocol on create. Universal
// "http1" — no per-plan differentiation per the ADR. The closed-
// set literal is also the canonical default declared at the column
// level (NOT NULL DEFAULT 'http1' in migration 00382) so handlers
// can fall back to the SQL default rather than relying on this
// constant for the empty-string case. Declared as a package
// constant (not a Plan receiver method) because the value is
// plan-independent — every Plan returns the same thing and a
// per-plan branch would just confuse a reader about whether
// defaults vary across tiers.
const DefaultAppProtocol = AppProtocolHTTP1

// TrafficSplitAllowed reports whether the plan permits a customer to
// set a non-default traffic_percent on a deployment (issue #556).
// Pro/Scale return true; Free/Hobby return false so apid's
// createDeployment handler and the new updateDeploymentTraffic
// handler (PATCH /v1/deployments/{id}/traffic) surface 403
// plan_traffic_split_not_allowed. The migration (00160) column
// default is 100, so every existing app routes 100% to its single
// live row regardless of plan — the gate only fires when a Free/
// Hobby customer tries to opt-in (which is denied). Unknown plans
// fail closed (return false), matching the RequireAuthnAllowed
// contract above. Hobby deliberately stays locked (vs Hobby's
// unlocked MinInstancesAllowed): see the Limits.TrafficSplit
// field comment.
func (p Plan) TrafficSplitAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false // fail-closed
	}
	return l.TrafficSplit
}

// MirrorRuleAllowed reports whether the plan permits a customer to
// create a mirror_rule (issue #72 / ADR-125). Pro/Scale return
// true; Free/Hobby return false so apid's createMirrorRule + PATCH-
// mirror handlers surface 403 plan_mirror_not_allowed. The per-app
// count cap (Limits.MirrorTargetsPerApp) is enforced separately
// inside CreateMirrorRuleIfUnderQuota's FOR UPDATE lock; this
// method is the plan-level gate, not the quota gate. Hobby stays
// locked for the same cost-shape rationale as TrafficSplit: a
// mirror VM wakes for every customer request. Unknown plans fail
// closed (return false), matching the TrafficSplitAllowed contract
// above and the broader plan-gate discipline in this file.
func (p Plan) MirrorRuleAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false // fail-closed
	}
	return l.MirrorRuleAllowed
}

// RequireAuthnDefault (issue #695 / ADR-080) returns the default
// value that apid's buildApp path stamps onto a freshly created app's
// `require_authn` column when the POST body omitted the field. Per-plan
// truth table: Free=false, Hobby=true, Pro=true, Scale=true. The field
// only affects the create-time stamp — pre-flip rows keep their
// pre-flip value (the migration 00155 grandfather marks them with
// auth_default_flipped_at without flipping the column itself).
// Unknown plans fail closed (return false), matching the
// RequireAuthnAllowed contract above.
func (p Plan) RequireAuthnDefault() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false // fail-closed — column default reverts to false
	}
	return l.RequireAuthnDefault
}

// PublicAuthModeDefault (issue #695 / ADR-080) returns the default mode
// apid stamps onto a freshly created app's `public_auth_mode` column.
// Closed enum: "open" / "bearer" / "basic". Per-plan truth table:
// Free="open", Hobby="open", Pro="bearer", Scale="bearer". Hobby unlocks
// the require_authn gate but not the bearer scope (PublicAuthBearerAllowed
// above is the gate for the PATCH) — defaulting to "bearer" without an
// unlocked scope would strand the customer with a 401 they can't fix.
// Unknown plans fail closed (return "open"), mirroring the
// PublicAuthBearerAllowed contract above.
func (p Plan) PublicAuthModeDefault() string {
	l, ok := LimitsFor(p)
	if !ok {
		return AppPublicAuthModeOpen // fail-closed
	}
	return l.PublicAuthModeDefault
}

// WarmSnapshotMinRequestsDefault returns the per-plan default for the
// per-app request-count threshold. Pro/Scale: 5. Free/Hobby: 0 (the
// column default — unused because warm-snapshot is gated off).
func (p Plan) WarmSnapshotMinRequestsDefault() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.WarmSnapshotMinRequestsDefault
}

// WarmSnapshotMinMsDefault returns the per-plan default for the per-app
// time-since-first-ready threshold (ms). Pro/Scale: 2000. Free/Hobby:
// 0 (unused). Used by buildApp in cmd/apid/handlers.go.
func (p Plan) WarmSnapshotMinMsDefault() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.WarmSnapshotMinMsDefault
}

// MaxResponseBodyBytes returns the per-response body cap in bytes for
// this plan, falling back to MaxResponseBodyBytesDefault (spec §4.1's
// 25 MB) when the plan row's field is unset or the plan is unknown.
// The default is the strict spec baseline (a guest cannot exceed it);
// when limits are missing the cap clamps to that baseline rather
// than dropping to a permissive ceiling. Used by gatewayd-internal to wrap
// the response writer in http.MaxBytesWriter at this number (PR-B
// activates it on the streaming path; PR-A's buffered path stays
// under the cap naturally).
func (p Plan) MaxResponseBodyBytes() int64 {
	l, ok := LimitsFor(p)
	if !ok {
		return MaxResponseBodyBytesDefault
	}
	if l.MaxResponseBodyBytes <= 0 {
		return MaxResponseBodyBytesDefault
	}
	return l.MaxResponseBodyBytes
}

// ResponseWriteTimeout returns the per-response write timeout for this
// plan, falling back to ResponseWriteTimeoutDefault (spec §4.1's 300 s)
// when the plan row's field is unset. Same fail-closed shape as
// MaxResponseBodyBytes. Used by gatewayd-internal to configure http.Server
// .WriteTimeout so a single response cannot pin the listener.
func (p Plan) ResponseWriteTimeout() time.Duration {
	l, ok := LimitsFor(p)
	if !ok {
		return time.Duration(ResponseWriteTimeoutDefault) * time.Second
	}
	if l.ResponseWriteTimeoutSeconds <= 0 {
		return time.Duration(ResponseWriteTimeoutDefault) * time.Second
	}
	return time.Duration(l.ResponseWriteTimeoutSeconds) * time.Second
}

// TailEnabled reports whether the plan defaults the per-app
// apps.tail_enabled column to true (issue #667 / ADR-078). Every
// plan (Free/Hobby/Pro/Scale) ships with tail_enabled=true by default;
// the per-plan TailTimeoutS + ConcurrentTailsPerInstance bounds make
// the primitive safe on the abuse-floor tier. The plan-level default
// is applied at CreateApp time via buildApp so a brand-new app on
// any plan is tail-ready without an extra PATCH round-trip; an
// existing app may still disable the primitive per-app via PATCH
// (gated by TailAllowed). Unknown plans fail closed (return false) —
// same contract as StreamingEnabled above.
func (p Plan) TailEnabled() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.TailEnabled
}

// TailAllowed reports whether the plan permits a customer to set
// apps.tail_enabled=true via PATCH. All four plans return true; the
// gate exists for symmetry with StreamingResponseAllowed so a future
// abuse-floor plan can be gated out cleanly without breaking
// dependents. Unknown plans fail closed (return false).
func (p Plan) TailAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.TailEnabled
}

// TailTimeoutSeconds returns the per-task wall-clock ceiling in
// seconds for this plan, clamped up to TailTimeoutFloorSeconds (5 s)
// when the plan row's field is unset, non-positive, or below the
// floor (issue #667 / ADR-078). The clamp guarantees the reaper's
// 5 s park-watchdog (ParkTailDrainTimeoutSeconds) can never be
// shorter than the per-plan tail timeout — otherwise the watchdog
// would fire mid-task and the graceful-drain contract would be a
// lie. Unknown plans fall back to the floor (5 s). Per-plan values:
// Free 5 s, Hobby 15 s, Pro 30 s, Scale 60 s.
func (p Plan) TailTimeoutSeconds() int {
	l, ok := LimitsFor(p)
	if !ok {
		return TailTimeoutFloorSeconds
	}
	if l.TailTimeoutS < TailTimeoutFloorSeconds {
		return TailTimeoutFloorSeconds
	}
	return l.TailTimeoutS
}

// TailCapMax returns the structural per-request cap on in-flight
// waitUntil(promise) registrations. The issue pins this as a
// single source of truth (TailCapMax = 16), NOT a per-plan matrix —
// the accessor returns the structural constant regardless of the
// plan row's field, matching the issue's "structural constant in
// pkg/api/limits.go" framing. The runner enforces this before any
// BumpInstanceTailCount call; over-cap attempts emit the
// tailCapReached metric and log wake.tail_failed{reason=cap_reached}.
func (p Plan) TailCapMax() int {
	return TailCapMax
}

// ConcurrentTailsPerInstance returns the per-plan cap on in-flight
// tails across all in-flight requests for one instance (issue #667).
// Distinct from TailCapMax (per-request); this is per-instance, not
// per-call. The runner enforces it locally with a mutex before any
// BumpInstanceTailCount call. Per-plan values: Free 4, Hobby 16,
// Pro 64, Scale 256. Unknown plans fail closed (return 0).
func (p Plan) ConcurrentTailsPerInstance() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.ConcurrentTailsPerInstance
}

// CronLimitPerApp returns the per-app cron cap for the plan (spec §4.4).
// 0 for Free (the handler returns 402 ErrPlanCronsNotAllowed before
// the store is touched) and a positive value for Hobby/Pro/Scale.
// Unknown plans fail closed (return 0) — same contract as
// EgressAllowlistMaxSize above.
func (p Plan) CronLimitPerApp() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.CronLimitPerApp
}

// CronLimitPerAccount returns the per-account cron cap for the plan
// (spec §4.4). Independent of CronLimitPerApp — defends against the
// N-apps-times-cap-per-app bypass. 0 for Free; positive for paid
// tiers. Unknown plans fail closed (return 0) — same contract as
// CronLimitPerApp above.
func (p Plan) CronLimitPerAccount() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.CronLimitPerAccount
}

// CorsPresetsPerAccount returns the per-account CORS preset cap
// (issue #975 item #4 / Mega-Foundation #979-b, slot 00294).
// Free=0 (Free is the abuse-floor tier; the abstraction is the
// upsell — Free customers stay on inline kind=cors rules). Paid
// tiers: Hobby 10, Pro 50, Scale 250. Unknown plans fail closed
// (return 0). PR-B (#979-c, slot 00295) reads this in
// apid-Validate's CreateCorsPreset handler.
func (p Plan) CorsPresetsPerAccount() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.CorsPresetsPerAccount
}

// CorsPresetsPerApp returns the per-app CORS preset cap.
// Independent of CorsPresetsPerAccount — a customer with N apps
// can split the per-account budget across the apps without
// tripping the per-account cap. Per-plan: Free 0, Hobby 5, Pro
// 15, Scale 50. Same fail-closed contract as
// CorsPresetsPerAccount.
func (p Plan) CorsPresetsPerApp() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.CorsPresetsPerApp
}

// CorsPresetMaxOrigins returns the per-preset cap on
// cors_presets.allow_origins entries. The gateway's per-request
// allowlist walk is O(AllowOrigins), so the cap defends against
// a customer shipping a 10k-entry wildcard collection. Per-plan:
// Free 0, Hobby 25, Pro 100, Scale 500. apid-Validate reads this
// before INSERT (PR-B).
func (p Plan) CorsPresetMaxOrigins() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.CorsPresetMaxOrigins
}

// CorsPresetMaxAllowMethods returns the per-preset cap on
// cors_presets.allow_methods entries. The closed-set ceiling is
// constant across plans (8 in the current ADR-091 D12 enum);
// the value here is the read-side cap a preset can ship, not
// the closed-set size. apid-Validate reads this in PR-B.
func (p Plan) CorsPresetMaxAllowMethods() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.CorsPresetMaxAllowMethods
}

// CorsPresetMaxNameLength pins the upper bound on
// cors_presets.name. The migration's CHECK caps it at 64; this
// accessor surfaces the same value so the apid writer can
// reject before INSERT. Same value across plans.
func (p Plan) CorsPresetMaxNameLength() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.CorsPresetMaxNameLength
}

// ConsumerKeysPerApp (ADR-120 / issue #975 item #5) returns the
// per-app cap on consumer_keys rows. The cap defends against a
// customer pinning one consumer key per customer-of-customer
// and inflating the gateway-side (app_id, prefix) hot-path
// lookup. Per-plan: Free 0, Hobby 100, Pro 100, Scale 1000. Free is gated off
// entirely (CodeConsumerKeysNotAllowed).
// Same 100-per-app floor across the lower three tiers — the
// cardinality budget per app is a tenant-stability concern,
// not a tier-upgrade lever. Unknown plans fail closed (return
// 0) so a missing plan row never silently unlocks the
// primitive.
func (p Plan) ConsumerKeysPerApp() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.ConsumerKeysPerApp
}

// ConsumerKeysPerAccount (ADR-120 / issue #975 item #5) returns
// the per-account cap on consumer_keys rows across all of the
// account's apps. Independent of ConsumerKeysPerApp so a
// customer with N apps under one account can split the
// per-account budget. Per-plan: Free 0, Hobby 250, Pro 2500,
// Scale 25000. Same fail-closed contract as ConsumerKeysPerApp.
func (p Plan) ConsumerKeysPerAccount() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.ConsumerKeysPerAccount
}

// OpenAPIDocsPerDeployment (ADR-122 / issue #975 item #1) returns
// the per-deployment cap on captured OpenAPI docs. The schema
// is 1 row per deployment (PRIMARY KEY on deployment_id), so the
// per-deployment cap is effectively 1 across all paid plans.
// Free returns 0 — the apid PATCH handler returns 402
// CodePlanOpenAPIDocsNotAllowed before the store is touched.
// Unknown plans fail closed (return 0) — same contract as
// ConsumerKeysPerApp.
func (p Plan) OpenAPIDocsPerDeployment() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.OpenAPIDocsPerDeployment
}

// OpenAPIDocMaxBytes (ADR-122 / issue #975 item #1) returns the
// per-plan byte cap on one captured / uploaded OpenAPI doc.
// Per-plan: Free 0 (irrelevant), Hobby/Pro/Scale 131072 (the
// global cap state.OpenAPIDocMaxBytes). The apid PATCH handler
// validates against this value and returns 413
// CodePlanOpenAPIDocTooLarge on overflow. Unknown plans fail
// closed (return 0) — same contract as ConsumerKeysPerApp.
func (p Plan) OpenAPIDocMaxBytes() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.OpenAPIDocMaxBytes
}

// OpenAPIDocsPerAccount (ADR-122 / issue #975 item #1) returns
// the per-account cap on captured OpenAPI docs across all of
// the account's deployments. Per-plan: Free 0, Hobby 100, Pro
// 1000, Scale 10000. The apid PATCH handler enforces via
// Store.CountOpenAPIDocsByAccount (the count is computed
// server-side, not by walking the row set). Independent of
// OpenAPIDocsPerDeployment — the per-account ceiling is reached
// before the per-deployment ceiling on a typical multi-app
// customer. Unknown plans fail closed (return 0) — same
// contract as ConsumerKeysPerApp.
func (p Plan) OpenAPIDocsPerAccount() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.OpenAPIDocsPerAccount
}

// OpenAPIImportsPerAccount (ADR-126 / issue #975 item #2)
// returns the per-account cap on imported OpenAPI docs
// across all of the account's apps. Per-plan: Free 100, Hobby
// 1000, Pro 10000, Scale 10000. The apid POST
// /v1/apps/{slug}/openapi handler enforces via
// Store.CountOpenAPIImportsByAccount; 403 when the cap is
// reached. Note: per-app the import is overwrite-not-multi-
// version (one row per app_id, primary-key shape), so the
// per-account cap is the load-bearing defensive cap against
// throwaway rows. Unknown plans fail closed (return 0) —
// same contract as OpenAPIDocsPerAccount.
func (p Plan) OpenAPIImportsPerAccount() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.OpenAPIImportsPerAccount
}

// EvictionPriorityReservedAllowed (issue #475) returns true if the plan
// may opt apps into the reserved eviction tier. Free = false; Hobby+ =
// true. apid's updateApp handler rejects a `reserved` PATCH on a Free
// plan with 403 plan_eviction_priority_reserved_not_allowed. The
// field is always set-able to 'best_effort' (the default) regardless
// of plan — only the reserved opt-in is gated. Unknown plans fail
// closed (return false) — same contract as WarmSnapshotAllowed above.
func (p Plan) EvictionPriorityReservedAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.EvictionPriorityReservedAllowed
}

// PublicAuthBearerAllowed (issue #477 / ADR-079) returns true if the
// plan may opt apps into public_auth_mode='bearer'. Free = false
// (Free apps stay public-by-default — no-signup friction); Hobby+ =
// true. apid's updateApp handler rejects a 'bearer' PATCH on a Free
// plan with 402 plan_public_auth_bearer_not_allowed. The 'open'
// mode is always available regardless of plan — only the bearer /
// basic opt-in is gated. Unknown plans fail closed (return false)
// — same contract as EvictionPriorityReservedAllowed above.
func (p Plan) PublicAuthBearerAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.PublicAuthBearerAllowed
}

// PublicAuthBasicAllowed (issue #477 / ADR-079) returns true if the
// plan may opt apps into public_auth_mode='basic'. Free + Hobby =
// false (basic adds sealed-credential storage cost the lower tiers
// don't need; bearer covers the Hobby admin-endpoint use case);
// Pro+ = true. apid's updateApp handler rejects a 'basic' PATCH on a
// Free/Hobby plan with 402 plan_public_auth_basic_not_allowed. The
// 'open' mode is always available regardless of plan. Unknown
// plans fail closed (return false) — same contract as the other
// accessors above.
func (p Plan) PublicAuthBasicAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.PublicAuthBasicAllowed
}

// ReservedConcurrencyPerAccount (issue #475) returns the per-account
// cap on apps with eviction_priority='reserved'. Free 0; Hobby 1; Pro
// 2; Scale 4. Counts APPS (not live instances). Unknown plans fail
// closed (return 0) — same contract as CronLimitPerAccount above.
func (p Plan) ReservedConcurrencyPerAccount() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.ReservedConcurrencyPerAccount
}

// KeysMax returns the per-account API-key cap for the plan
// (issue #189 / IAM-5). Free 3, Hobby 10, Pro 50, Scale 200. The
// handler enforces the cap at createKey (409
// api_key_limit_exceeded); rotateKey is quota-neutral and is allowed
// at the cap. Revoked keys (status='revoked') are excluded from the
// count so the customer's historical lineage doesn't pin them out of
// quota. Unknown plans fail closed (return 0) — same contract as
// CronLimitPerAccount above.
func (p Plan) KeysMax() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.KeysMax
}

// AlertRuleLimitPerApp returns the per-app alert-rule cap for the
// plan (issue #396 / ADR-045). 0 for Free (handler returns 402
// CodePlanAlertRulesNotAllowed before the store is touched);
// positive for Hobby/Pro/Scale. Account-wide rules (AppID == "")
// bypass this; only the per-account cap applies. Same fail-closed
// contract as CronLimitPerApp.
func (p Plan) AlertRuleLimitPerApp() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.AlertRuleLimitPerApp
}

// AlertRuleLimitPerAccount returns the per-account alert-rule cap.
// Independent of AlertRuleLimitPerApp — same N-apps-times-cap-per-app
// defence the cron shape used. Same fail-closed contract as
// CronLimitPerAccount.
func (p Plan) AlertRuleLimitPerAccount() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.AlertRuleLimitPerAccount
}

// AlertPresetCatalogLimitPerAccount (issue #1233 / ADR-123) returns
// the informational count of catalog rows visible to the plan.
// Currently 8 across every plan — the alert_presets catalog is
// system-seeded and not plan-tier conditional (the per-row
// `minimum_plan` column is what gates individual presets; the
// catalog row count is a single global figure). Surfaced so the
// CLI / dashboard can render "8 presets available" without
// hardcoding the seed count. Unknown plans fail closed (return 0).
func (p Plan) AlertPresetCatalogLimitPerAccount() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.AlertPresetCatalogLimitPerAccount
}

// WebhookPerApp returns the per-app outbound-webhook subscription cap
// for the plan (issue #476 / ADR-076). 0 for Free (the handler
// returns 402 CodePlanWebhooksNotAllowed before the store is touched);
// positive for Hobby/Pro/Scale. Same fail-closed contract as
// AlertRuleLimitPerApp.
func (p Plan) WebhookPerApp() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.WebhookPerApp
}

// WebhookPerAccount returns the per-account outbound-webhook
// subscription cap. Independent of WebhookPerApp — same
// N-apps-times-cap-per-app defence. Same fail-closed contract as
// AlertRuleLimitPerAccount.
func (p Plan) WebhookPerAccount() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.WebhookPerAccount
}

// TriggersAllowed (issue #757 / ADR-0NN) returns true if the plan
// may create triggers at all. Free = false; Hobby/Pro/Scale =
// true. apid's createTrigger handler rejects a POST on a Free
// plan with 402 CodePlanTriggersNotAllowed BEFORE loadApp so a
// Free customer posting to a non-existent slug gets the upsell
// instead of a 404 that would leak the slug's existence (the
// same PR-review finding F4 mirrored from createAlertRule +
// createAppWebhook). Unknown plans fail closed (return false) —
// same contract as CronLimitPerApp above.
func (p Plan) TriggersAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.TriggersAllowed
}

// TriggerLimitPerApp returns the per-app trigger cap for the plan
// (issue #757 / ADR-0NN). 0 for Free (the handler returns 402
// ErrPlanTriggersNotAllowed before the store is touched) and a
// positive value for Hobby/Pro/Scale. Unknown plans fail closed
// (return 0) — same contract as CronLimitPerApp above.
func (p Plan) TriggerLimitPerApp() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.TriggerLimitPerApp
}

// TriggerLimitPerAccount returns the per-account trigger cap for
// the plan. Independent of TriggerLimitPerApp — defends against
// the N-apps-times-cap-per-app bypass. 0 for Free; positive for
// paid tiers. Unknown plans fail closed (return 0) — same
// contract as TriggerLimitPerApp above.
func (p Plan) TriggerLimitPerAccount() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.TriggerLimitPerAccount
}

// TriggerBatchSizeMax returns the per-plan ceiling on
// `batch_size_max`. Hobby 50 / Pro 500 / Scale 5000 (the SQL
// CHECK ceiling). The apid createTrigger handler reads both
// this AND the SQL CHECK so a Hobby customer asking for 200 gets
// 403 trigger_batch_size_too_large with the Hobby cap in the
// body, not a 422 from the SQL constraint. Unknown plans fail
// closed (return 0) — same contract as CronLimitPerApp above.
func (p Plan) TriggerBatchSizeMax() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.TriggerBatchSizeMax
}

// TriggerBatchWindowMaxSec returns the per-plan ceiling on
// `batch_window_ms / 1000`. Hobby 30 s / Pro 300 s / Scale 300 s.
// The SQL CHECK (10 ms – 600 s) is the hard ceiling; this plan
// cap stops Hobby from holding 10-minute windows the 50-size
// cap doesn't justify. Unknown plans fail closed (return 0).
func (p Plan) TriggerBatchWindowMaxSec() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.TriggerBatchWindowMaxSec
}

// TriggerMaxAttemptsMax returns the per-plan ceiling on
// `max_attempts`. Hobby 3 / Pro 10 / Scale 25 — the upper
// bound mirrors the queue-attempts pattern (per MaxQueueAttempts
// in pkg/state/pgstore.go). Unknown plans fail closed (return 0).
func (p Plan) TriggerMaxAttemptsMax() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.TriggerMaxAttemptsMax
}

// TriggerRecordsPerSecondPerApp returns the per-app
// steady-state dispatch rate schedd permits to a single trigger.
// The cap is enforced by WakeRateLimiter (pkg/sched/rate_limit.go)
// — the same bucket the wake path drains. Hobby 100 / Pro 1000
// / Scale 10 000. Above the cap, records transition to
// dead_letter with reason='rate_limited' rather than
// back-pressuring the broker. Unknown plans fail closed
// (return 0).
func (p Plan) TriggerRecordsPerSecondPerApp() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.TriggerRecordsPerSecondPerApp
}

// MaxESMSourcesPerApp returns the per-app cap on EventSourceMapping
// sources, surfaced under the operator-facing `max_esm_sources_per_app`
// alias on GET /v1/plans/{slug}/limits (ADR-118 / issue #757
// closure, commit 3 of 11). Mirrors TriggerLimitPerApp exactly
// (Free 0 / Hobby 2 / Pro 10 / Scale 50). The runtime admission
// path reads TriggerLimitPerApp — this getter exists for the
// dashboard's dual-emit label only. Unknown plans fail closed
// (return 0) — same contract as TriggerLimitPerApp above.
func (p Plan) MaxESMSourcesPerApp() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.MaxESMSourcesPerApp
}

// MaxESMRecordsPerSecond returns the per-app steady-state ESM
// dispatch rate, surfaced under the operator-facing
// `max_esm_records_per_second` alias (ADR-118). Mirrors
// TriggerRecordsPerSecondPerApp exactly (0 / 100 / 1000 / 10000).
// Unknown plans fail closed (return 0).
func (p Plan) MaxESMRecordsPerSecond() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.MaxESMRecordsPerSecond
}

// BrokerEgressMbit returns the per-app broker-egress cap in
// megabits per second (ADR-118 / commit 8 of 11). The cap is
// enforced via the faas-brokerq.slice cgroup + tc commands
// (pkg/sched/broker_egress.go + pkg/sched/broker_egress_linux.go).
// Hobby 10 / Pro 50 / Scale 200. 0 for Free (gated off via
// TriggersAllowed=false). Unknown plans fail closed (return 0).
func (p Plan) BrokerEgressMbit() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.BrokerEgressMbit
}

// TLSSkipVerifyAllowed reports whether the customer's plan
// permits the `tls.skip_verify=true` flag on KafkaConfig
// (ADR-118 / commit 2 of 11). Hobby=false (a Hobby customer's
// plaintext-TLS path doesn't justify the weakened-verification
// posture). Pro / Scale = true. Free = false (gated off).
// The apid createTrigger handler reads this via
// Plan.TLSSkipVerifyAllowed() and rejects skip_verify=true on
// Hobby with 403 trigger_tls_skip_verify_not_allowed. Unknown
// plans fail closed (return false) — same contract as
// TriggersAllowed().
func (p Plan) TLSSkipVerifyAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.TLSSkipVerifyAllowed
}

// TrustedSignerCountMax returns the per-app cosign trusted-publisher
// cap for the plan (issue #472 / ADR-054). 0 for Free (the open-deploy
// posture for Free means customers on Free never need require_signed=true
// and so never need signers either); positive for Hobby/Pro/Scale.
// Unknown plans fail closed (return 0) — same contract as the cron +
// alert-rule getters above.
func (p Plan) TrustedSignerCountMax() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.TrustedSignerCountMax
}

// MaxMinInstances returns the per-plan cap on the per-app
// cold-wake floor (issue #557 / ADR-071). Free 0, Hobby 1, Pro 3,
// Scale 10. The apid updateApp handler rejects values above this
// with CodeMaxMinInstancesExceeded (422) carrying the limit + the
// observed value + a docs URL — the CLI renders the rejection
// with actionable retry guidance. Unknown plans fail closed
// (return 0) — same contract as TrustedSignerCountMax.
func (p Plan) MaxMinInstances() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.MaxMinInstances
}

// ConcurrencyPerVMBound returns the platform-advertised upper
// bound on concurrent in-flight requests one VM can handle at the
// listener layer (issue #559). Free 1, Hobby 5, Pro 25, Scale 80.
// Distinct from MaxConcurrency (the per-app *instance* cap, spec
// §6.2-1) — this is per-VM. Unknown plans fail closed (return 0) —
// same contract as MaxMinInstances above.
func (p Plan) ConcurrencyPerVMBound() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.ConcurrencyPerVMBound
}

// LogDeploymentFilterMax returns the per-plan cap on the
// `?deployment=` filter the customer may scope their app-logs
// stream to (issue #517 / PR-B, AC3). Free returns 0 so the
// handler rejects with `plan_deployment_filter_not_allowed`; paid
// tiers return 1 / 10 / 50 (Hobby/Pro/Scale). Unknown plans fail
// closed (return 0) — same contract as CronLimitPerApp above.
func (p Plan) LogDeploymentFilterMax() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.LogDeploymentFilterMax
}

// OrgMembersMax returns the per-non-personal-org member cap for the
// plan (issue #190 / IAM-6 / ADR-061). 0 for unknown plans — the
// fail-closed contract mirrors CronLimitPerApp above. The handler
// gates membership creation on this accessor and surfaces 403
// org_member_cap_exceeded once the cap is reached; the store still
// checks the same value as a defence-in-depth back-stop. PR 1 ships
// 0 for every plan; PR 2 sets the actual per-plan values from the
// financial model.
func (p Plan) OrgMembersMax() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.OrgMembersMax
}

// OrgPendingInvitationsMax returns the per-non-personal-org pending
// invitation cap for the plan (issue #190 / IAM-6 / ADR-061).
// Independent of OrgMembersMax — defends against the N-invites ×
// fast-accept botnet signature. Same fail-closed contract as
// OrgMembersMax above.
func (p Plan) OrgPendingInvitationsMax() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.OrgPendingInvitationsMax
}

// RateLimitPerAccountRPM returns the per-account requests/minute cap for
// the plan (ADR-040 / issue #292). Independent of RateLimitRPS/Burst
// which are per-app — defends against the N-apps-times-cap-per-app
// botnet signature. 0 for unknown plans (fail closed; the limiter math
// then returns zero rps and zero burst, refusing all traffic) — same
// contract as CronLimitPerAccount above.
func (p Plan) RateLimitPerAccountRPM() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.RateLimitPerAccountRPM
}

// ScaleUpTargetRPSAllowed reports whether the plan may set
// `autoscale_target_rps` (issue #169 / #172). Hobby + Pro + Scale opt
// in; Free stays off. apid's updateApp handler gates `req.AutoscaleTargetRPS`
// on this and surfaces the rejection with CodePlanScaleUpNotAllowed.
// Unknown plans fail closed (return false) — same contract as
// MinInstancesAllowed / EgressAllowlistAllowed.
func (p Plan) ScaleUpTargetRPSAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.ScaleUpTargetRPSAllowed
}

// ScaleUpTargetCPUAllowed reports whether the plan may set
// `autoscale_target_cpu_pct`. Pro + Scale opt in; Free + Hobby stay
// off (cost shape of "scale on CPU without a min_instances floor"
// is unbounded on the cheaper tiers). Same fail-closed contract as
// ScaleUpTargetRPSAllowed above.
func (p Plan) ScaleUpTargetCPUAllowed() bool {
	l, ok := LimitsFor(p)
	if !ok {
		return false
	}
	return l.ScaleUpTargetCPUAllowed
}

// SliceName returns the systemd sub-slice name for this plan. The
// 3-level cgroup hierarchy (issue #301 / ADR-044) is
//
//	/sys/fs/cgroup/faas-tenant.slice/<sliceName>/<instance>
//
// systemd drops the per-plan sub-slice at boot via
// deploy/ansible/roles/systemd_slices (4 copies: free/hobby/pro/scale);
// the jailer expects the parent dir to exist before it creates the
// per-instance scope. The slice name is the canonical form
// "tenant-<plan>" — it carries the "tenant-" prefix so the
// faas.rules.yml `slice=~"tenant-.*"` matcher stays stable for any
// future tenant-customer slice hierarchy. Unknown plans return the
// empty string so call sites fail closed (jailer will reject a zero
// parent cgroup path) rather than silently writing the wrong scope.
func (p Plan) SliceName() string {
	switch p {
	case PlanFree:
		return "tenant-free"
	case PlanHobby:
		return "tenant-hobby"
	case PlanPro:
		return "tenant-pro"
	case PlanScale:
		return "tenant-scale"
	default:
		return ""
	}
}

// CPUWeight returns the kernel cpu.weight value for the plan, used as
// the jailer `--cgroup cpu.weight=N` argv (issue #301 / ADR-044). The
// ratio 2:4:8:16 (Free:Hobby:Pro:Scale) ensures a Scale-customer
// burst can preempt a Free-customer burst but never starves them out of
// their weight. Unknown plans fail closed (return 100 — the kernel
// default) so a missing Limits row never silently disables the cgroup
// weight; the cpu.max quota still bounds the impact.
func (p Plan) CPUWeight() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 100
	}
	return l.CPUWeight
}

// CPUQuotaUS returns the cpu.max quota half (microseconds) for the
// plan — written directly to the per-instance cpu.max file. Issue
// #301 spec: Free 100ms / Hobby 200ms / Pro 500ms / Scale 1000ms.
// Unknown plans fail closed (return 0 — disabled quota, which the
// kernel treats as "no limit", so a misconfigured plan is detectable
// in dashboards rather than silently denied).
func (p Plan) CPUQuotaUS() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 0
	}
	return l.CPUQuotaUS
}

// CPUPeriodUS returns the cpu.max period half (microseconds) for the
// plan. Always equal to CPUQuotaUS for the issue #301 spec — the
// potential quota is "<period> microseconds per <period>". Unknown
// plans fail closed (return 100_000 — the standard default period),
// which then makes the quota half easy to reason about even when the
// row is missing.
func (p Plan) CPUPeriodUS() int {
	l, ok := LimitsFor(p)
	if !ok {
		return 100_000
	}
	return l.CPUPeriodUS
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

// SnapshotMemoryMaxMB returns the temporary per-instance memory.max used for
// a full Firecracker snapshot. Callers must restore BillableRAMMB(ramMB) as
// soon as the snapshot request and export path finish.
func SnapshotMemoryMaxMB(ramMB int) int {
	return ramMB + PerVMOverheadMB + SnapshotVMOverheadMB
}

// BuilderMemoryMaxMB returns the creation-time cgroup fence for a builder VM.
// Builders are charged to faas-cp-build.slice rather than tenant RAM, and
// need a larger host-side RSS allowance than ordinary app VMs.
func BuilderMemoryMaxMB(ramMB int) int {
	return ramMB + BuildVMOverheadMB
}

// BillableRAMMBWithSidecars is the sidecar-shape variant of
// BillableRAMMB (issue #463 / ADR-070 §Decision 6). The billable
// shutter is `plan.RAMMB + Σ(sidecar.ram_mb) + PerVMOverheadMB`:
// sidecars share the per-VM overhead (one netns, one cgroup
// scope per instance), but each sidecar contributes its own
// RAM to the admission ceiling. Caller is responsible for
// enforcing the SidecarCapMax bounds — this helper is purely
// the arithmetic.
//
// PR-A defines the math; PR-B wires the consumer (schedd's
// admission ledger + meterd's sampler). The sibling helper
// (no-sidecars form) is BillableRAMMB — both shapes coexist.
// A future cleanup can fold the single-arg form into this
// helper as a variadic / empty-slice overload, but for PR-A
// the two-form separation keeps the no-sidecar call sites
// unambiguous.
func BillableRAMMBWithSidecars(ramMB int, sidecarMBs []int) int {
	total := ramMB + PerVMOverheadMB
	for _, m := range sidecarMBs {
		if m > 0 {
			total += m
		}
	}
	return total
}

// IdleTimeoutBounds returns the [floor, ceiling] seconds a customer may configure
// their idle timeout to for this plan (spec §4.3).
func (l Limits) IdleTimeoutBounds() (floor, ceiling int) {
	return IdleTimeoutFloorSeconds, l.IdleTimeoutS * IdleTimeoutMaxMultiple
}

// Obs admin pagination (issue #777 / ADR-091). The operator surface
// differs from the customer surface (pkg/api/paging.go defaults to
// 25/100) because the operator UI is fleet-wide and a single page
// often renders a region of the table at a glance. 200 is the
// default; 500 is the hard cap; anything above is silently clamped
// to the cap so a misconfigured operator client cannot OOM the apid
// daemon. Documented in the ADR (§"Pagination caps are global
// constants") so the operator-vs-customer divergence is explicit.
const (
	ObsAdminPaginationDefault = 200
	ObsAdminPaginationMax     = 500

	// ObsAdminWindowMaxHours bounds the ?since= window on time-series
	// operator queries. 168h = 7d is the longest the operator can scan
	// without breaking the read-amplification budget; longer windows
	// are a deliberate future concern (anomaly detection moves to
	// PromQL when the control plane goes multi-node, ADR-091 §6).
	ObsAdminWindowMaxHours = 168

	// ObsAdminAnomalyLimitDefault / ObsAdminAnomalyLimitMax bound the
	// top-N size of /v1/admin/obs/anomalies (ADR-091 §3.6 / PR #2).
	// Default 50 keeps the dashboard tile to one screen; cap 200
	// matches the upper bound the underlying CTE handles without
	// spilling to disk.
	ObsAdminAnomalyLimitDefault = 50
	ObsAdminAnomalyLimitMax     = 200

	// ObsAdminRateLimitLimitDefault / ObsAdminRateLimitLimitMax bound
	// the top-N size of /v1/admin/obs/rate-limits (ADR-091 §3.5 /
	// PR #2). Default 100 covers the "show me everyone over budget"
	// tile; cap 500 = ObsAdminPaginationMax for parity with the rest
	// of the operator surface.
	ObsAdminRateLimitLimitDefault = 100
	ObsAdminRateLimitLimitMax     = ObsAdminPaginationMax

	// AppErrorsWindowMaxHours (ADR-096) bounds the ?since= window
	// on /v1/apps/{slug}/errors/summary. Mirrors ObsAdminWindowMaxHours
	// (168h = 7d) — the customer view is narrower in the dashboard
	// UI but the storage backend can serve the same window. The
	// summary endpoint clamps ?since=/?until= to this bound; longer
	// windows are silently clamped and the response sets
	// window_clamped=true.
	AppErrorsWindowMaxHours = 168

	// AppErrorsSummaryDefaultLimit / AppErrorsSummaryMaxLimit bound
	// ?limit= on GET /v1/apps/{slug}/errors/summary. 20 default =
	// "show me the top 20 errors" (matches the Sentry-default
	// grouping view the customer-facing dashboard renders); 100
	// max caps the scan so a misconfigured caller cannot blow the
	// row-budget. Past 100 the caller pages via ?cursor.
	AppErrorsSummaryDefaultLimit = 20
	AppErrorsSummaryMaxLimit     = 100

	// AppErrorsDedupeWindowSeconds (ADR-096) is the platform-wide
	// dedupe window for the IncrementAppError INSERT. NOT a
	// per-plan constant — it is a system-wide setting
	// (FAAS_APP_ERRORS_DEDUPE_WINDOW_SECONDS env var). 3600s = 1h
	// default; raising it inflates the on-disk row count per
	// fingerprint; lowering it increases the dedupe rate. Pinned
	// here so the limits-lint gate accepts the constant.
	AppErrorsDedupeWindowSeconds = 3600

	// AppErrorsSampleMessageCapBytes (ADR-096) is the hard cap on
	// the sample_message column at the writer. The
	// redact.Redactor truncates at this bound BEFORE INSERT; the
	// pg_column_size CHECK on the column is the backstop. 512 bytes
	// matches the existing app_log archive precedent — half a KiB
	// is enough to carry a 1-2 sentence error description with a
	// stack-trace snippet, and small enough to keep the index
	// narrow.
	AppErrorsSampleMessageCapBytes = 512

	// AppErrorsCardinalityBackstopMultiplier (ADR-096) is the
	// multiplier applied to AppErrorsMaxFingerprintsPerApp() when
	// computing the recorder's LRU cache cap. The recorder uses
	// this so the cache can absorb a brief burst above the plan
	// cap before the drop kicks in. 2x = the cache holds up to
	// 2x the steady-state cap; the cache evicts oldest entries
	// beyond that.
	AppErrorsCardinalityBackstopMultiplier = 2

	// ObsAdminAuditLogLimitDefault / ObsAdminAuditLogLimitMax bound
	// the top-N size of /v1/admin/obs/audit-log/search (ADR-091 §3.7 /
	// PR #3). Default 200 covers the operator's "what happened in
	// the last hour" drill-down; cap 500 = ObsAdminPaginationMax
	// for parity with the rest of the operator surface. The underlying
	// store is bounded by an over-read on the same idiom as
	// listAuditLogOverRead (cmd/apid/handlers_audit_log.go:67).
	ObsAdminAuditLogLimitDefault = 200
	ObsAdminAuditLogLimitMax     = ObsAdminPaginationMax

	// ObsAdminEventsLimitDefault / ObsAdminEventsLimitMax bound the
	// top-N size of /v1/admin/obs/events (ADR-091 §3.7 / PR #3).
	// Same shape as the audit-log search: default 200, cap 500. The
	// events table is append-only with no retention pruning today
	// so the over-read budget is also bounded by the
	// (kind, at DESC) index added by 00190_admin_obs_index.sql.
	ObsAdminEventsLimitDefault = 200
	ObsAdminEventsLimitMax     = ObsAdminPaginationMax
)

// End-to-end request budgets (ADR-093). The platform enforces a
// wall-clock budget on every customer-facing request and propagates
// the remaining time to every downstream call (DB, gRPC, outbound
// HTTP). The values here are the *defaults* — per-route overrides
// live on the edge-rule kind=budget JSON document, and per-plan
// overrides live on Limits.RequestBudgetMs (added below).
//
// Source of truth: pkg/reqbudget re-exports these as reqbudget.*
// so call-sites can use one import.
const (
	// RequestBudgetDefault is the per-request wall-clock budget the
	// gatewayd-public BudgetMiddleware installs when no edge-rule
	// kind=budget matches. 3 s matches the example in the user's
	// feature ask ("POST /payment → 3 s"). A misconfigured
	// deployment that wants a tighter or looser default can override
	// per-route via kind=budget, or per-plan via the
	// Limits.RequestBudgetMs accessor.
	RequestBudgetDefault = 3 * time.Second
	// RequestBudgetMax is the absolute upper bound on any per-request
	// budget. Defends against a misconfiguration that would re-pin a
	// 300 s stdlib WriteTimeout as the request budget. Per-plan max
	// lives on Limits.RequestBudgetMaxMs; 0 falls back here.
	RequestBudgetMax = 30 * time.Second
	// RequestBudgetApidDefault is the apid-side default budget.
	// apid serves dashboards + admin + sync-invoke long-polls that
	// are already capped at 910 s upstream (fwdStream) so 5 s is
	// the floor, not the ceiling; per-call context.WithTimeout calls
	// in handlers continue to enforce their own sub-ceilings
	// (EdgeRuleJWTVerifyTimeoutDefault, dashboard 3s, billing 30s,
	// sync-invoke 5-30s).
	RequestBudgetApidDefault = 5 * time.Second
	// RequestBudgetDefaultOverrideHeader is the platform-wide
	// default header name a kind=budget rule's AllowOverrideHeader
	// resolves to when the rule leaves the field empty. Customers
	// can override per-request by sending
	// `x-faas-budget-ms: 3000` on the inbound request — the value
	// is parsed as a positive integer milliseconds and replaces
	// the static rule's BudgetMs for that single request, subject
	// to the per-plan RequestBudgetMaxDuration ceiling. A
	// per-rule AllowOverrideHeader takes precedence over this
	// default when set.
	RequestBudgetDefaultOverrideHeader = "x-faas-budget-ms"

	// DefaultOverheadDB is the per-hop reservation for a local PG
	// round-trip. Reservation, not measurement — it ensures a
	// downstream call starts with at most (parentRemaining - 10 ms)
	// even before its own work begins. Sized for local control
	// plane; cross-region deployments may need larger reservations
	// in a follow-up.
	DefaultOverheadDB = 10 * time.Millisecond
	// DefaultOverheadGRPC is the per-hop reservation for a local
	// vmmd gRPC call. Same shape as DefaultOverheadDB — reservation,
	// not measurement.
	DefaultOverheadGRPC = 5 * time.Millisecond
	// DefaultOverheadHTTP is the per-hop reservation for an outbound
	// HTTP call (e.g. the public→internal RoundTrip).
	DefaultOverheadHTTP = 20 * time.Millisecond
	// DefaultOverheadStream is the per-hop reservation for a
	// streaming first-byte ack. Larger than DB/gRPC because the
	// first-byte acknowledgment involves one extra round-trip vs
	// the unary case.
	DefaultOverheadStream = 50 * time.Millisecond
	// DefaultOverheadQueue is the per-hop reservation for a wake /
	// enqueue / poll call. Small because these are local ops.
	DefaultOverheadQueue = 5 * time.Millisecond
)

// RequestBudget returns the wall-clock deadline the platform
// installs on customer-facing requests for this plan, falling back
// to RequestBudgetDefault when the per-plan field is unset. The
// returned duration is clamped to [1, RequestBudgetMax] so a
// misconfigured Limits row cannot pin the budget to zero or to a
// value larger than RequestBudgetMax. Per-route edge-rule
// kind=budget overrides still take precedence at request time —
// this accessor is the *baseline* the middleware starts from.
//
// ADR-093.
func (l Limits) RequestBudget() time.Duration {
	d := time.Duration(l.RequestBudgetMs) * time.Millisecond
	if d <= 0 {
		d = RequestBudgetDefault
	}
	if d > RequestBudgetMax {
		d = RequestBudgetMax
	}
	return d
}

// RequestBudgetMaxDuration returns the absolute upper bound for any
// per-request budget on this plan. 0 falls back to
// RequestBudgetMax. Per-plan override clamps to [RequestBudget(),
// 5 * RequestBudgetMax] so a customer with a 5 s plan default
// cannot accidentally configure a 100 ms max (which would force the
// default down to 100 ms).
//
// ADR-093.
func (l Limits) RequestBudgetMaxDuration() time.Duration {
	d := time.Duration(l.RequestBudgetMaxMs) * time.Millisecond
	if d <= 0 {
		d = RequestBudgetMax
	}
	if d < l.RequestBudget() {
		d = l.RequestBudget()
	}
	return d
}
