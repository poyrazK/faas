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

// Integration reads the integration, its app attachments and bindings, and
// its current AI price list in one pipelined round trip (ADR-1120).
func (r *PostgresResolver) Integration(ctx context.Context, id string) (Integration, error) {
	integrationID, err := uuid.Parse(id)
	if err != nil {
		return Integration{}, ErrIntegrationNotFound
	}
	batch := &pgx.Batch{}
	batch.Queue(integrationPolicySQL, integrationID)
	batch.Queue(integrationAppsSQL, integrationID)
	batch.Queue(currentAIPricesSQL, integrationID)
	results := r.pool.SendBatch(ctx, batch)
	integration, err := scanIntegration(id, results)
	if closeErr := results.Close(); err == nil && closeErr != nil {
		return Integration{}, closeErr
	}
	return integration, err
}

const integrationPolicySQL = `
	SELECT integration.account_id, integration.name, account.plan, integration.origin, integration.token_hash,
	       integration.rate_per_second, integration.burst, integration.max_in_flight,
	       integration.request_timeout_ms, integration.max_retries, integration.response_cache_ttl_seconds,
	       integration.circuit_breaker_failure_threshold, integration.circuit_breaker_open_seconds,
	       integration.retry_budget_per_minute,
	       integration.updated_at, integration.enabled, integration.provider_auth_mode,
	       integration.credential_source, integration.allowed_methods,
	       integration.allowed_path_prefixes, integration.owner_kind,
	       COALESCE(integration.daily_request_limit, 0),
	       COALESCE(integration.ai_provider, ''), COALESCE(integration.ai_daily_token_limit, 0),
	       COALESCE(integration.ai_daily_cost_limit_millicents, 0), COALESCE(integration.ai_monthly_cost_limit_millicents, 0),
	       integration.ai_request_log_enabled,
	       COALESCE(integration.ai_user_daily_token_limit, 0), COALESCE(integration.ai_user_daily_cost_limit_millicents, 0),
	       integration.ai_request_log_content, integration.ai_content_email, integration.ai_content_payment_card,
	       integration.ai_content_iban, integration.ai_content_secret, integration.ai_content_blocked_terms
	  FROM outbound_integrations integration
	  JOIN accounts account ON account.id = integration.account_id
	 WHERE integration.id = $1`

// integrationAppsSQL lists the operator attachments and, for a managed
// integration, the customer bindings of the integration's own account.
const integrationAppsSQL = `
	SELECT attachment.app_id::text, NULL::text[], NULL::text[], true, NULL::bigint, NULL::bigint,
	       NULL::bigint, NULL::bigint, NULL::text[], NULL::bigint
	  FROM outbound_integration_apps attachment
	  JOIN outbound_integrations integration ON integration.id = attachment.integration_id
	  JOIN apps app ON app.id = attachment.app_id
	 WHERE attachment.integration_id = $1 AND app.account_id = integration.account_id AND app.status <> 'deleted'
	UNION ALL
	SELECT binding.app_id::text, binding.allowed_methods, binding.allowed_path_prefixes, false, binding.daily_request_limit,
	       binding.ai_daily_token_limit, binding.ai_requests_per_minute::bigint, binding.ai_tokens_per_minute,
	       binding.ai_allowed_models, binding.ai_max_output_tokens::bigint
	  FROM outbound_app_bindings binding
	  JOIN outbound_integrations integration ON integration.id = binding.integration_id
	  JOIN apps app ON app.id = binding.app_id
	 WHERE binding.integration_id = $1 AND binding.account_id = integration.account_id
	   AND app.account_id = integration.account_id AND app.status <> 'deleted'
	   AND integration.provider_auth_mode = 'managed'`

// currentAIPricesSQL loads the newest price list already in effect (ADR-990).
const currentAIPricesSQL = `
	SELECT entry.model_prefix, entry.input_millicents_per_mtok, entry.cached_input_millicents_per_mtok,
	       entry.cache_write_millicents_per_mtok, entry.output_millicents_per_mtok,
	       entry.input_image_millicents_per_mtok, entry.output_image_millicents_per_mtok, entry.millicents_per_image,
	       entry.millicents_per_audio_minute, entry.millicents_per_million_characters
	  FROM outbound_ai_price_entries entry
	 WHERE entry.price_list_id = (
	       SELECT list.id FROM outbound_ai_price_lists list
	        WHERE list.integration_id = $1 AND list.effective_from <= now()
	        ORDER BY list.effective_from DESC, list.created_at DESC
	        LIMIT 1)`

