package ingress

import (
	"fmt"
	"math"
	"sync"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Generation names the two reviewed rosters at admission, never a future
// retirement permission. A scope retains its generation until forwarding ends.
type Generation struct{ PublicRevision, GatewayRevision string }

type Activity struct {
	Version                    int64
	Known                      bool
	Pending, Current, Previous int
}

type ActivityTracker struct {
	mu                sync.Mutex
	version           int64
	known             bool
	pending, total    int
	active            map[Generation]int
	withdrawal, fence string
}

func NewActivityTracker() *ActivityTracker {
	return &ActivityTracker{version: 1, known: true, active: make(map[Generation]int)}
}

type Admission struct {
	tracker         *ActivityTracker
	generation      Generation
	bound, finished bool // protected by tracker.mu
}

func validGeneration(g Generation) bool {
	for _, s := range []string{g.PublicRevision, g.GatewayRevision} {
		u, err := uuid.Parse(s)
		if err != nil || u == uuid.Nil || u.String() != s {
			return false
		}
	}
	return true
}

func (t *ActivityTracker) advance() {
	if t.version == math.MaxInt64 {
		t.known = false
		return
	}
	t.version++
}

// Begin precedes dialing/probing/database authorization. Unknown work is
// retained even if an older authorization returns after a roster transition.
func (t *ActivityTracker) Begin() (*Admission, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.withdrawal != "" {
		return nil, ErrUnverified
	}
	t.advance()
	if t.active == nil {
		t.known = false
		return nil, fmt.Errorf("ingress activity tracker uninitialized")
	}
	if t.total >= api.RuntimeUpgradeActivityForwardLimit {
		t.known = false
		return nil, fmt.Errorf("ingress activity capacity exhausted")
	}
	t.total++
	t.pending++
	return &Admission{tracker: t}, nil
}

func (a *Admission) Bind(g Generation) error {
	t := a.tracker
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.withdrawal != "" || a.finished || a.bound || !validGeneration(g) {
		return ErrUnverified
	}
	t.advance()
	if t.active[g] == 0 && len(t.active) >= api.RuntimeUpgradeActivityKeyLimit {
		t.known = false
		return fmt.Errorf("ingress generation capacity exhausted")
	}
	a.bound, a.generation = true, g
	t.pending--
	t.active[g]++
	return nil
}

func (a *Admission) Finish() {
	t := a.tracker
	t.mu.Lock()
	defer t.mu.Unlock()
	if a.finished {
		return
	}
	a.finished = true
	t.advance()
	t.total--
	if !a.bound {
		t.pending--
		return
	}
	t.active[a.generation]--
	if t.active[a.generation] == 0 {
		delete(t.active, a.generation)
	}
}

// Snapshot is called only after the database fences both current heads.
// Saturation permanently loses coverage for this process session; completing
// later work must never restore a false known zero.
func (t *ActivityTracker) Snapshot(g Generation) Activity {
	t.mu.Lock()
	defer t.mu.Unlock()
	current := t.active[g]
	return Activity{Version: t.version, Known: t.known && validGeneration(g), Pending: t.pending, Current: current, Previous: t.total - t.pending - current}
}

// AdmissionBegin supplies per-forward authorization and completion. Install it
// before exposing the guard. The completion must be idempotent.
type AdmissionBegin func() (Authorize, func(), error)

func (g *Guard) WithAdmissions(begin AdmissionBegin) (*Guard, error) {
	if begin == nil {
		return nil, fmt.Errorf("private ingress admission tracker required")
	}
	copy := *g
	copy.admissions = begin
	return &copy, nil
}

func (g *Guard) beginAdmission() (Authorize, func(), error) {
	if g.admissions != nil {
		return g.admissions()
	}
	return g.authorize, func() {}, nil
}
