package outbound

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type memoryLease struct {
	expiresAt time.Time
	// bindingApp and the reservation count against AI hard caps while the
	// lease lives (ADR-991).
	bindingApp     string
	endUser        string
	tags           []string
	reservedTokens int64
	reservedCost   int64
}

// memoryMinute is one app's fixed UTC-minute window (ADR-991).
type memoryMinute struct {
	window           time.Time
	requests, tokens int64
}

type memoryState struct {
	tokens                   float64
	last                     time.Time
	leases                   map[string]memoryLease
	dailyUsageDate           string
	dailyRequestCount        int64
	bindingDailyCounts       map[string]int64
	circuitFailures          int
	circuitOpenUntil         time.Time
	circuitProbeLease        string
	circuitThreshold         int
	circuitOpenSeconds       int
	providerCooldownUntil    time.Time
	providerCooldownRevision int64
	retryBudgetTokens        float64
	retryBudgetRefill        time.Time
	retryBudgetPerMinute     int
	dailyAITokenCount        int64
	bindingAITokenCounts     map[string]int64
	dailyAICost              int64
	usageMonth               string
	monthlyAICost            int64
	aiUsage                  map[memoryAIUsageKey]AIUsageTotals
	// userUsage is today's per-end-user rollup (ADR-994), and tagUsage the
	// per-tag one, with the tags the latest admissions limited (ADR-1410).
	userUsage      map[string]AIUserUsageTotals
	tagUsage       map[string]AIUserUsageTotals
	tagLimits      map[string]int64
	bindingMinutes map[string]memoryMinute
	// The limits of the latest admission, used to detect budget alerts at
	// settlement (ADR-991), and the alerts and request log recorded.
	aiLimits           aiBudgetLimits
	bindingTokenLimits map[string]*int64
	aiAlerts           []AIBudgetAlert
	aiRequestLog       []AIUsageRecord
	// contentCaptures counts today's captured calls (ADR-996).
	contentCaptures int
	// settledLeases keys today's settlements (ADR-1120).
	settledLeases map[string]struct{}
	// batches keep created batches' reservations until they settle
	// (ADR-1360).
	batches     map[string]*memoryBatch
	initialized bool
}

// memoryAIRequestLogMax bounds the in-process request log.
const memoryAIRequestLogMax = 10_000

type memoryAIUsageKey struct{ caller, model string }

// AIUsageTotals is one UTC-day rollup for a caller and model.
type AIUsageTotals struct {
	Requests, ErrorRequests, UsageUnknownRequests                  int64
	InputTokens, CachedInputTokens, CacheWriteTokens, OutputTokens int64
	CostMillicents                                                 int64
	// Media usage (ADR-997).
	Images, AudioSeconds, Characters int64
	// VideoSeconds is seconds of generated video (ADR-1370).
	VideoSeconds int64
	// Built-in tool uses (ADR-1380).
	WebSearches, FileSearches, CodeSessionGB int64
	// Cache hits and what caching saved (ADR-1440).
	CachedRequests, CacheSavedMillicents, PromptCacheSavedMillicents int64
}

// AIUserUsageTotals is one end user's UTC-day rollup (ADR-994).
type AIUserUsageTotals struct {
	Requests, ErrorRequests, InputTokens, OutputTokens, CostMillicents int64
}

