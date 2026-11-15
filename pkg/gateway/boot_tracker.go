package gateway

import (
	"sync"
	"time"
)

// bootTrackerMaxAge bounds how long an announced boot counts as in progress
// without a later state. It covers the longest startup window (Scale, 120 s)
// plus restore and readiness; a lost notification cannot hold a request
// longer than its own wake budget anyway.
const bootTrackerMaxAge = 3 * time.Minute

// bootTracker remembers the instances schedd announced as waking or cold
// booting, from instance_changed, until they reach any other state. A
// gateway answered at capacity while another gateway's (or producer's) boot
// holds the app's slot waits for that boot instead of answering 503 after a
// flat 2 s (prod 2026-11-15: a burst on a parked app got 503 "App instance
// starting" for ~11 requests on one gateway while the other gateway's
// requests got 200 one second later).
type bootTracker struct {
	mu    sync.Mutex
	boots map[string]map[string]time.Time // app -> instance -> announced
}

func (t *bootTracker) note(appID, instanceID string, now time.Time) {
	if appID == "" || instanceID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.boots == nil {
		t.boots = make(map[string]map[string]time.Time)
	}
	if t.boots[appID] == nil {
		t.boots[appID] = make(map[string]time.Time)
	}
	t.boots[appID][instanceID] = now
}

func (t *bootTracker) settle(appID, instanceID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if set := t.boots[appID]; set != nil {
		delete(set, instanceID)
		if len(set) == 0 {
			delete(t.boots, appID)
		}
	}
}

func (t *bootTracker) inProgress(appID string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	set := t.boots[appID]
	for id, at := range set {
		if now.Sub(at) > bootTrackerMaxAge {
			delete(set, id)
			continue
		}
		return true
	}
	if len(set) == 0 {
		delete(t.boots, appID)
	}
	return false
}

// NoteInstanceBooting records an instance schedd announced as waking or
// cold booting (instance_changed).
func (b *PGBackend) NoteInstanceBooting(appID, instanceID string) {
	b.boots.note(appID, instanceID, time.Now())
}

// NoteInstanceSettled forgets an announced boot once its instance reached
// any other state.
func (b *PGBackend) NoteInstanceSettled(appID, instanceID string) {
	b.boots.settle(appID, instanceID)
}

// BootInProgress reports whether an announced boot of appID has not reached
// another state yet.
func (b *PGBackend) BootInProgress(appID string) bool {
	return b.boots.inProgress(appID, time.Now())
}
