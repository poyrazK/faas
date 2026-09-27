package outbound

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type memoryLease struct{ expiresAt time.Time }

type memoryState struct {
	tokens             float64
	last               time.Time
	leases             map[string]memoryLease
	dailyUsageDate     string
	dailyRequestCount  int64
	bindingDailyCounts map[string]int64
	circuitFailures    int
	circuitOpenUntil   time.Time
	circuitProbeLease  string
	circuitThreshold   int
	circuitOpenSeconds int
	initialized        bool
}

// MemoryBackend is a deterministic in-process backend for tests and local
// development. Production deployments should use PostgresBackend so all
// gateway instances contend on one database row.
type MemoryBackend struct {
	mu     sync.Mutex
	states map[string]*memoryState
	now    func() time.Time
}

func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{states: make(map[string]*memoryState), now: time.Now}
}

// SetClock is intended for deterministic tests.
func (b *MemoryBackend) SetClock(now func() time.Time) { b.mu.Lock(); defer b.mu.Unlock(); b.now = now }

func (b *MemoryBackend) Admit(ctx context.Context, spec AdmissionSpec) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	if spec.IntegrationID == "" || math.IsNaN(spec.RatePerSecond) || math.IsInf(spec.RatePerSecond, 0) || spec.RatePerSecond <= 0 || spec.Burst < 1 || spec.MaxInFlight < 1 {
		return Decision{}, fmt.Errorf("%w: invalid admission spec", ErrInvalidIntegration)
	}
	if spec.DailyRequestLimit != nil && (*spec.DailyRequestLimit < 1 || *spec.DailyRequestLimit > api.MaxOutboundRequestsPerDay) {
		return Decision{}, fmt.Errorf("%w: daily request limit is outside the supported range", ErrInvalidIntegration)
	}
	if spec.BindingDailyRequestLimit != nil && (*spec.BindingDailyRequestLimit < 1 || *spec.BindingDailyRequestLimit > api.MaxOutboundRequestsPerDay) {
		return Decision{}, fmt.Errorf("%w: binding daily request limit is outside the supported range", ErrInvalidIntegration)
	}
	if spec.BindingDailyRequestLimit != nil && spec.BindingAppID == "" {
		return Decision{}, fmt.Errorf("%w: binding daily request limit requires an app ID", ErrInvalidIntegration)
	}
	if !api.ValidOutboundCircuitBreakerPolicy(spec.CircuitBreakerFailureThreshold, spec.CircuitBreakerOpenSeconds) {
		return Decision{}, fmt.Errorf("%w: circuit-breaker policy is invalid", ErrInvalidIntegration)
	}
	ttl := spec.LeaseTTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	utcNow := now.UTC()
	today := utcNow.Format("2006-01-02")
	state := b.states[spec.IntegrationID]
	if state == nil {
		state = &memoryState{tokens: float64(spec.Burst), last: now, leases: make(map[string]memoryLease), bindingDailyCounts: make(map[string]int64), initialized: true}
		b.states[spec.IntegrationID] = state
	}
	if state.dailyUsageDate != today {
		state.dailyUsageDate = today
		state.dailyRequestCount = 0
		state.bindingDailyCounts = make(map[string]int64)
	} else if state.bindingDailyCounts == nil {
		state.bindingDailyCounts = make(map[string]int64)
	}
	if !state.initialized {
		state.tokens = float64(spec.Burst)
		state.last = now
		state.initialized = true
	}
	if state.circuitThreshold != spec.CircuitBreakerFailureThreshold || state.circuitOpenSeconds != spec.CircuitBreakerOpenSeconds {
		state.circuitFailures = 0
		state.circuitOpenUntil = time.Time{}
		state.circuitProbeLease = ""
		state.circuitThreshold = spec.CircuitBreakerFailureThreshold
		state.circuitOpenSeconds = spec.CircuitBreakerOpenSeconds
	}
	for id, lease := range state.leases {
		if !lease.expiresAt.After(now) {
			delete(state.leases, id)
		}
	}
	elapsed := now.Sub(state.last).Seconds()
	if elapsed > 0 {
		state.tokens += elapsed * spec.RatePerSecond
		state.last = now
	}
	if state.tokens > float64(spec.Burst) {
		state.tokens = float64(spec.Burst)
	}
	if len(state.leases) >= spec.MaxInFlight {
		retry := ttl
		for _, lease := range state.leases {
			if d := lease.expiresAt.Sub(now); d < retry {
				retry = d
			}
		}
		if retry < time.Millisecond {
			retry = time.Millisecond
		}
		return Decision{RetryAfter: retry, Reason: ReasonConcurrency, RequestTimeout: ttl}, nil
	}
	if state.tokens < 1 {
		retry := time.Duration((1 - state.tokens) / spec.RatePerSecond * float64(time.Second))
		if retry < time.Millisecond {
			retry = time.Millisecond
		}
		return Decision{RetryAfter: retry, Reason: ReasonRate, RequestTimeout: ttl}, nil
	}
	if spec.DailyRequestLimit != nil && state.dailyRequestCount >= *spec.DailyRequestLimit {
		nextDay := time.Date(utcNow.Year(), utcNow.Month(), utcNow.Day()+1, 0, 0, 0, 0, time.UTC)
		retry := nextDay.Sub(utcNow)
		if retry < time.Millisecond {
			retry = time.Millisecond
		}
		return Decision{RetryAfter: retry, Reason: ReasonDailyLimit, RequestTimeout: ttl}, nil
	}
	if spec.BindingDailyRequestLimit != nil && state.bindingDailyCounts[spec.BindingAppID] >= *spec.BindingDailyRequestLimit {
		nextDay := time.Date(utcNow.Year(), utcNow.Month(), utcNow.Day()+1, 0, 0, 0, 0, time.UTC)
		retry := nextDay.Sub(utcNow)
		if retry < time.Millisecond {
			retry = time.Millisecond
		}
		return Decision{RetryAfter: retry, Reason: ReasonDailyLimit, RequestTimeout: ttl}, nil
	}
	state.tokens--
	state.dailyRequestCount++
	if spec.BindingAppID != "" {
		state.bindingDailyCounts[spec.BindingAppID]++
	}
	id := uuid.NewString()
	state.leases[id] = memoryLease{expiresAt: now.Add(ttl)}
	return Decision{
		Granted: true, LeaseID: id, RequestTimeout: ttl,
		CircuitBreakerFailureThreshold: spec.CircuitBreakerFailureThreshold,
		CircuitBreakerOpenSeconds:      spec.CircuitBreakerOpenSeconds,
	}, nil
}