// MemoryBackend is a deterministic in-process backend for tests and local
// development. Production deployments should use PostgresBackend so all
// gateway instances contend on one database row.
type MemoryBackend struct {
	mu     sync.Mutex
	states map[string]*memoryState
	now    func() time.Time
	// routeAttempts and logExports keep what settlements wrote for canaries
	// and log drains (ADR-1340), bounded like the request log.
	routeAttempts []AIRouteAttemptRecord
	logExports    []AILogExport
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
	if spec.DailyAITokenLimit != nil && (*spec.DailyAITokenLimit < 1 || *spec.DailyAITokenLimit > api.MaxOutboundAITokensPerDay) {
		return Decision{}, fmt.Errorf("%w: daily token limit is outside the supported range", ErrInvalidIntegration)
	}
	if spec.BindingDailyAITokenLimit != nil && (spec.BindingAppID == "" || *spec.BindingDailyAITokenLimit < 1 || *spec.BindingDailyAITokenLimit > api.MaxOutboundAITokensPerDay) {
		return Decision{}, fmt.Errorf("%w: binding daily token limit is invalid", ErrInvalidIntegration)
	}
	for _, limit := range []*int64{spec.DailyAICostLimit, spec.MonthlyAICostLimit} {
		if limit != nil && (*limit < 1 || *limit > api.MaxOutboundAICostMillicents) {
			return Decision{}, fmt.Errorf("%w: AI cost limit is outside the supported range", ErrInvalidIntegration)
		}
	}
	if err := validateAIAdmissionSpec(spec); err != nil {
		return Decision{}, err
	}
	if !api.ValidOutboundCircuitBreakerPolicy(spec.CircuitBreakerFailureThreshold, spec.CircuitBreakerOpenSeconds) {
		return Decision{}, fmt.Errorf("%w: circuit-breaker policy is invalid", ErrInvalidIntegration)
	}
	if spec.RetryBudgetPerMinute < 0 || spec.RetryBudgetPerMinute > api.MaxOutboundRetryBudgetPerMinute {
		return Decision{}, fmt.Errorf("%w: retry-budget policy is invalid", ErrInvalidIntegration)
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
	state.rollDay(today)
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
	if state.retryBudgetPerMinute != spec.RetryBudgetPerMinute {
		state.retryBudgetTokens = float64(spec.RetryBudgetPerMinute)
		state.retryBudgetRefill = now
		state.retryBudgetPerMinute = spec.RetryBudgetPerMinute
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
	state.aiLimits = aiBudgetLimits{dailyTokens: copyInt64Pointer(spec.DailyAITokenLimit),
		dailyCost: copyInt64Pointer(spec.DailyAICostLimit), monthlyCost: copyInt64Pointer(spec.MonthlyAICostLimit),
		userTokens: copyInt64Pointer(spec.UserDailyAITokenLimit), userCost: copyInt64Pointer(spec.UserDailyAICostLimit)}
	if spec.BindingAppID != "" {
		if state.bindingTokenLimits == nil {
			state.bindingTokenLimits = make(map[string]*int64)
		}
		state.bindingTokenLimits[spec.BindingAppID] = copyInt64Pointer(spec.BindingDailyAITokenLimit)
	}
	inflight := state.inflightReservations(spec.BindingAppID, spec.AIEndUser)
	reserve := aiReservationFromSpec(spec)
	user, tracked := state.userUsage[spec.AIEndUser]
	if reason, retry, limited := aiBudgetExhausted(utcNow, aiBudgetState{
		dailyTokens: state.dailyAITokenCount, bindingTokens: state.bindingAITokenCounts[spec.BindingAppID],
		dailyCost: state.dailyAICost, monthlyCost: state.monthlyAICost,
		inflightTokens: inflight.tokens, inflightBindingTokens: inflight.bindingTokens, inflightCost: inflight.cost,
		userTokens: user.InputTokens + user.OutputTokens, userCost: user.CostMillicents,
		inflightUserTokens: inflight.userTokens, inflightUserCost: inflight.userCost,
		userUntracked: spec.AIEndUser != "" && !tracked && len(state.userUsage) >= api.OutboundAIUsersPerDay,
	}, aiBudgetLimitsFromSpec(spec), reserve); limited {
		return Decision{RetryAfter: retry, Reason: reason, RequestTimeout: ttl}, nil
	}
	if reason, retry, limited := aiTagBudgetExhausted(utcNow, state.tagStates(spec), reserve); limited {
		return Decision{RetryAfter: retry, Reason: reason, RequestTimeout: ttl}, nil
	}
	minute := state.bindingMinute(spec.BindingAppID, utcNow)
	if spec.BindingAppID != "" {
		if reason, retry, limited := aiMinuteExhausted(utcNow, aiMinuteState{
			requests: minute.requests, tokens: minute.tokens, inflightTokens: inflight.bindingTokens,
		}, spec.BindingRequestsPerMinute, spec.BindingTokensPerMinute, reserve); limited {
			return Decision{RetryAfter: retry, Reason: reason, RequestTimeout: ttl}, nil
		}
	}
	state.tokens--
	state.dailyRequestCount++
	if spec.BindingAppID != "" {
		state.bindingDailyCounts[spec.BindingAppID]++
		minute.requests++
		state.bindingMinutes[spec.BindingAppID] = minute
	}
	id := uuid.NewString()
	state.leases[id] = memoryLease{expiresAt: now.Add(ttl), bindingApp: spec.BindingAppID, endUser: spec.AIEndUser,
		tags: append([]string(nil), spec.AITags...), reservedTokens: reserve.tokens, reservedCost: reserve.cost}
	decision := Decision{
		Granted: true, LeaseID: id, RequestTimeout: ttl,
		CircuitBreakerFailureThreshold: spec.CircuitBreakerFailureThreshold,
		CircuitBreakerOpenSeconds:      spec.CircuitBreakerOpenSeconds,
		RetryBudgetPerMinute:           spec.RetryBudgetPerMinute,
		CircuitClosed:                  spec.CircuitBreakerFailureThreshold > 0 && state.circuitOpenUntil.IsZero(),
	}
	// ADR-1120: the cooldown as admission sees it, like the Postgres backend.
	if state.providerCooldownRevision == spec.PolicyRevision {
		gate := ProviderCooldownDecision{Allowed: true}
		if state.providerCooldownUntil.After(now) {
			gate = ProviderCooldownDecision{RetryAfter: state.providerCooldownUntil.Sub(now)}
		}
		decision.ProviderGate = &gate
	}
	return decision, nil
}

func (b *MemoryBackend) ConsumeRetryToken(ctx context.Context, integrationID string, retryBudgetPerMinute int) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if integrationID == "" || retryBudgetPerMinute < 1 || retryBudgetPerMinute > api.MaxOutboundRetryBudgetPerMinute {
		return false, fmt.Errorf("%w: invalid retry-budget request", ErrInvalidIntegration)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[integrationID]
	if state == nil || state.retryBudgetPerMinute != retryBudgetPerMinute {
		return false, nil
	}
	now := b.now()
	if elapsed := now.Sub(state.retryBudgetRefill).Seconds(); elapsed > 0 {
		state.retryBudgetTokens += elapsed * float64(retryBudgetPerMinute) / 60
		state.retryBudgetRefill = now
	}
	if state.retryBudgetTokens > float64(retryBudgetPerMinute) {
		state.retryBudgetTokens = float64(retryBudgetPerMinute)
	}
	if state.retryBudgetTokens < 1 {
		return false, nil
	}
	state.retryBudgetTokens--
	return true, nil
}

func (b *MemoryBackend) AllowProviderRequest(ctx context.Context, integrationID string, policyRevision int64) (ProviderCooldownDecision, error) {
	if err := ctx.Err(); err != nil {
		return ProviderCooldownDecision{}, err
	}
	if integrationID == "" {
		return ProviderCooldownDecision{}, fmt.Errorf("%w: integration id is required", ErrInvalidIntegration)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[integrationID]
	if state == nil {
		return ProviderCooldownDecision{}, fmt.Errorf("outbound admission state is missing")
	}
	now := b.now()
	if state.providerCooldownRevision != policyRevision {
		state.providerCooldownUntil = time.Time{}
		state.providerCooldownRevision = policyRevision
	}
	if state.providerCooldownUntil.After(now) {
		return ProviderCooldownDecision{RetryAfter: state.providerCooldownUntil.Sub(now)}, nil
	}
	state.providerCooldownUntil = time.Time{}
	return ProviderCooldownDecision{Allowed: true}, nil
}

func (b *MemoryBackend) RecordProviderCooldown(ctx context.Context, integrationID string, policyRevision int64, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if integrationID == "" || delay <= 0 || delay > maxProviderCooldown {
		return fmt.Errorf("%w: provider cooldown is outside the supported range", ErrInvalidIntegration)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[integrationID]
	if state == nil {
		return fmt.Errorf("outbound admission state is missing")
	}
	if state.providerCooldownRevision != policyRevision {
		state.providerCooldownUntil = time.Time{}
		state.providerCooldownRevision = policyRevision
	}
	until := b.now().Add(delay)
	if until.After(state.providerCooldownUntil) {
		state.providerCooldownUntil = until
	}
	return nil
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

type memoryInflight struct{ tokens, bindingTokens, cost, userTokens, userCost int64 }

// inflightReservations sums live leases' reservations; expired leases were
// already removed by the caller.
func (s *memoryState) inflightReservations(bindingApp, endUser string) memoryInflight {
	var out memoryInflight
	for _, lease := range s.leases {
		out.tokens += lease.reservedTokens
		out.cost += lease.reservedCost
		if bindingApp != "" && lease.bindingApp == bindingApp {
			out.bindingTokens += lease.reservedTokens
		}
		if endUser != "" && lease.endUser == endUser {
			out.userTokens += lease.reservedTokens
			out.userCost += lease.reservedCost
		}
	}
	for _, batch := range s.batches {
		if batch.settled {
			continue
		}
		out.tokens += batch.batch.ReservedTokens
		out.cost += batch.batch.ReservedCostMillicents
		if bindingApp != "" && batch.batch.BindingAppID == bindingApp {
			out.bindingTokens += batch.batch.ReservedTokens
		}
	}
	return out
}

// bindingMinute returns the app's window for the current UTC minute.
func (s *memoryState) bindingMinute(bindingApp string, utcNow time.Time) memoryMinute {
	if s.bindingMinutes == nil {
		s.bindingMinutes = make(map[string]memoryMinute)
	}
	window := utcNow.Truncate(time.Minute)
	minute := s.bindingMinutes[bindingApp]
	if !minute.window.Equal(window) {
		minute = memoryMinute{window: window}
	}
	return minute
}

// rollDay resets every UTC-day counter when the day changes.
func (s *memoryState) rollDay(today string) {
	if month := today[:7]; s.usageMonth != month {
		s.usageMonth = month
		s.monthlyAICost = 0
	}
	if s.dailyUsageDate != today {
		s.dailyUsageDate = today
		s.dailyRequestCount = 0
		s.dailyAITokenCount = 0
		s.dailyAICost = 0
		s.bindingDailyCounts = make(map[string]int64)
		s.bindingAITokenCounts = make(map[string]int64)
		s.aiUsage = make(map[memoryAIUsageKey]AIUsageTotals)
		s.userUsage = make(map[string]AIUserUsageTotals)
		s.tagUsage = make(map[string]AIUserUsageTotals)
		s.contentCaptures = 0
		s.settledLeases = nil
		return
	}
	if s.userUsage == nil {
		s.userUsage = make(map[string]AIUserUsageTotals)
	}
	if s.tagUsage == nil {
		s.tagUsage = make(map[string]AIUserUsageTotals)
	}
	if s.bindingDailyCounts == nil {
		s.bindingDailyCounts = make(map[string]int64)
	}
	if s.bindingAITokenCounts == nil {
		s.bindingAITokenCounts = make(map[string]int64)
	}
	if s.aiUsage == nil {
		s.aiUsage = make(map[memoryAIUsageKey]AIUsageTotals)
	}
}

// RecordAIUsage settles one AI call with the same day rollover and model cap
// as the Postgres backend.
func (b *MemoryBackend) RecordAIUsage(ctx context.Context, record AIUsageRecord) error {
	_, err := b.RecordAIUsageAlerts(ctx, record)
	return err
}

// RecordAIUsageAlerts settles one AI call and records the budget alerts it
// reached and its request-log entry (ADR-991).
func (b *MemoryBackend) RecordAIUsageAlerts(ctx context.Context, record AIUsageRecord) ([]AIBudgetAlert, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateAIUsageRecord(record); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[record.IntegrationID]
	if state == nil {
		state = &memoryState{leases: make(map[string]memoryLease), bindingDailyCounts: make(map[string]int64)}
		b.states[record.IntegrationID] = state
	}
	utcNow := b.now().UTC()
	state.rollDay(utcNow.Format("2006-01-02"))
	if record.LeaseID != "" {
		// ADR-1120: the lease keys the settlement and leaves with it.
		if _, settled := state.settledLeases[record.LeaseID]; settled {
			return nil, nil
		}
		if state.settledLeases == nil {
			state.settledLeases = make(map[string]struct{})
		}
		state.settledLeases[record.LeaseID] = struct{}{}
		delete(state.leases, record.LeaseID)
		if state.circuitProbeLease == record.LeaseID {
			state.circuitProbeLease = ""
		}
	}
	before := aiBudgetState{dailyTokens: state.dailyAITokenCount, bindingTokens: state.bindingAITokenCounts[record.Caller],
		dailyCost: state.dailyAICost, monthlyCost: state.monthlyAICost}
	state.dailyAITokenCount += record.Usage.BudgetTokens()
	state.bindingAITokenCounts[record.Caller] += record.Usage.BudgetTokens()
	if appCaller(record.Caller) && record.Usage.BudgetTokens() > 0 {
		minute := state.bindingMinute(record.Caller, utcNow)
		minute.tokens += record.Usage.BudgetTokens()
		state.bindingMinutes[record.Caller] = minute
	}
	state.dailyAICost += record.CostMillicents
	state.monthlyAICost += record.CostMillicents
	key := memoryAIUsageKey{caller: record.Caller, model: record.Usage.Model}
	if _, exists := state.aiUsage[key]; !exists && memoryDistinctModels(state.aiUsage) >= api.OutboundAIUsageModelsPerDay {
		key.model = aiUsageOverflowModel
	}
	row := state.aiUsage[key]
	row.Requests++
	switch record.Outcome {
	case AIUsageOutcomeError:
		row.ErrorRequests++
	case AIUsageOutcomeUnknown:
		row.UsageUnknownRequests++
	}
	row.InputTokens += record.Usage.InputTokens
	row.CachedInputTokens += record.Usage.CachedInputTokens
	row.CacheWriteTokens += record.Usage.CacheWriteTokens
	row.OutputTokens += record.Usage.OutputTokens
	row.CostMillicents += record.CostMillicents
	row.Images += record.Usage.Images
	row.AudioSeconds += record.Usage.AudioSeconds
	row.Characters += record.Usage.Characters
	row.VideoSeconds += record.Usage.VideoSeconds
	row.WebSearches += record.Usage.WebSearches
	row.FileSearches += record.Usage.FileSearches
	row.CodeSessionGB += record.Usage.CodeSessionGB
	row.CachedRequests += cachedRequests(record)
	row.CacheSavedMillicents += record.CacheSavedMillicents
	row.PromptCacheSavedMillicents += record.PromptCacheSavedMillicents
	state.aiUsage[key] = row
	crossings := []aiBudgetCrossing{
		{api.OutboundAIBudgetDailyTokens, "", utcDayStart(utcNow), state.aiLimits.dailyTokens, before.dailyTokens, state.dailyAITokenCount, "", ""},
		{api.OutboundAIBudgetDailyCost, "", utcDayStart(utcNow), state.aiLimits.dailyCost, before.dailyCost, state.dailyAICost, "", ""},
		{api.OutboundAIBudgetMonthlyCost, "", utcMonthStart(utcNow), state.aiLimits.monthlyCost, before.monthlyCost, state.monthlyAICost, "", ""},
	}
	var tagDays []aiTagDay
	for _, tag := range record.Tags {
		tagDays = append(tagDays, state.recordTagUsage(tag, record))
	}
	if record.EndUser != "" {
		endUser, totals := state.recordUserUsage(record)
		crossings = append(crossings, userBudgetCrossings(endUser, utcDayStart(utcNow), state.aiLimits.userTokens, state.aiLimits.userCost,
			totals.InputTokens+totals.OutputTokens, totals.CostMillicents, record.Usage.InputTokens+record.Usage.OutputTokens, record.CostMillicents)...)
	}
	if appCaller(record.Caller) {
		crossings = append(crossings, aiBudgetCrossing{api.OutboundAIBudgetAppDailyTokens, record.Caller, utcDayStart(utcNow),
			state.bindingTokenLimits[record.Caller], before.bindingTokens, state.bindingAITokenCounts[record.Caller], "", ""})
	}
	crossings = append(crossings, tagBudgetCrossings(tagDays, utcDayStart(utcNow), record.CostMillicents)...)
	var recorded []AIBudgetAlert
	for _, alert := range aiBudgetAlertsFor(record.IntegrationID, crossings, utcNow) {
		if memoryAlertSeen(state.aiAlerts, alert) {
			continue
		}
		alert.ID = uuid.NewString()
		state.aiAlerts = append(state.aiAlerts, alert)
		recorded = append(recorded, alert)
	}
	if record.Log != nil {
		if record.Log.Content != nil {
			// The log keeps the record, so drop content past the day's cap
			// without touching the caller's copy.
			logged := *record.Log
			if state.contentCaptures >= api.OutboundAIContentCapturesPerDay {
				logged.Content = nil
			} else {
				state.contentCaptures++
			}
			record.Log = &logged
		}
		state.aiRequestLog = append(state.aiRequestLog, record)
		if len(state.aiRequestLog) > memoryAIRequestLogMax {
			state.aiRequestLog = state.aiRequestLog[len(state.aiRequestLog)-memoryAIRequestLogMax:]
		}
	}
	if record.Route != nil && len(b.routeAttempts) < memoryAIRequestLogMax {
		b.routeAttempts = append(b.routeAttempts, *record.Route)
	}
	if record.Export != nil && len(b.logExports) < memoryAIRequestLogMax {
		b.logExports = append(b.logExports, *record.Export)
	}
	return recorded, nil
}

// AIRouteAttempts reports the canary-tracked route attempts settled so far,
// for tests.
func (b *MemoryBackend) AIRouteAttempts() []AIRouteAttemptRecord {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]AIRouteAttemptRecord(nil), b.routeAttempts...)
}

// AILogExports reports the export lines settled so far, for tests.
func (b *MemoryBackend) AILogExports() []AILogExport {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]AILogExport(nil), b.logExports...)
}

// recordUserUsage adds a call to its end user's rollup. Beyond the per-day
// cap of distinct users, calls aggregate under the overflow row. It returns
// the row's user and totals.
func (s *memoryState) recordUserUsage(record AIUsageRecord) (string, AIUserUsageTotals) {
	endUser := record.EndUser
	if _, exists := s.userUsage[endUser]; !exists && len(s.userUsage) >= api.OutboundAIUsersPerDay {
		endUser = api.OutboundAIUserOverflow
	}
	row := s.userUsage[endUser]
	row.Requests++
	if record.Outcome == AIUsageOutcomeError {
		row.ErrorRequests++
	}
	row.InputTokens += record.Usage.InputTokens
	row.OutputTokens += record.Usage.OutputTokens
	row.CostMillicents += record.CostMillicents
	s.userUsage[endUser] = row
	return endUser, row
}

// tagStates reads the call's limited tags like the Postgres backend, and
// remembers them so their rows never overflow.
func (s *memoryState) tagStates(spec AdmissionSpec) []aiTagState {
	var states []aiTagState
	for _, tag := range spec.AITags {
		limit, ok := spec.AITagDailyCostLimits[tag]
		if !ok {
			continue
		}
		if s.tagLimits == nil {
			s.tagLimits = make(map[string]int64)
		}
		s.tagLimits[tag] = limit
		state := aiTagState{tag: tag, limit: limit, used: s.tagUsage[tag].CostMillicents}
		for _, lease := range s.leases {
			if slices.Contains(lease.tags, tag) {
				state.held += lease.reservedCost
			}
		}
		states = append(states, state)
	}
	return states
}

// recordTagUsage adds a call to one tag's rollup, or to its key's overflow
// row past the day's cap of distinct tags (ADR-1410).
func (s *memoryState) recordTagUsage(tag string, record AIUsageRecord) aiTagDay {
	_, limited := s.tagLimits[tag]
	if _, exists := s.tagUsage[tag]; !exists && !limited && len(s.tagUsage) >= api.OutboundAITagsPerDay {
		tag = tagOverflow(tag)
	}
	row := s.tagUsage[tag]
	row.Requests++
	if record.Outcome == AIUsageOutcomeError {
		row.ErrorRequests++
	}
	row.InputTokens += record.Usage.InputTokens
	row.OutputTokens += record.Usage.OutputTokens
	row.CostMillicents += record.CostMillicents
	s.tagUsage[tag] = row
	day := aiTagDay{tag: tag, cost: row.CostMillicents}
	if limit, ok := s.tagLimits[tag]; ok {
		day.limit = &limit
	}
	return day
}

// AITagUsage reports one tag's UTC-day rollup for tests.
func (b *MemoryBackend) AITagUsage(integrationID, tag string) (AIUserUsageTotals, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[integrationID]
	if state == nil || state.dailyUsageDate != b.now().UTC().Format("2006-01-02") {
		return AIUserUsageTotals{}, false
	}
	row, ok := state.tagUsage[tag]
	return row, ok
}

// AIUserUsage reports one end user's UTC-day rollup for tests.
func (b *MemoryBackend) AIUserUsage(integrationID, endUser string) (AIUserUsageTotals, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[integrationID]
	if state == nil || state.dailyUsageDate != b.now().UTC().Format("2006-01-02") {
		return AIUserUsageTotals{}, false
	}
	row, ok := state.userUsage[endUser]
	return row, ok
}

func memoryAlertSeen(alerts []AIBudgetAlert, alert AIBudgetAlert) bool {
	for _, seen := range alerts {
		if seen.Budget == alert.Budget && seen.AppID == alert.AppID && seen.EndUser == alert.EndUser && seen.Tag == alert.Tag &&
			seen.PeriodStart.Equal(alert.PeriodStart) &&
			seen.ThresholdPercent == alert.ThresholdPercent {
			return true
		}
	}
	return false
}

// AIBudgetAlerts reports the recorded budget alerts for tests.
func (b *MemoryBackend) AIBudgetAlerts(integrationID string) []AIBudgetAlert {
	b.mu.Lock()
	defer b.mu.Unlock()
	if state := b.states[integrationID]; state != nil {
		return append([]AIBudgetAlert(nil), state.aiAlerts...)
	}
	return nil
}

// AIRequestLog reports the logged AI calls for tests.
func (b *MemoryBackend) AIRequestLog(integrationID string) []AIUsageRecord {
	b.mu.Lock()
	defer b.mu.Unlock()
	if state := b.states[integrationID]; state != nil {
		return append([]AIUsageRecord(nil), state.aiRequestLog...)
	}
	return nil
}

// AICost reports the settled UTC-day and calendar-month cost for tests.
func (b *MemoryBackend) AICost(integrationID string) (daily, monthly int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[integrationID]
	if state == nil {
		return 0, 0
	}
	today := b.now().UTC().Format("2006-01-02")
	if state.dailyUsageDate == today {
		daily = state.dailyAICost
	}
	if state.usageMonth == today[:7] {
		monthly = state.monthlyAICost
	}
	return daily, monthly
}

func memoryDistinctModels(rows map[memoryAIUsageKey]AIUsageTotals) int {
	models := make(map[string]struct{}, len(rows))
	for key := range rows {
		models[key.model] = struct{}{}
	}
	return len(models)
}

// DailyAITokenCount reports the settled UTC-day AI tokens for tests.
func (b *MemoryBackend) DailyAITokenCount(integrationID string) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if state := b.states[integrationID]; state != nil && state.dailyUsageDate == b.now().UTC().Format("2006-01-02") {
		return state.dailyAITokenCount
	}
	return 0
}

// AIUsageRollup reports one caller/model UTC-day rollup for tests.
func (b *MemoryBackend) AIUsageRollup(integrationID, caller, model string) (AIUsageTotals, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[integrationID]
	if state == nil {
		return AIUsageTotals{}, false
	}
	row, ok := state.aiUsage[memoryAIUsageKey{caller: caller, model: model}]
	return row, ok
}

var _ CircuitBreakerBackend = (*MemoryBackend)(nil)
var _ RetryBudgetBackend = (*MemoryBackend)(nil)
var _ ProviderCooldownBackend = (*MemoryBackend)(nil)
var _ AIUsageBackend = (*MemoryBackend)(nil)
var _ AIBudgetAlertRecorder = (*MemoryBackend)(nil)
