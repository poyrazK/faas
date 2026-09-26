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
	tokens            float64
	last              time.Time
	leases            map[string]memoryLease
	dailyUsageDate    string
	dailyRequestCount int64
	initialized       bool
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
		state = &memoryState{tokens: float64(spec.Burst), last: now, leases: make(map[string]memoryLease), initialized: true}
		b.states[spec.IntegrationID] = state
	}
	if state.dailyUsageDate != today {
		state.dailyUsageDate = today
		state.dailyRequestCount = 0
	}
	if !state.initialized {
		state.tokens = float64(spec.Burst)
		state.last = now
		state.initialized = true
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
	state.tokens--
	state.dailyRequestCount++
	id := uuid.NewString()
	state.leases[id] = memoryLease{expiresAt: now.Add(ttl)}
	return Decision{Granted: true, LeaseID: id, RequestTimeout: ttl}, nil
}

func (b *MemoryBackend) Release(ctx context.Context, integrationID, leaseID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if state := b.states[integrationID]; state != nil {
		delete(state.leases, leaseID)
	}
	return nil
}
