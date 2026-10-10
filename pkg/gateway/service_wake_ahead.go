package gateway

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// ServiceWakeAheadPlan is the set of declared dependencies a waking caller
// may restore speculatively (ADR-950). Targets are already resolved through
// the service proxy's name resolver and authorized with the same caller
// policy a real call would meet, so a wake-ahead can never restore an app the
// caller could not reach. The counters explain bindings that were dropped.
type ServiceWakeAheadPlan struct {
	Targets    []App
	Denied     int
	Unresolved int
	Truncated  int
}

// ServiceWakeAheadPlanner loads the plan for one caller. It returns an empty
// plan when the caller has not opted in. It runs off the wake critical path,
// so it may read the store.
type ServiceWakeAheadPlanner func(ctx context.Context, callerAppID string) (ServiceWakeAheadPlan, error)

// ServiceWakeAheadOutcome is the closed label set for
// gateway_service_wake_ahead_total.
type ServiceWakeAheadOutcome string

const (
	ServiceWakeAheadRestored    ServiceWakeAheadOutcome = "restored"
	ServiceWakeAheadAlreadyWarm ServiceWakeAheadOutcome = "already_warm"
	ServiceWakeAheadAtCapacity  ServiceWakeAheadOutcome = "at_capacity"
	ServiceWakeAheadWakeFailed  ServiceWakeAheadOutcome = "wake_failed"
	ServiceWakeAheadDenied      ServiceWakeAheadOutcome = "denied"
	ServiceWakeAheadUnresolved  ServiceWakeAheadOutcome = "unresolved"
	ServiceWakeAheadPlanFailed  ServiceWakeAheadOutcome = "plan_failed"
	ServiceWakeAheadSaturated   ServiceWakeAheadOutcome = "saturated"
	ServiceWakeAheadTruncated   ServiceWakeAheadOutcome = "truncated"
)

// serviceWakeAhead launches detached dependency restores. slots bounds every
// planning and restore goroutine on this gateway process; when it is full the
// work is skipped rather than queued, so speculation never delays a real wake.
type serviceWakeAhead struct {
	plan    ServiceWakeAheadPlanner
	slots   chan struct{}
	timeout time.Duration
}

func (w *serviceWakeAhead) acquire() bool {
	select {
	case w.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (w *serviceWakeAhead) release() { <-w.slots }

// WithServiceWakeAhead arms ADR-950 depends_on wake-ahead. nil disables it,
// which preserves the ADR-196 behaviour of restoring each dependency only
// when it is called.
func (h *Handler) WithServiceWakeAhead(plan ServiceWakeAheadPlanner) *Handler {
	if plan == nil {
		h.wakeAhead = nil
		return h
	}
	h.wakeAhead = &serviceWakeAhead{
		plan:    plan,
		slots:   make(chan struct{}, api.ServiceWakeAheadInflightPerNodeMax),
		timeout: api.ServiceWakeAheadTimeout,
	}
	return h
}

// serviceWakeAheadTrigger reports whether a wake with this trigger is evidence
// that the app is about to serve traffic. Request-driven wakes qualify; so do
// wake-ahead restores, which is what carries a chain one hop further. Floors,
// crons, prewarm and rollouts do not: nothing is waiting on their callees.
func serviceWakeAheadTrigger(trigger string) bool {
	switch trigger {
	case sched.TriggerGateway, sched.TriggerServiceMesh, sched.TriggerServiceWakeAhead:
		return true
	default:
		return false
	}
}

// startServiceWakeAhead is called by the WakeGate leader just before it admits
// appID. It returns immediately: planning and every dependency restore run on
// detached goroutines so the caller's own restore is never delayed. Cycles
// terminate on their own because a dependency that is already waking joins
// that generation as a waiter and never becomes a leader again.
func (h *Handler) startServiceWakeAhead(appID, trigger string) {
	w := h.wakeAhead
	if w == nil || !serviceWakeAheadTrigger(trigger) {
		return
	}
	if !w.acquire() {
		h.metrics.AddServiceWakeAhead(ServiceWakeAheadSaturated, 1)
		return
	}
	go func() {
		defer w.release()
		planCtx, cancel := context.WithTimeout(context.Background(), w.timeout)
		plan, err := w.plan(planCtx, appID)
		cancel()
		if err != nil {
			h.metrics.AddServiceWakeAhead(ServiceWakeAheadPlanFailed, 1)
			h.logServiceWakeAhead("gateway: service wake-ahead plan failed", "app_id", appID, "err", err)
			return
		}
		h.metrics.AddServiceWakeAhead(ServiceWakeAheadDenied, plan.Denied)
		h.metrics.AddServiceWakeAhead(ServiceWakeAheadUnresolved, plan.Unresolved)
		h.metrics.AddServiceWakeAhead(ServiceWakeAheadTruncated, plan.Truncated)
		for _, target := range plan.Targets {
			if target.ID == "" || target.ID == appID {
				continue
			}
			if h.backend.HealthyCount(target.ID) > 0 {
				h.metrics.AddServiceWakeAhead(ServiceWakeAheadAlreadyWarm, 1)
				continue
			}
			if !w.acquire() {
				h.metrics.AddServiceWakeAhead(ServiceWakeAheadSaturated, 1)
				continue
			}
			go func(target App) {
				defer w.release()
				h.metrics.AddServiceWakeAhead(h.wakeAheadTarget(target, w.timeout), 1)
			}(target)
		}
	}()
}

// wakeAheadTarget restores one dependency through the same WakeGate a real
// call uses. A call that arrives meanwhile coalesces into this restore, and a
// restore already in flight absorbs this one, so wake-ahead never creates an
// instance a real call would not have created.
func (h *Handler) wakeAheadTarget(target App, timeout time.Duration) ServiceWakeAheadOutcome {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := h.ensureServiceCapacity(ctx, target, sched.TriggerServiceWakeAhead); err != nil {
		h.logServiceWakeAhead("gateway: service wake-ahead restore failed", "app_id", target.ID, "err", err)
		return ServiceWakeAheadWakeFailed
	}
	if h.backend.HealthyCount(target.ID) > 0 {
		return ServiceWakeAheadRestored
	}
	return ServiceWakeAheadAtCapacity
}

func (h *Handler) logServiceWakeAhead(msg string, args ...any) {
	if h.log != nil {
		h.log.Debug(msg, args...)
	}
}
