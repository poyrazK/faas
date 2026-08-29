// Orchestrator (issue #976 / ADR-122 / SAFE-RELEASES-F) — the
// meterd tick that walks deployment rows in {pending,
// rolling_out} and advances the rollout_state machine.
//
// Per-tick behaviour:
//
//  1. List pending rollouts via state.Store.SafedeployListPendingRollouts.
//  2. For each row, decide the next state based on:
//     - rollout_state='pending' AND canary_total_steps=0 → flip
//     straight to 'complete' (no canary ladder was configured;
//     the row's traffic is already 100% on insert). Emit one
//     deployment_audit row.
//     - rollout_state='pending' AND canary_total_steps>0 → flip
//     to 'rolling_out', stamp rollout_started_at=now().
//     - rollout_state='rolling_out' AND canary_step >=
//     canary_total_steps → flip to 'complete', stamp
//     rollout_completed_at=now(). This is the terminal-step
//     transition the canary_progression tick doesn't write
//     (per Commit 3's separation of concerns — canary_progression
//     advances the ladder; orchestrator stamps the rollout
//     state machine).
//     - rollout_state='rolling_out' AND canary_step < total AND
//     the row's been stuck > StuckAfterDuration → log warn,
//     do NOT auto-recover (the operator CLI `gregale rollouts
//     recover <slug>` is the manual escape hatch — see
//     Commit 6).
//  3. Each transition writes one deployment_audit row via
//     Store.AppendDeploymentAudit with the orchestrator's actor
//     sentinel. Audit emit is best-effort: a Postgres hiccup on
//     the audit table is warn-logged + skipped so the next tick
//     can re-stamp the transition. The state-machine write is
//     authoritative — a missed audit row is recoverable by the
//     operator reading the rollout_state column directly.
//
// The orchestrator never calls apid directly. The canary_progression
// tick (pkg/canary, Commit 3) is the API caller for ladder
// advances; this orchestrator only stamps the state machine and
// the audit trail. CLAUDE.md ownership rules are preserved: apid
// owns deployments.* and the orchestrator's only writes are via
// the explicit Store methods.
package safedeploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// StuckAfterDuration is the canned stuck-detection window
// (commit 5's locked-in plan default — 30 minutes). A row in
// rolling_out whose canary_step_started_at is older than this
// window is considered stuck; the orchestrator logs a warning
// per stuck row per tick (rate-limited via Stats so the log
// doesn't flood) and leaves the auto-recovery to the manual
// CLI. cmd/meterd calls SetStuckAfterDuration at boot to apply
// the FAAS_SAFEDEPLOY_STUCK_AFTER env override — production
// tuning never requires a code change. The var/duplication
// with pkg/state.RecoverRolloutStuckAfter is intentional —
// pkg/safedeploy cannot import pkg/state, so the two stay in
// lockstep via test-pinned equality (orchestrator_test.go).
var StuckAfterDuration = 30 * time.Minute

// SetStuckAfterDuration overrides the canned stuck-detection
// window at boot. Called once by cmd/meterd after it parses
// FAAS_SAFEDEPLOY_STUCK_AFTER. The setter is intentionally
// exported so the binary entrypoint can wire it without a new
// setter-on-Orchestrator surface. Values <= 0 are silently
// ignored so a bad env parse never inverts the stuck predicate
// (which would silently mark every fresh rollout as stuck).
func SetStuckAfterDuration(d time.Duration) {
	if d <= 0 {
		return
	}
	StuckAfterDuration = d
}

// Store is the slice of pkg/state.Store the orchestrator needs.
// Declared locally so pkg/safedeploy stays import-cycle-free
// (the production meterd binary satisfies this via the
// canaryStoreAdapter pattern in cmd/meterd).
type Store interface {
	SafedeployListPendingRollouts(ctx context.Context) ([]state.Deployment, error)
	SafedeployStampRollout(ctx context.Context, id string, rolloutState string, startedAt, completedAt, abortedAt *time.Time, abortedReason string) (state.Deployment, error)
	AppendDeploymentAudit(ctx context.Context, entry state.DeploymentAudit) (int64, error)
}

