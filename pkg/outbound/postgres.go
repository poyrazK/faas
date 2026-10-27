package outbound

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

// PostgresBackend provides the cross-process admission boundary. The
// integration row is locked for the short transaction, so twenty gateway
// processes still consume one token bucket and one lease set.
type PostgresBackend struct {
	pool *pgxpool.Pool
}

func NewPostgresBackend(pool *pgxpool.Pool) (*PostgresBackend, error) {
	if pool == nil {
		return nil, fmt.Errorf("outbound postgres pool is required")
	}
	return &PostgresBackend{pool: pool}, nil
}

// Admit decides one call in three pipelined round trips inside one
// transaction (ADR-1120): the policy reads, then the locked state reads, then
// the writes. The integration's admission-state row is locked from the second
// round trip to the commit, so every gateway process still consumes one token
// bucket and one lease set.
func (b *PostgresBackend) Admit(ctx context.Context, spec AdmissionSpec) (Decision, error) {
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
	if err := validateAIAdmissionSpec(spec); err != nil {
		return Decision{}, err
	}
	if !api.ValidOutboundCircuitBreakerPolicy(spec.CircuitBreakerFailureThreshold, spec.CircuitBreakerOpenSeconds) {
		return Decision{}, fmt.Errorf("%w: circuit-breaker policy is invalid", ErrInvalidIntegration)
	}
	if spec.RetryBudgetPerMinute < 0 || spec.RetryBudgetPerMinute > api.MaxOutboundRetryBudgetPerMinute {
		return Decision{}, fmt.Errorf("%w: retry-budget policy is invalid", ErrInvalidIntegration)
	}
	integrationID, err := uuid.Parse(spec.IntegrationID)
	if err != nil {
		return Decision{}, fmt.Errorf("%w: integration id must be a UUID", ErrInvalidIntegration)
	}
	var bindingAppID uuid.UUID
	if spec.BindingAppID != "" {
		if bindingAppID, err = uuid.Parse(spec.BindingAppID); err != nil {
			return Decision{}, fmt.Errorf("%w: binding app id must be a UUID", ErrInvalidIntegration)
		}
	}
	ttl := spec.LeaseTTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return Decision{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	policy, err := readAdmissionPolicy(ctx, tx, integrationID, bindingAppID, spec.AgentRun)
	if err != nil {
		return Decision{}, err
	}
	if policy.notAttached {
		return Decision{Reason: ReasonAppNotAttached}, nil
	}
	var bindingDailyRequestLimit *int64
	if policy.bindingDailyRequestLimit > 0 {
		maximum, ok := api.OutboundRequestsPerDayMaxForPlan(api.Plan(policy.plan))
		if !ok {
			return Decision{}, fmt.Errorf("%w: account plan has no outbound request budget ceiling", ErrInvalidIntegration)
		}
		limit := min(policy.bindingDailyRequestLimit, maximum)
		bindingDailyRequestLimit = &limit
	}
	var dailyRequestLimit *int64
	if policy.dailyRequestLimit > 0 {
		maximum, ok := api.OutboundRequestsPerDayMaxForPlan(api.Plan(policy.plan))
		if !ok {
			return Decision{}, fmt.Errorf("%w: account plan has no outbound request budget ceiling", ErrInvalidIntegration)
		}
		limit := min(policy.dailyRequestLimit, maximum)
		dailyRequestLimit = &limit
	}
	if policy.ownerKind == "customer" {
		requestPolicy, ok := api.EffectiveOutboundRequestPolicyForPlan(api.Plan(policy.plan), policy.requestPolicy)
		if !ok {
			return Decision{}, fmt.Errorf("%w: customer outbound request policy is invalid", ErrInvalidIntegration)
		}
		spec.RatePerSecond = requestPolicy.RatePerSecond
		spec.Burst = requestPolicy.Burst
		spec.MaxInFlight = requestPolicy.MaxInFlight
		spec.CircuitBreakerFailureThreshold = requestPolicy.CircuitBreakerFailureThreshold
		spec.CircuitBreakerOpenSeconds = requestPolicy.CircuitBreakerOpenSeconds
		spec.RetryBudgetPerMinute = requestPolicy.RetryBudgetPerMinute
		ttl = time.Duration(requestPolicy.RequestTimeoutMS) * time.Millisecond
	}
	now := policy.now
	utcNow := now.UTC()
	today := time.Date(utcNow.Year(), utcNow.Month(), utcNow.Day(), 0, 0, 0, 0, time.UTC)
	thisMonth := time.Date(utcNow.Year(), utcNow.Month(), 1, 0, 0, 0, 0, time.UTC)
	userLimited := spec.AIEndUser != "" && (policy.userTokenLimit > 0 || policy.userCostLimit > 0)
	state, err := lockAdmissionState(ctx, tx, admissionLock{
		integrationID: integrationID, now: now, burst: spec.Burst, bindingAppID: bindingAppID,
		today: today, minuteWindow: utcNow.Truncate(time.Minute), endUser: spec.AIEndUser, userLimited: userLimited,
		tags: spec.AITags, traceID: tracedForLimits(spec.AITraceID, policy.traceLimits),
	})
	if err != nil {
		return Decision{}, err
	}
	if state.circuitPolicyThreshold != spec.CircuitBreakerFailureThreshold || state.circuitPolicyOpenSeconds != spec.CircuitBreakerOpenSeconds {
		state.circuitFailureCount = 0
		state.circuitOpenUntil = pgtype.Timestamptz{}
		state.circuitProbeLeaseID = pgtype.UUID{}
		state.circuitPolicyThreshold = spec.CircuitBreakerFailureThreshold
		state.circuitPolicyOpenSeconds = spec.CircuitBreakerOpenSeconds
	}
	if state.circuitProbeLeaseID.Valid && !state.probeLeaseLive {
		// Expired admission-lease deletion clears the foreign key in SQL, but
		// the state row was read before that deletion.
		state.circuitProbeLeaseID = pgtype.UUID{}
	}
	if state.retryBudgetPolicyPerMinute != spec.RetryBudgetPerMinute {
		state.retryBudgetTokens = float64(spec.RetryBudgetPerMinute)
		state.retryBudgetRefilledAt = now
		state.retryBudgetPolicyPerMinute = spec.RetryBudgetPerMinute
	} else if elapsed := now.Sub(state.retryBudgetRefilledAt).Seconds(); elapsed > 0 {
		state.retryBudgetTokens += elapsed * float64(spec.RetryBudgetPerMinute) / 60
		state.retryBudgetRefilledAt = now
	}
	if state.retryBudgetTokens > float64(spec.RetryBudgetPerMinute) {
		state.retryBudgetTokens = float64(spec.RetryBudgetPerMinute)
	}
	if !state.usageDate.UTC().Truncate(24 * time.Hour).Equal(today) {
		state.dailyRequestCount = 0
		state.dailyAITokenCount = 0
		state.dailyAICost = 0
	}
	if !state.usageMonth.UTC().Equal(thisMonth) {
		state.monthlyAICost = 0
	}
	if elapsed := now.Sub(state.lastRefill).Seconds(); elapsed > 0 {
		state.tokens += elapsed * spec.RatePerSecond
		state.lastRefill = now
	}
	if state.tokens > float64(spec.Burst) {
		state.tokens = float64(spec.Burst)
	}
	reserve := aiReservationFromSpec(spec)
	reason, retry := admissionRejection(utcNow, today, ttl, spec, state, admissionLimits{
		daily: dailyRequestLimit, bindingDaily: bindingDailyRequestLimit, ai: aiBudgetLimits{
			dailyTokens: positiveLimit(policy.dailyAITokenLimit), bindingTokens: positiveLimit(policy.bindingAITokenLimit),
			dailyCost: positiveLimit(policy.dailyAICostLimit), monthlyCost: positiveLimit(policy.monthlyAICostLimit),
		},
		bindingRequestsPerMinute: positiveLimit(policy.bindingRequestsPerMinute),
		bindingTokensPerMinute:   positiveLimit(policy.bindingTokensPerMinute),
	}, userLimited, policy, reserve, bindingAppID != uuid.Nil)
	if reason == "" && spec.AITraceID != "" {
		// ADR 20261020202035: the trace's limits; a trace never resets.
		state.trace.named = spec.AITraceNamed
		reason = aiTraceRejection(state.trace, policy.traceLimits, reserve)
	}
	write := admissionWrite{integrationID: integrationID, today: today, thisMonth: thisMonth, state: state}
	if reason != "" {
		// Persist refill progress even when the request is rejected. This
		// prevents repeated callers from repeatedly receiving a stale
		// Retry-After value.
		if err := writeAdmission(ctx, tx, write); err != nil {
			return Decision{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Decision{}, err
		}
		return Decision{RetryAfter: retry, Reason: reason, RequestTimeout: ttl}, nil
	}
	write.state.tokens--
	write.state.dailyRequestCount++
	write.lease = &admissionLease{id: uuid.New(), expiresAt: now.Add(ttl), bindingAppID: bindingAppID,
		reserve: reserve, endUser: spec.AIEndUser, tags: spec.AITags, traceID: spec.AITraceID}
	if err := writeAdmission(ctx, tx, write); err != nil {
		return Decision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Decision{}, err
	}
	decision := Decision{
		Granted: true, LeaseID: write.lease.id.String(), RequestTimeout: ttl,
		CircuitBreakerFailureThreshold: spec.CircuitBreakerFailureThreshold,
		CircuitBreakerOpenSeconds:      spec.CircuitBreakerOpenSeconds,
		RetryBudgetPerMinute:           spec.RetryBudgetPerMinute,
		CircuitClosed:                  spec.CircuitBreakerFailureThreshold > 0 && !state.circuitOpenUntil.Valid,
	}
	if state.providerCooldownRevision == spec.PolicyRevision {
		gate := ProviderCooldownDecision{Allowed: true}
		if state.providerCooldownUntil.Valid && state.providerCooldownUntil.Time.After(now) {
			gate = ProviderCooldownDecision{RetryAfter: state.providerCooldownUntil.Time.Sub(now)}
		}
		decision.ProviderGate = &gate
	}
	return decision, nil
}

// admissionPolicy is what admission reads before it locks the integration's
// state row: the database clock, the account plan, and the stored limits of
// the integration and of the app's binding.
type admissionPolicy struct {
	now                                                     time.Time
	plan, ownerKind                                         string
	notAttached                                             bool
	bindingDailyRequestLimit, bindingAITokenLimit           int64
	bindingRequestsPerMinute, bindingTokensPerMinute        int64
	dailyRequestLimit                                       int64
	dailyAITokenLimit, dailyAICostLimit, monthlyAICostLimit int64
	userTokenLimit, userCostLimit                           int64
	requestPolicy                                           api.OutboundRequestPolicy
	// traceLimits are the integration's stored per-trace limits
	// (ADR 20261020202035).
	traceLimits aiTraceLimits
}

// readAdmissionPolicy reads the policy in one round trip. It locks the
// account first, then an explicit binding, then the integration, matching
// binding-policy writes and preventing a stale resolver snapshot from
// bypassing a completed update.
func readAdmissionPolicy(ctx context.Context, tx pgx.Tx, integrationID, bindingAppID uuid.UUID, agentRun bool) (admissionPolicy, error) {
	batch := &pgx.Batch{}
	batch.Queue(`
		SELECT now(), account.plan, integration.owner_kind
		  FROM accounts account
		  JOIN outbound_integrations integration ON integration.account_id = account.id
		 WHERE integration.id = $1
		 FOR SHARE OF account`, integrationID)
	if bindingAppID != uuid.Nil {
		batch.Queue(`
			SELECT COALESCE(daily_request_limit, 0), COALESCE(ai_daily_token_limit, 0),
			       COALESCE(ai_requests_per_minute, 0), COALESCE(ai_tokens_per_minute, 0)
			  FROM outbound_app_bindings
			 WHERE integration_id = $1 AND app_id = $2
			 FOR SHARE`, integrationID, bindingAppID)
	}
	batch.Queue(`
		SELECT COALESCE(daily_request_limit, 0), rate_per_second, burst,
		       max_in_flight, request_timeout_ms, max_retries, response_cache_ttl_seconds,
		       circuit_breaker_failure_threshold, circuit_breaker_open_seconds, retry_budget_per_minute, owner_kind,
		       COALESCE(ai_daily_token_limit, 0),
		       COALESCE(ai_daily_cost_limit_millicents, 0), COALESCE(ai_monthly_cost_limit_millicents, 0),
		       COALESCE(ai_user_daily_token_limit, 0), COALESCE(ai_user_daily_cost_limit_millicents, 0),
		       COALESCE(ai_trace_max_calls, 0), COALESCE(ai_trace_max_cost_millicents, 0),
		       COALESCE(ai_trace_max_repeated_tool_calls, 0)
		  FROM outbound_integrations
		 WHERE id = $1
		 FOR SHARE`, integrationID)
	results := tx.SendBatch(ctx, batch)
	policy, err := scanAdmissionPolicy(results, bindingAppID, agentRun)
	if closeErr := results.Close(); err == nil {
		err = closeErr
	}
	return policy, err
}

func scanAdmissionPolicy(results pgx.BatchResults, bindingAppID uuid.UUID, agentRun bool) (admissionPolicy, error) {
	var policy admissionPolicy
	if err := results.QueryRow().Scan(&policy.now, &policy.plan, &policy.ownerKind); err != nil {
		return policy, err
	}
	// An app's call always names its binding, whose budget it must not
	// skip. An agent run's call has no binding: its run grant authorized it.
	if policy.ownerKind == "customer" && bindingAppID == uuid.Nil && !agentRun {
		return policy, fmt.Errorf("%w: customer integration admission requires a binding app ID", ErrInvalidIntegration)
	}
	if bindingAppID != uuid.Nil {
		err := results.QueryRow().Scan(&policy.bindingDailyRequestLimit, &policy.bindingAITokenLimit,
			&policy.bindingRequestsPerMinute, &policy.bindingTokensPerMinute)
		if errors.Is(err, pgx.ErrNoRows) {
			policy.notAttached = true
			return policy, nil
		}
		if err != nil {
			return policy, err
		}
	}
	var ownerKind string
	request := &policy.requestPolicy
	if err := results.QueryRow().Scan(&policy.dailyRequestLimit, &request.RatePerSecond, &request.Burst,
		&request.MaxInFlight, &request.RequestTimeoutMS, &request.MaxRetries,
		&request.ResponseCacheTTLSeconds, &request.CircuitBreakerFailureThreshold,
		&request.CircuitBreakerOpenSeconds, &request.RetryBudgetPerMinute, &ownerKind,
		&policy.dailyAITokenLimit, &policy.dailyAICostLimit, &policy.monthlyAICostLimit,
		&policy.userTokenLimit, &policy.userCostLimit,
		&policy.traceLimits.calls, &policy.traceLimits.cost, &policy.traceLimits.repeats); err != nil {
		return policy, err
	}
	if ownerKind != policy.ownerKind {
		return policy, fmt.Errorf("%w: outbound integration owner changed during admission", ErrInvalidIntegration)
	}
	return policy, nil
}

// admissionLock names what the locked read needs.
type admissionLock struct {
	integrationID uuid.UUID
	now           time.Time
	burst         int
	bindingAppID  uuid.UUID
	today         time.Time
	minuteWindow  time.Time
	endUser       string
	userLimited   bool
	// tags are the call's usage tags, whose limits admission reads
	// (ADR-1436).
	tags []string
	// traceID is the call's trace when the integration limits traces
	// (ADR 20261020202035).
	traceID string
}

// lockedAdmission is the integration's admission state as admission holds
// it locked, with the app's binding counters, the live leases and, for an
// end user with limits, that user's day.
type lockedAdmission struct {
	tokens                                              float64
	lastRefill, usageDate, usageMonth                   time.Time
	dailyRequestCount, dailyAITokenCount, dailyAICost   int64
	monthlyAICost                                       int64
	circuitFailureCount                                 int
	circuitOpenUntil                                    pgtype.Timestamptz
	circuitProbeLeaseID                                 pgtype.UUID
	circuitPolicyThreshold, circuitPolicyOpenSeconds    int
	retryBudgetTokens                                   float64
	retryBudgetRefilledAt                               time.Time
	retryBudgetPolicyPerMinute                          int
	providerCooldownUntil                               pgtype.Timestamptz
	providerCooldownRevision                            int64
	bindingDailyRequestCount, bindingAITokenCount       int64
	minute                                              aiMinuteState
	inFlight                                            int
	earliestExpiry                                      time.Time
	inflightTokens, inflightBindingTokens, inflightCost int64
	probeLeaseLive                                      bool
	user                                                aiBudgetState
	tags                                                []aiTagState
	trace                                               aiTraceState
}

// lockAdmissionState creates the state row on first use, locks it, and reads
// everything admission decides on in one round trip. Expired leases are
// deleted on the way; the lease totals count only live ones (ADR-991).
func lockAdmissionState(ctx context.Context, tx pgx.Tx, lock admissionLock) (lockedAdmission, error) {
	batch := &pgx.Batch{}
	batch.Queue(`
		INSERT INTO outbound_admission_state (integration_id, tokens, last_refill)
		VALUES ($1, $2, $3) ON CONFLICT (integration_id) DO NOTHING`, lock.integrationID, float64(lock.burst), lock.now)
	batch.Queue(`
		SELECT tokens, last_refill, daily_usage_date, daily_request_count,
		       circuit_failure_count, circuit_open_until, circuit_probe_lease_id,
		       circuit_policy_failure_threshold, circuit_policy_open_seconds,
		       retry_budget_tokens, retry_budget_refilled_at, retry_budget_policy_per_minute,
		       daily_ai_token_count, daily_ai_cost_millicents, monthly_usage_month, monthly_ai_cost_millicents,
		       provider_cooldown_until, provider_cooldown_policy_revision
		  FROM outbound_admission_state
		 WHERE integration_id = $1
		 FOR UPDATE`, lock.integrationID)
	if lock.bindingAppID != uuid.Nil {
		batch.Queue(`
			INSERT INTO outbound_app_binding_usage (integration_id, app_id, daily_usage_date, daily_request_count, minute_window_start)
			VALUES ($1, $2, $3, 0, $4)
			ON CONFLICT (integration_id, app_id) DO UPDATE
			   SET daily_usage_date = EXCLUDED.daily_usage_date,
			       daily_request_count = CASE
			           WHEN outbound_app_binding_usage.daily_usage_date <> EXCLUDED.daily_usage_date THEN 0
			           ELSE outbound_app_binding_usage.daily_request_count
			       END,
			       daily_ai_token_count = CASE
			           WHEN outbound_app_binding_usage.daily_usage_date <> EXCLUDED.daily_usage_date THEN 0
			           ELSE outbound_app_binding_usage.daily_ai_token_count
			       END,
			       minute_request_count = CASE
			           WHEN outbound_app_binding_usage.minute_window_start IS DISTINCT FROM EXCLUDED.minute_window_start THEN 0
			           ELSE outbound_app_binding_usage.minute_request_count
			       END,
			       minute_ai_token_count = CASE
			           WHEN outbound_app_binding_usage.minute_window_start IS DISTINCT FROM EXCLUDED.minute_window_start THEN 0
			           ELSE outbound_app_binding_usage.minute_ai_token_count
			       END,
			       minute_window_start = EXCLUDED.minute_window_start
			RETURNING daily_request_count, daily_ai_token_count, minute_request_count, minute_ai_token_count`,
			lock.integrationID, lock.bindingAppID, lock.today, lock.minuteWindow)
	}
	// The DELETE runs whether or not the outer query reads it; the outer
	// query sees the leases as of the statement start, so it filters out the
	// expired ones itself.
	batch.Queue(`
		WITH expired AS (
		    DELETE FROM outbound_admission_leases WHERE integration_id = $1 AND expires_at <= $2 RETURNING lease_id)
		SELECT count(*)::int, COALESCE(min(expires_at), $2),
		       -- ADR-1431: a created batch's reservation counts until it settles.
		       (COALESCE(sum(ai_reserved_tokens), 0) +
		        (SELECT COALESCE(sum(reserved_tokens), 0) FROM outbound_ai_batches
		          WHERE integration_id = $1 AND state <> 'settled'))::bigint,
		       (COALESCE(sum(ai_reserved_tokens) FILTER (WHERE binding_app_id = $3), 0) +
		        (SELECT COALESCE(sum(reserved_tokens), 0) FROM outbound_ai_batches
		          WHERE integration_id = $1 AND state <> 'settled' AND binding_app_id = $3))::bigint,
		       (COALESCE(sum(ai_reserved_cost_millicents), 0) +
		        (SELECT COALESCE(sum(reserved_cost_millicents), 0) FROM outbound_ai_batches
		          WHERE integration_id = $1 AND state <> 'settled'))::bigint,
		       COALESCE(bool_or(lease_id = (SELECT circuit_probe_lease_id FROM outbound_admission_state
		                                     WHERE integration_id = $1)), false)
		  FROM outbound_admission_leases
		 WHERE integration_id = $1 AND expires_at > $2`, lock.integrationID, lock.now, lock.bindingAppID)
	if lock.userLimited {
		queueAIUserState(batch, lock.integrationID, lock.today, lock.endUser)
	}
	if len(lock.tags) > 0 {
		queueAITagState(batch, lock.integrationID, lock.today, lock.now, lock.tags)
	}
	if lock.traceID != "" {
		queueAITraceState(batch, lock.integrationID, lock.today, lock.now, lock.traceID)
	}
	results := tx.SendBatch(ctx, batch)
	state, err := scanLockedAdmission(results, lock)
	if closeErr := results.Close(); err == nil {
		err = closeErr
	}
	return state, err
}

func scanLockedAdmission(results pgx.BatchResults, lock admissionLock) (lockedAdmission, error) {
	var state lockedAdmission
	if _, err := results.Exec(); err != nil {
		return state, err
	}
	if err := results.QueryRow().Scan(&state.tokens, &state.lastRefill, &state.usageDate, &state.dailyRequestCount,
		&state.circuitFailureCount, &state.circuitOpenUntil, &state.circuitProbeLeaseID,
		&state.circuitPolicyThreshold, &state.circuitPolicyOpenSeconds,
		&state.retryBudgetTokens, &state.retryBudgetRefilledAt, &state.retryBudgetPolicyPerMinute,
		&state.dailyAITokenCount, &state.dailyAICost, &state.usageMonth, &state.monthlyAICost,
		&state.providerCooldownUntil, &state.providerCooldownRevision); err != nil {
		return state, err
	}
	if lock.bindingAppID != uuid.Nil {
		if err := results.QueryRow().Scan(&state.bindingDailyRequestCount, &state.bindingAITokenCount,
			&state.minute.requests, &state.minute.tokens); err != nil {
			return state, err
		}
	}
	if err := results.QueryRow().Scan(&state.inFlight, &state.earliestExpiry, &state.inflightTokens,
		&state.inflightBindingTokens, &state.inflightCost, &state.probeLeaseLive); err != nil {
		return state, err
	}
	state.minute.inflightTokens = state.inflightBindingTokens
	if lock.userLimited {
		user, err := scanAIUserState(results)
		if err != nil {
			return state, err
		}
		state.user = user
	}
	if len(lock.tags) > 0 {
		tags, err := scanAITagState(results)
		if err != nil {
			return state, err
		}
		state.tags = tags
	}
	if lock.traceID != "" {
		trace, err := scanAITraceState(results)
		if err != nil {
			return state, err
		}
		state.trace = trace
	}
	return state, nil
}

// admissionLimits are the request and AI limits one admission enforces.
type admissionLimits struct {
	daily, bindingDaily                              *int64
	ai                                               aiBudgetLimits
	bindingRequestsPerMinute, bindingTokensPerMinute *int64
}

// admissionRejection returns the first limit that refuses the call and when
// to retry, or an empty reason when the call is admitted.
func admissionRejection(utcNow, today time.Time, ttl time.Duration, spec AdmissionSpec, state lockedAdmission,
	limits admissionLimits, userLimited bool, policy admissionPolicy, reserve aiReservation, bound bool) (string, time.Duration) {
	untilTomorrow := max(today.Add(24*time.Hour).Sub(utcNow), time.Millisecond)
	switch {
	case state.inFlight >= spec.MaxInFlight:
		retry := ttl
		if now := utcNow; state.earliestExpiry.After(now) && state.earliestExpiry.Sub(now) < retry {
			retry = state.earliestExpiry.Sub(now)
		}
		return ReasonConcurrency, max(retry, time.Millisecond)
	case state.tokens < 1:
		return ReasonRate, max(time.Duration((1-state.tokens)/spec.RatePerSecond*float64(time.Second)), time.Millisecond)
	case limits.daily != nil && state.dailyRequestCount >= *limits.daily:
		return ReasonDailyLimit, untilTomorrow
	case limits.bindingDaily != nil && state.bindingDailyRequestCount >= *limits.bindingDaily:
		return ReasonDailyLimit, untilTomorrow
	}
	// Tokens and cost settle after each response (ADR-988, ADR-990); live
	// reservations make the stored limits hard caps (ADR-991).
	ai := limits.ai
	if userLimited {
		// ADR-994: the end user's settled day and live reservations.
		ai.userTokens, ai.userCost = positiveLimit(policy.userTokenLimit), positiveLimit(policy.userCostLimit)
	}
	if reason, retry, limited := aiBudgetExhausted(utcNow, aiBudgetState{
		dailyTokens: state.dailyAITokenCount, bindingTokens: state.bindingAITokenCount,
		dailyCost: state.dailyAICost, monthlyCost: state.monthlyAICost,
		inflightTokens: state.inflightTokens, inflightBindingTokens: state.inflightBindingTokens, inflightCost: state.inflightCost,
		userTokens: state.user.userTokens, userCost: state.user.userCost,
		inflightUserTokens: state.user.inflightUserTokens, inflightUserCost: state.user.inflightUserCost,
		userUntracked: state.user.userUntracked,
	}, ai, reserve); limited {
		return reason, retry
	}
	if reason, retry, limited := aiTagBudgetExhausted(utcNow, state.tags, reserve); limited {
		return reason, retry
	}
	if bound {
		if reason, retry, limited := aiMinuteExhausted(utcNow, state.minute, limits.bindingRequestsPerMinute,
			limits.bindingTokensPerMinute, reserve); limited {
			return reason, retry
		}
	}
	return "", 0
}

// admissionLease is the lease a granted admission inserts.
type admissionLease struct {
	id           uuid.UUID
	expiresAt    time.Time
	bindingAppID uuid.UUID
	reserve      aiReservation
	endUser      string
	tags         []string
	traceID      string
}

// admissionWrite is the state an admission persists, and the lease it
// inserts when it grants the call.
type admissionWrite struct {
	integrationID    uuid.UUID
	today, thisMonth time.Time
	state            lockedAdmission
	lease            *admissionLease
}

// writeAdmission persists the state, and for a granted call its lease and
// the app's request counters, in one round trip.
func writeAdmission(ctx context.Context, tx pgx.Tx, write admissionWrite) error {
	state := write.state
	batch := &pgx.Batch{}
	batch.Queue(`UPDATE outbound_admission_state
		SET tokens = $2, last_refill = $3, daily_usage_date = $4, daily_request_count = $5,
		    circuit_failure_count = $6, circuit_open_until = $7, circuit_probe_lease_id = $8,
		    circuit_policy_failure_threshold = $9, circuit_policy_open_seconds = $10,
		    retry_budget_tokens = $11, retry_budget_refilled_at = $12, retry_budget_policy_per_minute = $13,
		    daily_ai_token_count = $14, daily_ai_cost_millicents = $15,
		    monthly_usage_month = $16, monthly_ai_cost_millicents = $17
		WHERE integration_id = $1`, write.integrationID, state.tokens, state.lastRefill, write.today, state.dailyRequestCount,
		state.circuitFailureCount, state.circuitOpenUntil, state.circuitProbeLeaseID,
		state.circuitPolicyThreshold, state.circuitPolicyOpenSeconds,
		state.retryBudgetTokens, state.retryBudgetRefilledAt, state.retryBudgetPolicyPerMinute,
		state.dailyAITokenCount, state.dailyAICost, write.thisMonth, state.monthlyAICost)
	lease := write.lease
	if lease != nil {
		var leaseBindingApp *uuid.UUID
		if lease.bindingAppID != uuid.Nil {
			leaseBindingApp = &lease.bindingAppID
		}
		batch.Queue(`
			INSERT INTO outbound_admission_leases
			    (lease_id, integration_id, expires_at, binding_app_id, ai_reserved_tokens, ai_reserved_cost_millicents, ai_end_user,
			     ai_tags, ai_trace_id)
			VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, NULLIF($9, ''))`, lease.id, write.integrationID, lease.expiresAt,
			leaseBindingApp, lease.reserve.tokens, lease.reserve.cost, lease.endUser, nilIfEmpty(lease.tags), lease.traceID)
		if lease.bindingAppID != uuid.Nil {
			batch.Queue(`UPDATE outbound_app_binding_usage
				SET daily_request_count = daily_request_count + 1, minute_request_count = minute_request_count + 1
				WHERE integration_id = $1 AND app_id = $2 AND daily_usage_date = $3`, write.integrationID, lease.bindingAppID, write.today)
		}
	}
	results := tx.SendBatch(ctx, batch)
	err := execAdmissionWrites(results, lease)
	if closeErr := results.Close(); err == nil {
		err = closeErr
	}
	return err
}

func execAdmissionWrites(results pgx.BatchResults, lease *admissionLease) error {
	if _, err := results.Exec(); err != nil {
		return err
	}
	if lease == nil {
		return nil
	}
	if _, err := results.Exec(); err != nil {
		return err
	}
	if lease.bindingAppID != uuid.Nil {
		tag, err := results.Exec()
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("outbound binding usage row disappeared during admission")
		}
	}
	return nil
}

func (b *PostgresBackend) ConsumeRetryToken(ctx context.Context, integrationID string, retryBudgetPerMinute int) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if retryBudgetPerMinute < 1 || retryBudgetPerMinute > api.MaxOutboundRetryBudgetPerMinute {
		return false, fmt.Errorf("%w: invalid retry-budget request", ErrInvalidIntegration)
	}
	integrationUUID, err := uuid.Parse(integrationID)
	if err != nil {
		return false, fmt.Errorf("%w: integration id must be a UUID", ErrInvalidIntegration)
	}
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
		return false, err
	}
	var tokens float64
	var refilledAt time.Time
	var policyPerMinute int
	err = tx.QueryRow(ctx, `
		SELECT retry_budget_tokens, retry_budget_refilled_at, retry_budget_policy_per_minute
		  FROM outbound_admission_state
		 WHERE integration_id = $1
		 FOR UPDATE`, integrationUUID).Scan(&tokens, &refilledAt, &policyPerMinute)
	if err != nil {
		return false, err
	}
	// The current admission may have captured an older policy. Never let it
	// spend from a bucket after a concurrent policy update resets that bucket.
	if policyPerMinute != retryBudgetPerMinute {
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		return false, nil
	}
	if elapsed := now.Sub(refilledAt).Seconds(); elapsed > 0 {
		tokens += elapsed * float64(retryBudgetPerMinute) / 60
		refilledAt = now
	}
	if tokens > float64(retryBudgetPerMinute) {
		tokens = float64(retryBudgetPerMinute)
	}
	allowed := tokens >= 1
	if allowed {
		tokens--
	}
	if _, err := tx.Exec(ctx, `
		UPDATE outbound_admission_state
		   SET retry_budget_tokens = $2, retry_budget_refilled_at = $3
		 WHERE integration_id = $1`, integrationUUID, tokens, refilledAt); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return allowed, nil
}

