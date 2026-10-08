package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AppForkStatus is the fork lifecycle from ADR-732. apid creates queued rows;
// schedd owns every later transition except cancelling a queued fork.
type AppForkStatus string

const (
	AppForkQueued    AppForkStatus = "queued"
	AppForkRestoring AppForkStatus = "restoring"
	AppForkRunning   AppForkStatus = "running"
	AppForkExpired   AppForkStatus = "expired"
	AppForkCancelled AppForkStatus = "cancelled"
	AppForkFailed    AppForkStatus = "failed"
)

// Terminal reports whether no further transition is allowed.
func (s AppForkStatus) Terminal() bool {
	return s == AppForkExpired || s == AppForkCancelled || s == AppForkFailed
}

// Active reports whether the fork holds, or may soon hold, an instance.
func (s AppForkStatus) Active() bool {
	return s == AppForkQueued || s == AppForkRestoring || s == AppForkRunning
}

// AppFork is one production fork request: a quarantined, non-serving restore
// of the app's newest capture of DeploymentID that lives until ExpiresAt.
// SnapshotID and InstanceID are set by schedd once the restore starts.
type AppFork struct {
	ID              string
	AccountID       string
	AppID           string
	DeploymentID    string
	RequestedBy     string
	Status          AppForkStatus
	TTLSeconds      int
	ExpiresAt       time.Time
	SnapshotID      *string
	InstanceID      *string
	LeaseToken      *string
	LeaseOwner      *string
	LeaseExpiresAt  *time.Time
	CancelRequested *time.Time
	FailureCode     *string
	FailureMessage  *string
	StartedAt       *time.Time
	FinishedAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	// AccessTokenHash is the SHA-256 of the fork's access token; the
	// gateway compares it in constant time. Nil means unreachable.
	AccessTokenHash []byte
}

// Database backstops for the fork TTL (app_forks_ttl_chk). Plan limits in
// pkg/api/limits.go are always inside these bounds.
const (
	AppForkMinTTLSeconds = 60
	AppForkMaxTTLSeconds = 86400
)

// CreateAppForkParams is already-authorized fork intent. The limits are the
// caller's plan limits; the store enforces them atomically with the insert.
type CreateAppForkParams struct {
	AccountID     string
	AppID         string
	DeploymentID  string
	RequestedBy   string
	TTLSeconds    int
	MaxPerApp     int
	MaxPerAccount int
	CreatedAt     time.Time
	// AccessTokenHash is the SHA-256 of the access token apid minted.
	AccessTokenHash []byte
}

var (
	// ErrAppForkInvalid rejects malformed fork intent before it reaches SQL.
	ErrAppForkInvalid = errors.New("state: invalid app fork")
	// ErrAppForkDeploymentUnavailable means the app is gone or the named
	// deployment is not its live deployment.
	ErrAppForkDeploymentUnavailable = errors.New("state: app fork deployment unavailable")
)

// AppForkLimitError reports which active-fork limit refused a create.
// Scope is "app" or "account".
type AppForkLimitError struct {
	Scope    string
	Limit    int
	Observed int
}

func (e *AppForkLimitError) Error() string {
	return fmt.Sprintf("state: active app fork limit reached for %s (%d of %d)", e.Scope, e.Observed, e.Limit)
}

// AppForkStore is the apid-facing fork intent surface (ADR-732). The
// scheduler claim, lease and completion methods land with the coordinator.
type AppForkStore interface {
	CreateAppFork(ctx context.Context, params CreateAppForkParams) (AppFork, error)
	AppForkByID(ctx context.Context, accountID, appID, forkID string) (AppFork, error)
	// AppForkForApp is the gateway's lookup: scoped by the app the request
	// host resolved to.
	AppForkForApp(ctx context.Context, appID, forkID string) (AppFork, error)
	ListAppForks(ctx context.Context, accountID, appID string, limit int) ([]AppFork, error)
	// RequestAppForkCancellation cancels a queued fork, records the request
	// on a restoring or running one, and returns a terminal fork unchanged.
	RequestAppForkCancellation(ctx context.Context, accountID, appID, forkID string, requestedAt time.Time) (AppFork, error)
}

func validateCreateAppFork(p CreateAppForkParams) (CreateAppForkParams, error) {
	p.RequestedBy = strings.TrimSpace(p.RequestedBy)
	switch {
	case p.AccountID == "" || p.AppID == "" || p.DeploymentID == "":
		return p, fmt.Errorf("%w: account, app and deployment are required", ErrAppForkInvalid)
	case p.RequestedBy == "" || len(p.RequestedBy) > 256:
		return p, fmt.Errorf("%w: requested_by must be 1..256 bytes", ErrAppForkInvalid)
	case p.TTLSeconds < AppForkMinTTLSeconds || p.TTLSeconds > AppForkMaxTTLSeconds:
		return p, fmt.Errorf("%w: ttl_seconds %d outside %d..%d", ErrAppForkInvalid, p.TTLSeconds, AppForkMinTTLSeconds, AppForkMaxTTLSeconds)
	case p.MaxPerApp < 1 || p.MaxPerAccount < 1:
		return p, fmt.Errorf("%w: active fork limits must be positive", ErrAppForkInvalid)
	case p.CreatedAt.IsZero():
		return p, fmt.Errorf("%w: created_at is required", ErrAppForkInvalid)
	case p.AccessTokenHash != nil && len(p.AccessTokenHash) != 32:
		return p, fmt.Errorf("%w: access token hash must be 32 bytes", ErrAppForkInvalid)
	}
	// Postgres stores microseconds; truncate so both stores agree on
	// created_at and the expires_at CHECK holds exactly.
	p.CreatedAt = p.CreatedAt.UTC().Truncate(time.Microsecond)
	return p, nil
}

// appForkLimitExceeded applies the per-app limit before the per-account one
// so the error names the narrower scope the caller can act on.
func appForkLimitExceeded(p CreateAppForkParams, appActive, accountActive int) error {
	if appActive >= p.MaxPerApp {
		return &AppForkLimitError{Scope: "app", Limit: p.MaxPerApp, Observed: appActive}
	}
	if accountActive >= p.MaxPerAccount {
		return &AppForkLimitError{Scope: "account", Limit: p.MaxPerAccount, Observed: accountActive}
	}
	return nil
}

func clampAppForkListLimit(limit int) int {
	if limit <= 0 || limit > 100 {
		return 100
	}
	return limit
}
