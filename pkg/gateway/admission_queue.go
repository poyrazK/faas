package gateway

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// WakeAdmissionPolicy is the gateway-side policy for one app's cold-wake
// admission. The waiter cap is per app. Priority orders app leaders waiting
// for a gateway-wide slot; sequence preserves FIFO order within a tier.
//
// These are deliberately plan-derived capacity/wait defaults. The policy is
// carried as a value so a later app-level override can be hydrated without
// changing WakeGate or the queue contract.
type WakeAdmissionPolicy struct {
	MaxWaiters int
	MaxWait    time.Duration
	Priority   int
}

// WakeAdmissionPolicyForPlan returns the bounded cold-wake policy for plan.
// Unknown plans fail closed with a single waiter and the shortest wait budget.
func WakeAdmissionPolicyForPlan(plan api.Plan) WakeAdmissionPolicy {
	switch plan {
	case api.PlanFree:
		return WakeAdmissionPolicy{MaxWaiters: api.GatewayWakeAdmissionFreeMaxWaiters, MaxWait: api.GatewayWakeAdmissionFreeMaxWait, Priority: api.GatewayWakeAdmissionFreePriority}
	case api.PlanHobby:
		return WakeAdmissionPolicy{MaxWaiters: api.GatewayWakeAdmissionHobbyMaxWaiters, MaxWait: api.GatewayWakeAdmissionPaidMaxWait, Priority: api.GatewayWakeAdmissionHobbyPriority}
	case api.PlanPro:
		return WakeAdmissionPolicy{MaxWaiters: api.GatewayWakeAdmissionProMaxWaiters, MaxWait: api.GatewayWakeAdmissionPaidMaxWait, Priority: api.GatewayWakeAdmissionProPriority}
	case api.PlanScale:
		return WakeAdmissionPolicy{MaxWaiters: api.GatewayWakeAdmissionScaleMaxWaiters, MaxWait: api.GatewayWakeAdmissionPaidMaxWait, Priority: api.GatewayWakeAdmissionScalePriority}
	default:
		return WakeAdmissionPolicy{MaxWaiters: 1, MaxWait: api.GatewayWakeAdmissionFreeMaxWait, Priority: api.GatewayWakeAdmissionFreePriority}
	}
}

// WakeAdmissionPolicyForApp applies the app-level overflow and wait
// controls on top of the plan defaults. The queue mode preserves the
// existing bounded waiter budget. Drop mode admits only the single wake
// leader; followers receive a typed 429 outcome at the handler boundary.
func WakeAdmissionPolicyForApp(plan api.Plan, overflow string, maxQueueWaitMS int) WakeAdmissionPolicy {
	p := WakeAdmissionPolicyForPlan(plan)
	if overflow == api.ConcurrencyOverflowDrop {
		p.MaxWaiters = 1
	}
	if maxQueueWaitMS > 0 {
		if maxQueueWaitMS > api.MaxConcurrencyQueueWaitMS {
			maxQueueWaitMS = api.MaxConcurrencyQueueWaitMS
		}
		p.MaxWait = time.Duration(maxQueueWaitMS) * time.Millisecond
	}
	return p
}

func (p WakeAdmissionPolicy) normalized(gateCap int, fallbackTTL time.Duration) WakeAdmissionPolicy {
	if gateCap < 1 {
		gateCap = 1
	}
	if p.MaxWaiters < 1 {
		p.MaxWaiters = gateCap
	}
	if p.MaxWaiters > gateCap {
		p.MaxWaiters = gateCap
	}
	if p.MaxWait <= 0 {
		p.MaxWait = fallbackTTL
	}
	if p.MaxWait <= 0 {
		p.MaxWait = time.Second
	}
	if p.Priority < 1 {
		p.Priority = 1
	}
	return p
}

// ErrWakeQueueWaitTimeout means the app's bounded queue wait budget elapsed.
// It is distinct from a caller cancellation so the handler can return a
// useful Retry-After value without treating client disconnects as overload.
var ErrWakeQueueWaitTimeout = errors.New("gateway: wake queue wait timeout")

// WakeQueueFullError is returned when an app's per-app waiter budget is full.
// It unwraps ErrQueueFull so existing callers retain their error identity.
type WakeQueueFullError struct {
	Depth      int
	Limit      int
	RetryAfter time.Duration
}

func (e *WakeQueueFullError) Error() string {
	if e == nil {
		return ErrQueueFull.Error()
	}
	return fmt.Sprintf("gateway: wake queue full (%d/%d)", e.Depth, e.Limit)
}

func (e *WakeQueueFullError) Unwrap() error { return ErrQueueFull }

// WakeQueueWaitTimeoutError is returned when a request stayed behind a wake
// longer than its app policy permits.
type WakeQueueWaitTimeoutError struct {
	RetryAfter time.Duration
}

func (e *WakeQueueWaitTimeoutError) Error() string { return ErrWakeQueueWaitTimeout.Error() }

func (e *WakeQueueWaitTimeoutError) Unwrap() error { return ErrWakeQueueWaitTimeout }

