package main

// Host receiver for guest-init's stateless-runtime advisory (ADR-047,
// amended 2026-11-12). Each guest-initiated stream on port 1025 carries one
// batch of writes to state-shaped paths; the receiver decodes it and hands it
// to Manager.ForwardStatelessAdvisory, which turns it into one
// stateless.advisory audit row in apid.
//
// Customer code runs as root inside the guest and can open this port itself,
// so the receiver trusts nothing in the frame: the app comes from the
// listener that accepted the stream, the frame is bounded and validated, and
// each instance gets a small per-minute batch budget so a guest cannot turn
// the port into an audit-table flood.

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

const (
	statelessAdvisoryReadTimeout    = 2 * time.Second
	statelessAdvisoryForwardTimeout = 5 * time.Second
	// statelessAdvisoryBatchesPerMinute is one instance's batch budget. The
	// guest reports each path once per instance lifetime, so an honest guest
	// stays far below it.
	statelessAdvisoryBatchesPerMinute = 6
	// statelessAdvisoryBudgetEntries bounds the per-instance budget map.
	statelessAdvisoryBudgetEntries = 4096
)

type statelessAdvisoryForward func(ctx context.Context, instance, appID string, batch []fcvm.AdvisoryEvent) error

type statelessAdvisoryReceiver struct {
	log      *slog.Logger
	forward  statelessAdvisoryForward
	identity instanceIdentity
	now      func() time.Time

	mu      sync.Mutex
	budgets map[string]statelessAdvisoryBudget
}

type statelessAdvisoryBudget struct {
	windowStart time.Time
	batches     int
}

func newStatelessAdvisoryReceiver(log *slog.Logger, forward statelessAdvisoryForward, identity instanceIdentity) *statelessAdvisoryReceiver {
	if log == nil {
		log = slog.Default()
	}
	return &statelessAdvisoryReceiver{
		log: log, forward: forward, identity: identity, now: time.Now,
		budgets: make(map[string]statelessAdvisoryBudget),
	}
}

func (r *statelessAdvisoryReceiver) handleGuestStream(instance string, conn net.Conn) (string, error) {
	appID := r.appID(instance, conn)
	if appID == "" {
		return "identity", fmt.Errorf("stateless advisory: instance %s has no app identity", instance)
	}
	if err := conn.SetReadDeadline(time.Now().Add(statelessAdvisoryReadTimeout)); err != nil {
		return "read", err
	}
	events, err := fcvm.DecodeStatelessAdvisoryFrame(conn)
	if err != nil {
		return "protocol", err
	}
	if len(events) == 0 {
		return "", nil
	}
	if !r.allow(instance) {
		r.log.Debug("stateless advisory: batch budget spent; dropping", "instance", instance, "events", len(events))
		return "", nil
	}
	if r.forward == nil {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), statelessAdvisoryForwardTimeout)
	defer cancel()
	if err := r.forward(ctx, instance, appID, events); err != nil {
		return "forward", err
	}
	return "", nil
}

// appID prefers the origin the listener fixed at wake; an adopted VM's
// listener may carry none, so the live instance record is the fallback.
func (r *statelessAdvisoryReceiver) appID(instance string, conn net.Conn) string {
	if origin, ok := fcvm.GuestVsockOrigin(conn); ok && origin.AppID != "" {
		return origin.AppID
	}
	if r.identity == nil {
		return ""
	}
	appID, _, err := r.identity(instance)
	if err != nil {
		return ""
	}
	return appID
}

// allow spends one batch of the instance's per-minute budget.
func (r *statelessAdvisoryReceiver) allow(instance string) bool {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	budget, ok := r.budgets[instance]
	if !ok || now.Sub(budget.windowStart) >= time.Minute {
		if !ok && len(r.budgets) >= statelessAdvisoryBudgetEntries {
			r.pruneLocked(now)
			if len(r.budgets) >= statelessAdvisoryBudgetEntries {
				return false
			}
		}
		budget = statelessAdvisoryBudget{windowStart: now}
	}
	if budget.batches >= statelessAdvisoryBatchesPerMinute {
		return false
	}
	budget.batches++
	r.budgets[instance] = budget
	return true
}

func (r *statelessAdvisoryReceiver) pruneLocked(now time.Time) {
	for instance, budget := range r.budgets {
		if now.Sub(budget.windowStart) >= time.Minute {
			delete(r.budgets, instance)
		}
	}
}
