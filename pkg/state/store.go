package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrNotFound is returned by Store reads when a row does not exist.
var ErrNotFound = errors.New("state: not found")

// ErrCertFingerprintDrift is returned by UpsertComputeNodeFromVmmd
// when the cert_fingerprint on the existing compute_nodes row
// differs from the fingerprint vmmd just computed locally. This is
// the load-bearing guard for the multi-host safety audit F6 /
// ADR-052 amendment: a vmmd that boots with a freshly-rotated leaf
// on disk MUST NOT silently overwrite a row whose
// cert_fingerprint belongs to the previous leaf (the existing row
// is the public-key-pinning attestation; silently replacing it
// would let a leaked cert remain trusted across the rotation).
//
// The error wraps a fmt.Errorf with the OLD and NEW fingerprints
// so the operator can grep the log line and run `gregale pki
// reconcile <node>` to either (a) re-issue the leaf under the
// expected fingerprint (if the local file is the rogue one), or
// (b) re-stamp the row with the new fingerprint (if the operator
// confirmed the rotation is intentional).
//
// The migration 00347 unique partial index
// compute_nodes_active_unique_idx is the DB-level belt-and-braces
// guard against a future bug that fails to consult this error.
var ErrCertFingerprintDrift = errors.New("state: compute_node cert fingerprint drift")

// ErrConcurrentWake is returned by CreateInstance when the partial
// unique index instances_wake_attempt_active_idx (migration 00350,
// multi-host safety cluster PR-5 / audit F4) rejects an INSERT
// because another schedd has already inserted a row with the same
// wake_id AND state IN ('WAKING', 'COLD_BOOTING'). The caller
// (pkg/sched.Engine.EnsureWake) recovers by reading the existing
// row via ReadActiveInstanceForWakeID and observing the winner's
// progress.
//
// This is the cluster-coord primitive: in-memory wakeCoord
// (pkg/sched/wake_coord.go) serialises within ONE schedd; the
// partial unique index serialises ACROSS schedds on different
// boxes. The two layers are deliberately redundant — the in-
// memory coord is the fast path for the dominant single-box
// case, the partial index is the cluster-coord primitive that
// catches cross-box races.
var ErrConcurrentWake = errors.New("state: concurrent wake — wake_id conflict")

// ErrWakeAlreadyInflight is returned by the engine's wake-retry helper
// (pkg/sched.Engine.createInstanceWithWakeRetry) when the cluster-coord
// partial unique index instances_wake_attempt_active_idx (migration
// 00350) rejects its CREATE INSTANCE call because another schedd has
// already inserted an in-flight row with the same wake_id AND state
// IN ('WAKING', 'COLD_BOOTING'). The engine surfaces this as a
// "another box is handling this wake" outcome — the caller must
// propagate it; the gateway-side retry / cron-side reschedule /
// redeploy handles the follow-up.
//
// This is a strictly-losing outcome. The helper does NOT return the
// winner's row because the engine's downstream path (ledger.Admit,
// vmm.CreateColdBoot, SetInstanceRuntime) is keyed by (ins.ID,
// placement.NodeID) — returning the winner's row would cause the
// LOSER's engine to boot a local microVM tagged with a WINNER's
// instance UUID, double-billing the customer, double-allocating
// per-app concurrency slots, and (in single-box degenerate case)
// colliding on cgroup/jail-uid/netns per spec §6.2-5. The retry
// helper pins this contract by surfacing the typed sentinel and
// exiting the engine's wake path before any local side effect.
var ErrWakeAlreadyInflight = errors.New("state: wake already in flight on another node")

// ErrInstanceNotRunning is returned by Engine.ForceRestart
// (pkg/sched/engine.go) when the gated re-read under lockApp
// observes a state other than RUNNING. The race-loser posture:
// a customer-driven Park or Destroy won the lock between the
// apid gate-time read and schedd's locked re-read. The desired
// end-state (instance no longer running) is already achieved,
// but we still stamp the operator_intent row failed so the
// audit trail records that the admin click did not mutate
// state itself. Translated at the handler boundary to a 409
// instance_not_restartable via the apid gate (which holds its
// own forceRestartableStates check), so this sentinel is the
// *post-lock-re-read* correlate of that pre-lock gate.
var ErrInstanceNotRunning = errors.New("state: instance not in running state")

// ErrCorsWildcardWithCredentials is returned by
// MergeCorsPresetIntoRule when the merged AllowOrigins contains
// the bare "*" wildcard alongside AllowCredentials: true. This is
// the same footgun EdgeRuleCORSAction.Validate rejects at
// create-time (ADR-091 D12, pkg/api/dto.go) — a merge can
// reconstruct the dangerous combination from a rule with
// AllowCredentials: false and a preset with AllowCredentials:
// true (or vice versa), so the merge helper re-runs the same
// guard. The wire-side apid boundary maps this to 422 with the
// same message shape as the create-time gate so the customer
// sees one stable error message.
var ErrCorsWildcardWithCredentials = errors.New("state: cors action cannot combine AllowCredentials: true with AllowOrigins: [\"*\"]")

// ErrInvalidTrafficPercent is returned by UpdateDeploymentTraffic
// (and the create-deployment range-check backstop) when the
// requested traffic_percent falls outside [0, 100]. The CHECK
// constraint on deployments.traffic_percent (migration 00160) is
// the schema-layer guard; this sentinel surfaces the same range
// violation when the store is the one running the backstop
// (UpdateDeploymentTraffic holds the FOR UPDATE lock — the handler
// already validated the request before this method ran, but the
// store validates again as defence in depth). Translated at the
// handler boundary to api.ErrInvalidTrafficPercent (422) so the
// HTTP response uses the canonical RFC 7807 code.
var ErrInvalidTrafficPercent = errors.New("state: invalid traffic_percent")

// ErrCanaryStepConflict is returned by AdvanceCanary when the deployment's
// current step differs from the caller's expected step. The compare-and-swap
// is checked while the deployment row is locked, so this is the safe race
// loser result for concurrent meterd workers.
var ErrCanaryStepConflict = errors.New("state: canary step conflict")

// ErrCanaryStateInvalid is returned when an automatic canary advance reaches
// a deployment that is not an active, live rollout or whose persisted ladder
// is internally inconsistent.
var ErrCanaryStateInvalid = errors.New("state: canary state does not permit advance")

// ErrInvalidPreviewPrState is returned by SetPreviewPrState when the
// requested state is outside the closed set
// {open,closed,stale,torn_down}. The CHECK constraint
// apps_preview_pr_state_chk (migration 00220) is the authoritative
// gate, but the Go-side check lets the offending value ride in the
// error message so an operator typo is diagnosable without a
// Postgres round-trip. ADR-095 PR-C.
var ErrInvalidPreviewPrState = errors.New("state: invalid preview_pr_state")

// ErrTrafficPercentSumInvalid is the defensive backstop returned by
// UpdateDeploymentTraffic when the post-write Σ invariant check
// trips. Structurally unreachable with PR-A's "zero siblings"
// rebalance form (Σ = newPercent + 0 = 100 by construction), but
// pinned in the test suite as a tripwire against future refactors
// (PR-C's proportional redistribution, for example, must still
// satisfy Σ = 100). Translated at the handler boundary to
// api.ErrTrafficPercentSumInvalid (409 Conflict).
var ErrTrafficPercentSumInvalid = errors.New("state: traffic_percent sum != 100")

// ErrInvalidMirrorPercent is returned by CreateMirrorRule /
// UpdateMirrorRule when the requested percent falls outside
// [0, 100]. The CHECK constraint on mirror_rules.percent
// (migration 00348) is the schema-layer guard; this sentinel
// surfaces the same range violation when the store is the one
// running the backstop. Translated at the handler boundary to
// api.ErrInvalidMirrorPercent (422). Mirrors ErrInvalidTrafficPercent's
// contract exactly so the apid surface can lift the traffic-split
// range-check verbatim.
var ErrInvalidMirrorPercent = errors.New("state: invalid mirror_rules.percent")

// ErrMirrorSourceTargetSame is returned by CreateMirrorRule when
// the caller passes the same deployment id for source and mirror.
// The migrations/00348 SQL CHECK prevents the row from being
// inserted at the schema layer; this sentinel surfaces the same
// condition at the store layer so the handler can produce a
// stable RFC 7807 problem without a Postgres round-trip. Distinct
// from ErrInvalidMirrorPercent because the customer-facing problem
// code is different (422 mirror_source_target_same vs 422
// invalid_mirror_percent).
var ErrMirrorSourceTargetSame = errors.New("state: mirror_rules source_deployment_id == mirror_deployment_id")

// ErrMirrorDeploymentNotLive is returned by CreateMirrorRule /
// UpdateMirrorRule when one or both of the referenced deployments
// is not in status='live' (a superseded / failed / pending row
// cannot mirror — operators mirror against live rows, same as the
// traffic-split POST handler). Translated at the handler boundary
// to api.ErrMirrorDeploymentNotLive (409). The caller is
// responsible for passing the ids; the store does not infer them
// from the app.
var ErrMirrorDeploymentNotLive = errors.New("state: mirror_rules source/mirror deployment is not live")

// ErrMirrorCrossAppMismatch is returned by CreateMirrorRule when
// the source_deployment_id and mirror_deployment_id resolve to
// different apps (a single mirror_rule is app-scoped; cross-app
// mirroring is a follow-on ADR-125 §follow-on 4). Translated at
// the handler boundary to api.ErrMirrorCrossAppMismatch (422).
// Distinct from ErrMirrorDeploymentNotLive because the customer
// problem code is different (422 mirror_cross_app_mismatch vs
// 409 mirror_deployment_not_live).
var ErrMirrorCrossAppMismatch = errors.New("state: mirror_rules source/mirror deployment belong to different apps")

// ErrInvalidRecoverAction (issue #976 / ADR-122 / SAFE-RELEASES-R)
// is returned by RecoverRollout when the requested action is not
// in the closed set {"advance","promote","abort"}. The
// handler-level closed-set check (api.AllowedRecoverRolloutAction)
// is the primary gate; this sentinel surfaces the same condition
// at the store layer so a caller driving the store directly
// (e.g. the CLI's test path) gets a stable error. Translated at
// the handler boundary to api.ErrInvalidRecoverAction (422).
var ErrInvalidRecoverAction = errors.New("state: invalid recover_rollout action")

// ErrRolloutNotStuck is returned by RecoverRollout when the
// operator asks for action="advance" on a deployment that is NOT
// stuck — i.e. either rollout_state is 'aborted' / 'complete' or
// the canary_step_started_at is within the stuck-after window
// (StuckAfterDuration in pkg/safedeploy/orchestrator.go). The CLI
// distinguishes "fix a stuck rollout" (advance is the right call)
// from "force-step a healthy rollout" (use promote), so this
// sentinel exists to keep that distinction visible at the HTTP
// boundary. Translated at the handler boundary to
// api.ErrRolloutNotStuck (409 Conflict).
var ErrRolloutNotStuck = errors.New("state: rollout is not stuck; use promote instead")

// ErrRolloutStateInvalid is returned by RecoverRollout when the
// deployment's rollout_state is 'complete' (already done) or
// 'aborted' (closed). The handler is expected to translate this
// to api.ErrRolloutStateInvalid (409 Conflict) so the CLI's
// post-condition check is loud.
var ErrRolloutStateInvalid = errors.New("state: rollout state does not permit recovery")

// CanaryAdvanceParams is the state-owned portion of one automatic canary
// transition. The API layer resolves the next preset stage and supplies the
// audit envelope; the store atomically applies the expected-step CAS,
// traffic rebalance, rollout completion, and audit insert.
type CanaryAdvanceParams struct {
	ExpectedStep   int
	TrafficPercent int
	Audit          DeploymentAudit
}

// CanaryAdvancer is intentionally separate from Store so existing narrow test
// doubles do not have to grow a new method. Production stores implement it;
// the APID handler type-asserts it before accepting the transition.
type CanaryAdvancer interface {
	AdvanceCanary(ctx context.Context, id string, params CanaryAdvanceParams) (Deployment, int64, error)
}

// RecoverRolloutStuckAfter (issue #976 / ADR-122 / SAFE-RELEASES-R +
// production-leveling Stream C) is the canned stuck-detection
// window the RecoverRollout method uses to gate action="advance".
// It MUST match pkg/safedeploy.StuckAfterDuration so the CLI's view
// of "this rollout is stuck" agrees with the meterd orchestrator's
// view — the store layer duplicates the var (no import, to keep
// pkg/safedeploy off pkg/state's import graph) and pins the
// equality in tests.
//
// Default value 30 minutes is the ADR-122 canned value. cmd/apid
// + cmd/meterd call SetRecoverRolloutStuckAfter at boot to apply
// the FAAS_SAFEDEPLOY_STUCK_AFTER env override — production
// tuning never requires a code change.
var RecoverRolloutStuckAfter = 30 * time.Minute

// SetRecoverRolloutStuckAfter overrides the canned stuck-detection
// window at boot. Called once by cmd/apid + cmd/meterd after they
// parse FAAS_SAFEDEPLOY_STUCK_AFTER. The setter is intentionally
// exported (not an unexported func) so the binary entrypoints in
// cmd/{apid,meterd} can wire it without re-importing the constant.
// Values <= 0 are silently ignored so a bad env parse never
// inverts the stuck predicate (which would silently promote every
// healthy rollout).
func SetRecoverRolloutStuckAfter(d time.Duration) {
	if d <= 0 {
		return
	}
	RecoverRolloutStuckAfter = d
}

// ErrInvalidStateTransition is returned by CancelDeploymentTx /
// MarkDeploymentCancelled when the row's current status is not in
// the cancel-eligible set {pending, building, imaging, snapshotting}.
// Translates at the handler boundary to HTTP 409 with the
// deployment_cancel_not_cancellable code (ADR-124).
var ErrInvalidStateTransition = errors.New("state: invalid status transition")

// ErrCancelLiveForbidden is returned by CancelDeploymentTx when the
// caller attempts to cancel a DeployLive row. Cancel of a live
// deployment would either park the app (kills INV 3 — must always
// have a live snapshot OR cold-bootable rootfs) or scale-to-zero
// (kills INV 4 — parked consumes zero RAM). The deploys-rollback
// path (ADR-118) is the user-correct escape. Translates at the
// handler boundary to HTTP 409 with the
// deployment_cancel_live_forbidden code (ADR-124).
var ErrCancelLiveForbidden = errors.New("state: cancel of DeployLive deployment is forbidden; use deploys rollback")

// ErrReorderNotPending is returned by ReorderDeployment when the
// caller attempts to reorder a deployment that has already left
// DeployPending (typically because the build VM is running or the
// deploy reached DeployImaging). The reorder surface only makes
// sense while the row is still in the planner's queue. Translates
// at the handler boundary to HTTP 409 with the
// deployment_reorder_not_pending code (ADR-124).
var ErrReorderNotPending = errors.New("state: reorder only valid for pending deployments")

// ErrPriorityOutOfRange is the defensive backstop returned by
// ReorderDeployment when newPriority falls outside the closed range
// [0, 1000]. The CHECK constraint deployments_priority_check
// (migration 00426 — round-5 rebump above PR #1066's 00410) is the
// schema-layer guard; this sentinel surfaces the same range violation
// when the store is the one running the backstop. Translated at the
// handler boundary to HTTP 422 with the deployment_reorder_priority_invalid
// code.
var ErrPriorityOutOfRange = errors.New("state: priority must be in [0, 1000]")

// ErrQuotaExceeded is returned by CreateAppIfUnderQuota when the
// account already holds limits.DeployedApps live apps. The error wraps
// the observed count so apid can include it in the 403 envelope via
// api.ErrPlanLimitApps without re-running the count.
// QuotaErrorKind names the cap that tripped. "apps" is the
// Limits.DeployedApps cap; "crons" is Limits.CronLimitPerAccount.
// "apps" is the zero value so existing call sites that build a
// QuotaError without a Kind keep behaving the same.
type QuotaErrorKind string

const (
	QuotaErrorKindApps   QuotaErrorKind = "apps"
	QuotaErrorKindCrons  QuotaErrorKind = "crons"
	QuotaErrorKindMemory                = "memory" // reserved for ADR-046 follow-on
	// QuotaErrorKindMirror (issue #72 / ADR-125) trips when a
	// Pro/Scale customer tries to create more than
	// limits.MirrorTargetsPerApp mirror rules on one app. Free /
	// Hobby customers hit the plan-gate one layer up
	// (api.Plan.MirrorRuleAllowed returns false → apid returns 403
	// plan_mirror_not_allowed before the store ever sees the
	// request). The QuotaError carries Observed + Limit so the
	// handler can stamp both on the RFC 7807 problem without
	// re-running the count.
	QuotaErrorKindMirror QuotaErrorKind = "mirror"
	// QuotaErrorKindOpenAPIImports (issue #975 item #2 / ADR-126)
	// trips when an account exceeds Plan.OpenAPIImportsPerAccount
	// for per-app OpenAPI doc imports. The store's
	// UpsertAppOpenAPIDocIfUnderQuota runs count + lock + upsert
	// inside the same critical section so a TOCTOU race can't slip
	// past the cap.
	QuotaErrorKindOpenAPIImports QuotaErrorKind = "openapi_imports"
)

type QuotaError struct {
	Kind       QuotaErrorKind // "apps" | "crons" | "memory" | "mirror" | "openapi_imports"
	Limit      int            // caps at the time of the call
	Observed   int            // count(*) observed inside the same critical section
	NotAllowed bool           // true when the plan tier forbids the entity entirely (e.g. Free cron)
}

func (e *QuotaError) Error() string {
	switch e.Kind {
	case QuotaErrorKindCrons:
		if e.NotAllowed {
			return "state: crons not allowed on this plan"
		}
		return fmt.Sprintf("state: cron quota exceeded (limit=%d, observed=%d)", e.Limit, e.Observed)
	case QuotaErrorKindOpenAPIImports:
		if e.NotAllowed {
			return "state: openapi_imports not allowed on this plan"
		}
		return fmt.Sprintf("state: openapi_imports quota exceeded (limit=%d, observed=%d)", e.Limit, e.Observed)
	default:
		return fmt.Sprintf("state: deployed-app quota exceeded (limit=%d, observed=%d)", e.Limit, e.Observed)
	}
}

// Is allows errors.Is(err, ErrQuotaExceeded) to match any *QuotaError.
// Behaviour parity with ErrNotFound / ErrConflict.
func (e *QuotaError) Is(target error) bool {
	return target == ErrQuotaExceeded
}

// ErrQuotaExceeded is the sentinel callers compare against via errors.Is.
// Concrete instances are *QuotaError so handlers can read limit/observed.
var ErrQuotaExceeded = errors.New("state: deployed-app quota exceeded")

// Org-specific sentinels (ADR-061 / IAM-6, PR 2).
//
// ErrOrgLastOwner: caller is the only active owner of a non-personal
// org. Demoting/removing them would leave the org without an owner.
// ErrOrgAlreadyMember: accepting account already has an active membership.
// ErrOrgMemberCapExceeded: the org has hit Plan.OrgMembersMax() and the
// insert (or membership-from-invitation) was rejected inside the tx.
// ErrOrgInvitationInvalid: token not found, already consumed/revoked, or
// wrong accepting account (email mismatch).
// ErrOrgInvitationExpired: token consumed_at / revoked_at / expires_at past.
var (
	ErrOrgLastOwner         = errors.New("state: org last owner")
	ErrOrgAlreadyMember     = errors.New("state: org already member")
	ErrOrgMemberCapExceeded = errors.New("state: org member cap exceeded")
	ErrOrgInvitationInvalid = errors.New("state: org invitation invalid")
	ErrOrgInvitationExpired = errors.New("state: org invitation expired")
)

// ConsumeAccountCreditParams is the FIFO consumption request (issue
// #279 PR-C). All fields are required except Reason, which the
// reducer falls back to a system constant when empty. ProviderInvoiceID
// must be non-empty — see ConsumeAccountCredit docstring for why.
type ConsumeAccountCreditParams struct {
	AccountID         string
	TargetCents       int64  // >= 0; 0 is a no-op
	Provider          string // "stripe" | "paddle" (denormalised for audit context)
	ProviderInvoiceID string // dedupe key; required for the partial unique index to apply
	InvoiceID         string // for the audit row + ledger reason text
	Reason            string
	Actor             string // "apid" for the admin endpoint
}

// ConsumedCreditRow is one credit's contribution to a consumption call.
// NewBalance is the post-call cents_remaining for that credit.
type ConsumedCreditRow struct {
	CreditID   string
	DeltaCents int64
	NewBalance int64
}

// ConsumeAccountCreditResult is the reducer's outcome.
//
// ConsumedCents is the total drained this call (capped at TargetCents).
// PerCredit captures each drained credit's delta + post-balance.
// RemainingCreditsCents is the sum of cents_remaining for active
// credits after the call (for the audit row + response).
// AlreadyConsumedForInvoice is true when the partial unique index
// rejected every INSERT — the reducer was a no-op and the operator
// sees the same ConsumedCents as the first call (re-derived from the
// existing ledger rows, not the failed INSERT).
type ConsumeAccountCreditResult struct {
	ConsumedCents             int64
	PerCredit                 []ConsumedCreditRow
	RemainingCreditsCents     int64
	AlreadyConsumedForInvoice bool
}

// CronQuotaScope names the cap that CreateCronIfUnderQuota tripped on.
// The handler renders different copy per scope so the customer can tell
// whether to delete a cron from this app (Scope="app") or from one of
// the account's other apps (Scope="account").
type CronQuotaScope string

const (
	// CronQuotaScopeApp is set when limits.CronLimitPerApp was reached.
	CronQuotaScopeApp CronQuotaScope = "app"
	// CronQuotaScopeAccount is set when limits.CronLimitPerAccount was reached.
	CronQuotaScopeAccount CronQuotaScope = "account"
)

// CronQuotaError is returned by CreateCronIfUnderQuota when either
// cap (per-app or per-account) is reached. Distinct from QuotaError
// because it carries Scope, and we want errors.Is to match a cron-
// specific sentinel rather than overloading the deployed-apps chain.
type CronQuotaError struct {
	Scope    CronQuotaScope
	Limit    int
	Observed int
}

func (e *CronQuotaError) Error() string {
	return fmt.Sprintf("state: cron quota exceeded (scope=%s, limit=%d, observed=%d)", e.Scope, e.Limit, e.Observed)
}

// Is allows errors.Is(err, ErrCronQuotaExceeded) to match any *CronQuotaError.
func (e *CronQuotaError) Is(target error) bool {
	return target == ErrCronQuotaExceeded
}

// ErrCronQuotaExceeded is the sentinel callers compare against via errors.Is.
var ErrCronQuotaExceeded = errors.New("state: cron quota exceeded")

// TriggerQuotaScope names the cap that CreateTriggerIfUnderQuota
// tripped on. Mirrors CronQuotaScope so the apid handler can render
// the same "delete one to add another" copy regardless of which
// surface (cron / webhook / trigger) the quota came from.
type TriggerQuotaScope string

const (
	// TriggerQuotaScopeApp is set when limits.TriggerLimitPerApp was reached.
	TriggerQuotaScopeApp TriggerQuotaScope = "app"
	// TriggerQuotaScopeAccount is set when limits.TriggerLimitPerAccount was reached.
	TriggerQuotaScopeAccount TriggerQuotaScope = "account"
)

// TriggerQuotaError is returned by CreateTriggerIfUnderQuota when
// either cap (per-app or per-account) is reached (issue #757 /
// ADR-0NN). Distinct from CronQuotaError so the apid handler can
// branch on the typed error and emit CodePlanTriggerQuota rather
// than CodePlanCronQuota. errors.As recovers Scope/Limit/Observed.
type TriggerQuotaError struct {
	Scope    TriggerQuotaScope
	Limit    int
	Observed int
}

func (e *TriggerQuotaError) Error() string {
	return fmt.Sprintf("state: trigger quota exceeded (scope=%s, limit=%d, observed=%d)", e.Scope, e.Limit, e.Observed)
}

// Is allows errors.Is(err, ErrTriggerQuotaExceeded) to match any *TriggerQuotaError.
func (e *TriggerQuotaError) Is(target error) bool {
	return target == ErrTriggerQuotaExceeded
}

// ErrTriggerQuotaExceeded is the sentinel callers compare against via errors.Is.
var ErrTriggerQuotaExceeded = errors.New("state: trigger quota exceeded")

// ErrReplay is returned by CheckWebhookReplay when a webhook delivery
// has been seen inside its dedupe window. Webhook ingresses respond
// 200 on this error (idempotent — the upstream provider interprets
// as success and stops retrying), then emit a webhook.replay_rejected
// audit row. Issue #294 closes the gap that SOC 2 CC6.1 expects.
var ErrReplay = errors.New("state: webhook replay rejected")

// ErrAPIKeyExpired is returned by AuthenticateKey when the matched
// key has expires_at < now(). The first auth attempt after expiry
// flips the row to status='revoked' atomically before returning, so
// a second attempt returns ErrAPIKeyRevoked. The audit
// `key.expired` row is emitted by the auth middleware, not the
// store (the store has no Auditor dependency).
//
// Issue #189 / IAM-5.
var ErrAPIKeyExpired = errors.New("state: api key expired")

// ErrAPIKeyRevoked is returned by AuthenticateKey when the matched
// key has status='revoked'. The state is terminal — there is no
// recovery path. The auth middleware maps this to a 401 with the
// `api_key_revoked` Problem code.
//
// Issue #189 / IAM-5.
var ErrAPIKeyRevoked = errors.New("state: api key revoked")

// ErrInvalidCursor is returned by List*ForOrgPage and friends when
// the `before` cursor doesn't parse as a valid compound key. The
// handler maps this to a 400 with the `invalid_cursor` Problem code;
// PR-9 deliberately rejects the v1 behavior of silently returning
// zero rows because that made broken clients fall behind without
// any signal.
var ErrInvalidCursor = errors.New("state: invalid cursor")

// MaxDeploymentLogPage caps the per-call row count for
// ListDeploymentLogs. Both implementations clamp the caller's
// `limit` to this value before allocating — defense in depth so a
// caller that forgets to validate a query-string `limit` can't
// trigger an oversized allocation (CodeQL go/allocation-size).
const MaxDeploymentLogPage = 500

// clampLogLimit sanitizes the caller-supplied `limit` argument to
// ListDeploymentLogs so the slice allocation in the store
// implementations is provably bounded. CodeQL's
// go/allocation-size rule recognizes the result of this helper
// (small pure function returning a constant-bounded value) as a
// sanitizer; an inline `if limit > X { limit = X }` branch is not
// tracked.
func clampLogLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > MaxDeploymentLogPage {
		return MaxDeploymentLogPage
	}
	return limit
}

// PaddleOverageDedupeSchemaResult is the read-only snapshot of the
// paddle_overage_dedupe table that the B4 pre-flight surfaces. The
// bools are derived from information_schema.columns (PgStore) or
// from a structural check of the rows held in memory (MemStore);
// the counts are the per-state row totals the same call replaces.
// Returned by state.Store.PaddleOverageDedupeSchema.
type PaddleOverageDedupeSchemaResult struct {
	// TableExists is true if the paddle_overage_dedupe table is
	// present (00034+). False means "no Paddle overage flushes have
	// ever happened against this DB" — the operator needs migrations
	// 00034 then 00041 applied in order.
	TableExists bool
	// HasWindowStart / HasState / HasClaimedAt / HasClaimedBy are
	// the four columns added by migration 00041. All four must be
	// true for the meterd overage pusher to land; a partial
	// (HasWindowStart but !HasState) tripwire means the migration
	// was interrupted mid-flight and the DB is in a degraded state
	// the operator must resolve before any further push.
	HasWindowStart bool
	HasState       bool
	HasClaimedAt   bool
	HasClaimedBy   bool
	// PendingRows / CompletedRows are the per-state row totals.
	// Surfaced so the same CLI call replaces a manual
	// `select count(*) filter (where state = …)` query.
	PendingRows   int64
	CompletedRows int64
}

// AppErrorGroup is the typed row produced by the
// /v1/apps/{slug}/errors/summary endpoint (ADR-096). It mirrors
// sqlc.ListAppErrorGroupsRow but uses stdlib uuid.UUID + time.Time
// so handlers don't have to thread pgtype values through the
// wire layer. PgStore converts at the boundary.
type AppErrorGroup struct {
	ID            uuid.UUID
	Fingerprint   string
	ErrorClass    string
	Route         string
	HTTPStatus    int32
	Count         int64
	RequestCount  int64
	FirstSeenAt   time.Time
	LastSeenAt    time.Time
	SampleMessage string
}

// AppErrorRequestRow is the typed drill-down row for
// /v1/apps/{slug}/errors/{fingerprint}.
type AppErrorRequestRow struct {
	ID            uuid.UUID
	RequestID     uuid.UUID
	ReceivedAt    time.Time
	Route         string
	HTTPStatus    int32
	ErrorClass    string
	SampleMessage string
	DeploymentID  *uuid.UUID
}

// AppErrorSampleRow is the typed single-sample row for
// /v1/apps/{slug}/errors/{fingerprint}/first.
type AppErrorSampleRow struct {
	AppErrorRequestRow
	HeadersSample []byte // jsonb raw; handler parses to map[string]string
	Redactions    []string
}

// ComputeNodeUsageBatcher is the optional bulk form of ComputeNodeUsedMB.
// Placement uses it when a fleet has nodes without a fresh vmmd capacity
// report, avoiding one SQL round trip per candidate node. It is separate from
// Store so narrow test doubles and older integrations remain source-compatible.
type ComputeNodeUsageBatcher interface {
	ComputeNodeUsedMBByNode(ctx context.Context, nodeIDs []string) (map[string]int64, error)
}

// WebhookDeliveryReleaser is an optional rollback seam for webhook ingress.
// A delivery is claimed before its side effects run to serialize concurrent
// redeliveries; if those side effects fail, the claim must be removed so the
// provider can retry immediately instead of receiving a false duplicate ACK.
// It is separate from Store so narrow test doubles and older integrations
// remain source-compatible.
type WebhookDeliveryReleaser interface {
	ReleaseWebhookDelivery(ctx context.Context, provider, deliveryID string) error
}

// Store is the persistence boundary apid and schedd depend on (spec §6, ADR-006).
// The production implementation is Postgres via the embedded SQL queries in
// pkg/state/queries.sql; MemStore backs unit tests. Keeping this interface
// narrow keeps the ownership rules enforceable — apid only touches
// customer-intent tables through the methods it is given.
type Store interface {
	// Ping tests store/database connectivity.
	Ping(ctx context.Context) error

	// Accounts & auth.
	CreateAccount(ctx context.Context, email string, plan api.Plan) (Account, error)
	// CreateAccountWithPersonalOrg is the PR 3 canonical
	// account-creation entry point (issue #190 / ADR-061). It runs
	// the account INSERT + orgs INSERT + org_memberships INSERT
	// under a single transaction so the "every account has exactly
	// one personal org" invariant is atomic at the SQL layer (the
	// partial unique orgs_one_personal_per_account_uniq is the
	// tripwire for any caller bypassing this helper).
	//
	// Returns:
	//   - (CreateAccountWithPersonalOrgResult, nil) on success —
	//     Account is the freshly minted row, PersonalOrg is the
	//     matching personal org with plan/status mirroring the
	//     account.
	//   - (CreateAccountWithPersonalOrgResult{}, state.ErrConflict)
	//     on a duplicate email — the accounts.email UNIQUE trips as
	//     23505 and the existing mapErr funnel returns ErrConflict.
	//     The handler-level ladder in postSignup / OAuth callbacks
	//     collapses to the idempotent-signin path on this error.
	//   - (CreateAccountWithPersonalOrgResult{}, other) on
	//     transport / SQL errors.
	//
	// Iso-level is ReadCommitted (matches AddOrgMember and
	// ConsumeOrgInvitation); the partial unique is the SQL-level
	// tripwire at any isolation level.
	//
	// Callers that need a non-personal-org account (dev bootstrap
	// at cmd/apid/main.go:54, backfill test fixtures) must call
	// CreateAccount directly. All four production signup paths and
	// the e2e harness SeedAccount route through this helper.
	CreateAccountWithPersonalOrg(ctx context.Context, params CreateAccountWithPersonalOrgParams) (CreateAccountWithPersonalOrgResult, error)
	AccountByID(ctx context.Context, id string) (Account, error)
	// AccountsByIDs returns a map of every account present in `ids`
	// keyed by ID. Missing IDs are NOT errors — they simply don't
	// appear in the returned map. The handler that joins this into
	// dashboard renders treats absence as the "(deleted account)"
	// race case (same contract as AccountByID's per-row missing
	// case at cmd/apid/handlers_dashboard.go's
	// dashboardMembershipProjection).
	//
	// Caller contract: empty `ids` returns (empty map, nil) without
	// issuing a query; the planner renders an empty `ANY($1)` as
	// never-true, so PgStore short-circuits. This is the batch
	// equivalent of memstore.go's ListLatestInstancePerApp pattern
	// (function-top lock + map absence + no per-id error shape).
	//
	// PR-9 §1: closes the N+1 fan-out in the org-detail dashboard
	// render where every active member used to trigger a separate
	// AccountByID round-trip.
	AccountsByIDs(ctx context.Context, ids []string) (map[string]Account, error)
	AccountByEmail(ctx context.Context, email string) (Account, error)
	AccountByKeyHash(ctx context.Context, hash []byte) (Account, error)
	UpdateAccountPlan(ctx context.Context, id string, plan api.Plan) error
	UpdateAccountStatus(ctx context.Context, id string, status AccountStatus) error

	// MFA (issue #186 / IAM-2).
	// ConsumeRecoveryCode atomically matches `presented` against the
	// stored SHA-256 recovery-code hashes and removes the matching
	// hash from the array. Returns:
	//   - (false, 0, false, ErrNotFound) when the row is missing
	//   - (false, 0, false, nil)         when the presented code
	//     didn't match any stored hash
	//   - (true, lastCode, remaining, nil) when the code matched and
	//     was removed; lastCode is true iff exactly one hash
	//     remained (the handler refuses to burn the last code and
	//     prompts for a password instead); remaining is the count
	//     of hashes still on the row after the consume committed,
	//     which the handler uses to render the post-burn customer
	//     email with the right tone (one-of-many vs warning vs
	//     last-code) — see issue #329.
	// The sealed TOTP secret is preserved across consumes; the
	// customer can still /verify after burning every recovery code.
	ConsumeRecoveryCode(ctx context.Context, id string, presented []byte) (matched bool, lastCode bool, remaining int, err error)
	// MatchRecoveryCode tests a presented SHA-256 hash against the
	// stored set without mutating. Returns (matched=true, lastCode=true)
	// when the presented hash matches and the stored array has exactly
	// one element. Used by /recover to refuse-the-burn BEFORE calling
	// ConsumeRecoveryCode; the consume is only attempted when the
	// customer has at least one backup code. Returns ErrNotFound when
	// the row is missing or mfa_enrolled_at IS NULL.
	MatchRecoveryCode(ctx context.Context, id string, presented []byte) (matched bool, lastCode bool, err error)
	// ReadMFASecret returns the sealed TOTP secret (the at-rest age-
	// sealed blob the verify handler unseals with the host age
	// identity). Returns ErrNotFound when the row is missing OR the
	// secret column is NULL (the customer has never enrolled).
	ReadMFASecret(ctx context.Context, id string) (encrypted []byte, err error)
	// SetMFASecret writes the sealed secret + recovery-code hashes
	// WITHOUT stamping mfa_enrolled_at. Called by /v1/account/mfa/enroll:
	// the secret lives in the row while the user types the first
	// code, and the next /confirm is the "enrolled" point. Overwrites
	// any prior enrollment state (idempotent re-enroll).
	SetMFASecret(ctx context.Context, id string, encrypted []byte, recoveryHashes [][]byte) error
	// MarkMFAEnrolled stamps mfa_enrolled_at = now() and clears
	// mfa_required = false. Idempotent on retry. Returns ErrNotFound
	// when the row is missing — the handler must surface 404 then.
	MarkMFAEnrolled(ctx context.Context, id string) error
	// ClearMFA nulls mfa_secret_encrypted, mfa_recovery_codes_hash,
	// and mfa_enrolled_at. Does NOT touch mfa_required so an explicit
	// policy remains in force after a customer disables MFA. The audit
	// Emit is the caller's job
	// (the events table is the audit trail; this method does not
	// persist a reason string).
	ClearMFA(ctx context.Context, id string) error
	// SetMFARequired writes the explicit mfa_required policy flag and
	// reports whether the row actually changed. Returns (changed=true,
	// nil) on a real write, (changed=false, nil) when the value was
	// already what was requested, and (changed=false, ErrNotFound) when the row is
	// missing — distinguishable from a no-op so the handler can 404
	// the missing case.
	SetMFARequired(ctx context.Context, id string, required bool) (changed bool, err error)
	// CountDeployments returns the total number of live deployment
	// rows for the account across all apps. Soft-deleted apps and
	// `failed`/`superseded` deployments are excluded. Empty on a fresh
	// account.
	CountDeployments(ctx context.Context, id string) (int, error)

	// UpdateAccountProviderCustomerID records the Stripe `cus_…` ID on the
	// account row so the webhook + push paths can join. Idempotent — a
	// repeat call with the same value is a no-op (ADR-010, Slice 2).
	UpdateAccountProviderCustomerID(ctx context.Context, id, stripeCustomerID string) error
	// UpdateAccountStripeSubscriptionItem records the Stripe metered
	// subscription item ID (si_…) so meterd's hourly push knows
	// where to POST UsageRecord (issue #52, M7). Empty until the
	// customer's first subscription.created webhook lands.
	UpdateAccountStripeSubscriptionItem(ctx context.Context, id, subItem string) error
	// AccountByProviderCustomerID resolves an account from the Stripe customer
	// ID. The webhook is the only caller; backed by an index in production
	// (deferred). Returns ErrNotFound for unknown customers.
	//
	// Also serves as the Paddle `ctm_…` reverse-lookup: the shared
	// accounts.provider_customer_id column is reused per ADR-025, and
	// Stripe cus_… / Paddle ctm_… values are disjoint prefixes so the
	// shared column is safe in single-provider deployments (the
	// FAAS_BILLING_PROVIDER selector is per-deployment, not per-row).
	AccountByProviderCustomerID(ctx context.Context, providerCustomerID string) (Account, error)
	// ListAllAccounts returns every account. meterd walks it on every
	// quota tick and every Stripe push; on a one-box that's bounded
	// (Free + Hobby + Pro + Scale test accounts + a handful of paid).
	ListAllAccounts(ctx context.Context) ([]Account, error)

	// Account-scoped deletion (spec §17 G6, ADR-021). The customer's
	// DELETE /v1/account schedules a 30-day grace window; pkg/grace in
	// apid sweeps on a 60s timer and calls DeleteAccount once the
	// window lapses. RestoreAccount flips the row back to active iff
	// called inside the grace window — past that the only honest
	// answer is ErrConflict and the handler returns 409.
	//
	// DeleteAccount is a single transaction that walks the FK graph in
	// dependency order (app_secrets → custom_domains → crons → instances
	// → snapshots → builds → deployments → apps → api_keys →
	// idempotency_keys → usage_minutes → accounts). Returns ErrNotFound
	// when the final accounts row is already gone, so a redelivered
	// grace tick is idempotent.
	//
	// MarkAccountDeletionPending is idempotent: a repeat call leaves
	// deletion_requested_at untouched (it carries the original grace
	// deadline). RestoreAccount zeroes deletion_requested_at.
	DeleteAccount(ctx context.Context, id string) error
	// AppendGdprRequest records a single GDPR self-service action
	// (export, delete, restore) against the account email at the
	// moment it lands. Append-only by contract; the gdpr_requests
	// table outlives DeleteAccount so a customer (or a DPO) can be
	// shown the proof of erasure against an email + timestamp. The
	// Completed field is set when the action has run to its end
	// (export = on insert, delete = after pg/grace hard-delete fires,
	// restore = on insert since restore is itself the endpoint).
	AppendGdprRequest(ctx context.Context, req GdprRequest) error
	// ListGdprRequestsForAccount returns the ledger rows for an
	// account in requested_at desc order, bounded by limit. Used by
	// the GDPR export bundle's audit slice so the customer sees their
	// own actions reflected in the same JSON.
	ListGdprRequestsForAccount(ctx context.Context, accountID string, limit int) ([]GdprRequest, error)
	// CompleteGdprRequest stamps completed_at on the most recent
	// un-completed row of (account_id, action). Called by pkg/grace
	// after DeleteAccount succeeds so the delete row in the ledger
	// carries the actual hard-delete timestamp.
	CompleteGdprRequest(ctx context.Context, accountID, action string) error
	// CountGdprRequestsSince returns how many ledger rows for
	// (account_id, action) have requested_at >= since. Backed by an
	// index-only count on gdpr_requests so the rate-limit check on
	// GET /v1/account/export (PR-5.1 / issue #755) is O(log n) even
	// for accounts with long export histories. Used to enforce the
	// 24h export rate limit — the caller decides the policy window.
	CountGdprRequestsSince(ctx context.Context, accountID, action string, since time.Time) (int, error)
	// FindGdprRequestByRequestID returns the most recent ledger row
	// for (account_id, request_id) when one exists, or
	// (GdprRequest{}, ErrNotFound) otherwise. Used by the export
	// handler (PR-5.2 / issue #755) to make X-Request-Id retries
	// idempotent — the second call returns the original ledger row
	// (and the handler can re-build the bundle from the same data
	// sources without a fresh DB scan, or short-circuit to the
	// cached bundle if one is available). The account_id predicate
	// is load-bearing: an attacker who learns a request id cannot
	// probe another account's history.
	FindGdprRequestByRequestID(ctx context.Context, accountID, requestID string) (GdprRequest, error)
	ListBuildsForAccount(ctx context.Context, accountID string) ([]Build, error)
	// ListBuildsForAccountPaged returns one page of builds across
	// the account's deployments, ordered `started_at DESC NULLS
	// LAST, id DESC` (so queued builds sort to the bottom and
	// the id tiebreaker is deterministic on sub-second
	// collisions). statusFilter="" means "any status";
	// appIDFilter="" means "any app". When appIDFilter is set,
	// restricts to deployments.app_id = appIDFilter.
	//
	// Cursor contract (post-review fix, 6th commit, ADR-091 §3):
	// the keyset tuple is `(before.Time, beforeID)` under the
	// DESC NULLS LAST, id DESC ordering. Branch order on the
	// store is load-bearing:
	//   - before.IsZero() && beforeID != "" → queued-tail
	//     cursor ("|id_hex" wire format). Caller is paging
	//     through the queued tail via id alone.
	//   - before.IsZero() && beforeID == "" → first page; no
	//     keyset predicate.
	//   - before non-zero → keyset `(started_at, id) <
	//     (before, beforeID)` with an SQL disjunction that
	//     reaches the queued zone regardless of NULL semantics.
	// See pkg/state/pgstore.go::ListBuildsForAccountPaged for
	// the SQL implementation; pkg/state/memstore.go for the
	// memstore mirror.
	//
	// Mirrors ListDeploymentsForAccount (pkg/state/pgstore.go:4435).
	//
	// Used by GET /v1/builds (ADR-091, issue #741 close-out). The
	// unlimited ListBuildsForAccount(ctx, accountID) sibling stays
	// intact for the GDPR export at cmd/apid/handlers_account.go:643.
	ListBuildsForAccountPaged(ctx context.Context, accountID, statusFilter, appIDFilter string, before time.Time, beforeID string, limit int) ([]Build, error)
	ListCronsForAccount(ctx context.Context, accountID string) ([]Cron, error)
	// UsageByAccount aggregates every per-minute usage_minutes row that
	// landed in [since, now]. MemStore synthesizes the per-minute
	// rollup; PgStore runs a SELECT … WHERE account_id = $1 AND minute >= $2.
	// Empty since means "every row" — used by the GDPR export bundle.
	UsageByAccount(ctx context.Context, accountID string, since time.Time) ([]Usage, error)
	MarkAccountDeletionPending(ctx context.Context, id string) error
	RestoreAccount(ctx context.Context, id string) error

	// Dunning + quota warning (spec §4.7). These three are the load-
	// bearing primitives pkg/meter.Dunning + pkg/meter.EnforceQuota's
	// "warn" branch depend on.
	//
	// LoadAndStampLastQuotaWarning atomically stamps last_quota_warning_at
	// to the supplied UTC-day anchor AND reports whether the row already
	// carried a stamp for that day. Returns (true, nil) on a same-day
	// repeat call (the quota gate suppresses the second notify), (false,
	// nil) on a first-today call (caller emits the notify), and
	// ErrNotFound when the account row is gone.
	LoadAndStampLastQuotaWarning(ctx context.Context, accountID string, day time.Time) (alreadyWarned bool, err error)
	// ClearQuotaWarning nulls last_quota_warning_at so a customer who
	// paid an invoice (or any other path that resets the overage
	// counter) sees the next quota_warning on the *next* UTC day rather
	// than being skipped because of a stamp from days ago.
	ClearQuotaWarning(ctx context.Context, accountID string) error
	// MarkDunningStep atomically advances a row from `from` to `to`
	// (e.g. past_due → suspended), stamping past_due_at only when the
	// destination is past_due. Returns ErrNotFound when the row is
	// missing OR its status didn't match `from` (the latter is the
	// redelivery race: two ticks firing close together on the same
	// overdue row, the second must not double-transition).
	MarkDunningStep(ctx context.Context, accountID string, from, to AccountStatus) error

	// Mail suppression (issue #246 acceptance item 7, ADR-115 §D.3).
	// RecordMailSuppression writes a row for a hard-bounce or
	// complaint and reports whether the row was a fresh insert
	// (true) or a replay that hit the (source, provider_event_id)
	// unique index (false). The bounce handler reads the bool to
	// decide whether to advance dunning — a replay MUST NOT
	// trigger a second MarkDunningStep call. SQLSTATE 23505 on the
	// unique index is mapped to ErrConflict by the PgStore wrapper
	// (pr-1000 convention) so a caller that wants strict failure
	// on duplicate-event-id can detect it; the bounce handler does
	// not — replay-safety is built into the RETURNING bool.
	RecordMailSuppression(ctx context.Context, in MailSuppressionInput) (inserted bool, err error)
	// IsMailSuppressed reports whether any active suppression
	// matches the address. "Active" means expires_at IS NULL OR
	// expires_at > now(); the partial index keeps expired rows
	// out of the lookup so a row that fell out of TTL does not
	// block future mail. The suppression decorator (pkg/mail/
	// suppression.go) calls this on every outbound message;
	// cache it per-process for 60s to avoid hitting Postgres on
	// the hot path of the dunning timer.
	IsMailSuppressed(ctx context.Context, email string) (bool, error)

	// API keys.
	// CreateAPIKey persists a new key row. Scopes is the explicit set of
	// authorization scopes attached to the key (e.g. "admin",
	// "apps:read", "deploy:write", "secrets:read", "secrets:write",
	// "usage:read"); see ADR-034 rev2. The store does not validate the
	// scope vocabulary — that is the apid handler's responsibility
	// (api.NormalizeCreateKeyScopes is the canonical funnel; the DB
	// CHECK constraint added in migration 00044 is the floor a typo
	// cannot cross).
	CreateAPIKey(ctx context.Context, accountID string, hash []byte, label string, scopes []string) (APIKey, error)
	// DeleteAPIKey removes a key without returning the row. Used by
	// paths that don't need to surface the deleted scopes (none
	// today; DeleteAPIKeyReturning is the preferred shape for new
	// callers).
	DeleteAPIKey(ctx context.Context, accountID, keyID string) error
	// DeleteAPIKeyReturning deletes the key and returns the row in a
	// single statement (IAM-1, ADR-034 rev2). The handler uses the
	// returned APIKey.Scopes to emit a `key.deleted` audit event
	// carrying the dismissed permission set, so operators can
	// answer "what just got revoked?" without re-deriving it from
	// logs. Returns ErrNotFound when no matching row exists.
	//
	// **NOT atomic with audit emission.** This method issues a
	// single DELETE...RETURNING; the subsequent AppendEvent call in
	// the handler is a separate statement and round-trip. If the
	// audit INSERT fails (network blip, schema drift), the key is
	// gone but no `key.deleted` row exists. Callers MUST handle this
	// partial-failure mode — the IAM-1 handler in cmd/apid logs a
	// WARN via apid_audit_write_failures_total and accepts the
	// loss-of-audit-row risk. A future tx-wrapper method
	// (DeleteAPIKeyReturningAudited) could close this gap; today,
	// document the trade-off.
	DeleteAPIKeyReturning(ctx context.Context, accountID, keyID string) (APIKey, error)
	ListAPIKeys(ctx context.Context, accountID string) ([]APIKey, error)
	// APIKeyByHash resolves an api_keys row by its SHA-256 hash. Used
	// by the post-login audit log (cmd/apid/handlers_auth.go) so an
	// operator investigating "who signed in as alice?" can identify
	// which key authenticated. Returns ErrNotFound if no row matches.
	APIKeyByHash(ctx context.Context, hash []byte) (APIKey, error)
	// GetAPIKey returns a single api_keys row by (accountID, keyID).
	// Used by the legacy rotateKey (issue #190 / IAM-6, PR 6) dual-write
	// to discover the old key's org_id before the rotation. Cross-account
	// reads collapse to ErrNotFound at the SQL level (the WHERE pins
	// both account_id and id) — the same IDOR-safe shape the older
	// MarkAPIKeyRevoked uses. Returns ErrNotFound if no matching row.
	GetAPIKey(ctx context.Context, accountID, keyID string) (APIKey, error)
	// AuthenticateKey resolves a bearer token to the matching account
	// AND key in a single Store call. This is the canonical lookup for
	// the apid auth middleware (cmd/apid server.go s.auth) — it avoids
	// AccountByKeyHash + APIKeyByHash being two round-trips and ensures
	// the principal is assembled atomically. Returns ErrNotFound when
	// the hash has no matching key.
	//
	// IAM-5 (issue #189) extends the contract: an authenticated key
	// whose status='revoked' returns ErrAPIKeyRevoked; a key whose
	// expires_at is in the past is lazily flipped to status='revoked'
	// and revoked_at stamped before this method returns
	// ErrAPIKeyExpired. The audit `key.expired` row is emitted by
	// the auth middleware (which has the Auditor dependency), not
	// here. See pkg/auth/middleware for the HTTP-side translation.
	AuthenticateKey(ctx context.Context, hash []byte) (Account, APIKey, error)
	// AuthenticateOIDCBearer resolves an OIDC-derived short-lived
	// bearer (issue #270 / ADR-101) to its account + synthetic
	// APIKey. The hash lookup hits oidc_exchanged_tokens.token_hash
	// (UNIQUE index). Rows past ExpiresAt return ErrNotFound — the
	// 5-min TTL is the natural expiry path; no lazy-flip required.
	// The returned APIKey is a synthetic projection
	// (status='active', scopes=['deploy:write']) so the principal
	// stamp + downstream requireScope chain works unchanged. Returns
	// ErrNotFound when no row matches.
	AuthenticateOIDCBearer(ctx context.Context, hash []byte) (Account, APIKey, error)
	// AccountByOIDCSubject resolves an OIDC subject to the platform
	// account it's bound to. The binding lives in oidc_trust_policies
	// (issue #270 / ADR-101); a successful exchange requires the
	// (issuer_url, subject) pair to have an existing account_id
	// FK. Returns ErrNotFound when no binding exists. The handler
	// (pkg/oidc/handler.go) maps that to 401 "OIDC subject not
	// bound" — distinct from a bad-signature 401 so the customer
	// can tell "wrong CI job" from "wrong customer".
	AccountByOIDCSubject(ctx context.Context, issuerURL, subject string) (Account, error)
	// UpsertOIDCTrustPolicy inserts or updates the per-(account,
	// issuer) policy row. Used by the OIDC exchange handler's
	// first-use auto-create path (PR-A) and the dashboard's
	// refine form (PR-C). Returns ErrAlreadyExists on conflicting
	// PK inserts — the caller retries as UpdateOIDCTrustPolicy.
	UpsertOIDCTrustPolicy(ctx context.Context, p *OIDCTrustPolicy) (*OIDCTrustPolicy, error)
	// GetOIDCTrustPolicy returns the policy for (account_id,
	// issuer_url). Returns ErrNotFound on miss.
	GetOIDCTrustPolicy(ctx context.Context, accountID, issuerURL string) (*OIDCTrustPolicy, error)
	// ListOIDCTrustPoliciesForAccount returns every trust policy
	// the account owns. Empty slice on miss. Used by the dashboard
	// list page (PR-C).
	ListOIDCTrustPoliciesForAccount(ctx context.Context, accountID string) ([]*OIDCTrustPolicy, error)
	// InsertOIDCExchangedToken stores a fresh exchanged-token row.
	// The caller has already generated the bearer (api.GenerateOIDCKey)
	// and hashed it (api.HashAPIKey); the row carries only the hash.
	// Returns the server-minted row id (gen_random_uuid at the SQL
	// layer) so the caller can echo it in the response and stamp
	// it on the audit row as the correlation key.
	InsertOIDCExchangedToken(ctx context.Context, t *OIDCExchangedToken) (string, error)
	// GetOIDCExchangedTokenByHash returns the row whose TokenHash
	// equals the input. Returns ErrNotFound on miss. The caller
	// checks ExpiresAt before using the row — a stale row that
	// survived a TTL race surfaces as 401, not silent acceptance.
	GetOIDCExchangedTokenByHash(ctx context.Context, hash []byte) (*OIDCExchangedToken, error)
	// DeleteOIDCExchangedToken is the operator-driven revoke path
	// (PR-C). A 5-min TTL row is normally reaped by lazy-Get
	// (GetByHash on a row past ExpiresAt returns ErrNotFound);
	// Delete is for the "kill this CI job's credential now" case.
	DeleteOIDCExchangedToken(ctx context.Context, id string) error
	// TouchKeyLastUsed bumps the key's last_used_at to now(). Called
	// fire-and-forget on every successful bearer auth in the apid
	// middleware so the dashboard can show "X used 2 minutes ago"
	// (PRD §4.4) without coupling request latency to a non-critical
	// observability write.
	//
	// Not invoked by the OIDC branch — a 5-min TTL row would
	// dominate write load if every CI request stamped last_used_at.
	// The mint-time audit row (auth.token.exchanged) is the durable
	// record.
	TouchKeyLastUsed(ctx context.Context, keyID string) error

	// CreateAPIKeyWithExpiry is the IAM-5 (issue #189) shape. The
	// caller passes an explicit expiresAt (nil = never expires, the
	// admin contract); the five-arg CreateAPIKey stays for the 17+
	// test/handler sites that don't care about expiry. Production
	// apid.createKey uses this new shape so the dashboard sees
	// expires_at on every fresh non-admin key.
	//
	// Issue #189 / IAM-5.
	CreateAPIKeyWithExpiry(ctx context.Context, accountID string, hash []byte, label string, scopes []string, expiresAt *time.Time) (APIKey, error)
	// CreateAPIKeyWithExpiryAndProvenance is the IAM-hardening-mega-PR
	// (logical change 2) variant of CreateAPIKeyWithExpiry. Used
	// when the legacy /v1/keys POST handler falls back to the
	// account-scoped path (no personal org yet — pre-00127 fixtures).
	// Optional fields: nil/"" → NULL column.
	CreateAPIKeyWithExpiryAndProvenance(ctx context.Context, accountID string, hash []byte, label string, scopes []string, expiresAt *time.Time, createdIP, createdUA string, parent *string) (APIKey, error)

	// CountAPIKeys returns the number of non-revoked keys owned
	// by the account. Used by the create + rotate handlers to
	// enforce limits.KeysMax BEFORE minting a new key. The
	// "non-revoked" filter matches the partial index
	// api_keys_active_grace_idx; the query is O(1) per
	// account in the common case. Returns 0 on a fresh account.
	//
	// Issue #189 / IAM-5.
	CountAPIKeys(ctx context.Context, accountID string) (int, error)

	// MarkAPIKeyRevoked is the soft-delete path for keys
	// (replaces DeleteAPIKeyReturning in the IAM-5 surface).
	// Flips status to 'revoked' and stamps revoked_at if not
	// already set. Repeated calls are idempotent (returns the
	// same row, no error). Returns ErrNotFound when the key
	// doesn't exist OR belongs to a different account.
	//
	// Audit emission is the caller's responsibility; this
	// method is store-only.
	//
	// Issue #189 / IAM-5.
	MarkAPIKeyRevoked(ctx context.Context, accountID, keyID string) (APIKey, error)

	// RotateAPIKey atomically mints a new key (status='active')
	// and demotes the old key in a single transaction. The old
	// key's status flips to 'grace' (when graceWindow > 0) or
	// 'revoked' (atomic rotation) and its expires_at is
	// OVERWRITTEN to now() + graceWindow. The new key inherits
	// label + scopes + account_id from the predecessor; the
	// caller's pre-minted hash is stored directly (no
	// post-step patch).
	//
	// The two returned keys are the post-commit rows in
	// (newKey, oldKey) order. The caller is responsible for
	// surfacing newKey.plaintext (generated upstream) and
	// oldKey.ExpiresAt (the grace deadline) to the API
	// response.
	//
	// Errors:
	//   * ErrNotFound  — old key doesn't exist / wrong account.
	//   * state.ErrAPIKeyRevoked — old key already in 'revoked'
	//     (rotation of a revoked key is a 404, not idempotent).
	//
	// Issue #189 / IAM-5.
	RotateAPIKey(ctx context.Context, accountID, oldKeyID string, newHash []byte, newLabel string, graceWindow time.Duration) (newKey, oldKey APIKey, err error)

	// GetAccountKeyGraceWindow returns the per-account override
	// (accounts.key_grace_window_days). nil = no override; the
	// caller falls through to the plan default (7d). The
	// auth hot path does NOT call this; only the rotate
	// handler does, via a short-TTL in-process cache
	// (cmd/apid.graceWindowCache).
	//
	// Issue #189 / IAM-5.
	GetAccountKeyGraceWindow(ctx context.Context, accountID string) (*int, error)

	// SetAccountKeyGraceWindow sets the per-account override.
	// days == nil clears the override (falls through to plan
	// default). The handler invalidates the in-process
	// graceWindowCache entry after a successful write. The
	// audit `key.grace_window_set` event is emitted by the
	// handler, not the store.
	//
	// Issue #189 / IAM-5.
	SetAccountKeyGraceWindow(ctx context.Context, accountID string, days *int) error

	// GetAccountEgressAllowlistExtra returns the per-account
	// additive budget on top of the plan's apps.egress_allowlist
	// cap (issue #679 / PR-B / ADR-082). 0 = no override; the
	// plan cap is authoritative. The caller is the apid
	// validator at cmd/apid/handlers_ext.go:104 — it adds the
	// returned value to the plan cap before the >-maxSize check.
	//
	// The validator is the only caller; the upstream gate at the
	// admin handler (api.ErrAccountEgressAllowlistExtraOutOfRange)
	// refuses values > api.MaxAccountEgressAllowlistExtra (1024),
	// so a value > 1024 should never reach the store. A future
	// migration that lowers the apid ceiling does NOT need to
	// retro-fit the DB — the apid gate is the source of truth.
	//
	// The auth hot path does NOT call this; only the
	// handlers_ext.go PATCH path does.
	GetAccountEgressAllowlistExtra(ctx context.Context, accountID string) (int, error)

	// SetAccountEgressAllowlistExtra sets the per-account
	// additive budget. n == 0 clears the override (falls through
	// to plan cap). The handler emits the audit
	// `account.egress_allowlist_extra_set` event, not the store.
	//
	// Issue #679 / PR-B / ADR-082.
	SetAccountEgressAllowlistExtra(ctx context.Context, accountID string, n int) error

	// Org-bound API keys (issue #190 / IAM-6, PR 6).
	//
	// The org-scoped counterparts to CreateAPIKeyWithExpiry /
	// MarkAPIKeyRevoked / RotateAPIKey. The org_id is
	// principal.Membership.OrgID from the loadOrg middleware, so
	// the schema-level NOT NULL constraint (migration 00127) is
	// satisfied without a personal-org subquery. The org_id IS
	// the canonical bind; account_id is denormalised for
	// AuthenticateKey compatibility through PR 7.
	//
	// Cross-org read collapse: GetOrgAPIKey and RevokeOrgAPIKey
	// collapse (a) row missing, (b) row exists for a different
	// org to the same ErrNotFound at the SQL level (the WHERE
	// clause pins org_id). This is the IDOR-safe shape — same
	// precedent as DeleteAPIKeyReturning's (a)/(b) collapse; the
	// operator cannot probe other orgs' key ids.
	//
	// CreateOrgAPIKey persists a key against an explicit org.
	// The resulting row carries (account_id, org_id) both stamped
	// — the LoadOrg middleware stamps the active membership onto
	// the principal so the handler has both ready without a
	// round-trip. The legacy /v1/keys POST handler routes
	// through this method with org_id = caller's personal org
	// (the dual-write shape from the plan). The AccountID stamp
	// survives through PR 7 (AuthenticateKey signature bump) so
	// the principal is assembled exactly as it was pre-PR-6.
	//
	// ListOrgAPIKeys returns every non-revoked key for the org
	// (status IN ('active', 'grace')). Ordered by created_at
	// DESC to match ListAPIKeys. Empty for a fresh org.
	//
	// RotateOrgAPIKey mirrors RotateAPIKey but the lock
	// predicate is (id, org_id); rotation is org-local (org_id
	// is inherited onto the new row, never re-derived via
	// subquery — see PgStore implementation). Same graceWindow
	// semantics, same returned-key ordering.
	CreateOrgAPIKey(ctx context.Context, orgID, accountID string, hash []byte, label string, scopes []string, expiresAt *time.Time) (APIKey, error)
	// CreateOrgAPIKeyWithProvenance is the IAM-hardening-mega-PR
	// (logical change 2) variant. createdIP / createdUA are
	// best-effort client IP + User-Agent from the minting request;
	// parent is the optional FK to a predecessor key (distinct
	// from the rotation-internal rotated_from_id column).
	// Optional fields: nil/"" → column is NULL.
	CreateOrgAPIKeyWithProvenance(ctx context.Context, orgID, accountID string, hash []byte, label string, scopes []string, expiresAt *time.Time, createdIP, createdUA string, parent *string) (APIKey, error)
	ListOrgAPIKeys(ctx context.Context, orgID string) ([]APIKey, error)
	GetOrgAPIKey(ctx context.Context, orgID, keyID string) (APIKey, error)
	RevokeOrgAPIKey(ctx context.Context, orgID, keyID string) (APIKey, error)
	RotateOrgAPIKey(ctx context.Context, orgID, oldKeyID string, newHash []byte, newLabel string, graceWindow time.Duration) (newKey, oldKey APIKey, err error)
	// RotateOrgAPIKeyWithProvenance is the IAM-hardening-mega-PR
	// (logical change 2) variant of RotateOrgAPIKey. createdIP /
	// createdUA / parent are the provenance columns stamped on the
	// new key row. The old key's existing rotated_from_id is
	// unaffected (the rotation-internal stamp is a separate
	// lineage from the optional parent_key_id).
	RotateOrgAPIKeyWithProvenance(ctx context.Context, orgID, oldKeyID string, newHash []byte, newLabel string, graceWindow time.Duration, createdIP, createdUA string, parent *string) (newKey, oldKey APIKey, err error)

	// Consumer keys (ADR-120 / issue #975 item #5).
	//
	// Identity primitive for the *application's* customers (distinct
	// from APIKey, which is the operator's credential). Scoped to a
	// single (accountID, appID) pair: a leaked key affects only one
	// app, not the whole account. Wire format
	// `ck_<prefix>_<secret>` — see pkg/api/apikey.go and the ADR.
	//
	// Tenancy-at-the-boundary: every read method takes accountID
	// and pins it in the SQL WHERE (or the memstore filter). Cross-
	// tenant probes collapse to ErrNotFound — same precedent as
	// api_keys. IDOR safety is the SQL floor, not a post-hoc
	// caller-side compare.
	//
	// CreateConsumerKey persists a new key row. The caller supplies
	// the prefix + hash (already generated by pkg/api.Generate-
	// ConsumerKey); the store does NOT mint keys. Scopes is the
	// closed-set {read, write, admin} from migration 00329's CHECK
	// — the apid write path validates before this is called.
	// expiresAt is nullable: nil = never expires (rare; the
	// customer-of-an-app convention is "set an expires_at"). The
	// DB CHECK expires_at > created_at is the floor; callers that
	// pass expiresAt <= now will fail the insert.
	CreateConsumerKey(ctx context.Context, accountID, appID, name, prefix string, hash []byte, scopes []string, expiresAt *time.Time) (ConsumerKey, error)
	// GetConsumerKeyByID returns the row keyed by id, scoped to
	// accountID. IDOR-safe: cross-account lookups return ErrNotFound.
	// A revoked key is still returned (the caller decides whether
	// to honour it — typically the gatewayd-internal middleware
	// checks RevokedAt against now and rejects).
	GetConsumerKeyByID(ctx context.Context, accountID, keyID string) (ConsumerKey, error)
	// ListConsumerKeysForApp returns every key for one app,
	// including revoked ones (operator's audit view). Ordered by
	// created_at DESC. accountID is pinned in the WHERE; the
	// caller has already authorised the (account, app) pair via
	// the standard /v1/apps/{slug} auth path.
	ListConsumerKeysForApp(ctx context.Context, accountID, appID string) ([]ConsumerKey, error)
	// RevokeConsumerKey stamps revoked_at = now() and returns the
	// updated row. Idempotent: a second call returns the already-
	// revoked row with ErrNotFound only if the key never existed
	// for the account. No hard delete — revocation is the audit-
	// trail-preserving primitive (ADR-120 §D5).
	RevokeConsumerKey(ctx context.Context, accountID, keyID string) (ConsumerKey, error)
	// TouchConsumerKeyLastUsed stamps last_used_at = now(). Called
	// fire-and-forget by the gatewayd-internal middleware with a
	// 60s per-key debouncer (ADR-120 §Consequences — best-effort
	// observability, NOT a billing signal). A no-op for revoked
	// keys (caller-side skip; the store does NOT filter).
	TouchConsumerKeyLastUsed(ctx context.Context, keyID string) error
	// ConsumerKeyByAppAndPrefix is the gateway-side hot-path lookup.
	// Called once per inbound request with a `ck_<prefix>_<secret>`
	// header. Returns ErrNotFound when (appID, prefix) doesn't
	// match — the middleware then collapses to 401 (NOT 403, to
	// avoid leaking existence). accountID is pinned in the WHERE
	// so a foreign-prefix probe can't enumerate another tenant's
	// apps. The hash compare happens at the call site (constant-
	// time equal — see api.ConstantTimeEqualHash).
	ConsumerKeyByAppAndPrefix(ctx context.Context, accountID, appID, prefix string) (ConsumerKey, error)

	// Login tokens (M7.5 magic-link, spec §14 + ADR-011).
	//
	// IssueLoginToken persists a freshly-minted token's SHA-256 hash
	// with an expiry; the raw token is returned to the caller to
	// embed in the email. ConsumeLoginToken marks the token consumed
	// AND returns the bound account_id in a single statement so a
	// replay returns ErrNotFound (or sql.ErrNoRows) — never a stale
	// account. The DeleteOldLoginTokens helper is a maintenance call
	// (the dashboard backend or a daily cron can prune).
	IssueLoginToken(ctx context.Context, tokenHash []byte, accountID string, expiresAt time.Time) error
	ConsumeLoginToken(ctx context.Context, tokenHash []byte) (string, error)
	DeleteOldLoginTokens(ctx context.Context, before time.Time) (int64, error)

	// Audit events (IAM-4 / ADR-035, retention ADR-075). The
	// events table grows ~3-4 GB/year per active-tier customer
	// through the auth / key / secret / account / stateless
	// audit namespaces plus the future wake-timeline / sidecar
	// surfaces. The retention trim is a daily cron (wired by
	// pkg/eventretention); the Store method is the primitive it
	// calls. SOC 2 CC6.2 evidence-retention floor is 90 days
	// (pkg/eventretention.DefaultCutoffDays).
	//
	// The (subject, at desc) partial index on the events table
	// keeps the DELETE bounded by (cutoff × recent rows); the
	// AppendEvent / ListEvents paths exercise the same shape.
	DeleteOldEvents(ctx context.Context, before time.Time) (int64, error)

	// Dashboard sessions (IAM-3, issue #187 + #244 merged). One row per
	// successful dashboard login; the cookie envelope carries the row's
	// id as `sid`. Bearer API keys never touch this table (the cookie
	// branch of s.auth is the only caller). Revocation is `revoked_at !=
	// nil`; the revocation methods are account-scoped at the SQL level so
	// IDOR is a persistence invariant — a cross-account revoke returns
	// false (the handler maps to 404), never 403.
	//
	// CreateSession persists a new row for the freshly-minted sid.
	// Caller has already generated the uuid (the envelope seal needs the
	// same value). Returns ErrConflict when the sid already exists (uuid
	// collision — astronomically rare; the handler surfaces 500).
	//
	// GetSession looks up by primary key. Returns ErrNotFound when the
	// row is gone (sid was never issued OR was DELETEd by an op); the
	// apid cookie branch maps that to 401 CodeSessionExpired.
	//
	// RevokeSession atomically stamps revoked_at = now() iff (a) the row
	// exists, (b) it belongs to accountID, and (c) it is not already
	// revoked. Returns true on a real write, false on a no-op
	// (already-revoked / wrong-account / missing). The boolean lets the
	// handler map false to 404 without leaking cross-account existence.
	// Idempotent on a same-sid repeat call.
	//
	// ListSessions returns every active row for accountID, newest first.
	// Used by GET /v1/auth/sessions so the dashboard can render "this
	// is my phone / my laptop".
	//
	// RevokeAllSessions revokes every active row for accountID except
	// the supplied sid (the calling session). Returns the count of
	// rows affected. Idempotent (a repeat call with the same currentSid
	// returns 0 once siblings are gone).
	//
	// exceptID MUST be a real sid — the method does NOT special-case
	// the empty string. A caller that passes exceptID="" will revoke
	// every active session for accountID, including the caller's own
	// (because SQL `id <> ''` is true for every uuid). The handler
	// always passes the validated sid read out of the request context
	// (sessionFrom(r) at session_middleware.go); a future caller that
	// doesn't have the sid should pass a placeholder and let the
	// revocation proceed, NOT pass "" with intent-to-skip.
	//
	// TouchSessionLastSeen stamps last_seen_at = now(). Best-effort fire-
	// and-forget on the apid cookie branch (5-minute debounce) — failures
	// are logged but never reject the request. Touch is allowed on
	// revoked rows (observability-only signal, not authorization).
	CreateSession(ctx context.Context, id, accountID, issuedIP, issuedUA string) (Session, error)
	// CreateSessionWithBinding is the IAM-hardening-mega-PR
	// (logical change 5) variant. The bindingHash parameter is
	// the HMAC-SHA256 fingerprint of (ip, ua_family) — the same
	// value the cookie envelope stamps. Empty string → NULL
	// column (the unix-socket / CLI-auth code path has no
	// meaningful fingerprint).
	CreateSessionWithBinding(ctx context.Context, id, accountID, issuedIP, issuedUA, bindingHash string) (Session, error)
	// UpdateSessionBinding re-stamps the sessions.binding_hash
	// column on an existing live row. Called by
	// reissueSessionCookieWithStepUp (cmd/apid/handlers_mfa.go)
	// so the cookie envelope's binding_hash and the sessions
	// row's binding_hash stay in lockstep across /v1/account/mfa/
	// {confirm,verify,recover,disable}. The middleware
	// cross-check (pkg/auth/middleware/middleware.go
	// RequireSessionCookie step 3.5) compares the two; without
	// this update the row still carries the original mint's
	// fingerprint and every post-reissue request trips the
	// stolen-cookie auto-revoke branch on its own session.
	//
	// accountID is the IDOR guard: a cross-account update
	// returns ErrNotFound (the handler maps to 401
	// CodeSessionInvalid, byte-identical to a missing row).
	// Returns ErrNotFound when no live (non-revoked) row
	// matches the (id, accountID) pair.
	UpdateSessionBinding(ctx context.Context, id, accountID, bindingHash string) error
	GetSession(ctx context.Context, id string) (Session, error)
	RevokeSession(ctx context.Context, id, accountID string) (bool, error)
	ListSessions(ctx context.Context, accountID string) ([]Session, error)
	RevokeAllSessions(ctx context.Context, accountID, exceptID string) (int, error)
	TouchSessionLastSeen(ctx context.Context, id string) error

	// Account passwords (issue #165 / ADR-032 PR #2). The Argon2id
	// PHC hash lives in the account_passwords side table; OAuth-only
	// accounts have no row. SetAccountPassword upserts the hash
	// (overwriting any prior hash for the same account — the
	// password-set path). AccountPasswordByAccountID returns the
	// stored hash or ErrNotFound (the postLogin handler uses the
	// ErrNotFound branch as the trigger for the anti-enumeration
	// Argon2id pad). DeleteAccountPassword removes the row (used by
	// the G6 hard-delete path on account removal and reserved for a
	// future "switch to OAuth-only" opt-out).
	SetAccountPassword(ctx context.Context, accountID, phc string) error
	AccountPasswordByAccountID(ctx context.Context, accountID string) (string, error)
	DeleteAccountPassword(ctx context.Context, accountID string) error

	// OAuth links (issue #165 / ADR-032 PR #2). The (provider,
	// provider_subject) composite primary key is the §11
	// anti-takeover invariant: one OAuth subject binds to exactly
	// one account, period. UpsertOAuthLink is the "first party to
	// bind a sub owns the row" path: a re-bind by the same account
	// updates the email/email_verified snapshot, a re-bind by a
	// different account is rejected by the PK at the database floor.
	// OAuthLinkByProviderSubject is the sub-first lookup the OAuth
	// callback runs on every handshake to decide whether the
	// incoming sub belongs to an existing account or whether to
	// create a fresh one.
	UpsertOAuthLink(ctx context.Context, accountID, provider, providerSubject, email string, emailVerified bool) error
	OAuthLinkByProviderSubject(ctx context.Context, provider, providerSubject string) (OAuthLink, error)

	// CLI auth codes (spec §2.2 device-code flow). The mint + peek +
	// claim + consume cycle mirrors the magic-link primitives but with
	// a nullable account_id — the binding to a customer happens at
	// claim time (dashboard POST /cli-auth), not at mint time
	// (anonymous POST /v1/cli-auth/code).
	//
	// IssueCliAuthCode persists a freshly-minted code's SHA-256 hash
	// with no account (account_id NULL). PeekCliAuthCode returns the
	// row's status without mutating it (the dashboard render uses
	// this). ClaimCliAuthCode atomically transitions pending →
	// consumed and binds account_id in one statement; a racing second
	// claim returns ErrConflict. ConsumeCliAuthCode is the CLI's poll
	// path: returns (status, account_id, err) so the CLI can mint the
	// API key once it sees "consumed".
	IssueCliAuthCode(ctx context.Context, tokenHash []byte, expiresAt time.Time) error
	PeekCliAuthCode(ctx context.Context, tokenHash []byte) (api.CliAuthStatus, string, error)
	ClaimCliAuthCode(ctx context.Context, tokenHash []byte, accountID string) error
	ConsumeCliAuthCode(ctx context.Context, tokenHash []byte) (api.CliAuthStatus, string, error)

	// Apps (apid is the only writer, spec §Component ownership).
	CreateApp(ctx context.Context, app App) (App, error)
	// CreateAppIfUnderQuota inserts app iff the account currently holds
	// fewer than limits.DeployedApps live apps (active + evicted_cold).
	// The count + insert happen under a single critical section — PgStore
	// opens a transaction that SELECT … FOR UPDATE locks the parent
	// accounts row, MemStore holds m.mu — so two concurrent createApp
	// calls on a Free account cannot both pass the cap check (spec §4.2,
	// PR fix for the TOCTOU in handlers.go::createApp).
	//
	// Returns:
	//   - (App, nil) on success
	//   - (App{}, *QuotaError) when the cap is reached — handlers map
	//     this to 403 CodePlanLimitApps with limit + observed
	//   - (App{}, ErrConflict) when app.Slug is already taken
	//   - (App{}, ErrNotFound) when the account row is gone
	//   - (App{}, other) on transport / SQL errors
	//
	// limits.DeployedApps is the per-plan cap (api.MustLimitsFor(plan)).
	// Implementations enforce it authoritatively; callers MUST NOT also
	// call CountDeployedApps before this method (that's the bug).
	CreateAppIfUnderQuota(ctx context.Context, app App, limits api.Limits) (App, error)
	AppByID(ctx context.Context, id string) (App, error)
	// PreviewAppsByParent (ADR-095 / issue #272) lists every
	// preview app whose preview_of_slug matches the parent. Used
	// by the dashboard's "preview environments" pane. Results are
	// ordered by created_at DESC so the dashboard's newest-first
	// display is free. Returns an empty slice (not an error) when
	// the parent has no previews.
	//
	// Soft-deleted rows are filtered out — the teardown janitor's
	// tombstone-aware sweep uses ListPreviewsForTeardown instead.
	PreviewAppsByParent(ctx context.Context, accountID, parentSlug string) ([]App, error)
	// ListPreviewsForAccount (Mega-C PR-1 / issue #961 leaf 3) lists
	// every non-deleted preview row for the account, across all
	// parents. Backs the new /dashboard/previews page (a global
	// "all open PRs" view that complements the per-app preview
	// panel). Ordered by created_at DESC so the dashboard's
	// newest-first display is free.
	//
	// Returns an empty slice (not an error) when the account has
	// no previews. Production apps (preview_of_slug IS NULL) are
	// filtered out — this is a preview-only view.
	ListPreviewsForAccount(ctx context.Context, accountID string) ([]App, error)
	// ListPreviewsForTeardown (ADR-095 PR-C / issue #272) returns
	// preview rows the teardown janitor should consider this tick:
	// every non-torn_down preview that is either in a terminal-ish
	// PR state (closed / stale) or past its preview_expires_at TTL.
	//
	// Deliberately NOT filtered on status <> 'deleted': the janitor
	// is the component that sets status='deleted', and it must be
	// able to observe rows it has already tombstoned so a crash
	// between the tombstone write and the preview_pr_state write
	// is recoverable on the next tick. Callers that want the live
	// customer-facing view want PreviewAppsByParent instead.
	//
	// `now` is passed rather than read from the DB clock so the
	// janitor's test seam (and the e2e TTL case) can drive expiry
	// without waiting 7 real days. maxPerTick bounds the sweep;
	// <1 returns nil so a misconfigured caller is a no-op rather
	// than a full-table scan. Ordered by preview_expires_at ASC
	// (nulls last) so the most overdue rows are reaped first.
	ListPreviewsForTeardown(ctx context.Context, now time.Time, maxPerTick int) ([]App, error)
	// SetPreviewPrState (ADR-095 PR-C / issue #272) advances one
	// preview row's lifecycle label. Returns the updated row, or
	// ErrNotFound when no row matches the id.
	//
	// The value MUST satisfy PreviewPrStateIsValid — the SQL CHECK
	// apps_preview_pr_state_chk is the authoritative gate, but
	// implementations reject invalid values before touching the DB
	// so a typo surfaces as a Go error rather than SQLSTATE 23514.
	// Only preview rows (preview_of_slug <> '') are eligible; a
	// production app id is ErrNotFound, so a bug in the janitor's
	// query can never relabel a customer's live app.
	SetPreviewPrState(ctx context.Context, appID, prState string) (App, error)
	// StampPreviewDestroyCommentedAt (Mega-C PR-1 / issue #961
	// leaf 3) records that the one-click PR comment destroy hint
	// was posted to GitHub for this preview row. githubd's
	// previewCommentOnce helper calls this after a successful
	// POST so a closed → reopen → closed cycle does not spam
	// the customer with duplicate comments.
	//
	// Only preview rows (preview_of_slug <> '') are eligible; a
	// production app id is ErrNotFound. The stamp is idempotent:
	// re-stamping the same timestamp is a no-op (the column value
	// is the dedupe key, not the row identity).
	StampPreviewDestroyCommentedAt(ctx context.Context, appID string, when time.Time) (App, error)
	// StampFirstWake (Mega-C PR-2 / issue #961 leaf 8) sets
	// first_wake_at + first_5xx_window_ends_at on the
	// deployment iff both are NULL. The window ends_at is
	// derived as now + windowMinutes (constant
	// pkg/api.RollbackOn5xxWindowMinutes). Idempotent: a
	// second call within the window leaves the values
	// unchanged. Returns ErrNotFound when the deployment does
	// not exist.
	StampFirstWake(ctx context.Context, deploymentID string, windowMinutes int) (Deployment, error)
	// BumpFirst5xxCount atomically increments the per-deploy
	// first_5xx_count and returns the new value. Atomic via
	// UPDATE ... RETURNING first_5xx_count (PG) / mutex-guarded
	// map mutation (MemStore). schedd's threshold check is
	// post-increment: caller compares the returned count to
	// plan.RollbackOn5xxThreshold().
	BumpFirst5xxCount(ctx context.Context, deploymentID string) (int, error)
	// MarkAutoRollback stamps last_auto_rollback_at + the
	// closed-set reason (see deployments_last_auto_rollback_reason_check
	// from migration 00297). Called by the apid-internal
	// auto-rollback handler after the deployments status swap
	// commits. The two writes (status swap + this stamp) live
	// in the same transaction; the audit row that carries
	// trigger="auto_5xx" is the customer-visible signal.
	MarkAutoRollback(ctx context.Context, deploymentID, reason string, when time.Time) (Deployment, error)
	// AutoRollbackDeploymentsTx wraps the deployments status
	// swap in a tx: (a) current live → superseded, (b) most
	// recent superseded → live, (c) markAutoRollback on the
	// rolled-back id. Returns the new live deployment id (the
	// one schedd needs to park instances for). The instances
	// mutation belongs to schedd per CLAUDE.md — this tx
	// does NOT touch instances; schedd does that in a
	// sibling call after this returns.
	AutoRollbackDeploymentsTx(ctx context.Context, appID, currentDeploymentID string) (newLiveDeploymentID string, err error)
	AppBySlug(ctx context.Context, slug string) (App, error)
	ListApps(ctx context.Context, accountID string) ([]App, error)
	// ListAllApps returns every non-deleted app on the box. schedd's reaper and
	// cron loops walk this (one-box scale, spec §4.3); apid never calls it.
	// Phase 2 / Gate A: the schedd-owned variants (ListAppsByNodeID /
	// ListInstancesByNodeID / ListOwnedCronsByNodeID) replace this in the
	// reaper / cron / watchdog loops so an N-schedd fleet does N× the
	// row work. ListAllApps remains for apid read-only tooling and the
	// legacy one-box schedd posture.
	ListAllApps(ctx context.Context) ([]App, error)
	// ListAppsByNodeID returns every non-deleted app whose owner_node
	// matches nodeID (Phase 2 / Gate A, migration 00090). The schedd
	// that resolves to nodeID at startup calls this on every reaper /
	// scale-up tick. Default-local is a valid nodeID — the single-box
	// schedd reads the same shape as a peer schedd.
	ListAppsByNodeID(ctx context.Context, nodeID string) ([]App, error)
	// ListAllDeployments returns every non-deleted deployment (issue
	// #557 closure / ADR-072). The floor reconciler's wake sweep walks
	// this when no owner-node sharding is configured (the one-box
	// posture). Multi-box schedd callers read ListDeploymentsByNodeID.
	ListAllDeployments(ctx context.Context) ([]Deployment, error)
	// ListDeploymentsByNodeID returns every deployment whose parent
	// app's owner_node matches nodeID (Phase 2 / Gate A + issue #557
	// closure). Mirror of ListAppsByNodeID for the per-deployment
	// axis. The JOIN through apps lets the planner hit apps_node_id_idx
	// first; per-app deployments fall out via the deployments_app_id_idx
	// index added in migration 00007.
	ListDeploymentsByNodeID(ctx context.Context, nodeID string) ([]Deployment, error)
	// ConcurrencyForDeployment returns the live-instance count for a
	// (app, deployment) pair — the sum of state IN ('RUNNING',
	// 'WAKING', 'COLD_BOOTING'). Backed by the partial index added
	// in migration 00132.
	ConcurrencyForDeployment(ctx context.Context, appID, deploymentID string) (int, error)
	// UpdateDeploymentMinInstances stamps the per-deployment cold-wake
	// floor (issue #557 closure / ADR-072). The handler validates
	// against the parent app's plan ceiling before reaching this
	// method; the DB CHECK constraint (migration 00131) is the
	// belt-and-suspenders bound. Returns the fresh row so the
	// handler can build the response without a second round-trip.
	UpdateDeploymentMinInstances(ctx context.Context, id string, min int) (Deployment, error)
	// SetDeploymentParked stamps the per-deployment parked_reason +
	// parked_at columns introduced by migration 00157 (issue #554 /
	// ADR-079 follow-up). Idempotent: re-parking an already-parked
	// deployment is a no-op (the parked_at timestamp is set once).
	// The closed-set vocabulary is enforced at the schema layer via
	// the deployments_parked_reason_check constraint — callers
	// passing a non-literal are rejected with the standard CHECK
	// violation. Returns ErrNotFound when the deployment id is
	// unknown so callers can disambiguate "parked" from "absent".
	SetDeploymentParked(ctx context.Context, id, reason string, at time.Time) error
	// LatestParkedDeploymentForApp returns the most recently parked
	// deployment for an app, or ErrNotFound if none. Powers the
	// apid GET /v1/apps/{slug}.parked_deployment reference (AC #3
	// wire). The match is `parked_reason is not null order by
	// parked_at desc limit 1`; superseded deployments still match
	// since their parked_reason/parked_at are not cleared on
	// supersede.
	LatestParkedDeploymentForApp(ctx context.Context, appID string) (Deployment, error)
	// ListInstancesByNodeID returns every instance whose owning app's
	// owner_node matches nodeID. Same Phase 2 / Gate A contract; the
	// reaper's parked-instance timer + the watchdog's kill-stuck path
	// both want "instances I'm responsible for" rather than the
	// fleet-wide ListAllInstances.
	ListInstancesByNodeID(ctx context.Context, nodeID string) ([]Instance, error)
	// ListInstancesOnNodeID returns every instance whose physical
	// instances.node_id matches nodeID, regardless of the owning app's
	// scheduler owner. This is the safety/observability view for drains:
	// live migration updates instances.node_id before apps.node_id, so an
	// ownership-scoped query can otherwise miss a still-running VM on the
	// node being drained.
	ListInstancesOnNodeID(ctx context.Context, nodeID string) ([]Instance, error)
	// ListInstancesForLifecycleReconciliation returns live instances whose
	// parent app or account is in a deletion state. It is the durable fallback
	// for pg_notify: schedd uses it to destroy VMs after a missed notification
	// or a restart. A non-empty nodeID scopes the result to apps owned by that
	// node; limit <= 0 returns no rows.
	ListInstancesForLifecycleReconciliation(ctx context.Context, nodeID string, limit int) ([]Instance, error)
	// ListOwnedCronsByNodeID returns every cron whose owning app's
	// owner_node matches nodeID. The cron dispatcher runs once per
	// node and only fires crons on apps it owns; without this filter
	// every schedd would fire every cron and the cron_fired_audit row
	// would diverge from the actual dispatch.
	ListOwnedCronsByNodeID(ctx context.Context, nodeID string) ([]Cron, error)
	// ListUnplacedApps returns every non-deleted app whose node_id is
	// NULL — the input set for schedd's PlacementClaimSubscriber
	// (pkg/sched/placement_claim.go, Phase 2 / Gate A migration 00091).
	// The post-00091 schema allows node_id NULL at insert time so apid
	// can INSERT a fresh app with the owner undecided; schedd races to
	// stamp the owner on NotifyAppChanged "created". This method is the
	// cold-start sweep path that handles a schedd that was down while a
	// notify landed (pg_notify is fire-and-forget; missed events
	// surface as NULL-row apps at the next start).
	ListUnplacedApps(ctx context.Context) ([]App, error)
	// SetAppNodeID atomically claims the owner for an unplaced app.
	// The UPDATE is conditional on node_id IS NULL so exactly one
	// schedd wins the race; losers receive ErrConflict and the
	// subscriber drops silently. Returns ErrNotFound when the app row
	// is gone (hard-deleted between notify and claim — possible on the
	// M7 path; the subscriber treats this as a no-op drop). The FK +
	// empty-uuid CHECK on apps.node_id reject bad values via the
	// existing 23503 / 23514 paths.
	SetAppNodeID(ctx context.Context, appID, nodeID string) error
	// ListOrphanedApps returns every active/evicted_cold app whose
	// node_id points at a compute_node with active=false — the input
	// set for
	// schedd's rebalancer (pkg/sched/rebalancer.go, Tier A4 migration
	// 00092). Used by both the live compute_node_changed watcher (which
	// filters by deadNodeID in memory) and the cold-start sweep (which
	// scans every dead node at schedd boot — pg_notify is fire-and-
	// forget; a schedd down while a drain event landed recovers via
	// this path). Cooldown + per-tick cap are bound as parameters so
	// the live watcher and cold-start sweep can use different cadences
	// if needed; the rebalancer's caller passes
	// api.RebalanceCooldownSeconds and api.RebalanceMaxPerTickPerNode
	// (constants in pkg/api/limits.go).
	ListOrphanedApps(ctx context.Context, cooldownSeconds, maxPerTick int) ([]App, error)
	// ReassignAppOwner atomically transfers app ownership from
	// fromNodeID to toNodeID. Tier A4 / migration 00092 — the
	// conditional UPDATE that closes the Phase-2 follow-up "apps
	// pinned to a dead node" gap (ADR-062 §"Open follow-ups"). The
	// UPDATE stamps reassigned_at = now() in the same statement so
	// the two columns stay coherent. Returns ErrConflict on
	// RowsAffected()==0 — peer already won the race, app moved to
	// a non-active/non-evicted_cold status, or the row is gone. The
	// fromNodeID predicate is load-bearing: a peer-claim-then-
	// second-peer-claim race never silently succeeds with a stale
	// from-node.
	ReassignAppOwner(ctx context.Context, appID, fromNodeID, toNodeID string) error

	// ListLiveInstancesOnNode returns every live instance owned by
	// nodeID — the candidate set for Engine.MigrateLiveInstances
	// (Tier A5 / migration 00097, ADR-066, follow-up to ADR-064).
	// "Live" is the canonical predicate at IsLive: state ∈
	// {WAKING, COLD_BOOTING, RUNNING, SNAPSHOTTING}. Sorted by
	// instance id ASC for determinism so two peers observing the
	// same drain event migrate the same set (they still race on
	// MigrateInstanceOwner; this list is just the input set).
	// Caller-side filter: deadNodeID == "" returns every live
	// instance whose owning compute_node is inactive (the cold-
	// start sweep variant). maxPerTick caps the result set; the
	// caller passes api.MigrateLiveMaxPerTick
	// (pkg/api/limits.go).
	ListLiveInstancesOnNode(ctx context.Context, nodeID string, maxPerTick int) ([]Instance, error)
	// MarkInstanceMigrating is the Phase-2 atom of the four-phase
	// handoff (Tier A5 / ADR-066). Transitions the instance to
	// state='migrating' under a conditional UPDATE that requires
	// state='running' (the only state eligible for live migration;
	// WAKING/COLD_BOOTING stay where they are and the dying vmmd
	// drives the cold-boot to completion on the dead node) and
	// requires node_id = currentNodeID (the dying vmmd — a
	// concurrent owner change from a peer rebalancer would have
	// flipped the row already, in which case we abort). The
	// transition stamps state='migrating' atomically AND stamps
	// lease_token = the per-migration UUID the new owner minted
	// at Phase 1 — the rollback predicate (CancelInstanceMigration)
	// requires the lease_token match, so the stamp has to land at
	// Phase 2 (before any Phase-3 failure could strand the row in
	// 'migrating' without a lease). A peer claim that races us
	// sees the new state and bails. Returns ErrConflict on
	// RowsAffected()==0 — peer already moved the instance to a
	// non-RUNNING state, owner changed, or row gone.
	MarkInstanceMigrating(ctx context.Context, instanceID, currentNodeID, leaseToken string) error
	// MigrateInstanceOwner is the Phase-3 commit of the four-phase
	// handoff (Tier A5 / ADR-066). Conditional UPDATE that flips
	// instances.node_id from fromNodeID to toNodeID, stamps
	// migrated_from_node_id = fromNodeID, stamps
	// migrated_at = now(), writes lease_token (the per-migration
	// UUID the new owner minted at Phase 1), transitions state
	// from 'migrating' back to 'running', AND stamps
	// apps.migrated_at = now() in the same transaction so the
	// app-level lineage column stays coherent with the instance
	// row. The conditional predicates are load-bearing:
	//   1. state = 'migrating' (a peer rollback would have moved
	//      the row back to 'parked' already)
	//   2. node_id = fromNodeID (a peer re-owner would have
	//      flipped this already)
	//   3. lease_token = leaseToken (a stale lease can never
	//      silently commit; the new owner must present the
	//      same UUID it minted at Phase 1)
	// Returns ErrConflict on RowsAffected()==0 — peer rollback,
	// peer re-owner, lease expiry, or row gone.
	MigrateInstanceOwner(ctx context.Context, instanceID, fromNodeID, toNodeID, leaseToken string) error
	// CancelInstanceMigration is the Phase-4 rollback of the
	// four-phase handoff (Tier A5 / ADR-066). Conditional UPDATE
	// that transitions the instance back from 'migrating' to
	// 'parked' on the original owner (the dying vmmd resumes the
	// VM, the snapshot stays where it was). Predicates:
	//   1. state = 'migrating'
	//   2. node_id = originalNodeID (the dying vmmd — the
	//      rollback is owner-local; a peer commit racing us
	//      would have flipped node_id already)
	//   3. lease_token = leaseToken (stale-lease safety)
	// Returns ErrConflict on RowsAffected()==0 — peer already
	// committed (no rollback needed), lease expired, or row
	// gone. The store also clears the lease_token on rollback
	// so a future re-attempt at migration mints a fresh one.
	CancelInstanceMigration(ctx context.Context, instanceID, originalNodeID, leaseToken string) error

	// ListExpiredMigrations returns every instance row in
	// state='migrating' (Tier A6 / ADR-067). The watchdog is
	// the only writer that can move a row out of 'migrating'
	// without a peer commit, so the unresolved row is the
	// input set. Sorted by instance id ASC for determinism.
	// maxPerTick caps the result set (the caller passes
	// api.MigratingWatchdogTickLimit via pkg/api/limits.go).
	// Returns an empty slice (not ErrNotFound) when no rows
	// match; callers treat that as "nothing to reconcile this
	// tick".
	ListExpiredMigrations(ctx context.Context, maxPerTick int) ([]Instance, error)
	// ReinviteMigratingInstance is the active-owner ack gate of
	// the Tier A6 / ADR-067 watchdog. Conditional UPDATE that
	// flips state='migrating' → 'running', stamps
	// migrated_at = now(), and clears lease_token — the same
	// work the A5 Phase-3 commit (MigrateInstanceOwner) does,
	// but launched by the watchdog after a re-invite to the
	// new owner vmmd. The conditional predicates are load-
	// bearing:
	//   1. state = 'migrating' (peer rollback would have moved
	//      back to 'parked' already)
	//   2. lease_token = leaseToken (the new owner must present
	//      the same UUID it minted at Phase 1; a stale lease
	//      can never silently commit)
	// Returns ErrConflict on RowsAffected()==0 — peer already
	// committed, peer rolled back, lease expired, or row gone.
	ReinviteMigratingInstance(ctx context.Context, instanceID, leaseToken string) error
	// AbortMigratingInstance is the dead-owner hard-delete gate
	// of the Tier A6 / ADR-067 watchdog. Conditional UPDATE
	// that flips state='migrating' → 'parked', restores
	// node_id = migrated_from_node_id (the live, parked state
	// the row was in before Phase 2 of the original handoff),
	// and clears lease_token so a future re-attempt at
	// migration mints a fresh lease. The conditional
	// predicates are the same as ReinviteMigratingInstance:
	//   1. state = 'migrating'
	//   2. lease_token = leaseToken
	// Returns ErrConflict on RowsAffected()==0 — peer already
	// committed, peer rolled back, lease expired, or row gone.
	AbortMigratingInstance(ctx context.Context, instanceID, leaseToken string) error
	// CountDeployedApps counts apps that occupy a deploy slot (active or
	// evicted_cold) for quota enforcement (spec §4.2).
	CountDeployedApps(ctx context.Context, accountID string) (int, error)
	// CountAppsWithEvictionPriority returns the per-account count of
	// apps whose eviction_priority equals the given tier (issue #475).
	// Counts APPS (not instances) — the per-account cap
	// (Plan.ReservedConcurrencyPerAccount) is over apps. Excludes
	// soft-deleted apps so a recently-deleted reserved app doesn't
	// leak into the cap and reject a subsequent recreate.
	CountAppsWithEvictionPriority(ctx context.Context, accountID, priority string) (int, error)
	// CountAuthDefaultFlippedApps returns the per-account count of
	// apps that were stamped by the apps-auth-default grand-father
	// migration (issue #695 / ADR-080). The migration sets
	// auth_default_flipped_at on every pre-flip row; this query
	// reads back the live pre-flip count so the apid dashboard
	// banner can render "your existing N apps were grandfathered"
	// + "your existing 0 apps were grandfathered" naturally turns
	// the banner off (no dismissal cookie — count-zero is the
	// off-switch). Excludes soft-deleted apps so the banner count
	// tracks the customer's actual surface area.
	CountAuthDefaultFlippedApps(ctx context.Context, accountID string) (int, error)
	// AuthDefaultFlippedAt returns the timestamp the
	// apps-auth-default grand-father migration ran (issue #695
	// / ADR-080). The migration emits a single
	// `apps.auth_default_global_flipped` event with the cut-over
	// `at` timestamp; the dashboard banner reads this so the
	// "On YYYY-MM-DD" copy renders the actual cut-over date
	// instead of a hardcoded one. Returns (zero time, nil) when
	// the migration hasn't run yet (banner copy falls back to
	// "Recently" so the surface still works). Returns an error
	// only on transient store failure — apid's call site
	// swallows errors and falls back to the hardcoded date so a
	// store hiccup doesn't 500 the dashboard load.
	AuthDefaultFlippedAt(ctx context.Context) (time.Time, error)
	UpdateApp(ctx context.Context, id string, p UpdateAppParams) (App, error)

	// ProvisionedStaticEgressIPExists (ADR-119 redesign) is the
	// apid-side gate. Returns true iff the (account_id, customer_ip)
	// tuple is in the operator-provisioned set. The vmmd bundle
	// reload writes the table from the operator's TOML on SIGHUP;
	// the apid PUT path reads it here. A false return is the
	// "not provisioned" surface the customer sees as 404 Not
	// Found (api.ErrStaticEgressIPNotProvisioned).
	//
	// Implementation note: the lookup is a single-row PK read
	// against the `(account_id, customer_ip)` composite index.
	// Sub-millisecond under realistic load.
	ProvisionedStaticEgressIPExists(ctx context.Context, accountID string, ip netip.Addr) (bool, error)

	// ReplaceProvisionedStaticEgressIPs (ADR-119 redesign) is the
	// vmmd-side write that mirrors the operator's TOML into the
	// Postgres gate table. The watcher calls this on every SIGHUP
	// (and once at startup). The store clears the table for the
	// given account_id, then inserts the new set inside a single
	// transaction — the visible-state invariant is "either the
	// prior set OR the new set, never a partial mix". Empty
	// `entries` removes all rows for the account (the "revoke
	// provisioning" path).
	//
	// The implementation lives in pgstore.go (SQL via sqlc).
	// MemStore (used in unit tests) provides the same shape.
	ReplaceProvisionedStaticEgressIPs(ctx context.Context, accountID string, ips []netip.Addr) error
	// RenameApp changes an app's slug atomically (issue #63). Returns
	// ErrNotFound if oldSlug doesn't belong to accountID; ErrConflict if
	// newSlug is already taken by another live app. MemStore holds the
	// same unique-slug invariant the Postgres `apps.slug` index enforces.
	RenameApp(ctx context.Context, accountID, oldSlug, newSlug string) (App, error)
	// SetAppMinInstances stamps the per-app floor (ux_spec §6.5) the
	// reaper honors when parking idle instances. 0 => scale to zero.
	// Plan-tier gating is the apid handler's job; the store writes the
	// column unconditionally. Updates an existing row's min_instances
	// column in place; returns ErrNotFound when the app is gone.
	SetAppMinInstances(ctx context.Context, appID string, min int) error
	// SetAppWorkloadClass overwrites apps.workload_class (ADR-050 §3,
	// ADR-051 §"Consequences"). The `source` parameter records who
	// stamped the value ("scan_hint" | "observed" | "manual") and is
	// kept on the audit row emitted by the caller (engine in Phase 4;
	// pkg/reconcile in Phase 5) — the store does NOT emit audit. The
	// apps_workload_class_chk CHECK rejects unknown values; invalid
	// classes surface as ErrInvalidArgument via SQLSTATE 23514. Returns
	// ErrNotFound when the app is gone so a redelivered characterize
	// event returns cleanly.
	SetAppWorkloadClass(ctx context.Context, appID string, class WorkloadClass, source string) (App, error)
	// DeleteApp is the legacy soft-delete entry point used by the apid
	// deleteApp handler. New code (Phase 5 pkg/reconcile) should call
	// SoftDeleteAppCascade directly so it can read the returned App
	// for the workload.removed audit row.
	DeleteApp(ctx context.Context, id string) error
	// SoftDeleteAppCascade marks an app deleted (status='deleted') and
	// returns the freshly-deleted App row. Per Phase 5 user decision
	// the cascade is *status-only*: child rows (app_envs, crons,
	// custom_domains, deployments, instances, invocations) survive
	// for slug-reuse and the GDPR-style hard cascade still lives in
	// DeleteAccount. The name preserves intent for future readers —
	// "Cascade" is a verb (propagate the delete signal) not a
	// guarantee about child row destruction.
	//
	// Returns ErrNotFound when the app id is unknown. Reconciled
	// removes that race with a concurrent re-create rely on the
	// updated_at timestamp: the later caller wins.
	SoftDeleteAppCascade(ctx context.Context, id string) (App, error)

	// Projects (ADR-050, Phase 1).
	//
	// CreateProject persists a new project row. Returns ErrConflict
	// when (account_id, slug) collides (the projects_account_slug_uniq
	// constraint). Implementations are responsible for stamping
	// CreatedAt/UpdatedAt and assigning a non-empty ID.
	//
	// ProjectByID returns ErrNotFound when no project matches id.
	//
	// ProjectBySlug is the dashboard lookup; returns ErrNotFound when
	// no project matches (account_id, slug).
	//
	// ProjectByRepo is the push-dispatch lookup Phase 5 wires through.
	// Returns ErrNotFound when no project owns (install_id,
	// repo_full_name) — installID=0 and repoFullName="" return
	// ErrNotFound, consistent with the partial-unique WHERE clause.
	//
	// ListProjectsForAccount returns every project under the account
	// (sorted by created_at desc) for the dashboard list view.
	//
	// AppsForProject returns the project's member apps in
	// slug-ascending order, filtered to status <> 'deleted'. Cross-
	// account reads return ErrNotFound, mirroring the AppsByAccount
	// precedent so handlers can 404 cleanly without checking which
	// store is in use.
	//
	// SetProjectScanSource updates scan_source monotonically upward
	// (single → convention is the minimum baseline; compose never
	// reverts to convention). On a downgrade returns
	// ErrScanSourceDowngrade. Returns ErrNotFound when the project
	// row is gone. Phase 5's reconciler is the only intended caller
	// in Phase 1.
	//
	// DeleteProject removes a project row by ID. The apps.project_id
	// FK is declared ON DELETE SET NULL (migration 00074:74), so
	// apps already inserted via the reconcile path have their
	// project_id nulled (not the row deleted). Returns ErrNotFound
	// when the project doesn't exist. Used by cmd/apid's
	// scan_service to roll back a half-created project when the
	// subsequent reconcile errors out (PR-GH.6 review H9).
	CreateProject(ctx context.Context, p Project) (Project, error)
	ProjectByID(ctx context.Context, projectID string) (Project, error)
	ProjectBySlug(ctx context.Context, accountID, slug string) (Project, error)
	ProjectByRepo(ctx context.Context, accountID string, installID int64, repoFullName string) (Project, error)
	ListProjectsForAccount(ctx context.Context, accountID string) ([]Project, error)
	AppsForProject(ctx context.Context, accountID, projectID string) ([]App, error)
	SetProjectScanSource(ctx context.Context, projectID string, src ProjectScanSource) (Project, error)
	DeleteProject(ctx context.Context, projectID string) error

	// ApplyProjectPlan persists a project + its member apps + crons
	// in a single transaction. Quota is checked inside the locked
	// critical section; an over-quota call returns *QuotaError with
	// Kind set ("apps" or "crons") and zero rows inserted. The
	// returned slice ordering matches the input — apps[i] pairs with
	// the plan's selected workloads[i]. Crons reference the freshly
	// inserted app IDs through crons[i].WorkloadName lookup.
	//
	// The Cron input is the bare fields the apply handler resolved
	// from reposcan; this method writes crons directly without
	// re-running CreateCronIfUnderQuota (the account-level cap is
	// enforced here, in the same Tx). Returns:
	//   - (Project, []App, []Cron, nil) on success
	//   - (Project{}, nil, nil, ErrConflict) on slug or
	//     (project_id, workload_name) collision
	//   - (Project{}, nil, nil, ErrNotFound) when project.AccountID
	//     does not resolve
	//   - (Project{}, nil, nil, *QuotaError) over-quota
	ApplyProjectPlan(
		ctx context.Context,
		project Project,
		apps []App,
		crons []Cron,
		limits api.Limits,
	) (Project, []App, []Cron, error)

	// RecordGitHubBinding persists the (app → installation_id, repo,
	// branch) tuple after the /oauth/callback handler verified the
	// installation against api.github.com. Idempotent: re-binding the
	// same app overwrites the previous values. Two apps cannot claim
	// the same (install_id, repo) pair — the migration enforces a
	// unique partial index for the §11 least-privilege audit.
	RecordGitHubBinding(ctx context.Context, appID string, installID int64, repoFullName, productionBranch string) error
	// GitHubBindingForApp returns the persisted binding for an app.
	// Returns ErrNotFound if the app has never been GitHub-connected
	// (the zero-value binding with installID==0 is also a miss; callers
	// that need to distinguish "bound to install 0" — impossible per
	// the migration check — from "not bound" should check err).
	GitHubBindingForApp(ctx context.Context, appID string) (GitHubBinding, error)
	// InstallationIDForRepo is the reverse-lookup that closes the
	// review-finding #1+#2 §11 least-privilege regression: githubd's
	// checks.go needs to mint the right per-install access token for
	// the repo's push, not the hardcoded installation_id=1 placeholder
	// that shipped with M7.5. Returns ErrNotFound if no app is bound
	// to (repo). When two apps are bound to the same (install_id,
	// repo) — impossible per the migration unique index — the first
	// hit wins; apid is the canonical owner of bindings so this is
	// not a contention point in practice.
	InstallationIDForRepo(ctx context.Context, repoFullName string) (int64, error)
	// UpsertGithubInstallBinding persists the (account → app →
	// installation, repo, branch) edge with a deterministic
	// bindingID so the dashboard's bind flow is idempotent on retry
	// (PR-B, ADR-012 closure). Returns the bindingID; writes the
	// linked_at timestamp so the dashboard's "connected on" pill
	// has a single source. The (account_id, binding_id) unique
	// partial index (migration 00050) rejects duplicate binds under
	// the same account.
	UpsertGithubInstallBinding(ctx context.Context, b GitHubBinding) error
	// DeleteGithubInstallBinding clears the bind columns on an app.
	// Idempotent: returns nil even if no binding was present
	// (so the dashboard's "unbind" action is safe to retry). Returns
	// ErrNotFound if the app itself doesn't exist.
	DeleteGithubInstallBinding(ctx context.Context, appID string) error
	// GetGithubInstallBindingForApp returns the bind row for an
	// app, scoped by accountID (so a forged session cannot read
	// another tenant's binding). Returns ErrNotFound when the app
	// isn't bound OR the bind belongs to a different account. The
	// pre-PR-B GitHubBindingForApp is account-agnostic; PR-B uses
	// the new method on the apid/githubd read paths.
	GetGithubInstallBindingForApp(ctx context.Context, appID, accountID string) (GitHubBinding, error)
	// GithubInstallBindingForRepoBranch is the inbound-webhook dispatch
	// lookup githubd's push receiver uses to find the owning app.
	// Returns ErrNotFound when no app is bound to (repo, branch).
	// Uses the (repo, branch) partial index added in 00047.
	GithubInstallBindingForRepoBranch(ctx context.Context, repoFullName, productionBranch string) (GitHubBinding, error)
	// ListGithubInstallBindingsForAccount is the dashboard's hydrate
	// path: given an account_id, return every bind the account
	// currently owns. The map is keyed by appID so the dashboard's
	// per-app lookup is O(1). Uses the
	// apps_github_install_account_idx partial index.
	ListGithubInstallBindingsForAccount(ctx context.Context, accountID string) (map[string]GitHubBinding, error)

	// UpsertGitHubInstall persists the OAuth handshake state for one
	// account's GitHub App install (PR-C). Idempotent on (AccountID)
	// via ON CONFLICT (account_id) DO UPDATE so a retry of the OAuth
	// flow doesn't crash on the unique-PK constraint. The account row
	// itself must exist (the FK enforces this); pre-CASCADE deletes
	// of an account surface as ErrNotFound here so the caller can
	// distinguish "account doesn't exist" from a transient write
	// failure.
	UpsertGitHubInstall(ctx context.Context, inst GitHubInstall) error
	// GitHubInstallForAccount returns the durable install row for an
	// account. Used by githubd's cold-start rehydrate path
	// (RealService.ensureInstallToken): when TokenCache is empty
	// (process restart), unseal the SealedToken only if
	// TokenExpiresAt > now()+30s; otherwise the cold path mints a
	// fresh install token and re-seals. Returns ErrNotFound when the
	// account hasn't completed the OAuth handshake yet — the
	// dashboard's bind picker hydrates off this signal to decide
	// whether to render the "Connect GitHub" button vs the bind list.
	GitHubInstallForAccount(ctx context.Context, accountID string) (GitHubInstall, error)

	// UpsertGithubWebhookSecret installs or rotates the per-tenant
	// webhook secret for one GitHub App installation (PR-D / ADR-012
	// §7 amendment). Idempotent on (installation_id): a fresh
	// install INSERTs, a rotation ON CONFLICT DO UPDATE-set's
	// secret_value + now() + upgraded_by. The PlatformSecret
	// fallback at pkg/githubd/webhook_secret.go is unchanged — the
	// row's absence means "use the platform secret" so this is the
	// always-write path, never a delete.
	//
	// Returns the (upgraded_at, upgraded_by) stamp so the apid
	// admin route can echo the row back without a follow-up
	// SELECT.
	UpsertGithubWebhookSecret(ctx context.Context, installationID int64, secret []byte, upgradedBy string) (time.Time, string, error)
	// GetGithubWebhookSecret returns the raw bytes for the per-tenant
	// row, or ErrNotFound if the install hasn't been migrated off the
	// platform secret yet. Used by pkg/githubd's resolver cache;
	// the resolver treats ErrNotFound as a fall-through to
	// FAAS_GITHUB_WEBHOOK_SECRET rather than as an error.
	GetGithubWebhookSecret(ctx context.Context, installationID int64) ([]byte, error)

	// Deployments.
	// CreateDeployment atomically inserts a new pending deployment row
	// for the given app. When the app already has a pending or live
	// deployment row, the SAME transaction flips that prior row's
	// status to 'superseded' before INSERTing the new one. A
	// building/imaging/snapshotting row is left untouched — its
	// pipeline (vmmd VM, builderd, imaged ext4 conversion) is still
	// running and we must not orphan it; the second deploy then
	// creates a parallel row, and schedd's watchdog reaps the loser
	// on the next idle window.
	//
	// Callers that need to surface a NotifyDeploymentChanged for the
	// just-superseded row use LatestDeployment(ctx, appID) AFTER
	// CreateDeployment returns — the in-tx supersede means the prior
	// row is already visible as 'superseded' to the next read. The
	// two-step read avoids turning CreateDeployment into a 3-return
	// signature that breaks every pre-PR-B call site (notably the
	// slice-3 cascade test on main).
	//
	// AppDeleted apps must accept neither deployments nor supersedes;
	// the parent-app gate is the same FOR UPDATE as PR-A's
	// CreateAppIfUnderQuota pattern. The 404 s.notFound path at the
	// apid call site is unchanged.
	CreateDeployment(ctx context.Context, d Deployment) (Deployment, error)
	DeploymentByID(ctx context.Context, id string) (Deployment, error)
	LatestDeployment(ctx context.Context, appID string) (Deployment, error)
	// DeploymentOrdinal (issue #976 / ADR-122 / SAFE-RELEASES-C.2)
	// returns the per-app 1-based ordinal of the deployment row,
	// ordered by (created_at, id). Stable: the same {app_id,
	// deployment_id} pair always resolves to the same ordinal
	// even after later deploys land (the latest deploy is Nth,
	// the one before it is (N-1)th, etc.). Used by the
	// deployment-preview URL surface to stamp the
	// `deploy-{N}.{slug}.gregale.dev` host — N MUST be stable
	// for an existing row across runs so a previously-issued
	// URL doesn't silently rot when the customer deploys a
	// fresh row.
	//
	// Returns ErrNotFound when no row exists for deploymentID
	// (handled by the apid handler with the standard 404 +
	// IDOR posture).
	DeploymentOrdinal(ctx context.Context, appID, deploymentID string) (int, error)
	// LiveDeployment returns the app's current live deployment (status='live').
	// schedd's wake path boots from this; ErrNotFound if the app has never had a
	// successful deploy (an app always has a live snapshot OR a cold-bootable
	// rootfs — never neither, invariant §6.2-3).
	LiveDeployment(ctx context.Context, appID string) (Deployment, error)
	// LiveDeployments (issue #556 / PR-B) returns every live row
	// for the app, ordered created_at DESC. Empty slice (nil, nil)
	// when the app has no live deployments — the gateway's
	// per-deployment weighted picker treats that as "no live
	// deployment, 503". Backed by the partial index
	// deployments_live_traffic_idx (migration 00162); MemStore
	// iterates m.deployments filtered by status='live'.
	LiveDeployments(ctx context.Context, appID string) ([]Deployment, error)
	// ListCanaryInFlight (issue #976 / ADR-122 / SAFE-RELEASES-A)
	// returns every deployment row that is mid-canary: status='live'
	// AND canary_total_steps > 0 AND canary_step < canary_total_steps
	// AND rollout_state IN ('pending','rolling_out'). The orchestrator
	// (pkg/safedeploy) and the canary_progression meterd tick
	// (pkg/canary) both walk this set every tick. Empty slice when no
	// canaries are in flight — both consumers treat (nil, nil) as a
	// no-op.
	ListCanaryInFlight(ctx context.Context) ([]Deployment, error)
	// SafedeployListPendingRollouts (issue #976 / ADR-122 /
	// SAFE-RELEASES-F) returns the orchestrator's walk set: rows
	// whose rollout_state is 'pending' or 'rolling_out' AND
	// canary_total_steps > 0 (rollout_state alone isn't enough —
	// pre-F rows have rollout_state='pending' from the migration
	// fast-default but canary_total_steps=0, meaning no canary
	// ladder was configured and the rollout auto-completes on
	// first wake; the orchestrator skips them). Ordered
	// (rollout_started_at NULLS FIRST, created_at ASC) so a
	// brand-new pending row (no started_at yet) walks before a
	// half-finished rolling_out row — the fair-queue property the
	// operator dashboard relies on.
	SafedeployListPendingRollouts(ctx context.Context) ([]Deployment, error)
	// SafedeployStampRollout (issue #976 / ADR-122 /
	// SAFE-RELEASES-F) is the canonical atomic write the
	// orchestrator uses to transition rollout_state. The
	// (rollout_state, rollout_started_at, rollout_completed_at,
	// rollout_aborted_at, rollout_aborted_reason) tuple must move
	// together — partial writes would leave the row in a
	// half-finished state that the next ListCanaryInFlight walk
	// would re-pick. Implementations MUST take the same FOR
	// UPDATE lock that UpdateDeploymentTraffic uses
	// (deployments.id PK) so concurrent orchestrator ticks don't
	// race on the same row.
	//
	// The audit row is NOT written here — the orchestrator calls
	// AppendDeploymentAudit explicitly so the deployment_audit
	// row carries the orchestrator's actor sentinel, not a
	// generic state.Stamp.
	SafedeployStampRollout(ctx context.Context, id string, state string, startedAt, completedAt, abortedAt *time.Time, abortedReason string) (Deployment, error)
	// FinalizeServiceRollout atomically promotes a readiness-gated service
	// deployment to 100% traffic and supersedes every older live deployment in
	// the same app/scope. The target must be a live zero-step row marked
	// rollout_state='rolling_out'.
	FinalizeServiceRollout(ctx context.Context, id string) (Deployment, error)
	// AbortServiceRollout atomically removes a failed service rollout and
	// restores the newest older live deployment in the same app/scope to 100%
	// traffic. The target must be a live zero-step row marked
	// rollout_state='rolling_out'.
	AbortServiceRollout(ctx context.Context, id, reason string) (Deployment, error)
	// LiveDeploymentForScope (ADR-091 / PR-D) returns the unique
	// newest live deployment for (appID, scope). Stable rows remain
	// unique, while an active canary temporarily overlaps its predecessor
	// (issue #976); the deterministic newest-first read selects the canary
	// for new capacity. Returns ErrNotFound when no live deployment exists
	// for the scope (the wake path should fall back to ErrNoDeployment,
	// surfacing 404 from the gateway). Used by schedd Phase 1 wake when the
	// caller passed an explicit deploymentID: read `dep.Scope` from the
	// resolved deployment, then re-resolve the live row for that scope so
	// the env overlay only contains that scope's rows.
	LiveDeploymentForScope(ctx context.Context, appID, scope string) (Deployment, error)
	// CountLiveInstancesByDeployment returns the number of instances
	// currently in {WAKING, COLD_BOOTING, RUNNING} for the given
	// deployment_id (issue #555 PR-6). The DeploymentCounterWatcher
	// (pkg/sched/deployment_counter_watcher.go) consults this query
	// to detect the "last live instance parked" transition that
	// resets the per-deployment 100% sampling window. Returns 0
	// when no instance has ever been booted for the deployment or
	// when every live instance has already parked. An unknown
	// deployment_id returns (0, nil) — the SQL planner cannot fail
	// a count(*) on a WHERE clause with no matching rows.
	CountLiveInstancesByDeployment(ctx context.Context, deploymentID string) (int, error)
	LatestSupersededDeployment(ctx context.Context, appID string) (Deployment, error)
	// GetDeploymentByIDScopedToSuperseded returns the deployment only if it
	// belongs to appID AND has status='superseded'. SAFE-RELEASES-G (issue
	// #976) — used by the rollback handler when the caller passes a
	// specific deployment_id via POST /v1/apps/{slug}/rollback. Returns
	// ErrNoRollbackTarget if no row matches and ErrRollbackTargetAlreadyLive
	// if the row exists but its status is not 'superseded'. Both backends
	// (PgStore + MemStore) must honour this contract.
	GetDeploymentByIDScopedToSuperseded(ctx context.Context, appID, deploymentID string) (Deployment, error)
	// HasSnapshotHistory reports whether the deployment ever had a
	// snapshot row (stale or not) in the snapshots table. Used by the
	// rollback handler (SAFE-RELEASES-G) to gate the snapshot-GC race
	// check: a missing non-stale snapshot is meaningful ONLY if the
	// deployment had a snapshot row at some point. PgStore queries the
	// table; MemStore returns (false, nil) because the in-process store
	// doesn't model snapshot retention — the check is a no-op against
	// MemStore, which preserves the legacy happy-path semantics in
	// handler tests.
	HasSnapshotHistory(ctx context.Context, deploymentID string) (bool, error)
	// ListDeploymentsForApp returns deployments for an app, ordered DESC by
	// created_at. limit <= 0 means "no row cap" (return every remaining row
	// after offset). MemStore and PgStore both honour this contract — F-10
	// closed the prior silent asymmetry where Postgres' `LIMIT 0` returned
	// zero rows and MemStore returned all rows. NaN `offset` (= negative
	// value) is treated as 0 by both backends.
	ListDeploymentsForApp(ctx context.Context, appID string, limit, offset int) ([]Deployment, error)
	// UpdateDeploymentTraffic stamps the per-deployment traffic-split
	// weight (issue #556 PR-A). newPercent must be in [0, 100]; the
	// store layer validates this in addition to the schema CHECK
	// constraint (migration 00160). PR-A semantics: zero every
	// sibling live row (Σ = 100 by construction); PR-C may upgrade
	// to proportional redistribution. Returns the refreshed row or
	// ErrInvalidTrafficPercent / ErrTrafficPercentSumInvalid on
	// invariant violations, ErrNotFound when the deployment id is
	// unknown. The handler is responsible for the plan-gate (Pro+
	// only, ErrPlanTrafficSplitNotAllowed) and the request-time
	// range-check — this method holds the FOR UPDATE lock that
	// makes the rebalance race-free against CreateDeployment.
	UpdateDeploymentTraffic(ctx context.Context, id string, newPercent int) (Deployment, error)

	// RecoverRollout (issue #976 / ADR-122 / SAFE-RELEASES-R) is
	// the operator manual-recovery escape hatch — the back-end
	// counterpart of the `gregale rollouts recover <slug>` CLI
	// subcommand. The store runs the state-machine guards + the
	// canary-step advance + the deployment_audit emit in a single
	// atomic transaction so a concurrent canary_progression tick
	// (or a concurrent alert-driven action executor) cannot
	// interleave a partial state.
	//
	// action ∈ {"advance", "promote", "abort"}:
	//
	//   - "advance": bumps canary_step by 1, stamps
	//     canary_step_started_at = now(), redistributes the
	//     traffic-split (largest-remainder Σ = 100). Requires the
	//     rollout to be stuck (canary_step_started_at older than
	//     the stuck-after window) — returns ErrRolloutNotStuck
	//     for a healthy rollout.
	//
	//   - "promote": short-circuits the rollout to canary_step =
	//     canary_total_steps and rollout_state = 'complete',
	//     with traffic_percent = 100 on the in-flight row and 0
	//     on the siblings. No stuck-check — promote is the
	//     operator's "I'm sure, ship it" path.
	//
	//   - "abort": flips rollout_state = 'aborted',
	//     rollout_aborted_at = now(), rollout_aborted_reason =
	//     reason. Legal from rollout_state IN ('pending',
	//     'rolling_out'). Emits a deployment_audit row with
	//     kind = 'deploy.rolled_back' so the dashboard timeline
	//     surfaces the operator's call.
	//
	// Returns the refreshed Deployment row + the audit row id
	// (so the CLI can echo "audit_id=…"). Both pgstore and
	// memstore implement the same closed-set guards + audit
	// emit so handler tests can pin both backends to the same
	// shape.
	RecoverRollout(ctx context.Context, appID string, action, reason string) (Deployment, int64, error)

	// MirrorRules (issue #72 / ADR-125) — per-deployment traffic-
	// mirroring CRUD + comparison ledger reads.
	//
	// CreateMirrorRuleIfUnderQuota inserts a new mirror_rule after
	// holding FOR UPDATE on the apps row to serialise against
	// concurrent creators. The cap is limits.MirrorTargetsPerApp
	// (Free 0 / Hobby 0 / Pro 1 / Scale 3 per ADR-125 §Decision);
	// the gate fires before the INSERT, returning a *QuotaError
	// when tripped (the same shape CreateAppIfUnderQuota /
	// CreateEdgeRuleIfUnderQuota use, with Kind=QuotaErrorKindMirror
	// added to the QuotaErrorKind enum so the apid handler can map
	// it to a stable RFC 7807 code). Range-check on percent ∈
	// [0, 100] is layered: handler validates first (422), this
	// method re-validates as defence-in-depth. Source / mirror
	// distinctness is the migrations/00348 SQL CHECK; this method
	// surfaces ErrMirrorSourceTargetSame at the Go layer so the
	// handler doesn't have to inspect the SQL error. Source /
	// mirror must both be status='live' deployments of the same
	// app — returns ErrMirrorDeploymentNotLive / ErrMirrorCrossAppMismatch
	// respectively when either invariant trips.
	//
	// ListMirrorRules returns every rule for the app (enabled or
	// not), ordered by created_at ASC. The gateway picker reads
	// from this in the deployment_changed pg_notify refresh path;
	// MemStore's in-memory mirror makes it testable without a DB.
	//
	// GetMirrorRuleByID returns a single rule by id. IDOR safety
	// is the caller's responsibility (apid's loadApp + AccountID
	// check); this method scopes the read by id alone.
	//
	// UpdateMirrorRule applies a partial update via MirrorRulePatch.
	// Pointer fields let the caller distinguish "absent" from
	// "zero" (Percent=0 disables the rule without removing it).
	// Same FOR UPDATE discipline as CreateMirrorRuleIfUnderQuota so
	// concurrent writers serialise.
	//
	// DeleteMirrorRule removes a rule. ON DELETE CASCADE on
	// mirror_invocation_results.mirror_rule_id cleans up the
	// comparison ledger; ON DELETE CASCADE on the deployments FKs
	// means deleting a deployment cascades to its rules.
	//
	// InsertMirrorResult appends one row to mirror_invocation_results
	// after a mirror goroutine completes. Best-effort: the gateway
	// logs the error but doesn't roll back the customer-facing
	// response. Caller stamps CompletedAt; the column default is
	// now() for direct SQL inserts but the Go side sets it
	// explicitly so the audit + ledger timestamps agree.
	//
	// ListMirrorResults returns up to `limit` rows for a rule with
	// completed_at >= `since`, ordered DESC. limit <= 0 means "no
	// cap" (matches the same contract ListDeploymentsForApp uses).
	//
	// MirrorSummary aggregates the rows in the same window via SQL
	// aggregates — never client-side. The apid handler renders the
	// result as MirrorSummaryResponse.
	CreateMirrorRuleIfUnderQuota(ctx context.Context, in CreateMirrorRuleParams, limits api.Limits) (MirrorRule, error)
	ListMirrorRules(ctx context.Context, appID string) ([]MirrorRule, error)
	GetMirrorRuleByID(ctx context.Context, id string) (MirrorRule, error)
	UpdateMirrorRule(ctx context.Context, id string, patch MirrorRulePatch) (MirrorRule, error)
	DeleteMirrorRule(ctx context.Context, id string) error
	InsertMirrorResult(ctx context.Context, r MirrorInvocationResult) error
	ListMirrorResults(ctx context.Context, ruleID string, since time.Time, limit int) ([]MirrorInvocationResult, error)
	MirrorSummary(ctx context.Context, ruleID string, since time.Time) (MirrorSummary, error)

	// SetDeploymentFailed is the failure-specific helper ADR-021 introduced
	// alongside the deployments.error_code column. Status is pinned to
	// 'failed'; code is the RFC 7807 code pkg/api.SentinelToCode lifted
	// from the wrapping error (empty when the failure did not map to a
	// sentinel); message is the free-text debug string. Returns the
	// refreshed row. Idempotent — a redeploy after a fix overwrites
	// both columns.
	SetDeploymentFailed(ctx context.Context, id, code, message string) (Deployment, error)
	// SetDeploymentFailedEx is the error-explanations cluster (spec
	// §6.4 amendment 1) extension of SetDeploymentFailed. Writes the
	// four customer-facing prose fields (hint/why/fix/relevant_logs)
	// alongside error_code so post-mortem retrieval via
	// `gregale inspect <slug> --errors` surfaces the same prose the
	// deploy-time Problem emitted. Empty inputs map to NULL columns.
	// Idempotent on (status='failed') rows — a redeploy after a fix
	// overwrites all four columns. Returns the refreshed row.
	SetDeploymentFailedEx(
		ctx context.Context, id, code, message, hint, why, fix string,
		logs []api.LogExcerpt,
	) (Deployment, error)
	// ListDeploymentsForAccount returns deployments across every app the
	// account owns, cursor-paginated by created_at DESC. before is the
	// inclusive upper bound — pass the previous response's NextBefore to
	// page backwards. limit is the page cap (caller validates a sane upper
	// bound). MemStore sorts in memory; PgStore uses a LIMIT/OFFSET or
	// keyset pagination (deferred — LIMIT/OFFSET is fine at one-box scale).
	ListDeploymentsForAccount(ctx context.Context, accountID string, before time.Time, limit int) ([]Deployment, error)

	// Deployment logs (M7.5 slice 5).
	//
	// AppendDeploymentLog inserts one row of build output. Builderd is
	// the writer in production; tests write directly. Returns the seq
	// Postgres assigned (or the MemStore-picked seq). The seq is what
	// the SSE endpoint returns as the cursor — the client pages by
	// `(deployment_id, seq < before_seq) ORDER BY seq DESC`.
	//
	// ListDeploymentLogs returns the page of rows whose seq is < before
	// (zero → newest page first), ordered DESC. Returns the rows +
	// hasMore so the caller knows there's another page without an
	// extra round-trip (rows == limit + 1 sentinel keeps the impl
	// cheap).
	AppendDeploymentLog(ctx context.Context, deploymentID, stream, line string) (seq int64, err error)
	ListDeploymentLogs(ctx context.Context, deploymentID string, beforeSeq int64, limit int) (rows []LogEntry, hasMore bool, err error)
	UpdateDeploymentStatus(ctx context.Context, id string, status DeploymentStatus, errMsg string) error
	MarkDeploymentSuperseded(ctx context.Context, id string) error
	MarkDeploymentLive(ctx context.Context, id string) error

	// MarkDeploymentCancelled atomically transitions the row to
	// DeployCancelled, stamping cancelled_at / cancelled_by_principal /
	// cancel_reason audit columns. The CAS guard enforces
	// status ∈ {pending, building, imaging, snapshotting} —
	// concurrent terminal transitions are last-write-wins safe.
	// Returns ErrInvalidStateTransition if the row is already
	// terminal or DeployLive, and ErrNotFound if id is unknown.
	// (ADR-124 — deployment queue controls.)
	MarkDeploymentCancelled(ctx context.Context, id, principal string, reason CancelReason, when time.Time) error

	// CancelDeploymentTx is the single-transaction orchestrator
	// that mirrors AutoRollbackDeploymentsTx (ADR-118). On
	// success it has (a) flipped the deployment row to
	// DeployCancelled, (b) cascaded-cancelled every non-terminal
	// build row attached to the deployment, (c) SELECT FOR UPDATE
	// locked the parent apps row, and (d) emitted pg_notify on
	// the deployment_changed channel. The Firecracker VM tear-down
	// is intentionally OUT of this transaction — it happens via
	// the builderd cancel-LISTEN goroutine after the row flip
	// commits. Returns ErrCancelLiveForbidden when the current
	// status is DeployLive (use deploys rollback instead) and
	// ErrInvalidStateTransition for any other non-eligible state.
	// (ADR-124 — deployment queue controls.)
	CancelDeploymentTx(ctx context.Context, id, principal string, reason CancelReason) (Deployment, []string, error)

	// ReorderDeployment atomically sets the deployments.priority
	// column (range [0, 1000], 0 = deploy-immediately). The CAS
	// guard enforces status='pending' so reorder cannot race with
	// the build VM spin-up. Returns ErrReorderNotPending when the
	// row has already left pending, and ErrPriorityOutOfRange
	// when newPriority is outside the closed range. (ADR-124.)
	ReorderDeployment(ctx context.Context, id string, newPriority int, principal string) error

	// ClearDeployment soft-deletes a deployment row by stamping
	// deleted_at + deleted_by_principal. Status is intentionally
	// unchanged so the audit trail remains visible to admins while
	// the customer list surface hides the row. Only callable on
	// non-DeployLive rows; cancelling + clearing are distinct axes
	// by design. (ADR-124.)
	ClearDeployment(ctx context.Context, id, principal string) error

	// ClearObsoleteDeployments bulk-soft-deletes (a) superseded /
	// failed / cancelled rows where enqueued_at < olderThan and
	// (b) the row is NOT in the "current + previous" retention
	// window for its app (INV 3). Returns the count of rows
	// touched. (ADR-124.)
	ClearObsoleteDeployments(ctx context.Context, appID string, olderThan time.Time) (int, error)

	// MarkBuildCancelled atomically transitions a builds row to
	// BuildCancelled. The CAS guard enforces status ∈ {queued,
	// running}. Used by the builderd cancel-LISTEN goroutine when
	// a deployment row's pg_notify fires. (ADR-124.)
	MarkBuildCancelled(ctx context.Context, buildID, deploymentID string, cascade bool, when time.Time) error
	// SetDeploymentRootfs records the on-disk path + size + StorageBackend
	// key of the per-app ext4 layer imaged produced for this deployment
	// (spec §4.6, drive1). The snapshot-prime handshake reads this when
	// staging the cold boot so schedd can attach drive1 from the right
	// path / key (ADR-018, issue #96 / ADR-025 axis 2 — PR #116). Only
	// imaged writes it. rootfsKey is the canonical StorageBackend key
	// (e.g. "apps/<slug>/<depID>.ext4"); schedd carries it on the wake
	// wire and vmmd resolves it via Storage.Get before staging the chroot.
	SetDeploymentRootfs(ctx context.Context, id, path, key string, bytes int64) error

	// UpsertDeploymentScanResult records the per-deploy grype CVE
	// scan on the deployment row (issue #464 / ADR-055 / PR-3).
	// Only apid calls this (the apid-only-writer invariant on
	// customer-intent tables); imaged reaches it through the
	// ScanResultSink seam declared in pkg/imaged/scan_sink.go.
	//
	// scanResult is the marshalled JSON for the deployments.scan_result
	// jsonb column (the typed *imaged.ScanResult from the deploy-
	// complete hook, with SeverityCounts + Vulnerability[] payload).
	// status is the closed enum value 'complete' or 'failed' (matches
	// the migrations/00135 CHECK constraint). The row is filtered
	// by deployment_id at the SQL level — IDOR safety is the caller's
	// job (the apid-side handler does the AppByID + AccountID check
	// before invoking), but the row must be scoped to one deployment
	// so a misrouted call doesn't bleed across accounts.
	//
	// Idempotent: a re-delivered deploy notification overwrites the
	// same row with the same scan_result + scan_status + scanned_at,
	// not create a second one. Returns ErrNotFound when the
	// deployment row doesn't exist (the FK CASCADE in migration
	// 00135 makes this unreachable in practice, but the explicit
	// error lets a misuse at the call site fail closed).
	UpsertDeploymentScanResult(ctx context.Context, deploymentID string, scanResult []byte, status string) error

	// UpsertDeploymentSecretFindings records the per-deploy secret-
	// scan audit row (migrations/00221, secret-scan v2). Written by
	// cmd/apid/secretscan.go when the server-side tree scan finds
	// redactions needed (a 422 rejection). Mirror
	// UpsertDeploymentScanResult's IDOR + idempotency contract:
	// scoped to one deployment row, overwrites (not creates a second),
	// and returns ErrNotFound when the row is missing so a misroute
	// fails closed.
	//
	// findings is the marshalled JSON for the
	// deployments.secret_findings jsonb column (the typed
	// []api.SecretFinding list, including safe-snippet policy).
	// scannedAt is set on every call so a future query "show me the
	// deploys scanned in the last hour" has a typed timestamp. The
	// scan_status on the deployment row is updated to
	// 'complete_with_redactions' on a non-empty findings list — the
	// apid-side caller passes that status so the pgstore stays
	// schema-agnostic.
	UpsertDeploymentSecretFindings(ctx context.Context, deploymentID string, findings []byte, status string, scannedAt time.Time) error

	// RecordRestart (issue #586 / ADR-129 / cluster C commit 12 of
	// the platform-observability mega-PR) bumps the persisted
	// deployments.liveness_restart_count column by 1 in a single
	// statement. Called by pkg/sched/Engine alongside the
	// in-memory LivenessWindow.RecordRestart call so the column is
	// the source of truth across schedd restarts. Returns
	// ErrNotFound when the deployment row is missing so a
	// misroute fails closed (mirrors UpsertDeploymentScanResult
	// IDOR contract at line 2049). The CHECK constraint
	// (deployments_liveness_restart_count_nonneg_chk,
	// migrations/00411) rejects a negative bump at the SQL layer
	// even though the application code is monotonic — belt-and-
	// braces against a buggy caller.
	RecordRestart(ctx context.Context, deploymentID string) error

	// Issue #599 / ADR-130 / cluster D commit 14 of the
	// platform-observability mega-PR — status-page incidents
	// table (migrations/00412). Three methods on the Store
	// interface:
	//
	//   InsertStatusIncident   appends an open row (resolved_at
	//                          NULL). The status page surfaces
	//                          this verbatim.
	//   ResolveStatusIncident  stamps resolved_at on the row
	//                          identified by id. Idempotent: a
	//                          second call on an already-resolved
	//                          row returns nil (no error) — the
	//                          CLI can re-issue a resolve without
	//                          surfacing 23514.
	//   ListOpenStatusIncidents
	//                          reads the partial-index
	//                          (status_incidents_open WHERE
	//                          resolved_at IS NULL) sorted by
	//                          posted_at DESC. The
	//                          /v1/internal/slo.json endpoint
	//                          composes its response from this
	//                          list plus meterd's loopback
	//                          Prometheus exporter.
	InsertStatusIncident(ctx context.Context, component, severity, message string) (StatusIncident, error)
	ResolveStatusIncident(ctx context.Context, id int64) error
	ListOpenStatusIncidents(ctx context.Context) ([]StatusIncident, error)

	// ADR-122 / issue #975 item #1: per-deployment OpenAPI
	// document capture. The surface is paid-only (Free plan
	// returns 403 from the apid) but the microVM always captures
	// the body during cold boot — the read cost is one TCP
	// ReadAll against /openapi.json and is throughput-irrelevant
	// relative to the 350 ms wake budget. Free customers' docs
	// are persisted (the per-account quota is 0) but never
	// exposed via the apid GET; the apid never inserts a Free
	// row, so the row count for Free plans is always 0.
	//
	// The four methods pin the surface with one row per
	// deployment (PRIMARY KEY on deployment_id). The migration
	// puts the table behind ON DELETE CASCADE so a deployment
	// cleanup wipes the doc; the apid audit emit
	// (app.openapi_doc.deleted) records the lifecycle event.
	//
	// IDOR floor: every read and write takes accountID and the
	// SQL filters by (deployment_id, account_id). The
	// consumer_keys precedent (pkg/state/pgstore_consumer_keys.go)
	// applies the same defence-in-depth: the apps row is FK-scoped
	// to one account so a WHERE on deployment_id alone is
	// sufficient, but the Store boundary enforces tenancy at the
	// API surface so a future caller can't probe by
	// deploymentID without knowing the account.
	//
	// GetDeploymentOpenAPIDoc returns the (doc, meta) pair for
	// one deployment. ErrNotFound when the row is missing OR
	// when the caller's accountID does not match (the
	// pgx row.Scan errors on no rows; we map it to ErrNotFound).
	// The truncated flag is returned in the meta so the apid GET
	// handler can set X-OpenAPI-Doc-Truncated: 1 on the response.
	GetDeploymentOpenAPIDoc(ctx context.Context, deploymentID, accountID string) ([]byte, OpenAPIDocMeta, error)
	// UpsertDeploymentOpenAPIDoc records (or overwrites) the
	// captured OpenAPI body for one deployment. doc is the
	// validated JSON bytes (the apid jsonschema check passes
	// before this call); source is the closed enum 'cold_boot' or
	// 'manual_upload'. The row is filtered by (deployment_id,
	// account_id) at the WHERE clause so a misrouted call from a
	// different account fails with ErrNotFound (the FK CASCADE in
	// migration 00330 makes this unreachable in practice, but
	// the explicit error lets a misuse at the call site fail
	// closed). Idempotent: a re-delivered cold-boot event
	// overwrites the same row, not create a second one. The
	// capture count for the per-account quota is computed via
	// CountOpenAPIDocsByAccount.
	UpsertDeploymentOpenAPIDoc(ctx context.Context, deploymentID, accountID, appID string, doc []byte, source string, truncated bool) error
	// DeleteDeploymentOpenAPIDoc removes the doc row for one
	// deployment. ErrNotFound when no row OR the caller's
	// accountID does not match — same IDOR floor as the read.
	// The DELETE is idempotent only in the sense that "no row"
	// and "row deleted" both surface as ErrNotFound (the apid
	// caller treats a 404 as success after a delete).
	DeleteDeploymentOpenAPIDoc(ctx context.Context, deploymentID, accountID string) error
	// CountOpenAPIDocsByAccount returns the number of doc rows
	// the account owns. Drives the per-account quota gate
	// (api.Plan.OpenAPIDocsPerAccount). The count is computed
	// server-side via a SELECT COUNT(*) so the apid doesn't
	// load the full body slice.
	CountOpenAPIDocsByAccount(ctx context.Context, accountID string) (int, error)

	// Per-app OpenAPI import (issue #975 item #2 / ADR-126).
	// The four methods mirror the per-deployment methods above
	// but key on (app_id, account_id) — one row per app, last
	// write wins via INSERT ... ON CONFLICT DO UPDATE.

	// GetAppOpenAPIDoc returns the (doc, meta) pair for one app.
	// ErrNotFound when the row is missing OR when the caller's
	// accountID does not match (the pgx row.Scan errors on no
	// rows; we map it to ErrNotFound). The closed `openapi_version`
	// enum is surfaced in the meta so the dry-run handler can
	// reject a version-stamped suggestion on a different version
	// without a second DB read.
	GetAppOpenAPIDoc(ctx context.Context, appID, accountID string) ([]byte, AppOpenAPIDocMeta, error)
	// UpsertAppOpenAPIDoc records (or overwrites) the imported
	// OpenAPI body for one app. doc is the meta-schema-validated
	// JSON bytes (the apid openapiimport.ValidateImport check
	// passes before this call); endpointCount is the
	// pre-computed paths.* operation count; openapiVersion is
	// the closed enum value (one of ValidOpenAPIVersions). The
	// row is filtered by (app_id, account_id) at the WHERE clause
	// so a misrouted call from a different account fails with
	// ErrNotFound. Idempotent: a re-delivered import overwrites
	// the same row, not creates a second one.
	//
	// The per-account quota gate is upstream — prefer
	// UpsertAppOpenAPIDocIfUnderQuota which bundles count+lock+upsert
	// into a single atomic store call. This raw method is kept
	// for callers that have already taken the lock (admin tool,
	// test harness, internal API).
	UpsertAppOpenAPIDoc(ctx context.Context, appID, accountID string, doc []byte, endpointCount int, openapiVersion string) error
	// UpsertAppOpenAPIDocIfUnderQuota bundles the per-account quota
	// gate (count + lock + check) with the upsert so a TOCTOU race
	// between two concurrent imports under the same account can't
	// slip past the cap. The PgStore impl runs the count + upsert
	// inside a transaction with a row lock on the account; the
	// MemStore impl serialises under m.mu. Returns
	// ErrOpenAPIImportsPerAccountQuotaReached when count>=limit;
	// ErrNotFound when the parent app row is missing or the
	// caller's accountID does not match.
	UpsertAppOpenAPIDocIfUnderQuota(ctx context.Context, appID, accountID string, doc []byte, endpointCount int, openapiVersion string, planMax int) error
	// DeleteAppOpenAPIDoc removes the import row for one app.
	// ErrNotFound when no row OR the caller's accountID does not
	// match — same IDOR floor as the read. The apid caller
	// treats ErrNotFound as "already deleted" so a retry is a
	// no-op (idempotent 204).
	DeleteAppOpenAPIDoc(ctx context.Context, appID, accountID string) error
	// CountOpenAPIImportsByAccount returns the number of import
	// rows the account owns. Drives the per-account quota gate
	// (api.Plan.OpenAPIImportsPerAccount). The count is computed
	// server-side via a SELECT COUNT(*) so the apid doesn't
	// load the full body slice.
	CountOpenAPIImportsByAccount(ctx context.Context, accountID string) (int, error)

	// Per-workload filesystem handles for sidecars (issue #463 /
	// ADR-069 / PR-B). The PR-A surface (Deployment.Sidecars
	// jsonb) stays the contract layer; this is the per-sidecar
	// storage-key handle imaged writes and vmmd reads at wake
	// time. The 2-row cap is enforced upstream by the
	// `deployments.sidecars` CHECK constraint — this interface
	// does not duplicate it (its row count could exceed
	// SidecarCapMax via a hand-INSERT and that would only
	// surface when vmmd reads a row that no jsonb entry
	// references, which is a defence-in-depth concern, not a
	// correctness gate).
	//
	// SetDeploymentSidecarLayer upserts one sidecar's layer
	// handle. Imaged calls it once per sidecar in
	// buildImageLayer's per-sidecar loop. Returns the refreshed
	// row with CreatedAt / UpdatedAt populated. ON CONFLICT
	// updates bytes + content_digest + storage_key +
	// updated_at so a re-imaged rebuild's new key replaces the
	// prior build's key without orphaned-key drift.
	SetDeploymentSidecarLayer(ctx context.Context, layer DeploymentSidecarLayer) (DeploymentSidecarLayer, error)
	// ListDeploymentSidecarLayers returns the deployment's full
	// sidecar set, ordered by sidecar_name ASC for deterministic
	// iteration. Returns an empty slice when the deployment has
	// no sidecars (NOT NULL DEFAULT '[]' sidebars up the
	// contract). ErrNotFound is reserved for "deployment row
	// missing entirely" — a deployment with zero sidecars
	// returns ([]nil, nil). vmmd's Wake path consumes this
	// eagerly to convert into wake.Workloads entries.
	ListDeploymentSidecarLayers(ctx context.Context, deploymentID string) ([]DeploymentSidecarLayer, error)

	// SetDeploymentSourceURL records the canonical upstream URL +
	// commit SHA the build was triggered from (Tier 3 / issue #197
	// B3.10 schema half, migrations/00047). Populated by githubd's
	// CreateDeployment callback. Empty values are accepted (an
	// image: deploy with no upstream URL is a normal case). The
	// reader is build_provenance (Phase 2) which surfaces the
	// values at GET /v1/builds/{id}/provenance.
	SetDeploymentSourceURL(ctx context.Context, id, sourceURL, commitSHA string) error

	// Builds (apid creates the queued row; builderd writes status, spec §9).
	CreateBuild(ctx context.Context, deploymentID string, kind DeploymentKind, sourceBytes int64, logPath string) (Build, error)
	BuildByID(ctx context.Context, id string) (Build, error)
	BuildByDeployment(ctx context.Context, deploymentID string) (Build, error)
	// ClaimQueuedBuild atomically transitions queued → running and returns
	// the row. Returns ErrNotFound when the row is missing OR when its
	// status is no longer BuildQueued — callers (builderd's ProcessOne)
	// use the second case to drop duplicate notifications from the apid
	// write path and the imaged reaper (PR-A). started_at is set to now.
	ClaimQueuedBuild(ctx context.Context, id string) (Build, error)
	// ClaimNextQueuedBuild is the durability-net worker surface (PR-B).
	// Atomically picks the next queued row in enqueued_at ASC order using
	// SELECT … FOR UPDATE SKIP LOCKED, flips it to running, sets
	// started_at = now(). Returns ErrNotFound when the queue is empty so
	// the worker can sleep without surfacing an error. SKIP LOCKED is
	// what keeps a future second builderd process (or pg_cron) from
	// starving the in-process worker — both compete cleanly for the
	// head of the queue without ever observing the same row.
	ClaimNextQueuedBuild(ctx context.Context) (Build, error)
	// ClaimNextQueuedBuildWithFairness is the B2.2 (issue #196) variant
	// that prefers accounts whose last claim is older than fairnessWindow.
	// Identical SQL to ClaimNextQueuedBuild plus a CTE that filters out
	// rows whose account shows up in recent_build_claims (claimed within
	// the window) — falling back to all queued rows if every queued
	// account is recent, so no build is ever starved (issue #196 B2.2
	// critical invariant #1). The caller must RecordRecentBuildClaim on
	// the chosen row's account_id AFTER the claim succeeds so the next
	// round sees the just-claimed account in the "recent" set.
	ClaimNextQueuedBuildWithFairness(ctx context.Context, fairnessWindow time.Duration) (Build, error)
	// RecordRecentBuildClaim records that builderd just claimed a build
	// for the given account. Called by builderd after a successful
	// ClaimNextQueuedBuildWithFairness so the next fairness round
	// excludes this account from the "fresh" set. Rows persist until
	// they age out of the WHERE clause (claimed_at + window < now), so
	// the table grows by ~claim-rate per window — bounded by a future
	// Tier-3 GC (out of scope for #196).
	RecordRecentBuildClaim(ctx context.Context, accountID, buildID string) error
	// RequeueBuild resets a running build back to queued with enqueued_at
	// untouched (preserving FIFO order) when the builder slot allocator
	// (DecideSlot) rules the row out (builderd worker PR-B). started_at
	// is cleared. Returns ErrNotFound when the row is missing. The
	// caller (builderd worker) decides whether to requeue or fail;
	// RequeueBuild itself is unconditional.
	RequeueBuild(ctx context.Context, id string) error
	UpdateBuildStatus(ctx context.Context, id string, status BuildStatus, fc FailureClass, started, finished bool) error

	// CreateBuildProvenance persists the post-mortem "what ran?"
	// record for a successful Build (ADR-038, Tier 3 / issue #197
	// B3.1). Called by builderd's recordProvenance helper at the two
	// markSucceeded sites; ON CONFLICT (build_id) DO UPDATE makes
	// redelivery safe — a LISTEN race between apid and imaged's
	// reaper must not double-row.
	//
	// Builderd's failure path is best-effort: a failed INSERT is
	// logged at WARN and the build still succeeds (the builds row is
	// authoritative for customer-visible success/fail). The reader
	// (apid GET /v1/builds/{id}/provenance) renders 404 when the
	// row is missing — the customer-visible surface is "missing
	// provenance for build X" rather than "build X failed".
	CreateBuildProvenance(ctx context.Context, prov BuildProvenance) error
	// BuildProvenanceByBuildID resolves the row by build_id. Returns
	// ErrNotFound when the build has no provenance row — either a
	// pre-PR build, or a successful build whose populator INSERT
	// failed and was logged at WARN. Backs apid's GET
	// /v1/builds/{id}/provenance route + the `faas build provenance`
	// CLI command.
	BuildProvenanceByBuildID(ctx context.Context, buildID string) (BuildProvenance, error)
	// UpdateBuildProvenanceSBOM stamps the SBOM storage key onto an
	// existing build_provenance row (issue #299 / ADR-038 Phase 3).
	// The SBOM populator (imaged's writeBuildSBOM) runs AFTER the
	// row is created by builderd's recordProvenance — by the time
	// imaged has the source tree to enumerate, the build is already
	// marked succeeded and the provenance row is in place. Empty
	// sbomKey clears the column (best-effort: a syft failure leaves
	// the cell NULL). Returns ErrNotFound when no row exists for
	// buildID; the caller logs at WARN and continues.
	UpdateBuildProvenanceSBOM(ctx context.Context, buildID, sbomKey string) error

	// SweepStuckRunningBuilds flips every build row whose status is
	// 'running' AND whose started_at is older than threshold to
	// status='failed' with failure_class='timeout'. Used by the
	// builderd reaper (issue #195 B1.4) to clean up rows left
	// orphaned by a builder VM crash, OOM, or kernel panic that
	// bypassed the normal markFailed / markSucceeded path.
	//
	// Returns the number of rows affected. Idempotent: a second
	// call with the same threshold affects 0 rows because all
	// matching rows are now 'failed'.
	//
	// The owning in-flight deployment is flipped to DeployFailed in
	// the same store operation. This keeps the build and deployment
	// state machine consistent when the builder process disappears.
	SweepStuckRunningBuilds(ctx context.Context, threshold time.Time) (int, error)

	// Custom domains (apid is sole writer).
	CreateCustomDomain(ctx context.Context, domain, appID, token string) (CustomDomain, error)
	DomainByName(ctx context.Context, domain string) (CustomDomain, error)
	ListDomainsForApp(ctx context.Context, appID string) ([]CustomDomain, error)
	ListDomainsForAccount(ctx context.Context, accountID string) ([]CustomDomain, error)
	MarkDomainVerified(ctx context.Context, domain string) error
	DeleteCustomDomain(ctx context.Context, domain string) error

	// Domain doctor observations (ADR-120). The dns_poller
	// is the sole writer; the doctor HTTP handler is the
	// sole reader. UpsertDoctorObservation writes (or
	// refreshes) the per-domain observation row; the
	// GetDoctorObservation reader returns ErrNotFound when
	// the poller has not yet written a row for the domain
	// (the handler treats this as stale:true and triggers
	// a synchronous re-probe). ListAllCustomDomainsForDoctor
	// is the poller's enumeration seam — distinct from
	// ListDomainsForApp so the doctor pass doesn't have to
	// walk every app+account. Doctor observation methods
	// are hand-rolled (not sqlc-generated) because the
	// UPSERT shape and the lack of other consumers mean a
	// one-line pgx call is clearer than a generated method.
	UpsertDoctorObservation(ctx context.Context, obs DomainDoctorObservation) error
	GetDoctorObservation(ctx context.Context, domain string) (DomainDoctorObservation, error)
	ListAllCustomDomainsForDoctor(ctx context.Context) ([]string, error)
	// OldestDoctorObservation (ADR-120 Tier A1) returns the
	// minimum observed_at across every row in
	// domain_doctor_observations, or the zero time.Time when the
	// table is empty (cold start). The dns_poller
	// (cmd/apid/dns_poller.go::emitDoctorOldestObservationGauge)
	// converts that to a wall-clock age and Sets
	// apid_domain_doctor_oldest_observation_seconds so the
	// FaasDomainDoctorStalled / FaasDomainDoctorStretched alerts
	// can page on a stalled loop. Hand-rolled (not sqlc) — a
	// one-line MIN(...) query doesn't justify a generated method.
	OldestDoctorObservation(ctx context.Context) (time.Time, error)

	// Tenant surfaces (ADR-100; apid is sole writer to tenant_surfaces
	// and tenant_hostnames). The pg_notify trigger
	// (migrations/00243_tenant_surfaces.sql) bubbles mutations to
	// gatewayd-internal's cert-remint subscriber; the store itself is
	// pure SQL. Create*IfUnderQuota return:
	//   - (TenantSurface{}, *TenantSurfaceQuotaError) when limit trips
	//   - (TenantSurface{}, ErrTenantSurfacesNotAllowed) when the plan
	//     gate is off (Free today)
	//   - (TenantSurface{}, ErrNotFound) when the parent row is gone
	//   - (TenantSurface{}, ErrConflict) on FK + UQ violations
	// The TOCTOU defence is a BeginTx + FOR UPDATE on the accounts row
	// (surfaces) or tenant_surfaces row (hostnames), mirroring
	// CreateEdgeRuleIfUnderQuota at pgstore.go:5951.
	CreateTenantSurfaceIfUnderQuota(ctx context.Context, in CreateTenantSurfaceParams, limits api.Limits) (TenantSurface, error)
	GetTenantSurfaceByID(ctx context.Context, id string) (TenantSurface, error)
	GetTenantSurfaceByName(ctx context.Context, accountID, name string) (TenantSurface, error)
	ListTenantSurfacesForAccount(ctx context.Context, accountID string) ([]TenantSurface, error)
	ListTenantSurfacesForApp(ctx context.Context, appID string) ([]TenantSurface, error)
	CountTenantSurfacesForAccount(ctx context.Context, accountID string) (int, error)
	// ListTenantSurfacesNearingExpiry is the renewer hot-path
	// (PR-D cert-engine-real-mint commit 3). Returns up to
	// `limit` active surfaces with cert_state='issued' AND
	// cert_not_after < cutoff, using a keyset cursor on
	// (cert_not_after, id) for stable pagination. The renewer
	// iterates the returned slice and triggers a re-mint
	// through the existing pg_notify pipeline (via
	// TouchTenantSurfaceForRenewal bumping updated_at, which
	// fires the tenant_surface_changed notify trigger).
	//
	// PR-D code review (PR #959 candidate 6): the v1 unbounded
	// shape would issue N UPDATEs per tick after a CA outage
	// landed N>1000 surfaces in the renewal window. The
	// limit-and-cursor shape bounds each tick to a hard
	// cap (api.CertRenewTickBatchLimit, 1k) and the renewer
	// keeps calling until fewer than `limit` rows return.
	// afterCertNotAfter + afterID are the cursor (pass zero
	// values on the first page).
	ListTenantSurfacesNearingExpiry(ctx context.Context, cutoff time.Time, limit int, afterCertNotAfter time.Time, afterID string) ([]TenantSurface, error)
	// TouchTenantSurfaceForRenewal bumps updated_at on the
	// surface row so the tenant_surface_changed notify trigger
	// fires; the pg_notify subscriber routes the bare surface
	// UUID back through CertIssuer.RequestCertForSurface which
	// re-runs the full state machine. The renewer doesn't need
	// its own write path — it rides the existing pipeline so
	// the in-flight state machine (none → pending → issued)
	// stays the source of truth.
	TouchTenantSurfaceForRenewal(ctx context.Context, id string) error
	UpdateTenantSurfaceStatus(ctx context.Context, id string, status SurfaceStatus) error
	UpdateTenantSurfaceCert(ctx context.Context, in UpdateSurfaceCertParams) error
	// DeleteTenantSurface soft-deletes: status flips to 'deleted',
	// the row stays for audit / cert_history. Hard DELETE cascades
	// hostnames via the FK ON DELETE CASCADE.
	DeleteTenantSurface(ctx context.Context, id string) error
	// TenantSurfaceByHostname — hot-path lookup from pgRouter.ResolveHost.
	// Falls through to ErrNotFound when the hostname is not claimed
	// by any surface (the caller then consults DomainByName). Joins
	// tenant_hostnames → tenant_surfaces in SQL.
	TenantSurfaceByHostname(ctx context.Context, hostname string) (TenantSurface, error)

	// Hostname CRUD under per-surface quota. CreateTenantHostnameIfUnderQuota
	// takes a FOR UPDATE lock on the parent tenant_surfaces row before
	// counting. The global (per-hostname) UQ on tenant_hostnames.hostname
	// surfaces as ErrConflict when a hostname is already claimed by a
	// different surface (the apid handler maps this to
	// CodeTenantHostnameAlreadyClaimed in PR-C).
	CreateTenantHostnameIfUnderQuota(ctx context.Context, in CreateTenantHostnameParams, limits api.Limits) (TenantHostname, error)
	ListTenantHostnamesForSurface(ctx context.Context, surfaceID string) ([]TenantHostname, error)
	// ListVerifiedTenantHostnamesForSurface is the SAN-assembly hot path
	// used by CertIssuer.RequestCertForSurface; returns sorted by hostname
	// for deterministic primary/SAN assignment.
	ListVerifiedTenantHostnamesForSurface(ctx context.Context, surfaceID string) ([]TenantHostname, error)
	CountTenantHostnamesForSurface(ctx context.Context, surfaceID string) (int, error)
	MarkTenantHostnameVerified(ctx context.Context, hostname string) error
	MarkTenantHostnameCheckFailed(ctx context.Context, hostname, reason string) error
	// ListPendingTenantHostnames — dns_poller queue. Returns the
	// `limit` oldest unverified rows whose last_check_at is older than
	// `olderThan` (the poller sleeps batch+1 interval seconds between
	// passes, so a row re-enters the queue roughly every batch-time).
	ListPendingTenantHostnames(ctx context.Context, olderThan time.Time, limit int) ([]TenantHostname, error)
	DeleteTenantHostname(ctx context.Context, hostname string) error
	// GetTenantHostnameByName — pgRouter.ResolveHost's tenant-surface
	// branch needs the hostname row alongside the surface so it can
	// fail closed on hostname.Verified() == false (a pre-challenge
	// TXT record must not be routable; the legacy custom_domains
	// path's parallel contract is dom.Verified()). ErrNotFound when
	// the hostname is unclaimed. The hostname column is citext so
	// callers pass the canonical lowercase form.
	GetTenantHostnameByName(ctx context.Context, hostname string) (TenantHostname, error)

	// Crons (apid CRUDs; schedd fires).
	CreateCron(ctx context.Context, appID, schedule, path string, enabled bool) (Cron, error)
	// CreateCronIfUnderQuota inserts a cron iff the per-app and
	// per-account caps (limits.CronLimitPerApp / CronLimitPerAccount)
	// are not yet reached. The per-app count is authoritative under
	// an apps FOR UPDATE row lock; the per-account count is a
	// follow-up under the same tx so two concurrent POSTs for the
	// same account cannot both pass the account cap. Returns:
	//   - (Cron{}, *CronQuotaError) when either cap trips
	//   - (Cron{}, ErrNotFound) when the app row is missing or deleted
	// apid's createCron handler routes through this; schedd's
	// dispatch loop and existing tests still call CreateCron
	// (uncapped) because they bypass the customer-facing path.
	CreateCronIfUnderQuota(ctx context.Context, appID, schedule, path string, enabled bool, limits api.Limits) (Cron, error)
	CronByID(ctx context.Context, id string) (Cron, error)
	// UpdateCron mutates the optional fields of a cron row. nil pointers
	// leave the field untouched. createdAt is supported because schedd's
	// dispatch loop reads the boundary off CreatedAt (first-fire guard);
	// backfilling this field is the only honest way to rewind a test or
	// restore an imported schedule.
	UpdateCron(ctx context.Context, id string, schedule, path *string, enabled *bool, createdAt *time.Time) (Cron, error)
	DeleteCron(ctx context.Context, id, appID string) error
	ListCronsForApp(ctx context.Context, appID string) ([]Cron, error)
	ListEnabledCrons(ctx context.Context) ([]Cron, error)
	// MarkCronFired stamps the last_fired_at column. The schedd cron
	// dispatch loop calls this after a synthetic request has been
	// dispatched through gatewayd-internal (spec §4.4, M7). MemStore keeps a
	// lastFiredAt map; PgStore uses a column added in migration 00003.
	MarkCronFired(ctx context.Context, cronID string, at time.Time) error

	// Jobs (issue #1184 Workstream A / ADR-099 supplement).
	// Run-to-completion workloads land across migrations 00255-00257,
	// 00571-00578 (jobs / job_runs / job_tasks + the
	// soft_delete_job_if_no_live_instances helper). The JobStore
	// sub-interface is defined in jobs.go; embedding it here keeps
	// the footnote-heavy comments where they belong (next to the
	// method signatures) and lets narrow test doubles satisfy the
	// JobStore surface without dragging in the whole Store.
	JobStore

	// Workflows (ADR-081 / issue #669).
	// Multi-step durable execution workflows land in the timestamped workflow
	// schema migration.
	// The WorkflowStore sub-interface is defined in workflows.go.
	WorkflowStore

	// Trigger primitive (issue #757 / ADR-0NN; commit #5 + commit #6).
	// Per-method notes:
	//
	//   - TriggerByID / UpdateTrigger / DeleteTrigger take string IDs
	//     because the customer-facing api surfaces string UUIDs on
	//     the wire. The pgx-via-pgtype.UUID conversions live inside
	//     PgStore so the Store interface is uniform with the cron
	//     family (CreateCronIfUnderQuota, CronByID, etc.).
	//
	//   - CreateTriggerIfUnderQuota mirrors CreateCronIfUnderQuota's
	//     FOR UPDATE apps-row lock to defeat the per-app vs
	//     per-account TOCTOU window (see cron precedent in this
	//     Store interface above).
	//
	//   - ListTriggersForApp is the dashboard read-back
	//     (GET /v1/triggers). ListEnabledTriggers is the schedd's
	//     1-second tick scan (commit #14).
	//
	//   - ClaimTriggerRecords uses FOR UPDATE SKIP LOCKED so two
	//     concurrent dispatch workers (multi-host scale-out)
	//     distribute claims without retry-on-collision. Each call
	//     claims up to `limit` records for one trigger.
	//
	//   - Mark* writers are dispatcher-owned (commit #14): the
	//     apid-side operator verbs (retry / drop) bypass this seam.
	TriggerByID(ctx context.Context, id string) (sqlc.Trigger, error)
	UpdateTrigger(ctx context.Context, id string, enabled *bool, config []byte, batchSizeMax, batchWindowMs, maxAttempts, payloadMaxBytes *int32, brokerPoisonStrategy *string, filterCriteria *[]byte) (sqlc.Trigger, error)
	DeleteTrigger(ctx context.Context, id, appID string) error
	ListTriggersForApp(ctx context.Context, appID string) ([]sqlc.Trigger, error)
	ListEnabledTriggers(ctx context.Context) ([]sqlc.Trigger, error)
	CreateTriggerIfUnderQuota(ctx context.Context, appID, kind, slug string, enabled bool, config []byte, batchSizeMax, batchWindowMs, maxAttempts, payloadMaxBytes int32, brokerPoisonStrategy string, limits api.Limits) (sqlc.Trigger, error)
	ClaimTriggerRecords(ctx context.Context, triggerID string, limit int32) ([]sqlc.TriggerRecord, error)
	// InsertTriggerRecord persists a single broker-delivered record
	// into the trigger_records FSM queue so a subsequent
	// ClaimTriggerRecords call can pick it up. Returns the
	// persisted (or existing-on-conflict) trigger_records.id; the
	// dispatcher uses this id as the durable identity for the rest
	// of the FSM walk (succeeded / retry / dead_letter transitions).
	//
	// Review finding #1 (PR #910): without this insert, the
	// dispatch tick is structurally dead — ClaimTriggerRecords
	// returns 0 rows because nothing ever writes to trigger_records.
	InsertTriggerRecord(ctx context.Context, triggerID, itemIdentifier string, payload, headers, metadata []byte) (string, error)
	MarkTriggerRecordSucceeded(ctx context.Context, id string) error
	MarkTriggerRecordRetry(ctx context.Context, id, lastError string, nextFireAt time.Time) error
	MarkTriggerRecordDeadLetter(ctx context.Context, id, lastError string) error
	InsertTriggerDeadLetter(ctx context.Context, recordID, triggerID, reason, routedTo string, detail []byte) error
	// TriggerRecordIDByItemIdentifier resolves a broker-side handle
	// (kafka offset, NATS seq, SQS receipt handle, Redis entry-id,
	// queue invocation_id) to the durable trigger_records.id UUID
	// the dead_letter FK expects. Returns ("", nil) when no row
	// exists yet (rate-limit fires before the record insert on the
	// next dispatch step); callers MUST treat the empty string as
	// "skip the dead_letter insert; the record will be retried on
	// the next tick". See audit round 2 finding #1 (PR #910).
	TriggerRecordIDByItemIdentifier(ctx context.Context, triggerID, itemIdentifier string) (string, error)
	ListTriggerDeadLetter(ctx context.Context, triggerID string, limit int32) ([]sqlc.TriggerDeadLetter, error)
	ListTriggerRecordsForTrigger(ctx context.Context, triggerID string, limit int32) ([]sqlc.TriggerRecord, error)
	RetryTriggerRecordByOperator(ctx context.Context, id string) error
	DropTriggerRecordByOperator(ctx context.Context, id string) error

	// Fire-now request queue (ADR-090 PR-C / migrations/00193).
	// apid inserts on POST /v1/crons/{id}/run; schedd claims +
	// dispatches via RunCronNow. The interface is the single seam
	// between handlers (apid, pkg/api/client.go callers) and the
	// underlying store; MemStore keeps an in-memory slice for tests,
	// PgStore uses the cron_fire_now_requests table.
	InsertFireNowRequest(ctx context.Context, cronID, accountID string) (string, error)
	ClaimPendingFireNowRequest(ctx context.Context) (FireNowRequest, error)
	MarkFireNowRequestSucceeded(ctx context.Context, requestID, invocationID string) error
	MarkFireNowRequestFailed(ctx context.Context, requestID, errMsg string) error
	GetFireNowRequest(ctx context.Context, requestID string) (FireNowRequest, error)

	// Operator intent queue (PR #1099 P2 redesign / migrations/00445).
	// apid inserts on the two admin recovery endpoints
	// (POST /v1/admin/instances/{id}/force-park and
	// POST /v1/admin/apps/{slug}/force-cold-boot), emits
	// db.NotifyOperatorIntent, returns 202 Accepted. schedd (the
	// only consumer) claims via FOR UPDATE SKIP LOCKED LIMIT 1 and
	// dispatches by kind. Routes the admin primitives through a
	// table + pg_notify seam so apid never imports pkg/scheddgrpc
	// (the apid-control-plane-only depguard rule is preserved).
	//
	// Metadata is opaque json.RawMessage; today it's always empty
	// (force_park / force_cold_boot carry no extra payload) but the
	// column is reserved for future per-kind fields without a
	// migration.
	//
	// traceID (PR-#TBD / C2) is the optional OTel W3C 32-char hex
	// trace identifier stamped by the apid force-action handler.
	// Nil leaves the column NULL. The regex CHECK at
	// migrations/00486 enforces the format on INSERT for PgStore;
	// MemStore validates defensively via isOTelHex32.
	InsertOperatorIntent(
		ctx context.Context,
		kind OperatorIntentKind,
		targetID string,
		accountID *string,
		actorID string,
		reason string,
		metadata json.RawMessage,
		traceID *string,
	) (string, error)
	ClaimPendingOperatorIntent(ctx context.Context) (OperatorIntent, error)
	MarkOperatorIntentSucceeded(ctx context.Context, id string, snapIDs []string) error
	// MarkOperatorIntentFailed stamps the row's terminal failure
	// state. snapIDs captures the partial-success shape: when a
	// force_restart dispatch flips the deployment's warm + init
	// snapshots stale but timedDestroy fails (vmmd wedged), the
	// snapshots ARE stale in the database but the destroy is not.
	// Persisting snapIDs here means GET /v1/admin/operator-intents/{id}
	// surfaces "what this action affected" even on the failure
	// path — the operator learns the next wake WILL cold-boot
	// despite the destroy error. snapIDs may be nil (race-loser,
	// unknown-kind, deployment-not-found, etc.).
	MarkOperatorIntentFailed(ctx context.Context, id, errMsg string, snapIDs []string) error
	GetOperatorIntent(ctx context.Context, id string) (OperatorIntent, error)
	// ReclaimStuckRunningOperatorIntents resets every operator_intents
	// row whose status='running' AND whose started_at is older than
	// threshold back to status='pending' (clearing started_at to NULL)
	// so the next ClaimPendingOperatorIntent call picks it up. Used by
	// schedd's operatorIntentStuckRunningTimeout safety tick — without
	// it, a schedd crash between Claim and Mark* leaves the row stuck
	// in `running` forever and the intent is silently dropped.
	//
	// Returns the number of rows affected. Idempotent: a second call
	// with the same threshold after the rows have been re-claimed
	// affects 0 rows. The reclaim is a single UPDATE so the
	// FOR UPDATE SKIP LOCKED claim path sees a consistent snapshot.
	ReclaimStuckRunningOperatorIntents(ctx context.Context, threshold time.Time) (int, error)

	// Runtime configuration (ADR-132). The database stores desired state;
	// daemons apply it to an in-memory snapshot and call
	// MarkRuntimeConfigApplied after validation. pg_notify is emitted by the
	// writer/trigger, but callers must also reconcile from the table after a
	// restart because LISTEN delivery is intentionally not durable.
	ListRuntimeConfigs(ctx context.Context, scope RuntimeConfigScope, scopeID string) ([]RuntimeConfig, error)
	GetRuntimeConfig(ctx context.Context, key string, scope RuntimeConfigScope, scopeID string) (RuntimeConfig, error)
	UpsertRuntimeConfig(ctx context.Context, update RuntimeConfigUpdate) (RuntimeConfig, error)
	MarkRuntimeConfigApplied(ctx context.Context, key string, scope RuntimeConfigScope, scopeID string, version int64, effectiveValue json.RawMessage, applyErr string) error
	CreateRuntimeConfigOperation(ctx context.Context, config RuntimeConfig, actorID, reason string) (RuntimeConfigOperation, error)
	GetRuntimeConfigOperation(ctx context.Context, id string) (RuntimeConfigOperation, error)
	ClaimPendingRuntimeConfigOperation(ctx context.Context) (RuntimeConfigOperation, error)
	MarkRuntimeConfigOperationSucceeded(ctx context.Context, id string, effectiveValue json.RawMessage, appliedCount, targetCount int) error
	MarkRuntimeConfigOperationFailed(ctx context.Context, id, phase, errMsg string) error
	MarkRuntimeConfigOperationBlocked(ctx context.Context, id, phase, reason string) error
	ListRuntimeConfigRevisions(ctx context.Context, key string, scope RuntimeConfigScope, scopeID string, limit int) ([]RuntimeConfigRevision, error)
	GetRuntimeConfigRevision(ctx context.Context, key string, scope RuntimeConfigScope, scopeID string, version int64) (RuntimeConfigRevision, error)

	// OperatorIntentOutcomeMissingCounts (Obs-Meta + Trace-IDs Mega-PR / C7)
	// powers GET /v1/admin/obs/health. Returns a map of kind → count
	// for every operator_intents row that is "stuck running": status
	// is still `running` but started_at is older than threshold. The
	// map's keys cover the closed set
	// {force_park, force_cold_boot, force_restart} plus any
	// zero-count keys the caller asked for — caller-side
	// initialization pins the operator-action vocabulary so the
	// handler never has to special-case empty results.
	OperatorIntentOutcomeMissingCounts(ctx context.Context, threshold time.Time) (map[string]int, error)

	// OperatorActionTraceCompleteness (Obs-Meta + Trace-IDs Mega-PR /
	// C7) powers GET /v1/admin/obs/health's
	// trace_id_completeness_ratio tile. Returns a map of kind →
	// coverage ratio (0.0–1.0) for every events row of kind
	// LIKE 'operator.action.%' received in the last `since`
	// window. The ratio is the count of rows where trace_id IS
	// NOT NULL divided by the total count; when no rows match the
	// kind, the returned value is 1.0 (vacuous truth — the
	// completeness ratio is undefined for an empty set, and the
	// handler surfaces that as 1.0 to avoid a misleading "0%" tile
	// when nothing has happened in the window). Reads from events
	// (live), NOT audit_log (FK-free post-deletion evidence copy).
	OperatorActionTraceCompleteness(ctx context.Context, since time.Time) (map[string]float64, error)

	// Alert rules (issue #396, ADR-045). apid is the only writer;
	// meterd reads via ListEnabledAlertRules and the dispatch + cool-down
	// primitives. Account-scoped (per-app quotas enforced under the
	// per-account scope because account-wide rules do not pin an app).
	//
	// CreateAlertRule is the un-capped insert path used by tests. The
	// customer-facing handler always calls CreateAlertRuleIfUnderQuota
	// (same TOCTOU-defence pattern as CreateCronIfUnderQuota).
	CreateAlertRule(ctx context.Context, r AlertRule) (AlertRule, error)
	// CreateAlertRuleIfUnderQuota inserts a rule iff the per-app and
	// per-account caps (limits.AlertRuleLimitPerApp /
	// AlertRuleLimitPerAccount) are not yet reached. App-scoped rules
	// are counted under both caps; account-wide rules (AppID == "")
	// count toward the per-account cap only. Returns:
	//   - (AlertRule{}, *AlertRuleQuotaError) when either cap trips
	//   - (AlertRule{}, ErrNotFound) when the app row is missing or
	//     deleted — and ignored for account-wide rules (caller passes
	//     an empty appID; the method skips the FOR UPDATE row lock)
	//   - (AlertRule{}, ErrConflict) on a duplicate (account_id, name)
	CreateAlertRuleIfUnderQuota(ctx context.Context, r AlertRule, limits api.Limits) (AlertRule, error)
	AlertRuleByID(ctx context.Context, id string) (AlertRule, error)
	// AlertRuleByAccountAppAndPresetName resolves the alert_rules
	// row that was instantiated from a catalog preset (issue #1233
	// / ADR-123 PR-C "Send test alert" button). ADR-123 deliberately
	// rejected a preset_id FK on alert_rules — the catalog binding is
	// the parsed display-name prefix "<DisplayName> (<app_slug>)"
	// pinned by the createAlertRuleIfUnderQuota path at
	// handlers_alert_presets.go:255. We match by joining
	// alert_presets on (name) and selecting the rule whose name LIKE
	// (display_name || ' (%'); the existing
	// alert_rules_account_name_uniq index covers the LIKE prefix
	// scan as a range scan on (account_id, name) — no new index.
	//
	// Returns:
	//   - (AlertRule{}, ErrNotFound) when no rule exists for this
	//     (account, app, preset). The handler surfaces a 404 — a
	//     "send test" click on a card the customer never enabled is
	//     a UX bug, not a 500.
	//   - (AlertRule{}, ErrConflict) when the LIKE prefix matches
	//     >1 row (should not happen — the name is unique per
	//     (account_id, app_id) — but the pgstore row-lock turn
	//     exposes it as a defensive guard).
	AlertRuleByAccountAppAndPresetName(ctx context.Context, accountID, appID, presetName string) (AlertRule, error)
	// UpdateAlertRule mutates the optional fields of an alert row. nil
	// pointers leave the field untouched; the WebhookSecretSealed
	// argument is *[]byte so a nil means "don't reseal" (typical for
	// the customer editing name/threshold without rotating the secret)
	// and a non-nil replaces the seal. All other fields share the
	// nil-skip update convention.
	UpdateAlertRule(ctx context.Context, id string, params UpdateAlertRuleParams) (AlertRule, error)
	DeleteAlertRule(ctx context.Context, id string) error
	ListAlertRulesForAccount(ctx context.Context, accountID string) ([]AlertRule, error)
	// ListEnabledAlertRules returns every enabled rule across every
	// account. meterd's evaluator tick walks this in one round-trip
	// (one-box scale, spec §4.3). The partial index
	// alert_rules_enabled_idx keeps the sweep cheap; meterd's
	// per-rule target resolution happens inside the evaluator.
	ListEnabledAlertRules(ctx context.Context) ([]AlertRule, error)

	// ClaimAlertFire is the cool-down prime primitive (criterion 4).
	// Atomically attempts to claim a fire slot keyed by idempotencyKey
	// (rule_id + ':' + floor(unix_seconds/cooldown_seconds)); returns
	// won=true only on the FIRST claim inside that bucket. Subsequent
	// claims inside the same bucket return won=false. The CTE mirrors
	// LoadAndStampLastQuotaWarning (pgstore.go) and captures the OLD
	// stamp BEFORE the UPDATE so the predicate "$2 = old" doesn't
	// trivially succeed post-write (the regression CI caught in PR
	// #69 / memory/pkg-state-usage-monthly-tz-compare.md).
	//
	// payload and observed are stamped onto the alert_deliveries row
	// inside the same transaction so the row exists with its full
	// payload at insert time (avoids a window where the row is visible
	// to a dashboard scrape before the dispatcher would have stamped
	// it). payload may be empty for callers that don't track the
	// observed envelope — the INSERT writes '{}' so the dashboard's
	// `payload ->> 'metric'` query is safe.
	//
	// On won=true the returned deliveryID is the inserted row's UUID;
	// callers pass that to UpdateAlertDeliveryStatus after the
	// dispatch. On won=false the deliveryID is empty (the caller
	// stays silent and lets the cool-down carry on). On a missing
	// rule row, returns ("", false, ErrNotFound).
	ClaimAlertFire(ctx context.Context, ruleID, idempotencyKey string, payload []byte, observed float64, at time.Time) (deliveryID string, won bool, err error)
	// SetAlertRuleState transitions a rule's cool-down state. Called by
	// the evaluator on healthy ticks (firing→ok) and after a successful
	// delivery (ok→firing). returns (true, nil) on a real transition,
	// (false, nil) when the state already matched (no-op), and
	// (false, ErrNotFound) when the rule is missing.
	SetAlertRuleState(ctx context.Context, ruleID string, to AlertState, at time.Time) (changed bool, err error)
	// SetAlertRuleLastEvaluated stamps the last_evaluated_at column.
	// Called on every tick so the dashboard can surface "evaluated N
	// seconds ago". Fire-and-forget — failures are warn-logged at the
	// call site and never propagate.
	SetAlertRuleLastEvaluated(ctx context.Context, ruleID string, at time.Time) error

	// Alert deliveries (issue #396, ADR-045). ClaimAlertFire is the
	// canonical row-creation path: both PgStore and MemStore insert
	// the alert_deliveries row inside the claim transaction (UNIQUE
	// on idempotency_key is the load-bearing bucket dedupe). The
	// meterd evaluator (PR 4) consumes the returned deliveryID and
	// calls UpdateAlertDeliveryStatus after the dispatcher fires.
	// RecordAlertDelivery remains on the interface for ad-hoc
	// callers (test seeds, future dispatcher-direct paths, etc.)
	// and surfaces SQLSTATE 23505 / ErrConflict on a duplicate.
	RecordAlertDelivery(ctx context.Context, d AlertDelivery) (AlertDelivery, error)
	// UpdateAlertDeliveryStatus mutates the retry record in place.
	// Called after each attempt by the dispatcher (PR 2 / PR 4).
	UpdateAlertDeliveryStatus(ctx context.Context, id string, status AlertDeliveryStatus, attempt int, statusCode int, lastErr string, deliveredAt *time.Time) error
	// ListAlertDeliveriesForRule returns the most-recent delivery
	// rows for ruleID, newest-first, capped at limit. includeTest
	// (ADR-123 PR-D) toggles the `WHERE is_test = false` filter:
	//   - false → production rows only (the customer-facing default
	//     and the dashboard's "recent deliveries" pane)
	//   - true  → all rows, including Dispatcher.DispatchTest writes
	//     (the operator pane reachable via `?include_test=true`)
	// The partial index alert_deliveries_rule_fired_production_idx
	// (migrations/00528_alert_deliveries_is_test.sql) covers the
	// include_test=false path so the production read stays
	// index-only even as test row count grows unbounded.
	ListAlertDeliveriesForRule(ctx context.Context, ruleID string, limit int, includeTest bool) ([]AlertDelivery, error)

	// Edge rules (ADR-089, planned). apid is the only writer;
	// gatewayd-internal reads via MatchEdgeRulesForHost. Per-app
	// scope only — there is no account-wide flavour. The action
	// column is jsonb (kind-tagged union); see EdgeRuleAction in
	// types.go for the per-kind shapes.
	//
	// CreateEdgeRule is the un-capped insert path used by tests.
	// The customer-facing handler always calls
	// CreateEdgeRuleIfUnderQuota (same TOCTOU-defence pattern as
	// CreateAlertRuleIfUnderQuota). The quota is enforced under
	// FOR UPDATE on the parent app row so concurrent inserts can't
	// race past the cap.
	CreateEdgeRule(ctx context.Context, in CreateEdgeRuleParams) (EdgeRule, error)
	// CreateEdgeRuleIfUnderQuota inserts the rule iff the app is
	// under its plan's per-app quota (limits.EdgeRulesPerApp).
	// Returns:
	//   - (EdgeRule{}, *EdgeRuleQuotaError) when the cap trips
	//   - (EdgeRule{}, ErrNotFound) when the app row is missing
	//   - (EdgeRule{}, ErrConflict) on a FK violation (account gone)
	CreateEdgeRuleIfUnderQuota(ctx context.Context, in CreateEdgeRuleParams, limits api.Limits) (EdgeRule, error)
	ListEdgeRulesForAccount(ctx context.Context, accountID string) ([]EdgeRule, error)
	ListEdgeRulesForApp(ctx context.Context, appID string) ([]EdgeRule, error)
	GetEdgeRuleByID(ctx context.Context, id string) (EdgeRule, error)
	// UpdateEdgeRule coalesces the optional fields onto edge_rules.
	// nil pointers leave the field untouched; Action is
	// *EdgeRuleAction because a nil means "do not touch the jsonb
	// column"; a non-nil replaces it wholesale. The kind-tagged
	// union has no partial-update shape — the customer re-sends
	// the full action body.
	UpdateEdgeRule(ctx context.Context, id string, params UpdateEdgeRuleParams) (EdgeRule, error)
	DeleteEdgeRule(ctx context.Context, id string) error
	// ListCorsPresetsForAccount returns every preset the account
	// owns (both account-wide and app-scoped). The gatewayd cache
	// refreshes its preset map on pg_notify('cors_preset_changed')
	// and on boot reads via this path. The ordered-by-name return
	// keeps the deterministic cache key (per cmd/gatewayd-internal
	// /edge_rules.go::compileCORSRules).
	ListCorsPresetsForAccount(ctx context.Context, accountID string) ([]CorsPreset, error)
	// ListCorsPresetsForApp returns the app-scoped presets for one
	// app. accountID is required as defense-in-depth so a caller
	// cannot probe "do I have any preset for this app?" across
	// tenants — the SQL already pins the row to the app_id (which
	// is FK-scoped to the account), but passing accountID at the
	// Store boundary lets the same compile-side IDOR guard the
	// other read methods use. The compile path in PR-B calls this
	// to overlay app-scoped presets on top of the account-wide
	// set returned by ListCorsPresetsForAccount.
	ListCorsPresetsForApp(ctx context.Context, accountID, appID string) ([]CorsPreset, error)
	// GetCorsPresetByID returns one preset scoped to the caller's
	// account or ErrNotFound. accountID is required so the Store
	// boundary enforces tenancy — PR-B's apid CRUD surface
	// (GET/PATCH/DELETE by id) calls this directly and cannot
	// forget the AccountID compare (the previous design pushed the
	// compare into the merge helper, which is bypassable for any
	// caller that only wants the row). ErrNotFound is mapped to
	// 422 at the apid boundary ("preset has been deleted; re-save
	// the rule") and to 5xx at the gateway compile path.
	GetCorsPresetByID(ctx context.Context, accountID, id string) (CorsPreset, error)
	// CreateCorsPresetIfUnderQuota (issue #975 #4 PR-B / ADR-129
	// D2) inserts a preset iff both caps (per-app + per-account)
	// have room. Returns:
	//   - (CorsPreset{}, *CorsPresetQuotaError) when either cap trips
	//   - (CorsPreset{}, ErrConflict) on UNIQUE collision
	//     ((account_id, COALESCE(app_id, ...), name))
	//   - (CorsPreset{}, ErrNotFound) when AppID is set and the
	//     apps row is gone
	// The pgstore implementation runs the per-app row lock +
	// per-app count + per-account count in a single tx so parallel
	// inserts cannot race past the cap. Memstore mirrors via the
	// mutex. The "if under quota" prefix is a TOCTOU-defence naming
	// convention borrowed from CreateEdgeRuleIfUnderQuota.
	CreateCorsPresetIfUnderQuota(ctx context.Context, p CorsPreset, limits api.Limits) (CorsPreset, error)
	// UpdateCorsPreset is the un-quota'd PATCH path. The caller is
	// expected to have already validated name/origin/method
	// counts at the apid write boundary. The pgstore implementation
	// emits pg_notify('cors_preset_changed', $account_id) after the
	// UPDATE commits so the gatewayd-internal cache reloads the
	// account's preset overlay.
	UpdateCorsPreset(ctx context.Context, accountID, id string, p CorsPreset) (CorsPreset, error)
	// DeleteCorsPreset removes a preset by id (scoped to the
	// caller's account; cross-account deletes return
	// ErrNotFound). The pgstore implementation emits
	// pg_notify('cors_preset_changed', $account_id) so any
	// gatewayd-internal compile cache that references this preset
	// via edge_rules.cors_preset_id is invalidated; the next
	// compile reads the preset as missing and the rule's
	// ON DELETE SET NULL FK clears, so MergeCorsPresetIntoRule
	// fails closed (ADR-129 D3).
	DeleteCorsPreset(ctx context.Context, accountID, id string) error
	// CountEdgeRulesForApp is the quota check (called by the apid
	// handler before the insert; the insert itself runs the same
	// count inside the FOR UPDATE on the apps row).
	CountEdgeRulesForApp(ctx context.Context, appID string) (int, error)
	// CountEdgeRulesByKindForApp is the per-kind quota check
	// (ADR-091 D22 — kind=geo has a tighter per-app cap than the
	// general EdgeRulesPerApp; Free=1 vs 5). Called by the apid
	// handler to surface the specific kind + cap to the customer
	// (e.g. "kind=geo: 1/1 rules used on Free"). The store
	// implementations run the count inside the same apps-row FOR
	// UPDATE lock for race-freedom against parallel inserts.
	CountEdgeRulesByKindForApp(ctx context.Context, appID string, kind EdgeRuleKind) (int, error)
	// MatchEdgeRulesForHost is the gateway hot-path read. Returns
	// every enabled rule whose match_host matches `host` (or "*"),
	// ordered by priority ASC. The gatewayd matcher iterates in
	// priority order and short-circuits on first match. Returns
	// the full action payload so the gateway doesn't need a
	// second round-trip per kind.
	MatchEdgeRulesForHost(ctx context.Context, host string) ([]EdgeRule, error)

	// CountFailedInvocationsSince counts terminal-failed invocations on
	// (accountID, appID, source) since `since`. The meterd evaluator
	// uses this for the failed_invocations metric branch — Issue
	// #396 criterion 2's "scheduled-work failure" axis. appID is
	// "": both scoped and un-scoped rules reach this; caller decides
	// (account-wide rules expand to per-app at evaluation time, then
	// the per-app counts return here). source 'any' is expanded to
	// the four InvocationSource values by the caller before the call.
	CountFailedInvocationsSince(ctx context.Context, accountID, appID string, source InvocationSource, since time.Time) (int, error)

	// Alert preset catalog (issue #1233 / ADR-123). The catalog has
	// 8 system-owned rows; the Store exposes only read methods. The
	// only write path is migration 00348's idempotent seed.
	ListAlertPresets(ctx context.Context) ([]AlertPreset, error)
	AlertPresetByName(ctx context.Context, name string) (AlertPreset, error)

	// CountFailedDeploymentsSince counts deployments in
	// status='failed' for (accountID, appID) since `since`. Used
	// by the alert evaluator's deployment_failed metric branch
	// (issue #1233, ADR-123). Mirrors CountFailedInvocationsSince
	// but walks the deployments table; appID "" means "any app on
	// this account" per the Store contract.
	CountFailedDeploymentsSince(ctx context.Context, accountID, appID string, since time.Time) (int, error)

	// WasInvokedSuccessfullySince returns true iff at least one
	// non-failed invocation exists for (accountID, appID) since
	// `since`. Used by the alert evaluator's api_up metric branch
	// (issue #1233, ADR-123) — the binary reachability signal.
	// appID "" means "any app on this account" per the Store
	// contract; a cold-start app with no invocations returns false.
	WasInvokedSuccessfullySince(ctx context.Context, accountID, appID string, since time.Time) (bool, error)

	// MTDSpendEurCents returns the SUM(eur_cents) of every
	// account_spend_snapshot row for the account whose
	// period_start is within the current UTC month-to-date window.
	// Used by the alert evaluator's account_spend_eur metric
	// branch (issue #1233, ADR-123).
	MTDSpendEurCents(ctx context.Context, accountID string) (int64, error)

	// UpsertAccountSpendSnapshot is called by the meterd tick
	// loop on every AlertEvalInterval. Idempotent via the
	// (account_id, source, period_end) UNIQUE at migrations/00350.
	UpsertAccountSpendSnapshot(ctx context.Context, accountID string, periodStart, periodEnd time.Time, gbSeconds float64, eurCents int64, source string) error

	// MinCertExpiryForApp returns the smallest remaining seconds
	// until cert expiry across all per-app tenant_surfaces for the
	// given (account, app), or -1 when no surface has a cert in
	// 'ok' state. Used by the alert evaluator's cert_expiry_seconds
	// metric branch (issue #1233, ADR-123).
	MinCertExpiryForApp(ctx context.Context, accountID, appID string) (int64, error)

	// RefreshCertExpiryStates walks tenant_surfaces for rows with
	// cert_state='issued', upserts
	// meterd_tenant_surface_cert_expiry_state, and stamps
	// last_refreshed_at = now(). Returns the number of rows
	// updated. Called by the meterd cert-expiry refresher goroutine
	// (issue #1233, ADR-123) on a 1-hour cadence. Note: the table
	// is meterd-owned per the CLAUDE.md ownership rule; the writer
	// runs in cmd/meterd; readers (apid / alert-evaluator) use
	// MinCertExpiryForApp below.
	RefreshCertExpiryStates(ctx context.Context) (int, error)
	// ListCertExpiryStateForWalker returns every row in
	// meterd_tenant_surface_cert_expiry_state whose
	// last_refreshed_at is fresher than (now - staleCutoff).
	// Meterd's refresher uses this to stamp the
	// apid_tenant_surface_cert_expiry_seconds gauge (the metric
	// name keeps its legacy apid_ prefix for backward-compat with
	// already-deployed alert rules).
	ListCertExpiryStateForWalker(ctx context.Context, staleCutoff time.Duration) ([]TenantSurfaceCertExpiryState, error)

	// Invocations (Move 1 — async_invoke / queue / delayed_task / cron).
	// apid writes customer-intent rows; schedd's drain loop owns the
	// state transitions pending → dispatching → completed/failed.
	// InstanceID is stamped by ClaimInvocation (state→dispatching) so
	// pkg/meter can join. instance_id is unique to a dispatched row.
	EnqueueInvocation(ctx context.Context, inv Invocation) (Invocation, error)
	InvocationByID(ctx context.Context, id string) (Invocation, error)
	// ListDueInvocations returns up to `limit` rows whose state='pending'
	// and due_at <= now, ordered by due_at. The drain tick calls this with
	// LIMIT 64 inside a `for update skip locked` (MemStore is single-process
	// and intrinsically serialised; PgStore uses the row-level lock to
	// support a future multi-leader schedd without an ADR follow-up).
	ListDueInvocations(ctx context.Context, now time.Time, limit int) ([]Invocation, error)
	// ClaimInvocation atomically transitions pending → dispatching and
	// stamps lease_expires_at = now + leaseSeconds. The drain writes the
	// InstanceID it just woke as well. No-op if already dispatching with
	// an unexpired lease; the returned row reflects the post-state so the
	// caller can branch on claimed.State to recover from a double-claim.
	ClaimInvocation(ctx context.Context, id, instanceID string, leaseSeconds int) (Invocation, error)
	// ClaimInvocationWithCap is the cap-aware variant of
	// ClaimInvocation (ADR-134 PR-B). Atomically transitions
	// pending → dispatching + lease stamp + per-account counter
	// increment so the cap cannot be raced. Returns
	// ErrQuotaExceeded when the account's current_inflight is at
	// max_inflight. The MemStore shim implements the same
	// semantics in-process.
	ClaimInvocationWithCap(ctx context.Context, id, instanceID string, leaseSeconds, maxInflight int) (Invocation, error)
	// EnsureAccountAsyncQuota upserts the per-account cap row with
	// the given max_inflight. Returns the resulting
	// (max_inflight, current_inflight) pair.
	EnsureAccountAsyncQuota(ctx context.Context, accountID string, maxInflight int) (int, int, error)
	// GetAccountAsyncQuota returns the cap row's
	// (max_inflight, current_inflight) pair or ErrNotFound when
	// the row is missing.
	GetAccountAsyncQuota(ctx context.Context, accountID string) (int, int, error)
	// DecrementAccountAsyncInflight drops the per-account counter
	// by 1, clamped at zero via greatest(). Tolerant of missing
	// cap row.
	DecrementAccountAsyncInflight(ctx context.Context, accountID string) error
	// ListExpiredInvocationsForReaper returns IDs whose
	// result_retention_until is in the past.
	ListExpiredInvocationsForReaper(ctx context.Context, now time.Time, limit int) ([]string, error)
	// DeleteInvocationsByIDs removes the given rows. Returns the
	// number deleted.
	DeleteInvocationsByIDs(ctx context.Context, ids []string) (int, error)
	// ListDeadlineBreachedInvocations returns IDs still in
	// (pending|dispatching) whose deadline_at is in the past.
	ListDeadlineBreachedInvocations(ctx context.Context, now time.Time, limit int) ([]string, error)
	// ForceDeadlineBreachedInvocations transitions the listed IDs
	// to dead_letter with outcome='deadline'. Decrements the
	// per-account counter for each transitioned row.
	ForceDeadlineBreachedInvocations(ctx context.Context, ids []string) (int, error)
	// RetryQueueDeadLetter (ADR-134 PR-C) resets an invocations
	// row in state='dead_letter' back to 'pending' with
	// attempts=0, stamping last_replayed_at=NOW(). Scoped to the
	// caller-supplied accountID. Returns ErrNotFound when the
	// row is missing, not dead_letter, or owned by another
	// account.
	RetryQueueDeadLetter(ctx context.Context, accountID, invocationID string) (Invocation, error)
	// ListExpiredTriggerRecordsForReaper (ADR-134 PR-E) returns
	// trigger_records IDs whose result_retention_until is in
	// the past.
	ListExpiredTriggerRecordsForReaper(ctx context.Context, now time.Time, limit int) ([]string, error)
	// DeleteTriggerRecordsByIDs removes the given rows. Returns
	// the count deleted.
	DeleteTriggerRecordsByIDs(ctx context.Context, ids []string) (int, error)
	// CompleteInvocation finalises a dispatched row with an optional result
	// envelope (response status + body bytes for sync invoke; nil for the
	// other sources). State → completed.
	CompleteInvocation(ctx context.Context, id string, result json.RawMessage) error
	// FailInvocation records a terminal or retryable error. When retryAfter
	// > 0 the row goes back to state='pending' with due_at = now +
	// retryAfter; when retryAfter == 0 the row is terminal ('failed').
	// The drain uses the transient path on Wake/Invoke queue-full / timeout;
	// the permanent path on shape / capacity errors.
	//
	// budget (issue #394) is the per-plan retry ceiling (see
	// pkg/api.Limits.MaxQueueAttempts). Pass 0 to disable the budget —
	// the row will retry indefinitely regardless of `attempts`. Pass
	// any positive integer to enable the budget; once `attempts + 1`
	// would meet or exceed `budget`, the transient path transitions
	// the row to state='dead_letter' instead of state='pending'.
	// The legacy delayed-task-cap and account-suspended callers pass
	// budget=0 because their retry semantics are not plan-scoped; the
	// queue-source drain caller (drain.go:279/306) passes
	// plan.LimitsForPlan(acct.Plan).MaxQueueAttempts.
	//
	// opts (issue #791) refines the durable Outcome stamped on the
	// permanent branch. The default is OutcomeFailed; the drain's two
	// deadline paths pass WithOutcome(OutcomeTimeout) so the cron
	// run-history surface can distinguish a blown deadline from a
	// generic failure without parsing lastError. Ignored on the
	// transient branch (the row stays non-terminal, so it carries no
	// outcome) and overridden by the dead-letter branch.
	FailInvocation(ctx context.Context, id string, lastError string, retryAfter time.Duration, budget int, opts ...FailOption) error
	// CountPendingInvocations is index-backed by invocations_app_pending_idx;
	// used by the apid cap check on POST .../queues/invocations:send and
	// POST /v1/apps/{slug}/delayed-tasks, and by the drain's cap re-check
	// on DispatchDelayedTask rows.
	CountPendingInvocations(ctx context.Context, appID string, source InvocationSource) (int, error)
	// CancelInvocation moves a pending row to state='cancelled'. Customer
	// DELETE on /v1/delayed-tasks/{id}; the drain skips cancelled rows.
	// Returns ErrNotFound if the row is already terminal.
	CancelInvocation(ctx context.Context, id string) error
	// ListInvocationsForAccount is the dashboard's "recent invocations"
	// view; pagination cursor is the same opaque `before` convention used
	// by ListDeployments. The cursor is an Invocation.ID (uuid) — the
	// handler pages by ?before=<id>; "" means "start from the newest".
	// Move 2 cursor change: was time.Time (drifted across equal-second
	// rows); id is stable across ties.
	ListInvocationsForAccount(ctx context.Context, accountID string, limit int, before string) ([]Invocation, error)
	// ListInvocationsForApp is the per-app filtered variant used by
	// deleteApp's GC sweep (cancel every pending/dispatching row before
	// the app row goes away). Index-backed by `invocations_app_pending_idx`
	// (migrations/00030_invocations.sql) when the states filter is the
	// partial-index predicate (pending + dispatching); for other state
	// combinations the planner falls back to a sequential scan, which is
	// fine for the rare delete path.
	ListInvocationsForApp(ctx context.Context, appID string, states ...InvocationState) ([]Invocation, error)
	// ListCronRunsForCron is the per-cron run-history read (issue #791)
	// backing GET /v1/crons/{id}/runs. Filters on cron_id — the FK
	// schedd's dispatchOneCron stamps on every fire — and pages with the
	// same opaque `before` cursor convention as ListInvocationsForAccount
	// (an Invocation.ID; "" means "start from the newest"). Index-backed
	// by invocations_cron_idx (migrations/00166).
	//
	// Deliberately NOT account-scoped in SQL: the caller must have
	// already resolved the cron to an app it owns. apid's listCronRuns
	// does this with the CronByID → AppByID → AccountID check that
	// updateCron/deleteCron use, so a cross-tenant id 404s before it
	// reaches the store.
	ListCronRunsForCron(ctx context.Context, cronID string, limit int, before string) ([]Invocation, error)

	// Queue introspection (issue #394, Move 1 dead-letter + read API).
	// The three read-only methods back GET /v1/apps/{slug}/queues/{state,peek,dead_letter}
	// and are read-only against the invocations table — no advisory locks,
	// no UPDATE/INSERT/DELETE in the implementation. The PgStore
	// implementation uses invocations_app_pending_idx (state slice) and
	// invocations_app_dead_letter_idx (dead_letter filter) for index-only
	// scans on the hot path.
	//
	// QueueState returns the per-app live counters — depth
	// (pending+dispatching), in_flight (dispatching with lease_expires_at
	// either NULL or in the future), and the oldest pending created_at.
	// Used by the queueStats handler. OldestPendingAt is the zero-time
	// when the app has no pending rows; callers translate to nil.
	QueueState(ctx context.Context, appID string) (QueueStats, error)
	// QueuePeek lists the oldest pending queue messages for an app
	// without acquiring a lease or incrementing attempts. Paginated by
	// `before` (a queue row id, uuid) — same cursor convention as
	// ListInvocationsForAccount. `limit` is clamped by the handler to
	// [1, 200] (default 20); the store itself does no clamping. Returns
	// rows ordered by (created_at ASC, id ASC).
	QueuePeek(ctx context.Context, appID string, limit int, before string) ([]Invocation, error)
	// QueueDeadLetter lists dead-letter rows (state='dead_letter') for
	// an app. Same cursor / limit / ordering as QueuePeek. Backed by
	// invocations_app_dead_letter_idx.
	QueueDeadLetter(ctx context.Context, appID string, limit int, before string) ([]Invocation, error)
	// CountInstanceInvocationsInMinute is the meter's join key: it counts
	// dispatched rows for (instance, minute) so SampleAndRoll can set
	// usage_minutes.requests = N on each rolling minute. Index-backed by
	// invocations_instance_idx.
	CountInstanceInvocationsInMinute(ctx context.Context, instanceID string, minute time.Time) (int, error)
	// StampInstanceInvocation writes the live instance handle onto a
	// dispatching row. The drain calls this AFTER engine.Wake returns,
	// because the wake gate hands the drain the instance id only after
	// admission + boot, not at claim time. The column drives the meter
	// join; without this stamp the meter's CountInstanceInvocationsInMinute
	// sees 0 invocations for the minute and under-bills. State must be
	// 'dispatching' to avoid racing CompleteInvocation. Returns
	// ErrNotFound if no matching row exists in dispatching state.
	StampInstanceInvocation(ctx context.Context, id, instanceID string) error

	// Instances (schedd is sole writer, spec §6). apid reads only.
	//
	// nodeID is the compute_node the instance lives on (issue #97 /
	// ADR-025 axis 3). schedd's Wake flow resolves it via
	// sched.ChoosePlacement at instance creation; tests that don't
	// exercise routing may pass DefaultLocalNodeName (or the id
	// resolved via ComputeNodeByName) — the engine never accepts an
	// empty node_id once CreateInstance is reached, so the legacy
	// single-box path always passes DefaultLocalNodeName at minimum.
	//
	// wakeID is the per-wake-attempt correlation handle (gaps analysis
	// 2026-07-23); schedd mints this UUIDv7 in Engine.Wake Phase 2 right
	// before the INSERT so every row created by Wake carries a unique
	// wake_id. An empty wakeID triggers the column default
	// (gen_random_uuid()) which is the safe behavior for any caller that
	// hasn't been updated yet (test fixtures, ad-hoc backfill scripts).
	// Migration 00028 enforces NOT NULL going forward, so passing empty
	// is fine — the row still has a non-NULL wake_id after the write.
	CreateInstance(ctx context.Context, appID, deploymentID, state string, ramMB int, nodeID, wakeID string) (Instance, error)
	// CreateInstanceWithMode (issue #72 / ADR-125 PR-A3) is the
	// mode-aware overload the schedd uses to stamp mirror and
	// execution-mode values on new rows. mode must be one of the
	// InstanceMode constants admitted by the instances.mode CHECK.
	// The legacy CreateInstance method preserves its no-mode signature
	// (mode='normal' is the column default) so test fixtures and pre-A3
	// callers continue to work bit-for-bit.
	CreateInstanceWithMode(ctx context.Context, appID, deploymentID, state string, ramMB int, nodeID, wakeID, mode string) (Instance, error)
	// CreateJobInstance creates the job-task shape required by the jobs
	// schema: app_id and deployment_id are NULL, while job_id and kind are
	// populated. Job definitions point at OCI images directly, so there is
	// no deployment row to reuse for this instance.
	CreateJobInstance(ctx context.Context, instanceID, jobID, runID string, taskIndex int, state string, ramMB int, nodeID, wakeID string) (Instance, error)
	InstanceByID(ctx context.Context, id string) (Instance, error)
	// ReadActiveInstanceForWakeID is the cluster-coord lookup
	// primitive (multi-host safety cluster PR-5 / audit F4). When
	// CreateInstance returns ErrConcurrentWake (the partial unique
	// index instances_wake_attempt_active_idx rejected a duplicate
	// in-flight row), the engine's retry path calls this to discover
	// the winner's instance_id and observe its state transition
	// through the existing in-process state machine.
	ReadActiveInstanceForWakeID(ctx context.Context, wakeID string) (Instance, error)
	ListInstancesForApp(ctx context.Context, appID string) ([]Instance, error)
	// StampAppScaleOut (PR-C, issue #462) records apps.last_scale_out_at
	// = now() on the wake-gate admit path. Non-atomic with the
	// instances INSERT — single-leader schedd + per-app appMu
	// serialise the wake path; the second line of defence is the
	// wake-gate admitGate consult, which sees the freshly-stamped
	// column on the next wake attempt. The "stamp miss" direction
	// (the stamp UPDATEs after the instance INSERT, and a rare
	// concurrent wake sees NULL on the consult) is the SAFE
	// direction: the consult bypasses cooldown on NULL and the
	// wake proceeds normally. The opposite (Tx-wrapped insert +
	// stamp) would buy nothing because the wake-gate still
	// consults before INSERT.
	StampAppScaleOut(ctx context.Context, appID string) error
	// StampAppScaleIn (PR-C, issue #462) records apps.last_scale_in_at
	// = now() on the reaper park branch. Same shape as
	// StampAppScaleOut — best-effort, post-park call; stamp failure
	// does NOT roll back the park.
	StampAppScaleIn(ctx context.Context, appID string) error
	// ListLatestInstancesForApp returns up to `limit` instance rows for
	// appID ordered by started_at DESC. The dashboard's app-detail
	// "Recent wakes" table uses this to bound the per-render scan at
	// the SQL layer instead of fetching every row (including parked
	// history) and sorting in Go. limit must be > 0; a value ≤ 0
	// returns an empty slice so the caller fails closed rather than
	// rendering an unbounded table. The supporting partial index
	// `instances_wake_id_app_idx` (migration 00028) covers live
	// states but not parked; the SQL still scans parked rows in the
	// sort phase, so a future index on (app_id, started_at DESC)
	// WHERE state = 'parked' is the right optimization if a single
	// app accumulates enough parked history to make this slow.
	ListLatestInstancesForApp(ctx context.Context, appID string, limit int) ([]Instance, error)
	// ListLatestInstancePerApp returns the most-recently-started instance
	// for each app belonging to the account. Empty map when no instance
	// rows exist yet (a fresh deploy never woken). Used by the dashboard
	// to populate the cold-wake state badge in one round-trip instead of
	// N per-app ListInstancesForApp calls (PR #48 follow-up). Result is
	// keyed by app ID; callers must handle the "no row" case explicitly.
	ListLatestInstancePerApp(ctx context.Context, accountID string) (map[string]Instance, error)
	// LookupBootStartedForWakes (ADR-123) returns the wake-boot
	// telemetry (trigger / queued_count / concurrency_at_admit) for
	// each wake_id in the input slice. Batched in one SQL round-trip
	// (uses the events_wake_id_idx jsonb expression index) so the
	// dashboard's "Recent wakes" table doesn't fan out per row.
	// Empty map when no rows match (pre-ADR-123 fleet).
	LookupBootStartedForWakes(ctx context.Context, wakeIDs []string) (map[string]WakeBootMeta, error)
	// CountWakeBootStarted24h returns the count of wake.boot_started
	// events the schedd recorded for the given app in the trailing
	// 24 hours. Used by cmd/apid/handlers_metrics.go to populate
	// the AppMetricsResponse.Wakes24h field on the customer-facing
	// per-app dashboard (Free is gated off; Hobby/Pro/Scale only
	// — see pkg/api/limits.go::PerAppMetricsAllowed). Returns 0 on
	// an empty app, a degraded store call, or when the events
	// table predates the post-ADR-123 schema (pre-ADR-123
	// boot_started rows carry no app_id field, so the cast
	// returns NULL which COUNT(*) coerces to 0).
	//
	// Performance: the (data->>'app_id')::uuid predicate is NOT
	// covered by the existing events_wake_id_idx jsonb expression
	// index (migration 00114 indexes data->>'wake_id', not app_id).
	// On a Scale-tier app with a large wake fleet the planner will
	// seq-scan the trailing-24h wake.boot_started rows and
	// re-evaluate the jsonb cast per row. A follow-up migration
	// adding a covering index on (data->>'app_id', at) is tracked
	// separately.
	CountWakeBootStarted24h(ctx context.Context, appID string) (int64, error)
	// ListAllInstances returns every instance on the box, ordered newest
	// first. schedd's G7 reaper warm-passes this slice to the conntrack
	// reader (pkg/sched/flowcount) once per tick — a single bulk read is
	// cheaper than a per-app loop and lets the reader index by host_ip
	// up front. Scoped to RUNNING/WAKING/COLD_BOOTING/SNAPSHOTTING because
	// parked/stopped/failed instances have no veth and no flows by
	// construction (invariant §6.2-4). The partial index
	// `instances_reaper_state_idx` (migration 00009) covers this query.
	ListAllInstances(ctx context.Context) ([]Instance, error)
	// ListInstancesForAccount returns every live instance belonging to an
	// account. Used by the meterd quota loop to park everything when a Free
	// account crosses 100 % (spec §4.7). Per-account scan is O(instances);
	// on a one-box that's bounded by max_concurrency(plan) × apps, fine to
	// run on the minute boundary.
	ListInstancesForAccount(ctx context.Context, accountID string) ([]Instance, error)
	// ListInstancesForAccountPaged is the cursor-paginated variant of
	// ListInstancesForAccount (issue #393). The cursor is the
	// instances.id UUIDv7; the SQL filter `id < $before` pages
	// backwards in started_at DESC order. Used by the account-scoped
	// dashboard pages so one call replaces N per-app fan-outs. The
	// limit is server-side clamped to 1..100 — the handler validates
	// before this call so the SQL stays narrow.
	ListInstancesForAccountPaged(ctx context.Context, accountID string, limit int, before string) ([]Instance, error)
	UpdateInstanceState(ctx context.Context, id, state string) error
	// UpdateInstanceStateIf atomically changes an instance state only when
	// its current state equals expectedState. It returns ErrConflict when
	// the row is missing or another writer changed the state first. Recovery
	// paths use this to make load → validate → write transitions race-safe.
	// A transition to parked also stamps parked_at, preserving the retention
	// and watchdog invariant for direct recovery recreates.
	UpdateInstanceStateIf(ctx context.Context, id, expectedState, nextState string) error
	// UpdateInstanceStateWithTimestamp is the same write but stamps
	// parked_at to the supplied time on the same statement. Used by
	// schedd's snapshotAndPark (commit 3) when transitioning into
	// SNAPSHOTTING — the §6.1 watchdog reads parked_at for
	// SNAPSHOTTING rows (started_at means "row creation", not "time
	// entered current state"), so the engine must stamp it on entry.
	// Non-SNAPSHOTTING transitions should still use UpdateInstanceState.
	UpdateInstanceStateWithTimestamp(ctx context.Context, id, state string, parkedAt time.Time) error
	// IncInstanceRequestCount bumps the per-instance request_count
	// column by delta (ADR-098 C8). The writer is additive
	// ("request_count = request_count + delta") so a Phase-4-loser
	// re-apply is idempotent. Returns the post-increment total, or
	// -1 when the row is gone (Phase-4 loser landed after the
	// instance was evicted). The batched flush path (C9) is the only
	// caller; the writer is deliberately not used at single-request
	// granularity.
	IncInstanceRequestCount(ctx context.Context, id string, delta int64) (int64, error)
	// TouchInstancesWithRequestDelta (ADR-098 C9) is the batched
	// version of IncInstanceRequestCount: same per-instance delta
	// increment, but applied across a whole touch batch in one
	// round-trip via unnest. Mirrors TouchInstancesLastSeen — the
	// gateway's ReportActivity batch carries both last_request_at
	// AND the per-instance request_count delta, and the engine
	// stamps both atomically. Returns the number of rows updated.
	TouchInstancesWithRequestDelta(ctx context.Context, touches []InstanceTouch) (int, error)
	// UpdateInstanceStateToTerminal writes state AND stamps terminal_at
	// on the same UPDATE (PR #74, spec §17 follow-up). terminal_at is
	// the dedicated retention anchor the daily sweep (pkg/sched.Retention)
	// reads; started_at means "row creation" and parked_at is overloaded
	// (also means "entered PARKED"), so neither is correct for a STOPPED
	// row whose vmmd boot succeeded days earlier. Engine.transition
	// routes here when the target state is STOPPED or FAILED; every other
	// transition still uses UpdateInstanceState / UpdateInstanceStateWithTimestamp.
	UpdateInstanceStateToTerminal(ctx context.Context, id, state string, terminalAt time.Time) error
	// SetInstanceMode (issue #72 / ADR-125) flips instances.mode
	// from 'normal' to 'mirror' (or back). Used by the schedd
	// admission path when admitting a mirror instance under a
	// mirror_rule, and by tests that need to plant a mirror
	// instance without going through the full wake-coord loop.
	// Idempotent. ErrNotFound when the instance row is missing.
	SetInstanceMode(ctx context.Context, id string, mode InstanceMode) error
	// SetInstanceFrameworkReadyAt stamps the column added by
	// migrations/00112_instances_framework_ready_at.sql — the wall-clock
	// time the vmmd received the guest-init "framework ready" vsock
	// DGRAM (port 1027, msg=4) for this instance. The engine's
	// captureWarmSnapshot (PR #470-FU-A) waits on this column before
	// issuing the second PauseAndSnapshot that captures the warm tier.
	// Idempotent: callers can re-stamp on subsequent warm-capture cycles
	// (the engine resets to NULL at the start of each cycle and stamps
	// again when the new signal arrives). Errors only on
	// (a) instance missing — ErrNotFound, or (b) database error.
	// Cancellation via ctx is honoured at the next pool.Ping boundary.
	// NOT state-coupled: the column is nullable and writable in any
	// state (engine waits before SNAPSHOTTING begins; vmmd may stamp
	// while state is RUNNING or already PARKED).
	SetInstanceFrameworkReadyAt(ctx context.Context, id string, readyAt time.Time) error
	// ClearInstanceFrameworkReadyAt resets the column to NULL. Used by
	// the engine at the start of each warm-capture cycle so a stale
	// stamp from the previous cycle doesn't leak into the next wake
	// decision. Symmetric counterpart to SetInstanceFrameworkReadyAt.
	// Nullable-on-disk semantics mean the column is always
	// NULL-or-timestamp; there is no separate "delete" notion.
	ClearInstanceFrameworkReadyAt(ctx context.Context, id string) error
	// BumpInstanceTailCount atomically adds delta to the instance's
	// `tail_count` column and returns the post-update value (issue
	// #667 / ADR-078). Used by vmmd's MarkInstanceTailTerminal
	// receipt path when a runner increments / decrements the
	// in-flight tail task counter. delta is signed: positive on
	// waitUntil registration, negative on terminal completion.
	// Replay-safe: the SQL UPDATE is `SET tail_count =
	// tail_count + $2` (atomic at the row level), so concurrent
	// receipts cannot lose increments. Floor at 0 — a stray
	// decrement on a counter at 0 leaves it at 0 rather than
	// underflowing (a stale receipt from a parked instance is the
	// expected source of such drift; the snapshotAndPark 5s
	// watchdog (PR 4) force-parks anyway).
	// Returns ErrNotFound when the instance row is missing.
	BumpInstanceTailCount(ctx context.Context, id string, delta int32) (int32, error)
	// DecrementInstanceTailCount is the canonical "tail task
	// reached terminal" path (issue #667 / ADR-078). Equivalent
	// to BumpInstanceTailCount(ctx, id, -n) but with an extra
	// safety floor at 0 (UPDATE … SET tail_count = GREATEST(
	// tail_count - $2, 0)) so a stale receipt from a guest that
	// just parked cannot underflow the counter. n is the number of
	// tail tasks to decrement by (1 for the steady-state path, the
	// full unfinished-tail count for the snapshotAndPark watchdog).
	// Used by vmmd's MarkInstanceTailTerminal; PR 4 also calls it
	// from the snapshotAndPark watchdog when the 5s drain window
	// elapses with unfinished tails.
	// Returns ErrNotFound when the instance row is missing.
	DecrementInstanceTailCount(ctx context.Context, id string, n int32) error
	// GetInstanceTailCount returns the current value of the
	// instance's `tail_count` column (issue #667 / ADR-078). Used by
	// the snapshotAndPark watchdog to poll for drain completion.
	// Returns ErrNotFound when the instance row is missing.
	// Implementations may issue a single SELECT … FROM instances
	// WHERE id = $1; the column is on the hot path so the row is
	// already in shared_buffers under normal load.
	GetInstanceTailCount(ctx context.Context, id string) (int32, error)
	// ListInstancesByStatesOlderThan is the §6.1 watchdog's lookup.
	// Returns rows currently in any of the given states whose
	// "age timestamp" is strictly older than threshold. The age
	// column is state-aware: started_at for WAKING/COLD_BOOTING
	// (stamped on creation by migration 00015), parked_at for
	// SNAPSHOTTING (stamped on entry into that state by
	// UpdateInstanceStateWithTimestamp). Implementations must NOT
	// coalesce the two columns — pre-migration 00015 rows have
	// NULL started_at, and coalesce would silently use the stale
	// parked_at. PgStore relies on migration 00016's partial index
	// for the state predicate.
	ListInstancesByStatesOlderThan(ctx context.Context, states []State, threshold time.Time) ([]Instance, error)
	// ListRunningInstancesOnDeadNodes returns RUNNING instances whose
	// owning compute_node is no longer alive — either active = false
	// (schedd's heartbeat sweep already flipped it) or
	// last_heartbeat_at older than threshold (the flip has not landed
	// yet, e.g. the schedd that owns the heartbeat loop restarted).
	//
	// Why this exists: MarkComputeNodeInactive only writes
	// compute_nodes; it deliberately leaves instances untouched. A
	// vmmd that dies without transitioning its rows therefore leaves
	// them in RUNNING forever, and meterd bills on
	// State.CountsForRAM() with no node-liveness cross-check — so the
	// customer pays for a VM that no longer exists. This is the input
	// set for Engine.ReconcileDeadNodeInstances.
	//
	// Both liveness predicates are required: checking only active
	// misses the window before the heartbeat sweep runs, and checking
	// only last_heartbeat_at misses a node an operator drained by
	// hand. Rows are returned oldest-heartbeat-first so a capped tick
	// drains the worst offenders first.
	ListRunningInstancesOnDeadNodes(ctx context.Context, threshold time.Time, limit int) ([]Instance, error)
	// FailRunningInstanceOnDeadNode transitions a RUNNING instance to
	// FAILED, stamping terminal_at, when its owning node is gone.
	// FAILED is excluded from State.CountsForRAM(), so this is what
	// stops meterd billing for a VM that no longer exists; it also
	// frees the row from the §6.2-2 RAM ceiling.
	//
	// Implementations MUST make the write conditional on both
	// `state = 'running'` and the supplied nodeID, and MUST return
	// ErrConflict (not an error) when no row matches — that is the
	// benign "a peer got there first / the node recovered" path. The
	// nodeID predicate prevents a stale read from failing an instance
	// that has since migrated to a healthy node.
	FailRunningInstanceOnDeadNode(ctx context.Context, instanceID, nodeID string) error
	// ListInstancesInTerminalStatesOlderThan is the §17 retention sweep's
	// lookup (PR #74). Returns rows currently in any of the given states
	// (today: {STOPPED, FAILED}) whose terminal_at is strictly older than
	// threshold. Order is implementation-defined. Reads the dedicated
	// terminal_at column — distinct from ListInstancesByStatesOlderThan,
	// which uses the state-aware started_at/parked_at comparison and is
	// the wrong tool for retention aging (a STOPPED row that booted
	// successfully has a stale started_at). PgStore relies on migration
	// 00017's partial index for the state predicate.
	ListInstancesInTerminalStatesOlderThan(ctx context.Context, states []State, threshold time.Time) ([]Instance, error)
	// DeleteInstance removes a single instance row unconditionally
	// (PR #74). Returns ErrNotFound when the row is already gone — the
	// retention sweep swallows that case for redelivery. There are NO
	// foreign-key cascades: events.subject and usage_minutes.instance_id
	// carry no FK to instances today (audit log is append-only by spec
	// §6.1; usage is reconciled by account hard-delete). Adding a FK
	// in a future migration would silently break this sweep — review
	// PR-#74's readme when touching either table.
	DeleteInstance(ctx context.Context, id string) error
	// SetInstanceRuntime records the per-instance identity vmmd allocated on
	// wake (netns, routable host IP, jail uid) and stamps started_at=now. schedd
	// calls this between a successful vmmd boot and the RUNNING transition so the
	// gateway can route to host_ip:8080 (spec §7).
	SetInstanceRuntime(ctx context.Context, id, netns, hostIP string, guestUID int) error
	// RunningInstanceForApp returns the newest RUNNING instance for an app, or
	// ErrNotFound when none is live. schedd uses it to make Wake idempotent and
	// the gateway to seed its route target on startup.
	RunningInstanceForApp(ctx context.Context, appID string) (Instance, error)
	// TouchInstancesLastSeen batches last_request_at updates the gateway flushes
	// every 15 s (spec §4.1). schedd is the sole writer to instances, so the
	// gateway hands it the batch (ADR-018). Returns the number of rows updated.
	TouchInstancesLastSeen(ctx context.Context, touches []InstanceTouch) (int, error)

	// Snapshots (imaged is sole writer; schedd reads latest non-stale and marks
	// stale on a failed restore, ADR-005).
	CreateSnapshot(ctx context.Context, snap Snapshot) (Snapshot, error)
	LatestSnapshot(ctx context.Context, deploymentID string) (Snapshot, error)
	// LatestSnapshotForTier (issue #470 / ADR-055) returns the freshest
	// non-stale snapshot for the (deployment, tier) pair. Empty tier is
	// treated as "init". Returns ErrNotFound when no non-stale row
	// exists — schedd's tier-fallback chain treats that as "fall through
	// to the next tier". Warm-tier apps use this to pick warm.snap when
	// usable; cold-boot-only deployments continue to call
	// LatestSnapshot (which now ranks warm above init on ties).
	LatestSnapshotForTier(ctx context.Context, deploymentID, tier string) (Snapshot, error)
	MarkSnapshotStale(ctx context.Context, snapshotID string) error

	// Snapshot GC (imaged nightly + on FC upgrade, spec §4.6 + §4.4).
	//
	// ListSnapshotsForGC returns every non-stale snapshot joined with its
	// deployment + app + account. Soft-deleted apps (status='deleted') are
	// excluded; their snapshots have no in-flight wake target.
	ListSnapshotsForGC(ctx context.Context) ([]SnapshotForGC, error)
	// DeleteSnapshotsByID bulk-removes the named snapshot rows (no cascade).
	// Returns the number of rows deleted; a second call with the same ids
	// returns 0 and no error.
	DeleteSnapshotsByID(ctx context.Context, ids []string) (int64, error)
	// MarkAllSnapshotsStaleByFCVersion flips every non-stale row whose
	// fc_version != currentVersion stale (ADR-005: snapshots are pinned to
	// the Firecracker version that made them). Returns the number of rows
	// affected. Idempotent.
	MarkAllSnapshotsStaleByFCVersion(ctx context.Context, currentVersion string) (int64, error)
	// MarkAllSnapshotsStaleByAppProtocol flips every non-stale snapshot
	// whose deployment's app.app_protocol ∈ appProtocols stale
	// (ADR-127 §D1, Layer 6 — imaged F3 sweep, the app-protocol
	// dimension of the F2/F3 split). app_protocol=http1 snapshots are
	// never affected. Idempotent.
	MarkAllSnapshotsStaleByAppProtocol(ctx context.Context, appProtocols []string) (int64, error)
	// MarkSnapshotStaleByAppProtocol is the single-row mirror of
	// MarkAllSnapshotsStaleByAppProtocol. Returns ErrNotFound when no
	// snapshot matches the id AND the deployment's app.app_protocol
	// ∈ appProtocols. Empty appProtocols is an error (caller bug).
	MarkSnapshotStaleByAppProtocol(ctx context.Context, snapshotID string, appProtocols []string) error
	// MarkOldSnapshotsStale marks the given snapshot IDs stale (per-app
	// "current + previous" enforcement, run before DeleteSnapshotsByID).
	MarkOldSnapshotsStale(ctx context.Context, beforeSnapshotIDs []string) (int64, error)
	// DeleteSnapshotsStaleOlderThan removes rows where stale=true AND
	// created_at < now()-retention. Used by imaged's F2 startup sweep
	// after the mark-stale step: old snapshots stay restorable for a
	// retention window (typically 7 days per api.SnapshotStaleRetention)
	// so a firecracker downgrade or operator rollback doesn't pay an
	// extra cold boot. After the window they go away. Returns the row
	// count. Idempotent.
	DeleteSnapshotsStaleOlderThan(ctx context.Context, retention time.Duration) (int64, error)

	// Compute nodes (issue #97 / ADR-025 axis 3). schedd's Wake flow
	// is the sole reader of these methods (single-leader CP, no
	// consensus); apid writes via CreateComputeNode on
	// POST /v1/compute-nodes. The synthetic 'default-local' row is
	// seeded by migrations/00024_compute_nodes.sql so production
	// callers never have to insert it themselves.
	//
	// ActiveComputeNodes returns every active compute_node for
	// placement (Wake asks "which node has headroom?"; the partial
	// compute_nodes_active_idx keeps inactive rows out of the read
	// path). Order is by name (placement sorts in memory after
	// computing used_mb per node).
	ActiveComputeNodes(ctx context.Context) ([]ComputeNode, error)
	// ComputeNodeByID resolves a row by primary key. Wake calls this
	// after placement to fetch the target URL for the dial step.
	// Returns ErrNotFound when the id has no row.
	ComputeNodeByID(ctx context.Context, id string) (ComputeNode, error)
	// ComputeNodeByName resolves a row by its unique name. The
	// engine's startup path uses this once to cache the
	// default-local UUID (DefaultLocalNodeName → id) so subsequent
	// Wake flows don't repeat the SELECT. Returns ErrNotFound when
	// the name has no row — a config / migration drift that the
	// boot path surfaces as a loud failure (don't paper over it with
	// a default).
	ComputeNodeByName(ctx context.Context, name string) (ComputeNode, error)
	// ComputeNodeUsedMB returns the Σ(ram_mb + PerVMOverheadMB) for
	// live instances on the given node. Single SQL aggregate, no
	// client loop. Live = state IN ('waking','cold_booting',
	// 'running') per spec §6.2-2 re-stated per-node. Atomic with
	// the ledger; the ledger is the cache, this is the source of
	// truth after a schedd restart. PerVMOverheadMB is the 8 MB
	// fixed cost (spec §4.7 / billing model) added per live instance.
	ComputeNodeUsedMB(ctx context.Context, nodeID string) (int64, error)
	// HeartbeatComputeNode stamps last_heartbeat_at to now(). The
	// schedd watchdog goroutine calls this every HeartbeatInterval
	// (default 30s, env-overridable) for each registered node whose
	// dial succeeded. Idempotent — repeated calls just bump the
	// timestamp. A future gate will flip active=false when the
	// timestamp ages past the staleness threshold (2× the heartbeat
	// cadence); that policy lives in the watchdog, not here.
	HeartbeatComputeNode(ctx context.Context, nodeID string) error
	// CreateComputeNode inserts a new compute_node row on
	// POST /v1/compute-nodes (operator-only admin endpoint). The id
	// is gen_random_uuid() (column default). Returns the inserted
	// row with its assigned id and created_at. ErrConflict when
	// the name is already taken (UNIQUE constraint on name).
	CreateComputeNode(ctx context.Context, node ComputeNode) (ComputeNode, error)
	// MarkComputeNodeInactive flips a row's active flag to false
	// (issue #97 / ADR-025 axis 3, PR #114). schedd's heartbeat
	// loop calls this when VMRouter.Ping fails — placement's
	// ActiveComputeNodes filter then excludes the dead node so
	// future wakes don't dial an unreachable target. Idempotent:
	// flipping an already-inactive row is a no-op UPDATE. A
	// future staleness gate (last_heartbeat_at > 2 × interval)
	// will reuse this method; today only the heartbeat path
	// calls it. The row is preserved (no DELETE) so an operator
	// can flip it back via a future admin endpoint without
	// re-provisioning the cert/target_url.
	MarkComputeNodeInactive(ctx context.Context, nodeID string) error
	// UpsertComputeNode inserts or updates a row by name. The
	// vmmd self-registration path calls this on startup
	// (issue #98 / ADR-028): a node rebooting should bring itself
	// back without operator intervention. ON CONFLICT (name) DO
	// UPDATE SET target_url, vpcpus, mem_mb, max_concurrency,
	// admission_ceiling_mb, active=true — re-applies operator
	// config and re-activates a previously drained row in one
	// round-trip. Returns the row (id, timestamps refreshed).
	// ErrConflict is reserved for a future partial-cluster failure;
	// the upsert path doesn't currently fail.
	//
	// Deprecated: prefer UpsertComputeNodeFromOperator (apid POST
	// path) or UpsertComputeNodeFromVmmd (vmmd self-registration
	// path) so the two ownerships — operator-set target_url vs
	// vmmd-owned resource numbers — don't clobber each other on
	// upsert. Kept for backwards compat with the handful of test
	// fixtures that don't care about the ownership split; new
	// callers should pick the explicit variant.
	UpsertComputeNode(ctx context.Context, node ComputeNode) (ComputeNode, error)
	// UpsertComputeNodeFromOperator is the apid POST /v1/compute-nodes
	// write path. The operator owns target_url (the routable FQDN
	// schedd/gatewayd dial); vmmd's self-registration preserves it.
	// ON CONFLICT (name) DO UPDATE SET target_url = excluded.target_url,
	// vpcpus, mem_mb, max_concurrency, admission_ceiling_mb,
	// vcpu_budget, active=true — full set, the operator's POST
	// wins on every field.
	UpsertComputeNodeFromOperator(ctx context.Context, node ComputeNode) (ComputeNode, error)
	// UpsertComputeNodeFromVmmd is the vmmd self-registration
	// write path (cmd/vmmd/register.go). Writes only the
	// vmmd-owned resource numbers and preserves the operator-controlled
	// active bit. ON
	// CONFLICT (name) DO UPDATE SET vpcpus, mem_mb,
	// max_concurrency, admission_ceiling_mb, vcpu_budget,
	// active=compute_nodes.active, target_url = COALESCE(compute_nodes.target_url,
	// excluded.target_url) — the existing target_url (operator's
	// POSTed value, or the seed row's value on cold start) is
	// preserved on conflict. The COALESCE handles the cold-INSERT
	// case where no operator POST has happened yet: vmmd's own
	// view of its dialable address lands in the row, the same
	// shape as the old UpsertComputeNode. This split closes the
	// trap where vmmd's startup UPSERT silently overwrote an
	// operator's carefully-POSTed target_url with the bind
	// address.
	UpsertComputeNodeFromVmmd(ctx context.Context, node ComputeNode) (ComputeNode, error)
	// UpsertNodeKey inserts or updates a (compute_node_id, key_id)
	// row in compute_node_keys (ADR-053 / migration 00076). vmmd's
	// self-registration calls this on startup once it has loaded
	// its node signing key (cmd/vmmd/main.go::loadNodeSigningKey)
	// and computed the key_id (the SHA-256 hex of the
	// SubjectPublicKeyInfo). The PK is (compute_node_id, key_id),
	// so a single key per node is the typical shape; a future
	// rotation adds a new row with the same compute_node_id but a
	// different key_id. ON CONFLICT is a no-op (the existing row
	// is left unchanged) because key material is write-once —
	// re-applying public_key_pem would silently overwrite a
	// rotation that produced a different key.
	UpsertNodeKey(ctx context.Context, nodeID string, keyID string, publicKeyPEM string) error
	// SetComputeNodeActive flips the active flag on a row by id.
	// The schedd heartbeat staleness gate (issue #98) calls this
	// to mark a node active=false when last_heartbeat_at ages past
	// 90s, and again active=true when a heartbeat succeeds for a
	// previously-drained node. Emits compute_node_changed via the
	// pg_notify listener (pkg/db/notify.NotifyComputeNodeChanged) so
	// gatewayd-internal can add or drop its per-node client without a
	// restart. ErrNotFound when the id has no row.
	SetComputeNodeActive(ctx context.Context, id string, active bool) error
	// NodeGet returns a single ComputeNode by id with all lifecycle
	// fields populated. Workstream B (issue #1184) replaces the
	// legacy active-bool reads with this richer projection so the
	// recovery arbiter + drain handler can branch on the enum.
	// ErrNotFound when the id has no row.
	NodeGet(ctx context.Context, id string) (ComputeNode, error)
	// NodeGetByName mirrors NodeGet keyed by the human-stable name.
	// The apid drain handler (POST /v1/compute-nodes/{name}/drain)
	// is the only routine caller.
	NodeGetByName(ctx context.Context, name string) (ComputeNode, error)
	// NodeList returns every compute_node in name order, optionally
	// filtered by lifecycle (empty string = any). The recovery
	// arbiter uses the unfiltered form for cold-start reconciliation.
	NodeList(ctx context.Context, lifecycle NodeLifecycle) ([]ComputeNode, error)
	// NodeSetLifecycle is the CAS lifecycle transition added by
	// 00579. The CAS predicate (lifecycle::text = $expected) blocks
	// the two-writer race that the boolean toggle had — a node in
	// 'active' can't flip to 'draining' AND 'unavailable'
	// concurrently, because only one CAS will land. Returns
	// ErrNotFound when the id is unknown, ErrConflict when the
	// CAS didn't land (the caller is expected to re-read via
	// NodeGet and decide whether to retry).
	NodeSetLifecycle(ctx context.Context, id string, expected, next NodeLifecycle) error
	// NodeListRecoverable returns every node in
	// ('unavailable','recovering') — the recovery arbiter's input
	// set. Cold-start sweep + the 1s tick both consume this.
	NodeListRecoverable(ctx context.Context) ([]ComputeNode, error)
	// NodeListDrainable returns every 'active' node with zero live
	// instances — the set the drain handler is allowed to flip to
	// 'draining' without operator override. A non-empty live set
	// means the handler must surface RFC 7807 `node_draining_refused`
	// instead.
	NodeListDrainable(ctx context.Context) ([]ComputeNode, error)
	// NodeMarkDrainCompleted stamps drain_completed_at + flips
	// lifecycle back to 'active' (CAS on 'draining'). The recovery
	// arbiter calls this once the migrate-or-recreate sweep
	// confirms zero live instances remain.
	NodeMarkDrainCompleted(ctx context.Context, id string, completedAt time.Time) error
	// NodeMarkRecovered stamps last_recovery_outcome='succeeded' +
	// flips lifecycle to 'active' (CAS on 'recovering'). Called
	// after the recovery sweep clears stranded instances.
	NodeMarkRecovered(ctx context.Context, id string) error
	// InstanceListByNodeForRecovery returns the live instances on a
	// specific node — input to the arbiter's per-instance decision
	// matrix. Only states the arbiter can act on are included.
	InstanceListByNodeForRecovery(ctx context.Context, nodeID string) ([]RecoveryInstance, error)
	// DeploymentRecordSnapshotMiss stamps the per-deployment
	// backoff state added by 00585. Called by the wake flow on every
	// snapshot-fetch miss. The retry-after math lives in
	// pkg/sched/snapshot_backoff.go; this is the state write only.
	DeploymentRecordSnapshotMiss(ctx context.Context, deploymentID string, backoffUntil time.Time) error
	// DeploymentClearSnapshotBackoff resets the counter + clears
	// the backoff. Called by the recovery arbiter after a successful
	// sweep restores the destination's snapshot set, OR by the wake
	// flow on a successful cold boot.
	DeploymentClearSnapshotBackoff(ctx context.Context, deploymentID string) error
	// DeploymentSnapshotBackoffActive returns the stored backoff row when
	// snapshot_miss_backoff_until is non-null. The bool reports whether the
	// timestamp is still in effect; expired rows are returned so the wake
	// flow can preserve the miss count when computing the next backoff.
	// The partial index `deployments_snapshot_backoff_idx` covers the lookup.
	DeploymentSnapshotBackoffActive(ctx context.Context, deploymentID string) (Deployment, bool, error)
	// SetComputeNodeRole overwrites the role column on a row by id
	// (ADR-112 PR-B). PR-A's first-boot path populates the column via
	// UpsertComputeNodeFromOperator (keyed by name); PR-B's in-place
	// mutation needs a dedicated setter because the runtime identity
	// is the box's node-id, not the manifest name. The role value is
	// validated against the {empty, control-plane, compute-only}
	// allow-list at the Go boundary (matches pkg/roleTemplating.Validate)
	// so a SQL injection via an operator-supplied role is impossible —
	// the parameter is length-capped at 32 chars and member-tested.
	// Idempotent: writing the same role twice is a no-op UPDATE. Returns
	// ErrNotFound if the row is gone. Emits compute_node_changed via
	// the pg_notify trigger so gatewayd-internal's per-node cache and
	// schedd's chooser re-rank immediately (the chooser currently
	// ignores role, but the family is wired for ADR-110 follow-ons).
	SetComputeNodeRole(ctx context.Context, id string, role string) error
	// ListComputeNodes returns every compute_node in name order.
	// includeInactive=false (default) returns only active rows
	// (placement-equivalent); apid's GET /v1/compute-nodes handler
	// passes true so operators can drain visibility. Backed by the
	// existing compute_nodes_active_idx partial index.
	ListComputeNodes(ctx context.Context, includeInactive bool) ([]ComputeNode, error)
	// DeleteComputeNode hard-deletes a row by id. apid's
	// DELETE /v1/compute-nodes/{name}?hard=1 is the only caller;
	// soft-delete via SetComputeNodeActive(false) is the default
	// for the routine operator workflow. Returns ErrNotFound if
	// the id is unknown.
	DeleteComputeNode(ctx context.Context, id string) error

	// AppendComputeNodeHeartbeat stamps one row in the append-only
	// compute_node_heartbeats table (CP-1, migration 00065). The
	// schedd Heartbeat.Tick goroutine is the only writer on the
	// routine path; the deactivation/reactivation sources are also
	// stamped but on rarer paths. The endpoint read shape is
	// ListComputeNodeHeartbeats (below). received_at and
	// last_heartbeat_at are caller-supplied so the schedd-side
	// wall-clock pair is what the operator's wire shape shows —
	// the column default now() is intentionally NOT used here.
	// Returns ErrConflict when (node_id, received_at) collides
	// (the unique constraint is observed, not folded).
	AppendComputeNodeHeartbeat(ctx context.Context, nodeID string, receivedAt, lastHeartbeatAt time.Time, source string) error
	// ListComputeNodeHeartbeats returns up to limit rows for the
	// given node. **MUST return rows newest-first** (received_at
	// DESC); the heartbeat-history handler in
	// cmd/apid/handlers_compute_nodes_heartbeats.go relies on this
	// ordering to walk oldest-to-newest when emitting the response
	// so row 0 carries the baseline summary. Changing the order
	// here silently breaks the gap classification on the wire.
	//
	// since.IsZero() means "no lower bound, return most-recent N";
	// a non-zero since restricts to rows whose received_at >= since.
	// The composite index compute_node_heartbeats_node_at_idx
	// (node_id, received_at desc) matches this read shape. CP-1's
	// heartbeat-history endpoint uses this with default limit 200
	// and a 24h hard cap on since.
	//
	// An empty result set is NOT an error — a fresh node has no
	// history rows, the endpoint surfaces that as
	// { "heartbeats": [] }. The endpoint resolves the parent
	// compute_node by name first; a "no such node" path is the
	// store's ComputeNodeByName, not this call.
	ListComputeNodeHeartbeats(ctx context.Context, nodeID string, since time.Time, limit int) ([]ComputeNodeHeartbeat, error)

	// AppendComputeNodeHeartbeatWithStats (PR #4 / ADR-091 §3.6
	// amendment) extends AppendComputeNodeHeartbeat with the two new
	// stats columns added in migrations/00199. vmmd is the only caller
	// on the routine path; the existing AppendComputeNodeHeartbeat
	// (without stats) stays so schedd's deactivation/reactivation
	// stampers don't need to know about CPU/disk. cpuPct60s is the
	// 60-second sliding-window CPU utilization as a percentage of
	// vpcpus × 100 (range [0.00, 100.00] for sane values). The
	// memstore mirrors the nullable semantics: existing callers
	// without the stats columns pass 0 / 0 and the column defaults
	// to NULL on the postgres side via the IF NOT EXISTS migration.
	AppendComputeNodeHeartbeatWithStats(ctx context.Context, nodeID string, receivedAt, lastHeartbeatAt time.Time, source string, cpuPct60s float64, diskUsedBytes int64) error

	// LatestHeartbeatStats (PR #4) returns the most-recent heartbeat
	// row per node, with the CPU%/disk fields populated. Used by the
	// obs surface to fold onto the /v1/admin/obs/nodes row projection.
	// Returns one row per node — even nodes with no stats yet (the
	// CPU/disk fields are nil). The LEFT JOIN onto compute_nodes is
	// done in the handler; this query is just the latest-row-per-node.
	LatestHeartbeatStats(ctx context.Context) ([]ComputeNodeHeartbeatStats, error)

	// LatestBuilderHeartbeatStats (operator-side observability
	// mega-PR / Commit 7 — P5) is the builder_tick twin of
	// LatestHeartbeatStats. Filters to source='builder_tick' only;
	// cmd/builderd publishes these rows independently of the build
	// queue so idle builders remain observable.
	LatestBuilderHeartbeatStats(ctx context.Context) ([]ComputeNodeHeartbeatStats, error)

	// QueuedBuildsCount (Commit 7 — P5) returns the number of
	// builds in 'queued' state across the fleet. Per-node labeling
	// is deferred (builds.target_node_id is not yet a column; adding
	// it is a follow-up migration). Today the gauge is a
	// fleet-total — the operator dashboard renders "X builds in
	// the queue" without per-schedd attribution.
	QueuedBuildsCount(ctx context.Context) (int, error)

	// PerNodeLiveStats (PR #4) is the read-side aggregate for the
	// new per-node utilization fields on /v1/admin/obs/nodes. One row
	// per compute_node that has at least one live instance.
	//
	// The aggregate joins on instances.node_id (the existing NOT NULL
	// FK from migration 00024, backfilled to the default-local node on
	// pre-existing rows) — NOT a separate binding table. ADR-092 §2.1
	// was rewritten during PR #4 prep after this discovery; see the
	// §8 amendment in docs/adr/092-per-node-utilization-obs.md.
	//
	// The +8 on ram_mb mirrors §6.2 invariant #2 — Σ(ram_mb + 8) ≤
	// 47,600 MB. The aggregate is per-node; the fleet Σ is computed
	// by the caller if it wants the global number (the existing
	// fleet Σ lives in the schedd engine, not on this wire).
	PerNodeLiveStats(ctx context.Context) ([]PerNodeStats, error)
	// OperatorCapacity returns a bounded fleet-wide capacity snapshot. The
	// production implementation performs the live-instance and app-placement
	// rollups in Postgres; it must not be implemented by loading every instance
	// row into apid.
	OperatorCapacity(ctx context.Context) (OperatorCapacitySnapshot, error)

	// Audit (append-only, spec §6.1).
	//
	// AppendEvent is the pre-PR-#TBD shape retained as a shim that
	// delegates to AppendEventWithTrace(ctx, actor, kind, subject, data,
	// nil). Existing callers — including the extensive test
	// doubles — keep compiling without change.
	AppendEvent(ctx context.Context, actor, kind string, subject *string, data []byte) error
	// AppendEventWithTrace is the operator-obs Trace ID sibling.
	// When traceID is non-nil it must match the regex
	// `^[0-9a-f]{32}$` (the migration CHECK at 00486 enforces this
	// on the `events.trace_id` column for PgStore; MemStore
	// validates defensively at the boundary so test doubles
	// cannot accept an invalid value). When traceID is nil the
	// column is left NULL — the pre-PR rows + cron-fired rows
	// without an inbound trace_id keep that shape.
	AppendEventWithTrace(ctx context.Context, actor, kind string, subject *string, data []byte, traceID *string) error
	ListEvents(ctx context.Context, subject string, limit int) ([]Event, error)
	// ListEventsByWakeID (issue #517 / PR-C, ADR-064) is the
	// wake-timeline read-side query. Filters on the jsonb
	// expression index events_wake_id_idx
	// (migrations/00113_events_wake_id_idx.sql) and orders by at
	// ASC so the customer-facing timeline endpoint surfaces a
	// forward narrative. The since parameter is the RFC 3339
	// lower bound (zero-value passes the floor); limit is
	// bounded to 1000 by the handler.
	ListEventsByWakeID(ctx context.Context, wakeID string, since time.Time, limit int) ([]Event, error)
	// ListEventsBySidecar (issue #463 / ADR-069 / PR-B) is the
	// sidecar-aware read-side query for the customer-facing
	// timeline endpoint. Filters on the jsonb expression
	// data->>'sidecar_name' = $1 and the closed wake.kind IN
	// ('wake.sidecar_init_exit', 'wake.sidecar_restart') so a
	// query never returns non-sidecar rows even if a future
	// event reuses the field name. Orders by at ASC and respects
	// the same since / limit contract as ListEventsByWakeID so
	// the customer-facing timeline endpoint can chain the two
	// queries under the same cursor.
	ListEventsBySidecar(ctx context.Context, sidecarName string, since time.Time, limit int) ([]Event, error)

	// InsertAuditLog (issue #755 / PR-6) writes one row to the
	// FK-free audit_log table (migrations/00163_audit_log.sql).
	// Append-only by spec: there is no UpdateAuditLog / DeleteAuditLog
	// pair, and the table has no UPDATE / DELETE permission in
	// production. The pgstore implementation runs the insert
	// through pgx.Tx when called from inside DeleteAccount (so the
	// audit row rides the same tx as the accounts row delete) and
	// through the pool when called standalone.
	//
	// The memstore mirrors the shape so handler tests can exercise
	// the read path without spinning Postgres.
	InsertAuditLog(ctx context.Context, entry AuditLog) error

	// AppendDeploymentAudit (issue #976 / ADR-122 / SAFE-RELEASES-E.2)
	// inserts one row of the deployment_audit table
	// (migrations/00332_deployment_audit.sql). The table is the
	// per-deployment counterpart of the events stream: events rows
	// are per-emit, indexed for subject lookups; deployment_audit
	// rows are per-deployment, indexed for (deployment_id, at DESC)
	// timeline reads. The two coexist — AppendDeploymentAudit is
	// the structured counterpart of audit.EmitAs for the deploy
	// lifecycle.
	//
	// kind must be in the closed set enforced by
	// deployment_audit_kind_chk; the handler-level type alias
	// DeploymentAuditKind (pkg/state/types.go, defined alongside
	// this method) prevents drift between the Go vocabulary and
	// the SQL CHECK.
	//
	// Returns the id Postgres assigned. MemStore picks a sequential
	// id so the round-trip test pins both backends to the same
	// shape.
	AppendDeploymentAudit(ctx context.Context, entry DeploymentAudit) (int64, error)

	// ListDeploymentAudit (issue #976 / ADR-122 / SAFE-RELEASES-E.2)
	// returns deployment_audit rows for one deployment, ordered
	// (at DESC, id DESC) — same tiebreaker discipline as
	// ListAuditLog. The deployment_audit_deployment_idx
	// ((deployment_id, at DESC), migration 00332) backs the query
	// so the timeline endpoint stays sub-millisecond at one-box
	// scale.
	//
	// limit > 0 caps the page; <= 0 means "no row cap" — caller
	// must bound via the per-deployment retention (90 days per
	// ADR-122 §Consequences) or the customer-facing handler cap
	// (DeploymentAuditPageSizeMax). The deployment_id has no FK
	// to deployments(id), so a deployment row deleted by 90-day
	// GC can still have its audit rows listed — the dashboard
	// shows them under the orphaned-deployment_id sentinel.
	ListDeploymentAudit(ctx context.Context, deploymentID string, limit int) ([]DeploymentAudit, error)

	// RecentInvocations (SAFE-RELEASES-OBS PR-E, issue #976 /
	// ADR-122) is the read seam for the canary simulator's
	// `gregale canary simulate` subcommand. Returns invocation
	// rows for the app whose `created_at >= since`, ordered
	// (created_at DESC, id DESC), capped at `limit`. The
	// invocations_app_created_at_idx partial index
	// (migrations/00060) backs the query so the simulator's
	// 1h-look-back stays sub-millisecond at one-box scale.
	//
	// limit > 0 caps the page; <= 0 means "no row cap" (the
	// simulator passes 10_000 as a safety bound). The simulator
	// uses the count, not the rows themselves — the rows are
	// returned only so the simulator can render
	// `observed_traffic` truthfully.
	RecentInvocations(ctx context.Context, appID string, since time.Time, limit int) ([]Invocation, error)

	// RecentErrorRate (SAFE-RELEASES-OBS PR-E) returns the
	// fraction of failed invocations in the [since, now] window
	// for the given app. Computed as
	// COUNT(state='failed' OR state='dead_letter') /
	// COUNT(*). Returns 0.0 when no invocations match (no error;
	// the simulator's neutral path handles the empty-window case
	// separately via RecentInvocations).
	RecentErrorRate(ctx context.Context, appID string, since time.Time) (float64, error)

	// RecentP95LatencyMs (SAFE-RELEASES-OBS PR-E) returns the
	// 95th-percentile completion latency in milliseconds over the
	// [since, now] window for the given app. Computed via
	// percentile_cont(0.95) within group ORDER BY
	// (completed_at - created_at). Returns 0.0 when no
	// completed invocations match (no error; the simulator
	// surfaces this as a "no latency data" note rather than a
	// zero-projection failure).
	RecentP95LatencyMs(ctx context.Context, appID string, since time.Time) (float64, error)

	// ListDeploymentAuditByAlertRule (SAFE-RELEASES-OBS PR-D) is
	// the reverse-lookup query behind /dashboard/alerts/{id} — every
	// deployment_audit row whose alert_rule_id matches the supplied
	// rule, newest first. Backs the partial index
	// deployment_audit_alert_rule_idx created in migrations/20260905000000002 so
	// the query stays sub-millisecond. limit > 0 caps the page;
	// <= 0 means "no row cap" (caller is responsible for bounding).
	ListDeploymentAuditByAlertRule(ctx context.Context, alertRuleID string, limit int) ([]DeploymentAudit, error)

	// AppendDeploymentStage (ADR-117, migration 00302) appends a
	// closed stage transition to deployments.stage_state and returns
	// the new row. The `from` and `to` parameters are the
	// customer-visible `StageName` vocabulary (NOT `DeploymentStatus`
	// — they collapse the imaging+snapshotting micro-states onto one
	// `image_build` step); see pkg/state/types.go:89 for the closed
	// set and migrations/00302_deployments_stage_state.sql for the
	// schema CHECK that enforces it.
	//
	// Callers MUST pass `from != to`. Failure stamps go through
	// MarkDeploymentStageFailed (below) — they have different
	// semantics (stamp the in-flight stage rather than close it)
	// and a different wire shape on the SSE consumer.
	//
	// The stage_state jsonb is owned entirely by these two methods —
	// callers MUST NOT write the column directly. The atomic JSONB
	// merge is the load-bearing contract: the SSE handler's 2s
	// polling tick (`statusTicker` at
	// cmd/apid/handlers_ext.go:4156-4157) reads `stage_state`
	// verbatim and emits `event: stage` frames per transition, so
	// any drift between the in-Go state machine and the persisted
	// jsonb would surface as a missing or duplicated frame.
	AppendDeploymentStage(ctx context.Context, id string, from, to StageName, at time.Time, reason string) (Deployment, error)

	// MarkDeploymentStageFailed (ADR-117 PR-A fix) stamps the
	// in-flight `state.Current` stage with `status:"failed"` and the
	// caller-supplied `reason` — distinct from a forward transition
	// (which closes the active stage into history and advances).
	//
	// This method exists because the previous "from == to" failure
	// stamp inside AppendDeploymentStage mutated `history[len-1]`
	// (the previously-closed stage), not the in-flight stage — a
	// wire-shape bug the review cluster surfaced. Splitting the
	// failure path into its own method removes the silent-no-op
	// hazard when `history` is empty (a pre-first-transition failure
	// used to drop the stamp without error) and guarantees the
	// SSE consumer emits the failing stage, not the one that just
	// closed.
	//
	// Returns ErrNotFound if the deployment row does not exist or
	// state.Current is the zero value (no stage ever started — the
	// caller has not driven a single transitionWithStage, so there
	// is nothing to fail).
	MarkDeploymentStageFailed(ctx context.Context, id string, at time.Time, reason string) (Deployment, error)

	// CloseDeploymentStage (ADR-117 PR-A fix) moves the in-flight
	// `state.Current` stage into history with `status:"completed"`
	// and clears Current. The customer-facing wire shape requires
	// every stage that ever ran to appear in history so the SSE
	// consumer's 2s tick walks `history` in order. The terminal
	// stage of a successful deploy is the readiness stage; imaged
	// calls CloseDeploymentStage after MarkDeploymentLive so the
	// readiness row carries a duration_ms on the wire.
	//
	// Returns ErrNotFound when the deployment row does not exist or
	// when state.Current is the zero value.
	CloseDeploymentStage(ctx context.Context, id string, name StageName, at time.Time) (Deployment, error)

	// RetryDeploymentFromStage (ADR-117 §Production-ready follow-on,
	// C2) inserts a fresh `deployments` row copying every input
	// primitive from `failedID` (image / source_url / commit_sha /
	// overrides / sidecars / scope / traffic_percent) and seeds the
	// new row's `stage_state` to `{current: fromStage,
	// current_started_at: NULL, history: []}`. The original row is
	// NOT mutated; the new row carries a new ID so SSE consumers
	// (the dashboard stages-partial handler in commit 4) can detect
	// the retry via the row-creation event.
	//
	// `fromStage` MUST be one of AllStageNames — implementations
	// validate against the closed-6 vocabulary before insert (a
	// caller-supplied unknown stage returns ErrInvalidArgument).
	// The caller (apid handlers_retry.go) maps a wire-level 400 on
	// unknown stage to a structured RFC 7807 problem.
	//
	// The new row's status starts at DeployPending so imaged's
	// transition chokepoint picks it up the same way as a CLI-driven
	// `gregale deploy`. The `failedID` row's status remains at its
	// terminal value (DeployFailed / DeployLive) — the retry is a
	// new row, not a status flip on the old one.
	//
	// Implementation note: reuses the existing CreateDeployment
	// supersede step at pgstore.go:4187-4244 (which handles the
	// pending-vs-parked row dance on the same app). The retry path
	// doesn't supersede anything — the new row just inserts.
	RetryDeploymentFromStage(ctx context.Context, failedID string, fromStage StageName) (Deployment, error)

	// ListAuditLog (issue #755 / PR-6) is the dashboard read path
	// for the audit_log table. The filter struct drives every
	// WHERE clause; no string-built queries (matches the repo
	// convention enforced by the sqlc-check CI gate). Order is
	// (received_at DESC, id DESC) so the result is stable across
	// ties and honors the audit_log_received_at_idx.
	//
	// The customer-scoped handler pins AccountID to the calling
	// account's ID; the operator endpoint leaves AccountID nil and
	// passes IncludeAnonymous=true when ?include_anonymous=true.
	ListAuditLog(ctx context.Context, filter AuditLogFilter) ([]AuditLog, error)

	// ListAllEventsPaged (ADR-091 §3.7 / PR #3) is the operator
	// observability backend's read-side query for the live events
	// table. Distinct from ListAuditLog (audit_log table) — the
	// two surfaces do NOT overlap per ADR-091 §3.7.4.
	//
	// Filter shape: actor ("" / exact match), kind_prefix ("" /
	// LIKE 'prefix%'), subject ("" / UUID), since (zero time / since
	// floor), limit (top-N; bounded by handler to
	// ObsAdminEventsLimitMax). The "" / zero literals are the
	// "no filter" sentinel — sqlc binds them as interface{} because
	// the SQL uses the ($1 = '' OR ...) predicate shape.
	//
	// Order is (at DESC, id DESC) — the id tiebreaker keeps the
	// over-read stable across (kind, at DESC) index hits and
	// avoids an unstable sort.
	ListAllEventsPaged(ctx context.Context, actor, kindPrefix, subject string, since time.Time, limit int) ([]Event, error)

	// ListRecentEventsForAccount (ADR-091 §3.7 / PR #3) is the
	// per-account events drill-down. Backed by the events_actor_account_idx
	// partial index on (actor_account_id) WHERE actor_account_id IS NOT NULL
	// (migrations/00099_orgs_memberships_invitations.sql).
	//
	// since is the inclusive lower bound on at; limit is the top-N
	// bounded by the caller's ObsAdminEventsLimitMax. Order is
	// (at DESC, id DESC) — same rationale as ListAllEventsPaged.
	ListRecentEventsForAccount(ctx context.Context, actorAccountID string, since time.Time, limit int) ([]Event, error)
	// DeploymentSidecarRAMs (issue #463 / ADR-070 / PR-C) returns
	// the per-deployment sidecar RAM slice from the jsonb column.
	// Empty/nil when the deployment has no sidecars — matching the
	// no-sidecar admission shape; BillableRAMMBWithSidecars collapses
	// to the legacy single-arg helper in that case. Implementation
	// lives at pkg/state/deployment_sidecar_rams.go on both
	// PgStore and MemStore so schedd's Request builder and the
	// meterd sampler share one read path.
	DeploymentSidecarRAMs(ctx context.Context, deploymentID string) ([]int, error)

	// Usage (apid reads for GET /v1/usage; meterd writes in production).
	// AppendUsage is idempotent on (instance_id, minute): the first
	// write of mb_seconds / requests wins, a redelivered minute is
	// a no-op for those columns. cpu_usec, tx_bytes, net_tx_bytes,
	// net_rx_bytes, cold_boot_count, and tail_seconds are ADDITIVE on
	// (instance_id, minute): the schedd / meterd accumulators can
	// call AppendUsage many times within the same minute; the
	// columns are the sum of all per-tick deltas. The additive
	// merge is documented at migrations/00055_usage_minutes_cpu.sql
	// (cpu_usec), migrations/00065_usage_minutes_egress.sql
	// (tx_bytes, net_tx_bytes, ADR-046),
	// migrations/00067_extend_metering_telemetry.sql (net_rx_bytes,
	// cold_boot_count, ADR-048), and
	// migrations/00151_wait_until_tail.sql (tail_seconds, issue #667 /
	// ADR-078).
	//
	// tail_seconds is the per-minute wall-clock seconds the instance
	// spent draining waitUntil tasks. It is INFORMATIONAL ONLY —
	// tail_seconds does NOT enter Math.GBHours, Provider.PushUsageRecord,
	// or any Stripe/Paddle payload. The permanent guard test
	// pkg/meter/pusher_shadow_test.go::TestPushHour_ExcludesTailSeconds
	// pins this invariant; a follow-up ADR would have to remove it.
	//
	// builder_seconds / builder_kind are NOT accepted here — they
	// are written via AppendBuilderUsage (keyed by build_id) because
	// the per-build billing grain differs from the per-instance
	// usage_minutes grain and mixing them would lose the
	// build-id idempotency that webhook redelivery requires.
	AppendUsage(ctx context.Context, accountID, appID, instanceID string, minute time.Time, mbSeconds, requests, cpuUsec, txBytes, netTxBytes, netRxBytes int64, coldBootCount int32, tailSeconds int64) error
	// AppendBuilderUsage writes one builder-time usage row per
	// terminal build (succeeded or failed — the box burned cycles
	// either way; informational only per ADR-048 §4). Idempotent
	// on (build_id): the first write wins on builder_seconds /
	// builder_kind, a redelivered webhook is a no-op. The
	// per-build grain lives in a separate `builder_usage` table
	// (PK build_id) and is rolled up into usage_daily via the
	// meterd rollup cron.
	AppendBuilderUsage(ctx context.Context, accountID, appID, buildID string, finishedAt time.Time, kind string, seconds int64) error
	UsageByMonth(ctx context.Context, accountID string, month time.Time) ([]Usage, error)
	// UsageDaily returns the per-(account, app, day) rollup rows
	// (migrations/00067_extend_metering_telemetry.sql::usage_daily).
	// day is a UTC midnight time; the returned rows cover the
	// single day. Empty when no rollup has fired yet. ADR-048 §5.
	UsageDaily(ctx context.Context, accountID string, day time.Time) ([]DailyUsage, error)
	// TrafficAnomalyAggregate is the per-(account, app, minute) anomaly
	// scan that powers GET /v1/admin/obs/anomalies (ADR-091 §3.6 /
	// PR #2). The handler passes (since, baselineCutoff, limit);
	// see the sqlc query in pkg/state/queries.sql for the scoring
	// formula and reason taxonomy.
	TrafficAnomalyAggregate(ctx context.Context, arg sqlc.TrafficAnomalyAggregateParams) ([]sqlc.TrafficAnomalyAggregateRow, error)
	// TrafficAnomalyAggregateByNode (PR #4 / ADR-092 §3.4
	// amendment) is the per-(account, app, node, minute) variant
	// of TrafficAnomalyAggregate. Same scoring formula and
	// reason taxonomy; different GROUP BY keys (joins through
	// instances.node_id → compute_nodes.id). Powers
	// GET /v1/admin/obs/anomalies?group_by=node. The handler
	// resolves node_id → node_name via ListComputeNodes before
	// returning the wire shape.
	TrafficAnomalyAggregateByNode(ctx context.Context, arg sqlc.TrafficAnomalyAggregateByNodeParams) ([]sqlc.TrafficAnomalyAggregateByNodeRow, error)
	// PerAccountRateLimitAggregate is the durable view of
	// auth.rate_limited events grouped by account_id (subject)
	// over a rolling window. Powers GET /v1/admin/obs/rate-limits
	// (ADR-091 §3.5 / PR #2). Anonymous events (subject IS NULL)
	// collapse under the all-zeros UUID so the operator UI can
	// render the credential-stuffing signal distinctly.
	PerAccountRateLimitAggregate(ctx context.Context, arg sqlc.PerAccountRateLimitAggregateParams) ([]sqlc.PerAccountRateLimitAggregateRow, error)
	// UsageSLOForApp returns instance_hours and gb_hours
	// summed across usage_minutes for the given app over the
	// half-open UTC range [start, end). Powers the
	// GET /v1/apps/{slug}/slo endpoint (issue #696 / ADR-082).
	//
	// Cross-account isolation is enforced at the SQL level by
	// pinning BOTH `m.app_id = $1` AND `a.account_id = $2`. The
	// caller (cmd/apid/handlers_slo.go) reads `acct.ID` from the
	// session token and passes it here — so any future caller
	// that bypasses loadApp (an operator-side SLO aggregate, an
	// internal cron, a refactor that passes a fetched app_id from
	// a different account's context) cannot leak another
	// account's instance_hours / gb_hours. The JOIN to apps
	// rejects rows whose app_id does not resolve, which is
	// impossible by schema (NOT NULL FK on usage_minutes.app_id
	// to apps.id).
	//
	// Returns (0, 0, nil) when no rows fall in the window.
	UsageSLOForApp(ctx context.Context, appID, accountID string, start, end time.Time) (instanceHours, gbHours float64, err error)
	// UsageSLOForAccount is the account-wide rollup of
	// UsageSLOForApp. Powers GET /v1/account/slo. The pgstore
	// restrains by account_id at the SQL level (no handler-side
	// cross-account leak).
	UsageSLOForAccount(ctx context.Context, accountID string, start, end time.Time) (instanceHours, gbHours float64, err error)
	// AppendSnapshotStorage writes a snapshot_storage_daily row
	// for the given (account, app, day). Idempotent on PK
	// (account_id, app_id, day): a redelivered tick or a meterd
	// restart overwrites the existing row with the cumulative
	// total for that day (not additive merge — the storage rollup
	// is a point-in-time snapshot, not an accumulator). ADR-049 §B.3.
	AppendSnapshotStorage(ctx context.Context, accountID, appID string, day time.Time, snapshotBytes, layerBytes int64) error
	// LatestSnapshotBytes returns mem_bytes + disk_bytes for the
	// app's latest non-stale snapshot (the latest row in
	// public.snapshots joined to the app's active deployment,
	// filtered by stale=false). Returns (0, 0, nil) when the app
	// has no snapshot yet — a cold start, not an error. ADR-049
	// §B.3.
	LatestSnapshotBytes(ctx context.Context, appID string) (memBytes, diskBytes int64, err error)
	// StorageUsage returns the per-(account, app, day) storage
	// rollup rows (migrations/00070_snapshot_storage_daily.sql
	// ::snapshot_storage_daily). day is a UTC midnight time;
	// empty when the storage rollup has not fired for that day
	// yet. ADR-049 §B.3.
	StorageUsage(ctx context.Context, accountID string, day time.Time) ([]StorageUsage, error)
	// ListInvoicesForAccount returns the account's invoices, newest
	// first, ordered by (period_end DESC, id DESC) for deterministic
	// pagination. month is optional: when non-nil, the result is
	// filtered to the half-open UTC month [month, month+1mo). before
	// is the cursor — rows with period_end strictly less than
	// before are returned. limit is 1..100; clamp is the caller's
	// responsibility (handler clamps at 25 default). The returned
	// slice is empty (not nil) when the account has no rows.
	ListInvoicesForAccount(ctx context.Context, accountID string, month *time.Time, before time.Time, limit int) ([]Invoice, error)
	// GetInvoiceByID resolves a single invoice by primary key.
	// Returns ErrNotFound when no row matches. The consumption
	// reducer (issue #279 PR-C) and the future GET /v1/invoices/{id}
	// single-invoice endpoint both depend on this primitive; the
	// list method alone cannot fetch an unknown invoice without
	// knowing its account_id up-front.
	GetInvoiceByID(ctx context.Context, id string) (Invoice, error)
	// UpsertInvoice persists one provider invoice projection. The natural key
	// (account_id, provider, provider_invoice_id) makes webhook redelivery and
	// order status updates idempotent.
	UpsertInvoice(ctx context.Context, inv Invoice) error

	// Account credits (issue #279). The handler is the only writer to
	// account_credits + credit_ledger; meterd reads overage_cap_cents
	// on every quota tick. CreateAccountCredit inserts a new positive
	// balance and returns the row with the DB-assigned id and
	// created_at. ListAccountCredits reads the account's balance
	// rows; onlyActive filters to (cents_remaining > 0) and (expires_at
	// is null OR expires_at > now()) — the active set the consumption
	// reducer will use once it lands. CreateCreditLedgerEntry is the
	// append-only audit row — paired one-to-one with the issuance
	// (and later, with consumption). GetAccountOverageCapCents
	// returns (cents, true) when the column is set, (0, false, nil)
	// when NULL; the handler treats (cents=0, ok=true) as "no
	// overage allowed" and (ok=false) as "no cap".
	CreateAccountCredit(ctx context.Context, c AccountCredit) (AccountCredit, error)
	ListAccountCredits(ctx context.Context, accountID string, onlyActive bool) ([]AccountCredit, error)
	CreateCreditLedgerEntry(ctx context.Context, e CreditLedgerEntry) error
	GetAccountOverageCapCents(ctx context.Context, accountID string) (int64, bool, error)
	// UpdateAccountOverageCapCents sets accounts.overage_cap_cents to
	// the given value. Pass nil to clear the cap (NULL) — issue #561's
	// raiseOverageCap endpoint uses this to round-trip a customer
	// intent of "no cap." The migration CHECK constraint already
	// enforces >= 0; this layer does not re-validate. A cap value of
	// 0 is a valid write and means "no overage allowed" — the
	// distinguishability from "no cap" (nil) lives in the (cents,
	// ok=false) vs (cents=0, ok=true) return shape of the reader.
	UpdateAccountOverageCapCents(ctx context.Context, accountID string, cents *int64) error
	// LoadAllOverageCapCents returns every (account_id, cap) tuple in
	// one round-trip. meterd's quota tick walks all accounts every
	// minute and would otherwise issue N single-row reads; the bulk
	// read keeps the per-tick cost at one round-trip. Drops accounts
	// whose overage_cap_cents is NULL — the caller treats them as "no
	// cap". Returned map is keyed by account_id; cents is the integer
	// monthly ceiling (≥ 0).
	LoadAllOverageCapCents(ctx context.Context) (map[string]int64, error)
	// ListActiveCreditsForConsumption returns the account's active credit
	// rows ordered FIFO (created_at ASC) for the consumption reducer
	// (issue #279 PR-C). "Active" = cents_remaining > 0 AND
	// (expires_at IS NULL OR expires_at > now()). Distinct from
	// ListAccountCredits(onlyActive=true), which returns DESC — the
	// issuance surface's UI needs newest-first; the reducer needs
	// oldest-first so a goodwill credit with a near expiry drains
	// before a perpetual one.
	ListActiveCreditsForConsumption(ctx context.Context, accountID string) ([]AccountCredit, error)
	// ConsumeAccountCredit performs an atomic FIFO decrement across the
	// account's active credits, capped at TargetCents. The unique
	// (provider_invoice_id, credit_id) partial index on credit_ledger
	// (migration 00058) makes the call idempotent: re-running with
	// the same ProviderInvoiceID and credit set is a no-op and
	// returns AlreadyConsumedForInvoice=true.
	//
	// "Atomic" here means: for each credit, the conditional UPDATE
	// (WHERE cents_remaining >= $amt) cannot return a row that would
	// have driven cents_remaining negative — the existing CHECK
	// (cents_remaining >= 0) is the floor. The reducer loops credit
	// by credit inside ONE transaction so a concurrent operator
	// issuance on a different credit cannot interleave between read
	// and update. ProviderInvoiceID must be non-empty — the partial
	// unique index only kicks in when the column is NOT NULL on the
	// consumption row, and an empty key would dedupe every credit
	// against every other.
	ConsumeAccountCredit(ctx context.Context, p ConsumeAccountCreditParams) (ConsumeAccountCreditResult, error)
	// CurrentMonthOverageCents returns the account's derived overage
	// in integer cents for the current UTC month. The account plan's
	// included calendar-month allowance is subtracted before the
	// €0.01/GB-h conversion (CLAUDE.md: integer cents only, never float).
	// meterd consults this on every quota tick to decide whether the
	// overage row should be capped. The PgStore implementation sums
	// usage_minutes.mb_seconds since the UTC month start and converts
	// to cents; the MemStore mirrors the formula in Go.
	CurrentMonthOverageCents(ctx context.Context, accountID string) (int64, error)
	// UsageByHour returns the per-app usage rows whose minute ∈ [start,
	// end). The Stripe pusher calls this hourly to compute the billable
	// GB-RAM-hours for the past hour (spec §4.7, ADR-010). MemStore scans
	// in memory; PgStore runs a SELECT … WHERE minute >= $2 AND minute < $3.
	UsageByHour(ctx context.Context, accountID string, start, end time.Time) ([]Usage, error)
	// UsageWindows returns positive account-level usage aggregates for the
	// completed UTC-hour windows in [start, end). The query is the durable
	// backfill source for meterd: a restart or provider outage can safely
	// replay these rows because every provider records its own idempotency key.
	UsageWindows(ctx context.Context, start, end time.Time) ([]UsageWindow, error)

	// StripePushDedup is the dedupe table for hourly usage pushes. The
	// PushDedupe interface in pkg/billing/stripe is satisfied by both stores.
	HasStripePushHour(ctx context.Context, accountID string, hour time.Time) (bool, error)
	RecordStripePushHour(ctx context.Context, accountID string, hour time.Time) error

	// PaddleOverageDedup is the dedupe table for monthly overage pushes.
	// The PaddleOverageDedupe interface in pkg/billing/paddle is satisfied
	// by both stores. Mirrors StripePushDedup one block above; the PK
	// shape is (account_id, month) instead of (account_id, hour) because
	// the Paddle overage push fires at month-rollover rather than hourly
	// (paddle-go-sdk/v5 has no metered-subscription equivalent to Stripe).
	//
	// Deprecated: the month-scoped pair below is superseded by the
	// per-window ClaimPaddleOverageWindow + CompletePaddleOverageWindow
	// pair. The PK mismatch between PR #204's meterd loop (window-scoped
	// UsageByHour reads) and the month-scoped dedupe row underbilled
	// every account after its first positive window of the month. New
	// callers must use the window-scoped pair. Kept on the interface for
	// back-compat with PR #179 callers; will be removed once no
	// production code paths call them.
	HasPaddleOverageMonth(ctx context.Context, accountID string, month time.Time) (bool, error)
	RecordPaddleOverageMonth(ctx context.Context, accountID string, month time.Time) error

	// PaddleOverageDedupeSchema describes the column shape of the
	// paddle_overage_dedupe table. Read-only probe consumed by the
	// `faas billing reconcile-paddle-overage` pre-flight (B4 / Tier 1
	// follow-up to PR #802). Operators on a fresh install see this
	// return TableExists=true but all the per-window bools=false
	// until migration 00041 is applied; that mismatch is the
	// tripwire. PendingRows + CompletedRows are useful as a dashboard
	//-shaped read so the same call replaces a manual
	// `select count(*) … state=…` query. PgStore probes
	// information_schema.columns; MemStore derives the bools from any
	// rows it holds and reports zeros for the missing-table case
	// (the pre-flight maps zeros to "table missing" the same way).
	PaddleOverageDedupeSchema(ctx context.Context) (PaddleOverageDedupeSchemaResult, error)

	// WebhookReplayDedup is the dedupe gate for the external webhook
	// ingresses on the box (GitHub via gatewayd-internal, billing providers via
	// apid). One table covers all providers; the (provider,
	// delivery_id) primary key makes a (re-POSTed) webhook within the
	// TTL window a 200-on-replay no-op. cutoff is the lower bound on
	// received_at: rows older than the TTL are ignored so a fresh
	// delivery_id is always accepted. The CheckWebhookReplay caller
	// MUST treat a non-nil error as ErrReplay via errors.Is — PgStore
	// returns that sentinel directly when a row is found.
	//
	// RecordWebhookDelivery inserts (or refreshes, via ON CONFLICT
	// DO UPDATE) the dedupe row. expiresAt is stamped on the row so
	// the apid sweep goroutine (cmd/apid/server.go) can cheaply
	// delete rows older than the TTL.
	//
	// SweepExpiredWebhookDeliveries is the apid sweep's bulk delete:
	// delete from webhook_deliveries where expires_at < now(). Returns
	// the rows deleted (informational; sweep callers don't gate on
	// it). Mirrors the meterd dunning sweep at pkg/meter/dunning.go:223.
	//
	// Issue #294. The WebhookReplayDedup interface in pkg/webhookdedupe
	// is satisfied by both stores via a thin adapter that picks TTL
	// (=5min, the webhookdedupe.TTL constant) and computes cutoff +
	// expiresAt on each call.
	// ClaimWebhookDelivery is the atomic variant for ingress handlers:
	// it returns true only for a new or expired delivery and false for
	// a delivery already being processed inside the replay window.
	ClaimWebhookDelivery(ctx context.Context, provider, deliveryID string, cutoff, expiresAt time.Time) (claimed bool, err error)
	CheckWebhookReplay(ctx context.Context, provider, deliveryID string, cutoff time.Time) (bool, error)
	RecordWebhookDelivery(ctx context.Context, provider, deliveryID string, expiresAt time.Time) error
	SweepExpiredWebhookDeliveries(ctx context.Context, now time.Time) (int64, error)

	// ClaimPaddleOverageWindow atomically claims the (acct, window)
	// pair for the calling pod. Returns claimed=true only if this
	// caller now owns the window and must proceed to POST; another
	// pod holds the row otherwise (and the caller must skip the
	// POST). windowStart is hour.UTC().Truncate(Hour). claimedBy is
	// a free-form ops-debugging string (pod hostname or ULID); not
	// part of the unique constraint. lease is the freshness window
	// for reaping stale-pending rows (5 min in production); a
	// pending row whose claimed_at is older than lease is fair game
	// for re-claim. Mirrors the ClaimInvocation pattern at
	// pgstore.go:1297.
	ClaimPaddleOverageWindow(ctx context.Context, accountID string, windowStart time.Time, claimedBy string, lease time.Duration) (claimed bool, err error)
	// CompletePaddleOverageWindow transitions the row from pending
	// to completed after a successful SDK POST. Only the pod that
	// holds the claim (state='pending' with matching claimed_by) is
	// allowed to complete; a foreign caller sees 0 rows updated
	// and gets ErrClaimLost so the meterd can decide whether to
	// alert or silently drop. mb_seconds is stamped on the row
	// (column pushed_mb_seconds, added in migration 00280) so ops
	// can read the wire value directly; the Paddle merchant
	// dashboard's line item Quantity + CustomData["mb_seconds"]
	// carry the same value at the merchant side.
	CompletePaddleOverageWindow(ctx context.Context, accountID string, windowStart time.Time, mbSeconds int64) error
	// ReapStalePaddleOverageClaims resets pending rows whose
	// claimed_at is older than olderThan so the next push tick can
	// re-claim them. Returns the number of rows reset (informational;
	// not a load-bearing return value for the caller). Called from
	// meterd boot before the first Loop.Tick. Idempotent: re-running
	// it inside the lease window is a no-op.
	ReapStalePaddleOverageClaims(ctx context.Context, olderThan time.Duration) (int, error)

	// Idempotency (spec §4.2: Idempotency-Key stored 24 h).
	GetIdempotent(ctx context.Context, accountID, key string) (status int, body []byte, err error)
	PutIdempotent(ctx context.Context, accountID, key string, status int, body []byte) error

	// Customer secrets (spec §11/G2). apid is the only writer; schedd reads
	// ciphertext rows at wake time to hand to vmmd. Ciphertext is age-sealed
	// (pkg/secretbox); the plaintext VALUE is never stored.
	//
	// UpsertAppSecret writes-or-replaces the (app_id, key) row. accountID is
	// passed for ownership verification (the handler must own the app before
	// it can set a secret on it); the row also stores account_id for audit
	// and for the account-scoped delete path. ADR-089 PR-A: the kid column
	// is stamped via UpsertAppSecretWithKid; UpsertAppSecret is preserved
	// for backward compatibility with existing call sites that don't track
	// the kid (e.g. webhook secrets in pkg/webhook).
	UpsertAppSecret(ctx context.Context, accountID, appID, key string, ciphertext []byte) error
	// UpsertAppSecretWithKid is the kid-stamping sibling of UpsertAppSecret
	// (ADR-089 PR-A / migration 00166). The kid column records which host
	// identity sealed the row, so operators can answer "what key sealed
	// this row?" without parsing the ciphertext blob. New callers (the
	// rotate handler in PR-B, the rekey package in PR-A) should use this
	// variant; old call sites keep UpsertAppSecret which writes kid = "".
	UpsertAppSecretWithKid(ctx context.Context, accountID, appID, key, kid string, ciphertext []byte) error
	// GetAppSecret returns the (account_id, app_id, key) row including
	// ciphertext + kid + timestamps. Returns ErrNotFound if the row does
	// not exist. Used by the per-secret rotate handler (PR-B) to
	// distinguish first-time set (emits secret.set audit kind) from
	// rotation (emits secret.rotated).
	GetAppSecret(ctx context.Context, accountID, appID, key string) (*AppSecret, error)
	// DeleteAppSecret removes the (app_id, key) row. Returns ErrNotFound if
	// the row doesn't exist — handlers render 400 CodeSecretNotFound (not a
	// 404) because the URL resource IS the secret name, by design.
	DeleteAppSecret(ctx context.Context, accountID, appID, key string) error
	// ListAppSecrets returns every secret on the app (key + ciphertext). The
	// handler renders KEYS only; ciphertext flows to vmmd. Returns nil slice
	// (not error) when the app has no secrets.
	ListAppSecrets(ctx context.Context, accountID, appID string) ([]AppSecret, error)
	// UpsertAppSecretInScope is the scope-aware sibling of
	// UpsertAppSecret (ADR-092 PR-A). Writes-or-replaces the
	// (app_id, scope, key) row. Mirrors the env widening at
	// store.go:2891+ for InScope variant. accountID is passed
	// for ownership verification; the row also stores it for
	// account-scoped delete paths.
	UpsertAppSecretInScope(ctx context.Context, accountID, appID, scope, key string, ciphertext []byte) error
	// UpsertAppSecretWithKidInScope is the scope-aware
	// sibling of UpsertAppSecretWithKid (ADR-092 PR-A). Stamps
	// kid alongside (scope, ciphertext). Used by the rotate
	// handler (PR-B) and the rekey walk.
	UpsertAppSecretWithKidInScope(ctx context.Context, accountID, appID, scope, key, kid string, ciphertext []byte) error
	// UpsertAppSecretWithKidAndValueHashInScope is the
	// value-hash scope-aware sibling (ADR-117 env-diff matrix,
	// PR-C). Mirrors UpsertAppSecretWithKidInScope but stamps
	// value_hash from secretbox.ValueFingerprint(plaintext,
	// hostHMACKey()) alongside (scope, ciphertext, kid). Used by
	// the PUT + rotate paths and the rekey re-seal pass; the
	// legacy UpsertAppSecretWithKidInScope stays for callers
	// that don't carry the field.
	UpsertAppSecretWithKidAndValueHashInScope(ctx context.Context, accountID, appID, scope, key, kid, valueHash string, ciphertext []byte) error
	// GetAppSecretInScope is the scope-aware sibling of
	// GetAppSecret (ADR-092 PR-A). Returns ErrNotFound when no
	// row exists at (account_id, app_id, scope, key).
	GetAppSecretInScope(ctx context.Context, accountID, appID, scope, key string) (*AppSecret, error)
	// DeleteAppSecretInScope is the scope-aware sibling of
	// DeleteAppSecret (ADR-092 PR-A). Returns ErrNotFound if
	// the row doesn't exist — handlers render 400
	// CodeSecretNotFound (not a 404) because the URL resource
	// IS the secret name, by design.
	DeleteAppSecretInScope(ctx context.Context, accountID, appID, scope, key string) error
	// ListAppSecretsInScope is the scope-aware sibling of
	// ListAppSecrets (ADR-092 PR-A). Used by schedd's
	// pkg/sched/engine.go::loadSealedEnvFor wake-time reader
	// to enumerate the scope's sealed rows.
	ListAppSecretsInScope(ctx context.Context, accountID, appID, scope string) ([]AppSecret, error)
	// ListAllAppSecrets is the cross-scope mirror of
	// ListAppSecrets (ADR-092 PR-A). Used by apid's GET
	// ?scope=__all__ arm (PR-B) to render the nested
	// secrets_by_scope response shape. Order is
	// (scope ASC, key ASC).
	ListAllAppSecrets(ctx context.Context, accountID, appID string) ([]AppSecret, error)
	// ListAppSecretsForAccount is the account-scoped sibling of
	// ListAppSecrets (issue #393). The cursor is the (app_slug, key)
	// pair — encoded as "<slug>|<key>" by the handler and split via
	// split_part in SQL. Order is (app_slug ASC, key ASC) so a
	// paginated walk is deterministic across calls. Used by the
	// dashboard's account-wide secrets page so one call replaces N
	// per-app fan-outs.
	ListAppSecretsForAccount(ctx context.Context, accountID string, limit int, before string) ([]AccountAppSecret, error)
	// ListAppSecretsForRekey is the global paginated walk consumed by
	// pkg/rekey.Replayer.Run (ADR-089 PR-A). Order is
	// (account_id ASC, app_id ASC, key ASC) so a cursor based on the
	// last visited tuple yields a deterministic continuation across
	// daemon restarts. The cursor is the encoded "<account_id>|<app_id>|<key>"
	// from RekeyProgress.LastID; an empty cursor starts from the
	// beginning. limit is the page size (matches RekeyConfig.BatchSize;
	// default 50).
	ListAppSecretsForRekey(ctx context.Context, limit int, cursor string) ([]AppSecret, error)
	// CountAppSecrets is the quota check helper. apid calls it before
	// UpsertAppSecret to enforce Limits.SecretCountMax.
	CountAppSecrets(ctx context.Context, accountID, appID string) (int, error)

	// Per-app private-registry Basic Auth (issue #461 / ADR-062). apid
	// is the only writer; imaged is the only reader. PasswordEncrypted
	// is the age-sealed bytea produced by pkg/secretbox.SealBytes with
	// namespace="registry_creds" — the plaintext password is never
	// stored, never logged, never returned over the wire.
	//
	// Registry is the normalized host the customer supplied (lowercase,
	// no scheme, no path, no trailing slash, port preserved — see
	// apid's normalizeRegistryHost helper). imaged normalizes the
	// incoming ref.Host() the same way before the SQL query.
	//
	// UpsertAppRegistryCredential writes-or-replaces the
	// (app_id, registry) row. accountID is passed for ownership
	// verification (the handler must own the app before it can set a
	// credential on it); the row also stores account_id for audit and
	// the G6 GDPR cascade.
	UpsertAppRegistryCredential(ctx context.Context, accountID, appID, registry, username string, passwordEncrypted []byte) error
	// GetAppRegistryCredential returns the row for (app_id, registry).
	// Returns ErrNotFound if the row doesn't exist or the
	// (accountID, appID) ownership doesn't match — defense in depth so
	// a stale ID→slug mapping can't cross accounts.
	GetAppRegistryCredential(ctx context.Context, accountID, appID, registry string) (AppRegistryCredential, error)
	// ListAppRegistryCredentials returns every (registry, username,
	// ciphertext) row on the app. The handler renders registry +
	// username + timestamps only — ciphertext stays server-side.
	// Ordered by registry ASC for deterministic wire output.
	ListAppRegistryCredentials(ctx context.Context, accountID, appID string) ([]AppRegistryCredential, error)
	// DeleteAppRegistryCredential removes the (app_id, registry) row.
	// Returns ErrNotFound if the row doesn't exist — the handler
	// renders 400 CodeRegistryCredentialsNotFound (not a 404) because
	// the URL resource IS the registry host, by design (mirrors
	// DeleteAppSecret).
	DeleteAppRegistryCredential(ctx context.Context, accountID, appID, registry string) error
	// CountAppRegistryCredentials is the quota check helper. apid
	// calls it before UpsertAppRegistryCredential to enforce
	// Limits.RegistryCredentialMax — but the helper also needs to
	// detect an existing (app_id, registry) row so a password rotation
	// does not consume quota. The handler does the
	// "Get → if found → allow update past cap" check explicitly so
	// the count helper stays a simple count.
	CountAppRegistryCredentials(ctx context.Context, accountID, appID string) (int, error)
	// RegistryCredentialQuotaCheck combines the "count this app's
	// rows" + "does (app, host) already exist" probe into a single
	// query. apid's PUT handler calls this instead of running
	// CountAppRegistryCredentials + GetAppRegistryCredential back-to-
	// back — the round-trip cost is the same shape as the
	// AppSecret / AppEnv quota paths. The (count, exists) shape
	// mirrors the secrets handler's preflight contract: existing
	// (app, host) replaces in place and does not consume quota.
	// Returns (n, false, nil) when the host isn't set yet.
	RegistryCredentialQuotaCheck(ctx context.Context, accountID, appID, registry string) (count int, exists bool, err error)
	// MarkAppRegistryCredentialUsed is called by imaged after a
	// successful authenticated pull. Updates last_used_at + updated_at
	// to now(). Returns ErrNotFound if the row doesn't exist or the
	// (accountID, appID) ownership doesn't match — callers MUST treat
	// ErrNotFound as non-fatal (the deployment already succeeded;
	// missing-on-cascade is an expected race with account/app delete).
	MarkAppRegistryCredentialUsed(ctx context.Context, accountID, appID, registry string) error

	// AppEnv is plaintext runtime config (issue #395 / ADR-045). The
	// four methods mirror the secrets surface 1:1 minus the ciphertext
	// argument — values are stored as TEXT, not sealed bytea.
	//
	// UpsertAppEnv writes-or-replaces the (app_id, scope, key) row,
	// hardcoding scope='default'. Use UpsertAppEnvInScope for any
	// scope other than 'default' (PR-B / PR-C wake). accountID is
	// passed for ownership verification; the row also stores
	// account_id for the account-scoped delete path and the G6 GDPR
	// cascade.
	UpsertAppEnv(ctx context.Context, accountID, appID, key, value string) error
	// DeleteAppEnv removes the (app_id, scope='default', key) row.
	// Returns ErrNotFound if the row doesn't exist — handlers render
	// 400 CodeEnvVarNotFound (intentional: URL resource IS the
	// env-var name). Use DeleteAppEnvInScope for non-default scopes.
	DeleteAppEnv(ctx context.Context, accountID, appID, key string) error
	// ListAppEnv returns every env row on the app where scope =
	// 'default', scoped to accountID. Order: by scope ASC, key ASC
	// for deterministic wake staging (the flat reader sees the same
	// ordering as pre-00203 because all its rows share
	// scope='default'). Returns nil slice (not error) when the app
	// has no env rows — schedd treats that as "no env.json to
	// write". Use ListAppEnvInScope for non-default scopes.
	ListAppEnv(ctx context.Context, accountID, appID string) ([]AppEnv, error)
	// CountAppEnv is the quota check helper. apid calls it before
	// UpsertAppEnv to enforce Limits.EnvVarsMax — counts ALL scope
	// values for the app per ADR-090 D6 (EnvVarsMax is per-app, not
	// per-scope). Use CountAppEnvInScope for future per-scope caps
	// (ADR-091 follow-up).
	CountAppEnv(ctx context.Context, accountID, appID string) (int, error)

	// UpsertAppEnvInScope is the scope-aware sibling of UpsertAppEnv
	// (ADR-090 PR-B / PR-C). Writes-or-replaces the (app_id, scope,
	// key) row with the caller-supplied scope. The flat methods
	// (UpsertAppEnv / DeleteAppEnv / ListAppEnv / CountAppEnv) are
	// thin wrappers that hardcode scope='default' and are kept
	// non-breaking so the wake-time reader in
	// pkg/sched/engine.go:4153-4167 (loadAPIEnv) keeps working
	// until PR-C's nested-decode path lands.
	UpsertAppEnvInScope(ctx context.Context, accountID, appID, scope, key, value string) error
	// DeleteAppEnvInScope is the scope-aware sibling of DeleteAppEnv.
	// Returns ErrNotFound when no row matches the (app_id, scope,
	// key) tuple.
	DeleteAppEnvInScope(ctx context.Context, accountID, appID, scope, key string) error
	// ListAppEnvInScope is the scope-aware sibling of ListAppEnv.
	// Returns every (key, value) row on the app where scope matches
	// the caller-supplied value, scoped to accountID. Order: by
	// scope ASC, key ASC for deterministic staging.
	ListAppEnvInScope(ctx context.Context, accountID, appID, scope string) ([]AppEnv, error)
	// CountAppEnvInScope is the scope-aware sibling of CountAppEnv.
	// Counts only rows where scope matches the caller-supplied
	// value. Reserved for future per-scope caps (ADR-091 follow-up);
	// PR-A does not call it.
	CountAppEnvInScope(ctx context.Context, accountID, appID, scope string) (int, error)

	// ListAllAppEnv returns every env row on the app across ALL
	// scopes, scoped to accountID. Order: by scope ASC, key ASC.
	// Used by apid's GET /v1/apps/{slug}/envs?scope=__all__ arm
	// (ADR-090 PR-B) to render the nested `env_by_scope` response
	// shape (D3). The flat ListAppEnv + per-scope ListAppEnvInScope
	// methods are still the read path for the per-scope arms; this
	// one is reserved for the operator-only all-scopes read and
	// is rare enough that no quota is enforced at the call site.
	ListAllAppEnv(ctx context.Context, accountID, appID string) ([]AppEnv, error)

	// AppTrustedSigner is the per-app cosign trusted-publisher list
	// (issue #472 / ADR-054). apid is the only writer; imaged reads
	// the matching set at deploy-time verify. The four methods mirror
	// the AppEnv surface 1:1: accountID is passed for ownership
	// verification (and to scope the IDOR guard), pubKey is the raw
	// DER SPKI bytea, addedByAccountID is stamped on every write.
	UpsertAppTrustedSigner(ctx context.Context, accountID, appID, signerName string, pubKey []byte, addedByAccountID string) (addedAt time.Time, rotated bool, err error)
	// DeleteAppTrustedSigner removes the (app_id, signer_name) row.
	// Returns ErrNotFound if the row doesn't exist — handlers render
	// 404 CodeTrustedSignerNotFound (URL resource IS the signer name).
	DeleteAppTrustedSigner(ctx context.Context, accountID, appID, signerName string) error
	// ListAppTrustedSigners returns every trusted-signer row on the
	// app, scoped to accountID. Order: by signer_name ASC for
	// deterministic verify-loop ordering. Returns nil slice when the
	// app has no trusted signers — handler renders fail-closed 403.
	ListAppTrustedSigners(ctx context.Context, accountID, appID string) ([]AppTrustedSigner, error)
	// ListAppTrustedSignersForApp is the system-side equivalent of
	// ListAppTrustedSigners that takes only appID. The on-disk
	// mirror writer (cmd/apid/trusted_publisher_writer.go) is the
	// only production caller — it doesn't have an accountID in
	// scope (the notify payload carries app_id only). Sibling
	// helper of ListAppTrustedSigners; same shape, same order.
	ListAppTrustedSignersForApp(ctx context.Context, appID string) ([]AppTrustedSigner, error)
	// CountAppTrustedSigners is the quota check helper. apid calls it
	// before UpsertAppTrustedSigner to enforce
	// Limits.TrustedSignerCountMax.
	CountAppTrustedSigners(ctx context.Context, accountID, appID string) (int, error)

	// --- Organizations (ADR-061, IAM-6, PR 2) -----------------------------
	//
	// PR 2 introduces the schema and state-store surface for orgs,
	// memberships, and invitations. PR 3 stamps personal orgs onto every
	// existing account (backfill); PR 5 adds the handlers that call these
	// methods. PR 2 ships no handler-side callers, so the methods are
	// exercised only by sister-file parity tests for now.
	//
	// The Store interface is INTERFACE-PURE — no pgx.Tx leaks. The single
	// tx-heavy method (ConsumeOrgInvitation) opens and commits its own
	// transaction internally; the only seam of note is that pgstore's
	// internals use BeginTx inline.

	// TODO(issue-190 PR 4): wrap CreateOrg (and every mutating org
	// method below) with a pkg/authz RequireOrgAction(action) seam
	// before handlers land in PR 5. PR 2 ships only the store surface;
	// PR 4 introduces the closed role/action table and the middleware
	// facade. Until then, callers (only sister-file parity tests today)
	// bypass role enforcement; this is deliberate so PR 2 can merge
	// without a circular dep on pkg/authz.

	// CreateOrg inserts a new org row. Returns ErrConflict on slug
	// collision (case-insensitive via the lower(slug) unique index).
	// Personal = true rows must carry PersonalOwnerAccountID and the
	// (personal_owner_account_id) WHERE personal_org = true partial
	// unique enforces exactly-one-personal-per-account; a second personal
	// org for the same account returns ErrConflict.
	CreateOrg(ctx context.Context, o Org) (Org, error)

	// OrgByID is the canonical lookup by primary key. Returns
	// ErrNotFound when the row does not exist.
	OrgByID(ctx context.Context, id string) (Org, error)

	// OrgBySlug is the case-insensitive slug lookup that backs the
	// path-scoped /v1/orgs/{slug}/* routes (PR 4/5).
	OrgBySlug(ctx context.Context, slug string) (Org, error)

	// OrgByPersonalAccount returns the unique personal-org row for an
	// account. Returns ErrNotFound if the account has no personal org
	// yet (pre-PR-3) or if a personal-org row was never backfilled.
	OrgByPersonalAccount(ctx context.Context, accountID string) (Org, error)

	// ListOrgsForAccount returns every org the account has an active
	// membership in. Used by GET /v1/orgs in PR 4.
	ListOrgsForAccount(ctx context.Context, accountID string) ([]Org, error)

	// UpdateOrgPlan is the PR 7 seam — moves plan changes from
	// UpdateAccountPlan onto the personal org inside one transaction.
	UpdateOrgPlan(ctx context.Context, id string, plan api.Plan) error

	// UpdateOrgName updates the org's display name. Returns ErrNotFound
	// when the id is unknown. The handler bounds the name length and
	// trims whitespace before reaching the Store (the SQL CHECK rejects
	// empty strings and names >256 bytes — 23514).
	UpdateOrgName(ctx context.Context, id, name string) error

	// UpdateOrgStatus mirrors UpdateAccountStatus for PR 7's dunning
	// pivot. Valid statuses are active / past_due / suspended /
	// deleted_pending (CHECK enforced at SQL).
	UpdateOrgStatus(ctx context.Context, id string, status OrgStatus) error

	// SoftDeleteOrg sets deleted_pending = true. The hard-delete flow
	// lands in PR 8; PR 2 only stamps the flag.
	SoftDeleteOrg(ctx context.Context, id string) error

	// AddOrgMember inserts a membership row. Returns ErrConflict on
	// duplicate (org_id, account_id), and ErrOrgLastOwner if the
	// (org_id, role='owner' AND removed_at IS NULL) partial unique
	// trips (mapped from 23505). invitedBy may be nil when the
	// member was the personal-org backfill (no inviter).
	AddOrgMember(ctx context.Context, orgID, accountID string, role OrgRole, invitedBy *string) error

	// RemoveOrgMember stamps removed_at = now() on the row. Returns
	// ErrOrgLastOwner when the row is the only active owner — the
	// ownership-transfer seam in PR 5 is the only path that can
	// remove the last owner.
	RemoveOrgMember(ctx context.Context, orgID, accountID string) error

	// UpdateOrgMemberRole updates the role. Returns ErrOrgLastOwner
	// when demoting the only active owner to a non-owner role.
	UpdateOrgMemberRole(ctx context.Context, orgID, accountID string, role OrgRole) error

	// TransferOrgOwnership is the org-handler-only path to owner role.
	// It atomically (a) demotes the current owner to admin and (b)
	// promotes the to-account to owner inside one PostgreSQL
	// transaction. The exactly-one-owner invariant is enforced by
	// the partial unique org_memberships_one_owner_idx
	// (migrations/00099); the tx fails with ErrOrgLastOwner if
	// either row violates the invariant during the swap.
	//
	// Returns ErrNotFound if EITHER the from-account or to-account
	// has no current active membership in the org (the from-account
	// is the current owner — caller-side RBAC has already gated).
	// Returns ErrOrgLastOwner if the from-account is not the active
	// owner (already-transferred or never-owned; the partial unique
	// is the tripwire).
	TransferOrgOwnership(ctx context.Context, orgID, fromAccountID, toAccountID string) error

	// ListOrgMembers returns every membership row (including
	// removed_at != nil) ordered by joined_at. The handler layer
	// filters removed rows at the API boundary.
	ListOrgMembers(ctx context.Context, orgID string) ([]OrgMembership, error)

	// CountActiveOrgMembers returns the number of memberships with
	// removed_at IS NULL for the given org. Filtered at the SQL
	// layer so the count does not scan every row into Go. Used by:
	//   - the store-side cap-in-tx check inside consumeOrgInvitation
	//     (the load-bearing gate — Plan.OrgMembersMax)
	//   - GET /v1/orgs/{slug}/seat_usage for the visibility-only
	//     wire shape (IAM-6 / ADR-061 PR 7)
	//
	// IAM-6 / ADR-061 PR 7 note: the earlier comment claiming
	// "apid's enforceMemberCap gates the handler path" is stale.
	// That helper is intentionally unwired
	// (cmd/apid/org_handler_helpers.go) and the future direct-add
	// route (PR-11 follow-up) is what would call it.
	//
	// Returns 0 when the org has no rows.
	CountActiveOrgMembers(ctx context.Context, orgID string) (int, error)

	// OrgMemberByAccount returns the (org_id, account_id) row.
	// Returns ErrNotFound when no membership exists.
	OrgMemberByAccount(ctx context.Context, orgID, accountID string) (OrgMembership, error)

	// CreateOrgInvitation inserts a new pending invitation. The handler
	// (PR 5) generates the random token bytes and SHA-256-hashes them
	// before calling this method; only the hash is stored. Returns
	// ErrConflict on duplicate token_hash (astronomically unlikely).
	CreateOrgInvitation(ctx context.Context, inv OrgInvitation) (OrgInvitation, error)

	// OrgInvitationByTokenHash is the consume / revoke lookup. Returns
	// ErrNotFound for unknown hashes.
	OrgInvitationByTokenHash(ctx context.Context, hash []byte) (OrgInvitation, error)

	// ConsumeOrgInvitation is the tx-heavy PR 5 acceptance path. It
	// locks the invitation row FOR UPDATE, validates state (pending +
	// not expired), email-equality against the accepting account,
	// membership cap, then inserts the membership and stamps
	// consumed_at + accepting_account_id atomically. Concurrent
	// accepts race at the partial unique or at the cap check and
	// surface as ErrOrgAlreadyMember / ErrOrgInvitationInvalid /
	// ErrOrgMemberCapExceeded. Returns the resulting membership and
	// the consumed invitation row.
	ConsumeOrgInvitation(ctx context.Context, hash []byte, acceptingAccount Account) (OrgMembership, OrgInvitation, error)

	// RevokeOrgInvitation stamps revoked_at = now() if the row is
	// still pending. Returns ErrOrgInvitationInvalid when the
	// invitation is already consumed or revoked.
	RevokeOrgInvitation(ctx context.Context, hash []byte, byAccountID string) error

	// ListOrgInvitationsForOrg returns every invitation row
	// (regardless of state) ordered by created_at desc. The
	// invitation-cleanup loop filters pending + expired at the
	// caller.
	ListOrgInvitationsForOrg(ctx context.Context, orgID string) ([]OrgInvitation, error)

	// ListOrgInvitationsForOrgPage is the cursor-paginated variant
	// of ListOrgInvitationsForOrg (PR-8 acceptance). Cursor is
	// the invitation's id (UUID); the SQL filter `id < $before`
	// partitions rows by id and the handler emits
	// `out[len-1].ID` as the next cursor, so the walk visits every
	// row exactly once regardless of insertion order.
	//
	// limit is clamped to [1, 100] inside the implementation;
	// out-of-range values resolve to 25 (the documented default).
	// The order is (created_at DESC, id DESC) — id is the
	// tiebreaker so the cursor walk is well-defined when two rows
	// share a created_at timestamp (concurrent mint from the same
	// admin). Returned slice may be shorter than limit on the
	// final page; an empty slice means "no more rows".
	ListOrgInvitationsForOrgPage(ctx context.Context, orgID string, limit int, before string) ([]OrgInvitation, error)

	// CountPendingOrgInvitations returns the number of invitation
	// rows with consumed_at IS NULL AND revoked_at IS NULL AND
	// expires_at > now() for the given org. Filtered at the SQL
	// layer so the count does not scan every row into Go. Used by
	// apid's enforcePendingInvitationCap (Plan.OrgPendingInvitationsMax).
	// Returns 0 when the org has no rows.
	CountPendingOrgInvitations(ctx context.Context, orgID string) (int, error)

	// ExpireOrgInvitations is the cleanup-tick method (modelled on
	// the login-token cleanup loop) that stamps revoked_at on every
	// pending + past-expires_at row in one UPDATE. Returns the count
	// of rows transitioned so the caller can log a metric.
	ExpireOrgInvitations(ctx context.Context, now time.Time) (int64, error)

	// ----------------------------------------------------------------------------
	// Outbound webhook delivery (issue #476 / ADR-076)
	//
	// AppWebhook CRUD lives behind the same per-account / per-app quota
	// gate as AlertRule (mirrors CreateAlertRuleIfUnderQuota at
	// store.go:1422-1432). The dispatcher's claim/mark methods live
	// alongside the CRUD so the dispatcher can compose a tick without
	// touching the apid-side surface.
	// ----------------------------------------------------------------------------

	// CreateAppWebhook is the un-capped insert path used by tests. The
	// customer-facing handler always calls CreateAppWebhookIfUnderQuota.
	CreateAppWebhook(ctx context.Context, w AppWebhook) (AppWebhook, error)
	// CreateAppWebhookIfUnderQuota inserts a webhook iff the per-app
	// and per-account caps (limits.WebhookPerApp /
	// WebhookPerAccount) are not yet reached. The account cap is
	// checked under an accounts-row FOR UPDATE lock; the per-app cap
	// is checked under an apps-row FOR UPDATE lock — same TOCTOU-
	// defence pattern as CreateCronIfUnderQuota (store.go:1385-1396).
	// Returns:
	//   - (AppWebhook{}, *AppWebhookQuotaError) when either cap trips
	//   - (AppWebhook{}, ErrNotFound) when the app row is missing
	//   - (AppWebhook{}, ErrConflict) on a duplicate (app_id, target_url)
	CreateAppWebhookIfUnderQuota(ctx context.Context, w AppWebhook, limits api.Limits) (AppWebhook, error)
	AppWebhookByID(ctx context.Context, id string) (AppWebhook, error)
	// UpdateAppWebhook mutates the optional fields of a webhook row.
	// See UpdateAppWebhookParams at types.go for the pointer-to-
	// pointer contract (nil = don't touch).
	UpdateAppWebhook(ctx context.Context, id string, params UpdateAppWebhookParams) (AppWebhook, error)
	DeleteAppWebhook(ctx context.Context, id string) error
	ListAppWebhooksForApp(ctx context.Context, appID string) ([]AppWebhook, error)
	// ListAppWebhooksForAccount backs the per-account GET endpoint
	// and the operator's "all webhooks for an account" view.
	ListAppWebhooksForAccount(ctx context.Context, accountID string) ([]AppWebhook, error)

	// RecordAppWebhookDelivery is the apid-side enqueue. Called by
	// the event emitters (cron dispatcher, app lifecycle handlers)
	// once per (event, target webhook) emission. The dispatcher's
	// claim query picks the row up at next_attempt_at <= now().
	RecordAppWebhookDelivery(ctx context.Context, d AppWebhookDelivery) (AppWebhookDelivery, error)

	// ClaimDueAppWebhookDeliveries is the dispatcher's tick entry.
	// In a single transaction it:
	//   1. Locks up to `limit` rows whose status IN
	//      ('pending','in_flight') AND next_attempt_at <= `now`,
	//      ORDER BY account_id, next_attempt_at (per-account round-
	//      robin emerges from the ORDER BY).
	//   2. Transitions status='pending' → 'in_flight' for the locked
	//      rows. 'in_flight' rows that were already past
	//      next_attempt_at (orphaned by a dispatcher restart) are
	//      re-claimed and re-tried — see MarkAppWebhookDeliveryFailed
	//      for the crash-recovery reasoning.
	//   3. Returns the locked rows for the caller to process.
	// The transaction commits before the dispatcher starts the HTTP
	// work; status='in_flight' is the post-commit visible state.
	ClaimDueAppWebhookDeliveries(ctx context.Context, limit int, now time.Time) ([]AppWebhookDelivery, error)
	// MarkAppWebhookDeliverySucceeded stamps status='succeeded',
	// delivered_at=deliveredAt, last_response_code=responseCode,
	// attempt=currentAttempt+1 (the successful attempt count). The
	// dispatcher calls this on a 2xx response.
	MarkAppWebhookDeliverySucceeded(ctx context.Context, id string, responseCode int, currentAttempt int, deliveredAt time.Time) error
	// MarkAppWebhookDeliveryFailed stamps status='failed',
	// next_attempt_at=nextAttemptAt, attempt=currentAttempt+1,
	// last_error=errMsg, last_response_code=responseCode. The
	// dispatcher calls this on a retryable error (5xx/408/429/
	// network) when the next attempt is within the budget.
	MarkAppWebhookDeliveryFailed(ctx context.Context, id string, responseCode int, currentAttempt int, errMsg string, nextAttemptAt time.Time) error
	// MarkAppWebhookDeliveryDead stamps status='dead' with the
	// supplied errMsg. The dispatcher calls this on:
	//   - attempt >= 7 (budget exhausted)
	//   - terminal 4xx (non-408/429)
	// Once dead, the row stays dead until the customer POSTs
	// /deliveries/{id}/retry.
	MarkAppWebhookDeliveryDead(ctx context.Context, id string, currentAttempt int, errMsg string) error
	// ResetAppWebhookDeliveryFromDead is the customer-facing
	// "retry a dead delivery" path. Stamps status='pending',
	// next_attempt_at=now, attempt=0 (full budget re-armed). Used
	// by the apid POST /retry handler and the gregale
	// `webhooks retry` subcommand. The webhookID + accountID
	// filters are the SQL-level IDOR guard: a caller holding a
	// delivery id from another customer's webhook gets ErrNotFound,
	// not silent cross-tenant reset.
	ResetAppWebhookDeliveryFromDead(ctx context.Context, id, webhookID, accountID string, now time.Time) error

	// ListAppWebhookDeliveries backs GET
	// /v1/apps/{slug}/webhooks/{id}/deliveries. pageToken is the
	// opaque cursor returned by the previous call ("" = first page).
	// The result is ordered by created_at DESC (most recent first)
	// — the dashboard's "recent deliveries" pane orientation.
	ListAppWebhookDeliveries(ctx context.Context, appID, webhookID string, pageSize int, pageToken string) ([]AppWebhookDelivery, string, error)
	// AppWebhookDeliveryByID backs the per-delivery retry path (POST
	// /deliveries/{id}/retry) and the dispatcher-side audit
	// emission that needs to read the row's account_id + app_id.
	AppWebhookDeliveryByID(ctx context.Context, id string) (AppWebhookDelivery, error)

	// --- ADR-096 customer-facing automatic error grouping ---
	//
	// The writer methods (IncrementAppError / InsertAppErrorRequest)
	// are called by the apid gRPC server-side handler in
	// cmd/apid/grpc_server_apperrors.go; gatewayd-internal dials
	// apid over a unix socket and never touches the Store
	// directly. The reader methods back the
	// /v1/apps/{slug}/errors/* handlers in PR-B.

	// IncrementAppError is the dedupe-merge INSERT called by
	// grpc_server_apperrors.go. Runs ON CONFLICT (account_id,
	// app_id, fingerprint) DO UPDATE so a fresh row with an
	// existing fingerprint bumps count + request_count + bumps
	// last_seen_at. The handler wraps N calls in a single pgx
	// transaction (one per gRPC stream batch). params use sqlc
	// types to match the generated query method (ADR-006 +
	// TrafficAnomalyAggregate precedent at pkg/state/store.go
	// line 2611 — sqlc types leak through the Store interface
	// for the queries that sqlc owns; readers convert to typed
	// AppErrorGroup/Row/Row at the boundary).
	IncrementAppError(ctx context.Context, arg sqlc.IncrementAppErrorParams) (bool, error)

	// InsertAppErrorRequest writes one drill-down row per request
	// that hit the fingerprint. No ON CONFLICT — every request
	// gets its own row. Paired with IncrementAppError on the same
	// gRPC stream batch.
	InsertAppErrorRequest(ctx context.Context, arg sqlc.InsertAppErrorRequestParams) error

	// ListAppErrorGroups backs GET /v1/apps/{slug}/errors/summary
	// (PR-B). Cursor-paginated via (last_seen_at, fingerprint)
	// when cursor != ""; otherwise first page. limit MUST be
	// pre-clamped to api.AppErrorsSummaryMaxLimit by the handler.
	ListAppErrorGroups(ctx context.Context, arg sqlc.ListAppErrorGroupsParams) ([]AppErrorGroup, error)

	// ListAppErrorRequests backs GET /v1/apps/{slug}/errors/{fp}
	// (PR-B). Cursor-paginated via (received_at, request_id).
	// limit MUST be pre-clamped by the handler.
	ListAppErrorRequests(ctx context.Context, arg sqlc.ListAppErrorRequestsParams) ([]AppErrorRequestRow, error)

	// GetAppErrorSample backs GET /v1/apps/{slug}/errors/{fp}/first
	// (PR-B). Returns the OLDEST request row for one fingerprint.
	// headers_sample + redactions are populated for the
	// wire-side "we redacted X / Y / Z" badge.
	GetAppErrorSample(ctx context.Context, arg sqlc.GetAppErrorSampleParams) (AppErrorSampleRow, error)

	// ListAppErrorFingerprintsForPurge is the read-side of the
	// nightly retention purge (cmd/apid/app_errors_purge.go).
	// Returns IDs of app_errors rows for an account older than
	// cutoff, capped at 10000 per call. DeleteAppErrorsByIDs +
	// DeleteAppErrorRequestsByIDs follow.
	ListAppErrorFingerprintsForPurge(ctx context.Context, arg sqlc.ListAppErrorFingerprintsForPurgeParams) ([]uuid.UUID, error)

	// DeleteAppErrorsByIDs removes app_errors rows by ID array.
	// Used by the nightly retention purge.
	DeleteAppErrorsByIDs(ctx context.Context, ids []uuid.UUID) error

	// DeleteAppErrorRequestsByIDs removes app_error_requests rows
	// by ID array. Used by the nightly retention purge.
	DeleteAppErrorRequestsByIDs(ctx context.Context, ids []uuid.UUID) error

	// DeleteAppErrorRequestsOlderThan removes ALL
	// app_error_requests rows for one account older than the
	// cutoff timestamp. Used by the nightly retention purge to
	// age out orphaned request rows.
	DeleteAppErrorRequestsOlderThan(ctx context.Context, accountID uuid.UUID, cutoff time.Time) error

	// --- ADR-127 production debugger (§Decision 1) ---
	//
	// The writer methods (InsertRequestTelemetry) are called by
	// the apid gRPC server-side handler in
	// cmd/apid/grpc_server_request_telemetry.go; gatewayd-internal
	// dials apid over a unix socket and never touches the Store
	// directly. Same ownership pattern as the AppError writes
	// above (cmd/gatewayd-internal/app_errors_recorder.go:17-21).
	// The reader methods back the
	// /v1/apps/{slug}/debug/requests/* handlers in PR-A.

	// InsertRequestTelemetry is the per-request INSERT called by
	// grpc_server_request_telemetry.go. One row per gateway-served
	// request; no ON CONFLICT — every request gets its own row.
	// The recorder's in-process LRU dedupe at minute granularity
	// is the upstream tripwire; the unique-index absence here is
	// intentional (request_id is the natural dedupe, but the
	// recorder doesn't carry it).
	InsertRequestTelemetry(ctx context.Context, arg sqlc.InsertRequestTelemetryParams) error

	// UpdateSpansSummary is the per-trace UPDATE called by the
	// apid gRPC WriteSpansSummary handler (ADR-127 PR-D). It
	// enriches the existing row with the OTel span summary jsonb
	// payload coalesced by pkg/gateway/spans_accumulator.go. The
	// 24h time window matches the partial index
	// request_telemetry_trace_idx selectivity; last-writer-wins on
	// concurrent UPDATEs is acceptable. summary is the raw JSON
	// bytes (the caller has already validated json.Valid).
	//
	// accountID is REQUIRED — PR-D code-review #1 pins the
	// WHERE clause on (trace_id, account_id) so a buggy upstream
	// caller can't overwrite a different customer's row. The
	// gateway-side accumulator already rejects
	// trace_id/account_id collisions (ErrAccountMismatch); this
	// is the SQL-side defense in depth.
	UpdateSpansSummary(ctx context.Context, traceID string, accountID uuid.UUID, summary []byte) error

	// ListRequestTelemetryByApp backs GET /v1/apps/{slug}/debug/requests.
	// Time-windowed (since, until) with hard limit; cursor pagination
	// is by (received_at DESC, id) tuple, matching the
	// request_telemetry_app_received_idx index direction. limit MUST
	// be pre-clamped to api.DebugTelemetryMaxLimit by the handler.
	ListRequestTelemetryByApp(ctx context.Context, arg sqlc.ListRequestTelemetryByAppParams) ([]sqlc.ListRequestTelemetryByAppRow, error)

	// RequestTelemetryByDeployment backs the per-deployment
	// drilldown and the regression detector (PR-B cron). Uses
	// request_telemetry_app_dep_received_idx. Same limit contract
	// as ListRequestTelemetryByApp.
	RequestTelemetryByDeployment(ctx context.Context, arg sqlc.RequestTelemetryByDeploymentParams) ([]sqlc.RequestTelemetryByDeploymentRow, error)

	// RequestTelemetryBaselineP95ByRoute backs the regression
	// detector's per-route p95 baseline lookup. PR-B's cron calls
	// this per-deployment, then composes the result with
	// RequestTelemetryByDeployment in Go (the CTE-on-CTE shape
	// that would back a single-round-trip regression query trips
	// sqlc v1.31's "ambiguous column" parser; PR-B documents the
	// Go-side composition in pkg/state/pgstore.go).
	RequestTelemetryBaselineP95ByRoute(ctx context.Context, arg sqlc.RequestTelemetryBaselineP95ByRouteParams) ([]sqlc.RequestTelemetryBaselineP95ByRouteRow, error)

	// --- ADR-127 PR-B — regression observation persistence + dashboard reads ---

	// UpsertRegressionObservation writes (or refreshes) the
	// regression observation row for (app_id, deployment_id, route).
	// The cron in cmd/apid/debug_regression_cron.go calls this on
	// every 5-minute pass when a regression is detected. PRIMARY
	// KEY upsert keeps the table bounded — at most one row per
	// (deployment, route), not one row per cron tick.
	// first_detected_at is set on INSERT only; last_detected_at
	// refreshed to now() on every pass.
	UpsertRegressionObservation(ctx context.Context, arg sqlc.UpsertRegressionObservationParams) error

	// ListActiveRegressionsByApp backs GET /v1/apps/{slug}/debug/regressions
	// and the dashboard regression banner. since is an interval
	// (handler-side clamp: DebugTelemetryRetentionDays per plan).
	// ORDER BY regression_factor DESC, last_detected_at DESC matches
	// the dashboard render order (worst first, most-recent next).
	ListActiveRegressionsByApp(ctx context.Context, arg sqlc.ListActiveRegressionsByAppParams) ([]sqlc.ListActiveRegressionsByAppRow, error)

	// ListDeploymentsForCompare backs the dashboard compare panel's
	// two <select> dropdowns. Returns distinct deployment_ids that
	// have shipped traffic in the window, with first_seen / last_seen
	// / row_count metadata so the panel can render
	// "v81 — 17m of traffic, 4123 rows" without a second query.
	ListDeploymentsForCompare(ctx context.Context, arg sqlc.ListDeploymentsForCompareParams) ([]sqlc.ListDeploymentsForCompareRow, error)

	// ListAppsWithRecentTelemetry is the regression cron's discovery
	// loop seam. Returns distinct app_ids that have shipped at least
	// one request in the window so the cron doesn't have to walk
	// the full `apps` table on every tick.
	ListAppsWithRecentTelemetry(ctx context.Context, arg pgtype.Interval) ([]pgtype.UUID, error)

	// --- ADR-098 connection-aware execution (§9.A) ---
	//
	// Writer methods (InsertDataUpstream / DeleteDataUpstreamByID
	// / InsertDataUpstreamProbe / PruneDataUpstreamProbesOlderThan)
	// are wired by PR-B/C:
	//   - apid's env-classifier (cmd/apid/extract.go) calls
	//     InsertDataUpstream / DeleteDataUpstreamByID
	//   - meterd's probe loop (cmd/meterd/probe_loop.go) calls
	//     InsertDataUpstreamProbe every 30s per
	//     (host_redacted_hash, region)
	//   - meterd's hourly retention cron calls
	//     PruneDataUpstreamProbesOlderThan
	//
	// Reader methods (ListDataUpstreamsByApp /
	// GetDataUpstreamByID / ListDataUpstreamProbesByHostRegion)
	// back:
	//   - GET /v1/apps/{slug}/upstreams (PR-B;
	//     ListDataUpstreamsByApp)
	//   - GET /v1/apps/{slug}/upstreams/{id} (PR-B;
	//     GetDataUpstreamByID)
	//   - schedd's wake-side affinity read (PR-B/C;
	//     ListDataUpstreamProbesByHostRegion)
	//
	// PR-A ships these on the interface with Postgres + MemStore
	// stubs so the package compiles and unit tests don't regress.
	// PR-B replaces the apid / meterd / schedd call sites with
	// the production wiring.

	// InsertDataUpstream writes one data_upstreams row via the
	// dedupe-merge ON CONFLICT tripwire on
	// data_upstreams_dedupe_uniq (PR-B env-classifier). ADR-098
	// amendment (issue #954) widens the dedupe key to include
	// deployment_scope; the caller threads
	// arg.DeploymentScope alongside scope, kind, host, port.
	InsertDataUpstream(ctx context.Context, arg sqlc.InsertDataUpstreamParams) (uuid.UUID, error)

	// ListDataUpstreamsByApp backs
	// GET /v1/apps/{slug}/upstreams (PR-B). Cursor-paginated via
	// (created_at, id); limit MUST be pre-clamped to
	// api.DataUpstreamsListMaxLimit by the handler. ADR-098
	// amendment (issue #954) adds an optional
	// arg.CursorDeploymentScope server-side filter (empty =
	// "return all deployments"; non-empty = "one deployment").
	ListDataUpstreamsByApp(ctx context.Context, arg sqlc.ListDataUpstreamsByAppParams) ([]DataUpstream, error)

	// ListAllAppDataUpstreams backs
	// GET /v1/apps/{slug}/upstreams?scope=__all__ (PR-B).
	// Returns every data_upstreams row on the app across all
	// scopes — the count is bounded by
	// DataPlacementHintsPerApp (per ADR-098 §D5) so the scan
	// is cheap.
	ListAllAppDataUpstreams(ctx context.Context, accountID, appID string) ([]DataUpstream, error)

	// CountDataUpstreamsByApp backs the per-plan
	// DataPlacementHintsPerApp quota in createUpstream
	// (PR-B). Counts across ALL scopes per §D5.
	CountDataUpstreamsByApp(ctx context.Context, accountID, appID string) (int, error)

	// GetDataUpstreamByID backs
	// GET /v1/apps/{slug}/upstreams/{id} (PR-B).
	GetDataUpstreamByID(ctx context.Context, id uuid.UUID) (DataUpstream, error)

	// DeleteDataUpstreamByID backs
	// DELETE /v1/apps/{slug}/upstreams/{id} (PR-B).
	DeleteDataUpstreamByID(ctx context.Context, id uuid.UUID) error

	// InsertDataUpstreamProbe is meterd's probe-loop writer. One
	// row per (host_redacted_hash, region) per 30s sample.
	// Partitioning on sampled_at gives the hot-write path; the
	// partition creator (PR-C) drops old partitions wholesale.
	InsertDataUpstreamProbe(ctx context.Context, arg sqlc.InsertDataUpstreamProbeParams) error

	// ListDataUpstreamProbesByHostRegion is schedd's wake-side
	// read path (PR-B/C). Returns the N most recent samples for
	// one (host_redacted_hash, region) pair within a time window.
	// Partition pruning on sampled_at drops everything outside
	// the window.
	ListDataUpstreamProbesByHostRegion(ctx context.Context, arg sqlc.ListDataUpstreamProbesByHostRegionParams) ([]DataUpstreamProbe, error)

	// ListDistinctUpstreamHostHashes (PR-C / meterd probe
	// loop) walks data_upstreams and returns the
	// deduplicated set of (host_redacted_hash, kind, port)
	// tuples — the probe iterates this set on every tick.
	// The plaintext host is NEVER returned; the §11 secret
	// rule is the reason.
	ListDistinctUpstreamHostHashes(ctx context.Context) ([]DataUpstreamTarget, error)

	// PruneDataUpstreamProbesOlderThan is the hourly retention
	// purge. cutoff is typically now() - 30 days (matches the
	// §12 prom_retention_days:15 floor × 2 safety margin).
	PruneDataUpstreamProbesOlderThan(ctx context.Context, cutoff time.Time) error

	// ----------------------------------------------------------------------------
	// DeploymentScopeExclusion CRUD (ADR-124 follow-up #3, migration
	// 00418). Persistent --exclude history so a subsequent
	// `gregale deploy` without --exclude still honors the operator's
	// previous intent. The CRUD is small (4 methods) because the
	// apply path only needs Create + Lookup; the admin tooling
	// (List, Delete) is the rest.
	// ----------------------------------------------------------------------------

	// CreateDeploymentScopeExclusion inserts a single persisted
	// --exclude row. The un-capped path is used by tests; the
	// customer-facing handler always calls CreateDeploymentScopeExclusion
	// (there is no quota gate today — the row count per project is
	// bounded by the number of distinct workloads, typically 1-50).
	// Returns:
	//   - (DeploymentScopeExclusion{}, ErrConflict) on a duplicate
	//     (account_id, project_id, slug) — the UNIQUE constraint.
	CreateDeploymentScopeExclusion(ctx context.Context, in DeploymentScopeExclusion) (DeploymentScopeExclusion, error)
	// ListDeploymentScopeExclusions returns every active exclusion
	// for a project, sorted by created_at DESC. Backs the admin
	// tooling's "what's persisted for this project?" view.
	ListDeploymentScopeExclusions(ctx context.Context, projectID string) ([]DeploymentScopeExclusion, error)
	// DeleteDeploymentScopeExclusion is the operator-undo path
	// (the rare "I no longer want this exclusion" flow). Returns
	// ErrNotFound when no row matches the (account, project, slug).
	DeleteDeploymentScopeExclusion(ctx context.Context, accountID, projectID, slug string) error
	// LookupDeploymentScopeExclusions returns every active slug
	// the apply path should fold into the per-deploy exclude list.
	// Translates persisted → per-deploy set at scan/apply time so
	// `gregale deploy` without --exclude still honors the persisted
	// set. Sorted by created_at DESC for stable apply ordering.
	LookupDeploymentScopeExclusions(ctx context.Context, accountID, projectID string) ([]DeploymentScopeExclusion, error)
}
