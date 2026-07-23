package state

import (
	"encoding/json"
	"net/netip"
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
	// StripeCustomerID is the per-account `cus_…` returned by Stripe when
	// the customer signs up (spec §4.7). The unique index makes it a
	// stable webhook lookup key.
	StripeCustomerID string
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
}

// Active reports whether the account may deploy (not suspended/deleted).
func (a Account) Active() bool { return a.Status == AccountActive || a.Status == AccountPastDue }

// APIKey is a hashed, account-scoped credential.
type APIKey struct {
	ID         string
	AccountID  string
	Hash       []byte
	Label      string
	LastUsedAt time.Time
	CreatedAt  time.Time
}

// App is a deployed application (or function). The Manifest carries the
// runner-scaffold payload (env, healthz path, entrypoint) the guest-init
// consumes inside the microVM (spec §4.6, §4.9).
type App struct {
	ID             string
	AccountID      string
	Slug           string
	Type           AppType
	Runtime        string // node22|python312 for functions
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
	Status          AppStatus
	Manifest        AppManifest
	CreatedAt       time.Time
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

// GitHubBinding is the (app → github_installation) edge persisted on
// the apps row by the /oauth/callback handler after it verifies the
// installation against api.github.com (ADR-012, review finding #1+#2
// closure). githubd reads this via the BindingsLookup interface so
// CheckRun writes go out under the right installation token instead
// of the hardcoded install_id=1 placeholder that M7.5 shipped with.
type GitHubBinding struct {
	AppID            string
	InstallID        int64
	RepoFullName     string
	ProductionBranch string
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
type GdprRequest struct {
	ID           string
	AccountID    string
	AccountEmail string
	Action       GdprAction
	RequestedAt  time.Time
	CompletedAt  time.Time // zero until the downstream action completes
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
type ComputeNode struct {
	ID                 string
	Name               string
	TargetURL          string // wire.ParseTarget-compatible
	VPCPUs             int
	MemMB              int
	MaxConcurrency     int
	AdmissionCeilingMB int
	Active             bool
	LastHeartbeatAt    time.Time
	CreatedAt          time.Time
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

// Usage is one row of monthly usage (spec §10). meterd is the writer in
// production; for tests we seed rows directly.
type Usage struct {
	AccountID string
	AppID     string
	Month     time.Time // truncated to month
	MBSeconds int64
	Requests  int64
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
	// (the default — see migration 00028).
	EgressAllowlist    *[]netip.Prefix
	SetEgressAllowlist bool
	Status             *AppStatus
	Manifest           *AppManifest
}

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
	FCVersion    string
	MemBytes     int64
	DiskBytes    int64
	// StorageKey mirrors Snapshot.StorageKey; populated from the
	// join so imaged's snapshot GC can Storage.Delete under the
	// canonical key (issue #96, ADR-025 axis 2 final slice).
	StorageKey string
	Stale      bool
	CreatedAt  time.Time
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
