package main

import (
	"net"
	"sync"
)

// replicaSessions are the client sessions a hot standby serves as the
// database's read replica (ADR-1027 amendment 2026-11-11). PostgreSQL
// refuses their writes while the volume is a standby, but a promotion
// makes it a primary under the same sessions, so the guest ends them
// before it promotes: a read-only endpoint never becomes a writable one
// under a client's feet.
type replicaSessions struct {
	mu sync.Mutex
	// ending refuses new sessions while a promotion ends the open ones.
	ending bool
	conns  map[net.Conn]struct{}
}

// add takes a session, or reports false while a promotion is ending them.
func (r *replicaSessions) add(c net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ending {
		return false
	}
	if r.conns == nil {
		r.conns = map[net.Conn]struct{}{}
	}
	r.conns[c] = struct{}{}
	return true
}

func (r *replicaSessions) remove(c net.Conn) {
	r.mu.Lock()
	delete(r.conns, c)
	r.mu.Unlock()
}

// end closes every session and refuses new ones until resume.
func (r *replicaSessions) end() {
	r.mu.Lock()
	r.ending = true
	conns := r.conns
	r.conns = nil
	r.mu.Unlock()
	for c := range conns {
		_ = c.Close()
	}
}

// resume takes sessions again after a promotion that failed: the volume
// is still a standby.
func (r *replicaSessions) resume() {
	r.mu.Lock()
	r.ending = false
	r.mu.Unlock()
}
