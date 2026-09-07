package gateway

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// burstCapacityAdmitter is an optional gateway capability. The request path
// uses it only when the backend can admit more than one instance; legacy test
// backends and pre-burst adapters keep the existing one-instance behaviour.
// The backend remains the authority on capacity and may return fewer admits
// than requested when the scheduler or node ledger reaches a limit.
type burstCapacityAdmitter interface {
	AdmitBurst(ctx context.Context, appID, scope, trigger string, maxConcurrency, count int) (admitted int, err error)
}

// burstPressure tracks requests which have passed the edge rate limits and
// may still need a function target. It is deliberately local to gatewayd:
// unlike Prometheus, it is available immediately during a burst and does not
// depend on a scrape or a healthy control-plane metrics path.
type burstPressure struct {
	apps sync.Map // app id -> *burstPressureState
}

type burstPressureState struct {
	inflight atomic.Int64

	arrivalMu   sync.Mutex
	arrivals    []int64
	arrivalHead int

	mu     sync.Mutex
	worker *burstGeneration
}

const (
	burstArrivalWindow      = time.Second
	burstRateObservationMin = 250 * time.Millisecond
	// Leave a small routing headroom around each configured RPS boundary.
	// Fixed-rate senders otherwise oscillate into the next instance when timer
	// jitter retains one boundary request or shortens the measured span by a
	// few microseconds.
	burstRateHeadroomPercent int64 = 5
)

// burstGeneration represents one bounded capacity reconciliation. Keeping
// the result on the generation (rather than on burstPressureState) prevents a
// waiter from observing the result of a newer worker that started just after
// the one it joined completed.
type burstGeneration struct {
	done chan struct{}
	err  error
}

var errBurstCapacityStalled = errors.New("gateway: burst capacity admission made no progress")

func (p *burstPressure) state(appID string) *burstPressureState {
	if p == nil || appID == "" {
		return nil
	}
	value, _ := p.apps.LoadOrStore(appID, &burstPressureState{})
	return value.(*burstPressureState)
}

// begin records one request and returns its balanced release function. The
// state is intentionally retained after the count reaches zero: deployed-app
// cardinality is bounded, while deleting map entries on the hot path would
// introduce a load/store race with a concurrent burst worker.
func (p *burstPressure) begin(appID string) func() {
	state := p.state(appID)
	if state == nil {
		return func() {}
	}
	state.recordArrival(time.Now())
	state.inflight.Add(1)
	return func() {
		state.inflight.Add(-1)
	}
}

// recordArrival keeps an exact, bounded one-second arrival window. This is an
// edge-local signal, so it reacts before schedd's periodic telemetry loop can
// observe a new burst. The app rate limiter bounds the retained slice.
func (s *burstPressureState) recordArrival(now time.Time) {
	if s == nil {
		return
	}
	s.arrivalMu.Lock()
	defer s.arrivalMu.Unlock()
	s.pruneArrivalsLocked(now.Add(-burstArrivalWindow).UnixNano())
	s.arrivals = append(s.arrivals, now.UnixNano())
}

func (s *burstPressureState) recentArrivals(now time.Time) int64 {
	count, _ := s.recentArrivalSample(now)
	return count
}

// recentArrivalSample returns the number of arrivals in the trailing window
// and the interval spanned by those arrivals. The interval lets the gateway
// recognize a sustained rise before a full second of requests has accumulated.
// A separate minimum observation period in desiredBurstInstancesForApp keeps
// a very short cluster of requests from being extrapolated into a large rate.
func (s *burstPressureState) recentArrivalSample(now time.Time) (count int64, span time.Duration) {
	if s == nil {
		return 0, 0
	}
	s.arrivalMu.Lock()
	defer s.arrivalMu.Unlock()
	s.pruneArrivalsLocked(now.Add(-burstArrivalWindow).UnixNano())
	count = int64(len(s.arrivals) - s.arrivalHead)
	if count > 1 {
		first := s.arrivals[s.arrivalHead]
		last := s.arrivals[len(s.arrivals)-1]
		if last > first {
			span = time.Duration(last - first)
		}
	}
	return count, span
}

func (s *burstPressureState) pruneArrivalsLocked(cutoff int64) {
	for s.arrivalHead < len(s.arrivals) && s.arrivals[s.arrivalHead] <= cutoff {
		s.arrivalHead++
	}
	if s.arrivalHead == len(s.arrivals) {
		s.arrivals = s.arrivals[:0]
		s.arrivalHead = 0
		return
	}
	if s.arrivalHead >= 256 && s.arrivalHead*2 >= len(s.arrivals) {
		copy(s.arrivals, s.arrivals[s.arrivalHead:])
		s.arrivals = s.arrivals[:len(s.arrivals)-s.arrivalHead]
		s.arrivalHead = 0
	}
}