// WakeConcurrencyDropError marks an app explicitly configured with
// concurrency_overflow=drop. It unwraps ErrQueueFull so existing admission
// metrics still classify the event as an app queue saturation, while the
// HTTP boundary can distinguish it from the legacy 503 queue response.
type WakeConcurrencyDropError struct {
	RetryAfter time.Duration
}

func (e *WakeConcurrencyDropError) Error() string { return "gateway: concurrency overflow dropped" }

func (e *WakeConcurrencyDropError) Unwrap() error { return ErrQueueFull }

func isWakeConcurrencyDrop(err error) bool {
	var drop *WakeConcurrencyDropError
	return errors.As(err, &drop)
}

// ErrWakeAdmissionQueueFull means the gateway-wide cold-wake scheduler is
// saturated. It is intentionally separate from ErrQueueFull, which is the
// per-app single-flight waiter cap.
var ErrWakeAdmissionQueueFull = errors.New("gateway: wake admission queue full")

type WakeAdmissionQueueFullError struct {
	Depth      int
	Limit      int
	RetryAfter time.Duration
}

func (e *WakeAdmissionQueueFullError) Error() string {
	if e == nil {
		return ErrWakeAdmissionQueueFull.Error()
	}
	return fmt.Sprintf("gateway: wake admission queue full (%d/%d)", e.Depth, e.Limit)
}

func (e *WakeAdmissionQueueFullError) Unwrap() error { return ErrWakeAdmissionQueueFull }

// WakeAdmissionQueueWaitTimeoutError is the gateway-wide queue equivalent
// of WakeQueueWaitTimeoutError.
type WakeAdmissionQueueWaitTimeoutError struct {
	RetryAfter time.Duration
}

func (e *WakeAdmissionQueueWaitTimeoutError) Error() string {
	return "gateway: wake admission queue wait timeout"
}

func (e *WakeAdmissionQueueWaitTimeoutError) Unwrap() error {
	return ErrWakeQueueWaitTimeout
}

func wakeAdmissionOutcome(err error) string {
	switch {
	case err == nil:
		return "admitted"
	case errors.Is(err, ErrQueueFull):
		return "app_queue_full"
	case errors.Is(err, ErrWakeAdmissionQueueFull):
		return "gateway_queue_full"
	case errors.Is(err, ErrWakeQueueWaitTimeout):
		return "queue_timeout"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "canceled"
	default:
		return "error"
	}
}

type admissionTicket struct {
	appID    string
	plan     string
	priority int
	sequence uint64
	started  bool
	removed  bool
	start    chan struct{}
	index    int
}

type admissionTicketHeap []*admissionTicket

func (h admissionTicketHeap) Len() int { return len(h) }

func (h admissionTicketHeap) Less(i, j int) bool {
	if h[i].priority != h[j].priority {
		return h[i].priority > h[j].priority
	}
	return h[i].sequence < h[j].sequence
}

func (h admissionTicketHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *admissionTicketHeap) Push(x any) {
	t := x.(*admissionTicket)
	t.index = len(*h)
	*h = append(*h, t)
}

func (h *admissionTicketHeap) Pop() any {
	old := *h
	n := len(old)
	t := old[n-1]
	old[n-1] = nil
	t.index = -1
	*h = old[:n-1]
	return t
}

type admissionDepthUpdate struct {
	plan  string
	depth int
}

type admissionPreemption struct {
	fromApp  string
	fromPlan string
	toApp    string
	toPlan   string
}

type admissionPreemptSink func(context.Context, admissionPreemption)

// wakeAdmissionQueue bounds concurrent cold-wake admissions in one gateway
// process. Each queued item is a WakeGate leader, so one bursting app cannot
// consume one scheduler slot per incoming request. Priority orders distinct
// plans, while sequence gives FIFO ordering within a plan.
type wakeAdmissionQueue struct {
	mu            sync.Mutex
	capacity      int
	queueCap      int
	running       int
	nextSequence  uint64
	waiting       admissionTicketHeap
	queuedByPlan  map[string]int
	onDepthChange func(plan string, depth int)
	onPreempt     admissionPreemptSink
}

func newWakeAdmissionQueue(capacity, queueCap int, onDepthChange func(plan string, depth int)) *wakeAdmissionQueue {
	if capacity < 1 {
		capacity = 1
	}
	if queueCap < 1 {
		queueCap = 1
	}
	q := &wakeAdmissionQueue{
		capacity:      capacity,
		queueCap:      queueCap,
		queuedByPlan:  make(map[string]int),
		onDepthChange: onDepthChange,
	}
	heap.Init(&q.waiting)
	return q
}

// setPreemptSink installs the optional observer for priority reordering. It is
// called during handler wiring, before requests can enter the queue.
func (q *wakeAdmissionQueue) setPreemptSink(fn admissionPreemptSink) {
	if q != nil {
		previous := q.onPreempt
		q.onPreempt = func(ctx context.Context, event admissionPreemption) {
			if previous != nil {
				previous(ctx, event)
			}
			if fn != nil {
				fn(ctx, event)
			}
		}
	}
}

