package state

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dispatch"
)

// Domain types mirroring the schema (spec §5). These are the rows apid and
// schedd read and write; the Store abstracts the actual Postgres access (sqlc
// in production, the in-memory store in tests).

// AccountStatus tracks billing/dunning state (spec §4.7).
type AccountStatus string

const (
	AccountActive         AccountStatus = "active"
	AccountPastDue        AccountStatus = "past_due"
	AccountSuspended      AccountStatus = "suspended"
	AccountDeletedPending AccountStatus = "deleted_pending"
)

// MailSuppressionReason is the closed set the bounce handler emits
// (issue #246 acceptance item 7, ADR-115 §D.3). 'hard_bounce'
// triggers dunning via MarkDunningStep; 'complaint' suppresses but
// does NOT transition (suspending an account because someone hit
// "spam" is hostile); 'manual' is for operator overrides.
type MailSuppressionReason string

const (
	MailSuppressionHardBounce MailSuppressionReason = "hard_bounce"
	MailSuppressionComplaint  MailSuppressionReason = "complaint"
	MailSuppressionManual     MailSuppressionReason = "manual"
)

// MailSuppressionSource identifies the origin of a suppression row
// so the (source, provider_event_id) unique index dedupes correctly
// across providers. 'resend' / 'postmark' are the live transports;
// 'operator' covers manual overrides.
type MailSuppressionSource string

const (
	MailSuppressionSourceResend   MailSuppressionSource = "resend"
	MailSuppressionSourcePostmark MailSuppressionSource = "postmark"
	MailSuppressionSourceOperator MailSuppressionSource = "operator"
)

// MailSuppressionInput is the bounce handler's payload for
// Store.RecordMailSuppression. AccountID is nullable: a bounce can
// land before the handler has correlated the address with an
// account, in which case the row is still written and the
// suppression decorator drops future mail to that address. The FK
// on the table cascades so deleting an account drops its rows.
type MailSuppressionInput struct {
	AccountID       *string // nil if not correlated yet
	Email           string
	Reason          MailSuppressionReason
	Source          MailSuppressionSource
	ProviderEventID string
	ExpiresAt       *time.Time // nil = suppression is permanent
}

// AppType distinguishes a plain App from a Function (spec §2, ADR-003).
type AppType string

const (
	AppTypeApp      AppType = "app"
	AppTypeFunction AppType = "function"
)

// AppStatus is the app's lifecycle (distinct from an instance's State).
type AppStatus string

const (
	AppActive      AppStatus = "active"
	AppEvictedCold AppStatus = "evicted_cold"
	AppDeleted     AppStatus = "deleted"
)

// DeploymentKind distinguishes image / tarball / dockerfile deploys (spec §9).
type DeploymentKind string

const (
	DeploymentKindImage      DeploymentKind = "image"
	DeploymentKindTarball    DeploymentKind = "tarball"
	DeploymentKindDockerfile DeploymentKind = "dockerfile"
	// DeploymentKindGitHub tags builds enqueued by the githubd push
	// dispatch path (issue #432 phase 5 follow-up). The physical
	// build pipeline is identical to DeploymentKindTarball
	// (builderd reads dep.SourcePath as a local .tar.gz), but the
	// deployment row identifies it as github-triggered so ADR-048
	// metering dashboards and per-customer billing breakdowns
	// can split apid-CLI deploys from githubd webhook deploys.
	// The CHECK constraint on builds.kind is relaxed to allow
	// this value by migration 00085.
	DeploymentKindGitHub DeploymentKind = "github"
	// DeploymentKindPreview tags builds enqueued by the githubd
	// pull_request preview path (issue #272, ADR-094). Same wire
	// shape as DeploymentKindGitHub (the codeload tarball is
	// fetched server-side via the install-token mint), but the
	// deployment row identifies it as a PR preview so per-customer
	// dashboards and the preview-aware billing breakdown can
	// distinguish ephemeral previews from prod pushes. The CHECK
	// constraint on builds.kind is relaxed to allow this value
	// by migration 00218.
	DeploymentKindPreview DeploymentKind = "preview"
)

// DeploymentStatus tracks a deployment through the pipeline (spec §5, §9).
type DeploymentStatus string

const (
	DeployPending      DeploymentStatus = "pending"
	DeployBuilding     DeploymentStatus = "building"
	DeployImaging      DeploymentStatus = "imaging"
	DeploySnapshotting DeploymentStatus = "snapshotting"
	DeployLive         DeploymentStatus = "live"
	DeployFailed       DeploymentStatus = "failed"
	DeploySuperseded   DeploymentStatus = "superseded"
	// DeployCancelled is the terminal exit set by
	// Store.CancelDeploymentTx (ADR-124). Distinct from
	// DeploySuperseded: cancel is a user-driven retract of a
	// non-live row, while superseded is a system-driven event
	// triggered by a newer deployment landing. The closed-set
	// schema CHECK constraint migrations/00360 widens to accept
	// this value.
	DeployCancelled DeploymentStatus = "cancelled"
)

// IsTerminal reports whether the status is a terminal exit
// (no further transitions allowed). Live is intentionally NOT
// terminal — DeployLive has the implicit parking pipeline
// (engine.ParkDeployment + ADR-118 AutoRollbackDeploymentsTx)
// that can move it to DeploySuperseded.
func (s DeploymentStatus) IsTerminal() bool {
	switch s {
	case DeployFailed, DeploySuperseded, DeployCancelled:
		return true
	}
	return false
}

// IsCancelEligible reports whether the user-initiated cancel
// surface (POST /v1/apps/{slug}/deployments/{id}/cancel) can
// transition this row. The store layer mirrors the same
// predicate in the CAS WHERE clause — see
// pgstore.MarkDeploymentCancelled.
func (s DeploymentStatus) IsCancelEligible() bool {
	switch s {
	case DeployPending, DeployBuilding, DeployImaging, DeploySnapshotting:
		return true
	}
	return false
}

// StageName is the closed set of customer-visible named stages
// surfaced in the SSE `event: stage` frame and in the CLI's
// post-stream summary (ADR-117, migration 00302). Distinct from
// `DeploymentStatus` — the latter drives the state machine
// (`pending → building → imaging → snapshotting → live | failed |
// superseded`); the former is the customer-UX projection that maps
// multiple internal status flips onto one named step.
//
// Schema CHECK constraint `deployments_stage_state_current_check`
// (migrations/00302_deployments_stage_state.sql) enforces the same
// vocabulary at the storage layer so a typo from a future
// contributor lands as SQLSTATE 23514 check_violation at write time
// rather than leaking as a wire-frame typo on `event: stage {name}`.
type StageName string

const (
	// StageSourceDownload is entered when the deployment row leaves
	// DeployPending (the source fetch from the customer tarball /
	// GitHub ref / registry image). First visible stage.
	StageSourceDownload StageName = "source_download"
	// StageDependencyRestore is entered after the cached
	// dependency layer is restored (cache hit path on
	// pkg/builderd/builderd.go:394) or, on a cold cache, the
	// dependency install completes inside the builder VM.
	StageDependencyRestore StageName = "dependency_restore"
	// StageImageBuild is entered when builderd/imaged begin the
	// per-app layer construction (buildImageLayer /
	// buildFunctionLayer / buildLocalOCIAppLayer). Covers both
	// `imaging` and `snapshotting` micro-states of the state
	// machine; the customer sees one step, not two.
	StageImageBuild StageName = "image_build"
	// StageSecurityScan is entered when grype + secret-scan run
	// over the layer (`runDeployScan` at
	// pkg/imaged/handler.go:797-906). The scan completion stamp
	// on deployments.scan_status='complete' is the boundary.
	StageSecurityScan StageName = "security_scan"
	// StageSnapshotPrepare is entered when imaged posts the
	// per-app snapshot to the schedd / vmmd side
	// (`NotifySnapshotPrime` at pkg/imaged/handler.go:2341) and
	// schedd primes the Firecracker microVM for cold-boot.
	StageSnapshotPrepare StageName = "snapshot_prepare"
	// StageReadiness is the final visible stage — entered when
	// vmmd's first cold-boot returns 2xx at the readiness probe
	// (cmd/gatewayd-internal/run.go:967 wake.proxy_first_byte +
	// framework readiness stamp at
	// instances.framework_ready_at, migration 00122). Closes on
	// MarkDeploymentLive (pkg/imaged/handler.go:2240); the
	// `✓ Deployed.` line is owned by the existing terminal-status
	// branch in streamDeployLogs and is NOT a stage row.
	StageReadiness StageName = "readiness"
)

// AllStageNames is the closed vocabulary used by both the
// migrations/00302 jsonb CHECK constraint AND the wire-shape test
// in cmd/gregale/commands2_test.go. Adding a new value here
// without also widening the migration's IN list is a load-bearing
// failure mode — the wire vocabulary on `event: stage {name}` would
// silently drift out of step with the schema CHECK.
var AllStageNames = []StageName{
	StageSourceDownload,
	StageDependencyRestore,
	StageImageBuild,
	StageSecurityScan,
	StageSnapshotPrepare,
	StageReadiness,
}

// stageNameClosedSet is the lookup-table mirror of AllStageNames.
// Built once at package init so per-call validation is O(1) and
// there is no string-literal coupling between call sites and the
// vocabulary. Exported via IsStageName below for apid-side handler
// guards (RetryDeploymentFromStage's handler validates the
// wire-supplied from_stage against this set before the storage
// call).
var stageNameClosedSet = func() map[StageName]bool {
	m := make(map[StageName]bool, len(AllStageNames))
	for _, n := range AllStageNames {
		m[n] = true
	}
	return m
}()

// IsStageName reports whether name is one of the closed-6 stage
// vocabulary values (pkg/state.AllStageNames). Returns false for
// the empty string and any caller-supplied typo. Used by the apid
// retry handler (cmd/apid/handlers_retry.go) to validate the
// wire-supplied from_stage before calling
// Store.RetryDeploymentFromStage.
func IsStageName(name StageName) bool {
	return stageNameClosedSet[name]
}

// MaxStageHistory caps the per-deployment stage history. ADR-117
// §Production-ready follow-on (C1) and migration 00340. The trim
// is FIFO in AppendDeploymentStage (pgstore + memstore); the
// current stage (stage_state.current) is never trimmed. Schema-
// unchanged; the cap is enforced Go-side because a jsonb CHECK
// on jsonb_array_length(stage_state -> 'history') is fragile
// across mutation shapes.
//
// 64 is the customer-tested sweet spot: Hobby/Pro/Scale apps that
// deploy 5-10x/day hit ~30 days of history before the trim
// engages; longer deployments (multi-app monorepos) still keep
// enough surface to debug a stale row. The const is exported so
// the migration docblock, the type doc, and the trim sites all
// reference the same value.
const MaxStageHistory = 64

// ParkReason is the closed-set label on deployments.parked_reason
// (issue #554 / ADR-079 follow-up, migration 00157). The schema
// CHECK constraint deployments_parked_reason_check enforces the
// same vocabulary at the storage layer; this Go type exists so
// callers (engine.ParkDeployment, future admin parkApp handler)
// fail fast at the API boundary instead of surfacing a Postgres
// 23514 at runtime via a silent warn-log.
type ParkReason string

const (
	// ParkReasonLivenessExhausted is stamped when the liveness
	// window trips (issue #554 / ADR-079) — the only path that
	// emits today. The audit row is the durable source of
	// truth; this is the per-deployment column projection.
	ParkReasonLivenessExhausted ParkReason = "liveness_exhausted"
	// ParkReasonLifecyclePark is stamped by the admin
	// parkApp handler (cmd/apid/handlers_ext.go) when an
	// operator stops traffic on a deployment outside the
	// liveness window. Reserved vocabulary — wire lands in a
	// follow-up if observed.
	ParkReasonLifecyclePark ParkReason = "lifecycle_park"
	// ParkReasonAdminPark is reserved for an operator-driven
	// compliance-hold path. Not wired yet; the schema CHECK
	// accepts it so a future handler can stamp it without a
	// migration.
	ParkReasonAdminPark ParkReason = "admin_park"
)

// IsValidParkReason reports whether r is one of the closed-set
// ParkReason constants. Cheap — used by Engine.ParkDeployment
// and any future writer to fail fast on a stray value before
// the SQL UPDATE surfaces a 23514.
func (r ParkReason) IsValid() bool {
	switch r {
	case ParkReasonLivenessExhausted, ParkReasonLifecyclePark, ParkReasonAdminPark:
		return true
	default:
		return false
	}
}

// AutoRollbackReason tags the trigger that fired the most recent
// auto-rollback (Mega-C PR-2 / issue #961 leaf 8). Closed-set
// vocabulary enforced at the schema layer via
// deployments_last_auto_rollback_reason_check (migration 00315).
type AutoRollbackReason string

const (
	// AutoRollbackReasonThresholdExceeded is stamped when schedd's
	// per-deploy 5xx counter crosses the plan threshold inside the
	// first wake window (5 min by default; see
	// pkg/api/limits.go::RollbackOn5xxWindowMinutes).
	AutoRollbackReasonThresholdExceeded AutoRollbackReason = "threshold_exceeded"
	// AutoRollbackReasonFirstWindowExpired is reserved for a
	// future "deploy landed but the first wake never arrived"
	// fallback that supersedes the deploy before its window
	// closes. Not wired yet; the schema CHECK accepts it so the
	// caller can stamp it without a migration.
	AutoRollbackReasonFirstWindowExpired AutoRollbackReason = "first_window_expired"
)

// IsValidAutoRollbackReason reports whether r is one of the
// closed-set AutoRollbackReason constants. Mirrors
// ParkReason.IsValid. Used by the schedd emit path to fail fast
// on a stray value before the SQL UPDATE surfaces a 23514.
func (r AutoRollbackReason) IsValid() bool {
	switch r {
	case AutoRollbackReasonThresholdExceeded, AutoRollbackReasonFirstWindowExpired:
		return true
	default:
		return false
	}
}

// BuildStatus tracks the build row's lifecycle (spec §9).
type BuildStatus string

const (
	BuildQueued    BuildStatus = "queued"
	BuildRunning   BuildStatus = "running"
	BuildSucceeded BuildStatus = "succeeded"
	BuildFailed    BuildStatus = "failed"
	// BuildCancelled is the terminal exit set by the builderd
	// cancel-LISTEN goroutine (ADR-124). It is reachable both via
	// the CancelDeploymentTx deployment-driven cascade
	// (`cancelled_by_deployment_cascade=true`) and via a future
	// direct build-cancel path that flips the boolean to false.
	// The closed-set schema CHECK constraint migrations/00361
	// widens to accept this value.
	BuildCancelled BuildStatus = "cancelled"
)

// CancelReason is the closed-set label on
// deployments.cancel_reason (ADR-124, migration 00426). The schema
// CHECK constraint deployments_cancel_reason_check enforces the
// same vocabulary at the storage layer; this Go type exists so
// callers fail fast at the API boundary instead of surfacing a
// Postgres 23514 at runtime.
type CancelReason string

const (
	// CancelReasonUser is stamped by the user-initiated
	// POST /v1/apps/{slug}/deployments/{id}/cancel route. Most
	// common path; the CLI's --reason flag can override.
	CancelReasonUser CancelReason = "user"
	// CancelReasonAutoQuota is reserved for the future
	// "auto-cancel on quota breach" path. CHECK-only.
	CancelReasonAutoQuota CancelReason = "auto_quota"
	// CancelReasonAutoHealth is reserved for the future
	// "auto-cancel on liveness exhaustion" path. CHECK-only.
	CancelReasonAutoHealth CancelReason = "auto_health"
	// CancelReasonSystem is the operator-driven escape hatch
	// (admin CLI / control-plane janitor).
	CancelReasonSystem CancelReason = "system"
)

// IsValid reports whether r is one of the closed-set CancelReason
// constants. Cheap — used by apid handlers to fail fast on a
// stray value before the SQL UPDATE surfaces a 23514.
func (r CancelReason) IsValid() bool {
	switch r {
	case CancelReasonUser, CancelReasonAutoQuota, CancelReasonAutoHealth, CancelReasonSystem:
		return true
	}
	return false
}

// FailureClass tags the cause of a build failure (spec §9).
type FailureClass string

const (
	FailureOOM       FailureClass = "oom"
	FailureTimeout   FailureClass = "timeout"
	FailureUserError FailureClass = "user_error"
	FailureInfra     FailureClass = "infra"
)

// CancelReason is the closed-set label on
// deployments.cancel_reason (ADR-124, migration 00360). Mirrors
// the ParkReason precedent at :155-194 — the schema CHECK
// constraint deployments_cancel_reason_check enforces the same
// vocabulary at the storage layer; this Go type exists so
// callers fail fast at the API boundary instead of surfacing a
// Postgres 23514 at runtime. (kept as comment-only; declarations live earlier in this file from the ADR-124 cherry-pick)

// Account is a customer account.
type Account struct {
	ID     string
	Email  string
	Plan   api.Plan
	Status AccountStatus
	// ProviderCustomerID is the per-account `cus_…` returned by Stripe when
	// the customer signs up (spec §4.7). The unique index makes it a
	// stable webhook lookup key.
	ProviderCustomerID string
	// StripeSubscriptionItem is the per-account `si_…` (metered
	// subscription item) that meterd pushes hourly usage against
	// (issue #52, §4.7). Empty until pkg/billing/stripe::EnsureCustomer
	// receives the customer.subscription.created webhook and stamps it.
	// PushUsageRecord skips when this is blank so a customer that hasn't
	// subscribed yet never lands on the billing dashboard.
	StripeSubscriptionItem string
	CreatedAt              time.Time
	// DeletionRequestedAt is stamped when the customer schedules the
	// account for deletion (G6, ADR-021). NULL on every row that has
	// never been scheduled. pkg/grace uses it to decide whether the
	// 30-day grace window has lapsed and a hard delete should run.
	DeletionRequestedAt *time.Time
	// LastQuotaWarningAt is the UTC day (midnight-truncated timestamptz)
	// the meterd quota loop last emitted a `quota_warning` pg_notify for
	// this account (spec §4.7). The dedupe gate at quota.go reads +
	// stamps this column atomically so a paid-tier overage produces
	// exactly one warning event per UTC day — across daemon restarts.
	// NULL on every row that has never tripped.
	LastQuotaWarningAt *time.Time
	// PastDueAt is the moment the account entered `past_due` (set by
	// the apid invoice.payment_failed webhook). pkg/meter.Dunning uses
	// it as the anchor for the 7-day past_due → suspended and 21-day
	// suspended → deleted_pending transitions. NULL on accounts that
	// have never been past_due.
	PastDueAt *time.Time
	// MFAEnrolledAt is stamped by /v1/account/mfa/confirm on the first
	// successful TOTP verification. NULL = never enrolled. The gate
	// the login handlers check is (MFARequired && MFAEnrolledAt == nil)
	// — issued as an mfa_pending session cookie. See ADR-035 and the
	// IAM-2 plan in issue #186.
	MFAEnrolledAt *time.Time
	// MFASecretEncrypted is the age-sealed base32 TOTP secret produced
	// by pkg/auth/totp.GenerateSecret and sealed in pkg/secretbox
	// (same host age key as app_secrets). The plaintext never enters
	// logs or audit; the envelope is decrypted only inside the verify
	// handler. NULL when MFAEnrolledAt is NULL. CHECK constraint
	// accounts_mfa_enrolled_shape_chk enforces the (enrolled ⇒
	// secret+recovery present) shape at the DB layer.
	MFASecretEncrypted []byte
	// MFARecoveryCodesHash is the array of SHA-256 hashes of the
	// customer's 10 single-use recovery codes. Consumed (and removed)
	// by /v1/account/mfa/recover via SELECT FOR UPDATE + UPDATE,
	// because Postgres bytea[] has no array-diffing write. Stored as
	// bytea[] so the consume path is a single-row serialised update.
	MFARecoveryCodesHash [][]byte
	// MFARequired is an explicit policy flag. It is separate from
	// MFAEnrolledAt because "policy says enroll" and "customer has
	// enrolled" are different states. Customer lifecycle events do not
	// set this flag; MFA enrollment is opt-in. API keys ignore this
	// column per the IAM-2 design decision (keys are already
	// cryptographically scoped).
	MFARequired bool
	// KeyGraceWindowDays overrides the plan-default API-key rotation
	// grace window (issue #189 / IAM-5, default 7 days). NIL falls
	// through to the plan default; 0 forces atomic rotation (the old
	// key is revoked the moment the new one is minted); a positive
	// value sets the grace explicitly. The auth hot path does NOT
	// read this column — only the rotation handler does, via a
	// short-TTL in-process cache (cmd/apid.graceWindowCache). The
	// CHECK constraint accounts_key_grace_window_days_check enforces
	// the (NULL or >= 0) shape at the DB layer.
	KeyGraceWindowDays *int
	// EgressAllowlistExtra is the per-account additive budget on top
	// of the plan's apps.egress_allowlist cap (issue #679 / PR-B /
	// ADR-082). 0 = no override; the plan cap (Pro 16 / Scale 64 /
	// Free,Hobby 0) is authoritative. Positive values widen the
	// effective cap for THIS account's apps by the given amount,
	// subject to the apid-layer ceiling of
	// api.MaxAccountEgressAllowlistExtra (1024). The operator-bundle
	// axis (PR-A / ADR-081) is a separate additive layer that merges
	// at the vmmd side; this field is consumed at the apid
	// validator. The DB CHECK constraint
	// accounts.egress_allowlist_extra (>= 0) is the wire-bypass
	// backstop; the apid validator is the soft cap.
	EgressAllowlistExtra int
}

// Active reports whether the account may deploy (not suspended/deleted).
func (a Account) Active() bool { return a.Status == AccountActive || a.Status == AccountPastDue }

// MFAEnrolled reports whether the customer has at least one
// successful TOTP confirmation. An enrolled customer has opted in
// and is challenged on each new dashboard session. MFARequired is
// reserved for an explicit policy that can also require first-time
// enrollment from an otherwise unenrolled account.
func (a Account) MFAEnrolled() bool { return a.MFAEnrolledAt != nil }

// APIKey is a hashed, account-scoped credential. Scopes is the set of
// authorization scopes attached to the key (e.g. "admin", "read", "write");
// the apid middleware checks them on every authenticated request. See
// ADR-034 and the IAM-1 plan.
//
// The trailing four fields are the IAM-5 (issue #189) surface:
//
//   - ExpiresAt: nullable; nil = never expires (legacy admin keys +
//     the additive migration's promise to existing rows). The auth
//     path enforces the gate in state.AuthenticateKey, not via a DB
//     constraint. New non-admin keys receive `now() + 365 days` at
//     creation; admin keys stay nullable per the existing contract.
//   - Status: APIKeyStatus = 'active' | 'grace' | 'revoked'. The
//     state machine is enforced by the store (AuthenticateKey,
//     MarkAPIKeyRevoked, RotateAPIKey); the SQL CHECK is the floor.
//     See APIKeyStatus for the full contract.
//   - RevokedAt: terminal timestamp, set on explicit revoke, atomic
//     rotation, or lazy expiry. The lazy path is one UPDATE per
//     expired key on the first auth attempt that observes it
//     (state.AuthenticateKey), not a background sweeper.
//   - RotatedFromID: FK to the predecessor on the new key after
//     rotation. nil on the original key. The reverse direction
//     (which new key replaced this one) is a one-step walk at the
//     call site — there is no rotated_to_id column to keep the
//     key row immutable post-rotation.
//
// APIKeyStatus is the state machine for an API key (issue #189 /
// IAM-5). Three states; the CHECK constraint in migration 00106
// (`api_keys_status_check`) is the SQL floor, the store methods
// are the wall.
//
//   - Active: ready to authenticate. The default for fresh
//     non-admin keys.
//   - Grace: post-rotation window. The old key still authenticates
//     until its overwritten expires_at.
//   - Revoked: terminal. Lazy expiry (state.AuthenticateKey),
//     explicit DELETE, and atomic rotation all flip here.
type APIKeyStatus string

const (
	APIKeyStatusActive  APIKeyStatus = "active"
	APIKeyStatusGrace   APIKeyStatus = "grace"
	APIKeyStatusRevoked APIKeyStatus = "revoked"
)