func desiredBurstInstances(inflight int64, perVM, maxInstances int) int {
	if inflight <= 0 || perVM <= 0 || maxInstances <= 0 {
		return 0
	}
	desired := (inflight + int64(perVM) - 1) / int64(perVM)
	if desired > int64(maxInstances) {
		return maxInstances
	}
	return int(desired)
}

func desiredBurstInstancesForApp(state *burstPressureState, app App, perVM, maxInstances int, now time.Time) int {
	if state == nil {
		return 0
	}
	desired := desiredBurstInstances(state.inflight.Load(), perVM, maxInstances)
	if app.AutoscaleTargetRPS <= 0 || maxInstances <= 0 {
		return desired
	}
	arrivals, observed := state.recentArrivalSample(now)
	rateDenominator := int64(app.AutoscaleTargetRPS) * (100 + burstRateHeadroomPercent)
	byRPS := int((arrivals*100 + rateDenominator - 1) / rateDenominator)
	if arrivals > 1 && observed >= burstRateObservationMin {
		// There are arrivals-1 measured intervals between the first and last
		// timestamp. Compare that observed rate with the configured per-instance
		// target using integer ceiling arithmetic.
		numerator := (arrivals - 1) * int64(time.Second) * 100
		denominator := int64(observed) * rateDenominator
		byObservedRate := int((numerator + denominator - 1) / denominator)
		if byObservedRate > byRPS {
			byRPS = byObservedRate
		}
	}
	if byRPS > maxInstances {
		byRPS = maxInstances
	}
	if byRPS > desired {
		return byRPS
	}
	return desired
}

// maybeBurstCapacity reconciles desired capacity before the request is
// forwarded. There is still only one detached admission worker per app, but
// callers join its generation and wait until enough routable targets exist.
// This is the important distinction between a burst signal and burst
// admission: a request must not consume its entire wall-clock budget while
// extra capacity is merely being created in the background.
// waited tells the caller to discard any target selected before reconciliation.
func (h *Handler) maybeBurstCapacity(ctx context.Context, app App, maxInstances, perVM int) (waited bool, err error) {
	if h == nil || h.backend == nil || h.burstPressure == nil || app.ID == "" || maxInstances <= 0 || perVM <= 0 {
		return waited, nil
	}
	// Match schedd's effective ceiling, including legacy zero values and
	// apps whose saved limit exceeds a downgraded plan.
	if app.MaxConcurrency > 0 && app.MaxConcurrency < maxInstances {
		maxInstances = app.MaxConcurrency
	}
	admitter, ok := h.backend.(burstCapacityAdmitter)
	if !ok {
		return waited, nil
	}
	state := h.burstPressure.state(app.ID)
	if state == nil {
		return waited, nil
	}
	for {
		healthy := h.backend.HealthyCount(app.ID)
		desired := desiredBurstInstancesForApp(state, app, perVM, maxInstances, time.Now())
		if desired <= healthy {
			return waited, nil
		}

		state.mu.Lock()
		generation := state.worker
		if generation == nil {
			generation = &burstGeneration{done: make(chan struct{})}
			state.worker = generation
			go h.runBurstCapacity(ctx, app, maxInstances, perVM, state, generation, admitter)
		}
		state.mu.Unlock()

		waited = true
		select {
		case <-generation.done:
			if err := ctx.Err(); err != nil {
				return waited, err
			}
			// A scheduler refusal to expand does not invalidate targets that
			// already exist. Let the normal forwarding limits and request
			// budget bound their work instead of failing the whole burst.
			if generation.err != nil && h.backend.HealthyCount(app.ID) > 0 {
				return waited, nil
			}
			if generation.err != nil {
				return waited, generation.err
			}
			// The worker may have observed a lower demand after some
			// callers completed. Re-read pressure before forwarding.
		case <-ctx.Done():
			return waited, ctx.Err()
		}
	}
}

