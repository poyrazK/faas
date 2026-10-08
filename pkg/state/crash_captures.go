package state

import (
	"context"
	"errors"
	"time"
)

// CrashCaptureStatus is the ADR-733 lifecycle: requested → capturing →
// ready → expired, or failed from requested/capturing.
type CrashCaptureStatus string

const (
	CrashCaptureRequested CrashCaptureStatus = "requested"
	CrashCaptureCapturing CrashCaptureStatus = "capturing"
	CrashCaptureReady     CrashCaptureStatus = "ready"
	CrashCaptureFailed    CrashCaptureStatus = "failed"
	CrashCaptureExpired   CrashCaptureStatus = "expired"
)

// Crash capture triggers.
const (
	CrashTriggerHTTP5xx = "http_5xx"
	CrashTriggerManual  = "manual"
)

// CrashCapture is one capture of a running instance (ADR-733). It is never a
// snapshots row: only an ADR-732 fork can restore it.
type CrashCapture struct {
	ID                string
	AccountID         string
	AppID             string
	DeploymentID      string
	InstanceID        string
	Trigger           string
	StatusCode        *int
	Route             string
	Status            CrashCaptureStatus
	StorageKey        *string
	VMStateStorageKey *string
	FCVersion         *string
	MemBytes          *int64
	FailureCode       *string
	FailureMessage    *string
	RequestedAt       time.Time
	CapturedAt        *time.Time
	FinishedAt        *time.Time
	ExpiresAt         *time.Time
	UpdatedAt         time.Time
}

// CrashSnapshotSettings is an app's opt-in.
type CrashSnapshotSettings struct {
	AppID     string
	AccountID string
	Enabled   bool
	UpdatedAt time.Time
}

// CompleteCrashCaptureParams records a finished capture.
type CompleteCrashCaptureParams struct {
	ID                string
	StorageKey        string
	VMStateStorageKey string
	FCVersion         string
	MemBytes          int64
	CapturedAt        time.Time
	ExpiresAt         time.Time
}

// ErrCrashCaptureRefused means a capture request was not admitted: the app
// did not opt in (5xx trigger), has no running instance, already has a
// capture in flight, or captured within the cooldown.
var ErrCrashCaptureRefused = errors.New("state: crash capture refused")

// CrashCaptureStore is the ADR-733 surface. apid writes settings and manual
// requests, gatewayd-internal writes 5xx requests, schedd owns the rest.
type CrashCaptureStore interface {
	SetCrashSnapshotSettings(ctx context.Context, accountID, appID string, enabled bool, now time.Time) (CrashSnapshotSettings, error)
	CrashSnapshotSettingsFor(ctx context.Context, accountID, appID string) (CrashSnapshotSettings, error)
	RequestHTTPCrashCapture(ctx context.Context, appID, instanceID string, statusCode int, route string, cooldown time.Duration, now time.Time) (CrashCapture, error)
	RequestManualCrashCapture(ctx context.Context, accountID, appID string, cooldown time.Duration, now time.Time) (CrashCapture, error)
	ClaimNextCrashCapture(ctx context.Context, now time.Time) (CrashCapture, error)
	CompleteCrashCapture(ctx context.Context, params CompleteCrashCaptureParams) (CrashCapture, error)
	FailCrashCapture(ctx context.Context, id, code, message string, now time.Time) (CrashCapture, error)
	FailStaleCrashCaptures(ctx context.Context, cutoff, now time.Time) ([]CrashCapture, error)
	ExpiredCrashCaptures(ctx context.Context, now time.Time, limit int) ([]CrashCapture, error)
	ExpireCrashCapture(ctx context.Context, id string, now time.Time) (CrashCapture, error)
	ListCrashCaptures(ctx context.Context, accountID, appID string, limit int) ([]CrashCapture, error)
	CrashCaptureByID(ctx context.Context, accountID, appID, id string) (CrashCapture, error)
	// CrashCaptureForRestore is schedd's read when it restores a fork pinned
	// to a capture.
	CrashCaptureForRestore(ctx context.Context, id string) (CrashCapture, error)
}

// Snapshot projects a ready capture onto the restore inputs a fork uses. It
// is never written to the snapshots table.
func (c CrashCapture) Snapshot() (Snapshot, bool) {
	if c.Status != CrashCaptureReady || c.StorageKey == nil || c.FCVersion == nil {
		return Snapshot{}, false
	}
	return Snapshot{ID: c.ID, DeploymentID: c.DeploymentID, FCVersion: *c.FCVersion, StorageKey: *c.StorageKey}, true
}