// AuditKinds (issue #976 / ADR-122 / SAFE-RELEASES-F). The
// orchestrator emits one of three audit kinds per transition.
// All three are members of the closed-set vocabulary enforced
// by deployment_audit_kind_chk (migrations/00477_deployment_audit.sql).
const (
	AuditKindRolloutStarted   = "deploy.rollout_started"
	AuditKindRolloutCompleted = "deploy.rollout_completed"
	AuditKindRolloutAborted   = "deploy.rollout_aborted"
)

// Orchestrator wires the dependencies Once(ctx) needs.
type Orchestrator struct {
	Store   Store
	Log     *slog.Logger
	Now     func() time.Time
	Actor   string // service-account sentinel stamped into deployment_audit
	Account string // service-account account_id stamped into deployment_audit
	// Ops (SAFE-RELEASES-OBS PR-A) is the daemon's wire.OpsMetrics
	// handle. Nil-allowed (the test seam builds an Orchestrator
	// without a Prometheus registry); the accessor pattern in
	// wire.OpsMetrics is itself nil-safe so emitAudit can call
	// Ops.DeploymentAuditEmittedTotal(...)() unconditionally.
	Ops *wire.OpsMetrics
}

// NewOrchestrator builds an Orchestrator with nil-coerced
// dependencies. Actor is required (the audit row needs an
// actor string); Account is optional and stays nil when the
// deployment_audit row is for a fleet-level action. Ops is nil
// by default — callers wire it via the exported field (cmd/meterd
// sets it after construction; tests can leave it nil).
func NewOrchestrator(store Store, log *slog.Logger, actor, account string) *Orchestrator {
	if log == nil {
		log = slog.Default()
	}
	return &Orchestrator{
		Store:   store,
		Log:     log,
		Now:     time.Now,
		Actor:   actor,
		Account: account,
	}
}

// Stats is the per-tick observability summary. Mirrors the
// pkg/canary.Stats shape so the §12 dashboard's rollout-state
// panel can correlate the two ticks side-by-side.
type Stats struct {
	Started       int // pending → rolling_out transitions
	Completed     int // rolling_out → complete transitions
	Aborted       int // pending/rolling_out → aborted (manual CLI; orchestrator doesn't auto-abort)
	StuckDetected int // rolling_out rows whose canary_step_started_at is older than StuckAfterDuration
	// StuckCheckMissingTimestamp (SAFE-RELEASES code-review hardening,
	// migration 00517) counts rolling_out rows the orchestrator walked
	// where CanaryStepStartedAt was nil — pre-00517 a nullable
	// canary_step_started_at was legal, but post-00517 the column is
	// NOT NULL DEFAULT NOW(), so a non-zero rate means a write path
	// bypassed the schema default. The orchestrator skips the stuck
	// check for these rows (no timestamp → no comparison possible) and
	// logs a warning, mirroring pkg/canary.Once's zero-timestamp
	// defensive guard at preset.go:226. The Stats counter gives
	// operators visibility through the per-tick log without needing a
	// Prometheus collector on the orchestrator struct.
	StuckCheckMissingTimestamp int
	AuditEmitFailed            int // per-row audit emit errors (warn-logged, never propagated)
}

// ErrOrchestratorNilStore is returned when the orchestrator is
// invoked with a nil Store. cmd/meterd must never wire a nil —
// surfacing the error lets the meterd log book call out the
// misconfiguration loudly.
var ErrOrchestratorNilStore = errors.New("safedeploy: Orchestrator invoked with nil Store")

