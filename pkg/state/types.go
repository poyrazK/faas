package state

import (
	"encoding/json"
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
	ID               string
	Email            string
	Plan             api.Plan
	Status           AccountStatus
	StripeCustomerID string
	CreatedAt        time.Time
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
	Status         AppStatus
	Manifest       AppManifest
	CreatedAt      time.Time
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
	RootfsPath  string
	RootfsBytes int64
	Status      DeploymentStatus
	Error       string
	CreatedAt   time.Time
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
// status fields are user-mutable; type and runtime are immutable).
type UpdateAppParams struct {
	RAMMB          *int
	IdleTimeoutS   *int // explicit 0 clears to plan default
	SetIdleTimeout bool // distinguishes nil from zero
	MaxConcurrency *int
	Status         *AppStatus
	Manifest       *AppManifest
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
	Path         string
	Stale        bool
	CreatedAt    time.Time
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
