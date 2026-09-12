package builderd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

// DefaultWarmIdle is the amount of time a paused builder may remain reusable.
const DefaultWarmIdle = 5 * time.Minute

// WarmState describes the lifecycle of the guaranteed warm builder slot.
type WarmState string

const (
	WarmCold    WarmState = "cold"
	WarmRunning WarmState = "running"
	WarmPaused  WarmState = "warm_paused"
)

// WarmRestoreResult describes the outcome recorded when a build starts a
// guaranteed-slot builder. A hit means the VM transport accepted the
// snapshot restore; a transport failure that falls back to a cold boot is a
// miss.
type WarmRestoreResult string

const (
	WarmRestoreHit   WarmRestoreResult = "hit"
	WarmRestoreMiss  WarmRestoreResult = "miss"
	WarmRestoreStale WarmRestoreResult = "stale"
)

var (
	ErrWarmAlreadyRunning  = errors.New("builder warm slot is already running")
	ErrWarmNotRunning      = errors.New("builder warm slot is not running")
	ErrInvalidWarmSnapshot = errors.New("invalid builder warm snapshot")
)

// WarmSnapshot identifies the paused builder state and the Firecracker
// version it was created with. The paths are retained so the vmmd driver can
// evict the snapshot when it expires or becomes stale.
type WarmSnapshot struct {
	StorageKey        string
	VMStateStorageKey string
	VMStatePath       string
	// LayerPath is the retained per-builder drive1 image. Firecracker
	// snapshots do not contain block-device bytes; keeping this image is what
	// preserves the BuildKit state across a warm restore.
	LayerPath string
	// ScopeKey binds the retained BuildKit state to one app/runtime scope.
	// A mismatch forces a cold builder so dependency layers cannot cross apps.
	ScopeKey   string
	FCVersion  string
	CreatedAt  time.Time
	LastUsedAt time.Time
}

func builderWarmScopeKey(accountID, appID string, framework Framework, runtimeBaseRef string) string {
	if accountID == "" || appID == "" {
		return ""
	}
	parts := []string{accountID, appID, string(framework), runtimeBaseRef}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func (s WarmSnapshot) valid() bool {
	return s.StorageKey != "" &&
		(s.VMStateStorageKey != "" || s.VMStatePath != "") &&
		s.FCVersion != ""
}

// WarmLifecycle serializes transitions for the guaranteed warm builder slot.
// A cold start is reported as a miss; a paused snapshot can be restored only
// before its idle deadline and with the same Firecracker version.
type WarmLifecycle struct {
	mu       sync.Mutex
	state    WarmState
	idle     time.Duration
	snapshot *WarmSnapshot
}

// NewWarmLifecycle creates a lifecycle with the default idle window when idle
// is not configured.
func NewWarmLifecycle(idle time.Duration) *WarmLifecycle {
	if idle <= 0 {
		idle = DefaultWarmIdle
	}
	return &WarmLifecycle{state: WarmCold, idle: idle}
}

// State returns the current lifecycle state.
func (l *WarmLifecycle) State() WarmState {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state
}

// Snapshot returns a copy of the currently retained snapshot, if any.
func (l *WarmLifecycle) Snapshot() (WarmSnapshot, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.snapshot == nil {
		return WarmSnapshot{}, false
	}
	return *l.snapshot, true
}

// Start reserves the slot for a build. A cold start is a miss. A paused
// snapshot is consumed on a hit so a second build cannot restore it
// concurrently.
func (l *WarmLifecycle) Start(now time.Time, currentFCVersion string) (WarmRestoreResult, error) {
	result, _, err := l.StartWithSnapshot(now, currentFCVersion)
	return result, err
}

// StartWithSnapshot reserves the slot for a build and returns the consumed
// snapshot, when one existed. The caller must delete the returned snapshot
// from its backing store after a miss or stale result. On a hit, the caller
// uses it to restore the builder before completing the build.
func (l *WarmLifecycle) StartWithSnapshot(now time.Time, currentFCVersion string) (WarmRestoreResult, WarmSnapshot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.state == WarmRunning {
		return "", WarmSnapshot{}, ErrWarmAlreadyRunning
	}
	if l.state == WarmCold {
		l.state = WarmRunning
		return WarmRestoreMiss, WarmSnapshot{}, nil
	}

	snapshot := l.snapshot
	l.snapshot = nil
	l.state = WarmRunning
	if snapshot == nil || !snapshot.valid() {
		if snapshot == nil {
			return WarmRestoreMiss, WarmSnapshot{}, nil
		}
		return WarmRestoreMiss, *snapshot, nil
	}
	if currentFCVersion == "" || snapshot.FCVersion != currentFCVersion {
		return WarmRestoreStale, *snapshot, nil
	}
	if now.Sub(snapshot.LastUsedAt) >= l.idle {
		return WarmRestoreMiss, *snapshot, nil
	}
	return WarmRestoreHit, *snapshot, nil
}

// Complete publishes a paused snapshot after a successful build. Missing
// timestamps are filled from now so callers cannot accidentally create a
// snapshot that expires immediately.
func (l *WarmLifecycle) Complete(now time.Time, snapshot WarmSnapshot) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.state != WarmRunning {
		return ErrWarmNotRunning
	}
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = now
	}
	snapshot.LastUsedAt = now
	if !snapshot.valid() {
		l.state = WarmCold
		return ErrInvalidWarmSnapshot
	}
	l.snapshot = &snapshot
	l.state = WarmPaused
	return nil
}

// Expire evicts a paused snapshot once its idle window has elapsed.
func (l *WarmLifecycle) Expire(now time.Time) bool {
	_, expired := l.ExpireSnapshot(now)
	return expired
}

// ExpireSnapshot evicts an idle paused snapshot and returns its metadata so
// the caller can remove the corresponding memory and vmstate objects.
func (l *WarmLifecycle) ExpireSnapshot(now time.Time) (WarmSnapshot, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.state != WarmPaused || l.snapshot == nil {
		return WarmSnapshot{}, false
	}
	if now.Sub(l.snapshot.LastUsedAt) < l.idle {
		return WarmSnapshot{}, false
	}
	snapshot := *l.snapshot
	l.snapshot = nil
	l.state = WarmCold
	return snapshot, true
}

// Invalidate returns the slot to cold after a build failure, an operator
// shutdown, or any driver error that makes the paused snapshot unusable.
func (l *WarmLifecycle) Invalidate() {
	_, _ = l.InvalidateSnapshot()
}

// InvalidateSnapshot returns the retained snapshot while returning the slot
// to cold. The caller can use the metadata to delete backing-store objects.
func (l *WarmLifecycle) InvalidateSnapshot() (WarmSnapshot, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var snapshot WarmSnapshot
	if l.snapshot != nil {
		snapshot = *l.snapshot
	}
	retained := l.snapshot != nil
	l.snapshot = nil
	l.state = WarmCold
	return snapshot, retained
}
