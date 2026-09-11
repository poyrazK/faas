package builderd

import (
	"errors"
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

// WarmRestoreResult is emitted when a build asks to start a builder.
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
	FCVersion         string
	CreatedAt         time.Time
	LastUsedAt        time.Time
}

func (s WarmSnapshot) valid() bool {
	return s.StorageKey != "" &&
		s.VMStateStorageKey != "" &&
		s.VMStatePath != "" &&
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
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.state == WarmRunning {
		return "", ErrWarmAlreadyRunning
	}
	if l.state == WarmCold {
		l.state = WarmRunning
		return WarmRestoreMiss, nil
	}

	snapshot := l.snapshot
	l.snapshot = nil
	l.state = WarmRunning
	if snapshot == nil || !snapshot.valid() {
		return WarmRestoreMiss, nil
	}
	if currentFCVersion == "" || snapshot.FCVersion != currentFCVersion {
		return WarmRestoreStale, nil
	}
	if now.Sub(snapshot.LastUsedAt) >= l.idle {
		return WarmRestoreMiss, nil
	}
	return WarmRestoreHit, nil
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
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.state != WarmPaused || l.snapshot == nil {
		return false
	}
	if now.Sub(l.snapshot.LastUsedAt) < l.idle {
		return false
	}
	l.snapshot = nil
	l.state = WarmCold
	return true
}

// Invalidate returns the slot to cold after a build failure, an operator
// shutdown, or any driver error that makes the paused snapshot unusable.
func (l *WarmLifecycle) Invalidate() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.snapshot = nil
	l.state = WarmCold
}
