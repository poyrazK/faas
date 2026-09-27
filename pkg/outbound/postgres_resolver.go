package outbound

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

// PostgresResolver loads policies from the outbound integration tables. It is
// deliberately read-only; lifecycle/API code owns configuration mutations.
type PostgresResolver struct{ pool *pgxpool.Pool }

func NewPostgresResolver(pool *pgxpool.Pool) (*PostgresResolver, error) {
	if pool == nil {
		return nil, fmt.Errorf("outbound postgres pool is required")
	}
	return &PostgresResolver{pool: pool}, nil
}

func (r *PostgresResolver) Integration(ctx context.Context, id string) (Integration, error) {
	integrationID, err := uuid.Parse(id)
	if err != nil {
		return Integration{}, ErrIntegrationNotFound
	}
	var origin string
	var accountID uuid.UUID
	var plan string
	var providerAuthMode string
	var credentialSource string
	var ownerKind string
	var allowedMethods, allowedPathPrefixes []string
	var tokenHash []byte
	var rate float64
	var dailyRequestLimitValue int64
	var burst, maxInFlight, timeoutMS, maxRetries, responseCacheTTLSeconds int
	var circuitBreakerFailureThreshold, circuitBreakerOpenSeconds int
	var retryBudgetPerMinute int
	var enabled bool
	var policyUpdatedAt time.Time
	err = r.pool.QueryRow(ctx, `
		SELECT integration.account_id, account.plan, integration.origin, integration.token_hash,
		       integration.rate_per_second, integration.burst, integration.max_in_flight,
		       integration.request_timeout_ms, integration.max_retries, integration.response_cache_ttl_seconds,
		       integration.circuit_breaker_failure_threshold, integration.circuit_breaker_open_seconds,
		       integration.retry_budget_per_minute,
		       integration.updated_at, integration.enabled, integration.provider_auth_mode,
		       integration.credential_source, integration.allowed_methods,
		       integration.allowed_path_prefixes, integration.owner_kind,
		       COALESCE(integration.daily_request_limit, 0)
		  FROM outbound_integrations integration
		  JOIN accounts account ON account.id = integration.account_id
		 WHERE integration.id = $1`, integrationID).
		Scan(&accountID, &plan, &origin, &tokenHash, &rate, &burst, &maxInFlight, &timeoutMS, &maxRetries, &responseCacheTTLSeconds,
			&circuitBreakerFailureThreshold, &circuitBreakerOpenSeconds, &retryBudgetPerMinute, &policyUpdatedAt, &enabled, &providerAuthMode,
			&credentialSource, &allowedMethods, &allowedPathPrefixes, &ownerKind, &dailyRequestLimitValue)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Integration{}, ErrIntegrationNotFound
		}
		return Integration{}, err
	}
	if !enabled {
		return Integration{}, ErrIntegrationDisabled
	}
	u, err := url.Parse(origin)
	if err != nil {
		return Integration{}, fmt.Errorf("%w: invalid origin: %w", ErrInvalidIntegration, err)
	}
	if len(tokenHash) != sha256.Size {
		return Integration{}, fmt.Errorf("%w: token hash has invalid length", ErrInvalidIntegration)
	}
	var hash [sha256.Size]byte
	copy(hash[:], tokenHash)
	var dailyRequestLimit *int64
	if dailyRequestLimitValue > 0 {
		maxForPlan, ok := api.OutboundRequestsPerDayMaxForPlan(api.Plan(plan))
		if !ok {
			return Integration{}, fmt.Errorf("%w: account plan has no outbound request budget ceiling", ErrInvalidIntegration)
		}
		if dailyRequestLimitValue > maxForPlan {
			dailyRequestLimitValue = maxForPlan
		}
		dailyRequestLimit = &dailyRequestLimitValue
	}
	if ownerKind == IntegrationOwnerCustomer {
		policy, ok := api.EffectiveOutboundRequestPolicyForPlan(api.Plan(plan), api.OutboundRequestPolicy{
			RatePerSecond: rate, Burst: burst, MaxInFlight: maxInFlight, RequestTimeoutMS: timeoutMS,
			MaxRetries: maxRetries, ResponseCacheTTLSeconds: responseCacheTTLSeconds,
			CircuitBreakerFailureThreshold: circuitBreakerFailureThreshold, CircuitBreakerOpenSeconds: circuitBreakerOpenSeconds,
			RetryBudgetPerMinute: retryBudgetPerMinute,
		})
		if !ok {
			return Integration{}, fmt.Errorf("%w: customer outbound request policy is invalid", ErrInvalidIntegration)
		}
		rate, burst, maxInFlight, timeoutMS, maxRetries = policy.RatePerSecond, policy.Burst, policy.MaxInFlight, policy.RequestTimeoutMS, policy.MaxRetries
		responseCacheTTLSeconds = policy.ResponseCacheTTLSeconds
		circuitBreakerFailureThreshold = policy.CircuitBreakerFailureThreshold
		circuitBreakerOpenSeconds = policy.CircuitBreakerOpenSeconds
		retryBudgetPerMinute = policy.RetryBudgetPerMinute
	}
	rows, err := r.pool.Query(ctx, `
		SELECT attachment.app_id::text, NULL::text[], NULL::text[], true, NULL::bigint
		  FROM outbound_integration_apps attachment
		  JOIN apps app ON app.id = attachment.app_id
		 WHERE attachment.integration_id = $1 AND app.account_id = $3 AND app.status <> 'deleted'
		UNION ALL
		SELECT binding.app_id::text, binding.allowed_methods, binding.allowed_path_prefixes, false, binding.daily_request_limit
		  FROM outbound_app_bindings binding
		  JOIN apps app ON app.id = binding.app_id
		 WHERE binding.integration_id = $1 AND binding.account_id = $3
		   AND app.account_id = $3 AND app.status <> 'deleted'
		   AND $2 = 'managed'`, integrationID, providerAuthMode, accountID)
	if err != nil {
		return Integration{}, err
	}
	defer rows.Close()
	apps := make(map[string]struct{})
	operatorApps := make(map[string]struct{})
	bindingApps := make(map[string]struct{})
	customerRoutes := make(map[string]RoutePolicy)
	bindingDailyRequestLimits := make(map[string]*int64)
	for rows.Next() {
		var appID string
		var methods, paths []string
		var operator bool
		var bindingDailyRequestLimit *int64
		if err := rows.Scan(&appID, &methods, &paths, &operator, &bindingDailyRequestLimit); err != nil {
			return Integration{}, err
		}
		apps[appID] = struct{}{}
		if operator {
			operatorApps[appID] = struct{}{}
		} else {
			bindingApps[appID] = struct{}{}
			if methods != nil || paths != nil {
				customerRoutes[appID] = RoutePolicy{AllowedMethods: methods, AllowedPathPrefixes: paths}
			}
			if bindingDailyRequestLimit != nil && *bindingDailyRequestLimit > 0 {
				maximum, ok := api.OutboundRequestsPerDayMaxForPlan(api.Plan(plan))
				if !ok {
					return Integration{}, fmt.Errorf("%w: account plan has no outbound request budget ceiling", ErrInvalidIntegration)
				}
				if *bindingDailyRequestLimit > maximum {
					*bindingDailyRequestLimit = maximum
				}
				bindingDailyRequestLimits[appID] = bindingDailyRequestLimit
			}
		}
	}
	if err := rows.Err(); err != nil {
		return Integration{}, err
	}
	i := Integration{ID: id, Origin: u, TokenHash: hash, AppIDs: apps,
		OperatorAppIDs: operatorApps, BindingAppIDs: bindingApps, CustomerAppRoutes: customerRoutes,
		RatePerSecond: rate, Burst: burst, MaxInFlight: maxInFlight,
		DailyRequestLimit:              dailyRequestLimit,
		BindingDailyRequestLimits:      bindingDailyRequestLimits,
		RequestTimeout:                 time.Duration(timeoutMS) * time.Millisecond,
		MaxRetries:                     maxRetries,
		ResponseCacheTTLSeconds:        responseCacheTTLSeconds,
		CircuitBreakerFailureThreshold: circuitBreakerFailureThreshold,
		CircuitBreakerOpenSeconds:      circuitBreakerOpenSeconds,
		RetryBudgetPerMinute:           retryBudgetPerMinute,
		PolicyRevision:                 policyUpdatedAt.UnixNano(),
		ProviderAuthMode:               providerAuthMode, CredentialSource: credentialSource, OwnerKind: ownerKind, AllowedMethods: allowedMethods,
		AllowedPathPrefixes: allowedPathPrefixes, Enabled: true}
	if err := i.Validate(); err != nil {
		return Integration{}, err
	}
	return i, nil
}

var _ Resolver = (*PostgresResolver)(nil)