// Do executes fn under a bounded cross-app wake-admission slot. queued tells
// callers whether the request waited behind another app, and wait is the
// actual time spent waiting for that slot.
//
//nolint:contextcheck // the queue inherits the caller context; a nil context is normalized for test/adapter seams.
func (q *wakeAdmissionQueue) Do(ctx context.Context, appID, plan string, policy WakeAdmissionPolicy, fn func(context.Context) error) (bool, time.Duration, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if q == nil {
		return false, 0, fn(ctx)
	}
	policy = policy.normalized(q.capacity, api.GatewayWakeAdmissionPaidMaxWait)

	q.mu.Lock()
	if q.running < q.capacity && q.waiting.Len() == 0 {
		q.running++
		q.mu.Unlock()
		return false, 0, q.run(ctx, fn)
	}
	if q.waiting.Len() >= q.queueCap {
		depth := q.waiting.Len()
		q.mu.Unlock()
		return false, 0, &WakeAdmissionQueueFullError{Depth: depth, Limit: q.queueCap, RetryAfter: policy.MaxWait}
	}

	ticket := &admissionTicket{
		appID:    appID,
		plan:     plan,
		priority: policy.Priority,
		sequence: q.nextSequence,
		start:    make(chan struct{}),
	}
	q.nextSequence++
	var preemptions []admissionPreemption
	for _, waiting := range q.waiting {
		if waiting.removed || waiting.priority >= ticket.priority {
			continue
		}
		preemptions = append(preemptions, admissionPreemption{
			fromApp: waiting.appID, fromPlan: waiting.plan,
			toApp: ticket.appID, toPlan: ticket.plan,
		})
	}
	heap.Push(&q.waiting, ticket)
	q.queuedByPlan[plan]++
	updates := []admissionDepthUpdate{{plan: plan, depth: q.queuedByPlan[plan]}}
	q.mu.Unlock()
	q.notifyDepth(updates)
	q.notifyPreempt(ctx, preemptions)

	start := time.Now()
	timer := time.NewTimer(policy.MaxWait)
	defer timer.Stop()
	select {
	case <-ticket.start:
		return true, time.Since(start), q.run(ctx, fn)
	case <-ctx.Done():
		if q.cancel(ticket) {
			return true, time.Since(start), ctx.Err()
		}
		// The queue granted the slot concurrently with cancellation.
		// Consume the start signal so this goroutine owns and releases
		// the slot; do not run customer work after cancellation.
		<-ticket.start
		q.release()
		return true, time.Since(start), ctx.Err()
	case <-timer.C:
		timeout := &WakeAdmissionQueueWaitTimeoutError{RetryAfter: policy.MaxWait}
		if q.cancel(ticket) {
			return true, time.Since(start), timeout
		}
		<-ticket.start
		q.release()
		return true, time.Since(start), timeout
	}
}

func (q *wakeAdmissionQueue) run(ctx context.Context, fn func(context.Context) error) error {
	defer q.release()
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn(ctx)
}

func (q *wakeAdmissionQueue) cancel(ticket *admissionTicket) bool {
	q.mu.Lock()
	if ticket.started {
		q.mu.Unlock()
		return false
	}
	if ticket.removed {
		q.mu.Unlock()
		return true
	}
	ticket.removed = true
	heap.Remove(&q.waiting, ticket.index)
	q.queuedByPlan[ticket.plan]--
	if q.queuedByPlan[ticket.plan] <= 0 {
		delete(q.queuedByPlan, ticket.plan)
	}
	updates := q.dispatchLocked()
	updates = append(updates, admissionDepthUpdate{plan: ticket.plan, depth: q.queuedByPlan[ticket.plan]})
	q.mu.Unlock()
	// The callback is intentionally invoked after the mutex is released.
	q.notifyDepth(updates)
	return true
}

func (q *wakeAdmissionQueue) release() {
	q.mu.Lock()
	if q.running > 0 {
		q.running--
	}
	updates := q.dispatchLocked()
	q.mu.Unlock()
	q.notifyDepth(updates)
}

func (q *wakeAdmissionQueue) dispatchLocked() []admissionDepthUpdate {
	var updates []admissionDepthUpdate
	for q.running < q.capacity && q.waiting.Len() > 0 {
		ticket := heap.Pop(&q.waiting).(*admissionTicket)
		if ticket.removed {
			continue
		}
		ticket.started = true
		q.running++
		q.queuedByPlan[ticket.plan]--
		if q.queuedByPlan[ticket.plan] <= 0 {
			delete(q.queuedByPlan, ticket.plan)
		}
		updates = append(updates, admissionDepthUpdate{plan: ticket.plan, depth: q.queuedByPlan[ticket.plan]})
		close(ticket.start)
	}
	return updates
}

func (q *wakeAdmissionQueue) notifyDepth(updates []admissionDepthUpdate) {
	if q == nil || q.onDepthChange == nil {
		return
	}
	for _, update := range updates {
		q.onDepthChange(update.plan, update.depth)
	}
}

func (q *wakeAdmissionQueue) notifyPreempt(ctx context.Context, events []admissionPreemption) {
	if q == nil || q.onPreempt == nil {
		return
	}
	for _, event := range events {
		q.onPreempt(ctx, event)
	}
}