func (b *PostgresBackend) AllowProviderRequest(ctx context.Context, integrationID string, policyRevision int64) (ProviderCooldownDecision, error) {
	if err := ctx.Err(); err != nil {
		return ProviderCooldownDecision{}, err
	}
	integrationUUID, err := uuid.Parse(integrationID)
	if err != nil {
		return ProviderCooldownDecision{}, fmt.Errorf("%w: integration id must be a UUID", ErrInvalidIntegration)
	}
	for range 3 {
		var now time.Time
		var cooldownUntil pgtype.Timestamptz
		var storedRevision int64
		if err := b.pool.QueryRow(ctx, `
			SELECT now(), provider_cooldown_until, provider_cooldown_policy_revision
			  FROM outbound_admission_state
			 WHERE integration_id = $1`, integrationUUID).Scan(&now, &cooldownUntil, &storedRevision); err != nil {
			return ProviderCooldownDecision{}, err
		}
		if storedRevision == policyRevision {
			if cooldownUntil.Valid && cooldownUntil.Time.After(now) {
				return ProviderCooldownDecision{RetryAfter: cooldownUntil.Time.Sub(now)}, nil
			}
			return ProviderCooldownDecision{Allowed: true}, nil
		}
		// Policy revisions change rarely. Only lock/update the shared row when
		// resetting stale cooldown state; ordinary cache misses remain readers.
		err := b.pool.QueryRow(ctx, `
			UPDATE outbound_admission_state
			   SET provider_cooldown_until = NULL,
			       provider_cooldown_policy_revision = $2
			 WHERE integration_id = $1 AND provider_cooldown_policy_revision = $3
			RETURNING now(), provider_cooldown_until`, integrationUUID, policyRevision, storedRevision).Scan(&now, &cooldownUntil)
		if err == nil {
			return ProviderCooldownDecision{Allowed: true}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return ProviderCooldownDecision{}, err
		}
	}
	return ProviderCooldownDecision{}, fmt.Errorf("outbound provider cooldown policy changed repeatedly during admission")
}