func (h *Handler) runBurstCapacity(ctx context.Context, app App, maxInstances, perVM int, state *burstPressureState, generation *burstGeneration, admitter burstCapacityAdmitter) {
	lifecycleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), admissionLifecycleTimeout)
	defer cancel()

	var workerErr error
	for lifecycleCtx.Err() == nil {
		healthy := h.backend.HealthyCount(app.ID)
		desired := desiredBurstInstancesForApp(state, app, perVM, maxInstances, time.Now())
		if desired <= healthy {
			break
		}
		count := desired - healthy
		if count > api.ScaleUpMaxBurstPerTick {
			count = api.ScaleUpMaxBurstPerTick
		}
		policy := WakeAdmissionPolicyForPlan(app.Plan)
		var admitted int
		var err error
		var queued bool
		var wait time.Duration
		admit := func(admitCtx context.Context) error {
			var admitErr error
			admitted, admitErr = admitter.AdmitBurst(admitCtx, app.ID, app.Scope, sched.TriggerGateway, maxInstances, count)
			return admitErr
		}
		if h.admissionQueue != nil {
			queued, wait, err = h.admissionQueue.Do(lifecycleCtx, app.ID, string(app.Plan), policy, admit)
			if h.metrics != nil {
				h.metrics.ObserveWakeAdmission(string(app.Plan), err, queued, wait)
			}
		} else {
			err = admit(lifecycleCtx)
		}
		if err != nil {
			workerErr = err
			if h.log != nil {
				h.log.Warn("gateway: burst admission failed", "app_id", app.ID, "requested", count, "admitted", admitted, "err", err)
			}
			break
		}
		if admitted == 0 || h.backend.HealthyCount(app.ID) <= healthy {
			workerErr = errBurstCapacityStalled
			if h.log != nil {
				h.log.Warn("gateway: burst admission made no progress", "app_id", app.ID, "requested", count, "admitted", admitted)
			}
			break
		}
	}
	if workerErr == nil && lifecycleCtx.Err() != nil {
		workerErr = lifecycleCtx.Err()
	}

	state.mu.Lock()
	generation.err = workerErr
	if state.worker == generation {
		state.worker = nil
	}
	close(generation.done)
	state.mu.Unlock()
}

// AdmitBurst runs a bounded set of scheduler admissions concurrently. The
// production schedd client preserves the scheduler's first-admit plus
// continuation semantics over the existing RPC; older Scheduler adapters
// fall back to concurrent single admissions. The schedd ledger remains the
// authoritative source for per-app and per-node limits.
func (b *PGBackend) AdmitBurst(ctx context.Context, appID, scope, trigger string, maxConcurrency, count int) (int, error) {
	if b == nil || appID == "" || maxConcurrency <= 0 || count <= 0 {
		return 0, nil
	}
	if count > api.ScaleUpMaxBurstPerTick {
		count = api.ScaleUpMaxBurstPerTick
	}
	// The production schedd client carries the scheduler's burst
	// continuation marker over gRPC. That preserves the existing
	// Engine.AdmitInstances contract: the first admission passes the
	// ordinary gates, while its siblings do not get rejected by the
	// same app's scale-out cooldown.
	if sched, err := b.resolveSched(ctx, appID); err != nil {
		return 0, err
	} else if burst, ok := sched.(burstScheduler); ok {
		var (
			mu       sync.Mutex
			admitted int
			firstErr error
		)
		err := burst.AdmitInstances(ctx, appID, scope, trigger, count,
			func(instanceID, nodeID, deploymentID, wakeID string, method int32, atCapacity bool, port int, admitErr error) {
				if admitErr != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = admitErr
					}
					mu.Unlock()
					return
				}
				_, _, atCap, recordErr := b.recordAdmission(ctx, appID, deploymentID, instanceID, nodeID, deploymentID, wakeID, method, atCapacity, port)
				mu.Lock()
				defer mu.Unlock()
				if recordErr != nil {
					if firstErr == nil {
						firstErr = recordErr
					}
					return
				}
				if !atCap {
					admitted++
				}
			})
		mu.Lock()
		defer mu.Unlock()
		if firstErr != nil {
			return admitted, firstErr
		}
		return admitted, err
	}

	type result struct {
		admitted bool
		err      error
	}
	results := make(chan result, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wakeID, _, atCapacity, err := b.Admit(ctx, appID, "", scope, trigger, maxConcurrency)
			results <- result{admitted: err == nil && !atCapacity && wakeID != "", err: err}
		}()
	}
	wg.Wait()
	close(results)

	admitted := 0
	var firstErr error
	for result := range results {
		if result.admitted {
			admitted++
		}
		if result.err != nil && firstErr == nil {
			firstErr = result.err
		}
	}
	return admitted, firstErr
}

var _ burstCapacityAdmitter = (*PGBackend)(nil)
