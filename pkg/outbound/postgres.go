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
	ttl := spec.LeaseTTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return Decision{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
		return Decision{}, err
	}
	// Lock the account first. Explicitly bound admissions then lock their
	// binding before the integration row, matching binding-policy writes and
	// preventing a stale resolver snapshot from bypassing a completed update.
	var plan, ownerKind string
	var storedDailyRequestLimit int64
	var requestPolicy api.OutboundRequestPolicy
	err = tx.QueryRow(ctx, `
		SELECT account.plan
		  FROM accounts account
		  JOIN outbound_integrations integration ON integration.account_id = account.id
		 WHERE integration.id = $1
		 FOR SHARE OF account`, integrationID).Scan(&plan)
	if err != nil {
		return Decision{}, err
	}
	var observedOwnerKind string
	if err := tx.QueryRow(ctx, `SELECT owner_kind FROM outbound_integrations WHERE id = $1`, integrationID).Scan(&observedOwnerKind); err != nil {
		return Decision{}, err
	}
	var bindingAppID uuid.UUID
	var bindingDailyRequestLimit *int64
	if observedOwnerKind == "customer" && spec.BindingAppID == "" {
		return Decision{}, fmt.Errorf("%w: customer integration admission requires a binding app ID", ErrInvalidIntegration)
	}
	if spec.BindingAppID != "" {
		bindingAppID, err = uuid.Parse(spec.BindingAppID)
		if err != nil {
			return Decision{}, fmt.Errorf("%w: binding app id must be a UUID", ErrInvalidIntegration)
		}
		var storedBindingDailyRequestLimit int64
		err = tx.QueryRow(ctx, `
			SELECT COALESCE(daily_request_limit, 0)
			  FROM outbound_app_bindings
			 WHERE integration_id = $1 AND app_id = $2
			 FOR SHARE`, integrationID, bindingAppID).Scan(&storedBindingDailyRequestLimit)
		if errors.Is(err, pgx.ErrNoRows) {
			return Decision{Reason: ReasonAppNotAttached}, nil
		}
		if err != nil {
			return Decision{}, err
		}
		if storedBindingDailyRequestLimit > 0 {
			maximum, ok := api.OutboundRequestsPerDayMaxForPlan(api.Plan(plan))
			if !ok {
				return Decision{}, fmt.Errorf("%w: account plan has no outbound request budget ceiling", ErrInvalidIntegration)
			}
			if storedBindingDailyRequestLimit > maximum {
				storedBindingDailyRequestLimit = maximum
			}
			bindingDailyRequestLimit = &storedBindingDailyRequestLimit
		}
	}
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(daily_request_limit, 0), rate_per_second, burst,
		       max_in_flight, request_timeout_ms, max_retries, response_cache_ttl_seconds,
		       circuit_breaker_failure_threshold, circuit_breaker_open_seconds, retry_budget_per_minute, owner_kind
		  FROM outbound_integrations
		 WHERE id = $1
		 FOR SHARE`, integrationID).
		Scan(&storedDailyRequestLimit, &requestPolicy.RatePerSecond, &requestPolicy.Burst,
			&requestPolicy.MaxInFlight, &requestPolicy.RequestTimeoutMS, &requestPolicy.MaxRetries,
			&requestPolicy.ResponseCacheTTLSeconds, &requestPolicy.CircuitBreakerFailureThreshold,
			&requestPolicy.CircuitBreakerOpenSeconds, &requestPolicy.RetryBudgetPerMinute, &ownerKind)
	if err != nil {
		return Decision{}, err
	}
	if ownerKind != observedOwnerKind {
		return Decision{}, fmt.Errorf("%w: outbound integration owner changed during admission", ErrInvalidIntegration)
	}
	var dailyRequestLimit *int64
	if storedDailyRequestLimit > 0 {
		maximum, ok := api.OutboundRequestsPerDayMaxForPlan(api.Plan(plan))
		if !ok {
			return Decision{}, fmt.Errorf("%w: account plan has no outbound request budget ceiling", ErrInvalidIntegration)
		}
		if storedDailyRequestLimit > maximum {
			storedDailyRequestLimit = maximum
		}
		dailyRequestLimit = &storedDailyRequestLimit
	}
	if ownerKind == "customer" {
		requestPolicy, ok := api.EffectiveOutboundRequestPolicyForPlan(api.Plan(plan), requestPolicy)
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
	// The state row is created lazily. The lock below serializes all admissions
	// for this integration; leases are still deleted by expiry during admission.
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbound_admission_state (integration_id, tokens, last_refill)
		VALUES ($1, $2, $3) ON CONFLICT (integration_id) DO NOTHING`, integrationID, float64(spec.Burst), now); err != nil {
		return Decision{}, err
	}
	var tokens float64
	var lastRefill time.Time
	var usageDate time.Time
	var dailyRequestCount int64
	var circuitFailureCount int
	var circuitOpenUntil pgtype.Timestamptz
	var circuitProbeLeaseID pgtype.UUID
	var circuitPolicyThreshold, circuitPolicyOpenSeconds int
	var retryBudgetTokens float64
	var retryBudgetRefilledAt time.Time
	var retryBudgetPolicyPerMinute int
	if err := tx.QueryRow(ctx, `
		SELECT tokens, last_refill, daily_usage_date, daily_request_count,
		       circuit_failure_count, circuit_open_until, circuit_probe_lease_id,
		       circuit_policy_failure_threshold, circuit_policy_open_seconds,
		       retry_budget_tokens, retry_budget_refilled_at, retry_budget_policy_per_minute
		FROM outbound_admission_state
		WHERE integration_id = $1
		FOR UPDATE`, integrationID).Scan(&tokens, &lastRefill, &usageDate, &dailyRequestCount,
		&circuitFailureCount, &circuitOpenUntil, &circuitProbeLeaseID,
		&circuitPolicyThreshold, &circuitPolicyOpenSeconds,
		&retryBudgetTokens, &retryBudgetRefilledAt, &retryBudgetPolicyPerMinute); err != nil {
		return Decision{}, err
	}
	if circuitPolicyThreshold != spec.CircuitBreakerFailureThreshold || circuitPolicyOpenSeconds != spec.CircuitBreakerOpenSeconds {
		circuitFailureCount = 0
		circuitOpenUntil = pgtype.Timestamptz{}
		circuitProbeLeaseID = pgtype.UUID{}
		circuitPolicyThreshold = spec.CircuitBreakerFailureThreshold
		circuitPolicyOpenSeconds = spec.CircuitBreakerOpenSeconds
	}
	if retryBudgetPolicyPerMinute != spec.RetryBudgetPerMinute {
		retryBudgetTokens = float64(spec.RetryBudgetPerMinute)
		retryBudgetRefilledAt = now
		retryBudgetPolicyPerMinute = spec.RetryBudgetPerMinute
	} else if elapsed := now.Sub(retryBudgetRefilledAt).Seconds(); elapsed > 0 {
		retryBudgetTokens += elapsed * float64(spec.RetryBudgetPerMinute) / 60
		retryBudgetRefilledAt = now
	}
	if retryBudgetTokens > float64(spec.RetryBudgetPerMinute) {
		retryBudgetTokens = float64(spec.RetryBudgetPerMinute)
	}
	utcNow := now.UTC()
	today := time.Date(utcNow.Year(), utcNow.Month(), utcNow.Day(), 0, 0, 0, 0, time.UTC)
	if !usageDate.UTC().Truncate(24 * time.Hour).Equal(today) {
		usageDate = today
		dailyRequestCount = 0
	}
	var bindingDailyRequestCount int64
	if bindingAppID != uuid.Nil {
		if err := tx.QueryRow(ctx, `
			INSERT INTO outbound_app_binding_usage (integration_id, app_id, daily_usage_date, daily_request_count)
			VALUES ($1, $2, $3, 0)
			ON CONFLICT (integration_id, app_id) DO UPDATE
			   SET daily_usage_date = EXCLUDED.daily_usage_date,
			       daily_request_count = CASE
			           WHEN outbound_app_binding_usage.daily_usage_date <> EXCLUDED.daily_usage_date THEN 0
			           ELSE outbound_app_binding_usage.daily_request_count
			       END
			RETURNING daily_request_count`, integrationID, bindingAppID, today).Scan(&bindingDailyRequestCount); err != nil {
			return Decision{}, err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM outbound_admission_leases WHERE integration_id = $1 AND expires_at <= $2`, integrationID, now); err != nil {
		return Decision{}, err
	}
	if circuitProbeLeaseID.Valid {
		var probeExpiresAt time.Time
		probeErr := tx.QueryRow(ctx, `SELECT expires_at FROM outbound_admission_leases WHERE integration_id = $1 AND lease_id = $2`, integrationID, uuid.UUID(circuitProbeLeaseID.Bytes)).Scan(&probeExpiresAt)
		if errors.Is(probeErr, pgx.ErrNoRows) || (probeErr == nil && !probeExpiresAt.After(now)) {
			// Expired admission-lease deletion clears the foreign key in SQL, but
			// this transaction's earlier row snapshot still contains the ID.
			circuitProbeLeaseID = pgtype.UUID{}
		} else if probeErr != nil {
			return Decision{}, probeErr
		}
	}
	if elapsed := now.Sub(lastRefill).Seconds(); elapsed > 0 {
		tokens += elapsed * spec.RatePerSecond
		lastRefill = now
	}
	if tokens > float64(spec.Burst) {
		tokens = float64(spec.Burst)
	}
	// Persist refill progress even when the request is rejected. This prevents
	// repeated callers from repeatedly receiving a stale Retry-After value.
	if _, err := tx.Exec(ctx, `UPDATE outbound_admission_state
		SET tokens = $2, last_refill = $3, daily_usage_date = $4, daily_request_count = $5,
		    circuit_failure_count = $6, circuit_open_until = $7, circuit_probe_lease_id = $8,
		    circuit_policy_failure_threshold = $9, circuit_policy_open_seconds = $10,
		    retry_budget_tokens = $11, retry_budget_refilled_at = $12, retry_budget_policy_per_minute = $13
		WHERE integration_id = $1`, integrationID, tokens, lastRefill, today, dailyRequestCount,
		circuitFailureCount, circuitOpenUntil, circuitProbeLeaseID, circuitPolicyThreshold, circuitPolicyOpenSeconds,
		retryBudgetTokens, retryBudgetRefilledAt, retryBudgetPolicyPerMinute); err != nil {
		return Decision{}, err
	}
	var inFlight int
	var earliest time.Time
	if err := tx.QueryRow(ctx, `
		SELECT count(*)::int, COALESCE(min(expires_at), $2)
		FROM outbound_admission_leases WHERE integration_id = $1`, integrationID, now).Scan(&inFlight, &earliest); err != nil {
		return Decision{}, err
	}
	if inFlight >= spec.MaxInFlight {
		retry := ttl
		if earliest.After(now) && earliest.Sub(now) < retry {
			retry = earliest.Sub(now)
		}
		if retry < time.Millisecond {
			retry = time.Millisecond
		}
		if err := tx.Commit(ctx); err != nil {
			return Decision{}, err
		}
		return Decision{RetryAfter: retry, Reason: ReasonConcurrency, RequestTimeout: ttl}, nil
	}
	if tokens < 1 {
		retry := time.Duration((1 - tokens) / spec.RatePerSecond * float64(time.Second))
		if retry < time.Millisecond {
			retry = time.Millisecond
		}
		if err := tx.Commit(ctx); err != nil {
			return Decision{}, err
		}
		return Decision{RetryAfter: retry, Reason: ReasonRate, RequestTimeout: ttl}, nil
	}
	if dailyRequestLimit != nil && dailyRequestCount >= *dailyRequestLimit {
		retry := today.Add(24 * time.Hour).Sub(utcNow)
		if retry < time.Millisecond {
			retry = time.Millisecond
		}
		if err := tx.Commit(ctx); err != nil {
			return Decision{}, err
		}
		return Decision{RetryAfter: retry, Reason: ReasonDailyLimit, RequestTimeout: ttl}, nil
	}
	if bindingDailyRequestLimit != nil && bindingDailyRequestCount >= *bindingDailyRequestLimit {
		retry := today.Add(24 * time.Hour).Sub(utcNow)
		if retry < time.Millisecond {
			retry = time.Millisecond
		}
		if err := tx.Commit(ctx); err != nil {
			return Decision{}, err
		}
		return Decision{RetryAfter: retry, Reason: ReasonDailyLimit, RequestTimeout: ttl}, nil
	}
	tokens--
	dailyRequestCount++
	leaseID := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO outbound_admission_leases (lease_id, integration_id, expires_at) VALUES ($1, $2, $3)`, leaseID, integrationID, now.Add(ttl)); err != nil {
		return Decision{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE outbound_admission_state
		SET tokens = $2, last_refill = $3, daily_usage_date = $4, daily_request_count = $5
		WHERE integration_id = $1`, integrationID, tokens, lastRefill, today, dailyRequestCount); err != nil {
		return Decision{}, err
	}
	if bindingAppID != uuid.Nil {
		result, err := tx.Exec(ctx, `UPDATE outbound_app_binding_usage
			SET daily_request_count = daily_request_count + 1
			WHERE integration_id = $1 AND app_id = $2 AND daily_usage_date = $3`, integrationID, bindingAppID, today)
		if err != nil {
			return Decision{}, err
		}
		if result.RowsAffected() != 1 {
			return Decision{}, fmt.Errorf("outbound binding usage row disappeared during admission")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Decision{}, err
	}
	return Decision{
		Granted: true, LeaseID: leaseID.String(), RequestTimeout: ttl,
		CircuitBreakerFailureThreshold: spec.CircuitBreakerFailureThreshold,
		CircuitBreakerOpenSeconds:      spec.CircuitBreakerOpenSeconds,
		RetryBudgetPerMinute:           spec.RetryBudgetPerMinute,
	}, nil
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
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
		return err
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
