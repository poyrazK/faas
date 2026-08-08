package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
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
)

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

// BuildStatus tracks the build row's lifecycle (spec §9).
type BuildStatus string

const (
	BuildQueued    BuildStatus = "queued"
	BuildRunning   BuildStatus = "running"
	BuildSucceeded BuildStatus = "succeeded"
	BuildFailed    BuildStatus = "failed"
)

// FailureClass tags the cause of a build failure (spec §9).
type FailureClass string

const (
	FailureOOM       FailureClass = "oom"
	FailureTimeout   FailureClass = "timeout"
	FailureUserError FailureClass = "user_error"
	FailureInfra     FailureClass = "infra"
)

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
	// MFARequired is the policy flag set by the three chokepoints:
	// plan upgrade, card attached, 2nd deploy. The customer clears it
	// only by completing /enroll + /confirm (MarkMFAEnrolled flips it
	// to false on the first successful confirm) or by /disable. API
	// keys ignore this column per the IAM-2 design decision (keys are
	// already cryptographically scoped).
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
// successful TOTP confirmation. Distinct from MFARequired: a
// customer who has enrolled is no longer blocked even if a future
// plan change again sets MFARequired=true. The LATCH is on
// MFAEnrolled, not MFARequired — the chokepoints set required=true,
// the customer clears it once via /confirm, and the chokepoints
// re-arm on the next trigger.
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

type APIKey struct {
	ID        string
	AccountID string
	// OrgID is the org the key was minted against (issue #190 / IAM-6,
	// PR 6). Migration 00127 flips api_keys.org_id from NULL to
	// NOT NULL after the deterministic personal-org backfill, so every
	// row carries a non-empty string. The PR 7 (schedd/meterd/gatewayd
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
	// through gatewayd (issue #471 / ADR-047). When true, gatewayd
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
	WebSocketEnabled bool
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
	// PublicAuthMode='basic'. Gatewayd-internal unseals it
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
	CreatedAt        time.Time
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

// AppManifest is the runner-scaffold payload. Stored as jsonb in Postgres;
// lives inside the snapshot for guest-init.
type AppManifest struct {
	Entrypoint []string          `json:"entrypoint,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	WorkingDir string            `json:"working_dir,omitempty"`
	Port       int               `json:"port,omitempty"`
	Healthz    string            `json:"healthz,omitempty"`
	User       string            `json:"user,omitempty"`
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
	if m.Entrypoint == nil && m.Env == nil && m.WorkingDir == "" && m.Port == 0 && m.Healthz == "" && m.User == "" {
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
	Error       string
	// ErrorCode is the RFC 7807 code stamped at the same time as
	// Error when a deployment transitions to `failed`. ADR-021:
	// oci.ErrImageNotFound / ErrImageEgressDenied /
	// ErrImageManifestInvalid map via pkg/api.SentinelToCode to
	// the stable codes that imaged writes here. Empty for every
	// other transition (and for deployments created before the
	// migrations/00021 column add).
	ErrorCode string
	CreatedAt time.Time
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

	// Parking reason + timestamp (issue #554 / ADR-079 follow-up).
	// pkg/sched.Engine.ParkDeployment sets these before flipping
	// apps.status to `evicted_cold`; the apid GET /v1/apps/{slug}
	// surface renders them as the `parked_deployment: { id,
	// parked_reason, parked_at }` reference. closed-set vocabulary
	// is enforced at the schema layer via the
	// deployments_parked_reason_check constraint (migration 00157).
	ParkedReason string     `json:"parked_reason,omitempty"`
	ParkedAt     *time.Time `json:"parked_at,omitempty"`
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
}

// CustomDomain is a customer's CNAME'd domain. apid owns this table;
// gatewayd reads it to decide whether to mint a cert (spec §4.1, §7).
type CustomDomain struct {
	Domain         string
	AppID          string
	ChallengeToken string
	VerifiedAt     time.Time // zero = unverified
}

// Verified reports whether the TXT challenge has been satisfied.
func (d CustomDomain) Verified() bool { return !d.VerifiedAt.IsZero() }

// Cron is a scheduled synthetic POST through gatewayd (spec §4.3).
type Cron struct {
	ID          string
	AppID       string
	Schedule    string // cron expression
	Path        string
	Enabled     bool
	CreatedAt   time.Time
	LastFiredAt time.Time // zero until first fire; updated by MarkCronFired
}

// AlertMetric is a closed vocabulary for the metric side of an AlertRule
// condition (issue #396, ADR-045). Names mirror the AppMetricsResponse
// payload verbatim so the evaluator and the customer-facing metrics
// endpoint cannot drift. failed_invocations is the only non-Prometheus
// metric; its source dimension comes through AlertRule.FailureSource.
type AlertMetric string

const (
	AlertMetricErrorRate    AlertMetric = "error_rate_pct"
	AlertMetricLatencyP50   AlertMetric = "latency_p50_ms"
	AlertMetricLatencyP95   AlertMetric = "latency_p95_ms"
	AlertMetricLatencyP99   AlertMetric = "latency_p99_ms"
	AlertMetricColdStartPct AlertMetric = "cold_start_pct"
	AlertMetricRequestCount AlertMetric = "request_count"
	AlertMetricFailedInvocs AlertMetric = "failed_invocations"
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
	Name                *string
	Enabled             *bool
	Metric              *AlertMetric
	Comparison          *AlertComparison
	Threshold           *float64
	WindowSpec          *AlertWindowSpec
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
type ComputeNode struct {
	ID                 string
	Name               string
	TargetURL          string // wire.ParseTarget-compatible — the vmmd dial target (Firecracker + jailer)
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
	// TargetURL which is the vmmd dial target. gatewayd reads
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
}

// InstanceTouch is one entry in a last_request_at flush batch (spec §4.1). The
// gateway accumulates these in memory and hands them to schedd every 15 s.
type InstanceTouch struct {
	InstanceID  string
	LastRequest time.Time
}

// Event is one row in the append-only audit log (spec §6.1).
type Event struct {
	ID      int64
	At      time.Time
	Actor   string
	Kind    string
	Subject *uuid.UUID
	Data    json.RawMessage
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
	Provider          string // "stripe" | "paddle"
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
// Nil pointers mean "leave unchanged" (only the slug/ram/idle/concurrency/
// min_instances/status fields are user-mutable; type and runtime are
// immutable).
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
	// seal happens at PATCH time so the gatewayd hot path
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
// if a fourth is ever added, mirror the constant here.
const (
	AppPublicAuthModeOpen   = "open"
	AppPublicAuthModeBearer = "bearer"
	AppPublicAuthModeBasic  = "basic"
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
// on (AccountID, AppID, Key) so cross-account access returns ErrNotFound
// (handlers render 400 CodeSecretNotFound by design — the URL resource IS
// the secret name).
type AppSecret struct {
	AccountID  string
	AppID      string
	Key        string
	Ciphertext []byte
	CreatedAt  time.Time
	UpdatedAt  time.Time
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
type AccountAppSecret struct {
	AccountID  string
	AppID      string
	AppSlug    string
	Key        string
	Ciphertext []byte
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
// on (AccountID, AppID, Key) so cross-account access returns ErrNotFound.
type AppEnv struct {
	AccountID string
	AppID     string
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
