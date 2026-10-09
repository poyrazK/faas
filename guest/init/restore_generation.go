package main

import (
	"sync"
	"time"
)

// restoreGeneration counts completed post-restore hooks in this guest. An
// in-place snapshot (warm or crash capture) resumes the same VM without the
// hook, so the count changes only in a copy restored from a snapshot. A
// caller blocked across a capture uses it to tell whether it woke up in a
// fork of that capture.
var restoreGeneration = newRestoreCounter()

type restoreCounter struct {
	mu      sync.Mutex
	gen     uint64
	changed chan struct{}
}

func newRestoreCounter() *restoreCounter {
	return &restoreCounter{changed: make(chan struct{})}
}

// current returns the generation now.
func (c *restoreCounter) current() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

// bump records one completed restore and wakes every waiter.
func (c *restoreCounter) bump() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	close(c.changed)
	c.changed = make(chan struct{})
}

// changedSince reports whether the generation moved past gen, waiting up to
// wait for it to: after a restore the guest's vsock streams are reset before
// the host's resume hook has run, so the caller sees the reset first.
func (c *restoreCounter) changedSince(gen uint64, wait time.Duration) bool {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		c.mu.Lock()
		now, changed := c.gen, c.changed
		c.mu.Unlock()
		if now != gen {
			return true
		}
		select {
		case <-changed:
		case <-deadline.C:
			return false
		}
	}
}
