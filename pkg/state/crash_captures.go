package state

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"
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
	// CrashTriggerSDK: the app asked from inside its error handler, through
	// the guest metadata endpoint; vmmd writes the request.
	CrashTriggerSDK = "sdk"
	// CrashTriggerLiveFork: an ADR-732 live fork captured the instance it
	// restores. Kept only as long as the longest fork can live.
	CrashTriggerLiveFork = "live_fork"
)

// CrashCaptureReasonMaxBytes bounds an SDK capture's reason label.
const CrashCaptureReasonMaxBytes = 256

// CrashCapture is one capture of a running instance (ADR-733). It is never a
// snapshots row: only an ADR-732 fork can restore it.
type CrashCapture struct {
	ID           string
	AccountID    string
	AppID        string
	DeploymentID string
	InstanceID   string
	Trigger      string
	StatusCode   *int
	Route        string
	// Reason is the app's label for an SDK capture; empty otherwise.
	Reason            string
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
	// PlaintextState, SealedKey and EncryptedAt are imaged's encryption
	// at rest (ADR-733). SealedKey is the capture's age identity sealed to
	// the fleet recipient; it is dropped at expiry.
	PlaintextState CrashCapturePlaintext
	SealedKey      []byte
	EncryptedAt    *time.Time
}

// CrashCapturePlaintext says whether a capture's plaintext objects exist.
type CrashCapturePlaintext string

const (
	// CrashPlaintextPresent: captured, not yet encrypted.
	CrashPlaintextPresent CrashCapturePlaintext = "present"
	// CrashPlaintextPurging: encrypted; plaintext delete pending or retried.
	CrashPlaintextPurging CrashCapturePlaintext = "purging"
	// CrashPlaintextAbsent: encrypted; no plaintext on storage.
	CrashPlaintextAbsent CrashCapturePlaintext = "absent"
	// CrashPlaintextStaging: encrypted; plaintext being restored for a fork.
	CrashPlaintextStaging CrashCapturePlaintext = "staging"
	// CrashPlaintextStaged: encrypted; plaintext restored for an active fork.
	CrashPlaintextStaged CrashCapturePlaintext = "staged"
)

// PlaintextReadable reports whether a fork can restore the capture now.
func (c CrashCapture) PlaintextReadable() bool {
	return c.PlaintextState == CrashPlaintextPresent || c.PlaintextState == CrashPlaintextStaged
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
// requests, gatewayd-internal writes 5xx requests, vmmd writes SDK
// requests, schedd owns the capture
// lifecycle, and imaged owns the files: encryption, staging and expiry.
type CrashCaptureStore interface {
	SetCrashSnapshotSettings(ctx context.Context, accountID, appID string, enabled bool, now time.Time) (CrashSnapshotSettings, error)
	CrashSnapshotSettingsFor(ctx context.Context, accountID, appID string) (CrashSnapshotSettings, error)
	RequestHTTPCrashCapture(ctx context.Context, appID, instanceID string, statusCode int, route string, cooldown time.Duration, now time.Time) (CrashCapture, error)
	RequestManualCrashCapture(ctx context.Context, accountID, appID string, cooldown time.Duration, now time.Time) (CrashCapture, error)
	// RequestSDKCrashCapture is vmmd's write for the SDK trigger. The
	// instance comes from the vsock listener, never from the guest.
	RequestSDKCrashCapture(ctx context.Context, appID, instanceID, route, reason string, cooldown time.Duration, now time.Time) (CrashCapture, error)
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
	CrashCaptureEncryptionStore
}

// CrashCaptureEncryptionStore is imaged's encryption-at-rest surface
// (ADR-733). A fork is active while queued, restoring or running; plaintext
// is kept or restored only for an active fork. Every Begin/Finish/Mark is a
// compare-and-swap that returns ErrNotFound when the row moved on.
type CrashCaptureEncryptionStore interface {
	// LiveCrashCaptureDeploymentIDs lists deployments with a capture being
	// written or kept, whose capture files the orphan sweep must keep.
	LiveCrashCaptureDeploymentIDs(ctx context.Context) ([]string, error)
	CrashCapturesToEncrypt(ctx context.Context, now time.Time, limit int) ([]CrashCapture, error)
	// MarkCrashCaptureEncrypted stores the sealed key; the row becomes
	// staged when an active fork is pinned to it, otherwise purging.
	MarkCrashCaptureEncrypted(ctx context.Context, id string, sealedKey []byte, now time.Time) (CrashCapture, error)
	CrashCapturesToPurge(ctx context.Context, limit int) ([]CrashCapture, error)
	// BeginCrashCapturePurge refuses while an active fork is pinned.
	BeginCrashCapturePurge(ctx context.Context, id string, now time.Time) (CrashCapture, error)
	FinishCrashCapturePurge(ctx context.Context, id string, now time.Time) (CrashCapture, error)
	CrashCapturesToStage(ctx context.Context, now time.Time, limit int) ([]CrashCapture, error)
	// BeginCrashCaptureStage requires an active fork.
	BeginCrashCaptureStage(ctx context.Context, id string, now time.Time) (CrashCapture, error)
	FinishCrashCaptureStage(ctx context.Context, id string, now time.Time) (CrashCapture, error)
}

// Snapshot projects a ready capture onto the restore inputs a fork uses. It
// is never written to the snapshots table.
func (c CrashCapture) Snapshot() (Snapshot, bool) {
	if c.Status != CrashCaptureReady || c.StorageKey == nil || c.FCVersion == nil {
		return Snapshot{}, false
	}
	return Snapshot{ID: c.ID, DeploymentID: c.DeploymentID, FCVersion: *c.FCVersion, StorageKey: *c.StorageKey}, true
}

// truncateUTF8 cuts s to at most n bytes on a rune boundary. The columns'
// CHECKs bound bytes, while Postgres left() counts characters, so a long
// non-ASCII route or reason must be cut here first.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