// ConsumerKey is a hashed, per-(account, app) credential for the
// application's customers (ADR-120 / issue #975 item #5). Distinct
// from APIKey because it is scoped to a single (AccountID, AppID)
// pair (a leaked key affects only one app) and exposed to the
// public internet (every customer of the app sees one — hence 2×
// the entropy at mint time, see pkg/api/apikey.go).
//
// Wire format `ck_<8-hex-prefix>_<64-hex-secret>`. The plaintext is
// shown to the operator exactly once at mint; only Hash is stored.
// Prefix is the human-shareable portion used by the
// (app_id, prefix) hot-path index; the store narrows to one row
// before the hash compare.
//
// RevokedAt is a terminal timestamp; expiry is a soft gate enforced
// at the read path (cmd/gatewayd-internal/auth_consumer.go — PR
// #5-C). LastUsedAt is best-effort observability updated via
// TouchConsumerKeyLastUsed with a 60s debouncer — never a billing
// signal.
type ConsumerKey struct {
	ID         string
	AccountID  string
	AppID      string
	Name       string
	Prefix     string
	Hash       []byte
	Scopes     []string
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// Active reports whether the key is in an authentication-eligible
// state (not revoked, not expired). The gatewayd-internal
// middleware reads this on every inbound request.
func (k ConsumerKey) Active(now time.Time) bool {
	if k.RevokedAt != nil {
		return false
	}
	if k.ExpiresAt != nil && !k.ExpiresAt.After(now) {
		return false
	}
	return true
}

type APIKey struct {
	ID        string
	AccountID string
	// OrgID is the org the key was minted against (issue #190 / IAM-6,
	// PR 6). Migration 00127 flips api_keys.org_id from NULL to
	// NOT NULL after the deterministic personal-org backfill, so every
	// row carries a non-empty string. The PR 7 (schedd/meterd/gatewayd-internal
	// cutover) bump to AuthenticateKey's signature will thread this
	// into admission decisions; PR 6 only adds the field so the
	// Store/handler/auth triple don't need a coordinated rename.
	OrgID         string
	Hash          []byte
	Label         string
	Scopes        []string
	LastUsedAt    time.Time
	CreatedAt     time.Time
	ExpiresAt     *time.Time
	Status        string
	RevokedAt     *time.Time
	RotatedFromID *string
	// CreatedIP is the best-effort client IP at mint time, captured
	// by clientIPFromRequest on the minting request. NULL for pre-PR
	// rows and for the unix-socket code path (no meaningful client IP).
	// Stored as a string (Postgres inet renders to a textual form on
	// scan) so the handler-side sanitiser is the same as the audit
	// payload path.
	CreatedIP string
	// CreatedUA is the best-effort User-Agent at mint time, captured
	// from r.UserAgent() and run through logsanitize.Field before
	// reaching the DB. NULL for pre-PR rows.
	CreatedUA string
	// ParentKeyID is the optional FK to the predecessor key. Distinct
	// from RotatedFromID: RotatedFromID is the rotation-internal
	// "this key was minted by a rotation" stamp, set by the Store layer
	// on every rotate. ParentKeyID is the explicit provenance lineage
	// (e.g. a non-rotation mint that wants to record a parent in a
	// future PR). ON DELETE SET NULL — a hard-deleted predecessor
	// leaves the lineage intact but un-anchored.
	ParentKeyID *string
}

// App is a deployed application (or function). The Manifest carries the
// runner-scaffold payload (env, healthz path, entrypoint) the guest-init
// consumes inside the microVM (spec §4.6, §4.9).
type App struct {
	ID             string
	AccountID      string
	Slug           string
	Type           AppType
	Runtime        string // node22|python312|go124|go124-alpine|node24|python313 for functions
	RAMMB          int
	IdleTimeoutS   int // 0 => plan default
	MaxConcurrency int
	// MinInstances is the per-app floor the reaper honors when parking
	// idle instances (ux_spec §6.5). 0 => scale to zero (default);
	// >0 => keep at least this many RUNNING instances alive regardless
	// of idle timeout. Pro/Scale only — the apid updateApp handler
	// rejects Hobby/Free with 403 plan_min_instances_not_allowed.
	MinInstances int
	// EgressAllowlist is the per-app outbound CIDR allowlist (ADR-031,
	// tier-2 of the network roadmap). Empty => no allowlist rule
	// emitted, current behaviour preserved; non-empty => the per-netns
	// forward chain gains an `iifname tap0 ip daddr { … } accept`
	// rule after the lateral-movement deny. v4 only in v1 (the v6
	// mirror is a separate ADR). Plan-gated: Free/Hobby always read
	// empty (apid updateApp rejects PATCH with 403
	// plan_egress_allowlist_not_allowed); Pro max 16 entries; Scale
	// max 64 entries — see pkg/api/limits.go.
	EgressAllowlist []netip.Prefix
	// StaticEgressIP (ADR-119) is the customer-supplied IPv4 that
	// the app's egress traffic presents on the wire. NULL => no
	// static IP — egress exits with the host's primary IP (the
	// default behaviour). Non-nil => the host bridge aliases the IP
	// and a per-host postrouting MASQUERADE sibling rewrites
	// matching tenant source traffic to this IP. Plan-gated:
	// Free/Hobby/Pro always read nil (apid rejects PATCH with 402
	// plan_static_egress_ip_not_allowed); Scale quota = 1 per app
	// (Limits.StaticEgressIPsPerApp). The DB-side
	// apps_static_egress_ip_key partial unique index defends
	// against two apps on the same account pinning the same IP
	// (alias-IP collision on br-tenants). IPv4 only in v1.
	StaticEgressIP *netip.Addr
	// StaticEgressIPSetAt is the audit stamp for when the customer
	// pinned the IP. Nullable — NULL when StaticEgressIP is NULL
	// (no pin). Stamped on every non-null write by pgstore.go's
	// UpdateApp CASE branch.
	StaticEgressIPSetAt *time.Time

	// PublicAuthIPAllowlist (ADR-118) is the per-app ingress CIDR
	// allowlist consulted at the request layer by
	// pkg/gateway/handler.go::applyIngressIPAllowlist (runs before
	// applyEdgeRuleIP, before wake). Empty => no rule (current
	// behaviour preserved); non-empty with mode='ip_allowlist' =>
	// every public request must originate from a client IP inside
	// the allowlist, otherwise 403. Plan-gated to Pro/Scale; Free/Hobby
	// always read empty (apid rejects with 403
	// plan_public_auth_ip_allowlist_not_allowed). Pro max 16
	// entries; Scale max 64 entries — same ladder as EgressAllowlist.
	PublicAuthIPAllowlist []netip.Prefix
	// AutoscaleTargetRPS is the per-instance RPS target for the
	// reactive scale-up trigger (issue #169 / #172). 0 means
	// "disabled" — the trigger skips this app. Plan-gated upstream
	// (Free returns 403 plan_autoscale_not_allowed); apid enforces
	// value > 0 in [1, max-int]. Hobby/Pro/Scale only. When
	// measured RPS / live_instance_count exceeds this, schedd admits
	// another instance up to plan.MaxConcurrency.
	AutoscaleTargetRPS int
	// AutoscaleTargetCPUPct is the per-instance CPU% target (1..100)
	// for the scale-up trigger. 0 means "disabled". Pro/Scale only
	// (the plan gate is stricter than RPS — Hobby does not get CPU
	// because the cost shape is unbounded on the cheaper tiers).
	// Signal source is pkg/sched/instancestats.Reader (PR #205); a
	// nil reader falls back to RPS-only mode and this target is
	// silently skipped.
	AutoscaleTargetCPUPct int
	Status                AppStatus
	// ProjectID is the parent project for apps that came from a
	// multi-workload repo (ADR-050). Empty for standalone apps; the
	// apps.project_id column is nullable. ON DELETE SET NULL so a
	// project's hard delete (Phase 5) orphans the app rather than
	// cascading it.
	ProjectID string
	// RootDir is the build context relative to the repo root
	// ("" = repo root). Defaults to '' so standalone apps keep the
	// pre-projects deploy contract bit-for-bit.
	RootDir string
	// WorkloadName is the workload's name within the project
	// (unique under (project_id, workload_name) per migration 00073).
	// Empty for standalone apps.
	WorkloadName string
	// WorkloadClass is the per-app shape classification. Phase 1
	// (ADR-050) stamps the value as a scan hint from the repo; Phase
	// 4 (ADR-051) re-derives the authoritative class via the probe
	// boot. The schema CHECK (apps_workload_class_chk) admits
	// http|graphql|grpc|job|worker only; the Go zero-value is "" and
	// is intentionally NOT in that set — code that persists an App
	// must set WorkloadClassHTTP (or another canonical value)
	// explicitly before CreateApp.
	WorkloadClass WorkloadClass
	// StreamingEnabled toggles the per-app streaming response path
	// through gatewayd-internal (issue #471 / ADR-047). When true, gatewayd-internal
	// streams response body chunks instance → gateway → client with
	// a periodic 200 ms / 256 KiB tx_bytes flush so ADR-046 metering
	// stays accurate. Plan-gated upstream: Free defaults to false and
	// cannot PATCH it to true (apid returns 403 plan_streaming_not_allowed);
	// Hobby/Pro/Scale default to true. The buffered path remains the
	// v1 contract — a Free app with StreamingEnabled=false keeps the
	// legacy 25 MB / 300 s envelope (spec §4.1).
	StreamingEnabled bool
	// WebSocketEnabled (issue #676 / ADR-080) toggles the per-app
	// raw-bytes Upgrade bridge. When true, gatewayd-internal's
	// Upgrade detector routes inbound Connection: Upgrade +
	// Upgrade: <token> requests to rawStreamReverseProxy (which
	// opens the ForwardRawStream RPC and pumps raw bytes into the
	// guest's netns TCP socket). Mirrors StreamingEnabled's
	// plan-gating contract: Free defaults to false and cannot
	// PATCH it to true (apid returns 403
	// plan_websocket_not_allowed); Hobby/Pro/Scale default to true.
	//
	// Per-session caps (issue #676 / ADR-080 PR-C):
	//   - Inbound bytes capped at api.RawStreamMaxRequestBytes
	//     (100 MiB) at pkg/vmmdgrpc/forward.go:rawBridgeBodyLoop.
	//     Exceeding it returns ResourceExhausted → gateway 502.
	//   - Outbound bytes capped at api.RawStreamMaxResponseBytes
	//     (1 GiB, configured via the existing init-frame
	//     max_request_bytes proto field — a dedicated
	//     max_response_bytes wire field is a deferred ADR) at
	//     pkg/vmmdgrpc/forward.go:rawBridgePumpBody. Exceeding
	//     it returns ResourceExhausted → gateway 502.
	//   - Outbound bytes the gateway forwards to the client
	//     (body_chunks + init-error fallbacks) flow into the
	//     per-instance egress ring via
	//     pkg/gateway/forwardproxy.go's
	//     rawStreamOnceWithEvents — see pkg/gateway/handler.go
	//     WithEgressSink + pkg/gateway/egresssink. They
	//     populate usage_minutes.tx_bytes alongside plain HTTP
	//     chunk writes (ADR-046).
	WebSocketEnabled bool
	// RouteMetricsEnabled (ADR-093) opts the app into the per-route
	// observability surface. Mirrors WebSocketEnabled's plan-gating
	// contract: Free defaults to false and cannot PATCH it to true
	// (apid returns 403 plan_route_metrics_not_allowed); Hobby/Pro/
	// Scale default to true. The per-app route cap (50) +
	// __route_other__ overflow bound the cardinality regardless of
	// the customer's traffic shape.
	RouteMetricsEnabled bool
	// AppProtocol (ADR-124) is the per-app wire-protocol
	// selector stored on the apps row as text NOT NULL DEFAULT
	// 'http1'. Closed set {http1, http2, grpc} enforced by
	// apps_app_protocol_chk (migration 00382). Plan gate for
	// 'grpc' is at the apid boundary (Plan.AppProtocolAllowed),
	// not the SQL CHECK — every pre-existing app reads "http1"
	// without a migration. The per-app transport-selection seam
	// (x-faas-protocol header stamp at pkg/gateway/handler.go)
	// reads this field via pgRouter.toApp.
	AppProtocol string
	// MaintenanceMode (ADR-091 amendment, PR-A #???) opts the
	// whole app into 503 + Retry-After mode — the coarse primitive
	// for "every route is in maintenance". When true, the gatewayd
	// applier (pkg/gateway.(*Handler).applyAppsMaintenanceMode,
	// §4.1.2.0) short-circuits every request to this app with
	// 503 + Retry-After: api.EdgeRuleMaintenanceRetryAfterSeconds
	// (60 s) BEFORE auth, BEFORE wake, BEFORE any kind=maintenance
	// rule fires (coarse gate beats fine-grained). The boolean is
	// Free-tier allowed (no IsPaidOnly change); a Free customer
	// may pin their own app for maintenance. The apps_maintenance_
	// mode_notify trigger (migrations/00221_apps_maintenance_mode.
	// sql) fires pg_notify('app_changed', NEW.id) only on a flip so
	// the gatewayd-internal apps LRU cache can be flushed without
	// waking up on every unrelated app update.
	MaintenanceMode bool
	// RequireSigned gates OCI image deploys (issue #472 / ADR-054) on
	// a valid cosign signature from a trusted publisher. When true,
	// imaged's buildImageLayer calls pkg/cosign.VerifyImageSignature
	// before pulling the OCI manifest and rejects (FAILED +
	// failure_reason=signature_missing|signature_invalid) if no
	// signer in app_trusted_signers matches. Source-tarball deploys
	// (Railpack path) bypass the gate — they never touch a customer
	// OCI image. Default false: pre-existing apps stay on the
	// open-deploy path; operator opt-in via PATCH /v1/apps/{slug}
	// (admin-scoped). Operators MUST add at least one trusted signer
	// before flipping the flag — empty trust list + require_signed=true
	// is fail-closed (every deploy is rejected).
	RequireSigned bool
	// StartCommand overrides the OCI image's CMD when present.
	// Phase 3 writes it from compose/Procfile declarations; Phase
	// 1 carries the column through but the apid handler does not
	// yet set it. Nullable text in SQL; the empty-string-means-NULL
	// convention is enforced by `nullString` on writes and `coalesce`
	// on reads (same shape as Runtime).
	StartCommand string
	Manifest     AppManifest
	// ScalingPolicy is the per-app autoscaling configuration
	// (issue #462 / ADR-058). The on-disk shape is jsonb
	// (`apps.scaling_policy`); the in-memory field is the
	// canonical source for new writes. Legacy rows read back
	// through the empty-policy projection path: nil pointer +
	// zero-value `min_instances` / `max_concurrency` = scale to
	// zero, the pre-#462 contract. See apid's appResponse for
	// the projection logic.
	ScalingPolicy *ScalingPolicy
	// LastScaleOutAt is the wall-clock time of the most recent
	// scale-out event schedd admitted for this app (issue #462 /
	// ADR-058). Used by the wake-gate cooldown helper
	// (`pkg/sched/engine.go::admitGate`) to short-circuit requests
	// that hit the `ScaleOutCooldownS` window. Nullable:
	// schedd stamps this on the admit branch (same Tx as the
	// `instances` row insert), so a NULL means "never scaled
	// out this app". Same shape for LastScaleInAt, stamped on the
	// reaper park branch.
	LastScaleOutAt *time.Time
	LastScaleInAt  *time.Time
	// NodeID is the durable shard key that ties an app to its
	// owner compute_node (Phase 2 / Gate A, migration 00090).
	// Set once at CreateApp time by apid's PlacementScheduler
	// and immutable post-create — except via the Tier A4
	// reassignment path (Store.ReassignAppOwner), which the
	// pkg/sched/rebalancer.go watcher triggers when the owning
	// compute_node flips active=false. Every schedd gRPC handler
	// enforces the owner match via pkg/scheddgrpc.authorizeApp /
	// authorizeInstance; a non-owner schedd returns
	// codes.FailedPrecondition rather than silently mutating
	// state. The synthetic default-local row's id is the
	// backfill target for every pre-Phase-2 row, so single-box
	// installs preserve bit-for-bit behaviour.
	NodeID string
	// OverflowNode is the customer's per-app preferred spill
	// target (Tier A10, ADR-088, migration 00167). When set, the
	// Tier A9 capacity-pressure rebalancer consults it BEFORE
	// falling back to the first-peer-with-headroom selection.
	// Nullable: a NULL app is the "no preference" default — the
	// engine behaves exactly like A9 (random first peer with
	// headroom, sorted by name ASC). UNSET-uuid is rejected by
	// apps_overflow_node_chk (migration 00167) as a tripwire
	// against buggy INSERT paths. The wire field is the
	// human-readable compute_nodes.name; apid resolves the name
	// to the UUID server-side via Store.ComputeNodeByName
	// (cmd/apid/compute_nodes.go:250). FK cascades ON DELETE SET
	// NULL — draining a node clears the preference, never
	// orphans it.
	OverflowNode *string
	// ReassignedAt is the wall-clock time of the most recent
	// successful cross-node reassignment (Tier A4, migration
	// 00095). Stamped by Store.ReassignAppOwner in the same
	// UPDATE that flips apps.node_id. The rebalancer's hot
	// filter is `reassigned_at IS NULL OR reassigned_at <
	// now() - interval '<cooldown>s'`, so a fresh
	// reassignment suppresses further moves for at least
	// RebalanceCooldownSeconds (default 60s, env-overridable
	// via FAAS_REBALANCE_COOLDOWN_SECONDS; the constant lives
	// in pkg/api/limits.go). Nullable: a NULL row is the
	// never-reassigned case, always eligible for the first
	// drain event.
	ReassignedAt *time.Time
	// MigratedAt is the wall-clock time of the most recent
	// cross-node LIVE-INSTANCE migration for this app
	// (Tier A5, migration 00098, ADR-068, follow-up to
	// ADR-064). Distinct from ReassignedAt, which carries the
	// PARKED-app rebalance commit. Both columns can coexist
	// on the same app — an app whose instances migrated live
	// last week AND whose owner was rebalanced to a new node
	// via the A4 parked path yesterday has both stamps set.
	// Nullable: a fresh app has never been migrated. The
	// clock-skew CHECK tolerates `migrated_at <= now() +
	// interval '1 minute'`; values clearly in the future
	// still error loud (23514). Stamped by
	// Store.MigrateInstanceOwner in the same UPDATE that
	// flips instances.node_id. Telemetry only — the A5
	// rebalancer's hot filter is on instances.state, not on
	// apps.migrated_at.
	MigratedAt *time.Time
	// WarmSnapshotEnabled (issue #470 / ADR-055) toggles the
	// two-tier snapshot path (init.snap + warm.snap). When true
	// and the customer's plan is Pro or Scale, schedd captures a
	// second snapshot after the framework listener reports ready,
	// and the wake-path prefers warm.snap on restore. The column
	// is per-app (operators may PATCH it via /v1/apps/{slug}); the
	// plan-gated default lives in pkg/api/limits.go
	// (WarmSnapshotDefault).
	WarmSnapshotEnabled bool
	// RequireAuthn (issue #560) toggles per-deployment
	// authentication. When true, gatewayd-internal demands a valid
	// Authorization: Bearer <token> header on every routed request;
	// the token must belong to the app's owning account
	// (cross-account tokens receive 403). Default false: every
	// pre-existing app stays public-by-default. Plan-gated at
	// PATCH time (Pro/Scale only — Free/Hobby get 403
	// plan_require_authn_not_allowed); the per-plan default is
	// always false (the column default + the create-time default
	// keep every new app public). Operators may PATCH true → false
	// on any plan to opt out per-app.
	RequireAuthn bool
	// PublicAuthMode (issue #477 / ADR-079) is the per-app
	// public-URL auth mode ('open'|'bearer'|'basic'). When
	// 'open' (the default — every pre-existing app stays
	// public-by-default), gatewayd-internal pass-throughs
	// anonymous traffic. When 'bearer', gatewayd-internal
	// demands an Authorization: Bearer header (re-using the
	// require_authn key chain). When 'basic', gatewayd-internal
	// demands an Authorization: Basic header and verifies
	// against the secretbox-sealed PublicAuthBasicSealed
	// blob. Plan-gated at PATCH time (open=all, bearer=Hobby+,
	// basic=Pro+); the per-plan default is always 'open'.
	PublicAuthMode string
	// PublicAuthBasicSealed (issue #477 / ADR-079) is the
	// secretbox-sealed APP_BASIC_AUTH blob carrying the
	// {username, password} pair the basic-auth path verifies
	// against. Nil/empty for open/bearer modes; set ONLY when
	// PublicAuthMode='basic'. gatewayd-internal unseals it
	// at boot (and caches the unsealed form for 60s +
	// db.NotifyKeyChanged invalidation) so the secretbox
	// hot-path doesn't run on every request.
	PublicAuthBasicSealed []byte
	// AuthDefaultFlippedAt (issue #695 / ADR-080) is the
	// grandfather marker for the apps-auth-default flip. Stamped
	// on every pre-flip app by migration 00155 so the dashboard
	// banner query + `faas apps list` annotation can render the
	// "since YYYY-MM-DD" suffix on grandfathered rows. NULL on
	// apps created AFTER the migration (no grandfather needed;
	// the default is already applied at create-time via
	// plan.RequireAuthnDefault() + plan.PublicAuthModeDefault()
	// in apid's buildApp path). Read-only at the wire surface —
	// the PATCH path never writes this column. A future
	// contributor adding a PATCH path that writes it must refuse
	// the field with 422 unprocessable_entity per ADR-080 §9.
	AuthDefaultFlippedAt *time.Time
	// WarmSnapshotMinRequests is the minimum successful request
	// count before schedd promotes a warm-tier capture. Range
	// [1, 100], default 5. Lowering this shortens the time to
	// first warm.snap but risks capturing mid-warmup states; the
	// range bound is enforced both at the SQL CHECK layer and at
	// the apid handler so a bad PATCH can't degrade the box.
	WarmSnapshotMinRequests int
	// WarmSnapshotMinMs is the minimum time-since-first-ready in
	// milliseconds before schedd promotes a warm-tier capture.
	// Range [100, 60000], default 2000. The 100 ms floor blocks
	// capturing too early (before JIT/AOT has a chance to fire);
	// the 60 s ceiling bounds the per-park latency cost the warm
	// capture adds to the cold path (R1 in the plan).
	WarmSnapshotMinMs int
	// EvictionPriority (issue #475) classifies the app under
	// cross-account RAM pressure (spec §4.3 / §6.2-2). 'best_effort'
	// (default for every existing app) keeps the pre-#475
	// LRU-by-last_request_at reaper behaviour bit-for-bit; 'reserved'
	// still obeys idle / per-account / per-app caps but is protected
	// from cross-account RAM-pressure eviction — every best_effort
	// candidate is drained before any reserved is parked. Not
	// Lambda-style provisioned concurrency: a reserved app does NOT
	// keep instances resident (ADR-005 — cold boot must always work).
	// Plan-gated upstream in apid via
	// api.Plan.EvictionPriorityReservedAllowed(); the column CHECK
	// (apps_eviction_priority_chk) is the data-integrity backstop.
	EvictionPriority string
	// PreviewOfSlug names the parent (production) app this row is
	// a PR preview of. Empty for production apps. Non-empty for
	// preview rows provisioned by githubd's pull_request handler
	// (issue #272 / ADR-094). The slug pair (preview_of_slug,
	// preview_pr_number) is the natural lookup key for the
	// dashboard's "preview environments" pane and for the teardown
	// janitor's nightly scan. Nullable text in SQL; the empty-
	// string-means-NULL convention matches Runtime / StartCommand.
	PreviewOfSlug string
	// PreviewPrNumber is the GitHub PR number this preview row
	// tracks. Stable across synchronize/reopened events on the
	// same PR; the slug is `pr-{N}-{parent_slug}`. Zero on
	// production apps.
	PreviewPrNumber int
	// PreviewPrState is the closed-set lifecycle label on a
	// preview row. NULL on production apps. The
	// closed → stale → torn_down transitions are driven by the
	// cmd/schedd/janitor_preview.go loop (PR-C). Values:
	//
	//   - "open"     : PR is open on GitHub; preview serves traffic.
	//   - "closed"   : PR was closed within the last 24h; preview
	//                  still serves traffic, allowing a quick
	//                  reopen-replay to land without a fresh build.
	//   - "stale"    : PR was closed more than 24h ago OR has
	//                  expired (preview_expires_at < now()).
	//                  Preview refuses new wakes (410 Gone).
	//   - "torn_down": Teardown complete; the apps row is
	//                  tombstoned (deleted_at set). The slug is
	//                  free for reuse.
	PreviewPrState string
	// PreviewExpiresAt is the wall-clock time the teardown
	// janitor should reap the preview, regardless of GitHub state.
	// Computed at provision time as created_at + 7 days; the
	// dashboard's preview panel surfaces the expiry to the
	// customer so they can pin a preview they want to keep. NULL
	// on production apps.
	PreviewExpiresAt *time.Time
	// PreviewDestroyCommentedAt is the dedupe carrier for the
	// one-click PR comment destroy surface (Mega-C PR-1 / issue
	// #961 leaf 3). githubd's previewCommentOnce writes now() to
	// this column after a successful POST to
	// api.github.com/repos/{owner}/{repo}/issues/{pr_number}/comments;
	// subsequent events for the same (app, PR) tuple skip the
	// post. NULL on rows where the dispatcher has never
	// commented (the common case for production apps and for
	// previews provisioned before this migration landed).
	PreviewDestroyCommentedAt *time.Time
	// CORSDefaultEnabled is the per-app default CORS opt-in
	// (ADR-091 CORS improvements D1 / spec §4.1.2.6). When
	// false (the default for every pre-PR app), the gateway
	// applies no default CORS and the "no rule → no CORS"
	// contract is preserved unchanged. When true, the gateway
	// consults CORSDefaultOrigins for every request that
	// misses a kind=cors edge rule and stamps
	// Access-Control-Allow-Origin + Allow-Methods +
	// Allow-Headers on the response. The OPTIONS
	// short-circuit is intentionally SKIPPED on the default
	// path so the customer's backend remains the authority
	// on the preflight answer. Free on every plan; no
	// plan gate (per-issue #561 framing — the fallback is
	// the "just allow my origin" surface that customers
	// expect from any FaaS).
	//
	// Pointer (not plain bool) because the wire
	// DISTINGUISHES "schema default false" (legacy rows,
	// pre-PR apps) from "explicit PATCH false"
	// (customer opted out after enabling once). With a
	// plain bool both project identically, and the
	// customer-facing "did I ever turn this on?" question
	// becomes unanswerable on the wire. nil = never set /
	// schema default; *true = opt-in; *false = explicit
	// opt-out. The pgstore layer hydrates legacy rows as
	// *false so the wire shape collapses schema-default and
	// opt-out to the same wire value (false) — the
	// three-way distinction lives only on the write path.
	CORSDefaultEnabled *bool
	// CORSDefaultOrigins is the per-app default CORS
	// allowlist. Same string shape as
	// edge_rules_cors.allow_origins; the gateway reuses the
	// matchOrigin matcher verbatim (which is widened in the
	// same PR to accept subdomain/port wildcards). nil and
	// an empty slice are both treated as "deny all" by
	// the gateway. The column is text[] (not jsonb) so the
	// matcher is reused bit-for-bit; see migration
	// 00215_apps_cors_defaults.sql for the rationale.
	CORSDefaultOrigins []string
	CreatedAt          time.Time
}

// EvictionPriorityOrBestEffort (issue #475) snaps the empty Go zero
// to the schema DEFAULT 'best_effort' so the INSERT path never trips
// the CHECK constraint apps_eviction_priority_chk on a missing column.
// The schema DEFAULT would catch this on the wire anyway, but calling
// the snap explicitly preserves the pre-#475 create behaviour
// bit-for-bit and avoids a 23514 transient visible in pgx error logs.
//
// Lives on the state.App type because all three call sites read or
// write App.EvictionPriority:
//
//   - pkg/state/pgstore.go::CreateApp / CreateAppIfUnderQuota stamp
//     the column at INSERT time.
//   - pkg/sched/loop.go::resolvePriority stamps the per-instance
//     carrier at reaper tick (the counter observation depends on the
//     post-stamp label value, not the pre-stamp Go zero).
//
// The helper is intentionally nil-safe on a zero App — passing "" is
// the only "unset" shape the column has.
func EvictionPriorityOrBestEffort(p string) string {
	if p == "" {
		return string(api.EvictionPriorityBestEffort)
	}
	return p
}

// PreviewPrStateOpen / Closed / Stale / TornDown are the four
// closed-set values for state.App.PreviewPrState. Mirrors the
// apps_preview_pr_state_chk CHECK constraint introduced by
// migration 00218 (issue #272 / ADR-094). Empty string means
// "production app, no preview state" — the SQL CHECK allows
// NULL or one of the four values; the Go side represents NULL
// as "" (same convention as EvictionPriorityOrBestEffort).
const (
	PreviewPrStateOpen     = "open"
	PreviewPrStateClosed   = "closed"
	PreviewPrStateStale    = "stale"
	PreviewPrStateTornDown = "torn_down"
)

// PreviewPrStateIsValid reports whether the value is one of
// the four legal preview_pr_state values. Empty string is the
// "production app" shape (preview_pr_state IS NULL) — the SQL
// CHECK allows NULL; the Go side uses "" for that. Callers
// building a new preview App MUST set a non-empty value from
// the closed set above.
func PreviewPrStateIsValid(s string) bool {
	switch s {
	case PreviewPrStateOpen, PreviewPrStateClosed, PreviewPrStateStale, PreviewPrStateTornDown:
		return true
	default:
		return false
	}
}

// ServiceReplicas is the desired-count policy for service-mode deployments.
type ServiceReplicas struct {
	Min     int `json:"min"`
	Max     int `json:"max"`
	Desired int `json:"desired"`
}

// AppManifest is the runner-scaffold and app-owned lifecycle payload. Stored
// as jsonb in Postgres; lifecycle fields are overlaid onto each deployment's
// image manifest before it is written into the snapshot for guest-init.
type AppManifest struct {
	Entrypoint       []string          `json:"entrypoint,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	WorkingDir       string            `json:"working_dir,omitempty"`
	Port             int               `json:"port,omitempty"`
	Healthz          string            `json:"healthz,omitempty"`
	User             string            `json:"user,omitempty"`
	ExecutionMode    string            `json:"execution_mode,omitempty"`
	RestartPolicy    string            `json:"restart_policy,omitempty"`
	StartupDeadlineS int               `json:"startup_deadline_s,omitempty"`
	MaxRetries       int               `json:"max_retries,omitempty"`
	ServiceReplicas  *ServiceReplicas  `json:"service_replicas,omitempty"`
}

// IsZero reports whether the manifest carries no runner or lifecycle fields.
// It keeps the legacy empty-manifest JSON shape while allowing lifecycle-only
// app rows to persist a non-empty contract.
func (m AppManifest) IsZero() bool {
	return m.Entrypoint == nil && m.Env == nil && m.WorkingDir == "" &&
		m.Port == 0 && m.Healthz == "" && m.User == "" &&
		m.ExecutionMode == "" && m.RestartPolicy == "" &&
		m.StartupDeadlineS == 0 && m.MaxRetries == 0 &&
		m.ServiceReplicas == nil
}

// ScalingPolicy is the per-app autoscaling configuration (issue #462 /
// ADR-058). The struct is the canonical in-memory form; the on-disk
// shape is jsonb (column `apps.scaling_policy`) round-tripped through
// MarshalJSON / UnmarshalJSON.
//
// The struct is layered on top of the existing per-app knobs
// (`min_instances`, `max_concurrency`, `autoscale_target_rps`) rather
// than replacing them. The plan is:
//
//  1. PR-A persists the policy + adds the Hobby+ tier-up for
//     min_instances. The struct is the in-memory source of truth for
//     new writes; legacy rows read back through the empty-policy
//     projection (empty struct = "use min_instances / max_concurrency
//     from the existing columns").
//  2. PR-C wires the engine to read the policy and stamps
//     `last_scale_out_at` / `last_scale_in_at` on the admit / park
//     branches. The cooldown fields land here in JSON form because
//     legacy rows default to a sane floor (the engine applies the
//     default on the empty-policy read path).
//  3. PR-D carves out the worker-class branch and adds the financial
//     ADR.
//
// Empty values are equivalent to "policy unset" (the field default
// is the per-app knob's default). The DTO mirrors this with
// pointer-fields so the wire form can distinguish "don't touch" from
// "explicit zero".
type ScalingPolicy struct {
	// MinInstances is the per-app cold-wake floor. Mirrors the
	// existing top-level `min_instances` column; persisted in the
	// jsonb for the new policy surface, but the column-write stays
	// in sync (the apid handler writes both, so legacy readers
	// remain consistent). 0 = scale to zero (default).
	MinInstances int
	// MaxInstances is the per-app ceiling on live instances. The
	// PlanGate (`MaxInstancesAllowed`) gates this; bounded above by
	// the plan's `MaxConcurrency`. 0 = "use plan max_concurrency".
	MaxInstances int
	// Target is the per-instance signal the engine watches for the
	// scale-up trigger. nil = "engine-derives from
	// autoscale_target_rps / autoscale_target_cpu_pct" (legacy
	// compat, also the empty-policy projection). A non-nil zero-
	// value struct is legal and round-trips (e.g. `{metric: "rps",
	// value: 0}` — the engine reads Metric rather than Value for
	// "disabled"). PR-A only persists the shape; PR-B wires the
	// `concurrent_requests` metric, PR-C the engine cooldown.
	Target *ScalingTarget
	// ScaleOutCooldownS is the minimum number of seconds between
	// two scale-out events for the same app. Floor = 1 s (no
	// `0` traps); ceiling = 3600 s (1 h). Default = 0 means
	// "engine uses the plan default" (PR-C sets Hobby = 5, Pro = 3,
	// Scale = 1).
	ScaleOutCooldownS int
	// ScaleInCooldownS is the minimum number of seconds between
	// two scale-in events for the same app. Floor = 5 s (longer
	// than the scale-out floor to dampen oscillation); ceiling =
	// 86400 s (1 day). Default = 0 means "engine uses the plan
	// default" (PR-C sets Hobby = 60, Pro = 30, Scale = 15).
	ScaleInCooldownS int
}

// ScalingTarget is the (metric, value) pair the engine watches for
// the scale-up trigger. The metric surface is closed: `rps`,
// `concurrent_requests`, `p99_latency_ms`. Empty Metric = "disabled"
// (the engine falls back to the legacy autoscale_target_rps column).
type ScalingTarget struct {
	Metric string  // "" | "rps" | "concurrent_requests" | "p99_latency_ms"
	Value  float64 // target value (units depend on Metric)
}

// MarshalJSON encodes the policy as the canonical jsonb shape. The
// shape is the one the migration's `apps.scaling_policy` column
// round-trips, and the one the wire DTO mirrors. Empty fields render
// as `0` (the convention is "value zero = use default"; the apid
// gate is what enforces the floor / ceiling, not the encoder).
// Target uses an inline struct so the nil-pointer case emits `null`
// (mirrors the DTO's `*ScalingTarget`).
func (p ScalingPolicy) MarshalJSON() ([]byte, error) {
	type policyShape struct {
		MinInstances      int            `json:"min_instances,omitempty"`
		MaxInstances      int            `json:"max_instances,omitempty"`
		Target            *ScalingTarget `json:"target,omitempty"`
		ScaleOutCooldownS int            `json:"scale_out_cooldown_s,omitempty"`
		ScaleInCooldownS  int            `json:"scale_in_cooldown_s,omitempty"`
	}
	// The struct conversion pins the jsonb encoder's tag set to the
	// policyShape local — adding a json tag here does not silently
	// change how the canonical state.ScalingPolicy serialises.
	return json.Marshal(policyShape(p))
}

// UnmarshalJSON decodes the jsonb shape into the policy. Unknown
// fields are rejected at the wire boundary (see DTO Strict Unmarshal),
// but the on-disk schema is the canonical source — the `column OR
// legacy` union is what the production read paths project back into
// the in-memory struct.
func (p *ScalingPolicy) UnmarshalJSON(data []byte) error {
	type policyShape struct {
		MinInstances      int            `json:"min_instances,omitempty"`
		MaxInstances      int            `json:"max_instances,omitempty"`
		Target            *ScalingTarget `json:"target,omitempty"`
		ScaleOutCooldownS int            `json:"scale_out_cooldown_s,omitempty"`
		ScaleInCooldownS  int            `json:"scale_in_cooldown_s,omitempty"`
	}
	var raw policyShape
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.MinInstances = raw.MinInstances
	p.MaxInstances = raw.MaxInstances
	p.Target = raw.Target
	p.ScaleOutCooldownS = raw.ScaleOutCooldownS
	p.ScaleInCooldownS = raw.ScaleInCooldownS
	return nil
}

// ScalingPolicyOrDefault returns the policy if non-nil, otherwise the
// zero-value ScalingPolicy. Used at the read path so an empty jsonb
// column projects back into a structured value rather than a nil
// pointer the rest of the codebase has to special-case.
func ScalingPolicyOrDefault(p *ScalingPolicy) ScalingPolicy {
	if p == nil {
		return ScalingPolicy{}
	}
	return *p
}

// GitHubBinding is the (app → github_installation) edge persisted on
// the apps row by the /oauth/callback handler after it verifies the
// installation against api.github.com (ADR-012, review finding #1+#2
// closure). githubd reads this via the BindingsLookup interface so
// CheckRun writes go out under the right installation token instead
// of the hardcoded install_id=1 placeholder that M7.5 shipped with.
//
// PR-B adds AccountID + BindingID + LinkedAt so the bind row carries
// the (account → app → install) shape and the dashboard's "connected
// on" pill has a single source.
type GitHubBinding struct {
	AppID            string
	AccountID        string
	BindingID        string
	LinkedAt         time.Time
	InstallID        int64
	RepoFullName     string
	ProductionBranch string
}

// GitHubInstall is the durable OAuth handshake state for one account's
// GitHub App install (PR-C, audit-gap closure). Pre-PR-C this lived
// only in pkg/githubd/realservice.go's in-memory `s.installs` map
// and evaporated on kill -TERM, breaking the dashboard's
// /v1/install/repos/list + /v1/apps/{slug}/install/bind paths with
// 502s the moment githubd restarted. PR-C moves the source of truth
// to the github_installations table (migration 00059).
//
// AccountID is the PK (a uuid, references accounts(id) ON DELETE
// CASCADE — GDPR §17 G2 path deletes the row when the owning account
// goes away). InstallationID is GitHub's int64. DefaultBranch is
// captured at the OAuth handshake so the bind picker doesn't need a
// re-fetch. SealedToken holds the age-encrypted install token (the
// "ghs_…" form, minted via AppAuth.ExchangeInstallationToken and
// sealed with pkg/secretbox.SealOne before persisting — the
// plaintext token never touches the database). TokenExpiresAt is
// when the sealed blob expires (GitHub's install tokens are 1 h);
// cold-start readers unseal via pkg/secretbox.Open only when
// expires_at > now()+30s, otherwise re-mint + re-seal. SealedAt
// records when this row's sealed blob was last written (telemetry
// only — rotation cadence). AuditGithubLogin is the §11 paper trail
// on the durable row: the GitHub login who owned the install at
// seal time, used by cold-start re-verification to assert the
// session envelope's expected_login matches the durable record.
type GitHubInstall struct {
	AccountID        string
	InstallationID   int64
	DefaultBranch    string
	SealedToken      []byte
	TokenExpiresAt   time.Time
	SealedAt         time.Time
	AuditGithubLogin string
}

// MarshalJSON encodes a zero-value Manifest as {} so the jsonb default
// round-trips cleanly.
func (m AppManifest) MarshalJSON() ([]byte, error) {
	type alias AppManifest
	if m.IsZero() {
		return []byte("{}"), nil
	}
	return json.Marshal(alias(m))
}

// Deployment is one attempt to ship a version of an app.
type Deployment struct {
	ID          string
	AppID       string
	BuildID     string // empty for image: deploys
	ImageDigest string
	Kind        DeploymentKind
	SourcePath  string // tarball spool path (kind=tarball|dockerfile)
	SourceBytes int64
	Handler     string // function handler (kind=tarball when type=function)
	LogPath     string // build log spool path
	// SourceURL is the canonical upstream URL the build was triggered
	// from (Tier 3 / issue #197 B3.10). For githubd-triggered deploys
	// this is the repo + branch; for registry pulls it is the OCI
	// reference; for tarball / dockerfile deploys it is empty.
	// Populated by githubd's CreateDeployment callback. Phase 2 reads
	// it for build_provenance.source_url.
	SourceURL string
	// CommitSHA is the upstream commit SHA (when known). Length-bounded
	// at 64 hex chars in the DB (deployments_commit_sha_len_chk,
	// migrations/00047). Empty for image/tarball deploys that don't
	// have an upstream commit.
	CommitSHA string
	// RootfsPath / RootfsBytes are stamped by imaged after the per-app ext4 layer
	// is built (spec §4.6, drive1). schedd's prime handshake reads this row so
	// it can attach drive1 from the right path on the cold boot (ADR-018).
	RootfsPath string
	// RootfsKey is the canonical StorageBackend key for the same layer
	// (issue #96 / ADR-025 axis 2, PR #116). Mirror column of
	// RootfsPath: every row carries both. schedd carries the key on the
	// wake wire; vmmd resolves it via Storage.Get and stages into the
	// jail chroot. Local backends map the key to the same file as
	// RootfsPath; remote backends (OCI registry) resolve over HTTP. The
	// key is stamped by imaged at the same time as RootfsPath (see
	// SetDeploymentRootfs) and backfilled by migrations/00025 from the
	// legacy path on the default apps root. Empty only on rows written
	// before the migration landed and whose apps root was non-default;
	// imaged re-stamps them on the next build via SetDeploymentRootfs.
	RootfsKey   string
	RootfsBytes int64
	Status      DeploymentStatus
	// Priority is the deployment-queue priority (lower = run
	// sooner). Range [0, 1000], default 100. Migration 00362
	// widens the column CHECK + adds the partial index that
	// builderd's claim path reads (ADR-124).
	Priority int
	// ReorderedByPrincipal is the opaque principal that last
	// bumped Priority (account owner / API key id /
	// "operator:<username>"). Empty for deployments that have
	// never been reordered.
	ReorderedByPrincipal string
	// ReorderedAt is the wall-clock at which the priority was
	// last bumped (ADR-124). Nil for never-reordered rows.
	ReorderedAt *time.Time
	Error       string
	// ErrorCode is the RFC 7807 code stamped at the same time as
	// Error when a deployment transitions to `failed`. ADR-021:
	// oci.ErrImageNotFound / ErrImageEgressDenied /
	// ErrImageManifestInvalid map via pkg/api.SentinelToCode to
	// the stable codes that imaged writes here. Empty for every
	// other transition (and for deployments created before the
	// migrations/00021 column add).
	ErrorCode string
	// ErrorHint / ErrorWhy / ErrorFix / ErrorRelevantLogs are the
	// customer-facing explanation prose stamped alongside ErrorCode
	// (spec §6.4 amendment 1). Mirrors the wire-side Problem.Hint /
	// Problem.Fix / Problem.RelevantLogs fields so post-mortem
	// retrieval via `gregale deployment <id>` or
	// `gregale inspect <slug> --errors` surfaces the same 3-5 line
	// shape that the deploy-time Problem emits. All four are
	// omitempty in the wire DTO; rows written before the column
	// additions land leave them empty.
	ErrorHint         string
	ErrorWhy          string
	ErrorFix          string
	ErrorRelevantLogs []api.LogExcerpt
	// CancelledAt is the wall-clock at which the row transitioned
	// to DeployCancelled. Set by MarkDeploymentCancelled /
	// CancelDeploymentTx (ADR-124). Populates the `cancelled_at`
	// column added in migration 00360. Nil for every other row.
	CancelledAt *time.Time
	// CancelledByPrincipal is the opaque principal who initiated
	// the cancel (account owner / API key id / "operator:<u>").
	// Pairs with CancelReason for the audit trail.
	CancelledByPrincipal string
	// CancelReason is the closed-set reason: user|auto_quota|
	// auto_health|system. Distinct from DeployFailed.ErrorCode —
	// cancelled rows don't carry the worker's failure taxonomy.
	CancelReason string
	// DeletedAt is the wall-clock at which the customer-initiated
	// `deploys clear` stamped the row as hidden from their
	// deployment list. Status is intentionally NOT changed by the
	// clear path — admins see the row, customers don't. Nil until
	// the first clear.
	DeletedAt *time.Time
	// DeletedByPrincipal is the opaque principal who triggered the
	// clear. Pairs with DeletedAt.
	DeletedByPrincipal string
	CreatedAt          time.Time
	// Override columns (issue #460 / ADR-053). Six optional fields
	// that layer on top of the OCI image config when the customer
	// redeploys the same digest-pinned image with a different
	// entrypoint/cmd/env/port/healthcheck. PR-A persists the
	// shape; PR-B injects the merged manifest into the app-layer
	// ext4 at imaged time; PR-C threads the port through the
	// host-side wake path. Env / EnvSecrets are json.RawMessage
	// because the DB columns are jsonb and the handler marshals
	// the validated map before INSERT (mirrors how RootfsKey
	// carries the canonical storage handle).
	OverrideEntrypoint  []string        `json:"override_entrypoint,omitempty"`
	OverrideCmd         []string        `json:"override_cmd,omitempty"`
	OverrideEnv         json.RawMessage `json:"override_env,omitempty"`
	OverrideEnvSecrets  json.RawMessage `json:"override_env_secrets,omitempty"`
	OverridePort        int             `json:"override_port,omitempty"`
	OverrideHealthcheck json.RawMessage `json:"override_healthcheck,omitempty"`
	// OverrideLivenessProbe is the per-deployment liveness-probe
	// override JSON (issue #554 / ADR-078). Mirrors
	// OverrideHealthcheck — coalesce(override_liveness_probe,
	// '{}'::jsonb) on the read side; nullable jsonb column
	// (migrations/00154_deployment_liveness_probe.sql). The
	// cmd/vmmd liveness_recv goroutine consumes the resolved
	// struct (cmd/vmmd/liveness_recv.go::livenessProbeConfig) at
	// every BringUp. Per-plan defaults (Hobby/Pro/Scale → 5s /
	// 3 / 60s) are applied on the apid read path when this
	// column is empty.
	OverrideLivenessProbe json.RawMessage `json:"override_liveness_probe,omitempty"`
	// Sidecars (issue #463 / ADR-068). Up to 2 stateless sidecars
	// (1 init + 1 sidecar) per app. Persisted as jsonb on the
	// `deployments.sidecars` column (migration 00095). Field is
	// json.RawMessage (NOT []api.Sidecar) so the state package
	// does NOT import pkg/api — see pkg/api ↔ pkg/state cycle
	// (memory: pkg-api-cannot-import-pkg-state). The decoder
	// logic lives at the handler boundary (cmd/apid/handlers_deployments.go)
	// where pkg/api and pkg/state meet. PR-A only persists the
	// shape; PR-B threads the array into imaged's pull path and
	// guest-init's supervise loop. env values are stored
	// envelope-sealed (namespace="sidecar_env"); PR-B unseals
	// transiently at the pull path (mirrors app_env_secret
	// unseal at the same seam).
	Sidecars json.RawMessage `json:"sidecars,omitempty"`
	// Workflows is the validated ADR-081 workflow definition set attached
	// to this deployment. Keeping it on the deployment row makes the live
	// deployment the source of truth for new run snapshots.
	Workflows json.RawMessage `json:"workflows,omitempty"`
	// MinInstances is the per-deployment cold-wake floor override
	// (issue #557 closure / ADR-072). Default 0 = "inherit from
	// parent app"; an explicit positive value is the deployment's
	// own floor. Effective per-instance floor =
	// `max(app.EffectiveMinInstances(), d.EffectiveMinInstances())`
	// (composed at the trigger, not here). Validated against the
	// parent app's plan ceiling at the PATCH handler; this struct
	// does NOT carry the plan context, so the helper is just
	// min(0, MinInstances)→0 + raw value.
	MinInstances int `json:"min_instances,omitempty"`
	// TrafficPercent is the per-deployment traffic-split weight
	// (issue #556 PR-A). Integer in [0, 100] enforced by the
	// deployments_traffic_percent_chk CHECK constraint (migration
	// 00160). On create: default 100 (server-side when caller passes
	// 0); on supersede: zeroed in the same tx as the INSERT so Σ over
	// live rows remains 100 by construction. PR-B's gateway picker
	// consults this column via the new LiveDeployments(appID)
	// plural query; PR-A only persists the shape and exposes it on
	// the DTO wire. The "zero siblings" semantics for the
	// UpdateDeploymentTraffic transaction live in pgstore.go, not
	// here — this struct just carries the field.
	TrafficPercent int `json:"traffic_percent,omitempty"`
	// Scan columns (issue #464 / ADR-055 / PR-3). Per-deploy grype
	// scan result, status, and scanned_at. Mirror the deployments
	// table columns added by migrations/00135. The pgstore reads
	// these as raw bytes via SQL (ScanResult []byte, ScanStatus
	// string, ScannedAt time.Time) — the apid-side decoder turns
	// ScanResult bytes into the typed *api.ScanResult at the
	// handler boundary. The memstore mirror keeps the in-memory
	// shape aligned with PgStore so unit tests that exercise the
	// write path don't need Postgres.
	ScanResult []byte    `json:"scan_result,omitempty"`
	ScanStatus string    `json:"scan_status,omitempty"`
	ScannedAt  time.Time `json:"scanned_at,omitempty"`
	// Secret-scan columns (migrations/00221, secret-scan v2). The
	// server-side scanner in cmd/apid/secretscan.go writes the
	// per-line findings + timestamp when the upload is rejected
	// (422 secret_scan_strict). The audit row survives the tarball
	// cleanup so post-mortem forensics can read what was found
	// without re-walking bytes. Mirrors the Scan{Result,Status,At}
	// shape above — kept distinct so the two pipelines (grype CVE +
	// secret scan) don't clobber each other's columns. The apid
	// decoder turns SecretFindings []byte into the typed
	// *api.SecretScanResult at the handler boundary; the raw-bytes
	// shape here matches the existing ScanResult convention.
	SecretFindings  []byte     `json:"secret_findings,omitempty"`
	SecretScannedAt *time.Time `json:"secret_scanned_at,omitempty"`

	// LivenessRestartCount (issue #586 / ADR-129 / cluster C
	// commit 12) is the persisted lifetime restart counter for
	// this deployment. pkg/state.pgstore.RecordRestart bumps it
	// alongside the in-memory LivenessWindow.RecordRestart call
	// (migrations/00411). On schedd startup the LivenessWindow
	// seeds from this column so a fresh process inherits the
	// prior count instead of starting at zero — closing the
	// "schedd restart resets the restart-loop signal" gap. The
	// column is monotonic in the application code; the
	// deployments_liveness_restart_count_nonneg_chk CHECK is a
	// belt-and-braces SQL-level guard. Dashboard surfaces query
	// this column for the "Restart count (lifetime)" stat on
	// /v1/deployments/{id}.
	LivenessRestartCount int `json:"liveness_restart_count,omitempty"`

	// Canary preset + step ladder (issue #976 / ADR-122 /
	// SAFE-RELEASES-A). CanaryPreset is the catalog name from
	// pkg/api/canary (none/slow/balanced/aggressive/1-10-50-100);
	// CanaryStep is the zero-indexed position in
	// pkg/api/canary.LookupPreset(CanaryPreset).Stages while a rollout
	// is in flight; a completed rollout uses the terminal sentinel
	// CanaryStep == CanaryTotalSteps. CanaryTotalSteps is the ladder
	// length. canary_step_bounds_chk locks the invariant
	// (total=0,step=0) OR (total>0,0<=step<=total).
	//
	// Stamped at deploy time by the apid CreateDeployment path
	// (BuildDeploymentForInsert at cmd/apid/handlers_sidecars.go:308).
	// Advanced on a wall-clock boundary by the canary_progression
	// meterd tick (pkg/canary, issue #976) which calls APID's atomic
	// AdvanceCanary endpoint — apid remains the authoritative writer
	// of deployments.* per CLAUDE.md ownership rules.
	CanaryPreset        string     `json:"canary_preset,omitempty"`
	CanaryStep          int        `json:"canary_step,omitempty"`
	CanaryTotalSteps    int        `json:"canary_total_steps,omitempty"`
	CanaryStepStartedAt *time.Time `json:"canary_step_started_at,omitempty"`
	// CanaryStages (SAFE-RELEASES production-leveling Stream F)
	// is the jsonb-serialised canary ladder when CanaryPreset
	// is "custom" (migrations/00487). The wire form is a
	// []api/canary.CustomStage; the DB column is jsonb. NULL
	// for every catalog preset (none / slow / balanced /
	// aggressive / 1-10-50-100) and for pre-PR rows. The
	// orchestrator's per-row resolve reads this column when
	// CanaryPreset == "custom" (catalog presets resolve via
	// canary.LookupPreset).
	CanaryStages json.RawMessage `json:"canary_stages,omitempty"`

	// Rollout state machine (issue #976 / ADR-122 / SAFE-RELEASES-F).
	// Pending → RollingOut → Complete; Aborted reachable from
	// Pending/RollingOut (rollback path). The closed-set is
	// enforced at the schema layer via deployments_rollout_state_chk
	// (migration 00480). Stamped at deploy time by apid; walked by
	// pkg/safedeploy.Orchestrator.Once (Mega PR #2 commit 5);
	// mutatable via the manual gregale rollouts recover <slug>
	// CLI (commit 6).
	RolloutState         string     `json:"rollout_state,omitempty"`
	RolloutStartedAt     *time.Time `json:"rollout_started_at,omitempty"`
	RolloutCompletedAt   *time.Time `json:"rollout_completed_at,omitempty"`
	RolloutAbortedAt     *time.Time `json:"rollout_aborted_at,omitempty"`
	RolloutAbortedReason string     `json:"rollout_aborted_reason,omitempty"`

	// Parking reason + timestamp (issue #554 / ADR-079 follow-up).
	// pkg/sched.Engine.ParkDeployment sets these before flipping
	// apps.status to `evicted_cold`; the apid GET /v1/apps/{slug}
	// surface renders them as the `parked_deployment: { id,
	// parked_reason, parked_at }` reference. closed-set vocabulary
	// is enforced at the schema layer via the
	// deployments_parked_reason_check constraint (migration 00157).
	ParkedReason string     `json:"parked_reason,omitempty"`
	ParkedAt     *time.Time `json:"parked_at,omitempty"`
	// Scope (ADR-091 / PR-D) — per-deployment env targeting.
	// The deployment declares which named scope (`default`/
	// `staging`/`prod`/...) its wake should read env from.
	// Backfilled to `'default'` by migration 00213's PG11+
	// fast-default (metadata-only on pre-PR rows, no UPDATE
	// rewrite). Enforced at the schema layer via the
	// `deployments_scope_shape` CHECK and the partial unique
	// index `deployments_app_scope_live_uniq` (at most one
	// live row per (app_id, scope)). A scope change requires a
	// NEW deployment — there is no update-time scope change.
	Scope string `json:"scope,omitempty"`
	// StageState (ADR-117, migration 00302) — per-deployment
	// customer-UX stage projection. Owned entirely by
	// Store.AppendDeploymentStage — handlers MUST NOT write the
	// column directly. The 2s SSE polling tick at
	// cmd/apid/handlers_ext.go:4156-4157 diffs this struct
	// against a per-connection `announced map` and emits one
	// `event: stage {name, started_at, duration_ms, status}`
	// frame per transition. See pkg/state/types.go:89 for the
	// closed StageName vocabulary.
	//
	// Stored as json.RawMessage (NOT a typed StageState struct)
	// so the pgstore scan path mirrors ScanResult / SecretFindings
	// — the typed shape is unmarshalled lazily by the SSE
	// handler that needs it, exactly once per connection.
	StageState json.RawMessage `json:"stage_state,omitempty"`

	// Actor columns (issue #606). Orthogonal to the human-readable
	// `DeployedBy` text column from issue #977 / ADR-116 (PR #984):
	// that column carries the resolved name for the dashboard
	// ("Poyraz Küçükarslan"), this group carries the
	// machine-readable attribution needed for SOC 2 CC7.2 / GDPR
	// ("who deployed v3 of app X at 14:32?"). Migration 00305.
	//
	//   DeployedByUserID  — UUID FK to accounts(id). Nullable:
	//                       (a) anonymous / unauthenticated CLI
	//                       deploys predate the FK (PR-D cluster,
	//                       issue #879 PR-D); (b) GitHub-push
	//                       deploys are not attributable to a
	//                       local Gregale account. The dashboard
	//                       renders the resolved name via JOIN.
	//                       ON DELETE SET NULL on the FK so a
	//                       GDPR-erased account keeps the row
	//                       but nulls the attribution.
	//   DeployedVia       — closed-set: 'api' | 'cli' |
	//                       'dashboard' | 'github' | 'operator'.
	//                       NOT NULL DEFAULT 'api' so pre-feature
	//                       rows stay valid without a backfill
	//                       (CHECK enforces the vocabulary,
	//                       migration 00305). The CLI sends 'cli';
	//                       the dashboard sends 'dashboard';
	//                       githubd sends 'github'; the API
	//                       surfaces 'api' by default; the
	//                       `gregale operator ...` subcommands
	//                       surface 'operator'.
	//   DeployedFromIP    — INET, nullable. Stamped from
	//                       r.RemoteAddr via the same loopback+XFF
	//                       trust contract the auth-limit bucket
	//                       uses (pkg/middleware.ClientIP). The
	//                       column is observability data, not a
	//                       security gate — the trust contract is
	//                       documented at the apid handler that
	//                       stamps it (PR-E1.2).
	//   PusherLogin       — TEXT, nullable. Distinct from
	//                       `DeployedBy` (issue #977): that
	//                       carries the resolved human-readable
	//                       name, this carries the raw GitHub
	//                       login string (e.g. `poyrazK`) so the
	//                       audit reader can disambiguate a
	//                       renamed / deleted GitHub user from a
	//                       stale `deployed_by` label.
	DeployedByUserID string `json:"deployed_by_user_id,omitempty"`
	DeployedVia      string `json:"deployed_via,omitempty"`
	DeployedFromIP   string `json:"deployed_from_ip,omitempty"`
	PusherLogin      string `json:"pusher_login,omitempty"`

	// Annotation columns (issue #977 / ADR-116). Free-form operator
	// note + closed-set tag + auto-captured actor + PR number.
	// Stamped at create time; not editable post-hoc (out of scope
	// per ADR-116 D9). All nullable; pre-feature rows have all four
	// NULL.
	//
	//   Reason     — free-form prose, ≤280 chars (CHECK enforces).
	//                Example: "Emergency rollback after payment
	//                provider incident" (issue body literal).
	//   Tag        — closed-set enum: incident_recovery | hotfix |
	//                scheduled_maintenance | compliance_hold |
	//                partner_request (CHECK enforces).
	//   DeployedBy — human-readable actor label. CLI auto-captures
	//                `git config user.name`; githubd stamps
	//                `push.pusher.name`; the Action defaults the
	//                input to `${{ github.actor }}`. Operator can
	//                override with `--deployed-by`.
	//   PRNumber   — positive int when the wire offers it (githubd
	//                pull_request.number; Action's `pr-number`
	//                input). Push-to-main with no inferred PR
	//                leaves this NULL (D5).
	Reason     string `json:"reason,omitempty"`
	Tag        string `json:"tag,omitempty"`
	DeployedBy string `json:"deployed_by,omitempty"`
	PRNumber   int    `json:"pr_number,omitempty"`

	// RollbackOn5xx (Mega-C PR-2 / issue #961 leaf 8): when
	// true, schedd subscribes to wake.response_5xx events on
	// this deployment and fires the apid-internal
	// /v1/internal/auto-rollback-on-5xx endpoint when the
	// per-plan 5xx threshold is crossed inside the first-wake
	// window. Pro+ only; Free/Hobby customers get a 403 on the
	// create-deployment request (ErrPlanRollbackOn5xxNotAllowed).
	// Default false; the column is BOOLEAN NOT NULL DEFAULT
	// false (migration 00354).
	RollbackOn5xx bool `json:"rollback_on_5xx,omitempty"`
	// FirstWakeAt + First5xxWindowEndsAt stamp the start of the
	// first-wake window (anchored at the first
	// wake.proxy_first_byte event). Both nullable; the window
	// ends at FirstWakeAt + 5 min, controlled by
	// pkg/api.RollbackOn5xxWindowMinutes (5 min). Both nullable;
	// the auto-rollback only fires inside this window.
	FirstWakeAt          *time.Time `json:"first_wake_at,omitempty"`
	First5xxWindowEndsAt *time.Time `json:"first_5xx_window_ends_at,omitempty"`
	// First5xxCount is the running tally of wake.response_5xx
	// events on this deployment. Incremented atomically by the
	// BumpFirst5xxCount pgstore method on every wake.response_5xx
	// event; schedd's AutoRollbackWatcher checks it against the
	// per-plan threshold (plan.RollbackOn5xxThreshold()) inside the
	// First5xxWindowEndsAt window. NOT NULL DEFAULT 0 (migration
	// 00354); pre-feature rows backfill to 0.
	First5xxCount int `json:"first_5xx_count,omitempty"`
	// LastAutoRollbackAt + LastAutoRollbackReason record the
	// most-recent auto-rollback (Mega-C PR-2). Stamped by
	// pgstore.AutoRollbackDeploymentsTx; idempotent on subsequent
	// passes (re-stamping with the same reason does NOT shift
	// the timestamp). LastAutoRollbackReason uses the closed-set
	// vocabulary {threshold_exceeded, first_window_expired},
	// enforced at the schema layer via
	// deployments_last_auto_rollback_reason_check.
	LastAutoRollbackAt     *time.Time `json:"last_auto_rollback_at,omitempty"`
	LastAutoRollbackReason string     `json:"last_auto_rollback_reason,omitempty"`
}

// OpenAPISnapshot is the projected-customer-OpenAPI snapshot
// captured at a deployment's status='live' transition (ADR-121,
// migration 00358). One row per deployment; the PR-C gate
// compares the pending deployment's snapshot against the
// current live deployment's snapshot at the same scope and
// rejects prod-scope promotions that emit a SeverityError
// break.
//
// Snapshot is the canonical JSON of pkg/openapidiff.Spec —
// produced by pkg/openapidiff.MarshalSnapshot. SHA-256 is the
// hex-64 digest of the same canonical bytes; the migration
// pins the digest shape via a CHECK constraint. SchemaVersion
// starts at 1; bump on a breaking serializer change (and a
// separate migration that widens the schema_version CHECK).
//
// Scope is the per-deployment env targeting (deployed.scope
// column) — the gate looks up the latest snapshot for
// (app_id, scope="prod") and rejects the promotion when the
// differ emits a break. The schema's scope CHECK mirrors
// deployments_scope_shape so cross-table lookups never drop
// rows on a regex mismatch.
type OpenAPISnapshot struct {
	DeploymentID  string
	AppID         string
	Scope         string
	Snapshot      json.RawMessage
	SHA256        string
	SchemaVersion int
	CapturedAt    time.Time
}

// DeploymentPreviewActive (issue #976 / ADR-122 / SAFE-RELEASES-C)
// returns true iff the deployment is in a state where a customer
// can usefully visit its preview URL. Mirrors the App.PreviewOpen()
// shape used by the PR-preview allowlist branch
// (pkg/gateway/allowlist.go::previewOpen) — both gateways call
// into the same allowlist, so the two predicates must agree on the
// "previewable" semantics.
//
// Active states (return true):
//
//   - DeployPending, DeployBuilding, DeployImaging,
//     DeploySnapshotting, DeployLive: the deployment is in flight
//     or live; the preview URL serves either the staging snapshot
//     or the live one. A pending deploy is "previewable" because
//     the customer might want to verify the build hasn't crashed
//     before flipping traffic.
//
// Inactive states (return false):
//
//   - DeployFailed: the deployment failed at the build / image /
//     snapshot stage; serving the preview URL would 502 the
//     customer. The dashboard surfaces the failure instead.
//   - DeploySuperseded: a newer deployment has flipped traffic
//     away from this one. The preview URL is still resolvable
//     for post-mortem, but the allowlist denies it so a stale
//     link doesn't accidentally serve traffic.
func (d Deployment) DeploymentPreviewActive() bool {
	switch d.Status {
	case DeployPending, DeployBuilding, DeployImaging,
		DeploySnapshotting, DeployLive:
		return true
	default:
		return false
	}
}

// StageState is the typed view of the
// `deployments.stage_state` jsonb column (ADR-117,
// migration 00302). Shape:
//
//	{
//	  "current": "<StageName>",
//	  "current_started_at": "<RFC3339Nano>" | null,
//	  "history": [
//	    {"name":"source_download","started_at":"...",
//	     "ended_at":"...","duration_ms":1203,"status":"completed"},
//	    ...
//	  ]
//	}
//
// `Current` lives outside `History` until it closes. The atomic
// JSONB merge is implemented by `appendDeploymentStage` in
// pkg/state/queries.sql — read-modify-write at the Go layer is
// NOT safe (two transitions from concurrent goroutines would race).
// The pgstore implementation is the only writer; memstore mirrors
// the shape so unit tests can exercise the read path without
// spinning Postgres.
type StageState struct {
	Current          StageName        `json:"current"`
	CurrentStartedAt *time.Time       `json:"current_started_at,omitempty"`
	History          []StageStateItem `json:"history"`
}

// StageStateItem is one closed stage transition in the
// `stage_state.history` array. `DurationMs` is measured server-side
// by `appendDeploymentStage` (now - current_started_at) so the SSE
// consumer doesn't have to trust a 2s-tick-derived `time.Now()`
// reconstruction.
//
// `StartedAt` is a *time.Time (NOT time.Time) so the JSON wire shape
// is `null` when the migration seed left it unset — time.Time zero
// value marshals to the literal string "0001-01-01T00:00:00Z" which
// is indistinguishable from a real epoch and contradicts the
// "uninitialized = null" contract the SSE consumer expects. The
// pointer nil-vs-set distinction preserves that contract.
type StageStateItem struct {
	Name       StageName  `json:"name"`
	StartedAt  *time.Time `json:"started_at"`
	EndedAt    *time.Time `json:"ended_at"`
	DurationMs int64      `json:"duration_ms"`
	Status     string     `json:"status"` // "completed" | "failed"
	Reason     string     `json:"reason,omitempty"`
}

// DeploymentSidecarLayer is one sidecar's per-workload filesystem
// handle (issue #463 / ADR-069 / PR-B). imaged writes one row per
// sidecar during the buildImageLayer pass; vmmd reads it at wake
// time to resolve the StorageBackend key into a tmp path. The
// 2-row cap is mirrored at the schema layer via the
// `deployments.sidecars` jsonb CHECK constraint (migration 00118);
// this table's own constraint is just the PK uniqueness
// (deployment_id, sidecar_name). The FK CASCADE means deleting
// the deployment carries the rows with it (defence-in-depth —
// imaged's cleanupAppFiles also explicitly removes the storage
// keys).
//
// SidecarName is the customer-chosen name from
// `pkg/api/dto.go::Sidecar.Name`. StorageKey follows the
// convention `apps/<slug>/<depID>-<sidecarName>.ext4`. ContentDigest
// is the OCI digest of the sidecar image at pull time
// (sha256:...; matches PR-A's digest-pinned contract). CreatedAt /
// UpdatedAt are stamped by Postgres defaults and refreshed on
// UPDATE.
type DeploymentSidecarLayer struct {
	DeploymentID  string    `json:"deployment_id"`
	SidecarName   string    `json:"sidecar_name"`
	StorageKey    string    `json:"storage_key"`
	Bytes         int64     `json:"bytes"`
	ContentDigest string    `json:"content_digest"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Build is one build pipeline run for a deployment (spec §9). Builderd writes
// status transitions; apid only creates the queued row.
type Build struct {
	ID           string
	DeploymentID string
	Kind         DeploymentKind // railpack|dockerfile in production; we mirror kind here
	SourceBytes  int64
	Status       BuildStatus
	FailureClass FailureClass
	LogPath      string
	StartedAt    time.Time
	FinishedAt   time.Time
	EnqueuedAt   time.Time // set at CreateBuild; builderd measures queue wait against it (ADR-030)
	// CancelledAt is the wall-clock at which the build row
	// transitioned to BuildCancelled. Set by MarkBuildCancelled
	// (ADR-124). Nil for every other row.
	CancelledAt *time.Time
	// CancelledByDeploymentCascade is true when the cancel came
	// from a deployment-row flip (CancelDeploymentTx). False when
	// a future direct build-cancel path lands. Disambiguates the
	// audit trail.
	CancelledByDeploymentCascade bool
}

// BuildProvenance is the post-mortem "what ran?" record for a Build
// (ADR-038, Tier 3 / issue #197 B3.1). Populated by builderd at the
// two markSucceeded sites; read by apid at GET /v1/builds/{id}/provenance
// and by the CLI at `faas build provenance <id>`.
//
// One row per build_id (UNIQUE constraint enforces it). Fields mirror
// the table shape with no translation; empty strings round-trip the
// nullable columns. sbom_storage_key is empty in this PR — Phase 3's
// syft populator fills it.
type BuildProvenance struct {
	ID             string
	BuildID        string
	BuildkitVer    string
	RailpackVer    string
	BaseDigest     string
	SourceSHA256   string
	SourceURL      string
	CommitSHA      string
	Plan           string
	RunnerDigest   string
	BuilderNodeID  string
	StartedAt      time.Time
	FinishedAt     time.Time
	SBOMStorageKey string
	// FrameworkVer is the language version the customer's source
	// declares they want (engines.node / requires-python / .nvmrc /
	// .python-version / .tool-versions / go.mod ::go X.Y). Surfaced
	// for operator observability — NEVER read by the build pipeline
	// (the runtime version is bound by the OCI base ref via
	// FAAS_DEPLOY_BASE_REF_<RUNTIME>; see ADR-052 §rejected-alts).
	// Populated by pkg/builderd::recordProvenance (issue #740 /
	// DEPLOY-PROV-5 / ADR-087). Empty when no version file is found
	// or any parser fails — best-effort, never an error.
	FrameworkVer string
}

// CustomDomain is a customer's CNAME'd domain. apid owns this table;
// gatewayd-internal reads it to decide whether to mint a cert (spec §4.1, §7).
type CustomDomain struct {
	Domain         string
	AppID          string
	ChallengeToken string
	VerifiedAt     time.Time // zero = unverified
}

// Verified reports whether the TXT challenge has been satisfied.
func (d CustomDomain) Verified() bool { return !d.VerifiedAt.IsZero() }

// DomainDoctorObservation (ADR-120) is the dns_poller's
// view of a per-domain probe pass. The struct mirrors the
// domain_doctor_observations table (migrations/00309) so
// the apid handler can hand the row straight to the
// doctor DTO without per-field translation. The
// nullable-ish fields use *string / *bool / time.Time-zero
// rather than sql.NullX because the table is hand-rolled
// pgx and the conversion happens at the store boundary.
type DomainDoctorObservation struct {
	Domain          string
	SurfaceID       string // empty for legacy custom_domains rows
	ObservedAt      time.Time
	DNSRecordFound  bool
	PointsToGregale bool
	CAAPermits      *bool // nil = no CAA published (allowed by default)
	IPv6Conflict    bool
	ObservedTarget  string
	ObservedAAAA    string
	CAAObserved     string
	CertState       string // none|pending|issued|failed|dial_failed
	CertNotAfter    time.Time
	LastError       string
	DNSCheckedAt    time.Time
	CertCheckedAt   time.Time
}

// Cron is a scheduled synthetic POST through gatewayd-internal (spec §4.3).
type Cron struct {
	ID          string
	AppID       string
	Schedule    string // cron expression
	Path        string
	Enabled     bool
	CreatedAt   time.Time
	LastFiredAt time.Time // zero until first fire; updated by MarkCronFired
}

// FireNowStatus is the closed vocabulary for cron_fire_now_requests.status
// (migrations/00193). Mirrors the audit-event `cron.fired.manually` status
// field for the 4 success/fail outcomes; `cancelled` is reserved for a
// future pause/cancel surface.
type FireNowStatus string

const (
	FireNowStatusPending   FireNowStatus = "pending"
	FireNowStatusRunning   FireNowStatus = "running"
	FireNowStatusSucceeded FireNowStatus = "succeeded"
	FireNowStatusFailed    FireNowStatus = "failed"
	FireNowStatusCancelled FireNowStatus = "cancelled"
)

// FireNowRequest is one row of cron_fire_now_requests (migrations/00193).
// ADR-090 PR-C: apid inserts on POST /v1/crons/{id}/run; schedd claims
// (FOR UPDATE SKIP LOCKED) on each NotifyCronRunNow wakeup, calls
// RunCronNow in-process, then transitions status to terminal.
//
// Invariant: a single row represents a single fire-now attempt. The
// customer-side idempotency wrapper (cmd/apid/server.go::s.idempotent)
// guards the INSERT so a retry with the same Idempotency-Key never
// creates a second row.
type FireNowRequest struct {
	ID           string
	CronID       string
	AccountID    string
	RequestedAt  time.Time
	Status       FireNowStatus
	InvocationID *string // nil while pending/running; set on terminal
	Error        *string // nil until status=failed
	FinishedAt   *time.Time
}

// OperatorIntentStatus is the closed vocabulary for
// operator_intents.status (migrations/00431). Mirrors the audit-event
// `operator.action.<verb>.outcome` shape — only `succeeded` and
// `failed` are emitted by schedd's terminal transition; `cancelled`
// is reserved for a future operator-cancel surface (schema is
// forward-compatible).
type OperatorIntentStatus string

const (
	OperatorIntentPending   OperatorIntentStatus = "pending"
	OperatorIntentRunning   OperatorIntentStatus = "running"
	OperatorIntentSucceeded OperatorIntentStatus = "succeeded"
	OperatorIntentFailed    OperatorIntentStatus = "failed"
	OperatorIntentCancelled OperatorIntentStatus = "cancelled"
)

// OperatorIntentKind is the closed vocabulary for
// operator_intents.kind (migrations/00431). Matches the CHECK
// constraint byte-for-byte.
type OperatorIntentKind string

const (
	OperatorIntentKindForcePark     OperatorIntentKind = "force_park"
	OperatorIntentKindForceColdBoot OperatorIntentKind = "force_cold_boot"
	// OperatorIntentKindForceRestart is the operator-initiated
	// kill-instance + cold-boot-on-next-wake primitive (P2d).
	// Dispatched by schedd's Engine.ForceRestart; the
	// operator_intents.kind CHECK constraint widened by
	// migrations/00446 to include this value. Audit envelope:
	// operator.action.restart_instance (apid, at intent-write)
	// + operator.action.restart_instance.outcome (schedd, at
	// terminal). Mirrors the existing two in shape — apid
	// inserts a pending row + emits db.NotifyOperatorIntent;
	// schedd claims via FOR UPDATE SKIP LOCKED LIMIT 1 and
	// dispatches by kind.
	OperatorIntentKindForceRestart OperatorIntentKind = "force_restart"
)

// OperatorIntent is one row of operator_intents (migrations/00431).
// PR #1099 P2 redesign: apid (the only producer) inserts on
// POST /v1/admin/instances/{id}/force-park or
// POST /v1/admin/apps/{slug}/force-cold-boot, emits
// `db.NotifyOperatorIntent`, returns 202 Accepted. schedd (the only
// consumer) claims the row via ClaimPendingOperatorIntent
// (FOR UPDATE SKIP LOCKED LIMIT 1), dispatches by kind
// (force_park → Engine.Park, force_cold_boot →
// Engine.ForceColdBootNextWake, force_restart → Engine.ForceRestart),
// then transitions status to terminal.
//
// Invariant: a single row represents a single admin-action attempt.
// Admin actions are deliberate re-clicks, not retries, so there is
// no per-request idempotency wrapper — two clicks produce two intents.
//
// Target_id is free-text (NOT a uuid column) because it is either an
// instance_id (force_park) OR a deployment_id (force_cold_boot). The
// kind column disambiguates.
//
// TraceID is the OTel W3C 32-char hex identifier shared with the
// inbound HTTP request (apid) and the terminal outcome audit row
// (schedd). Lets the operator-action observability layer join
// alert ↔ action ↔ outcome on one column. Nullable: NULL when
// the action arrived via a path that did not stamp a trace_id
// (e.g. legacy cron-fired reclaim_build).
type OperatorIntent struct {
	ID                 string
	Kind               OperatorIntentKind
	TargetID           string
	AccountID          *string // nil for fleet-level actions (e.g. reclaim_build); set for per-account actions
	ActorID            string
	Reason             string
	Metadata           json.RawMessage
	Status             OperatorIntentStatus
	RequestedAt        time.Time
	StartedAt          *time.Time
	FinishedAt         *time.Time
	Error              string
	SnapIDsMarkedStale []string
	TraceID            *string
}

// AlertMetric is a closed vocabulary for the metric side of an AlertRule
// condition (issue #396, ADR-045). Names mirror the AppMetricsResponse
// payload verbatim so the evaluator and the customer-facing metrics
// endpoint cannot drift. failed_invocations is the only non-Prometheus
// metric; its source dimension comes through AlertRule.FailureSource.
//
// Issue #1233 / ADR-123 — extended with 5 metrics backing the alert
// preset catalog (api_up / account_spend_eur / deployment_failed /
// cert_expiry_seconds / queue_depth). The pkg/api.AllowedAlertRuleMetrics
// slice and the alert_rules_metric_chk DB CHECK mirror these byte-for-byte
// (migrations/00349_alert_rules_extend_metrics_chk.sql).
type AlertMetric string

const (
	AlertMetricErrorRate         AlertMetric = "error_rate_pct"
	AlertMetricLatencyP50        AlertMetric = "latency_p50_ms"
	AlertMetricLatencyP95        AlertMetric = "latency_p95_ms"
	AlertMetricLatencyP99        AlertMetric = "latency_p99_ms"
	AlertMetricColdStartPct      AlertMetric = "cold_start_pct"
	AlertMetricRequestCount      AlertMetric = "request_count"
	AlertMetricFailedInvocs      AlertMetric = "failed_invocations"
	AlertMetricAPIUp             AlertMetric = "api_up"
	AlertMetricAccountSpendEUR   AlertMetric = "account_spend_eur"
	AlertMetricFailedDeployments AlertMetric = "deployment_failed"
	AlertMetricCertExpirySeconds AlertMetric = "cert_expiry_seconds"
	AlertMetricQueueDepth        AlertMetric = "queue_depth"
	// AlertMetricCanaryStuckStep (SAFE-RELEASES-OBS PR-B) is the
	// Prometheus-counter-backed tripwire for a canary sitting at the
	// same step past StuckAfterDuration. The actual firing happens
	// in Prometheus against safedeploy_orchestrator_stuck_detected_total;
	// the catalog entry exists so customers / operators see the
	// preset in /dashboard/alerts. See pkg/alerts/safe_releases_presets.go.
	AlertMetricCanaryStuckStep AlertMetric = "canary_stuck_step"
	// AlertMetricSafedeployAuditEmitFailing — trips when
	// safedeploy_orchestrator_audit_emit_failed_total rate > 0.1/sec
	// for 10 min. Critical; closes the audit-trail-blacked-out failure
	// mode PR-A unblocked.
	AlertMetricSafedeployAuditEmitFailing AlertMetric = "safedeploy_audit_emit_failing"
	// AlertMetricDeploymentAuditGCFailing — trips when
	// deployment_audit_gc_failed_total rate > 0 for 1 h. Warning;
	// 90-day GC failure is a disk-fill risk.
	AlertMetricDeploymentAuditGCFailing AlertMetric = "deployment_audit_gc_failing"
	// AlertMetricCanaryFleetInFlightHigh — trips when
	// safedeploy_in_flight_rollouts > 50 for 10 min. Warning;
	// operator back-pressure signal.
	AlertMetricCanaryFleetInFlightHigh AlertMetric = "canary_fleet_in_flight_high"
)

// AlertComparison is the textual form of the comparison operator stored
// on alert_rules. Symbolic operators (`>`, `>=`) force escaping in JSON
// and the OpenAPI schema; the textual enum is the floor a typo cannot
// cross. The CHECK constraint on alert_rules mirrors this set.
type AlertComparison string

const (
	AlertGt  AlertComparison = "gt"
	AlertGte AlertComparison = "gte"
	AlertLt  AlertComparison = "lt"
	AlertLte AlertComparison = "lte"
)

// AlertWindowSpec is the closed window vocabulary; sharing it with the
// metrics endpoint (migrations/00023_metrics_range_vocabulary.sql)
// means the evaluator can never ask Prometheus for data outside
// prom_retention_days:15.
type AlertWindowSpec string

const (
	AlertWindow5m  AlertWindowSpec = "5m"
	AlertWindow15m AlertWindowSpec = "15m"
	AlertWindow1h  AlertWindowSpec = "1h"
	AlertWindow6h  AlertWindowSpec = "6h"
	AlertWindow24h AlertWindowSpec = "24h"
	AlertWindow7d  AlertWindowSpec = "7d"
	AlertWindow15d AlertWindowSpec = "15d"
)

// AlertFailureSource is the dimension selector for the failed_invocations
// metric. Values mirror the existing InvocationSource enum with the
// addition of "any" so a rule can collapse across sources. The CHECK
// constraint on alert_rules rejects any value outside this set.
type AlertFailureSource string

const (
	AlertFailureAny         AlertFailureSource = "any"
	AlertFailureCron        AlertFailureSource = "cron"
	AlertFailureQueue       AlertFailureSource = "queue"
	AlertFailureDelayedTask AlertFailureSource = "delayed_task"
	AlertFailureAsyncInvoke AlertFailureSource = "async_invoke"
)

// AlertState is the cool-down state machine (issue #396 criterion 4).
// ok   — no current breach; a fresh evaluate can transition to firing.
// firing — the rule is mid-cooldown. The ClaimAlertFire store method
//
//	refuses to enqueue a second delivery until the rule returns
//	to 'ok' AND the cool-down bucket has elapsed.
type AlertState string

const (
	AlertStateOk     AlertState = "ok"
	AlertStateFiring AlertState = "firing"
)

// AlertDeliveryStatus is the terminal state of one alert_deliveries row.
// The dispatcher (issue #396 PR 2 + PR 4) walks the retry ladder; a
// delivery lands in 'delivered' on a 2xx, 'failed' on a terminal 4xx
// or after the retry exhaustion.
type AlertDeliveryStatus string

const (
	AlertDeliveryPending   AlertDeliveryStatus = "pending"
	AlertDeliveryDelivered AlertDeliveryStatus = "delivered"
	AlertDeliveryFailed    AlertDeliveryStatus = "failed"
)

// AlertAction (issue #976 / ADR-122 / SAFE-RELEASES-B). Closed-set
// vocabulary mirroring migrations/00481_alert_rules_action.sql +
// pkg/api.AllowedAlertRuleActions. Typed alias so the Store layer
// can pass values across method boundaries without round-tripping
// through stringly-typed params; the handler does the conversion at
// the pkg/api ↔ pkg/state boundary.
type AlertAction string

const (
	// AlertActionWebhook — legacy Dispatcher fan-out only. Every
	// pre-PR rule lands here via the column's NOT NULL DEFAULT
	// 'webhook' fast-default.
	AlertActionWebhook AlertAction = "webhook"
	// AlertActionRollback — auto-rollback the rule's app to its
	// previous live deployment via pkg/api.Client.RollbackTo. The
	// orchestrator's ActionDispatcher (Mega PR #2 commit 5)
	// implements this; the evaluator only sees the interface.
	AlertActionRollback AlertAction = "rollback"
	// AlertActionDemote — pin the rule's current canary step at
	// 0% traffic via pkg/api.Client.PatchDeploymentsIdTraffic.
	AlertActionDemote AlertAction = "demote"
	// AlertActionPromote — short-circuit the canary ladder to
	// 100% traffic via pkg/api.Client.PatchDeploymentsIdTraffic.
	AlertActionPromote AlertAction = "promote"
)

// IsValidAlertAction reports whether v is a member of the closed
// AlertAction vocabulary above. The mirror membership helper on
// the wire side is pkg/api.AllowedAlertRuleAction; this one is for
// callers already in pkg/state (the alerts evaluator fan-out at
// pkg/alerts/evaluator.go::runAction) that don't want to import
// pkg/api. The two stay in lockstep — goconst catches drift.
func IsValidAlertAction(v string) bool {
	switch AlertAction(v) {
	case AlertActionWebhook, AlertActionRollback, AlertActionDemote, AlertActionPromote:
		return true
	default:
		return false
	}
}

// UpdateAlertRuleParams carries the optional fields of UpdateAlertRule.
// All fields are pointers; nil means "don't touch". Enabled and
// Threshold intentionally use their own types (bool / float64) since
// the caller must distinguish "leave alone" from "set to false / 0".
// The pointer-to-pointer pattern keeps the Store API narrow without
// falling back on sentinel values.
//
// FailureSource is intentionally absent: the column is derived from
// `metric` via the alert_rules_failure_source_xor_chk constraint, so
// rotating one half in isolation is rejected by the DB. PR 3's
// handler must rotate Metric + FailureSource together by issuing a
// fresh CreateAlertRule if the metric family actually changes, or by
// an explicit dedicated wrapper. Today's UpdateAlertRule will silently
// ignore a FailureSource change, which is a footgun — the field
// exists nowhere on this struct on purpose.
type UpdateAlertRuleParams struct {
	Name       *string
	Enabled    *bool
	Metric     *AlertMetric
	Comparison *AlertComparison
	Threshold  *float64
	WindowSpec *AlertWindowSpec
	// Action (issue #976 / ADR-122 / SAFE-RELEASES-B). Pointer
	// PATCH shape so a missing body field leaves the row alone.
	// Validated against pkg/api.AllowedAlertRuleActions at the
	// handler boundary (cmd/apid/handlers_alerts.go). Storage shape
	// is a plain string in migrations/00481.
	Action              *string
	WebhookURL          *string
	WebhookSecretSealed *[]byte // nil = don't reseal; non-nil replaces
	CooldownMinutes     *int
}

// AlertRule is one customer-configurable rule (issue #396, ADR-045).
// Account-scoped on the FK root; AppID is empty (zero string) for an
// account-wide rule, set otherwise. The webhook secret is at-rest
// sealed (webhookSecretSealed, age/X25519 via pkg/secretbox) and is
// never surfaced on a read — the apid response carries a masked
// constant.
type AlertRule struct {
	ID                  string
	AccountID           string
	AppID               string // empty = account-wide
	Name                string
	Enabled             bool
	Metric              AlertMetric
	Comparison          AlertComparison
	Threshold           float64
	WindowSpec          AlertWindowSpec
	FailureSource       AlertFailureSource // empty unless Metric == failed_invocations
	Action              AlertAction        // issue #976 / ADR-122 / SAFE-RELEASES-B
	WebhookURL          string
	WebhookSecretSealed []byte // age/X25519 ciphertext; never logged
	CooldownMinutes     int
	State               AlertState
	LastFiredAt         time.Time // zero until first fire
	LastEvaluatedAt     time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// AlertDelivery is one delivery attempt record. IdempotencyKey is
// '<ruleID>:<cooldown_bucket>' and is UNIQUE in Postgres — this is the
// dispatcher dedupe primitive (issue #396 criterion 4).
//
// IsTest (ADR-123 PR-D) is true when the row was written by
// Dispatcher.DispatchTest — the customer-facing "send a test
// alert" path. Production fire rows always have IsTest=false.
// The handler's `?include_test=true` toggle on the deliveries
// list endpoint is the only opt-in path to surface test rows in
// the operator pane; the default customer-facing read hides them.
type AlertDelivery struct {
	ID             string
	RuleID         string
	AccountID      string
	AppID          string // empty when the rule was account-wide
	IdempotencyKey string
	Payload        json.RawMessage
	Status         AlertDeliveryStatus
	AttemptCount   int
	LastStatusCode int    // 0 when the attempt never reached the wire
	LastError      string // empty when no error
	ObservedValue  float64
	FiredAt        time.Time
	DeliveredAt    time.Time // zero until status=delivered
	IsTest         bool      // true iff row written by Dispatcher.DispatchTest
}

// AlertDeliveryRow embeds AlertDelivery with a pinned list return —
// used by ListAlertDeliveriesForRule (the dashboard's recent-deliveries
// pane). Kept distinct from AlertDelivery so the Store interface can
// return one or the other without a sentinel split.
type AlertDeliveryRow = AlertDelivery

// AlertRuleQuotaError is returned by CreateAlertRuleIfUnderQuota when
// either cap (per-app or per-account) is reached. Mirrors the cron
// shape (CronQuotaError at store.go:52): distinguishes the two scopes
// so the handler can render different copy ("delete from this app" vs
// "delete from any app on your account").
type AlertRuleQuotaError struct {
	Scope    AlertRuleQuotaScope
	Limit    int
	Observed int
}

// AlertRuleQuotaScope names the cap that CreateAlertRuleIfUnderQuota
// tripped on. A rule with app_id NULL counts toward the per-account
// cap but not the per-app cap.
type AlertRuleQuotaScope string

const (
	AlertRuleQuotaScopeApp     AlertRuleQuotaScope = "app"
	AlertRuleQuotaScopeAccount AlertRuleQuotaScope = "account"
)

func (e *AlertRuleQuotaError) Error() string {
	return fmt.Sprintf("state: alert rule quota exceeded (scope=%s, limit=%d, observed=%d)", e.Scope, e.Limit, e.Observed)
}

// ----------------------------------------------------------------------------
// Outbound webhook delivery (issue #476 / ADR-076)
//
// AppWebhook is the per-app subscription; AppWebhookDelivery is the
// persistent ledger row drained by cmd/schedd's
// pkg/webhook.Dispatcher. The wire format, signing scheme, and
// per-account fairness algorithm live on the dispatcher side; the
// Store only owns the durable shape.
//
// Why a parallel outbound surface (not alert_deliveries):
//   - alert_deliveries is alert-shaped (rule_id, observed_value,
//     idempotency_key keyed on (rule_id, cooldown_bucket)) — its
//     dispatch is synchronous inside meterd and lives for one
//     cool-down window.
//   - app_webhook_deliveries is event-shaped (event name, arbitrary
//     payload, attempt ladder up to 7) — its dispatch is asynchronous
//     inside schedd and lives until the customer POSTs
//     /retry or deletes the subscription.
// Co-locating them would force a union type and break both
// dispatcher's hot-path claim queries. ADR-076 documents this split.
// ----------------------------------------------------------------------------

// AppWebhookEvent is the closed vocabulary on app_webhooks.event_filter.
// An empty filter ([]) means "all events"; non-empty filters accept
// events whose name appears in the array. The vocabulary must stay
// in sync with app_webhook_deliveries.event CHECK in migration 00141.
type AppWebhookEvent string

const (
	AppWebhookEventCronFired   AppWebhookEvent = "cron.fired"
	AppWebhookEventAppDeployed AppWebhookEvent = "app.deployed"
	AppWebhookEventAppScaled   AppWebhookEvent = "app.scaled"
	AppWebhookEventAppParked   AppWebhookEvent = "app.parked"
	AppWebhookEventAppWoken    AppWebhookEvent = "app.woken"
)

// AppWebhookRetryPolicy names the backoff schedule. The dispatcher
// reads this column at claim time and computes next_attempt_at
// against the matching schedule.
//
//   - default:    30s, 2m, 10m, 1h, 6h (5 retries → 7 attempts max)
//   - aggressive: half of each default step
//   - none:       no retries — first 5xx/408/429 lands the row in
//     status='dead' immediately
type AppWebhookRetryPolicy string

const (
	AppWebhookRetryDefault    AppWebhookRetryPolicy = "default"
	AppWebhookRetryAggressive AppWebhookRetryPolicy = "aggressive"
	AppWebhookRetryNone       AppWebhookRetryPolicy = "none"
)

// AppWebhookDeliveryStatus is the dispatcher's state machine on
// app_webhook_deliveries. The closed set matches the migration 00141
// status CHECK; new states land as a controller addition first.
type AppWebhookDeliveryStatus string

const (
	AppWebhookDeliveryPending   AppWebhookDeliveryStatus = "pending"
	AppWebhookDeliveryInFlight  AppWebhookDeliveryStatus = "in_flight"
	AppWebhookDeliverySucceeded AppWebhookDeliveryStatus = "succeeded"
	AppWebhookDeliveryFailed    AppWebhookDeliveryStatus = "failed"
	AppWebhookDeliveryDead      AppWebhookDeliveryStatus = "dead"
)

// UpdateAppWebhookParams carries the optional fields of
// UpdateAppWebhook. Same pointer-to-pointer pattern as
// UpdateAlertRuleParams (types.go:951-976): nil means "don't touch";
// the pointer distinguishes "leave alone" from "set to false / ” /
// 0". WebhookSecretSealed non-nil replaces the sealed secret.
type UpdateAppWebhookParams struct {
	TargetURL           *string
	EventFilter         *[]string // nil = don't touch; non-nil replaces
	RetryPolicy         *AppWebhookRetryPolicy
	Enabled             *bool
	WebhookSecretSealed *[]byte // nil = don't reseal; non-nil replaces
}

// AppWebhook is one per-app subscription row (issue #476 /
// ADR-076). The webhook secret is at-rest sealed
// (SecretSealed, age/X25519 via pkg/secretbox) and is never surfaced
// on a read — the apid response carries a masked constant.
type AppWebhook struct {
	ID           string
	AppID        string
	AccountID    string
	TargetURL    string
	SecretSealed []byte // age/X25519 ciphertext; never logged
	EventFilter  []string
	RetryPolicy  AppWebhookRetryPolicy
	Enabled      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// AppWebhookDelivery is one (event × target) ledger row. The
// dispatcher mutates the row in place on every attempt until
// status='succeeded' or status='dead'. Payload is the wire body the
// customer receives; the dispatcher signs with HMAC-SHA256 over
// "<unix>.<delivery_id>.<body>" using the unsealed secret.
type AppWebhookDelivery struct {
	ID               string
	WebhookID        string
	AppID            string
	AccountID        string
	Event            AppWebhookEvent
	Payload          json.RawMessage
	Attempt          int // 0..7
	Status           AppWebhookDeliveryStatus
	LastError        string
	LastResponseCode int // 0 when the attempt never reached the wire
	NextAttemptAt    time.Time
	DeliveredAt      *time.Time // nil until status=succeeded
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// AppWebhookQuotaError is returned by CreateAppWebhookIfUnderQuota
// when either cap (per-app or per-account) is reached. Mirrors
// AlertRuleQuotaError at types.go:1030-1053.
type AppWebhookQuotaError struct {
	Scope    AppWebhookQuotaScope
	Limit    int
	Observed int
}

// AppWebhookQuotaScope names the cap that
// CreateAppWebhookIfUnderQuota tripped on. A subscription always
// counts toward the per-account cap and its owning app's per-app
// cap (no NULL-app_id shape, unlike alert rules).
type AppWebhookQuotaScope string

const (
	AppWebhookQuotaScopeApp     AppWebhookQuotaScope = "app"
	AppWebhookQuotaScopeAccount AppWebhookQuotaScope = "account"
)

func (e *AppWebhookQuotaError) Error() string {
	return fmt.Sprintf("state: app webhook quota exceeded (scope=%s, limit=%d, observed=%d)", e.Scope, e.Limit, e.Observed)
}

// Is allows errors.Is(err, ErrAlertRuleQuotaExceeded) to match any
// *AlertRuleQuotaError. Mirrors CronQuotaError's Is() contract.
func (e *AlertRuleQuotaError) Is(target error) bool {
	return target == ErrAlertRuleQuotaExceeded
}

// ErrAlertRuleQuotaExceeded is the sentinel callers compare against
// via errors.Is. Distinct from ErrCronQuotaExceeded so a handler
// that distinguishes the two by errors.Is gets a clean match.
var ErrAlertRuleQuotaExceeded = errors.New("state: alert rule quota exceeded")

// CorsPresetQuotaError is returned by
// CreateCorsPresetIfUnderQuota when either cap (per-app or
// per-account) is reached. Mirrors AlertRuleQuotaError's shape:
// the Scope field distinguishes the two so the apid handler can
// render different copy ("delete from this app" vs "delete from
// any app on your account"). A preset with AppID == "" counts
// toward the per-account cap only; an app-scoped preset counts
// toward both.
//
// Plan-tier table is pre-declared at pkg/api/limits.go:601-645
// (PR-A shipped the table; PR-B adds the writer-side enforcer).
// The Free tier declares 0 for every dimension
// (limits.go:1382-1386) so the api.ErrPlanCorsPresetsNotAllowed
// gate fires before CreateCorsPresetIfUnderQuota is reached —
// this type only fires for Hobby/Pro/Scale at-cap.
type CorsPresetQuotaError struct {
	Scope    CorsPresetQuotaScope
	Limit    int
	Observed int
}

// CorsPresetQuotaScope names the cap that
// CreateCorsPresetIfUnderQuota tripped on.
type CorsPresetQuotaScope string

const (
	CorsPresetQuotaScopeApp     CorsPresetQuotaScope = "app"
	CorsPresetQuotaScopeAccount CorsPresetQuotaScope = "account"
)

func (e *CorsPresetQuotaError) Error() string {
	return fmt.Sprintf("state: cors preset quota exceeded (scope=%s, limit=%d, observed=%d)", e.Scope, e.Limit, e.Observed)
}

// Is lets errors.As match the quota error type across the apid
// handler boundary (mirrors AlertRuleQuotaError.Is above).
func (e *CorsPresetQuotaError) Is(target error) bool {
	_, ok := target.(*CorsPresetQuotaError)
	return ok
}

// InvocationSource tags the API surface that originated a row on the
// invocations table (Move 1 — async_invoke / queue / delayed_task / cron).
// Mirrored as a CHECK constraint in migrations/00030_invocations.sql.
type InvocationSource string

const (
	InvocationAsyncInvoke InvocationSource = "async_invoke"
	InvocationQueue       InvocationSource = "queue"
	InvocationDelayedTask InvocationSource = "delayed_task"
	InvocationCron        InvocationSource = "cron"
	// InvocationReplay (issue #315 / tier-2 DX) is the source
	// stamped on a replayed invocation. The dashboard's
	// per-invocation detail page renders this so a customer
	// triaging an incident can tell at a glance whether a row
	// is original or a re-issue. NOT in the read-side render
	// path — it shows up in `gregale invocation <id>` under the
	// `Source:` label.
	InvocationReplay InvocationSource = "replay"
)

// InvocationState is the row lifecycle on the invocations table. The
// allowed transitions are pending→dispatching→completed (happy path)
// and pending/dispatching→failed or pending→cancelled (terminal). The
// CHECK constraint enforces the discrete values; the engine admits
// transitions only through the Store.ClaimInvocation / Complete / Fail /
// Cancel methods.
type InvocationState string

const (
	InvocationPending     InvocationState = "pending"
	InvocationDispatching InvocationState = "dispatching"
	InvocationCompleted   InvocationState = "completed"
	InvocationFailed      InvocationState = "failed"
	InvocationCancelled   InvocationState = "cancelled"
	// InvocationDeadLetter (issue #394 / Move 1) is the terminal state
	// for queue messages that exhausted their per-plan retry budget
	// (see pkg/api.Limits.MaxQueueAttempts). Rows reach this state only
	// via pkg/state.Store.FailInvocation with budget > 0; the drain
	// (pkg/sched/drain.go) is the sole writer in production. The
	// invocations_state_check CHECK constraint (migrations/00060) and
	// the invocations_app_dead_letter_idx partial index back the
	// reader surface (GET /v1/apps/{slug}/queues/dead_letter).
	InvocationDeadLetter InvocationState = "dead_letter"
)

// Invocation mirrors a row on the invocations table. apid writes
// customer-intent rows; schedd's drain loop owns state transitions
// pending → dispatching → completed/failed via the Store.Claim /
// Complete / Fail methods. InstanceID is NULL on the inbound INSERT path
// and is stamped by the drain's claim step (state→dispatching); the
// meter reads it via CountInstanceInvocationsInMinute to set
// usage_minutes.requests.
type Invocation struct {
	ID             string           `json:"id"`
	AppID          string           `json:"app_id"`
	AccountID      string           `json:"account_id"`
	InstanceID     string           `json:"instance_id,omitempty"`
	Source         InvocationSource `json:"source"`
	State          InvocationState  `json:"state"`
	Method         string           `json:"method"`
	Path           string           `json:"path"`
	Payload        json.RawMessage  `json:"payload"`
	Headers        json.RawMessage  `json:"headers"`
	DueAt          time.Time        `json:"due_at"`
	ScheduledAt    *time.Time       `json:"scheduled_at,omitempty"`
	CronID         *string          `json:"cron_id,omitempty"`
	AckURL         string           `json:"ack_url,omitempty"`
	Result         json.RawMessage  `json:"result,omitempty"`
	LeaseExpiresAt *time.Time       `json:"lease_expires_at,omitempty"`
	ReceivedAt     *time.Time       `json:"received_at,omitempty"`
	CompletedAt    *time.Time       `json:"completed_at,omitempty"`
	Attempts       int              `json:"attempts"`
	LastError      string           `json:"last_error,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	// Outcome is the normalized terminal classification (issue #791).
	// nil while the row is non-terminal (pending / dispatching); the
	// read surfaces render nil as "running". See InvocationOutcome.
	Outcome *InvocationOutcome `json:"outcome,omitempty"`
	// DeadlineAt is the absolute hard-stop time for this invocation
	// (ADR-134 PR-B). NULL means "use the plan default"
	// (MaxAsyncInvocationDeadlineSeconds from pkg/api.Limits). The
	// drain's deadline-breach reaper reads this column and forces
	// state='dead_letter' on rows whose deadline_at < now() while
	// still in (pending|dispatching). Wired via pkg/dispatch.Job
	// (accessor .Deadline() below).
	DeadlineAt *time.Time `json:"deadline_at,omitempty"`
	// RetryPolicyJSON is the per-row override of dispatch.RetryPolicy
	// (ADR-134 PR-B). Raw JSON so pkg/state stays free of the
	// dispatch import — pkg/sched unmarshals it lazily in
	// (*Invocation).RetryPolicy() and falls back to the plan default
	// when NULL.
	RetryPolicyJSON json.RawMessage `json:"retry_policy,omitempty"`
	// ResultRetentionUntil is the absolute horizon past which the
	// retention reaper may DELETE this row (ADR-134 PR-B). NULL
	// means "use the plan default"
	// (MaxAsyncResultRetentionSeconds from pkg/api.Limits). The
	// reaper is conservative: rows without an explicit override get
	// MaxAsyncResultRetentionSeconds applied relative to
	// completed_at; only rows whose override has actually elapsed
	// are deleted.
	ResultRetentionUntil *time.Time `json:"result_retention_until,omitempty"`
	// LastReplayedAt (ADR-134 PR-C) is when this row was most
	// recently replayed from a dead_letter parent via
	// POST /v1/apps/{slug}/queues/dead_letter/{id}/replay. NULL
	// until the first replay. Read by the dashboard's
	// "DLQ replay history" view.
	LastReplayedAt *time.Time `json:"last_replayed_at,omitempty"`
}

// RetryPolicy unmarshals RetryPolicyJSON into a pkg/dispatch.RetryPolicy.
// Falls back to a zero-valued RetryPolicy when JSON is missing or
// malformed — pkg/sched treats a zero RetryPolicy as "use the
// plan default" (see pkg/dispatch.(*RetryPolicy).Backoff: returns 0
// for an empty policy). The lazy decode keeps pkg/state free of a
// pkg/dispatch import, which would otherwise invert the layer cake
// (pkg/state is below pkg/sched which is below pkg/dispatch).
func (inv Invocation) RetryPolicy() dispatch.RetryPolicy {
	if len(inv.RetryPolicyJSON) == 0 {
		return dispatch.RetryPolicy{}
	}
	var p dispatch.RetryPolicy
	if err := json.Unmarshal(inv.RetryPolicyJSON, &p); err != nil {
		return dispatch.RetryPolicy{}
	}
	return p
}

// Deadline returns the effective dispatch.DeadlinePolicy for this
// invocation. Today only DeadlineAt is honoured (StartToCloseTimeout
// is a future extension; the wire DTO does not yet expose it). When
// DeadlineAt is nil the returned policy is the Zero value and the
// drain falls back to the plan's MaxAsyncInvocationDeadlineSeconds.
func (inv Invocation) Deadline() dispatch.DeadlinePolicy {
	if inv.DeadlineAt == nil {
		return dispatch.DeadlinePolicy{}
	}
	return dispatch.DeadlinePolicy{
		DeadlineAt:          sql.NullTime{Time: *inv.DeadlineAt, Valid: true},
		StartToCloseTimeout: 0,
	}
}

// ID, AppID, AccountID dispatch.Job accessors cannot live on
// Invocation directly: Go forbids a field and method of the same
// name on the same struct, and those three names already exist as
// fields. The adapter in `invocation_job_adapter.go` wraps
// Invocation and proxies the accessors. Putting the adapter in
// pkg/state (not pkg/sched) keeps the Job shape co-located with
// the row type.
//
// The remaining dispatch.Job methods (Kind, Origin, RetryPolicy,
// Deadline, CurrentAttempts, ErrorText, Snapshot) live on
// Invocation directly because their method names do not collide
// with the existing field names (Origin vs Source, ErrorText vs
// LastError, the rest are derived). The adapter in
// invocation_job_adapter.go inherits them via embedding.

// Kind implements dispatch.Job. Every *state.Invocation is the
// "invocation" JobKind.
func (inv Invocation) Kind() dispatch.JobKind { return dispatch.JobKindInvocation }

// Origin implements dispatch.Job. Mirrors the dispatchable surface
// (async / queue / delayed / cron) so the drain can route on Origin
// without re-reading Source. Named Origin because *state.Invocation
// has a Source field of type InvocationSource.
func (inv Invocation) Origin() string { return string(inv.Source) }

// CurrentAttempts implements dispatch.Job. Mirrors the column-stored
// Attempts counter so the drain's retry budget check can run via the
// dispatch.Job interface without leaking through to state.Invocation's
// struct shape.
func (inv Invocation) CurrentAttempts() int { return inv.Attempts }

// ErrorText implements dispatch.Job. Mirrors LastError. Named
// ErrorText because *state.Invocation has a LastError string field.
func (inv Invocation) ErrorText() string { return inv.LastError }

// Snapshot implements dispatch.Job. Returns the row marshalled as
// JSON so a later replay can reconstruct the row without re-reading
// the table (PR-B's result-retention snapshot column). Returns nil
// when the row fails to marshal — drains that want a non-nil
// snapshot must guard.
func (inv Invocation) Snapshot() []byte {
	b, err := json.Marshal(inv)
	if err != nil {
		return nil
	}
	return b
}

// InvocationOutcome is the normalized, durable classification of a
// terminal invocation (issue #791, migrations/00166). It exists
// because InvocationState collapses every permanent failure into
// InvocationFailed: a gateway 504 (the app blew its deadline) and a
// malformed-payload reject are indistinguishable on `state` alone,
// and recovering the difference by substring-matching LastError is
// brittle — that text is operator-facing, unversioned, and varies by
// call site. The classification is therefore recorded at write time,
// where the caller already knows it.
//
// Written only by Store.CompleteInvocation (always OutcomeSuccess)
// and Store.FailInvocation (via WithOutcome, defaulting to
// OutcomeFailed / OutcomeDeadLetter). Non-terminal rows carry no
// outcome at all.
type InvocationOutcome string

const (
	// OutcomeSuccess is stamped by CompleteInvocation.
	OutcomeSuccess InvocationOutcome = "success"
	// OutcomeFailed is the default terminal classification for a
	// permanent failure with no more specific cause.
	OutcomeFailed InvocationOutcome = "failed"
	// OutcomeTimeout marks a dispatch that exceeded its deadline —
	// a gateway 504 or an expired claim lease. Callers opt in via
	// FailInvocation(..., WithOutcome(OutcomeTimeout)); it is never
	// inferred from LastError.
	OutcomeTimeout InvocationOutcome = "timeout"
	// OutcomeDeadLetter mirrors InvocationDeadLetter: the per-plan
	// retry budget was exhausted. Set automatically by FailInvocation
	// on the dead-letter branch, so callers need not pass it.
	OutcomeDeadLetter InvocationOutcome = "dead_letter"
)

// FailOptions carries the optional, non-breaking extras for
// Store.FailInvocation. It is threaded as a variadic option rather
// than a positional parameter because FailInvocation has ~20 call
// sites across pkg/sched and the test suites; only the two deadline
// paths in the drain need to say anything beyond the default.
type FailOptions struct {
	// Outcome overrides the terminal classification on the permanent
	// branch (retryAfter == 0). Ignored on the transient-requeue
	// branch, which leaves the row non-terminal and therefore
	// outcome-less. The dead-letter branch always wins over this.
	Outcome InvocationOutcome
}

// FailOption mutates FailOptions. See WithOutcome.
type FailOption func(*FailOptions)

// WithOutcome classifies a permanent failure as something more
// specific than OutcomeFailed — in practice OutcomeTimeout, passed by
// the drain's deadline paths. A no-op when the row takes the
// transient-retry or dead-letter branch.
func WithOutcome(o InvocationOutcome) FailOption {
	return func(f *FailOptions) { f.Outcome = o }
}

// ApplyFailOptions folds opts over the defaults. Exported so both
// store backends derive the effective options identically.
func ApplyFailOptions(opts []FailOption) FailOptions {
	f := FailOptions{Outcome: OutcomeFailed}
	for _, opt := range opts {
		if opt != nil {
			opt(&f)
		}
	}
	if f.Outcome == "" {
		f.Outcome = OutcomeFailed
	}
	return f
}

// QueueStats is the projection returned by Store.QueueState (issue
// #394 / Move 1 dead-letter). It is the read-side mirror of the three
// counters apid's GET /v1/apps/{slug}/queues/state surfaces to the
// customer:
//
//	Depth:          pending + dispatching rows on the per-app queue.
//	                Same numerator the apid cap check uses on POST
//	                .../queues/send (CountPendingInvocations), so the
//	                two numbers cannot disagree without a race.
//	InFlight:       dispatching rows with lease_expires_at either
//	                NULL or in the future. A row whose lease has
//	                expired is treated as effectively pending again —
//	                the next drain tick will re-claim it. Mapping
//	                makes "in_flight" a tight upper bound on the
//	                worker queue, excluding zombie leases.
//	OldestPendingAt: zero-time when no pending rows exist. cmd/apid
//	                  translates this to a nil pointer + omitempty
//	                  on the JSON wire so dashboards can render
//	                  "queue is empty" cleanly.
type QueueStats struct {
	Depth           int
	InFlight        int
	OldestPendingAt time.Time
}

// GdprAction enumerates the GDPR self-service actions recorded in
// the gdpr_requests ledger. The DB CHECK constraint enforces these
// three values; exporting the constants avoids typo bugs in apid +
// schedd callers.
type GdprAction string

const (
	GdprActionExport  GdprAction = "export"
	GdprActionDelete  GdprAction = "delete"
	GdprActionRestore GdprAction = "restore"
)

// GdprRequest is one row of the gdpr_requests ledger. Inserted on
// the customer-facing path; completed_at is stamped after the
// downstream action (export bundle returned, DeleteAccount fired,
// restore succeeded). The ledger is INSERT-only from the application
// side; the table survives the account's DeleteAccount so a DPO can
// audit completed erasure against an email + timestamp.
//
// RequestID carries the inbound X-Request-Id (PR-5.2 / issue #755)
// when the customer supplied one — used to make GET /v1/account/export
// idempotent across retries so a flaky network doesn't double-cost the
// 24h rate-limit window. Zero value ("") means the customer did not
// supply an id; for non-export actions the column is also zero. The
// id is stored as a text column (no FK to anywhere) so a regenerated
// request id from a future process does not orphan historical rows.
type GdprRequest struct {
	ID           string
	AccountID    string
	AccountEmail string
	Action       GdprAction
	RequestedAt  time.Time
	CompletedAt  time.Time // zero until the downstream action completes
	RequestID    string    // optional X-Request-Id from the inbound request (PR-5.2)
}

// Instance mirrors the instances row; schedd is the sole writer (spec §6).
type Instance struct {
	ID            string
	AppID         string
	DeploymentID  string
	State         string
	Netns         string
	GuestUID      int
	HostIP        string
	RAMMB         int
	StartedAt     time.Time
	LastRequestAt time.Time
	ParkedAt      time.Time
	// TerminalAt is stamped by Engine.transition on the same UPDATE that
	// writes state = 'stopped' or 'failed' (PR #74, spec §17 follow-up).
	// It is the dedicated retention anchor: started_at means "row
	// creation" and parked_at is overloaded (also means "entered
	// PARKED"). A STOPPED row whose vmmd boot succeeded 25 days ago
	// has a stale started_at; terminal_at is the only correct age.
	// The retention sweep (pkg/sched.Retention) DELETEs rows where
	// state ∈ {STOPPED, FAILED} AND terminal_at < now-30d.
	TerminalAt *time.Time
	// NodeID is the compute_node the instance lives on
	// (issue #97 / ADR-025 axis 3). Set by Engine.Wake via
	// sched.ChoosePlacement at instance creation; read by Park /
	// snapshotAndPark to route the vmmd RPC through the right
	// target URL. NOT NULL enforced by migrations/00024_compute_nodes;
	// pre-existing rows were backfilled to DefaultLocalNodeID.
	// Empty in test fixtures only when the fixture is exercising a
	// pre-#97 code path that predates the column add.
	NodeID string
	// WakeID is the per-wake-attempt correlation handle (gaps analysis
	// 2026-07-23). Distinct from ID (the row PK): every fresh WAKING /
	// COLD_BOOTING transition mints a new UUIDv7 Go-side in schedd's
	// Engine.Wake so a single instance row can carry many wake_ids over
	// its lifetime as the app parks and wakes again. UUIDv7 (time-ordered)
	// picked over UUIDv4 so the partial index `(app_id, wake_id)` on the
	// dashboard's recent-wakes scan serves time-range queries without a
	// separate sort. NOT NULL enforced by migrations/00028_instances_wake_id;
	// pre-existing rows were backfilled to gen_random_uuid() (v4) on apply.
	// Empty in test fixtures only when the fixture predates the column add.
	WakeID string
	// MigratedFromNodeID is the prior owner compute_node after a
	// cross-node live-instance handoff (Tier A5, migration 00097,
	// ADR-068, follow-up to ADR-064). FK to compute_nodes(id) ON
	// DELETE SET NULL — the lineage reference stays honest when a
	// node is decommissioned (the row stays, the column flips to
	// NULL). Distinct from apps.node_id (the durable shard key for
	// the app itself) and from instances.node_id (the current
	// owner of this specific instance row). All three are nullable
	// in different shapes: apps.node_id is NOT NULL post-Phase 2,
	// instances.node_id is NOT NULL post-00024, and
	// migrated_from_node_id is nullable forever (a fresh instance
	// has no migration history). Set on the conditional UPDATE at
	// Phase 3 of the four-phase handoff, read by the dashboard's
	// "fleet migrated-from" panel via the
	// instances_migrated_from_node_id_idx partial index.
	MigratedFromNodeID *string
	// MigratedAt is the wall-clock stamp of the commit (Phase 3
	// of the four-phase handoff). The clock-skew CHECK tolerates
	// `migrated_at <= now() + interval '1 minute'`; values clearly
	// in the future still error loud (23514). Nullable forever.
	MigratedAt *time.Time
	// LeaseToken is the per-migration UUID minted by the new owner
	// at Phase 1 of the four-phase handoff. It is part of the
	// conditional-UPDATE predicate at Phase 3 (commit), so a peer
	// claim can never silently succeed with a stale lease. The
	// column is also the lookup key for the dying vmmd's
	// pause-resume lease bookkeeping: when the new owner aborts
	// (Phase 4 cancel), the dying vmmd resumes the VM on lease
	// expiry. Mirrors the A4 `apps.reassigned_at` schema
	// discipline. Nullable forever.
	LeaseToken string
	// FrameworkReadyAt is the wall-clock stamp the vmmd records
	// when the guest-init signals "framework ready" via vsock DGRAM
	// port 1027 (msg=4). Two-tier snapshot (issue #470, PR
	// #470-FU-B): the engine waits on this column BEFORE issuing
	// the second PauseAndSnapshot that captures the warm tier, so
	// the warm snapshot is captured AFTER the framework has
	// compiled routes, primed JIT, populated ORM pools, etc.
	// Without this stamp the engine would snapshot the app the
	// moment it returned from `waitReady` -- framework listeners
	// cold, JIT at tier 0, route tables empty -- and warm-tier
	// restore would pay the framework-warmup cost on every wake.
	// Nullable forever: legacy rows never had a vsock signal;
	// Free/Hobby plans never opt in; instances that flipped
	// warm-snapshot off mid-flight stay null. The engine treats
	// null as "no warm capture available, fall through to init
	// tier". The vmmd gRPC `FrameworkReady` RPC writes the column
	// (PR #470-FU-B); the engine reads it via the existing
	// `e.store.GetInstance(ctx, id)` path. NOT NULL NOT enforced
	// (no CHECK constraint in migration 00112). Pre-existing rows
	// are backfilled with the migration's DEFAULT NULL.
	FrameworkReadyAt *time.Time
	// TailCount is the in-flight waitUntil(promise) task count
	// for this instance (issue #667 / ADR-078). Incremented by
	// the runner each time ctx.waitUntil(promise) is called and
	// decremented when the task reaches a terminal outcome
	// (completed / failed / timeout). Persisted in the `tail_count`
	// column added by migrations/00151_wait_until_tail.sql and
	// mirrored here so schedd's reaper can read it without a
	// second SQL hop. The schedd reaper treats instances with
	// tail_count > 0 as NOT idle-eligible — the wake stays in
	// RUNNING until the runner drains its tail tasks or the
	// snapshotAndPark 5s watchdog fires (PR 4 wires that gate).
	// Tail count is a column, not a state; the state machine is
	// untouched. NOT NULL DEFAULT 0 enforced by migration 00151;
	// pre-existing rows are backfilled to 0 on apply.
	TailCount int
	// RequestCount is the per-instance monotonically-increasing
	// request counter (ADR-098 C8/C9/C10). Persisted in the
	// `request_count` column added by migrations/00221_instances_request_count.sql.
	// The counter is the gate for warm-snapshot promotion (C10):
	// when count >= WarmSnapshotMinRequests (per-app config), the
	// captured snapshot is promoted to a permanent warm key. Bigint
	// even at 100 RPS sustained — a 73-day-running instance
	// accumulates ~6.3e8 rows; int4's 2.1e9 ceiling would be the
	// next upgrade cycle's blocker. Mirrored here so the warm-gate
	// reads request_count alongside TailCount without a second SQL
	// hop. NOT NULL DEFAULT 0 enforced by migration 00221.
	RequestCount int64
	// Mode (issue #72 / ADR-125) tags an instance as 'mirror' when
	// the schedd created it for a mirror invocation rather than a
	// customer-facing wake. The pkg/meter sampler skips mode='mirror'
	// rows so the customer is never billed for the shadow VM (spec
	// §4.7 carve-out: shadow VMs are not customer-served). The reaper
	// also skips mode='mirror' rows for idle-reap because mirror VMs
	// self-park on request completion — there's no idle lifetime to
	// reap. Two-value closed vocabulary ('normal' default + 'mirror');
	// the CHECK constraint on the SQL column (migrations/00349)
	// enforces it. Default 'normal' on pre-feature rows so no
	// existing customer is affected.
	Mode string
	// Kind (issue #1184 Workstream A / ADR-099) discriminates
	// app VMs from job-task VMs. Closed vocabulary enforced at
	// the SQL layer by migration 00578's
	// instances_kind_check CHECK constraint:
	//   - "" / "app_task"  — legacy app VM (the default)
	//   - "job_task"       — job-task VM (one per active job_task row)
	// Empty string on pre-Mega-1 rows; the canonical constant
	// is KindApp = "" and KindJobTask = "job_task" (see
	// pkg/state/instances_kind.go for the typed constants).
	// The pkg/meter sampler keys off this field to decide which
	// AppendUsage path to take (job rows set AppID="" and rely
	// on JobID for billing attribution).
	Kind string
	// JobID is the jobs.id (== job_runs.job_id, NOT job_runs.id;
	// JobRunID below carries the run PK) for kind="job_task"
	// instances. Empty for app VMs. The meter sampler uses this
	// to compute per-job billing attribution; a null JobID on a
	// kind="job_task" row is a contract violation surfaced by
	// the meterd sanity check (MeterdHealth) at startup.
	JobID string
	// JobRunID is the job_runs.id (parent of the task that
	// spawned this instance). Distinct from JobID (the job
	// definition). Empty for app VMs.
	JobRunID string
	// JobTaskIndex is the per-run task ordinal that
	// (job_run_id, job_task_index) maps back to a job_tasks row.
	// Empty for app VMs.
	JobTaskIndex int
}

// InstanceMode (issue #72 / ADR-125 + issue #1186 / ADR-137) is the
// closed vocabulary for the `instances.mode` column. The string
// values match the migrations/00385 (initial) + 00570 (M-2 widening)
// CHECK constraints; main's 00577 adds 'job' (PR #1195) and was
// absorbed into the M-2 superset widening {normal, mirror, worker,
// service, job} so M-2 lands as a single widening, not a tightening.
// The sampler, reaper, and schedd engine compare against these
// constants rather than literal strings so a future widening lands
// as a compile error at every callsite.
//
// The closed set is {normal, mirror, worker, service, job} after M-2
// commit 4 lands. `normal` and `mirror` are the legacy two-value
// shape (ADR-125); `worker`, `service`, `job` are the M-2 / ADR-137
// execution-mode axis mirrored on the instance row. The Go-side
// constants here are the single source of truth — every call site
// compares via these constants or via the IsMeteredSkippableMode /
// CountsForRAMByMode helpers (pkg/state/machine.go).
type InstanceMode string

const (
	// InstanceModeNormal is the default for every customer-facing
	// wake. The schedd engine stamps this at instance creation
	// unless the WakeRequest carries is_mirror=true or the app's
	// execution_mode is worker/service/job (issue #1186 §D / ADR-137).
	InstanceModeNormal InstanceMode = "normal"
	// InstanceModeMirror tags an instance that the schedd created
	// to serve a mirror invocation (issue #72 / ADR-125). The
	// customer is never billed for this row; the reaper does not
	// idle-reap it (the request-completion path self-parks it).
	InstanceModeMirror InstanceMode = "mirror"
	// InstanceModeWorker tags an instance whose app is
	// execution_mode='worker' (ADR-137). Long-running, no public
	// port, idle-reap exempt, billed at the standard mb_seconds
	// rate (no skip). Created by schedd commit 6.
	InstanceModeWorker InstanceMode = "worker"
	// InstanceModeService tags an instance whose app is
	// execution_mode='service' (ADR-137). Replicated per
	// deployment.service_replicas; the engine maintains
	// desired-count via replacement wakes (commit 6).
	InstanceModeService InstanceMode = "service"
	// InstanceModeJob tags an instance that the schedd created to
	// run a single job task (issue #1184 Workstream A / ADR-099
	// supplement + ADR-137). Unlike mirror, a job VM IS
	// billable — the sampler path counts it like a normal VM;
	// the only mode-specific branch is the per-account
	// JobConcurrentByAccount quota gate (which uses kind=
	// 'job_task', not mode). For apps with execution_mode='job'
	// (ADR-137) the run-to-completion RestartPolicy default 'no'
	// applies; billed at standard rate while RUNNING.
	InstanceModeJob InstanceMode = "job"
)

// MirrorRule (issue #72 / ADR-125) is the customer-intent row
// that links a live source_deployment to a live mirror_deployment.
// Every customer request served by source is duplicated to mirror
// asynchronously; the customer always sees source's response. The
// (source, mirror) pair is enforced as distinct by the SQL CHECK
// (migrations/00348); percent + enabled + include_body + redact_headers
// give the customer the levers they need without exposing the
// implementation (wake coord, detached ctx, JCS canonicalization).
//
// `RedactHeaders` is the customer's additive redact-list beyond
// the always-stripped set (Authorization / Cookie / Set-Cookie /
// X-API-Key / Proxy-Authorization / WWW-Authenticate). The gateway
// consults both lists at runMirror time.
//
// MirrorRules are owned by apid (the customer-intent writer per
// the spec component-ownership rule). schedd + gatewayd-internal
// read them via the in-process cache refreshed on the
// deployment_changed pg_notify payload's `kind="mirror"`
// discriminant.
type MirrorRule struct {
	ID                 string
	AccountID          string
	AppID              string
	SourceDeploymentID string
	MirrorDeploymentID string
	Percent            int
	Enabled            bool
	IncludeBody        bool
	RedactHeaders      []string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// MirrorRulePatch (issue #72 / ADR-125) is the partial-update
// shape for PATCH /v1/apps/{slug}/mirrors/{id}. Pointer fields
// let the handler distinguish "field absent" from "field set to
// zero value" — the latter is rare but legal (e.g. Percent=0
// disables the rule without removing it).
type MirrorRulePatch struct {
	Percent       *int
	Enabled       *bool
	IncludeBody   *bool
	RedactHeaders *[]string
}

// CreateMirrorRuleParams (issue #72 / ADR-125) is the parameter
// shape for CreateMirrorRuleIfUnderQuota. The store stamps id,
// created_at, and updated_at; everything else comes from the
// caller. RedactHeaders is non-nil even on the empty case
// (the SQL DEFAULT is '{}' so an empty slice round-trips
// identically); nil and []string{} are both accepted by the
// pgstore via the same array_length check.
type CreateMirrorRuleParams struct {
	AccountID          string
	AppID              string
	SourceDeploymentID string
	MirrorDeploymentID string
	Percent            int
	Enabled            bool
	IncludeBody        bool
	RedactHeaders      []string
}

// MirrorInvocationResult (issue #72 / ADR-125) is one row in the
// per-request comparison ledger (migrations/00350). The gateway
// stamps this on every mirror invocation: status, latency, schema
// hash, body hash, crash flag, plus the source-side counterparts
// so a comparison query doesn't need a second table. Pre-computed
// status_diff / schema_diff / body_diff booleans let the summary
// endpoint SUM these columns instead of comparing values client
// side — the customer's read path stays O(1) per row.
//
// All *bytea fields are 32 bytes (SHA-256). Go-side: `[]byte`
// with len==32, OR nil when the rule has include_body=false (the
// `body_hash` columns are the only ones that can be nil — the
// schema_hash columns are always populated for JSON responses).
type MirrorInvocationResult struct {
	ID                 string
	MirrorRuleID       string
	AccountID          string
	AppID              string
	SourceDeploymentID string
	MirrorDeploymentID string
	InstanceID         string
	SourceInstanceID   string
	StatusCode         int
	SourceStatusCode   int
	LatencyMs          int
	SourceLatencyMs    int
	BodyHash           []byte
	SourceBodyHash     []byte
	SchemaHash         []byte
	SourceSchemaHash   []byte
	StatusDiff         bool
	SchemaDiff         bool
	BodyDiff           bool
	Crashed            bool
	RequestID          string
	CompletedAt        time.Time
}

// MirrorSummary (issue #72 / ADR-125) is the aggregate the
// GET /v1/apps/{slug}/mirrors/{id}/summary endpoint returns over
// a window. Computed by the store via SQL aggregates
// (COUNT/SUM/AVG/p99_cont) — never by client-side iteration.
// `MeanLatencyDiffMs` is signed (mirror_ms - source_ms, positive
// = mirror is slower). `P99LatencyDiffMs` is signed and is the
// operator's drift signal.
type MirrorSummary struct {
	TotalInvocations  int
	StatusDiffCount   int
	SchemaDiffCount   int
	BodyDiffCount     int
	MeanLatencyDiffMs int
	P99LatencyDiffMs  int
	CrashCount        int
	WindowSeconds     int
}

// ComputeNode is one vmmd host in the fleet (issue #97 / ADR-025 axis
// 3). schedd's single-leader CP owns placement across N rows; the
// legacy single-host deployment has exactly one row (the synthetic
// 'default-local' seeded by migrations/00024_compute_nodes.sql).
// Operators register additional nodes via cmd/apid's
// POST /v1/compute-nodes admin endpoint; the heartbeat loop in
// cmd/schedd/main.go keeps LastHeartbeatAt fresh on a tick.
//
// The struct's field names track the SQL columns 1:1; Active == false
// is a runtime "drained" flag (placement skips), distinct from a row
// delete (re-registration is idempotent on conflict).
//
// Region / Zone are nullable locality labels added by
// migrations/00069_compute_nodes_region_zone.sql. The chooser
// (pkg/sched/ChoosePlacement) uses them as a secondary tie-break
// when two nodes have equal RAM headroom. Pointer types so a SQL
// NULL round-trips as nil rather than collapsing into "" — that
// distinction matters when the chooser compares for ordering and
// the seeded default-local row is backfilled to ('local','local')
// in the migration so the single-box deploy has a deterministic
// ordering.
//
// PublicIp / PublicIpSetAt were added by migration 00174 (PR A8
// multi-IP work) to the SQL schema but historically not surfaced
// on this Go struct — sqlc-generated models.go carries them. PR-3a
// adds them here to close the latent drift so the consumer-shaped
// pgstore scanner can return them.
//
// ReleaseID / ManifestHash / HostCertificate / CertFingerprint / Role
// / Generation are nullable columns added by migration 00266
// (issue #911 / ADR-110 PR-3a) for release-bundle storage. PR-3
// (release bundle install) stamps ReleaseID + CertFingerprint; PR-2
// renderer stamps ManifestHash + Role; PR-X secrets init stamps
// HostCertificate + CertFingerprint; PR-4 doctor bumps Generation
// on mismatch. The pointer types mirror Region/Zone (a SQL NULL
// round-trips as nil, never collapsing into "").
type ComputeNode struct {
	ID        string
	Name      string
	TargetURL string // wire.ParseTarget-compatible — the vmmd dial target (Firecracker + jailer)
	// GatewayTargetURL is the private HTTP endpoint for this node's
	// gatewayd-internal listener. It is separate from TargetURL because the
	// latter is the vmmd gRPC endpoint and the two services may use different
	// ports, certificates, or network paths. nil means the node is not yet
	// eligible for public data-plane ingress.
	GatewayTargetURL   *string
	VPCPUs             int
	MemMB              int
	MaxConcurrency     int
	AdmissionCeilingMB int
	// VCPUBudget is the per-node vCPU admission ceiling (migration
	// 00123, Tier A2). schedd's NodeLedger checks vCPU against
	// this value rather than the legacy box-wide api.VCPUSlots.
	// Defaults to 160 (api.VCPUSlots) on the synthetic default-local
	// row seeded by migration 00024; operators tune it per-node in
	// a heterogeneous fleet (a smaller box gets a smaller budget).
	VCPUBudget      int
	Active          bool
	LastHeartbeatAt time.Time
	CreatedAt       time.Time
	// Region is a free-form locality label (e.g. "eu-fsn1", "local").
	// nil means the row was inserted before 00069 OR the operator
	// didn't set a region on registration. The chooser treats nil
	// and "" identically.
	Region *string
	// Zone is the finer-grained locality inside a region. Currently
	// informational; pkg/sched/ChoosePlacement uses it as a tertiary
	// tie-break after (headroom DESC, region ASC, name ASC).
	Zone *string
	// ScheddTargetURL is the per-node schedd gRPC dial target
	// (Phase 2 / Gate A, migration 00090). Distinct from
	// TargetURL which is the vmmd dial target. gatewayd-internal reads
	// this to lazily dial the owner schedd for a customer
	// request; the per-node dial cache is keyed by node_id and
	// refreshed through the compute_node_changed pg_notify.
	// Nullable for legacy operator-added rows: schedd startup
	// treats nil as "not yet configured" and refreshes from the
	// cache. The CHECK constraint on the column admits only
	// (unix|tcp):// schemes (the wire.ParseTarget shape). The
	// synthetic default-local row is backfilled to
	// 'unix:///run/faas/schedd.sock' by migration 00090 so
	// single-box installs preserve bit-for-bit behaviour.
	ScheddTargetURL *string
	// PublicIp is the per-node public-facing IP for the multi-IP
	// (PR-A8) bring-up path. nil for the synthetic default-local
	// row + pre-00174 operator rows. Reuses pkg/netip's value type
	// because the column stores a Postgres INET.
	PublicIp *netip.Addr
	// PublicIpSetAt is the wall-clock time PublicIp was last set
	// (migration 00174). nil for rows that haven't been assigned a
	// public IP yet (single-box installs stay on the legacy dial path).
	PublicIpSetAt *time.Time
	// ReleaseID is the release_bundles.git_sha this node claims
	// membership in (PR-3a, ADR-110). nil = pre-bundle row, or a
	// node registered against a release that hasn't been bundled
	// yet. PR-3 release install stamps this on UPSERT.
	ReleaseID *string
	// ManifestHash is the sha256:<64hex> hash of the manifest the
	// PR-2 renderer materialised on this node (PR-3a, ADR-110).
	// nil = pre-manifest row. PR-4 doctor compares this against
	// release_bundles.manifest_hash for the ReleaseID to detect
	// manifest drift between fleet boxes.
	ManifestHash *string
	// HostCertificate is the PEM-encoded leaf certificate for this
	// node (PR-3a, ADR-110). nil = pre-PR-X or pre-cmd/hostage-gen
	// row. PR-4 doctor reads to verify the cert on disk matches
	// CertFingerprint.
	HostCertificate *string
	// CertFingerprint is the sha256:<64hex> fingerprint of
	// HostCertificate (PR-3a, ADR-110). nil until PR-X secrets
	// init or cmd/hostage-gen stamps it. PR-3 release install
	// and PR-4 doctor compare against pkg/pki.LoadCertificateFingerprint
	// at mTLS handshake time.
	CertFingerprint *string
	// Role is the per-node role label: control-plane | compute-node
	// (PR-3a, ADR-110). Populated from manifest.fleet.hosts[].role
	// by PR-2 renderer. nil = pre-manifest row.
	Role *string
	// Generation is a monotonic counter bumped by PR-4 doctor on
	// per-node inconsistency detection (PR-3a, ADR-110). nil on
	// pre-PR-3a rows (treated as 0); never decreases.
	Generation *int
}

// ComputeNodeHeartbeat is one row in the append-only
// compute_node_heartbeats table (CP-1, migration 00065). The
// schedd Heartbeat.Tick goroutine writes one row per successful
// ping; the operator's GET /v1/compute-nodes/{name}/heartbeats
// endpoint reads from this table.
//
// Source is the enum-shaped stamp trigger:
//   - "heartbeat_tick"  — the routine stamp path (every successful
//     ping; this is the dominant row).
//   - "deactivation"   — the watchdog's last contact attempt before
//     flipping active=false (the deactivation event row).
//   - "reactivation"   — the recovery path (a previously drained
//     node whose ping succeeded again).
//
// The CHECK constraint on the column keeps the set Go-shaped.
type ComputeNodeHeartbeat struct {
	ID              int64
	NodeID          string
	ReceivedAt      time.Time
	LastHeartbeatAt time.Time
	Source          string
	// CPUPct60s is the 60-second sliding-window CPU utilization as
	// a percentage of vpcpus × 100 (PR #4 / ADR-091 §3.6 amendment).
	// Nil when the row predates migrations/00199 or vmmd hadn't
	// sampled yet. Bounded [0.00, 100.00] for sane values.
	CPUPct60s *float64
	// DiskUsedBytes is the byte size of /srv/fc/snapshots + spool
	// scratchpad at heartbeat-mint time (PR #4). Nil when the row
	// predates the migration or vmmd hadn't sampled yet.
	DiskUsedBytes *int64
}

// ComputeNodeHeartbeatStats is the read shape for LatestHeartbeatStats
// (PR #4). One row per compute node; NodeID + the latest heartbeat's
// stats fields. The handler folds this onto the ObsNodeRow projection.
type ComputeNodeHeartbeatStats struct {
	NodeID        string
	ReceivedAt    time.Time
	CPUPct60s     *float64
	DiskUsedBytes *int64
}

// PerNodeStats is one row of the per-node live-stats aggregate (PR #4).
// NodeName is the compute_nodes.name. The counts and RAM sums are over
// instances whose binding row has released_at IS NULL (i.e. the VM is
// currently hosted on that node). The +8 on ram_mb mirrors §6.2
// invariant #2 — Σ(ram_mb + 8) ≤ 47,600 MB — so the per-node RAMUsedMB
// is the operator-side number that adds up to the fleet ceiling.
type PerNodeStats struct {
	NodeName             string
	InstancesLive        int64
	InstancesRunning     int64
	InstancesWaking      int64
	InstancesColdBooting int64
	RAMUsedMB            int64
}

// OperatorCapacityNode is the bounded read-side capacity projection used by
// the operator console. It deliberately contains counts and resource
// numbers, not app or instance rows, so the projection remains cheap as the
// fleet grows.
type OperatorCapacityNode struct {
	ID                   string
	Name                 string
	Active               bool
	VPCPUs               int
	VCPUBudget           int
	MemMB                int
	AdmissionCeilingMB   int
	InstancesLive        int64
	InstancesRunning     int64
	InstancesWaking      int64
	InstancesColdBooting int64
	RAMUsedMB            int64
	AppsCount            int64
	TenantsCount         int64
}

// OperatorCapacitySnapshot is the fleet-wide capacity projection. AppsTotal
// and TenantsTotal are exact distinct counts across the fleet; per-node
// tenant counts are placement counts and may intentionally overlap when a
// tenant owns apps on more than one node.
type OperatorCapacitySnapshot struct {
	Nodes        []OperatorCapacityNode
	AppsTotal    int64
	TenantsTotal int64
	UnplacedApps int64
}

// InstanceTouch is one entry in a last_request_at flush batch (spec §4.1). The
// gateway accumulates these in memory and hands them to schedd every 15 s.
type InstanceTouch struct {
	InstanceID  string
	LastRequest time.Time
	// RequestDelta (ADR-098 C9) is the per-instance request count
	// delta the gateway has observed since the last touch. The
	// gateway's per-instance cache (Target.RequestCount) is the
	// authoritative hot path; the engine batched-writer flushes
	// per-instance deltas into the instances.request_count column
	// in the same transaction as last_request_at. 0 = no delta
	// (the gateway observed a request but the per-instance counter
	// already moved — the explicit zero avoids a no-op UPDATE).
	// The increment is additive ("request_count = request_count +
	// delta") so a re-delivered batch is idempotent on
	// Phase-4-loser re-applies, mirroring the writer in
	// pkg/state/pgstore.go::IncInstanceRequestCount.
	RequestDelta int64
}

// Event is one row in the append-only audit log (spec §6.1).
//
// TraceID is the OTel W3C 32-char hex trace identifier (when set)
// that ties this event to the inbound request, the operator_intents
// row it produced (enqueue ↔ outcome), and any downstream
// dispatch context. Nullable: pre-PR #TBD rows + cron-fired
// rows without an inbound trace_id keep NULL. Reads of TraceID
// stay scoped to paths that explicitly opt in (e.g.
// ListEventsWithTraceID used by /v1/admin/obs/health); the
// canonical ListEvents SELECT does not include the column for
// backwards compatibility with hot-path readers.
type Event struct {
	ID      int64
	At      time.Time
	Actor   string
	Kind    string
	Subject *uuid.UUID
	TraceID *string
	Data    json.RawMessage
}

// WakeBootMeta (ADR-123) is the typed projection of the wake-boot
// telemetry stamped on wake.boot_started / wake.boot_completed event
// rows (events.data jsonb). Source: pkg/events.BootStarted.Payload().
// Used by the dashboard's "Recent wakes" table and the per-app
// wake-timeline view to render "why did this instance start?"
// without a separate client-side join.
//
// PR-A extends the projection with two more fields that close the
// user's "2/2 at concurrency limit" + "Ready in: 112 ms" reference
// lines:
//
//   - AtCapacity is the bool stamped on wake.boot_started by
//     pkg/sched.Engine.admitGate's wakeAdmit branch (true when the
//     pre-admit ledger reading was maxConc-1). Always populated in
//     PR-A fleet rows; defaults to false for pre-PR-A rows via the
//     pgstore's COALESCE.
//
//   - AtCapacityPresent distinguishes "jsonb key absent" (pre-PR-A
//     fleet row that lacks the at_capacity key entirely) from "jsonb
//     key present and explicitly false" (PR-A row that was admitted
//     below the cap). The dashboard's em-dash-on-absent convention
//     (render.go / wake_timeline.go) requires this distinction so a
//     pre-PR-A row renders "—" (we don't know) instead of "No" (we
//     know it wasn't at the cap). Source: pgstore's
//     `data ? 'at_capacity'` jsonb contains operator (NULL when the
//     key is absent). Defaults to false when nil (treat as absent).
//
//   - ReadyInMS is the wall-clock duration between boot_started.at
//     and the matching boot_completed.at, computed in SQL with
//     EXTRACT(EPOCH FROM (delta)) * 1000 (NOT EXTRACT(MILLISECONDS …)
//     — that's silently wrong for >=60s deltas because PostgreSQL
//     intervals are stored as months/days/seconds; EXTRACT(MILLISECONDS
//     …) returns only the seconds-field milliseconds). Zero when no
//     boot_completed row exists yet (wake still booting or rejected);
//     the template renders em-dash on zero per the existing
//     absent-value convention.
type WakeBootMeta struct {
	Trigger            string // pkg/sched/triggers.go closed enum; "" if absent
	QueuedCount        int    // ledger.Concurrency at admit; 0 if absent
	ConcurrencyAtAdmit int    // same reading; 0 is the cold-start case
	AtCapacity         bool   // PR-A — true when admitted at the plan's per-app MaxConcurrency ceiling
	AtCapacityPresent  bool   // PR-A — true when the at_capacity key was present in jsonb (false = absent; em-dash)
	ReadyInMS          int    // PR-A — wall-clock boot_started → boot_completed delta in ms; 0 if still booting or rejected
}

// AuditLog is one row of the FK-free, immutable post-deletion evidence
// table (migrations/00163_audit_log.sql, issue #755 / PR-5). The row
// outlives the account it relates to so a DPO / regulator can re-derive
// the post-deletion state without joining back to a deleted accounts row.
//
// Distinct from Event on two load-bearing axes:
//
//   - account_id is nullable (anonymous / background activity can emit
//     rows), and is not FK-bound — a deleted accounts row does not
//     cascade the audit_log row.
//   - account_email is captured at copy-time so the audit row is
//     self-contained: a regulator reading a row for a UUID that no
//     longer exists in accounts still sees the human identifier.
//
// Stored shape matches the migration: UUID PK, TEXT kind, nullable UUID
// account_id, nullable TEXT account_email + actor, NOT NULL TIMESTAMPTZ
// received_at with default now(), nullable JSONB data.
type AuditLog struct {
	ID           uuid.UUID
	Kind         string
	AccountID    *uuid.UUID // nullable; survives account deletion
	AccountEmail string     // captured at copy-time; empty when anonymous
	Actor        string     // optional; "" when the emitter is anonymous
	ReceivedAt   time.Time
	Data         json.RawMessage // nullable; verbatim payload at emit time
	// TraceID is the OTel W3C 32-char hex trace identifier (when
	// set) that ties this audit_log row to the inbound request, the
	// operator_intents row it produced, and any downstream dispatch
	// context. Mirrors Event.TraceID (migrations/00486). Nullable:
	// pre-PR rows + cron-fired rows without an inbound trace_id keep
	// NULL. The OperatorActionTraceCompleteness read aggregates the
	// NOT-NULL ratio over audit_log rows of kind LIKE
	// 'operator.action.%'.
	TraceID *string
}

// DeploymentAuditKind is the closed-set vocabulary enforced by
// deployment_audit_kind_chk on the deployment_audit table
// (migrations/00332_deployment_audit.sql, issue #976 / ADR-122 /
// SAFE-RELEASES-E.2). The Go type prevents drift between the
// handler-level emit sites and the SQL CHECK constraint — every
// kind the meterd orchestrator (Mega PR #2) or the apid CreateDeployment
// path emits must be one of these constants.
//
// Closed set (8 kinds):
//   - DeployCreated:     apid CreateDeployment path + 90-day backfill
//     rename of legacy app.deployed.
//   - DeploySourceRef:   apid source-ref path.
//   - DeployLocalTarball: apid tarball path.
//   - DeployTrafficChanged:  meterd orchestrator (canary step).
//   - DeployHealthProbeFailed: meterd orchestrator (first-N-5xx gate).
//   - DeployHealthRecovered:  meterd orchestrator (recovery).
//   - DeployRolledBack:  meterd orchestrator (auto-rollback).
//   - DeployRemoved:     meterd orchestrator (90-day GC).
type DeploymentAuditKind string

const (
	DeployCreated           DeploymentAuditKind = "deploy.created"
	DeploySourceRef         DeploymentAuditKind = "deploy.source_ref"
	DeployLocalTarball      DeploymentAuditKind = "deploy.local_tarball"
	DeployTrafficChanged    DeploymentAuditKind = "deploy.traffic_changed"
	DeployHealthProbeFailed DeploymentAuditKind = "deploy.health_probe_failed"
	DeployHealthRecovered   DeploymentAuditKind = "deploy.health_recovered"
	DeployRolledBack        DeploymentAuditKind = "deploy.rolled_back"
	DeployRemoved           DeploymentAuditKind = "deploy.removed"
	// SAFE-RELEASES-OBS PR-A (migrations/20260905000000000_deployment_audit_kinds_widen.sql):
	// orchestrator emit surface. Closed-set widening that
	// migration 00477 promised-but-never-shipped when Mega PR #2
	// added the orchestrator goroutine. Without this widening the
	// orchestrator's emitAudit calls hit SQLSTATE 23514 silently
	// (the state-machine write landed regardless) — exactly the
	// silent soak-bypass the audit trail exists to prevent.
	DeployRolloutStarted   DeploymentAuditKind = "deploy.rollout_started"
	DeployRolloutCompleted DeploymentAuditKind = "deploy.rollout_completed"
	DeployRolloutAborted   DeploymentAuditKind = "deploy.rollout_aborted"
	// SAFE-RELEASES-OBS PR-D: canary-step + alert-rule audit
	// kinds. Mirrors the orchestrator's per-tick emit surface
	// (deploy.canary_step_advanced will replace the existing
	// deploy.traffic_changed payload shape; deploy.alert_rule_fired
	// surfaces alert-driven rollbacks/demotes/promotes with a
	// rule-scoped stamp).
	DeployCanaryStepAdvanced DeploymentAuditKind = "deploy.canary_step_advanced"
	DeployAlertRuleFired     DeploymentAuditKind = "deploy.alert_rule_fired"
)

// DeploymentAudit is one row of the deployment_audit table
// (migrations/00332_deployment_audit.sql). Mirrors the AuditLog
// shape (issue #755 / PR-5) but is per-deployment instead of
// per-account: a deployment row outlives the deployment it
// relates to so a SOC 2 / GDPR auditor can re-derive the
// post-deploy state without joining back to a deleted deployment
// row.
//
// Stored shape matches the migration: BIGINT IDENTITY PK, NOT NULL
// UUID deployment_id (no FK — see migration commentary), nullable
// UUID account_id (no FK — same rationale), NOT NULL TEXT kind
// (closed-set CHECK), NOT NULL TEXT actor, NOT NULL TIMESTAMPTZ at
// with default now(), nullable JSONB data.
type DeploymentAudit struct {
	ID           int64               // assigned by Postgres IDENTITY on insert
	DeploymentID uuid.UUID           // NOT NULL; no FK to deployments(id)
	AccountID    *uuid.UUID          // nullable; no FK to accounts(id)
	Kind         DeploymentAuditKind // NOT NULL; closed-set CHECK
	Actor        string              // NOT NULL; resolved actor from EmitAs
	At           time.Time           // NOT NULL DEFAULT now()
	Data         json.RawMessage     // nullable; verbatim payload at emit time
	// AlertRuleID (SAFE-RELEASES-OBS PR-D, issue #976 / ADR-122) is
	// the alert_rule.id that triggered this audit row. Populated by
	// pkg/safedeploy.ActionDispatcher when a rollback/demote/promote
	// fires, AND by pkg/alerts/evaluator when the canary preset
	// advances. nil for non-rule-triggered rows (the orchestrator's
	// own deploy.rollout_* lifecycle emits). The DB column
	// (migrations/20260905000000002) is UUID NULL + partial index
	// (alert_rule_id, at DESC) WHERE alert_rule_id IS NOT NULL so
	// the /dashboard/alerts/{id} reverse-lookup query stays cheap.
	AlertRuleID *uuid.UUID
}

// AuditLogFilter is the read-side query shape for the audit_log table.
// Handlers build one from the inbound query string; the store method
// translates it into a single WHERE clause without string concatenation.
// All fields are optional — zero values mean "no constraint".
type AuditLogFilter struct {
	// AccountID, when set, restricts the result to rows whose
	// account_id matches. The customer-scoped handler pins this to
	// the calling account's ID; the operator endpoint leaves it
	// nil and exposes the optional ?account_id= query param.
	AccountID *uuid.UUID
	// KindPrefix, when non-empty, restricts to rows whose kind
	// starts with this string (LIKE 'prefix%'). Used for the
	// dashboard's kind-narrowing dropdown.
	KindPrefix string
	// Since is the inclusive lower bound on received_at. Zero
	// value means "no floor" — the full table is scanned.
	Since time.Time
	// IncludeAnonymous controls whether rows with account_id IS
	// NULL are returned. Customer endpoint always sets this false;
	// operator endpoint reads ?include_anonymous=.
	IncludeAnonymous bool
	// Limit is the maximum number of rows to return. Bounded by
	// the handler to the over-read constant; a zero value means
	// "store default" (the operator endpoint passes a sane cap).
	Limit int
	// ActorEmail, when set, restricts to rows whose
	// account_email column matches exactly (case-sensitive).
	// Operator-only filter — the customer endpoint does not
	// expose this. Added in P4 of the operator-side
	// observability mega-PR (Commit 6). Empty pointer = no
	// constraint.
	ActorEmail *string
	// OperatorOnly, when true, restricts to rows whose kind
	// starts with "operator.action." (the operator action
	// vocabulary adopted in Commit 3). Equivalent to setting
	// KindPrefix="operator.action." but with the dedicated
	// query-param `?operator_only=true` so the operator
	// dashboard's filter chip strip can render a single
	// boolean toggle. Mutually exclusive with a non-empty
	// KindPrefix at the handler layer.
	OperatorOnly bool
	// TargetAccountID, when set, restricts to rows whose
	// data->>'target_account_id' matches. The data column is
	// JSONB; the query uses the containment operator
	// (@> jsonb_build_object('target_account_id', $N)) so the
	// GIN index on data (verified at PR-open) is used.
	// Operator-only filter. Empty pointer = no constraint.
	TargetAccountID *string
}

// AuditLogKindAccountDeleted is the canonical kind value emitted into
// audit_log when an account is hard-deleted (issue #755 / PR-6, written
// from inside PgStore.DeleteAccount and MemStore.DeleteAccount). Kept
// as a package-level const so the SQL insert, the grace-side narration
// in pkg/grace, and the dashboard's kind-narrowing dropdown can all
// reference the same string without drift.
const AuditLogKindAccountDeleted = "account.deleted"

// Usage is one row of monthly usage (spec §10). meterd is the writer in
// production; for tests we seed rows directly.
type Usage struct {
	AccountID string
	AppID     string
	Month     time.Time // truncated to month
	MBSeconds int64
	// CPUUsec is the cumulative host cgroup CPU-µs consumed by
	// this app in this month (issue #279 / PR-B). Measurement
	// only — billing is on plan RAM. Populated by UsageByMonth
	// from the usage_monthly view; zero on the mb-only legacy
	// rows.
	CPUUsec  int64
	Requests int64
	// TXBytes is the cumulative HTTP response body bytes the
	// gateway forwarded for this app in this month. Source:
	// pkg/gateway/handler.go statusRecorder.Bytes → per-(instance,
	// minute) ring buffer → meterd Sampler.SampleAndRoll →
	// AppendUsage. ADR-046. Informational — not billed.
	TXBytes int64
	// NetTxBytes is the cumulative byte delta on root-side
	// vethHost.rx_bytes for this app in this month. Source:
	// vmmd pkg/fcvm/netstats.Cache → vmmd.Stats → schedd
	// instancestats.Poller → meterd Sampler.SampleAndRoll →
	// AppendUsage. ADR-046. Informational — not billed. Unit
	// = interface bytes (includes Ethernet/IP framing).
	NetTxBytes int64
	// NetRxBytes is the cumulative byte delta on root-side
	// vethHost.tx_bytes (root→guest = ingress) for this app
	// in this month. Source: vmmd pkg/fcvm/netstats.Cache TX
	// path → vmmd.Stats → schedd instancestats.Poller → meterd
	// Sampler.SampleAndRoll → AppendUsage. ADR-048.
	// Informational — not billed. Unit = interface bytes.
	NetRxBytes int64
	// ColdBootCount is the per-month sum of WAKE_RESTORE→
	// WAKE_COLD_BOOT transitions observed across this app's
	// instances. Source: scheddgrpc.InstanceStatsRow.
	// LastWakeMethod, sampled by meterd Sampler.
	// ADR-048. Informational — not billed.
	ColdBootCount int64
}

// UsageWindow is the account-level billable usage aggregate for one
// completed UTC hour. It is intentionally provider-neutral: every metered
// provider uses the same durable source rows and its own idempotency key.
type UsageWindow struct {
	AccountID string
	Hour      time.Time
	MBSeconds int64
}

// DailyUsage is the per-(account, app, day) row read by
// Store.UsageDaily (ADR-048 §5). Mirrors the columns declared
// in migrations/00067_extend_metering_telemetry.sql::usage_daily.
// Day is a UTC midnight date; PK is (account_id, app_id, day).
// Informational — not billed.
type DailyUsage struct {
	AccountID      string
	AppID          string
	Day            time.Time
	MBSeconds      int64
	Requests       int64
	CPUUsec        int64
	TXBytes        int64
	NetTxBytes     int64
	NetRxBytes     int64
	ColdBootCount  int64
	BuilderSeconds int64
	// TailSeconds (issue #667 / ADR-078) is the per-day wall-clock
	// seconds this instance spent draining waitUntil tasks.
	// INFORMATIONAL ONLY — does not enter billing. Pinned by
	// pkg/meter/pusher_shadow_test.go::TestPushHour_ExcludesTailSeconds.
	// Source: rollup.go::rollupSQL SUM(tail_seconds).
	TailSeconds int64
}

// StorageUsage is the per-(account, app, day) row read by
// Store.StorageUsage (ADR-049 §B.3). Mirrors
// migrations/00070_snapshot_storage_daily.sql::snapshot_storage_daily.
// Day is a UTC midnight date; PK is (account_id, app_id, day).
// Informational — not billed today; the future "Pro plan 1 GB
// included" PR consumes this surface.
type StorageUsage struct {
	AccountID     string
	AppID         string
	Day           time.Time
	SnapshotBytes int64
	LayerBytes    int64
}

// Invoice is one persisted invoice from a billing provider (issue #259,
// BILLING: plan comparison + invoice history). Rows arrive via the
// webhook ingestion path (PR B); the read API and dashboard read this
// table. Per-account filter is enforced by the Store method that
// returns this type — never expose the cross-account scan.
//
// Money is integer cents in the provider's currency; the financial
// model distills to EUR at the API edge. Currency is preserved per
// row so future multi-currency support can land without a backfill.
//
// HostedURL is intentionally NOT exposed on the read surface — the
// column lives in invoices.hosted_url for PR-B audit only. Provider
// invoice URLs and PDF URLs are session-scoped; we never hand them to
// the customer via this API.
type Invoice struct {
	ID                string
	AccountID         string
	Provider          string // "stripe" | "paddle" | "polar"
	ProviderInvoiceID string
	Number            string
	Status            string // "draft" | "open" | "paid" | "uncollectible" | "void"
	PeriodStart       time.Time
	PeriodEnd         time.Time
	SubtotalCents     int64
	TaxCents          int64
	TotalCents        int64
	AmountPaidCents   int64
	Currency          string
	PDFAvailable      bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// AccountCredit is one positive-cents balance issued by an operator
// via POST /v1/admin/accounts/{id}/credits (issue #279). cents_remaining
// is decremented at consumption time (the consumption reducer is the
// PR #323 invoice-finalization follow-up; this PR only lands the
// issuance surface). Cents is integer — never float on money (CLAUDE.md).
//
// @migration 00049 creates this table. expires_at is optional; a NULL
// expiry means the credit is valid until fully consumed. The active
// partial index (where cents_remaining > 0) speeds up the
// "consume next credit" query when the consumption reducer lands.
type AccountCredit struct {
	ID             string
	AccountID      string
	CentsRemaining int64
	Reason         string
	CreatedAt      time.Time
	ExpiresAt      *time.Time
}

// CreditLedgerEntry is one immutable row in the append-only audit log
// of credit deltas (issue #279). One row is inserted per issuance
// (delta positive) and per consumption (delta negative, when the
// consumption reducer lands). The handler always supplies a reason
// text and the operator account ID as actor; the row is never
// updated or deleted by code convention (no surface grants a write).
//
// @migration 00049 creates this table. ON DELETE CASCADE on account_id
// and credit_id so GDPR DeleteAccount scrubs both tables in the same
// transaction that scrubs the rest of the customer's data.
//
// ProviderInvoiceID is NULL on issuance rows (today's only writer);
// the consumption reducer (issue #279 PR-C, @migration 00058) sets it
// to the provider's invoice identifier and pairs it with CreditID in
// a unique partial index so a webhook re-fire or admin endpoint
// replay cannot double-decrement cents_remaining.
type CreditLedgerEntry struct {
	ID                string
	AccountID         string
	CreditID          string
	DeltaCents        int64
	Reason            string
	Actor             string
	CreatedAt         time.Time
	ProviderInvoiceID *string
}

// UpdateAppParams is the partial-update payload for PATCH /v1/apps/{slug}.
// Nil pointers mean "leave unchanged". Type and runtime remain immutable;
// lifecycle fields are stored as an app-owned manifest patch alongside the
// existing resource and scaling settings.
type UpdateAppParams struct {
	RAMMB          *int
	IdleTimeoutS   *int // explicit 0 clears to plan default
	SetIdleTimeout bool // distinguishes nil from zero
	MaxConcurrency *int
	// MinInstances is the per-app floor for idle reaping
	// (ux_spec §6.5). SetMinInstances distinguishes "unset" (don't
	// touch the column) from "explicit zero" (scale to zero, the
	// default for Free/Hobby).
	MinInstances    *int
	SetMinInstances bool
	// EgressAllowlist is the per-app outbound CIDR allowlist
	// (ADR-031). SetEgressAllowlist distinguishes "unset" from
	// "explicit empty" (= "no allowlist rule, current behaviour").
	// A nil pointer when SetEgressAllowlist is false leaves the
	// column unchanged; a non-nil empty slice with
	// SetEgressAllowlist true replaces the stored array with '{}'
	// (the default — see migration 00029).
	EgressAllowlist    *[]netip.Prefix
	SetEgressAllowlist bool
	// StaticEgressIP (ADR-119) is the customer-supplied IPv4.
	// SetStaticEgressIP distinguishes "don't touch the column"
	// (false) from "explicit IP" (true). SetStaticEgressIP=true
	// with StaticEgressIP=nil means "clear the pin" (DELETE wire
	// shape). SetStaticEgressIP=false leaves the column unchanged.
	// Apid gates this on Plan.StaticEgressIPAllowed + the family=4
	// CHECK + the partial unique index (defends against
	// alias-IP collision).
	StaticEgressIP    *netip.Addr
	SetStaticEgressIP bool

	// PublicAuthIPAllowlist (ADR-118) is the per-app ingress CIDR
	// allowlist. SetPublicAuthIPAllowlist distinguishes "unset"
	// from "explicit empty". Same nil-pointer semantics as
	// EgressAllowlist above. The column is
	// apps.public_auth_ip_allowlist (migration 00308); the DB
	// trigger rejects non-v4/v6 families and masklen /0 (defence
	// in depth on top of the apid parse step).
	PublicAuthIPAllowlist    *[]netip.Prefix
	SetPublicAuthIPAllowlist bool
	// AutoscaleTargetRPS is the per-instance RPS target for the
	// reactive scale-up trigger (issue #169 / #172). SetAutoscaleTargetRPS
	// distinguishes "unset" (don't touch the column) from "explicit
	// zero" (disable autoscale for RPS). Plan-gated upstream (apid
	// rejects Free PATCH with 403). Hobby/Pro/Scale only.
	AutoscaleTargetRPS    *int
	SetAutoscaleTargetRPS bool
	// AutoscaleTargetCPUPct is the per-instance CPU% target (1..100).
	// SetAutoscaleTargetCPUPct has the same "unset" vs "explicit zero"
	// semantics as AutoscaleTargetRPS. Pro/Scale only.
	AutoscaleTargetCPUPct    *int
	SetAutoscaleTargetCPUPct bool
	// StreamingEnabled (issue #471) toggles the per-app response
	// streaming path. SetStreamingEnabled distinguishes "unset"
	// (don't touch) from "explicit false" (opt out of streaming).
	// Plan-gated upstream (apid returns 403 plan_streaming_not_allowed
	// when the plan lacks the gate). Hobby/Pro/Scale customers may
	// PATCH true → false to disable streaming for a specific app
	// (e.g. a synchronous JSON API that wants Content-Length).
	StreamingEnabled    *bool
	SetStreamingEnabled bool
	// WebSocketEnabled (issue #676 / ADR-080) toggles the per-app
	// raw-bytes Upgrade bridge. SetWebSocketEnabled distinguishes
	// "unset" (don't touch) from "explicit false" (opt out of
	// Upgrade traffic). Plan-gated upstream: apid returns 403
	// plan_websocket_not_allowed when the plan lacks the gate
	// (Free). Hobby/Pro/Scale customers may PATCH true → false
	// to disable WS for a specific app (e.g. a JSON API that
	// should never accept an Upgrade).
	WebSocketEnabled    *bool
	SetWebSocketEnabled bool
	// RouteMetricsEnabled (ADR-093) toggles the per-app per-route
	// observability surface. SetRouteMetricsEnabled distinguishes
	// "unset" (don't touch) from "explicit false" (opt out of the
	// per-route breakdown). Plan-gated upstream: apid returns 403
	// plan_route_metrics_not_allowed when the plan lacks the gate
	// (Free). Hobby/Pro/Scale customers may PATCH true → false to
	// disable per-route metrics for a specific app (e.g. an app
	// that does not want the per-route cardinality on the box).
	RouteMetricsEnabled    *bool
	SetRouteMetricsEnabled bool
	// AppProtocol (ADR-124) is the per-app wire-protocol
	// selector stored on the apps row as text NOT NULL DEFAULT
	// 'http1'. Closed set {http1, http2, grpc} enforced by
	// apps_app_protocol_chk (migration 00382). Plan gate for
	// 'grpc' is at the apid boundary (Plan.AppProtocolAllowed),
	// not the SQL CHECK — every pre-existing app reads "http1"
	// without a migration. The per-app transport-selection seam
	// (x-faas-protocol header stamp at pkg/gateway/handler.go)
	// reads this field via pgRouter.toApp.
	//
	// Pointer semantics mirror WebSocketEnabled: nil means "don't
	// touch" (the SQL keeps the existing value via the
	// `app_protocol = case when $N then $M else app_protocol end`
	// pattern at pgstore.go::UpdateApp); non-nil writes the
	// value verbatim. SetAppProtocol is the boolean that toggles
	// the SET clause at UpdateApp time so a customer may PATCH
	// back to "http1" explicitly (the schema's NOT NULL DEFAULT
	// would otherwise mask the explicit-write intent).
	AppProtocol    *string
	SetAppProtocol bool
	// MaintenanceMode (ADR-091 amendment) opts the whole app
	// into 503 + Retry-After mode. SetMaintenanceMode
	// distinguishes "unset" (don't touch) from "explicit false"
	// (opt out of maintenance mode). Free-tier allowed (no plan
	// gate); the gatewayd applier short-circuits every request
	// before wake, so this primitive is the cheapest way to pin
	// an app during a deploy rollback or a billing investigation.
	MaintenanceMode    *bool
	SetMaintenanceMode bool
	// RequireSigned (issue #472 / ADR-054) gates OCI image deploys
	// on a valid cosign signature from a trusted publisher. SetRequireSigned
	// distinguishes "unset" (don't touch) from "explicit false"
	// (opt out of signature enforcement). Admin-only via PATCH
	// /v1/apps/{slug}; not plan-gated (any plan may opt in). Source-tarball
	// deploys are unaffected.
	RequireSigned    *bool
	SetRequireSigned bool
	// WarmSnapshotEnabled (issue #470 / ADR-055) toggles the
	// two-tier snapshot path. SetWarmSnapshotEnabled distinguishes
	// "unset" (don't touch) from "explicit false" (disable warm
	// capture). Plan-gated upstream: apid returns
	// 403 plan_warm_snapshot_not_allowed when the customer's plan
	// is Free or Hobby AND the value would be true. Customers may
	// PATCH true → false on any plan to opt out per-app.
	WarmSnapshotEnabled    *bool
	SetWarmSnapshotEnabled bool
	// WarmSnapshotMinRequests (issue #470 / ADR-055) is the
	// request-count threshold for warm-tier capture. Range [1, 100].
	// SetWarmSnapshotMinRequests distinguishes "unset" from
	// "explicit zero" (illegal — the SQL CHECK rejects 0).
	WarmSnapshotMinRequests    *int
	SetWarmSnapshotMinRequests bool
	// WarmSnapshotMinMs (issue #470 / ADR-055) is the
	// time-since-first-ready threshold for warm-tier capture.
	// Range [100, 60000]. SetWarmSnapshotMinMs distinguishes
	// "unset" from "explicit low value" (illegal — the SQL CHECK
	// rejects <100).
	WarmSnapshotMinMs    *int
	SetWarmSnapshotMinMs bool
	// EvictionPriority (issue #475) classifies the app under
	// cross-account RAM pressure. SetEvictionPriority distinguishes
	// "unset" (don't touch) from "explicit best_effort" (opt out of
	// the reserved tier). apid's updateApp handler validates the
	// value (must be 'best_effort' or 'reserved'), gates 'reserved'
	// behind the plan's EvictionPriorityReservedAllowed() flag, and
	// enforces the per-account cap (Plan.ReservedConcurrencyPerAccount())
	// under an apps-row FOR UPDATE lock — the same shape as
	// CreateCronIfUnderQuota. The audit kind
	// app.eviction_priority_changed is emitted from apid only on an
	// actual value change (no-op PATCHes are silent).
	EvictionPriority    *string
	SetEvictionPriority bool
	// RequireAuthn (issue #560) toggles per-deployment
	// authentication on an existing app. SetRequireAuthn
	// distinguishes "unset" (don't touch the column) from
	// "explicit false" (opt out — back to public-by-default).
	// Plan-gated upstream: apid returns 403
	// plan_require_authn_not_allowed when the customer's plan is
	// Free or Hobby AND the value would be true. Customers on any
	// plan may PATCH true → false to opt out per-app.
	RequireAuthn    *bool
	SetRequireAuthn bool
	// PublicAuth (issue #477 / ADR-079) is the per-app
	// public-URL auth block on the PATCH request. Three
	// shapes:
	//   - nil (the default): leave the apps row's
	//     public_auth_mode + public_auth_basic columns
	//     alone.
	//   - non-nil with Mode="open": explicit opt-out back
	//     to public-by-default; the sealed blob is cleared
	//     so a stale secretbox row never reaches a fresh
	//     request.
	//   - non-nil with Mode="bearer": bearer auth gate
	//     flips on; cleared blob.
	//   - non-nil with Mode="basic": basic auth gate
	//     flips on; sealed blob is populated from
	//     Username/Password (or, in v1, env-var reference
	//     names per the issue body).
	// SetPublicAuth distinguishes "unset" (don't touch the
	// columns) from "explicit {open,bearer,basic}" the same
	// way SetRequireAuthn does. The apid PATCH validator
	// enforces the plan gate (open=all, bearer=Hobby+,
	// basic=Pro+) and the canonical mode enum; the secretbox
	// seal happens at PATCH time so the gatewayd-internal hot path
	// only reads ciphertext.
	PublicAuth    *AppPublicAuthUpdate
	SetPublicAuth bool
	// ClearAuthDefaultFlippedAt (issue #695 / ADR-080) is set
	// by the apid PATCH handler when the customer makes any
	// explicit choice on a grandfathered app (require_authn or
	// public_auth PATCH). The signal means "the customer has
	// seen the dashboard banner and made a deliberate
	// choice" — the stamp is cleared so the per-account
	// CountAuthDefaultFlippedApps drops, the yellow banner
	// stops re-rendering, and the CLI AUTH column drops the
	// `since YYYY-MM-DD` suffix. New post-flip apps are
	// created with the column NULL so this flag is a no-op
	// for them. Default false (a no-touch PATCH that doesn't
	// touch auth columns must NOT silently clear the stamp).
	ClearAuthDefaultFlippedAt bool
	Status                    *AppStatus
	Manifest                  *AppManifest
	// RootDir is the workload's repo-relative build context (Phase 5
	// repo decomposition, ADR-050 §3). Populated by pkg/reconcile on
	// update; the apid handler leaves it nil on customer-initiated
	// PATCH. Empty string = default ('' in DB). No Set* flag: the
	// "nil vs explicit empty" distinction is irrelevant for a column
	// whose canonical "unset" is the empty string.
	RootDir *string
	// WorkloadName is the per-app workload identity (e.g. the
	// compose service name). Mirrors apps.workload_name. Same
	// semantics as RootDir: nil = leave alone, empty string = reset
	// to default. Reconcile writes this on every update.
	WorkloadName *string
	// StartCommand is the customer-supplied override for the image's
	// entrypoint (e.g. compose `command:`). apps.start_command is
	// NULL-able; the nullString helper treats the empty string as
	// NULL on the wire. Reconcile writes this on every update; the
	// apid handler leaves it nil.
	StartCommand *string
	// ScalingPolicy is the per-app autoscaling configuration
	// (issue #462 / ADR-058). SetScalingPolicy distinguishes "unset"
	// (don't touch) from "explicit zero" (scale to zero, the
	// default behaviour). The on-disk shape is jsonb; the in-memory
	// field is the canonical form for the (de)serialiser. When
	// SetScalingPolicy is true the handler writes the jsonb column
	// AND keeps the legacy `min_instances` column in sync (so
	// legacy readers don't see a stale floor).
	ScalingPolicy    *ScalingPolicy
	SetScalingPolicy bool
	// OverflowNode (issue Tier A10 / ADR-088) is the customer's
	// per-app preferred spill target (compute_node UUID). Apid
	// has already resolved the wire name → UUID server-side
	// (cmd/apid/handlers_ext.go::validateUpdateApp). Set bit
	// distinguishes "unset" (don't touch the column) from
	// "explicit NULL" (clear — back to A9 default fallback). The
	// store is a plain column write; the empty-uuid CHECK +
	// FK with ON DELETE SET NULL (migration 00167) cover the
	// identity + ON-cascade contract.
	OverflowNode    *string
	SetOverflowNode bool
	// CORSDefaultEnabled is the per-app default CORS opt-in
	// (ADR-091 CORS improvements D1). SetCORSDefaultEnabled
	// distinguishes "unset" (don't touch) from "explicit
	// false" (turn the default off). PATCHing from true →
	// false is non-destructive: the column is metadata only,
	// no row is touched on the gateway hot path until the
	// next request.
	CORSDefaultEnabled    *bool
	SetCORSDefaultEnabled bool
	// CORSDefaultOrigins is the per-app default CORS
	// allowlist. SetCORSDefaultOrigins distinguishes "unset"
	// (don't touch) from "explicit empty slice" (clear the
	// allowlist — back to deny all). The validator
	// (apid's updateApp handler) rejects nil when
	// SetCORSDefaultOrigins is true and CORSDefaultEnabled is
	// nil pointer (we need a value to know whether the
	// explicit-empty case is intentional or a wire-shape
	// bug). The column is text[]; the gateway reuses the
	// matchOrigin matcher verbatim against this list.
	CORSDefaultOrigins    *[]string
	SetCORSDefaultOrigins bool
}

// AppPublicAuthUpdate (issue #477 / ADR-079) is the
// per-app public-URL auth block carried on UpdateAppParams
// + the apid PATCH /v1/apps/{slug} handler. The store
// layer turns Mode + Sealed into the column pair
// (public_auth_mode, public_auth_basic); the seal happens
// at PATCH time (in the apid handler) so the on-row bytes
// are always ciphertext and the store layer never sees
// plaintext.
//
// Mode is the canonical 'open'|'bearer'|'basic' string
// (must match apps_public_auth_mode_chk). Username +
// Password are only meaningful when Mode='basic'; they
// carry the plaintext the apid seal step encodes under
// the APP_BASIC_AUTH secretbox namespace. For Mode='open'
// or 'bearer', the apid handler ignores them (and clears
// any existing sealed blob — Sealed is left nil).
// Sealed carries the ciphertext the apid wrote; nil for
// mode='open'|'bearer' (the store will NULL the column)
// and non-nil for mode='basic' (the store will write it).
type AppPublicAuthUpdate struct {
	Mode     string
	Username string
	Password string
	Sealed   []byte
}

// Canonical public-auth mode strings for the state
// layer (issue #477 / ADR-079). Values must stay in sync
// with the apps_public_auth_mode_chk CHECK constraint in
// migrations/00153_apps_public_auth.sql AND with the
// pkg/gateway's package-local copies. The three layers
// (sqlc / state / gateway) all share the same vocabulary;
// if a fifth is ever added, mirror the constant here.
// ADR-119 added 'internal_only' — see also the drift-guard
// test pkg/api/public_auth_constants_test.go.
const (
	AppPublicAuthModeOpen         = "open"
	AppPublicAuthModeBearer       = "bearer"
	AppPublicAuthModeBasic        = "basic"
	AppPublicAuthModeIPAllowlist  = "ip_allowlist"
	AppPublicAuthModeInternalOnly = "internal_only"
)

// Snapshot is one restoreable microVM state (spec §4.6, ADR-005).
//
// imaged is the only writer; schedd reads the latest non-stale row per
// deployment to decide whether to wake-from-snapshot or cold-boot. The
// `Stale` flag is flipped on Firecracker upgrades (snapshots are pinned to
// the FC version that made them — see ADR-005).
type Snapshot struct {
	ID           string
	DeploymentID string
	FCVersion    string
	MemBytes     int64
	DiskBytes    int64
	// Tier (issue #470 / ADR-055) is which snapshot tier this row
	// belongs to: "init" (taken right after guest-init signals
	// :8080 bound; restore pays framework warmup) or "warm"
	// (taken after N successful requests ≥ warm_snapshot_min_ms,
	// when the framework is hot — restore skips the warmup cost).
	// The DEFAULT 'init' on the column covers every pre-PR row.
	// Empty string in Go is treated as "init" by LatestSnapshotForTier
	// so legacy callers stay valid.
	Tier string
	// StorageKey is the canonical StorageBackend key for the mem
	// blob (issue #96, ADR-025 axis 2). Local backends resolve it
	// to a file under /srv/fc; remote backends (OCI registry)
	// resolve it to a manifest tag. Always populated by the
	// production write path (imaged copies it from the
	// snapshot_written payload); empty only on rows written by
	// test fixtures that bypass the storage contract. Wake sends
	// StorageKey on the wire; vmmd resolves it through the
	// configured StorageBackend.
	StorageKey string
	Stale      bool
	CreatedAt  time.Time
}

// Snapshot tier constants (issue #470 / ADR-055). Use these rather
// than bare string literals so the snapshot_written payload wire
// shape and the LatestSnapshotForTier callers cannot drift. The
// CHECK constraint on snapshots.tier enforces the same vocabulary at
// the DB layer (migrations/00102_snapshots_tier.sql).
const (
	SnapshotTierInit string = "init"
	SnapshotTierWarm string = "warm"
)

// SnapshotForGC is the join-projection used by the imaged nightly GC
// (spec §4.6: keep current + previous deployment's snapshots per app;
// fleet budget pressure evicts from biggest-over-quota accounts first).
// It denormalises snapshot → deployment → app → account into one row so
// the GC algorithm doesn't have to round-trip per row.
//
// Snapshots for soft-deleted apps (apps.status = 'deleted') are filtered
// at the SQL layer; they have no in-flight wake target and keeping them
// would leak the 452 GB budget indefinitely.
type SnapshotForGC struct {
	ID           string
	DeploymentID string
	AppID        string
	AccountID    string
	// AppSlug is the apps.slug of the parent app. Populated from the
	// snapshot → deployments → apps JOIN so the GC algorithm doesn't
	// have to issue per-eviction DeploymentByID + AppByID lookups to
	// build the apps/<slug>/<dep>.ext4 storage key (issue #195 B1.1).
	// An empty AppSlug after the projection runs is an invariant
	// violation — call sites should log + skip, never silently fall
	// back to a slow path.
	AppSlug   string
	FCVersion string
	MemBytes  int64
	DiskBytes int64
	// Tier (issue #470 / ADR-055) is the snapshot tier — see
	// Snapshot.Tier for the semantics. The GC projection carries
	// it so the perAppKeepCurrentPrevious policy can keep
	// (current warm + previous init) per app instead of the
	// legacy (current + previous) regardless-of-tier rule.
	Tier       string
	StorageKey string
	Stale      bool
	CreatedAt  time.Time
	// AppWarmSnapshotEnabled (issue #470 / PR C / ADR-072) projects
	// apps.warm_snapshot_enabled from the JOIN so the GC policy can
	// apply the 2+2 floor only on apps that opted in to the warm
	// tier. Apps with warm_snapshot_enabled=false keep only the
	// 2-init floor. Denormalised to avoid an AppByID round-trip per
	// eviction row.
	AppWarmSnapshotEnabled bool
}

// LoginToken is one row in login_tokens (M7.5 magic-link). The token
// itself never appears in storage — only its SHA-256 hash does. The
// raw token is emailed to the user once and is consumed by
// /auth/verify?token=… (one-shot).
type LoginToken struct {
	TokenHash  []byte
	AccountID  string
	ExpiresAt  time.Time
	ConsumedAt *time.Time
}

// CliAuthCode is one row of the cli_auth_codes table (spec §2.2
// device-code flow). AccountID is empty between mint and claim; the
// claim statement fills it in atomically. The 4-byte entropy + 5-min
// TTL + per-IP rate limit means brute-force on the code space is not
// realistic, so we don't bump the byte length here.
type CliAuthCode struct {
	TokenHash  []byte
	AccountID  string // empty until ClaimCliAuthCode
	ExpiresAt  time.Time
	ConsumedAt *time.Time
}

// AccountPassword is one row of the account_passwords table
// (issue #165 / ADR-032 PR #2). It carries the Argon2id PHC string
// for an account that has set a password. OAuth-only accounts have
// no row — the absence of a row is the signal that an OAuth-only
// flow is required to mint a session on that account.
//
// Hash is the PHC wire format ($argon2id$v=19$m=...,t=...,p=...$<salt>$<hash>),
// produced by pkg/auth.Encode. The Argon2id parameters (memory,
// time, threads) are EMBEDDED in the stored string so a future
// parameter bump is a no-op migration.
//
// UpdatedAt is stamped at every SetAccountPassword call. A future
// "rotate hash on login" hardening (PR #2.5 follow-up) reads this
// to decide whether to re-hash.
type AccountPassword struct {
	AccountID string
	Hash      string
	UpdatedAt time.Time
}

// OAuthLink is one row of the oauth_links table (issue #165 /
// ADR-032 PR #2). It binds an OAuth (provider, subject) pair to
// exactly one account_id. The composite primary key on the table
// enforces the §11 anti-takeover invariant: one OAuth subject maps
// to one account, period.
//
// Email is captured at link time so the dashboard can render "this
// Google account is bound" without a re-fetch. EmailVerified is a
// snapshot of the provider's email_verified value at link time.
// Once true at link, the row stays; a future "re-verify" flow can
// refresh EmailVerified (ADR-032 "Open follow-ups"). Per spec §11,
// no session is ever minted with EmailVerified=false at link time.
type OAuthLink struct {
	Provider        string // "google" | "github" | (future providers)
	ProviderSubject string // Google's `sub`, GitHub's numeric `id`
	AccountID       string
	Email           string
	EmailVerified   bool
	CreatedAt       time.Time
}

// LogEntry is one line of build output for a deployment (slice 5).
// The dashboard's SSE stream tails this row at seq > cursor; clients
// use the combination (DeploymentID, Seq) to dedupe across reconnects
// (an id-replay after a network blip will see the same seqs).
type LogEntry struct {
	DeploymentID string
	Seq          int64
	Stream       string // "stdout" | "stderr" | "system"
	Line         string
	WrittenAt    time.Time
}

// Session is one server-side record of a dashboard login (IAM-3,
// issue #187 + #244 merged). The cookie envelope carries the row's
// ID as `sid`; every authenticated dashboard request re-validates
// the row before the handler runs.
//
// Revocation is `RevokedAt != nil`. The store methods that mutate
// RevokedAt are account-scoped at the SQL level so IDOR is a
// persistence invariant — a cross-account DELETE returns false
// (mapped to 404 in the handler), not 403.
//
// LastSeenAt continues to update post-revoke (TouchSessionLastSeen
// is a no-op gate-wise but updates the column for ops triage). This
// is observability only; authorization uses RevokedAt exclusively.
//
// IssuedIP / IssuedUA are recorded at login and surface on
// GET /v1/auth/sessions so the customer can recognize "this is my
// phone" / "this is the laptop I logged in from last Tuesday".
// IssuedIP is empty when RemoteAddr is unparseable (rare; never
// log a parse-failure string verbatim). Dashboard-only — bearer API
// keys never create or query rows on this table.
type Session struct {
	ID         string // uuid, primary key, also the cookie `sid`
	AccountID  string // uuid, FK to accounts.id
	IssuedIP   string // empty when RemoteAddr unparseable
	IssuedUA   string // user-agent at login, may be empty
	IssuedAt   time.Time
	LastSeenAt *time.Time // nil until first authenticated request post-mint
	RevokedAt  *time.Time // nil == active; non-nil == revoked
	// BindingHash is the IAM-3-evolved ADR-076 fingerprint
	// (HMAC-SHA256 of `ip || "\x00" || ua_family`, keyed by the
	// host's session-key secret). Empty for pre-PR-076 rows and
	// for the unix-socket / CLI-auth code paths that don't have
	// a meaningful fingerprint. The RequireSession cookie branch
	// compares the live request's binding hash against this
	// value; mismatch ⇒ auto-revoke + audit + 401 (the
	// stolen-cookie defence).
	BindingHash string
}

// AppSecret is one row of customer secrets (spec §11/G2). apid is the only
// writer. Ciphertext is the age-sealed Envelope produced by pkg/secretbox;
// the plaintext VALUE is never stored, never logged, and only exists
// transiently in apid's PUT handler and vmmd's per-wake staging path.
//
// AccountID is the row's owning account. Both PgStore and MemStore filter
// on (AccountID, AppID, Scope, Key) so cross-account access returns
// ErrNotFound (handlers render 400 CodeSecretNotFound by design — the URL
// resource IS the secret name).
//
// ADR-092 PR-A: the PRIMARY KEY widened from (app_id, key) to
// (app_id, scope, key) by migration 00214_app_secrets_scope.sql — mirrors
// the same widening that 00203_app_envs_scope.sql did for app_envs. Pre-PR
// rows backfill on first read with scope='default' via the PG11+
// fast-default, so legacy callers (ListAppSecrets, UpsertAppSecret,
// etc.) continue to work unchanged; the scope-aware variants
// (ListAppSecretsInScope, UpsertAppSecretWithKidInScope, …) take an
// explicit scope parameter and are the canonical path. The flat
// methods hardcode scope='default' as a thin delegation.
type AppSecret struct {
	AccountID string
	AppID     string
	// Scope is the env-scope identifier attached at write time.
	// Always 'default' for legacy rows backfilled via the
	// column DEFAULT. Validated by `pkg/api.ValidateScope`
	// (regex ^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$) on every PUT /
	// POST / DELETE that flows through apid's `?scope=` parse
	// helper — the same shape as `app_envs.scope` (00203).
	// Sealing (the secretbox step) is scope-agnostic; scope is
	// purely a per-row address, not a seal-time identity.
	Scope      string
	Key        string
	Ciphertext []byte
	// Kid is the age-1... recipient string of the host identity
	// that sealed this row's ciphertext. Set by the apid PUT
	// handler (cmd/apid/handlers_secrets.go::setSecret) and by
	// pkg/rekey.Replayer at every re-seal. Rows sealed before
	// migration 00166 (ADR-089 PR-A) have Kid = "" until the
	// rekey pass or a subsequent PUT stamps it.
	//
	// Operators reading the kid column answer "what key sealed
	// this row?" without parsing the ciphertext blob.
	Kid string
	// ValueHash is the trustworthy value-equality discriminator
	// (ADR-117 env-diff matrix, PR-C). 16 hex chars (HMAC-SHA256
	// truncated to 64 bits, keyed by the per-host
	// /etc/faas/secrets/host.hmac.key). Empty for rows sealed
	// before migration 00296 — those rows have value_hash = NULL
	// in PG and the COALESCE on read surfaces "" so the empty
	// string matches the omitempty wire shape. Two rows with
	// the same ValueHash therefore share byte-identical
	// plaintext (collision probability 2^-64 — negligible at
	// SecretCountMax <= 100). The handler computes ValueHash
	// BEFORE SealOne in handlers_secrets.go::sealAndPersist so
	// the same plaintext byte string feeds both the HMAC and
	// the seal (Issue 1 fix — age X25519 + ChaCha20-Poly1305 is
	// probabilistically non-deterministic, so a
	// ciphertext-derived hash would diverge for every row).
	ValueHash string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AccountAppSecret is the per-row shape returned by
// ListAppSecretsForAccount (issue #393). Distinct from AppSecret
// because the account-scoped variant needs the app_slug (the per-app
// variant doesn't expose it — the URL slug is the path parameter, so
// the row doesn't need to carry it).
//
// Ciphertext is the same age-sealed Envelope that AppSecret carries;
// the handler emits it base64-encoded on the wire (paginated walk
// orders by (app_slug ASC, key ASC), so the cursor is the pair).
//
// Scope is the env-scope identifier attached at write time
// (ADR-092 PR-B). Always 'default' for legacy rows backfilled via
// the column DEFAULT (migration 00217, PR-A). The account-wide
// list crosses scopes — a customer with prod + staging rows
// needs the scope echoed alongside (app_slug, key) so the
// dashboard can group by scope without a second GET.
//
// ValueHash mirrors AppSecret.ValueHash (ADR-117 PR-C). Same
// semantic + same empty-string-for-NULL posture.
type AccountAppSecret struct {
	AccountID  string
	AppID      string
	AppSlug    string
	Key        string
	Scope      string
	Ciphertext []byte
	ValueHash  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// AppEnv is one row of customer runtime env vars (issue #395 / ADR-045).
// apid is the only writer. Value is plaintext TEXT (NOT sealed): env vars
// are explicitly non-credential config, and putting non-sensitive config
// behind the age seal would (a) double-count against SecretCountMax for no
// reason and (b) blur the secret.set audit trail. Anything sensitive
// belongs in app_secrets, not here — endpoints cross-reference the
// distinct audit kinds env.set vs secret.set.
//
// AccountID is the row's owning account. Both PgStore and MemStore filter
// on (AccountID, AppID, Scope, Key) so cross-account access returns
// ErrNotFound.
//
// Scope is the multi-scope identifier from ADR-090 (Phase 2 of the
// 2026-08-10 secrets+envs roadmap). Mirrors the schema's
// `app_envs.scope` column added by migration 00203; the default is
// the literal string "default" for pre-00203 rows and for the
// flat-shape writers (UpsertAppEnv / DeleteAppEnv / ListAppEnv /
// CountAppEnv) which hardcode scope='default' at the SQL boundary.
// Scope-aware writers (UpsertAppEnvInScope and its siblings) set
// this field from the caller-supplied scope. The shape must match
// the validSlug regex from cmd/apid/handlers.go:600 — lowercase
// alnum + dash, 3..40 chars — and the app_envs_scope_shape CHECK
// enforces this server-side.
type AppEnv struct {
	AccountID string
	AppID     string
	Scope     string
	Key       string
	Value     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AppTrustedSigner is one row of the per-app cosign trusted-publisher
// list (issue #472 / ADR-054). apid is the only writer; imaged reads
// the matching set at deploy-time verify and caches the parsed keys in
// memory (refreshed on pg_notify('trusted_signer_changed')).
//
// CosignPublicKey is the raw DER SubjectPublicKeyInfo blob (mirrors the
// bytea shape pkg/cosign.LoadPublicKeyFile returns for ECDSA P-256 per
// ADR-038). SignerName matches the DNS-1123-label CHECK on the table —
// the handler MUST pre-validate before INSERT.
//
// AccountID is the row's owning account. PgStore filters on
// (AccountID, AppID) so cross-account access returns ErrNotFound.
type AppTrustedSigner struct {
	AccountID        string
	AppID            string
	SignerName       string
	CosignPublicKey  []byte
	AddedAt          time.Time
	AddedByAccountID string
}

// AppRegistryCredential is one row of per-app private-registry Basic
// Auth (issue #461 / ADR-062). apid is the only writer (the PUT
// handler in cmd/apid/handlers_registry_auth.go). imaged is the only
// reader (pkg/imaged/handler.go::buildImageLayer looks up by
// (accountID, appID, registryHost) and unseals transiently in the
// pull call frame — plaintext lives only in that frame and is GC'd
// on return).
//
// PasswordEncrypted is the age-sealed bytea produced by
// pkg/secretbox.SealBytes with namespace="registry_creds". Username
// is metadata (not a secret) and stays in plaintext, mirroring the
// AppSecret precedent where each value is sealed independently and
// metadata is not. The plaintext password NEVER appears in any slog
// field, audit payload, error string, or HTTP response.
//
// Registry is the normalized host the customer supplied (lowercase,
// no scheme, no path, no trailing slash, port preserved — see
// apid's normalizeRegistryHost helper). Storage stores the
// normalized form; the imaged lookup normalizes again before the
// SQL query.
//
// LastUsedAt is updated by imaged after a successful authenticated
// pull. It's telemetry, not gating; failure to update it is warned
// but not fatal to the deployment.
//
// AccountID is the row's owning account. Both PgStore and MemStore
// filter on (AccountID, AppID, Registry) so cross-account access
// returns ErrNotFound.
type AppRegistryCredential struct {
	ID                string
	AccountID         string
	AppID             string
	Registry          string
	Username          string
	PasswordEncrypted []byte
	CreatedAt         time.Time
	UpdatedAt         time.Time
	LastUsedAt        *time.Time
}

// ProjectScanSource is the discoverer that produced the project's
// current scan (ADR-050 §3, docs/repo_decomposition_implementation.md
// §2). The stable set is closed by projects_scan_source_chk on the
// projects table. 'single' is the backfill label for projects that
// existed before repo decomposition; every other value comes from
// pkg/reposcan in Phase 2+. Phase 5's reconciler enforces monotonic
// upgrade ('single' → 'convention' allowed; 'compose' never reverts).
//
// The Go zero-value (ScanSource("")) is NOT in the schema CHECK set;
// code that persists a Project must set a canonical value (the
// backfill uses 'single'; the apid createProject transaction sets
// the scanner tier from the reposcan result; the rebuild path
// stamps 'unknown' when nothing has scanned yet).
type ProjectScanSource string

const (
	ProjectScanSourceCompose    ProjectScanSource = "compose"
	ProjectScanSourceProcfile   ProjectScanSource = "procfile"
	ProjectScanSourceK8s        ProjectScanSource = "k8s"
	ProjectScanSourceRender     ProjectScanSource = "render"
	ProjectScanSourceFly        ProjectScanSource = "fly"
	ProjectScanSourceServerless ProjectScanSource = "serverless"
	ProjectScanSourceWorkspace  ProjectScanSource = "workspace"
	ProjectScanSourceConvention ProjectScanSource = "convention"
	ProjectScanSourceSingle     ProjectScanSource = "single"
	ProjectScanSourceUnknown    ProjectScanSource = "unknown"
	// ProjectScanSourceWorkspaces is the plural form the
	// reconcile.Service.DeriveScanSource helper returns for
	// projects whose root tree enumerates a workspaces section
	// (e.g. yarn/npm/pnpm workspaces). It is distinct from
	// ProjectScanSourceWorkspace (singular) because the two
	// occupy different scan-source tiers today: the singular
	// form is the typed-const slot committed in the apps.scan_source
	// CHECK constraint, while the plural form is the working
	// pre-typed value reconciliation stores while it
	// converges the apps rows. Callers that want to seed or
	// compare against the plural form should reference this
	// constant instead of the raw string literal — the L1
	// review flagged the raw "workspaces" string in a service
	// test as a tierRank-asymmetry tripwire.
	ProjectScanSourceWorkspaces ProjectScanSource = "workspaces"
)

// scanSourceRank is the monotonic-upgrade total ordering. Higher rank
// wins; the Store.SetProjectScanSource path rejects a downgrade from
// a higher to a lower rank. The ordering closely tracks the tier
// table in docs/repo_decomposition_implementation.md §3:
//
//   - compose / k8s / render / fly / serverless are the "strong"
//     declarative sources (Tier 1); they cross-rank (a customer who
//     started on render and moved to compose doesn't "lose" data).
//   - procfile / workspace sit one tier below (they enumerate but
//     do not classify).
//   - convention (Tier 3) is the directory-shape guess.
//   - single (backfill label) is below convention.
//   - unknown is the floor.
//
// tierRank collapses onto this table once at init time.
type scanSourceRank int

const (
	scanSourceRankUnknown    scanSourceRank = 0
	scanSourceRankSingle     scanSourceRank = 1
	scanSourceRankConvention scanSourceRank = 2
	scanSourceRankWorkspace  scanSourceRank = 4
	scanSourceRankProcfile   scanSourceRank = 6
	scanSourceRankServerless scanSourceRank = 8
	scanSourceRankFly        scanSourceRank = 8
	scanSourceRankRender     scanSourceRank = 8
	scanSourceRankK8s        scanSourceRank = 8
	scanSourceRankCompose    scanSourceRank = 8
)

func tierRank(s ProjectScanSource) scanSourceRank {
	switch s {
	case ProjectScanSourceCompose:
		return scanSourceRankCompose
	case ProjectScanSourceK8s:
		return scanSourceRankK8s
	case ProjectScanSourceRender:
		return scanSourceRankRender
	case ProjectScanSourceFly:
		return scanSourceRankFly
	case ProjectScanSourceServerless:
		return scanSourceRankServerless
	case ProjectScanSourceProcfile:
		return scanSourceRankProcfile
	case ProjectScanSourceWorkspace:
		return scanSourceRankWorkspace
	case ProjectScanSourceConvention:
		return scanSourceRankConvention
	case ProjectScanSourceSingle:
		return scanSourceRankSingle
	default:
		return scanSourceRankUnknown
	}
}

// WorkloadClass is the per-app shape classification (ADR-050 §3).
// Phase 1 stamps the value as a scan hint from the repo; Phase 4
// (ADR-051) re-derives the authoritative class via the probe boot.
// Every state column carries a CHECK (CLAUDE.md), so the
// apps_workload_class_chk constraint mirrors this set.
//
// The Go zero-value WorkloadClass("") is NOT a valid DB CHECK value;
// code that persists an App must set WorkloadClassHTTP (or another
// canonical value) explicitly before CreateApp. Test fixtures that
// rely on zero values pass through MemStore but would trip the
// PgStore CHECK — see the comment on WorkloadClassHTTP.
type WorkloadClass string

const (
	WorkloadClassHTTP    WorkloadClass = "http"
	WorkloadClassGraphQL WorkloadClass = "graphql"
	WorkloadClassGRPC    WorkloadClass = "grpc"
	WorkloadClassJob     WorkloadClass = "job"
	WorkloadClassWorker  WorkloadClass = "worker"
)

// Project groups apps that share one (account, install_id, repo)
// binding (ADR-050 / impl plan §2). Phase 1 lands the read +
// monotonic-upgrade seams; the apid createProject transactional
// endpoint and the push-dispatch path are Phase 3 / Phase 5.
//
// Members: apps.project_id references this row. Standalone apps
// keep project_id NULL.
type Project struct {
	ID               string
	AccountID        string
	Slug             string
	RepoFullName     string // empty for standalone (non-bound) projects
	ProductionBranch string // empty until BindAppRepo runs
	InstallID        int64  // 0 until BindAppRepo runs
	ScanSource       ProjectScanSource
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// IsZero reports whether this is an unset Project (Go zero value).
// store-layer scans can return such a value via the concrete-type
// `Project{}` initializer that the pgx `Scan` into a value receiver
// produces on no-rows errors; callers should prefer ErrNotFound over
// IsZero, but IsZero is useful as a defensive guard on pagination
// cursors and test fixtures.
func (p Project) IsZero() bool {
	return p.ID == "" && p.AccountID == "" && p.Slug == ""
}

// ErrScanSourceDowngrade is returned by SetProjectScanSource when
// the caller asks to move from a stronger scan tier to a weaker
// one. The corresponding sentinel in MemStore's state package.
//
// AllowUpward re-classifies an error via errors.Is like the other
// shared sentinels (ErrNotFound, ErrConflict, ErrQuotaExceeded).
var ErrScanSourceDowngrade = errors.New("state: scan_source downgrade rejected")

// --- Organizations (ADR-061, IAM-6) -----------------------------------------
//
// PR 2 introduces `orgs`, `org_memberships`, and `org_invitations` tables
// plus nullable `org_id` columns on the 19 tenant-root tables
// (see docs/iam-6-ownership-inventory.md §B). The Go types here mirror
// those tables 1:1 and are the surface PR 5's handlers consume.
//
// The OrgRole / OrgStatus / OrgInvitationStatus enums match the SQL
// CHECK shapes — the pgstore maps a CHECK violation (SQLSTATE 23514)
// to ErrOrgRoleForbidden / ErrOrgInvitationInvalid / etc. via the
// existing mapErr helper. MemStore enforces the same enum in code.

// OrgRole is one of the five RBAC roles per ADR-061 §Role vocabulary.
// "owner" is reachable only via TransferOwnership (PR 5); the handler
// PATCH /members/{id} never sets it (the SQL CHECK on
// org_memberships.role ALSO enforces this — see migration 00099).
type OrgRole string

const (
	OrgRoleOwner     OrgRole = "owner"
	OrgRoleAdmin     OrgRole = "admin"
	OrgRoleDeveloper OrgRole = "developer"
	OrgRoleViewer    OrgRole = "viewer"
	OrgRoleBilling   OrgRole = "billing"
)

// OrgStatus mirrors AccountStatus so meterd / dunning can pivot onto
// orgs with no enum fork (PR 7).
type OrgStatus string

const (
	OrgStatusActive         OrgStatus = "active"
	OrgStatusPastDue        OrgStatus = "past_due"
	OrgStatusSuspended      OrgStatus = "suspended"
	OrgStatusDeletedPending OrgStatus = "deleted_pending"
)

// OrgInvitationStatus is the runtime materialisation of the
// (consumed_at, revoked_at) state machine; the SQL CHECK on
// org_invitations pins the three valid combinations and the store
// translates one to this enum for handler consumption.
type OrgInvitationStatus string

const (
	OrgInvitationPending  OrgInvitationStatus = "pending"
	OrgInvitationConsumed OrgInvitationStatus = "consumed"
	OrgInvitationRevoked  OrgInvitationStatus = "revoked"
	OrgInvitationExpired  OrgInvitationStatus = "expired"
)

// Org is the tenant root. Owns apps, projects, domains, builds,
// deployments, secrets, env, API keys, billing customer identifiers,
// dunning state, plan, and quotas (ADR-061 §Definitions).
//
// One personal org per account is immutable: it cannot accept
// additional members, transfer ownership, or be deleted independently
// of the account (Personal = true). PersonalOwnerAccountID is set on
// personal orgs only (the partial unique on
// (personal_owner_account_id) WHERE personal_org = true enforces
// exactly-one-personal-per-account at the SQL layer).
type Org struct {
	ID                     string
	Slug                   string
	Name                   string
	Personal               bool
	PersonalOwnerAccountID *string
	Plan                   api.Plan
	Status                 OrgStatus
	ProviderCustomerID     string
	StripeSubscriptionItem string
	DeletedPending         bool
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// OrgMembership is the (org, account) join row. The pair is the PK;
// one account has at most one membership per org.
//
// Role + RemovedAt semantics: an "active" member has RemovedAt == nil
// and contributes to the per-org member cap (Plan.OrgMembersMax()).
// A "removed" member has RemovedAt != nil; the row stays for audit
// purposes (who used to be in the org) but does not count.
//
// InvitedByAccountID is ON DELETE SET NULL per ADR-061 §D — the
// inviter attribution survives account deletion.
type OrgMembership struct {
	OrgID              string
	AccountID          string
	Role               OrgRole
	InvitedByAccountID *string
	JoinedAt           time.Time
	RemovedAt          *time.Time
}

// OrgInvitation is a one-shot token-bearing invite. The token is
// 32 random bytes generated in the PR 5 handler; only the SHA-256
// hash is stored (matching the login_tokens / cli_auth_codes shape).
//
// Email + Role are immutable once written; ExpiresAt is the only
// timestamp the cleanup loop uses to retire pending invitations.
// InvitedByAccountID and AcceptingAccountID both use ON DELETE SET
// NULL per ADR-061 §D.
type OrgInvitation struct {
	ID                 string
	OrgID              string
	Email              string
	Role               OrgRole
	TokenHash          []byte
	InvitedByAccountID *string
	ExpiresAt          time.Time
	ConsumedAt         *time.Time
	RevokedAt          *time.Time
	AcceptingAccountID *string
	CreatedAt          time.Time
}

// PersonalOrgNamespace is the UUID v5 namespace that derives the
// deterministic personal-org slug from each account's id (issue #190
// / ADR-061, PR 3). Frozen at PR 3 design time; any rotation
// requires a new ADR.
//
// Generated via
// uuid.NewSHA1(uuid.NameSpaceURL,
//
//	[]byte("onebox-faas/iam-6/personal-org-namespace/v1"))
//
// at design time and pinned here via uuid.MustParse so the compiler
// enforces well-formedness. The literal is binary-stable across
// google/uuid versions because the SHA-1 input is a fixed byte slice
// (precedent: pkg/meter/sampler.go's FloorNamespace).
//
// TestPersonalOrgNamespaceFrozen re-derives the value from the
// v1-locked namespace string and asserts equality — it must remain
// in lockstep if this file is ever edited.
var PersonalOrgNamespace = uuid.MustParse("1f7c8c29-273e-5a18-ae00-58fceba4fe6c")

// PersonalOrgSlug returns the deterministic personal-org slug for an
// account id. Pure function: same input → same output, every call,
// every process. The 14-char shape ("u-" + 12 hex chars) is the
// shortest valid slug that fits the orgs_slug_shape CHECK
// (`^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$`) while being machine-derived
// rather than user-supplied.
//
// First-write-wins on the slug CHECK is preserved across retries
// because the slug is deterministic — a backfill that re-runs
// produces the same slug for each account row, and the
// orgs_one_personal_per_account_uniq partial unique is the SQL-level
// tripwire against any concurrent caller.
func PersonalOrgSlug(accountID string) string {
	hex := strings.ReplaceAll(uuid.NewSHA1(PersonalOrgNamespace, []byte(accountID)).String(), "-", "")
	return "u-" + hex[:12]
}

// CreateAccountWithPersonalOrgParams is the input bundle for the
// PR 3 wrap. Email and Plan are required; the personal org's slug
// is derived deterministically from the account id (PersonalOrgSlug),
// name defaults to "Personal", status defaults to OrgStatusActive.
type CreateAccountWithPersonalOrgParams struct {
	Email string
	Plan  api.Plan
}

// CreateAccountWithPersonalOrgResult bundles the freshly minted
// account + personal org so callers can read both without an extra
// round-trip. PR 3 callsites currently read only Account; the
// PersonalOrg field is reserved for PR 5's LoadOrg middleware.
type CreateAccountWithPersonalOrgResult struct {
	Account     Account
	PersonalOrg Org
}

// ----------------------------------------------------------------------------
// Edge rules (ADR-089, planned)
//
// A customer-configurable resource that runs in pkg/gateway BEFORE
// host→app resolution. One table + jsonb action + closed-kind
// vocabulary unifies seven per-kind surfaces (route / rewrite /
// redirect / headers / cors / jwt / ip) into a single priority-ordered
// matcher. See docs/adr/089-edge-rules.md for the design decisions;
// this file is the Go type half.
//
// Why jsonb: seven per-kind tables would multiply the Store
// interface by ~5× for the same CRUD. The schema CHECK on `kind` +
// per-kind Validate() in pkg/api/dto.go is the validation tripwire.
// ----------------------------------------------------------------------------

// EdgeRuleKind is the closed vocabulary for edge_rules.kind. The
// gateway matcher's Kind switch is the compile-time guard against a
// stray string landing in a default switch arm. Mirrors the schema
// CHECK in migrations/00192_edge_rules.sql.
type EdgeRuleKind string

const (
	EdgeRuleKindRoute    EdgeRuleKind = "route"
	EdgeRuleKindRewrite  EdgeRuleKind = "rewrite"
	EdgeRuleKindRedirect EdgeRuleKind = "redirect"
	EdgeRuleKindHeaders  EdgeRuleKind = "headers"
	EdgeRuleKindCORSA    EdgeRuleKind = "cors"
	EdgeRuleKindJWT      EdgeRuleKind = "jwt"
	EdgeRuleKindIP       EdgeRuleKind = "ip"
	// EdgeRuleKindValidate runs the inbound request body through the
	// customer's JSON Schema before the wake gate; rejections return
	// 422 request_validation_failed without paying a cold-boot cost.
	// Plan-gated Free-and-above (no IsPaidOnly change). Schema lives
	// inline in action jsonb, capped at api.MaxEdgeRuleValidateSchemaBytes.
	// See migrations/00214_edge_rules_kind_validate.sql for the
	// schema CHECK widening.
	EdgeRuleKindValidate EdgeRuleKind = "validate"
	// EdgeRuleKindLimit caps the inbound request body at a per-rule
	// byte threshold before the wake gate; an oversize request is
	// rejected with 413 request_too_large without paying a cold-boot
	// cost. The cap is the standalone primitive (vs the validate
	// kind's body-cap side effect): a customer who only wants
	// per-route body-size protection declares this kind without
	// shipping a JSON Schema. Plan-gated Free-and-above (no
	// IsPaidOnly change). MaxBodyBytes ≤ api.MaxRequestBodyBytes
	// (buffered path) and an optional MaxBodyBytesStreaming ≤
	// api.MaxBodyBytesStreaming (streaming opt-in); both clamped
	// at apid-create time. See migrations/00219_edge_rules_kind_limit.sql
	// for the schema CHECK widening.
	EdgeRuleKindLimit EdgeRuleKind = "limit"
	// EdgeRuleKindMaintenance short-circuits a matched
	// (host, path, http_method) request with 503 + Retry-After
	// before the wake gate. ADR-091 amendment (PR-A #??? / PR-B /
	// PR-C). Per-rule RetryAfterSeconds overrides the platform
	// default api.EdgeRuleMaintenanceRetryAfterSeconds (60 s); the
	// hard upper bound api.MaxEdgeRuleMaintenanceRetryAfterSeconds
	// (24 h) is enforced at apid-create time. Optional Message
	// goes into Problem.detail (≤ 512 B; same payload-size budget
	// as EdgeRuleValidateAction.Schema). Plan-gated Free-and-above
	// (no IsPaidOnly change). See migrations/00236_edge_rules_kind_maintenance.sql
	// for the schema CHECK widening.
	EdgeRuleKindMaintenance EdgeRuleKind = "maintenance"
	// EdgeRuleKindGeo is the country allow/deny primitive (ADR-091 D21).
	// Migration 00229 widens the schema CHECK from 9 to 10 values
	// (post-00219 'limit', post-00214 'validate'). Not in IsPaidOnly
	// — Free customers get one rule under a tighter per-app quota
	// (Limits.EdgeRulesGeoPerApp=1).
	EdgeRuleKindGeo EdgeRuleKind = "geo"
	// EdgeRuleKindThrottle is the per-route token-bucket rate limit
	// (ADR-091 D20.5 amendment, issue #881). Customers tighten the
	// per-route rps/burst below their plan's plan.RateLimitRPS — the
	// apid validator enforces the sub-plan ceiling; the gateway
	// compiler enforces it again at load time. Plan-gated
	// Free-and-above (no IsPaidOnly change) with the same per-app
	// quota posture as geo: Free=1, Hobby=5, Pro=25, Scale=100
	// (Limits.EdgeRulesThrottlePerApp). Migration 00244 widens the
	// schema CHECK to admit the value.
	EdgeRuleKindThrottle EdgeRuleKind = "throttle"
	// EdgeRuleKindBudget pins a per-request wall-clock budget on
	// the matched (host, path, method) tuple (ADR-093 §Decision).
	// The runtime is `pkg/reqbudget`: the gateway matcher resolves
	// the matched rule and stamps the budget onto the inbound ctx
	// via `reqbudget.WithRemaining`, then every downstream hop
	// (JWT verify, forward, gRPC, DB) propagates remaining time via
	// `reqbudget.WithOverhead` / `WithCeiling`. Deadline fire
	// surfaces as 504 + RFC 7807 `code: request_budget_exceeded`.
	// Open to Free and every other plan (no IsPaidOnly change — a
	// 3 s default budget is a baseline safety floor that all
	// customers benefit from). See
	// migrations/00254_edge_rules_kind_budget.sql for the schema
	// CHECK widening.
	EdgeRuleKindBudget EdgeRuleKind = "budget"
	// EdgeRuleKindCache stamps a per-(app, host, path, vary) TTL on
	// the matched GET/HEAD response (ADR-122 §Decision). The runtime
	// is `pkg/gateway/response_cache.go`; the gateway matcher resolves
	// the matched rule and serves a stored body when (a) the request
	// is unauthenticated — authed requests are a hard bypass and are
	// never stored or served — (b) the response is cacheable
	// (2xx/3xx, no Set-Cookie, no Cache-Control: no-store/private,
	// body under the per-entry byte cap), and (c) the entry is fresh
	// (or, on origin failure only, within stale_if_error_seconds).
	// Plan-gated via EdgeRulesCachePerApp on pkg/api/limits.go
	// (Free 0 / Hobby 1 / Pro 5 / Scale 20) — cache is NOT in
	// IsPaidOnly() (geo/throttle precedent: gated by per-app count,
	// not a per-plan bool). See
	// migrations/00321_edge_rules_kind_cache.sql for the schema
	// CHECK widening.
	EdgeRuleKindCache EdgeRuleKind = "cache"
)

// IsValid reports whether k is a closed-set kind. New kinds land via
// ADR; this guard is the compile-time tripwire for a stale gateway
// switch.
func (k EdgeRuleKind) IsValid() bool {
	switch k {
	case EdgeRuleKindRoute, EdgeRuleKindRewrite, EdgeRuleKindRedirect,
		EdgeRuleKindHeaders, EdgeRuleKindCORSA, EdgeRuleKindJWT,
		EdgeRuleKindIP, EdgeRuleKindValidate, EdgeRuleKindLimit,
		EdgeRuleKindMaintenance, EdgeRuleKindThrottle, EdgeRuleKindGeo,
		EdgeRuleKindBudget, EdgeRuleKindCache:
		return true
	}
	return false
}

// IsPaidOnly reports whether the kind is Hobby+ gated (ADR-089 §7).
// The apid handler enforces this before insert; the gatewayd matcher
// does NOT — a paid-only rule for a Free customer fails at create
// time, never at request time.
//
// EdgeRuleKindGeo is NOT in this set — geo is available on Free
// (ADR-091 D21 sub-decision hop). The tighter Free-tier guardrail is
// enforced via Limits.EdgeRulesGeoPerApp (Free=1, Hobby=5, Pro=25,
// Scale=100) inside the CreateEdgeRuleIfUnderQuota FOR UPDATE lock,
// not via a Hobby+ plan gate. Rationale: the abuse-desk customer
// persona needs ONE geo rule on Free ("block everything except DE")
// before they'll convert; locking them out at the plan gate forces
// them to upgrade for a feature they haven't sized yet.
func (k EdgeRuleKind) IsPaidOnly() bool {
	return k == EdgeRuleKindJWT || k == EdgeRuleKindIP
}

// EdgeRuleRouteAction re-targets the request to another app owned by
// the same account. The target slug is resolved by the gatewayd
// matcher against the per-account app lookup; cross-account routing
// is deferred to a future ADR.
type EdgeRuleRouteAction struct {
	TargetAppSlug string `json:"target_app_slug"`
}

// EdgeRuleRewriteAction mutates the request path before forwarding.
// From is a glob prefix; To is the replacement. A trailing "*" on
// From captures the tail and exposes it as $1 in To — mirrors the
// nginx/CloudFront rewrite shape so customer config is portable.
type EdgeRuleRewriteAction struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// EdgeRuleRedirectAction is a 3xx short-circuit. StatusCode is one of
// {301,302,307,308}. To is a URL or path (with $1 capture from the
// path glob). Headers are stamped on the redirect response.
type EdgeRuleRedirectAction struct {
	StatusCode int               `json:"status_code"`
	To         string            `json:"to"`
	Headers    map[string]string `json:"headers,omitempty"`
}

// EdgeRuleHeaderOp is one mutation. Action ∈ {add,set,remove}; Value
// is empty for "remove". The gatewayd matcher enforces a hard-coded
// blacklist (Host, Content-Length, Transfer-Encoding, Connection,
// x-faas-*) at apply time.
type EdgeRuleHeaderOp struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
	Action string `json:"action"`
}

// EdgeRuleHeadersAction mutates request headers BEFORE auth and
// response headers AFTER streaming. RequestHeaders and
// ResponseHeaders are independent; the gateway applies each side at
// the matching hook.
type EdgeRuleHeadersAction struct {
	RequestHeaders  []EdgeRuleHeaderOp `json:"request_headers,omitempty"`
	ResponseHeaders []EdgeRuleHeaderOp `json:"response_headers,omitempty"`
}

// EdgeRuleCORSAction stamps CORS response headers and handles
// preflight in-process. AllowOrigins is the closed allowlist
// (["https://app.example.com"] or ["*"]); AllowMethods is required;
// AllowCredentials toggles the credentials header; MaxAgeSeconds
// stamps Access-Control-Max-Age on preflight responses.
//
// CorsPresetID (issue #975 #4 PR-B / ADR-129 D1/D2) is the
// nullable pointer to a cors_presets row. When set, the inline
// action fields MUST be empty/zero — the preset is the entire
// policy. The compile-side merge helper
// (pkg/state.MergeCorsPresetIntoRule, kind=cors branch in
// cmd/gatewayd-internal/edge_rules.go::compileCORSRules) resolves
// the preset's allow_origins / allow_methods / allow_headers /
// expose_headers / allow_credentials / max_age_seconds into the
// runtime CORS response. The EdgeRulesWire field on the wire DTO
// (pkg/api/dto.go) mirrors this as `cors_preset_id` and the
// apid-write boundary rejects `cors_preset_id + any non-empty
// inline field` with 422 (ADR-129 D2 mutual exclusivity).
type EdgeRuleCORSAction struct {
	AllowOrigins     []string `json:"allow_origins"`
	AllowMethods     []string `json:"allow_methods"`
	AllowHeaders     []string `json:"allow_headers,omitempty"`
	ExposeHeaders    []string `json:"expose_headers,omitempty"`
	AllowCredentials bool     `json:"allow_credentials"`
	MaxAgeSeconds    int      `json:"max_age_seconds"`
	// CorsPresetID is *string (nullable). Empty pointer = inline
	// policy only. Non-nil pointer = preset reference; inline
	// fields above MUST be empty/zero. Migration 00428 adds
	// edge_rules.cors_preset_id as the SQL-side mirror.
	CorsPresetID *string `json:"cors_preset_id,omitempty"`
}

// EdgeRuleJWTAction validates an inbound Bearer JWT against a JWKS
// endpoint (ADR-089 §6). Issuer is required; Audience is optional
// (empty = skip aud check); Algorithms is the closed set of allowed
// algs. RequiredClaims enforces a key=value check on top of the
// standard iss/aud/exp/nbf validation.
type EdgeRuleJWTAction struct {
	Issuer         string            `json:"issuer"`
	Audience       []string          `json:"audience,omitempty"`
	JWKSURL        string            `json:"jwks_url"`
	Algorithms     []string          `json:"algorithms"`
	RequiredClaims map[string]string `json:"required_claims,omitempty"`
}

// EdgeRuleIPAction is a CIDR allow/deny evaluator. Allow empty =
// "no allowlist, only the deny list applies". Deny is evaluated AFTER
// allow so a single-IP deny sticks even when the allow list is broad.
type EdgeRuleIPAction struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// EdgeRuleValidateAction carries the customer's JSON Schema + the
// per-rule gating policy for kind=validate. Schema is the raw schema
// body the customer posted (no re-serialise) so the SHA-256 cache key
// in pkg/edgevalidate matches byte-for-byte across apid↔gatewayd.
//
// Per-request gating:
//   - ContentTypes is the optional media-type allowlist; empty ==
//     validate any Content-Type. Closed set application/* (the
//     spec runtime is JSON; non-JSON schemas are out of scope).
//   - ApplyWhileStreaming decides whether validation fires on the
//     streaming response path (issue / ADR-047). Default false
//     mirrors the §4.1 Accept: application/json opt-out: a customer
//     who emits SSE with `streaming_enabled=true` keeps validation
//     off until they opt in per-rule.
//   - RejectOnUnknownFields sets additionalProperties=false (Draft
//     2020-12) before the schema is compiled. Default false keeps
//     the schema body byte-stable.
//   - MaxBodyBytes is the per-rule inbound body cap. Default 0
//     means "inherit api.MaxRequestBodyBytes". The plan cap is
//     enforced as a sanity floor — MaxBodyBytes > plan cap is a
//     create-time 422 from pkg/api/dto.go.
type EdgeRuleValidateAction struct {
	Schema              json.RawMessage `json:"schema"`
	ContentTypes        []string        `json:"content_types,omitempty"`
	ApplyWhileStreaming bool            `json:"apply_while_streaming,omitempty"`
	RejectOnUnknown     bool            `json:"reject_on_unknown_fields,omitempty"`
	MaxBodyBytes        int             `json:"max_body_bytes,omitempty"`
	// ValidateMode (issue #975 #3 / Mega-Foundation #979-a)
	// mirrors the wire-side field. Default empty == 'block' to
	// match the schema-side default at 00293. The state mirror
	// is intentionally permissive — the closed-set enforcement
	// lives at the apid write boundary (pkg/api.Validate).
	ValidateMode string `json:"validate_mode,omitempty"`
}

// EdgeRuleLimitAction carries the per-rule body caps for kind=limit.
// MaxBodyBytes is the buffered-path cap (≤ api.MaxRequestBodyBytes,
// 25 MiB); MaxBodyBytesStreaming is the streaming opt-in cap (≤
// api.MaxBodyBytesStreaming, 100 MiB). The streaming cap defaults to
// 0 ("unspecified") and only takes effect on requests that opt into
// the streaming response path (operator gate FAAS_GATEWAY_STREAMING +
// app-level streaming_enabled + request Accept: application/json);
// non-streaming requests still cap at MaxBodyBytes.
//
// Clamps and the negative-rejection check live in
// pkg/api/dto.go::EdgeRuleLimitAction.Validate. The state mirror
// carries the values verbatim so the gatewayd compile step can
// defence-in-depth against any direct-DB write that bypassed
// apid-Validate (cmd/e2e/edge_rules_common_test.go::seedEdgeRuleDirect).
type EdgeRuleLimitAction struct {
	MaxBodyBytes          int `json:"max_body_bytes"`
	MaxBodyBytesStreaming int `json:"max_body_bytes_streaming,omitempty"`
}

// EdgeRuleMaintenanceAction carries the per-rule 503 + Retry-After
// payload for kind=maintenance. RetryAfterSeconds overrides the
// platform default api.EdgeRuleMaintenanceRetryAfterSeconds (60 s)
// when > 0; 0 means "use the platform default". The hard upper
// bound (api.MaxEdgeRuleMaintenanceRetryAfterSeconds, 24 h) is
// enforced in pkg/api/dto.go::EdgeRuleMaintenanceAction.Validate so
// a customer cannot ship a rule that asks a client to back off for
// a week. Message is an optional operator-friendly string that goes
// into Problem.detail (≤ 512 B; same payload-size budget as
// EdgeRuleValidateAction.Schema). The state mirror carries the
// values verbatim so the gatewayd compile step can defence-in-depth
// against any direct-DB write that bypassed apid-Validate
// (cmd/e2e/edge_rules_common_test.go::seedEdgeRuleDirect).
type EdgeRuleMaintenanceAction struct {
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
	Message           string `json:"message,omitempty"`
}

// EdgeRuleGeoAction is an ISO 3166-1 alpha-2 country allow/deny
// evaluator. The wire shape mirrors EdgeRuleIPAction exactly — Allow
// and Deny are both lists of country codes ("DE", "FR", "US"). The
// gateway matcher evaluate order is identical to IP: Deny is evaluated
// AFTER Allow so a single-country deny sticks even when the allow
// list is broad. The plan wire-shape use case is
// {action: "deny", countries: ["US"]} which decodes into
// {Allow: [], Deny: ["US"]} — the "action" field name is a
// Cloudflare idiom we deliberately do NOT adopt on the wire (the IP
// action set the precedent, and the wire schema is closed for kind=ip).
//
// EdgeRuleGeoAction is the eighth kind-tagged union member, gated
// by EdgeRuleKindGeo above. Membership validation (closed-set
// country codes) lives in pkg/api/dto.go's EdgeRuleGeoAction.Validate
// — the state-side mirror is intentionally minimal, same as the
// other seven kinds.
type EdgeRuleGeoAction struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// EdgeRuleBudgetAction carries the per-rule wall-clock budget for
// kind=budget (ADR-093 §Decision). BudgetMs is the per-request budget
// the gateway stamps onto the inbound ctx via
// `reqbudget.WithRemaining`. The runtime range is [1 ms,
// api.RequestBudgetMaxMs]; 0 / negative / > ceiling is rejected at
// apid-create time (pkg/api/dto.go::EdgeRuleBudgetAction.Validate)
// and clamped at cmd-side compileBudgetRules as defence-in-depth
// against a direct-DB write.
//
// AllowOverrideHeader is the optional customer-tunable knob: when
// set, the gateway reads the named HTTP header (default
// `x-faas-budget-ms`) on the inbound request and, if it parses as a
// positive integer ≤ api.RequestBudgetMaxMs, uses that value as the
// per-request budget for that single request — a runtime
// per-customer override of the static rule's BudgetMs. An absent or
// unparseable header falls through to BudgetMs unchanged. Header
// overrides never WIDEN past BudgetMs or the per-plan
// api.RequestBudgetMaxMs ceiling.
//
// The state mirror carries the values verbatim so the gatewayd
// compile step (cmd/gatewayd-internal/edge_rules.go::compileBudgetRules)
// can defence-in-depth against any direct-DB row that bypassed
// apid-Validate.
type EdgeRuleBudgetAction struct {
	BudgetMs            int    `json:"budget_ms"`
	AllowOverrideHeader string `json:"allow_override_header,omitempty"`
}

// EdgeRuleCacheAction is the per-rule TTL + vary parameter set for
// kind=cache (ADR-122 §Decision). The runtime is
// pkg/gateway/response_cache.go::ResponseCache; the apply step is
// pkg/gateway/handler_apply_edge_rule_cache.go. The apid-create
// validator (pkg/api/dto.go::EdgeRuleCacheAction.Validate) enforces
// MaxAgeSeconds/StaleIfErrorSeconds bounds and a closed VaryOn
// vocabulary.
//
// MaxAgeSeconds is the fresh window in seconds (default 60, range
// [0, 3600]). A zero value disables fresh-cache hits but still
// permits stale-on-error within StaleIfErrorSeconds.
//
// StaleIfErrorSeconds is the post-fresh window in seconds during
// which a stored entry MAY be served ONLY on origin failure (wake
// gate failure or upstream 5xx/timeout). Hard cap 300 — exceeding
// the cap trips Validate. Stale-while-revalidate (serving stale
// while a refresh runs in the background) is explicitly out of
// scope and not supported by this field.
//
// VaryOn is the closed set of non-credential header names whose
// values participate in the cache key. Closed vocabulary is
// {Accept-Language, Accept-Encoding}; Authorization, Cookie, and
// any other credential-bearing header are hard bypasses (never a
// key dimension) — a principal-keyed cache would be a separate ADR.
// Empty slice means "no vary dimension beyond the URL" and is the
// default.
//
// Methods is the optional method allowlist (default {GET, HEAD}).
// Only idempotent methods are cacheable; anything outside this set
// trips Validate.
//
// Per-route byte cap (1 MiB) and per-instance byte ceiling are
// constants in pkg/gateway/response_cache.go, not per-rule knobs,
// so a single misconfigured rule cannot blow the in-memory budget.
type EdgeRuleCacheAction struct {
	MaxAgeSeconds       int      `json:"max_age_seconds"`
	StaleIfErrorSeconds int      `json:"stale_if_error_seconds"`
	VaryOn              []string `json:"vary_on,omitempty"`
	Methods             []string `json:"methods,omitempty"`
}

// EdgeRuleThrottleAction is the per-rule token-bucket parameter set
// for kind=throttle (ADR-091 D20.5 amendment, issue #881). The
// runtime is pkg/gateway/ratelimit.go::Limiter — the gateway matcher
// resolves the matched rule and consumes one token per request from a
// bucket keyed by (appID, ruleID). The bucket is LRU-evicted (see
// ratelimit.go::NewLimiterWithLRU) so the unbounded route-key space
// is bounded by the configured edge-rule count.
//
// RequestsPerSecond is the refill rate (float, ≥ 1, ≤ plan.RateLimitRPS).
// Burst is the bucket ceiling (int, ≥ 1, ≤ plan.RateLimitBurst). The
// sub-plan-ceiling is enforced at apid-create time
// (pkg/api/dto.go::EdgeRuleThrottleAction.Validate) and again at
// gatewayd compile time
// (cmd/gatewayd-internal/edge_rules.go::compileThrottleRules).
//
// The wire shape's primary motivation for float rps: the
// recommendation endpoint (cmd/apid/handlers_throttle_suggestions.go)
// emits ceil(observed_rps * 2) which can be a non-integer —
// coercing to int would shave headroom from the suggestion. The
// runtime spends the float as `tokens += dt * rps` so fractional
// values are exact under the refill formula.
//
// Per-IP sub-keying is deliberately absent in v1 — a per-IP boolean
// would multiply the limiter's map cardinality by unique-IP count
// (unbounded, attacker-controlled). If a per-IP variant is wanted
// later it gets its own bounded design (ADR-093-style cap + an
// `__ip_other__` overflow that still consumes the parent rule's
// bucket). Shipping the field now and bounding it later is not safe.
//
// Phase 3 (ADR-091 D20.5 amendment 4, ADR-104, issue #881 Phase 3)
// extends the wire shape with optional per-consumer keying. The new
// fields are byte-identical to the DTO mirror at
// pkg/api/dto.go::EdgeRuleThrottleAction — gatewayd reads them through
// the limiter constructor (pkg/gateway/ratelimit.go::AllowWithConsumerKey,
// Phase 3) which owns the __other__ collapse. See ADR-104 §"Consequences"
// for the load-bearing safety property (the collapse bucket is
// pinned non-evictable so an attacker can't bypass the throttle by
// minting many keys).
type EdgeRuleThrottleAction struct {
	RequestsPerSecond float64 `json:"requests_per_second"`
	Burst             int     `json:"burst"`
	KeyBy             string  `json:"key_by,omitempty"`
	JWTClaimName      string  `json:"jwt_claim_name,omitempty"`
	MaxKeysPerRule    int     `json:"max_keys_per_rule,omitempty"`
}

// EdgeRuleAction is the kind-tagged union stored in edge_rules.action
// as jsonb. The wire shape lives in pkg/api/dto.go (one struct per
// kind); the state-side mirror is intentionally minimal — the
// gatewayd matcher only reads Kind + the kind-specific subset it
// needs.
type EdgeRuleAction struct {
	Kind     EdgeRuleKind            `json:"kind"`
	Route    *EdgeRuleRouteAction    `json:"route,omitempty"`
	Rewrite  *EdgeRuleRewriteAction  `json:"rewrite,omitempty"`
	Redirect *EdgeRuleRedirectAction `json:"redirect,omitempty"`
	Headers  *EdgeRuleHeadersAction  `json:"headers,omitempty"`
	CORS     *EdgeRuleCORSAction     `json:"cors,omitempty"`
	JWT      *EdgeRuleJWTAction      `json:"jwt,omitempty"`
	IP       *EdgeRuleIPAction       `json:"ip,omitempty"`
	// Validate carries the JSON Schema body for kind=validate. The
	// Schema blob is byte-exact what the customer POSTed (no
	// re-serialise) so the SHA-256 cache key in pkg/edgevalidate is
	// stable across apid↔gatewayd round-trips. The runtime caps
	// (MaxEdgeRuleValidateSchemaBytes + per-rule MaxBodyBytes) are
	// enforced in pkg/api/dto.go::EdgeRuleValidateAction.Validate
	// at apid-create time; the state mirror carries them verbatim.
	Validate *EdgeRuleValidateAction `json:"validate,omitempty"`
	// Limit carries the per-rule body caps for kind=limit.
	// MaxBodyBytes is the buffered-path cap; MaxBodyBytesStreaming
	// is the streaming opt-in cap (0 = unspecified, defer to
	// MaxBodyBytes). The companion validate kind's MaxBodyBytes
	// stays — it is the "cap the body I am about to schema-check"
	// knob; kind=limit is the standalone gate.
	Limit *EdgeRuleLimitAction `json:"limit,omitempty"`
	// Maintenance carries the per-rule 503 + Retry-After payload
	// for kind=maintenance. RetryAfterSeconds > 0 overrides the
	// platform default (api.EdgeRuleMaintenanceRetryAfterSeconds);
	// 0 means "use the default". Message is an optional human-
	// readable string that goes into Problem.detail. The applier
	// (pkg/gateway.(*Handler).applyEdgeRuleMaintenance, §4.1.2.13)
	// sets Retry-After via api.WriteProblem + WithHeader and emits
	// a `gateway_edge_rule_match_total{kind="maintenance"}` metric.
	Maintenance *EdgeRuleMaintenanceAction `json:"maintenance,omitempty"`
	// Geo carries the country allow/deny list for kind=geo. Plan
	// gating (Free allowed with 1 rule, Hobby+ 5/25/100 by plan) is
	// enforced at apid-create time via Limits.EdgeRulesGeoPerApp —
	// see pkg/api/dto.go::EdgeRuleGeoAction.Validate.
	Geo *EdgeRuleGeoAction `json:"geo,omitempty"`
	// Throttle carries the per-route token-bucket parameters for
	// kind=throttle. The Sub-plan ceiling (rps ≤ plan.RateLimitRPS,
	// burst ≤ plan.RateLimitBurst) is enforced at apid-create time
	// (pkg/api/dto.go::EdgeRuleThrottleAction.Validate) and again
	// at gatewayd compile time
	// (cmd/gatewayd-internal/edge_rules.go::compileThrottleRules).
	// Run-time enforcement is pkg/gateway/ratelimit.go's LRU
	// limiter; the bucket is keyed by (appID, ruleID) so a single
	// rule's throttle does not bleed into another rule's bucket.
	Throttle *EdgeRuleThrottleAction `json:"throttle,omitempty"`
	// Budget carries the per-request wall-clock budget for kind=budget.
	// BudgetMs is the per-request budget the gateway stamps via
	// `reqbudget.WithRemaining`. AllowOverrideHeader is the optional
	// per-customer-tunable knob (`x-faas-budget-ms` by default).
	// See EdgeRuleBudgetAction for the full per-field contract.
	Budget *EdgeRuleBudgetAction `json:"budget,omitempty"`
	// Cache carries the per-route TTL knobs for kind=cache
	// (ADR-122 §Decision). MaxAgeSeconds is the fresh window
	// (default 60, capped per pkg/api/dto.go Validate).
	// StaleIfErrorSeconds is the post-fresh window during which a
	// stored entry may be served after origin failure ONLY (default
	// 300, hard cap 300). VaryOn is the closed set of non-credential
	// header names whose values discriminate cache entries
	// (Accept-Language / Accept-Encoding only — Authorization and
	// cookies are hard bypasses by construction, never a key
	// dimension). Methods is the optional method list (default
	// {GET, HEAD}). The runtime is pkg/gateway/response_cache.go;
	// the apply step is pkg/gateway/handler_apply_edge_rule_cache.go.
	Cache *EdgeRuleCacheAction `json:"cache,omitempty"`
}

// EdgeRule is the in-memory row mirrored from edge_rules.
type EdgeRule struct {
	ID           string
	AccountID    string
	AppID        string
	MatchHost    string
	MatchPath    string
	MatchMethods []string
	Priority     int
	Enabled      bool
	Kind         EdgeRuleKind
	Action       EdgeRuleAction
	// CorsPresetID (issue #975 #4 PR-B / ADR-129 D1) is the
	// top-level nullable mirror of edge_rules.cors_preset_id.
	// Pointer so the SQL NULL is distinguishable from "" (a
	// pre-existing convention: EdgeRule.ID is also a string but
	// we use *string for nullable FK columns). The compile-side
	// helper pkg/state.MergeCorsPresetIntoRule uses this to
	// resolve the preset; the runtime edge_rules.go reads
	// Action.CORS.CorsPresetID (the JSONB mirror) so both stay
	// in sync via scanEdgeRuleCols.
	CorsPresetID *string
	// ValidateMode (issue #975 item #3 / ADR-128) is the
	// source-of-truth column for kind=validate enforcement.
	// Empty == 'block' (the SQL-side default at 00293 also
	// defaults to 'block' for pre-existing rows). Action.Validate
	// .ValidateMode is kept as a read-side fallback for one
	// release per ADR-128 §D2 so legacy JSONB-only rows
	// preserve the customer's intended mode.
	ValidateMode string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CorsPreset is the in-memory row mirrored from cors_presets (issue
// #975 item #4 / Mega-Foundation #979-b). One row = one reusable CORS
// configuration a customer attaches to a kind=cors edge rule via the
// per-rule cors.preset_id field (PR-B, slot 00295).
//
// Scope: account-scoped when AppID is the empty string, app-scoped
// when AppID is set. The UNIQUE constraint
// (account_id, COALESCE(app_id, '00..00'), name) backs both shapes
// from a single index; the COALESCE-back pattern is documented in
// migrations/00294_cors_presets.sql.
//
// AllowOrigins / AllowMethods / AllowHeaders / ExposeHeaders are
// stored as Postgres text[] but the wire-side gate in pkg/api/dto.go
// rejects empty arrays for AllowOrigins and AllowMethods (a CORS rule
// without any origin allowlist is meaningless and a common footgun).
// The DB CHECKs in 00294 cover the size + name bounds only.
//
// AllowCredentials, MaxAgeSeconds, and AllowHeaders / ExposeHeaders
// use the rule-field-overrides-preset compile convention
// (cmd/gatewayd-internal/edge_rules.go::compileCORSRules): the
// rule's non-zero values win, the preset fills in the rest. A
// preset that ships only the allowlist is therefore a valid
// "convention" preset.
type CorsPreset struct {
	ID               string
	AccountID        string
	AppID            string
	Name             string
	Description      string
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	ExposeHeaders    []string
	AllowCredentials bool
	MaxAgeSeconds    int
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// AccountSpendSnapshot is the in-memory row mirrored from
// account_spend_snapshot (issue #1233 / ADR-123 / migrations/00350).
// meterd ticks write one row per (account, source) per
// AlertEvalInterval. The alert evaluator reads SUM(eur_cents)
// for the MTD window via MTDSpendEurCents.
//
// source is a closed vocabulary mirroring the
// account_spend_snapshot_source_chk DB constraint:
// 'running_seconds' | 'overage' | 'build_seconds' | 'snapshot_storage'.
type AccountSpendSnapshot struct {
	ID          string
	AccountID   string
	PeriodStart time.Time
	PeriodEnd   time.Time
	GBSeconds   float64
	EurCents    int64
	Source      string
	CreatedAt   time.Time
}

// TenantSurfaceCertExpiryState is the in-memory row mirrored from
// meterd_tenant_surface_cert_expiry_state (issue #1233 / ADR-123 /
// migrations/00351). The meterd cert-expiry refresher goroutine
// (cmd/meterd/alert_presets_ticks.go) updates the
// last_observed_cert_not_after + last_walk_status; the alert
// evaluator reads MinCertExpiryForApp to compute the
// cert_expiry_seconds metric.
type TenantSurfaceCertExpiryState struct {
	TenantSurfaceID          string
	AccountID                string
	AppID                    string
	Hostname                 string
	LastObservedCertNotAfter *time.Time
	LastWalkStatus           string
	LastRefreshedAt          time.Time
}

// AlertPreset is the in-memory row mirrored from alert_presets
// (issue #1233, ADR-123). Catalog rows are system-owned; the
// meterd + apid system-owner role is the only writer. Customers
// have SELECT-only access via the apid GET surface.
//
// The struct is read-only at the Store boundary — there is no
// Update / Delete / Create method on the Store interface for
// alert_presets. The only write path is migration 00348's
// idempotent seed.
//
// Comparison / Metric / WindowSpec mirror the alert_rules closed
// vocabularies byte-for-byte (the DB CHECK constraints in
// migrations/00347_alert_presets.sql pin this). When the
// evaluator's `observe` dispatch learns a new metric, the catalog
// can include it on the same PR — but a catalog entry MUST NOT
// reference a metric the evaluator has not learned, or the
// enable path would persist an alert_rules row whose metric the
// evaluator then drops at run-time.
type AlertPreset struct {
	ID                     string
	Name                   string
	DisplayName            string
	Description            string
	Category               string
	Metric                 string
	Comparison             string
	Threshold              float64
	WindowSpec             string
	DefaultCooldownMinutes int
	EnabledInCatalog       bool
	MinimumPlan            string
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// CreateEdgeRuleParams is the input bundle for CreateEdgeRule and
// CreateEdgeRuleIfUnderQuota. Action is marshalled to jsonb at the
// pgstore boundary. ValidateMode is the kind=validate enforcement
// mode (issue #975 item #3 / ADR-128) — empty string is coerced
// to 'block' at the SQL-side default (00293 NOT NULL DEFAULT
// 'block'), so callers can pass "" to opt into the strictest
// mode.
type CreateEdgeRuleParams struct {
	AccountID    string
	AppID        string
	MatchHost    string
	MatchPath    string
	MatchMethods []string
	Priority     int
	Enabled      bool
	Kind         EdgeRuleKind
	Action       EdgeRuleAction
	// CorsPresetID (issue #975 #4 PR-B / ADR-129 D1) is the
	// nullable FK target on edge_rules.cors_preset_id. nil =
	// inline-only policy. Non-nil must reference a preset owned
	// by the same account (the apid-Validate gate enforces this
	// before the row is written; the gateway compile path also
	// re-validates via MergeCorsPresetIntoRule).
	CorsPresetID *string
	ValidateMode string
}

// UpdateEdgeRuleParams carries the optional fields of
// UpdateEdgeRule. Every field is a pointer; nil = "leave alone".
// Action is *EdgeRuleAction because a nil means "do not touch the
// jsonb column"; a non-nil replaces it wholesale (the kind-tagged
// union has no partial-update shape — the customer re-sends the
// full action body). ValidateMode follows the same nil-skip
// pattern as the other optional scalars — nil means "do not
// touch the column"; non-nil replaces the column verbatim.
// CorsPresetID follows the same nil-skip pattern (ADR-129 D1) —
// a non-nil pointer (including one that points to "") replaces
// the column; nil leaves the FK untouched.
type UpdateEdgeRuleParams struct {
	MatchHost    *string
	MatchPath    *string
	MatchMethods *[]string
	Priority     *int
	Enabled      *bool
	Action       *EdgeRuleAction
	CorsPresetID **string
	ValidateMode *string
}

// EdgeRuleQuotaError is returned by CreateEdgeRuleIfUnderQuota when
// the per-app cap is reached. Mirrors AlertRuleQuotaError's shape;
// only the per-app scope exists today because edge rules are
// per-app (an account-wide variant is not a v1 surface).
//
// The Kind, PerKind, and PerAppOnly fields are populated only by
// the per-kind quota branch (ADR-091 D22). When set, the apid
// handler renders a distinct RFC 7807 code
// (`plan_edge_rule_kind_quota_reached`) so the customer sees the
// specific kind that tripped the cap ("kind=geo: 1/1 rules used
// on Free; upgrade to Hobby for 5"). The classic general-cap
// path leaves these fields zero and the apid handler uses the
// pre-existing `plan_edge_rules_quota_reached` code (or
// whatever it ends up named — see apid/handlers_edge_rules.go).
type EdgeRuleQuotaError struct {
	Limit      int
	Observed   int
	Kind       string // empty for the general cap; "geo" for the per-kind cap
	PerKind    bool   // true → cap is per-kind (EdgeRulesGeoPerApp)
	PerAppOnly bool   // true → cap is per-app (always true today; reserved for the per-account cap that doesn't exist yet)
}

func (e *EdgeRuleQuotaError) Error() string {
	if e.PerKind {
		return fmt.Sprintf("state: edge rule %s quota exceeded (limit=%d, observed=%d)", e.Kind, e.Limit, e.Observed)
	}
	return fmt.Sprintf("state: edge rule quota exceeded (limit=%d, observed=%d)", e.Limit, e.Observed)
}

// Is lets errors.As match the quota error type across the apid
// handler boundary (mirrors AlertRuleQuotaError.Is at types.go:1382
// for the cron sibling).
func (e *EdgeRuleQuotaError) Is(target error) bool {
	_, ok := target.(*EdgeRuleQuotaError)
	return ok
}

// ----------------------------------------------------------------------------
// ADR-098 connection-aware execution (§9.A). In-memory mirror of the
// data_upstreams + data_upstream_probes tables (migrations/
// 00226_data_upstreams.sql). The Store interface exposes typed
// DataUpstream / DataUpstreamProbe at the boundary so handlers don't
// touch pgtype. Field ordering matches the sqlc-generated DataUpstream /
// DataUpstreamProbe (pkg/state/sqlc/models.go) — pointer discipline for
// the nullable pair (LastRTTMs/LastProbedAt, RTTMs/ErrorClass) maps to
// the SQL CHECK pair constraints. The coalesce-empty-string convention
// for `text NULL` columns (DeclaredRegion, ProbeNode) matches the
// app_errors handler-side boundary (queries.sql:949-960).
// ----------------------------------------------------------------------------

// DataUpstreamSource is the closed vocabulary for data_upstreams.source.
// 'inferred' = apid env-classifier captured the host from a
// customer env value (ADR-098 D1.a). 'explicit' = the customer
// POSTed /v1/apps/{slug}/upstreams with the row (ADR-098 D4).
type DataUpstreamSource string

const (
	DataUpstreamSourceInferred DataUpstreamSource = "inferred"
	DataUpstreamSourceExplicit DataUpstreamSource = "explicit"
)

// DataUpstreamKind is the closed vocabulary for data_upstreams.kind +
// data_upstream_probes.kind. Fourteen values from ADR-098 D1. The
// SQL CHECK mirrors this set (migrations/00226_data_upstreams.sql);
// the Go side exposes the constants + IsValid() so callers fail fast
// on a typo at the API boundary instead of surfacing a 23514 at
// runtime.
type DataUpstreamKind string

const (
	DataUpstreamKindPostgres      DataUpstreamKind = "postgres"
	DataUpstreamKindRedis         DataUpstreamKind = "redis"
	DataUpstreamKindMongo         DataUpstreamKind = "mongo"
	DataUpstreamKindCassandra     DataUpstreamKind = "cassandra"
	DataUpstreamKindClickhouse    DataUpstreamKind = "clickhouse"
	DataUpstreamKindElasticsearch DataUpstreamKind = "elasticsearch"
	DataUpstreamKindOpensearch    DataUpstreamKind = "opensearch"
	DataUpstreamKindRabbitmq      DataUpstreamKind = "rabbitmq"
	DataUpstreamKindKafka         DataUpstreamKind = "kafka"
	DataUpstreamKindNats          DataUpstreamKind = "nats"
	DataUpstreamKindMinio         DataUpstreamKind = "minio"
	DataUpstreamKindMemcached     DataUpstreamKind = "memcached"
	DataUpstreamKindEtcd          DataUpstreamKind = "etcd"
	DataUpstreamKindS3            DataUpstreamKind = "s3"
	DataUpstreamKindHTTPSAPI      DataUpstreamKind = "https_api"
)

// IsValid reports whether k is one of the closed-set DataUpstreamKind
// constants. Cheap — used by the env-classifier (cmd/apid/extract.go,
// PR-B) to fail fast on an unknown kind before the SQL INSERT surfaces
// a 23514. Mirrors (EdgeRuleKind).IsValid() at types.go:3268.
func (k DataUpstreamKind) IsValid() bool {
	switch k {
	case DataUpstreamKindPostgres, DataUpstreamKindRedis, DataUpstreamKindMongo,
		DataUpstreamKindCassandra, DataUpstreamKindClickhouse,
		DataUpstreamKindElasticsearch, DataUpstreamKindOpensearch,
		DataUpstreamKindRabbitmq, DataUpstreamKindKafka, DataUpstreamKindNats,
		DataUpstreamKindMinio, DataUpstreamKindMemcached, DataUpstreamKindEtcd,
		DataUpstreamKindS3, DataUpstreamKindHTTPSAPI:
		return true
	}
	return false
}

// DataUpstream is one row of data_upstreams. apid is the only writer
// (ADR-098 D1.a / D4); schedd reads via the dashboard's
// GET /v1/apps/{slug}/upstreams (PR-B). Source / Kind are typed enums
// (DataUpstreamSource / DataUpstreamKind) so the wire shape matches
// the schema CHECKs. LastRTTMs / LastProbedAt are nullable pointers so
// the "never probed" shape (last_rtt_ms IS NULL on the wire; the
// sqlc read path projects pgtype.Int4.Valid) is distinguishable from
// "measured at 0ms".
type DataUpstream struct {
	ID        uuid.UUID
	AccountID uuid.UUID
	AppID     uuid.UUID
	Source    DataUpstreamSource
	Scope     string
	// DeploymentScope is the deployment this upstream belongs to.
	// ADR-098 amendment (issue #954, migration
	// 00281_data_upstreams_deployment_scope.sql) widens the dedupe
	// key to (app_id, scope, deployment_scope, kind, host, port);
	// default 'default' matches every pre-#954 row + every single-
	// deployment app. The apid env-classifier resolves the
	// deployment from the env-scope at PUT time via
	// pgstore.LiveDeploymentForScope (shim at pgstore.go:4111,
	// ErrNotFound → defaultEnvScope="default" fallback).
	DeploymentScope  string
	Kind             DataUpstreamKind
	Host             string
	Port             int
	HostRedactedHash string
	// DeclaredRegion is the operator / customer-supplied region
	// hint (NULL until the classifier derives it or the customer
	// sets it via POST). Empty string projects to NULL on the
	// wire (coalesce('') in the queries.sql read paths).
	DeclaredRegion string
	// LastRTTMs is the most recent probe RTT (nil = never
	// probed). Pointer so "never probed" projects to nil rather
	// than the SQL NULL sentinel.
	LastRTTMs *int
	// LastProbedAt pairs with LastRTTMs (nil until first probe).
	// Pointer so the pair-violation check on the SQL side stays
	// consistent with the typed boundary.
	LastProbedAt *time.Time
	LastSeenAt   time.Time
	CreatedAt    time.Time
}

// DataUpstreamProbe is one row of data_upstream_probes. meterd is
// the only writer; schedd reads via
// ListDataUpstreamProbesByHostRegion (PR-B). The PK is
// (id, sampled_at) — id is a caller-supplied uuidv7 so retry dedupe
// is trivial. RTTMs is nullable on ok=false rows (the SQL CHECK
// data_upstream_probes_ok_pair_chk enforces the pair). ErrorClass
// is the NULL on ok=true / NOT NULL on ok=false closed-set
// ('timeout' | 'refused' | 'tls_handshake' | 'dns' |
// 'unreachable').
type DataUpstreamProbe struct {
	ID               uuid.UUID
	HostRedactedHash string
	Region           string
	Kind             DataUpstreamKind
	SampledAt        time.Time
	// RTTMs is nil on ok=false rows. Pointer so the typed
	// boundary distinguishes "not measured" from "measured at
	// 0ms".
	RTTMs *int
	// OK is the TCP+TLS handshake outcome (true = handshake
	// completed within ProbeTimeoutMs).
	OK bool
	// ErrorClass is nil on ok=true rows. Closed-set on the SQL
	// side; the Go side mirrors with a string pointer so callers
	// can compare against the constants in
	// pkg/meter/upstream_probe.go (PR-C). Untyped here so the
	// PR-A addition doesn't force a pkg/meter import in
	// pkg/state.
	ErrorClass *string
	// ProbeNode is the meterd node's compute_nodes.name that
	// ran the probe. NULL on single-box installs. Empty string
	// projects to NULL on the wire (coalesce('') in the
	// queries.sql read paths).
	ProbeNode string
}

// DataUpstreamTarget is the deduplicated (host_redacted_hash,
// kind, port) tuple the meterd probe loop iterates on every
// tick. The plaintext host is NEVER on this struct — the
// probe knows the host only by hash (§11 secret rule).
type DataUpstreamTarget struct {
	HostRedactedHash string
	Kind             DataUpstreamKind
	Port             int
	// Host is the plaintext host. Returned ONLY to the
	// meterd probe loop (PR-C) so the dial can resolve
	// to a real address. The plaintext host NEVER
	// appears on the wire elsewhere (§11 invariant):
	// the Prom labels carry host_redacted_hash, the
	// audit kind carries host_redacted_hash, the pg_notify
	// payload carries host_redacted_hash. meterd loads
	// the host from this struct, dials, then drops it
	// on the floor.
	Host string
}

// OIDCTrustPolicy is the per-(account, issuer) admission rule for
// OIDC-derived bearer exchanges (issue #270 / ADR-101). Mirrors the
// 1:N variant of github_installations (one account can trust many
// issuers). SubjectPattern is a regex matched against the JWT
// `sub` claim (compile-once at the edgejwks layer; this type
// stays stringy for portability). RequiredClaims is the strict-
// equality gate (e.g. {"actor":"poyrazk"}); the regex variant
// lives on pkg/edgejwks.VerifierRule.RequiredClaimPatterns.
//
// audit_login='auto' marks a policy the system created on first use
// (the dashboard "refine" CTA uses this to distinguish "you set
// this" from "system defaulted this"). For an OIDC trust policy
// the "login" is the customer's account_id rather than a real
// login name — the column is reused for symmetry with the GitHub
// install shape.
type OIDCTrustPolicy struct {
	AccountID      string
	IssuerURL      string
	JWKSURL        string
	Audience       []string
	SubjectPattern string
	Algorithms     []string
	RequiredClaims map[string]string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	AuditLogin     string
}

// OIDCExchangedToken is the persisted row behind an exchanged
// short-lived bearer (fp_oidc_<48 hex>). The wire-side bearer is
// hashed (TokenHash); the row carries the OIDC provenance so the
// audit reader can answer "which CI job shipped this?" without
// joining against the IdP. TTL bounds GDPR deletion; the
// account_id FK CASCADE is the contractual guarantee.
//
// JTI is the optional JWT ID claim (empty if the IdP omits it).
// Customer-side billing attribution excludes the JTI — it's the
// audit-only key that lets a customer correlate a row to an IdP
// log entry when investigating a deploy.
type OIDCExchangedToken struct {
	ID        string
	AccountID string
	TokenHash []byte
	ExpiresAt time.Time
	IssuerURL string
	Subject   string
	Audience  []string
	JTI       string
	CreatedAt time.Time
}

// ToAPIKey projects the OIDC-bearer row into a synthetic state.APIKey
// for the principal stamp in pkg/auth/middleware. The struct is
// reused (no new type) so the principalHasScope + requireScope chain
// works unchanged. Status is fixed at APIKeyStatusActive — there is
// no revoked state for 5-min TTL tokens; the row disappears via TTL
// or FK CASCADE, both of which surface as ErrNotFound to the lookup.
//
// The method lives here (not on pkg/oidc.ExchangedToken) because Go
// type aliases don't carry over method sets defined on the alias:
//
//	type A = B  // A has whatever methods B has
//
// Since the store layer needs to call row.ToAPIKey() on the
// state-side value (the alias is the only public surface), the
// method set must be on the canonical type. The pkg/oidc-side
// alias gets the method for free.
func (e OIDCExchangedToken) ToAPIKey() APIKey {
	return APIKey{
		ID:        e.ID,
		AccountID: e.AccountID,
		Scopes:    []string{"deploy:write"},
		Status:    string(APIKeyStatusActive),
	}
}

// ---------------------------------------------------------------------------
// ADR-122 / issue #975 item #1: per-deployment OpenAPI document pin.
// ---------------------------------------------------------------------------

// OpenAPIDocMeta is the metadata slice returned alongside the captured
// document body. The doc bytes travel separately so the read path can
// short-circuit the JSON unmarshal on a Cache-Control hit (the CORS
// preset precedent applies the same way: the body is 128 KiB and the
// meta is a fixed-size struct). The fields are the columns of
// deployment_openapi_docs (migrations/00330_endpoint_discovery.sql)
// minus the doc payload itself.
//
// Source is the closed enum value 'cold_boot' or 'manual_upload'
// (matches the migrations/00330 CHECK constraint). Truncated is
// true when the guest-init probe read VSockCharacterizationMaxBody
// bytes and the body was longer; the apid PATCH surface treats it
// as a 200 with a warning header, not an error (the doc IS saved —
// the customer can replace it via PATCH).
//
// DocSHA256 is the canonical SHA-256 of the stored bytes — the
// migrations/00330 CHECK constraint pins it to exactly 32 bytes.
// Surface parity with the consumer_keys table: a column that carries
// the hash in the row, not derived-at-read, so the pgstore-side
// consistency check is a no-op (computed once at Upsert).
type OpenAPIDocMeta struct {
	DeploymentID string
	AccountID    string
	AppID        string
	Source       string
	ByteSize     int
	DocSHA256    []byte
	Truncated    bool
	CapturedAt   time.Time
	UpdatedAt    time.Time
}

// OpenAPIDocMaxBytes is the hard cap on the captured body. The
// guest-init probe hard-truncates at this size (ADR-122 §D4); the
// apid PATCH surface validates against the same constant. The
// per-plan cap is layered on top of this global constant via
// api.Plan.OpenAPIDocMaxBytes() (limits.go).
//
// The constant lives here (not in pkg/api/limits.go) because the
// guest-init probe reads it through the wire-vsock boundary and
// has no dependency on the api package's plan tables. The plan
// accessors fail-closed to 0 on unknown plans — the per-plan cap
// is a layer over the global cap, not a replacement.
const OpenAPIDocMaxBytes = 128 * 1024

// OpenAPIDocSource values mirror the migrations/00330 CHECK
// constraint. Centralised here so the pgstore and the apid handler
// agree on the enum without a string-literal drift. The
// consumer_keys equivalent is api.ConsumerKeyScope* (a closed set
// declared in the api package); we follow the same pattern but
// inline-declare these because the table is hidden behind the
// Store interface.
const (
	OpenAPIDocSourceColdBoot     = "cold_boot"
	OpenAPIDocSourceManualUpload = "manual_upload"
)

// StatusIncident (issue #599 / ADR-130 / cluster D commit 14 of
// the platform-observability mega-PR) is the in-memory mirror of
// the status_incidents table. The public status page
// (deploy/statuspage/index.html) reads this via the
// gatewayd-internal /v1/internal/slo.json endpoint, which
// fetches the open subset via ListOpenStatusIncidents. Operators
// create + resolve rows via the gregalectl CLI (`gregale status
// incident post|resolve`).
//
// Closed-set vocabulary at the schema layer (migrations/00412):
//   - component ∈ {apid, schedd, vmmd, gatewayd, meterd, imaged,
//     builderd, faas-control-plane}
//   - severity  ∈ {degraded, partial_outage, full_outage, maintenance}
//   - message ≤ 1024 chars (CHECK length cap so a paste of a 50
//     KB stack trace can't bloat the response).
//
// The component / severity strings are also defined as named
// constants below so the CLI surface can range-check before
// hitting the SQL CHECK.
type StatusIncident struct {
	ID         int64
	Component  string
	Severity   string
	Message    string
	PostedAt   time.Time
	ResolvedAt *time.Time
}

// StatusIncidentComponent* are the closed-set vocabulary for the
// status_incidents.component column (migrations/00412). Add a
// new component by appending a constant + extending the SQL CHECK
// (canonical DROP+ADD pair mirrors the migrations/00412 pattern).
const (
	StatusIncidentComponentApid             = "apid"
	StatusIncidentComponentSchedd           = "schedd"
	StatusIncidentComponentVmmd             = "vmmd"
	StatusIncidentComponentGatewayd         = "gatewayd"
	StatusIncidentComponentMeterd           = "meterd"
	StatusIncidentComponentImaged           = "imaged"
	StatusIncidentComponentBuilderd         = "builderd"
	StatusIncidentComponentFaasControlPlane = "faas-control-plane"
)

// StatusIncidentSeverity* are the closed-set vocabulary for the
// status_incidents.severity column (migrations/00412). Same
// migration-extension pattern as the component constants above.
const (
	StatusIncidentSeverityDegraded      = "degraded"
	StatusIncidentSeverityPartialOutage = "partial_outage"
	StatusIncidentSeverityFullOutage    = "full_outage"
	StatusIncidentSeverityMaintenance   = "maintenance"
)

// ---------------------------------------------------------------------------
// OpenAPI Import (issue #975 item #2 / ADR-126).
//
// The per-app table `app_openapi_docs` carries the customer's
// imported OpenAPI document — the "declared surface" side of the
// closed loop. It co-exists with the per-deployment
// deployment_openapi_docs (item #1): the latter holds what Gregale
// captured during cold boot, the former holds what the customer
// declared for the app.
// ---------------------------------------------------------------------------

// AppOpenAPIDocMeta is the metadata slice returned alongside the
// imported document body. Mirrors OpenAPIDocMeta but keys on
// (app_id, account_id) — one row per app, not per deployment.
//
// OpenAPIVersion is the closed enum value '3.0.0'..'3.1.1' (matches
// the migrations/00416 CHECK constraint). Source is the closed
// enum value 'manual_import' (item #2 does not admit cold-boot
// captures; cold-boot goes to deployment_openapi_docs from item #1).
//
// EndpointCount is the count of HTTP operations in the imported
// doc's paths.* — a generous ceiling for a single-app surface
// (Stripe-scale 700-operation docs would be split per-app, not
// per-spec). The SQL CHECK pins it 0..50; the apid layer enforces
// the abuse-surface cap of 50 via api.Plan.OpenAPIImportMaxEndpoints.
type AppOpenAPIDocMeta struct {
	AppID          string
	AccountID      string
	Source         string
	OpenAPIVersion string
	EndpointCount  int
	ByteSize       int
	DocSHA256      []byte
	CapturedAt     time.Time
	UpdatedAt      time.Time
}

// OpenAPIImportSource values mirror the migrations/00416 CHECK
// constraint. Inline-declared (same pattern as OpenAPIDocSource
// above) so the pgstore and the apid handler agree on the enum
// without a string-literal drift.
const (
	OpenAPIImportSourceManualImport = "manual_import"
)

// OpenAPIImportMaxDocBytes is the hard cap on the imported body,
// applied at the apid layer (the SQL CHECK in migration 00416
// applies the same constant for defense-in-depth). The
// per-plan cap is layered on top via
// api.Plan.OpenAPIImportMaxDocBytes() (limits.go). The constant
// lives here (not in pkg/api/limits.go) because the validator at
// pkg/openapiimport/validator.go and the cache at
// pkg/openapidiff/spec_cache.go both reference it through the
// Store interface and need a stable address.
//
// 256 KiB is generous for a single-app surface (Stripe-scale
// 700-operation docs split per-app land well under this cap).
// The cap is the abuse-surface ceiling, not the plan-tier ceiling.
const OpenAPIImportMaxDocBytes = 256 * 1024

// OpenAPIImportMaxEndpoints is the hard cap on the imported
// doc's paths.* operation count, applied at the apid layer (the
// SQL CHECK in migration 00416 applies the same constant for
// defense-in-depth). 50 operations is generous for a single-app
// surface.
const OpenAPIImportMaxEndpoints = 50

// ValidOpenAPIVersions is the closed enum the SQL CHECK admits.
// Mirrors migrations/00416_openapi_import.sql. The validator at
// pkg/openapiimport/validator.go compiles the imported doc
// against the OpenAPI 3.1 meta-schema regardless of the declared
// version — 3.0.x docs that don't use 3.0-only features pass;
// customers needing strict 3.0 can ship 3.1.
var ValidOpenAPIVersions = []string{
	"3.0.0", "3.0.1", "3.0.2", "3.0.3", "3.0.4",
	"3.1.0", "3.1.1",
}

// DeploymentScopeExclusion is one persisted --exclude row (ADR-124
// follow-up #3). The apply path folds every active row into the
// per-deploy exclude list when req.Exclude is empty so an operator
// who ran `--exclude foo --persist-exclude` once does not need to
// keep typing it on every subsequent deploy. The schema (migration
// 00418) has NO FK to apps(id) by design (see the SOFT-DELETE
// CASCADE BLIND SPOT comment in 00509_deployment_scope_exclusions.sql
// header) — app_id is a snapshot reference that may go stale; the
// janitor PurgeOrphanedScopeExclusions reaps stale rows after the
// 90-day retention window.
type DeploymentScopeExclusion struct {
	ID        string
	AccountID string
	ProjectID string
	AppID     string
	Slug      string
	Reason    string
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}