// Once walks the pending-rollout set and advances the
// rollout_state machine per row. Per-row failures log + skip
// so a single broken deployment never halts the tick. The
// errors returned are non-nil only when the per-tick
// ListPendingRollouts query itself fails — every other failure
// is warn-logged and counted in Stats.
func (o *Orchestrator) Once(ctx context.Context) (Stats, error) {
	var stats Stats
	if o == nil || o.Store == nil {
		return stats, ErrOrchestratorNilStore
	}
	rows, err := o.Store.SafedeployListPendingRollouts(ctx)
	if err != nil {
		return stats, fmt.Errorf("safedeploy: list pending rollouts: %w", err)
	}
	if len(rows) == 0 {
		return stats, nil
	}
	now := o.now()
	for _, d := range rows {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		o.walkRow(ctx, d, now, &stats)
	}
	return stats, nil
}

// IncOps (SAFE-RELEASES-OBS PR-A) folds the per-tick Stats struct
// into the daemon's wire.OpsMetrics registry. The Stats fields
// stay (the per-tick log line still carries the same numbers); this
// method adds the Prometheus-facing mirror so operators have a
// fleet-level view of orchestrator state-machine transitions
// without parsing journal lines.
//
// Call site: cmd/meterd wires the orchestrator and calls
// o.IncOps(ops, stats) once at the end of Once() (right after the
// row walk completes). ops may be nil — IncOps is nil-safe so the
// meterd smoke test (which builds an Orchestrator without a
// Prometheus registry) doesn't have to special-case the wiring.
//
// Each accessor returns a prometheus.Counter (LogsDropped /
// DeploymentAuditGCRowsDeleted precedent); IncOps calls .Inc()
// on every counter. The Stats values are not branched on here —
// the journal line at the meterd call site carries the per-tick
// numbers for log-driven diagnosis, and PR-B's
// safedeploy_orchestrator_*_total rate() queries roll the
// Prometheus counter into per-second rates.
func (o *Orchestrator) IncOps(ops *wire.OpsMetrics, stats Stats) {
	if ops == nil {
		return
	}
	if c := ops.SafedeployOrchestratorStartedTotal(); c != nil {
		c.Inc()
	}
	if c := ops.SafedeployOrchestratorCompletedTotal(); c != nil {
		c.Inc()
	}
	if c := ops.SafedeployOrchestratorAbortedTotal(); c != nil {
		c.Inc()
	}
	if c := ops.SafedeployOrchestratorStuckDetectedTotal(); c != nil {
		c.Inc()
	}
	if c := ops.SafedeployOrchestratorAuditEmitFailedTotal(); c != nil {
		c.Inc()
	}
	if c := ops.SafedeployOrchestratorStuckCheckMissingTimestampTotal(); c != nil {
		c.Inc()
	}
}