func (b *MemoryBackend) AllowCircuit(ctx context.Context, integrationID, leaseID string, threshold, openSeconds int) (CircuitBreakerDecision, error) {
	if err := ctx.Err(); err != nil {
		return CircuitBreakerDecision{}, err
	}
	if integrationID == "" || !validUUID(leaseID) || !api.ValidOutboundCircuitBreakerPolicy(threshold, openSeconds) {
		return CircuitBreakerDecision{}, fmt.Errorf("%w: invalid circuit-breaker request", ErrInvalidIntegration)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[integrationID]
	if state == nil {
		return CircuitBreakerDecision{}, fmt.Errorf("outbound admission state is missing")
	}
	now := b.now()
	lease, ok := state.leases[leaseID]
	if !ok || !lease.expiresAt.After(now) {
		return CircuitBreakerDecision{}, fmt.Errorf("outbound admission lease is missing or expired")
	}
	if state.circuitThreshold != threshold || state.circuitOpenSeconds != openSeconds {
		state.circuitFailures = 0
		state.circuitOpenUntil = time.Time{}
		state.circuitProbeLease = ""
		state.circuitThreshold = threshold
		state.circuitOpenSeconds = openSeconds
	}
	if threshold == 0 || state.circuitOpenUntil.IsZero() {
		return CircuitBreakerDecision{Allowed: true}, nil
	}
	if state.circuitOpenUntil.After(now) {
		return CircuitBreakerDecision{RetryAfter: state.circuitOpenUntil.Sub(now)}, nil
	}
	if probeLease, active := state.leases[state.circuitProbeLease]; state.circuitProbeLease != "" && active && probeLease.expiresAt.After(now) {
		retry := probeLease.expiresAt.Sub(now)
		if retry < time.Millisecond {
			retry = time.Millisecond
		}
		return CircuitBreakerDecision{RetryAfter: retry}, nil
	}
	state.circuitProbeLease = leaseID
	return CircuitBreakerDecision{Allowed: true, Probe: true}, nil
}

func (b *MemoryBackend) RecordCircuitOutcome(ctx context.Context, integrationID, leaseID string, threshold, openSeconds int, outcome CircuitBreakerOutcome) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if integrationID == "" || !validUUID(leaseID) || !api.ValidOutboundCircuitBreakerPolicy(threshold, openSeconds) ||
		(outcome != CircuitOutcomeSuccess && outcome != CircuitOutcomeFailure && outcome != CircuitOutcomeNeutral) {
		return fmt.Errorf("%w: invalid circuit-breaker outcome", ErrInvalidIntegration)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[integrationID]
	if state == nil || state.circuitThreshold != threshold || state.circuitOpenSeconds != openSeconds || threshold == 0 {
		return nil
	}
	now := b.now()
	_, hasLease := state.leases[leaseID]
	if !hasLease {
		return nil
	}
	probe := state.circuitProbeLease == leaseID
	if probe {
		state.circuitProbeLease = ""
		switch outcome {
		case CircuitOutcomeSuccess:
			state.circuitFailures = 0
			state.circuitOpenUntil = time.Time{}
		case CircuitOutcomeFailure, CircuitOutcomeNeutral:
			state.circuitFailures = 0
			state.circuitOpenUntil = now.Add(time.Duration(openSeconds) * time.Second)
		}
		return nil
	}
	// Requests admitted before another call opened the breaker are no longer
	// evidence for the current closed-state failure streak.
	if !state.circuitOpenUntil.IsZero() {
		return nil
	}
	switch outcome {
	case CircuitOutcomeSuccess:
		state.circuitFailures = 0
	case CircuitOutcomeFailure:
		state.circuitFailures++
		if state.circuitFailures >= threshold {
			state.circuitFailures = 0
			state.circuitOpenUntil = now.Add(time.Duration(openSeconds) * time.Second)
		}
	}
	return nil
}

func (b *MemoryBackend) Release(ctx context.Context, integrationID, leaseID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if state := b.states[integrationID]; state != nil {
		delete(state.leases, leaseID)
		if state.circuitProbeLease == leaseID {
			state.circuitProbeLease = ""
		}
	}
	return nil
}

func validUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

var _ CircuitBreakerBackend = (*MemoryBackend)(nil)