func (b *PostgresBackend) RecordProviderCooldown(ctx context.Context, integrationID string, policyRevision int64, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay <= 0 || delay > maxProviderCooldown {
		return fmt.Errorf("%w: provider cooldown is outside the supported range", ErrInvalidIntegration)
	}
	integrationUUID, err := uuid.Parse(integrationID)
	if err != nil {
		return fmt.Errorf("%w: integration id must be a UUID", ErrInvalidIntegration)
	}
	result, err := b.pool.Exec(ctx, `
		UPDATE outbound_admission_state
		   SET provider_cooldown_until = CASE
		       WHEN provider_cooldown_policy_revision = $2 THEN GREATEST(
		           COALESCE(provider_cooldown_until, '-infinity'::timestamptz),
		           now() + ($3::bigint * interval '1 microsecond'))
		       ELSE now() + ($3::bigint * interval '1 microsecond')
		   END,
		       provider_cooldown_policy_revision = $2
		 WHERE integration_id = $1`, integrationUUID, policyRevision, delay.Microseconds())
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("outbound admission state is missing")
	}
	return nil
}

func (b *PostgresBackend) AllowCircuit(ctx context.Context, integrationID, leaseID string, threshold, openSeconds int) (CircuitBreakerDecision, error) {
	if err := ctx.Err(); err != nil {
		return CircuitBreakerDecision{}, err
	}
	if !api.ValidOutboundCircuitBreakerPolicy(threshold, openSeconds) {
		return CircuitBreakerDecision{}, fmt.Errorf("%w: circuit-breaker policy is invalid", ErrInvalidIntegration)
	}
	integrationUUID, err := uuid.Parse(integrationID)
	if err != nil {
		return CircuitBreakerDecision{}, fmt.Errorf("%w: integration id must be a UUID", ErrInvalidIntegration)
	}
	leaseUUID, err := uuid.Parse(leaseID)
	if err != nil {
		return CircuitBreakerDecision{}, fmt.Errorf("invalid outbound lease id: %w", err)
	}
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return CircuitBreakerDecision{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
		return CircuitBreakerDecision{}, err
	}
	var failures, storedThreshold, storedOpenSeconds int
	var openUntil pgtype.Timestamptz
	var probeLease pgtype.UUID
	err = tx.QueryRow(ctx, `
		SELECT circuit_failure_count, circuit_open_until, circuit_probe_lease_id,
		       circuit_policy_failure_threshold, circuit_policy_open_seconds
		  FROM outbound_admission_state
		 WHERE integration_id = $1
		 FOR UPDATE`, integrationUUID).
		Scan(&failures, &openUntil, &probeLease, &storedThreshold, &storedOpenSeconds)
	if err != nil {
		return CircuitBreakerDecision{}, err
	}
	if storedThreshold != threshold || storedOpenSeconds != openSeconds {
		return CircuitBreakerDecision{}, nil
	}
	if threshold == 0 || !openUntil.Valid {
		if err := tx.Commit(ctx); err != nil {
			return CircuitBreakerDecision{}, err
		}
		return CircuitBreakerDecision{Allowed: true}, nil
	}
	if openUntil.Time.After(now) {
		retry := openUntil.Time.Sub(now)
		if err := tx.Commit(ctx); err != nil {
			return CircuitBreakerDecision{}, err
		}
		return CircuitBreakerDecision{RetryAfter: retry}, nil
	}
	if probeLease.Valid {
		var probeExpiresAt time.Time
		probeErr := tx.QueryRow(ctx, `
			SELECT expires_at FROM outbound_admission_leases
			 WHERE integration_id = $1 AND lease_id = $2`, integrationUUID, uuid.UUID(probeLease.Bytes)).Scan(&probeExpiresAt)
		if probeErr == nil && probeExpiresAt.After(now) {
			retry := probeExpiresAt.Sub(now)
			if retry < time.Millisecond {
				retry = time.Millisecond
			}
			if err := tx.Commit(ctx); err != nil {
				return CircuitBreakerDecision{}, err
			}
			return CircuitBreakerDecision{RetryAfter: retry}, nil
		}
		if probeErr != nil && !errors.Is(probeErr, pgx.ErrNoRows) {
			return CircuitBreakerDecision{}, probeErr
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE outbound_admission_state SET circuit_probe_lease_id = $2 WHERE integration_id = $1`, integrationUUID, leaseUUID); err != nil {
		return CircuitBreakerDecision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CircuitBreakerDecision{}, err
	}
	return CircuitBreakerDecision{Allowed: true, Probe: true}, nil
}

func (b *PostgresBackend) RecordCircuitOutcome(ctx context.Context, integrationID, leaseID string, threshold, openSeconds int, outcome CircuitBreakerOutcome) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !api.ValidOutboundCircuitBreakerPolicy(threshold, openSeconds) ||
		(outcome != CircuitOutcomeSuccess && outcome != CircuitOutcomeFailure && outcome != CircuitOutcomeNeutral) {
		return fmt.Errorf("%w: circuit-breaker outcome is invalid", ErrInvalidIntegration)
	}
	integrationUUID, err := uuid.Parse(integrationID)
	if err != nil {
		return fmt.Errorf("%w: integration id must be a UUID", ErrInvalidIntegration)
	}
	leaseUUID, err := uuid.Parse(leaseID)
	if err != nil {
		return fmt.Errorf("invalid outbound lease id: %w", err)
	}
	var failures, storedThreshold, storedOpenSeconds int
	var openUntil pgtype.Timestamptz
	var probeLease pgtype.UUID
	// ADR-1120: most outcomes change nothing (a success with no failure
	// streak, or any outcome while another call holds the breaker open), so
	// read the state without its lock first. Skipping places the outcome at
	// this read, which is a valid order for concurrent calls.
	err = b.pool.QueryRow(ctx, `
		SELECT circuit_failure_count, circuit_open_until, circuit_probe_lease_id,
		       circuit_policy_failure_threshold, circuit_policy_open_seconds
		  FROM outbound_admission_state
		 WHERE integration_id = $1`, integrationUUID).
		Scan(&failures, &openUntil, &probeLease, &storedThreshold, &storedOpenSeconds)
	if err != nil {
		return err
	}
	if circuitOutcomeChangesNothing(failures, openUntil, probeLease, storedThreshold, storedOpenSeconds,
		threshold, openSeconds, leaseUUID, outcome) {
		return nil
	}
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `
		SELECT circuit_failure_count, circuit_open_until, circuit_probe_lease_id,
		       circuit_policy_failure_threshold, circuit_policy_open_seconds
		  FROM outbound_admission_state
		 WHERE integration_id = $1
		 FOR UPDATE`, integrationUUID).
		Scan(&failures, &openUntil, &probeLease, &storedThreshold, &storedOpenSeconds)
	if err != nil {
		return err
	}
	// Ignore outcomes from in-flight requests that began under an older policy.
	if storedThreshold != threshold || storedOpenSeconds != openSeconds || threshold == 0 {
		return tx.Commit(ctx)
	}
	probe := probeLease.Valid && probeLease.Bytes == [16]byte(leaseUUID)
	if probe {
		probeLease = pgtype.UUID{}
		if outcome == CircuitOutcomeSuccess {
			failures = 0
			openUntil = pgtype.Timestamptz{}
		} else {
			// A failed or cancelled probe keeps the circuit open for a fresh
			// cool-down before another probe can be claimed.
			failures = 0
			openUntil = pgtype.Timestamptz{Time: now.Add(time.Duration(openSeconds) * time.Second), Valid: true}
		}
	} else {
		// An older in-flight request must not close or prolong a circuit after
		// another request has already tripped it.
		if openUntil.Valid {
			return tx.Commit(ctx)
		}
		switch outcome {
		case CircuitOutcomeSuccess:
			failures = 0
		case CircuitOutcomeFailure:
			failures++
			if failures >= threshold {
				failures = 0
				openUntil = pgtype.Timestamptz{Time: now.Add(time.Duration(openSeconds) * time.Second), Valid: true}
			}
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE outbound_admission_state
		   SET circuit_failure_count = $2, circuit_open_until = $3, circuit_probe_lease_id = $4
		 WHERE integration_id = $1`, integrationUUID, failures, openUntil, probeLease); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// circuitOutcomeChangesNothing reports whether recording the outcome would
// leave the breaker as it is: a stale policy, a disabled breaker, a call that
// is not the probe while the breaker is open, or a non-probe success or
// neutral outcome with no failure streak.
func circuitOutcomeChangesNothing(failures int, openUntil pgtype.Timestamptz, probeLease pgtype.UUID,
	storedThreshold, storedOpenSeconds, threshold, openSeconds int, lease uuid.UUID, outcome CircuitBreakerOutcome) bool {
	if storedThreshold != threshold || storedOpenSeconds != openSeconds || threshold == 0 {
		return true
	}
	if probeLease.Valid && probeLease.Bytes == [16]byte(lease) {
		return false
	}
	return openUntil.Valid || outcome == CircuitOutcomeNeutral || (outcome == CircuitOutcomeSuccess && failures == 0)
}

func (b *PostgresBackend) Release(ctx context.Context, integrationID, leaseID string) error {
	integrationUUID, err := uuid.Parse(integrationID)
	if err != nil {
		return fmt.Errorf("%w: integration id must be a UUID", ErrInvalidIntegration)
	}
	leaseUUID, err := uuid.Parse(leaseID)
	if err != nil {
		return fmt.Errorf("invalid outbound lease id: %w", err)
	}
	_, err = b.pool.Exec(ctx, `DELETE FROM outbound_admission_leases WHERE integration_id = $1 AND lease_id = $2`, integrationUUID, leaseUUID)
	return err
}

var _ Backend = (*PostgresBackend)(nil)
var _ CircuitBreakerBackend = (*PostgresBackend)(nil)
var _ ProviderCooldownBackend = (*PostgresBackend)(nil)