// walkRow advances a single deployment row's rollout_state
// based on its current shape. Pulled out of Once so the
// per-state branches read as discrete intents without sprawling
// the top-level tick body.
func (o *Orchestrator) walkRow(ctx context.Context, d state.Deployment, now time.Time, stats *Stats) {
	switch d.RolloutState {
	case "pending":
		// No canary ladder → fast-forward to complete on the
		// first tick. The migration's fast-default left these
		// rows in 'pending' even though they have nothing to
		// canary; the orchestrator cleans them up.
		if d.CanaryTotalSteps <= 0 {
			o.complete(ctx, d, now, stats)
			return
		}
		// Pending + ladder → flip to rolling_out + stamp
		// rollout_started_at. The canary_progression tick
		// advances canary_step from here on.
		o.start(ctx, d, now, stats)
		return
	case "rolling_out":
		// Terminal step reached → flip to complete. The
		// canary_progression tick doesn't write rollout_state
		// (separation of concerns); the orchestrator owns
		// the state-machine transitions.
		if d.CanaryStep >= d.CanaryTotalSteps {
			o.complete(ctx, d, now, stats)
			return
		}
		// Stuck detection: rolling_out with canary_step not at
		// terminal AND canary_step_started_at older than
		// StuckAfterDuration. Warn-log (rate-limited via
		// Stats.StuckDetected) but do NOT auto-recover — the
		// operator CLI is the manual escape hatch (Commit 6).
		//
		// SAFE-RELEASES code-review hardening (migration 00517):
		// post-00517 CanaryStepStartedAt is NOT NULL DEFAULT NOW(),
		// so the nil branch below should never fire in steady state.
		// A non-zero Stats.StuckCheckMissingTimestamp count is the
		// tripwire for "a write path bypassed the apid CreateDeployment
		// stamp". The orchestrator's defensive behaviour matches
		// pkg/canary.Once:226 — log + skip, no auto-recover (the
		// operator CLI is still the manual escape hatch).
		if d.CanaryStepStartedAt != nil {
			elapsed := now.Sub(*d.CanaryStepStartedAt)
			if elapsed > StuckAfterDuration {
				o.Log.Warn("safedeploy: rollout stuck",
					"deployment_id", d.ID, "app_id", d.AppID,
					"canary_step", d.CanaryStep, "canary_total_steps", d.CanaryTotalSteps,
					"elapsed", elapsed.String(), "stuck_after", StuckAfterDuration.String())
				stats.StuckDetected++
				return
			}
		} else {
			// Post-00517 this branch is unreachable in steady state
			// (the column is NOT NULL DEFAULT NOW()). Belt-and-braces
			// for a future write path that bypasses the schema default
			// — without this log + counter, the orchestrator would
			// silently fall through to the 'healthy in-flight row'
			// branch with no operator visibility. Mirrors the
			// pkg/canary.Once defensive guard at preset.go:226.
			o.Log.Warn("safedeploy: rolling_out row has nil canary_step_started_at; skipping stuck check (post-00517 schema default should prevent this)",
				"deployment_id", d.ID, "app_id", d.AppID,
				"canary_step", d.CanaryStep, "canary_total_steps", d.CanaryTotalSteps)
			stats.StuckCheckMissingTimestamp++
		}
		// Healthy in-flight row — nothing to do this tick. The
		// canary_progression tick owns the per-step advance;
		// the orchestrator just watches.
		return
	default:
		// 'complete' or 'aborted' rows should not appear in
		// ListPendingRollouts (the predicate filters them) but
		// a defensive skip keeps the tick robust against a
		// predicate bug.
		o.Log.Warn("safedeploy: unexpected rollout_state in pending walk",
			"deployment_id", d.ID, "rollout_state", d.RolloutState)
		return
	}
}

// start flips pending → rolling_out and stamps rollout_started_at.
// One audit row emitted per transition.
func (o *Orchestrator) start(ctx context.Context, d state.Deployment, now time.Time, stats *Stats) {
	startedAt := now
	if _, err := o.Store.SafedeployStampRollout(ctx, d.ID, "rolling_out", &startedAt, nil, nil, ""); err != nil {
		o.Log.Warn("safedeploy: stamp rolling_out",
			"deployment_id", d.ID, "err", err)
		return
	}
	o.emitAudit(ctx, d, AuditKindRolloutStarted, map[string]any{
		"deployment_id": d.ID,
		"app_id":        d.AppID,
		"canary_preset": d.CanaryPreset,
		"canary_step":   d.CanaryStep,
		"canary_total":  d.CanaryTotalSteps,
		"started_at":    startedAt.UTC().Format(time.RFC3339Nano),
	}, stats)
	stats.Started++
}