// scanIntegration reads the batch's results in the order they were queued.
func scanIntegration(id string, results pgx.BatchResults) (Integration, error) {
	var origin string
	var accountID uuid.UUID
	var name string
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
	var aiProvider string
	var aiDailyTokenLimitValue, aiDailyCostLimitValue, aiMonthlyCostLimitValue int64
	var aiRequestLogEnabled, aiRequestLogContent bool
	var contentPolicy api.OutboundAIContentPolicy
	var aiUserTokenLimitValue, aiUserCostLimitValue int64
	err := results.QueryRow().
		Scan(&accountID, &name, &plan, &origin, &tokenHash, &rate, &burst, &maxInFlight, &timeoutMS, &maxRetries, &responseCacheTTLSeconds,
			&circuitBreakerFailureThreshold, &circuitBreakerOpenSeconds, &retryBudgetPerMinute, &policyUpdatedAt, &enabled, &providerAuthMode,
			&credentialSource, &allowedMethods, &allowedPathPrefixes, &ownerKind, &dailyRequestLimitValue,
			&aiProvider, &aiDailyTokenLimitValue, &aiDailyCostLimitValue, &aiMonthlyCostLimitValue, &aiRequestLogEnabled,
			&aiUserTokenLimitValue, &aiUserCostLimitValue, &aiRequestLogContent, &contentPolicy.Email,
			&contentPolicy.PaymentCard, &contentPolicy.IBAN, &contentPolicy.Secret, &contentPolicy.BlockedTerms)
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
	rows, err := results.Query()
	if err != nil {
		return Integration{}, err
	}
	defer rows.Close()
	apps := make(map[string]struct{})
	operatorApps := make(map[string]struct{})
	bindingApps := make(map[string]struct{})
	customerRoutes := make(map[string]RoutePolicy)
	bindingDailyRequestLimits := make(map[string]*int64)
	bindingDailyAITokenLimits := make(map[string]*int64)
	bindingAIGuardrails := make(map[string]AIGuardrails)
	for rows.Next() {
		var appID string
		var methods, paths []string
		var operator bool
		var bindingDailyRequestLimit, bindingDailyAITokenLimit *int64
		var guard AIGuardrails
		if err := rows.Scan(&appID, &methods, &paths, &operator, &bindingDailyRequestLimit, &bindingDailyAITokenLimit,
			&guard.RequestsPerMinute, &guard.TokensPerMinute, &guard.AllowedModels, &guard.MaxOutputTokens); err != nil {
			return Integration{}, err
		}
		if !operator && aiProvider != "" && (guard.restrictsCalls() || guard.RequestsPerMinute != nil || guard.TokensPerMinute != nil) {
			bindingAIGuardrails[appID] = guard
		}
		if !operator && bindingDailyAITokenLimit != nil && *bindingDailyAITokenLimit > 0 {
			bindingDailyAITokenLimits[appID] = bindingDailyAITokenLimit
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
	rows.Close()
	priceRows, err := results.Query()
	if err != nil {
		return Integration{}, err
	}
	prices, err := scanAIPrices(priceRows)
	if err != nil {
		return Integration{}, err
	}
	if aiProvider == "" {
		prices = nil
	}
	i := Integration{ID: id, AccountID: accountID.String(), Name: name, Origin: u, TokenHash: hash, AppIDs: apps,
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
		AllowedPathPrefixes: allowedPathPrefixes, Enabled: true, AIProvider: aiProvider,
		BindingDailyAITokenLimits: bindingDailyAITokenLimits, AIPrices: prices,
		BindingAIGuardrails: bindingAIGuardrails, AIRequestLogEnabled: aiRequestLogEnabled, AIRequestLogContent: aiRequestLogContent,
		AccountPlan:       api.Plan(plan),
		AIContentPolicy:   contentPolicy,
		DailyAITokenLimit: positiveLimit(aiDailyTokenLimitValue), DailyAICostLimit: positiveLimit(aiDailyCostLimitValue),
		MonthlyAICostLimit: positiveLimit(aiMonthlyCostLimitValue), AIUserDailyTokenLimit: positiveLimit(aiUserTokenLimitValue),
		AIUserDailyCostLimit: positiveLimit(aiUserCostLimitValue)}
	if err := i.Validate(); err != nil {
		return Integration{}, err
	}
	return i, nil
}

// scanAIPrices reads the current price list's entries.
func scanAIPrices(rows pgx.Rows) ([]AIPrice, error) {
	defer rows.Close()
	entries := make([]api.OutboundAIPriceEntry, 0)
	for rows.Next() {
		var entry api.OutboundAIPriceEntry
		if err := rows.Scan(&entry.ModelPrefix, &entry.InputMillicentsPerMTok, &entry.CachedInputMillicentsPerMTok,
			&entry.CacheWriteMillicentsPerMTok, &entry.OutputMillicentsPerMTok, &entry.InputImageMillicentsPerMTok,
			&entry.OutputImageMillicentsPerMTok, &entry.MillicentsPerImage, &entry.MillicentsPerAudioMinute,
			&entry.MillicentsPerMillionCharacters); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ResolveAIPrices(entries), nil
}

var _ Resolver = (*PostgresResolver)(nil)
