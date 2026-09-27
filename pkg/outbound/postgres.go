package outbound

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
		       max_in_flight, request_timeout_ms, max_retries, owner_kind
		  FROM outbound_integrations
		 WHERE id = $1
		 FOR SHARE`, integrationID).
		Scan(&storedDailyRequestLimit, &requestPolicy.RatePerSecond, &requestPolicy.Burst,
			&requestPolicy.MaxInFlight, &requestPolicy.RequestTimeoutMS, &requestPolicy.MaxRetries, &ownerKind)
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
	if err := tx.QueryRow(ctx, `
		SELECT tokens, last_refill, daily_usage_date, daily_request_count
		FROM outbound_admission_state
		WHERE integration_id = $1
		FOR UPDATE`, integrationID).Scan(&tokens, &lastRefill, &usageDate, &dailyRequestCount); err != nil {
		return Decision{}, err
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
		SET tokens = $2, last_refill = $3, daily_usage_date = $4, daily_request_count = $5
		WHERE integration_id = $1`, integrationID, tokens, lastRefill, today, dailyRequestCount); err != nil {
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
	return Decision{Granted: true, LeaseID: leaseID.String(), RequestTimeout: ttl}, nil
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
