package main

import (
	"context"
	"sync"
	"time"
)

const mcpTaskLeaseDuration = 30 * time.Second

// Renew from the start of the database request, so response latency cannot
// extend the locally proven lease. The timer also bounds blocked heartbeats.
type mcpTaskLeaseDeadline struct {
	mu       sync.Mutex
	ctx      context.Context
	timer    *time.Timer
	deadline time.Time
	closed   bool
	onExpiry func()
}

func newMCPTaskLeaseDeadline(ctx context.Context, deadline time.Time, onExpiry func()) *mcpTaskLeaseDeadline {
	lease := &mcpTaskLeaseDeadline{ctx: ctx, onExpiry: onExpiry}
	if deadline.IsZero() {
		deadline = time.Now().Add(mcpTaskLeaseDuration)
	}
	lease.renew(deadline.Add(-mcpTaskLeaseDuration))
	return lease
}

func (lease *mcpTaskLeaseDeadline) renew(requestStarted time.Time) {
	lease.mu.Lock()
	if lease.closed || lease.ctx.Err() != nil {
		lease.mu.Unlock()
		return
	}
	if !lease.deadline.IsZero() && !time.Now().Before(lease.deadline) {
		lease.closed = true
		lease.timer.Stop()
		lease.mu.Unlock()
		lease.onExpiry()
		return
	}
	lease.deadline = requestStarted.Add(mcpTaskLeaseDuration)
	remaining := max(0, time.Until(lease.deadline))
	if lease.timer == nil {
		lease.timer = time.AfterFunc(remaining, lease.expire)
	} else {
		lease.timer.Reset(remaining)
	}
	lease.mu.Unlock()
}

func (lease *mcpTaskLeaseDeadline) expire() {
	lease.mu.Lock()
	if lease.closed || lease.ctx.Err() != nil {
		lease.mu.Unlock()
		return
	}
	if remaining := time.Until(lease.deadline); remaining > 0 {
		lease.timer.Reset(remaining)
		lease.mu.Unlock()
		return
	}
	lease.closed = true
	lease.mu.Unlock()
	lease.onExpiry()
}

func (lease *mcpTaskLeaseDeadline) close() {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	lease.closed = true
	if lease.timer != nil {
		lease.timer.Stop()
	}
}