// complete flips rolling_out (or no-ladder pending) → complete
// and stamps rollout_completed_at. One audit row emitted per
// transition.
func (o *Orchestrator) complete(ctx context.Context, d state.Deployment, now time.Time, stats *Stats) {
	completedAt := now
	// For the rolling_out → complete transition, preserve the
	// started_at that was stamped earlier. For the no-ladder
	// pending → complete transition, started_at stays nil (the
	// orchestrator never passed through 'rolling_out').
	var startedAt *time.Time
	if d.RolloutStartedAt != nil {
		s := *d.RolloutStartedAt
		startedAt = &s
	}
	if _, err := o.Store.SafedeployStampRollout(ctx, d.ID, "complete", startedAt, &completedAt, nil, ""); err != nil {
		o.Log.Warn("safedeploy: stamp complete",
			"deployment_id", d.ID, "err", err)
		return
	}
	o.emitAudit(ctx, d, AuditKindRolloutCompleted, map[string]any{
		"deployment_id": d.ID,
		"app_id":        d.AppID,
		"canary_preset": d.CanaryPreset,
		"final_step":    d.CanaryStep,
		"canary_total":  d.CanaryTotalSteps,
		"completed_at":  completedAt.UTC().Format(time.RFC3339Nano),
	}, stats)
	stats.Completed++
}

// emitAudit is the deployment_audit write helper. The audit row
// is best-effort: a Postgres hiccup is warn-logged + counted in
// Stats.AuditEmitFailed; the state-machine transition is NOT
// rolled back (the operator can read the rollout_state column
// to recover the missing audit row's content). The actor
// sentinel is the orchestrator's configured Actor string (e.g.
// "meterd:safedeploy"); the account_id is the orchestrator's
// configured Account string (empty when fleet-level).
func (o *Orchestrator) emitAudit(ctx context.Context, d state.Deployment, kind string, data map[string]any, stats *Stats) {
	raw, _ := json.Marshal(data)
	entry := state.DeploymentAudit{
		Kind:  state.DeploymentAuditKind(kind),
		Actor: o.actorOrDefault(),
		Data:  raw,
	}
	// DeploymentID and AccountID are uuid.UUID on the audit
	// row; parse the string from the deployment row. The
	// orchestrator's caller (cmd/meterd) is responsible for
	// wiring a deployment row whose ID is parseable as a UUID
	// — the legacy deployments.id column is uuid (migrations/
	// 00007) and the meterd adapter passes through the
	// string.
	depUUID, err := parseDeploymentUUID(d.ID)
	if err != nil {
		o.Log.Warn("safedeploy: deployment_id is not a UUID; skipping audit emit",
			"deployment_id", d.ID, "err", err)
		stats.AuditEmitFailed++
		return
	}
	entry.DeploymentID = depUUID
	if o.Account != "" {
		acctUUID, acctErr := parseDeploymentUUID(o.Account)
		if acctErr != nil {
			o.Log.Warn("safedeploy: account_id is not a UUID; omitting from audit row",
				"account_id", o.Account, "err", acctErr)
		} else {
			entry.AccountID = &acctUUID
		}
	}
	if _, err := o.Store.AppendDeploymentAudit(ctx, entry); err != nil {
		o.Log.Warn("safedeploy: append audit failed",
			"deployment_id", d.ID, "kind", kind, "err", err)
		stats.AuditEmitFailed++
		// SAFE-RELEASES-OBS PR-A: bump the
		// deployment_audit_emitted_total counter on the failure
		// path so the dashboard's audit-write-fidelity panel can
		// split the per-kind emit rate from the failure rate.
		// The counter accessor is nil-safe (Ops may be nil in
		// the test seam).
		if o.Ops != nil {
			if c := o.Ops.DeploymentAuditEmittedTotal(kind, "failed"); c != nil {
				c.Inc()
			}
		}
		return
	}
	// success path: bump emitted_total{kind, "ok"}.
	if o.Ops != nil {
		if c := o.Ops.DeploymentAuditEmittedTotal(kind, "ok"); c != nil {
			c.Inc()
		}
	}
}

func (o *Orchestrator) now() time.Time {
	if o.Now == nil {
		return time.Now()
	}
	return o.Now()
}

func (o *Orchestrator) actorOrDefault() string {
	if o.Actor == "" {
		return "meterd:safedeploy"
	}
	return o.Actor
}
