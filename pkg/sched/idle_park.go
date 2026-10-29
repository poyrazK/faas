package sched

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// ErrParkActivity reports that Engine.ParkIdle found activity newer than the
// reaper's decision and left the instance RUNNING (ADR-20261024090000).
var ErrParkActivity = errors.New("sched: park skipped: instance has fresh activity")

// IdleParkCheck is what a reaper saw when it chose an instance to park.
type IdleParkCheck struct {
	// LastRequest and RequestCount are the instance's activity in the tick
	// snapshot (InstanceInfo.LastRequest and RequestCount).
	LastRequest  time.Time
	RequestCount int64
	// AllowNewRequests keeps scale-in (ReapAggressive) parking instances of
	// apps that still take traffic: only work in flight blocks those parks.
	AllowNewRequests bool
	// Guest re-reads vmmd's view of the guest. Nil, or ok=false, leaves only
	// the durable row to compare.
	Guest func(ctx context.Context) (activity GuestActivity, ok bool)
}

// GuestActivity is the guest's current activity as vmmd and conntrack see it.
type GuestActivity struct {
	Inflight    int64
	OpenConns   int64
	LastRequest time.Time
}

// activity names why the fresh row and guest activity no longer show the
// idle instance the reaper chose, or returns "".
func (c IdleParkCheck) activity(ctx context.Context, fresh state.Instance) string {
	if fresh.TailCount > 0 {
		return "wait_until_tasks"
	}
	last := fresh.LastRequestAt
	if c.Guest != nil {
		if guest, ok := c.Guest(ctx); ok {
			switch {
			case guest.Inflight > 0:
				return "request_in_flight"
			case guest.OpenConns > 0:
				return "open_connections"
			}
			if guest.LastRequest.After(last) {
				last = guest.LastRequest
			}
		}
	}
	if !c.AllowNewRequests && (last.After(c.LastRequest) || fresh.RequestCount > c.RequestCount) {
		return "new_request"
	}
	return ""
}
