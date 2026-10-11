package durable

import (
	"sync"
	"time"

	entitysocket "github.com/onebox-faas/faas/pkg/durableentity/socket"
)

// This process-local snapshot is bounded and contains no tenant identities,
// cursors or provider errors. It is advisory, never cleanup authority.
type registryCleanupHealth struct {
	mu           sync.Mutex
	running      bool
	started      time.Time
	index, roots registryCleanupLaneHealth
}
type registryCleanupLaneHealth struct {
	LastAttempt time.Time `json:"last_attempt_at"`
	LastSuccess time.Time `json:"last_success_at"`
	NextAttempt time.Time `json:"next_attempt_at"`
	Failures    int       `json:"consecutive_discovery_failures"`
	State       string    `json:"state"`
}

func (h *registryCleanupHealth) start(now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.running, h.started = true, now
	h.index, h.roots = registryCleanupLaneHealth{}, registryCleanupLaneHealth{}
}
func (h *registryCleanupHealth) stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.running = false
}
func (h *registryCleanupHealth) observe(index bool, now, next time.Time, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	lane := &h.roots
	if index {
		lane = &h.index
	}
	lane.LastAttempt, lane.NextAttempt = now, next
	if err != nil {
		lane.Failures = min(6, lane.Failures+1)
	} else {
		lane.LastSuccess, lane.Failures = now, 0
	}
}
func (h *registryCleanupHealth) snapshot(now time.Time, enabled bool) map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := "running"
	if !enabled {
		state = "disabled"
	} else if !h.running {
		state = "not_running"
	}
	lane := func(v registryCleanupLaneHealth) registryCleanupLaneHealth {
		v.State = state
		if state != "running" {
			return v
		}
		if v.LastAttempt.IsZero() {
			v.State = "starting"
			if now.After(h.started.Add(time.Minute)) {
				v.State = "stalled"
			}
		} else if now.After(v.NextAttempt.Add(time.Minute)) {
			v.State = "stalled"
		} else if v.Failures > 0 {
			v.State = "retrying"
		} else {
			v.State = "healthy"
		}
		return v
	}
	return map[string]any{"scope": "serving_node", "state": state, "started_at": h.started, "index": lane(h.index), "reconciliation": lane(h.roots)}
}

func registryCleanupProgress(now time.Time, c *entitysocket.RegistryCleanup, w *entitysocket.RegistryCleanupWorkerStatus, unavailable, enabled bool) map[string]any {
	state := "idle"
	var age *int64
	if c != nil && c.Phase == "complete" {
		state = "complete"
	} else if c != nil {
		seconds := int64(now.Sub(c.At).Seconds())
		if seconds < 0 {
			seconds = 0
		}
		age = &seconds
		switch {
		case now.Before(c.NotBefore):
			state = "reader_grace"
		case !enabled:
			state = "manual"
		case unavailable:
			state = "observation_unavailable"
		case w == nil:
			state = "awaiting_observation"
		case now.Before(w.LeaseUntil):
			state = "in_progress"
		case now.Before(w.NextAttemptAt):
			state = "retry_wait"
		default:
			state = "ready"
			// Anchor to the later of grace and observed progress. Lease/backoff
			// are checked first; overdue means inspection, not unsafe replay.
			anchor := c.NotBefore
			if w.LastSuccessAt.After(anchor) {
				anchor = w.LastSuccessAt
			}
			if w.NextAttemptAt.After(anchor) {
				anchor = w.NextAttemptAt
			}
			if now.After(anchor.Add(10 * time.Minute)) {
				state = "stalled"
			}
		}
	}
	return map[string]any{"state": state, "unfinished_age_seconds": age}
}
