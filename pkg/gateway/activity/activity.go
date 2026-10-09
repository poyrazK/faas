// Package activity records process-local forwarding activity (ADR-696) and
// optional private forwarding admission fences (ADR-697). It provides no
// scheduler or VM authority to retire an instance or deployment.
package activity

import (
	"fmt"
	"math"
	"sync"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type key struct{ app, deployment string }

// Unknown reasons are bounded diagnostics, never target IDs or request data.
const (
	TrackingDisabled        = "tracking_disabled"
	TrackingUninitialized   = "tracking_uninitialized"
	MissingIdentity         = "missing_identity"
	KeyCapacityExceeded     = "key_capacity_exceeded"
	ForwardCapacityExceeded = "forward_capacity_exceeded"
	VersionExhausted        = "activity_version_exhausted"
)

// Observation describes only forwards that enter this process's wrapped VM
// bridge. CoverageKnown is permanently false after missing identity or capacity
// exhaustion. A zero count is not a drain receipt: another request may enter
// immediately, and this observation says nothing about ingress completeness,
// raw TCP/UDP services, scheduler admission or guest-side connections.
type Observation struct {
	SessionID             string
	ActivityVersion       uint64
	CoverageKnown         bool
	CoverageUnknownReason string
	ActiveForwards        int
	TotalActiveForwards   int
}

// Tracker is safe for concurrent use. Construct it before exposing any
// forwarding factory and retain it for the entire process session.
type Tracker struct {
	mu            sync.Mutex
	session       string
	version       uint64
	known         bool
	unknownReason string
	active        map[key]int
	total         int
	fencing       bool
	fences        map[string]Fence
}

func canonicalIdentity(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}

// New binds observations to the same fresh process UUID as private routing
// confirmations. No identity, coverage or counts survive a process restart.
func New(sessionID string) (*Tracker, error) {
	if !canonicalIdentity(sessionID) {
		return nil, fmt.Errorf("activity: invalid gateway session identity")
	}
	return &Tracker{session: sessionID, version: 1, known: true, active: make(map[key]int)}, nil
}

// Begin runs at ServeHTTP entry, including when a target was selected earlier.
// The returned completion is idempotent and must run after the entire forward,
// including a hijacked connection, ends. Unknown coverage never changes traffic
// handling and cannot be repaired by later completions or eviction.
func (t *Tracker) Begin(appID, deploymentID string) func() {
	if t == nil {
		return func() {}
	}
	k := key{appID, deploymentID}
	t.mu.Lock()
	t.advance()
	reason := t.untrackable(k)
	tracked := reason == ""
	if tracked {
		t.active[k]++
		t.total++
	} else {
		t.markUnknown(reason)
	}
	t.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { t.finish(k, tracked) }) }
}

func (t *Tracker) untrackable(k key) string {
	switch {
	case t.active == nil:
		return TrackingUninitialized
	case !canonicalIdentity(k.app) || !canonicalIdentity(k.deployment):
		return MissingIdentity
	case t.total >= api.RuntimeUpgradeActivityForwardLimit:
		return ForwardCapacityExceeded
	case t.active[k] == 0 && len(t.active) >= api.RuntimeUpgradeActivityKeyLimit:
		return KeyCapacityExceeded
	default:
		return ""
	}
}

func (t *Tracker) markUnknown(reason string) {
	t.known = false
	if t.unknownReason == "" {
		t.unknownReason = reason
	}
}

func (t *Tracker) finish(k key, tracked bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.advance()
	if !tracked {
		return
	}
	t.total--
	t.active[k]--
	if t.active[k] == 0 {
		delete(t.active, k)
	}
}

// advance never wraps an observation version back to an earlier value.
func (t *Tracker) advance() {
	if t.version == math.MaxUint64 {
		t.markUnknown(VersionExhausted)
		return
	}
	t.version++
}

// Observe takes a consistent count/version snapshot. The version changes on
// every start and finish, even an untracked one, so zero -> busy -> zero never
// recreates an earlier observation within this session. Unrelated activity can
// conservatively invalidate an observation too. Invalid reads do not poison the
// process, but never return known coverage.
func (t *Tracker) Observe(appID, deploymentID string) Observation {
	if t == nil {
		return Observation{CoverageUnknownReason: TrackingDisabled}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	known, reason := t.known, t.unknownReason
	if t.active == nil {
		known, reason = false, TrackingUninitialized
	} else if known && (!canonicalIdentity(appID) || !canonicalIdentity(deploymentID)) {
		known, reason = false, MissingIdentity
	}
	return Observation{
		SessionID: t.session, ActivityVersion: t.version,
		CoverageKnown: known, CoverageUnknownReason: reason,
		ActiveForwards: t.active[key{appID, deploymentID}], TotalActiveForwards: t.total,
	}
}
