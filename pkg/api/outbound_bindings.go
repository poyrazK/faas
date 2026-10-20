package api

import (
	"math"
	"time"
)

// MaxOutboundRetries is the platform ceiling for extra upstream attempts
// within one admitted outbound request.
const MaxOutboundRetries = 2

// MaxOutboundResponseCacheTTLSeconds bounds freshness for opt-in outbound
// response caching. It is intentionally short because provider data may
// change outside Gregale's control.
const MaxOutboundResponseCacheTTLSeconds = 300

// MaxOutboundRetryBudgetPerMinute bounds extra provider attempts shared by
// all outbound gateway replicas for one integration.
const MaxOutboundRetryBudgetPerMinute = 3000

// Outbound circuit-breaker settings are deliberately bounded. A zero pair
// disables the breaker; enabled policies need a failure threshold and a
// finite cool-down before one half-open probe is allowed.
const (
	MaxOutboundCircuitBreakerFailureThreshold = 20
	MaxOutboundCircuitBreakerOpenSeconds      = 300
)

// OutboundBindingProbePolicy selects an endpoint the owner declares safe to probe.
// Only successful HTTP statuses can satisfy verification.
type OutboundBindingProbePolicy struct {
	Method         string `json:"method"`
	Path           string `json:"path"`
	ExpectedStatus int    `json:"expected_status"`
}

// OutboundIntegrationOffer is an account-visible managed integration. It
// intentionally contains no provider credential or gateway admission token;
// its request limits are effective for customer integrations after applying
// the account plan ceiling.
type OutboundIntegrationOffer struct {
	ID                   string                `json:"id"`
	Name                 string                `json:"name"`
	Origin               string                `json:"origin"`
	AllowedMethods       []string              `json:"allowed_methods"`
	AllowedPathPrefixes  []string              `json:"allowed_path_prefixes"`
	Enabled              bool                  `json:"enabled"`
	RunsEnabled          bool                  `json:"runs_enabled"`
	CredentialSource     string                `json:"credential_source"`
	CredentialConfigured bool                  `json:"credential_configured"`
	OwnerKind            string                `json:"owner_kind"`
	DailyRequestLimit    *int64                `json:"daily_request_limit"`
	RequestPolicy        OutboundRequestPolicy `json:"request_policy"`
	// AIProvider is empty for ordinary integrations. "openai" (the
	// OpenAI-compatible wire format) or "anthropic" enables token usage
	// extraction and the optional daily token limit (ADR-988).
	AIProvider        string `json:"ai_provider,omitempty"`
	AIDailyTokenLimit *int64 `json:"ai_daily_token_limit"`
	// Cost limits in millicents per UTC day and calendar month, priced from
	// the integration's price list (ADR-990).
	AIDailyCostLimitMillicents   *int64 `json:"ai_daily_cost_limit_millicents"`
	AIMonthlyCostLimitMillicents *int64 `json:"ai_monthly_cost_limit_millicents"`
	// AIRequestLogEnabled records per-call metadata (ADR-991).
	AIRequestLogEnabled bool `json:"ai_request_log_enabled"`
	// Per-end-user UTC-day limits (ADR-994).
	AIUserDailyTokenLimit          *int64 `json:"ai_user_daily_token_limit"`
	AIUserDailyCostLimitMillicents *int64 `json:"ai_user_daily_cost_limit_millicents"`
	// AIRequestLogContent captures logged calls' bodies (ADR-996).
	AIRequestLogContent bool `json:"ai_request_log_content"`
	// AIContentPolicy is the integration's content guardrails (ADR-997).
	AIContentPolicy OutboundAIContentPolicy `json:"ai_content_policy"`
	// AILogExport sends each settled app call to the app's log drains
	// (ADR-1340).
	AILogExport bool `json:"ai_log_export"`
	// AIAllowUnmetered lets through writes the gateway cannot meter, outside
	// the caps (ADR-1432).
	AIAllowUnmetered bool `json:"ai_allow_unmetered"`
	// AIAutoPromptCache adds prompt-cache breakpoints to Claude requests
	// that carry none (ADR-1437).
	AIAutoPromptCache bool `json:"ai_auto_prompt_cache"`
}

// Outbound AI providers select the credential handling and the response
// format the gateway parses for token usage. OpenAI covers any provider that
// speaks its wire format (ADR-988, ADR-991).
const (
	OutboundAIProviderOpenAI      = "openai"
	OutboundAIProviderAnthropic   = "anthropic"
	OutboundAIProviderGemini      = "gemini"
	OutboundAIProviderAzureOpenAI = "azure_openai"
	OutboundAIProviderBedrock     = "bedrock"
	OutboundAIProviderVertex      = "vertex"
)

// ValidOutboundAIProvider accepts the empty value (not an AI integration) and
// the supported provider formats.
func ValidOutboundAIProvider(provider string) bool {
	switch provider {
	case "", OutboundAIProviderOpenAI, OutboundAIProviderAnthropic, OutboundAIProviderGemini,
		OutboundAIProviderAzureOpenAI, OutboundAIProviderBedrock, OutboundAIProviderVertex:
		return true
	}
	return false
}

// ValidOutboundAIPolicy reports whether a provider and optional daily token
// limit can be stored together. A token limit requires a provider.
func ValidOutboundAIPolicy(provider string, dailyTokenLimit *int64) bool {
	if !ValidOutboundAIProvider(provider) {
		return false
	}
	if dailyTokenLimit == nil {
		return true
	}
	return provider != "" && *dailyTokenLimit >= 1 && *dailyTokenLimit <= MaxOutboundAITokensPerDay
}

// PutOutboundAIPolicyRequest replaces an integration's AI gateway settings.
// An empty provider turns AI handling off and requires a null token limit.
type PutOutboundAIPolicyRequest struct {
	AIProvider                 string `json:"ai_provider"`
	DailyTokenLimit            *int64 `json:"daily_token_limit"`
	DailyCostLimitMillicents   *int64 `json:"daily_cost_limit_millicents"`
	MonthlyCostLimitMillicents *int64 `json:"monthly_cost_limit_millicents"`
	// RequestLogEnabled stores per-call metadata (ADR-991).
	RequestLogEnabled bool `json:"request_log_enabled"`
	// RequestLogContent also keeps the start of each logged call's request
	// and response bodies; it requires the request log (ADR-996).
	RequestLogContent bool `json:"request_log_content,omitempty"`
	// UserDailyTokenLimit and UserDailyCostLimitMillicents cap each end
	// user named by X-Gregale-AI-User per UTC day (ADR-994).
	UserDailyTokenLimit          *int64 `json:"user_daily_token_limit,omitempty"`
	UserDailyCostLimitMillicents *int64 `json:"user_daily_cost_limit_millicents,omitempty"`
	// LogExport sends each settled call of an app to that app's log drains
	// as one JSON line, without prompts or completions (ADR-1340).
	LogExport bool `json:"log_export,omitempty"`
	// AllowUnmetered lets through writes the gateway can neither meter nor
	// knows to be free, such as realtime client secrets, vector stores or
	// fine-tuning jobs. Their spend is outside the caps (ADR-1432).
	AllowUnmetered bool `json:"allow_unmetered,omitempty"`
	// AutoPromptCache adds prompt-cache breakpoints to Claude requests that
	// carry none: after a repeated system prompt or tool list, and after the
	// last message of a continuing conversation (ADR-1437).
	AutoPromptCache bool `json:"auto_prompt_cache,omitempty"`
}

// Valid reports whether the complete AI policy can be stored.
func (r PutOutboundAIPolicyRequest) Valid() bool {
	return ValidOutboundAIPolicy(r.AIProvider, r.DailyTokenLimit) &&
		ValidOutboundAICostLimits(r.AIProvider, r.DailyCostLimitMillicents, r.MonthlyCostLimitMillicents) &&
		ValidOutboundAIUserLimits(r.AIProvider, r.UserDailyTokenLimit, r.UserDailyCostLimitMillicents) &&
		(!r.RequestLogEnabled || r.AIProvider != "") && (!r.RequestLogContent || r.RequestLogEnabled) &&
		(!r.LogExport || r.AIProvider != "") && (!r.AllowUnmetered || r.AIProvider != "") &&
		(!r.AutoPromptCache || r.AIProvider != "")
}

// OutboundAIUsageItem is one UTC-day rollup for a caller and model. Caller is
// an app ID, or "runs" for stateless Runs. InputTokens includes cached reads
// and cache writes; those are also reported separately.
type OutboundAIUsageItem struct {
	Caller               string `json:"caller"`
	Model                string `json:"model"`
	Requests             int64  `json:"requests"`
	ErrorRequests        int64  `json:"error_requests"`
	UsageUnknownRequests int64  `json:"usage_unknown_requests"`
	InputTokens          int64  `json:"input_tokens"`
	CachedInputTokens    int64  `json:"cached_input_tokens"`
	CacheWriteTokens     int64  `json:"cache_write_tokens"`
	OutputTokens         int64  `json:"output_tokens"`
	// CostMillicents is priced from the price list effective at each call,
	// rounded up per call; zero without a price list (ADR-990).
	CostMillicents int64 `json:"cost_millicents"`
	// Media usage (ADR-997): images billed per image, seconds of
	// transcribed audio and characters of synthesized speech.
	Images       int64 `json:"images"`
	AudioSeconds int64 `json:"audio_seconds"`
	Characters   int64 `json:"characters"`
	// VideoSeconds is seconds of generated video (ADR-1432).
	VideoSeconds int64 `json:"video_seconds"`
	// Built-in tool uses (ADR-1433): web searches as the provider bills
	// them, file searches, and GB of code-interpreter container sessions.
	WebSearches   int64 `json:"web_searches"`
	FileSearches  int64 `json:"file_searches"`
	CodeSessionGB int64 `json:"code_session_gb"`
	// ADR-1440: requests the response cache served, the provider cost they
	// avoided, and what provider prompt caching saved, negative when cache
	// writes outweighed reads.
	CachedRequests             int64 `json:"cached_requests"`
	CacheSavedMillicents       int64 `json:"cache_saved_millicents"`
	PromptCacheSavedMillicents int64 `json:"prompt_cache_saved_millicents"`
	// Audio tokens, part of the input and output totals (ADR 20261019072722).
	InputAudioTokens  int64 `json:"input_audio_tokens"`
	OutputAudioTokens int64 `json:"output_audio_tokens"`
}

// OutboundAIUsageResponse reports one UTC day of AI usage for an integration.
// DailyTokenCount is the settled admission counter for today, and the sum of
// input and output tokens for an earlier day.
type OutboundAIUsageResponse struct {
	UsageDate       string                `json:"usage_date"`
	AIProvider      string                `json:"ai_provider"`
	DailyTokenCount int64                 `json:"daily_token_count"`
	DailyTokenLimit *int64                `json:"daily_token_limit"`
	ResetsAt        time.Time             `json:"resets_at"`
	Items           []OutboundAIUsageItem `json:"items"`
	// Settled cost of usage_date and of its calendar month through that day.
	DailyCostMillicents        int64  `json:"daily_cost_millicents"`
	DailyCostLimitMillicents   *int64 `json:"daily_cost_limit_millicents"`
	Month                      string `json:"month"`
	MonthlyCostMillicents      int64  `json:"monthly_cost_millicents"`
	MonthlyCostLimitMillicents *int64 `json:"monthly_cost_limit_millicents"`
}

// OutboundRequestPolicy is the per-integration admission and timeout policy.
// Customer-owned values are effective after applying the account plan ceiling.
type OutboundRequestPolicy struct {
	RatePerSecond    float64 `json:"rate_per_second"`
	Burst            int     `json:"burst"`
	MaxInFlight      int     `json:"max_in_flight"`
	RequestTimeoutMS int     `json:"request_timeout_ms"`
	// MaxRetries is the maximum number of extra upstream attempts for safe,
	// bodyless GET/HEAD requests. Zero preserves the original single-attempt
	// behavior. The gateway applies retries within RequestTimeoutMS.
	MaxRetries int `json:"max_retries"`
	// ResponseCacheTTLSeconds opts into a private, process-local cache for
	// eligible GET responses. Zero disables caching.
	ResponseCacheTTLSeconds int `json:"response_cache_ttl_seconds"`
	// CircuitBreakerFailureThreshold opens the integration after this many
	// consecutive transient provider failures. Zero disables the breaker and
	// requires CircuitBreakerOpenSeconds to be zero too.
	CircuitBreakerFailureThreshold int `json:"circuit_breaker_failure_threshold"`
	// CircuitBreakerOpenSeconds is the cool-down before one provider probe is
	// admitted. Zero disables the breaker and requires a zero threshold.
	CircuitBreakerOpenSeconds int `json:"circuit_breaker_open_seconds"`
	// RetryBudgetPerMinute caps extra safe-method attempts with a shared token
	// bucket. Its capacity equals the configured rate; zero leaves the existing
	// per-request MaxRetries policy as the only retry limit.
	RetryBudgetPerMinute int `json:"retry_budget_per_minute"`
}

// DefaultOutboundRequestPolicy preserves the original customer-integration
// admission defaults.
func DefaultOutboundRequestPolicy() OutboundRequestPolicy {
	limits := MustLimitsFor(PlanFree)
	return OutboundRequestPolicy{
		RatePerSecond:                  limits.OutboundRatePerSecondMax,
		Burst:                          limits.OutboundBurstMax,
		MaxInFlight:                    limits.OutboundMaxInFlightMax,
		RequestTimeoutMS:               limits.OutboundRequestTimeoutMSMax,
		MaxRetries:                     0,
		ResponseCacheTTLSeconds:        0,
		CircuitBreakerFailureThreshold: 0,
		CircuitBreakerOpenSeconds:      0,
		RetryBudgetPerMinute:           0,
	}
}

// EffectiveOutboundRequestPolicyForPlan applies the current plan's safety
// ceilings to a persisted policy. A plan downgrade therefore takes effect on
// the next outbound admission without rewriting the configured policy.
func EffectiveOutboundRequestPolicyForPlan(plan Plan, policy OutboundRequestPolicy) (OutboundRequestPolicy, bool) {
	limits, ok := LimitsFor(plan)
	if !ok || limits.OutboundRatePerSecondMax <= 0 || limits.OutboundBurstMax < 1 ||
		limits.OutboundMaxInFlightMax < 1 || limits.OutboundRequestTimeoutMSMax < 1 || limits.OutboundMaxRetriesMax < 0 ||
		limits.OutboundResponseCacheTTLSecondsMax < 0 || limits.OutboundRetryBudgetPerMinuteMax < 0 ||
		policy.RatePerSecond <= 0 || math.IsNaN(policy.RatePerSecond) || math.IsInf(policy.RatePerSecond, 0) ||
		policy.Burst < 1 || policy.MaxInFlight < 1 || policy.RequestTimeoutMS < 1 || policy.MaxRetries < 0 ||
		policy.ResponseCacheTTLSeconds < 0 || !ValidOutboundCircuitBreakerPolicy(policy.CircuitBreakerFailureThreshold, policy.CircuitBreakerOpenSeconds) ||
		policy.RetryBudgetPerMinute < 0 || policy.RetryBudgetPerMinute > MaxOutboundRetryBudgetPerMinute {
		return OutboundRequestPolicy{}, false
	}
	if policy.RatePerSecond > limits.OutboundRatePerSecondMax {
		policy.RatePerSecond = limits.OutboundRatePerSecondMax
	}
	if policy.Burst > limits.OutboundBurstMax {
		policy.Burst = limits.OutboundBurstMax
	}
	if policy.MaxInFlight > limits.OutboundMaxInFlightMax {
		policy.MaxInFlight = limits.OutboundMaxInFlightMax
	}
	if policy.RequestTimeoutMS > limits.OutboundRequestTimeoutMSMax {
		policy.RequestTimeoutMS = limits.OutboundRequestTimeoutMSMax
	}
	if policy.MaxRetries > limits.OutboundMaxRetriesMax {
		policy.MaxRetries = limits.OutboundMaxRetriesMax
	}
	if policy.ResponseCacheTTLSeconds > limits.OutboundResponseCacheTTLSecondsMax {
		policy.ResponseCacheTTLSeconds = limits.OutboundResponseCacheTTLSecondsMax
	}
	if policy.RetryBudgetPerMinute > limits.OutboundRetryBudgetPerMinuteMax {
		policy.RetryBudgetPerMinute = limits.OutboundRetryBudgetPerMinuteMax
	}
	return policy, true
}

// ValidOutboundCircuitBreakerPolicy accepts either the disabled zero pair or
// a bounded threshold/cool-down pair. Partial configurations are rejected.
func ValidOutboundCircuitBreakerPolicy(failureThreshold, openSeconds int) bool {
	if failureThreshold == 0 || openSeconds == 0 {
		return failureThreshold == 0 && openSeconds == 0
	}
	return failureThreshold >= 1 && failureThreshold <= MaxOutboundCircuitBreakerFailureThreshold &&
		openSeconds >= 1 && openSeconds <= MaxOutboundCircuitBreakerOpenSeconds
}

// OutboundRequestPolicyAllowedForPlan reports whether a customer-selected
// policy is valid without silently clamping an API write.
func OutboundRequestPolicyAllowedForPlan(plan Plan, policy OutboundRequestPolicy) bool {
	effective, ok := EffectiveOutboundRequestPolicyForPlan(plan, policy)
	return ok && effective == policy
}

// CreateOutboundIntegrationRequest creates a customer-owned managed
// integration. Its provider Authorization value is uploaded separately.
type CreateOutboundIntegrationRequest struct {
	Name                string                 `json:"name"`
	Origin              string                 `json:"origin"`
	AllowedMethods      []string               `json:"allowed_methods"`
	AllowedPathPrefixes []string               `json:"allowed_path_prefixes"`
	DailyRequestLimit   *int64                 `json:"daily_request_limit,omitempty"`
	RequestPolicy       *OutboundRequestPolicy `json:"request_policy,omitempty"`
	AIProvider          string                 `json:"ai_provider,omitempty"`
	AIDailyTokenLimit   *int64                 `json:"ai_daily_token_limit,omitempty"`
}

// PutOutboundRequestPolicyRequest replaces the complete admission policy.
// Omitting max_retries preserves the zero-retry behavior for older clients.
type PutOutboundRequestPolicyRequest struct {
	RequestPolicy OutboundRequestPolicy `json:"request_policy"`
}

// PutOutboundCredentialRequest sets or rotates the provider Authorization
// value. The response intentionally has no equivalent secret field.
type PutOutboundCredentialRequest struct {
	Authorization string `json:"authorization"`
}

// PutOutboundDailyRequestBudgetRequest sets a per-integration daily limit;
// null removes the customer-selected limit.
type PutOutboundDailyRequestBudgetRequest struct {
	DailyRequestLimit *int64 `json:"daily_request_limit"`
}

// PutOutboundRunsBindingRequest explicitly grants or revokes a managed
// integration for stateless Runs. The grant is distinct from app bindings.
type PutOutboundRunsBindingRequest struct {
	Enabled *bool `json:"enabled"`
}

// OutboundIntegrationUsageResponse reports UTC-day gateway admissions. Calls
// count when admitted, including those whose upstream request later fails.
// DailyRequestLimit is the effective optional cap after applying the account
// plan ceiling.
type OutboundIntegrationUsageResponse struct {
	DailyRequestCount int64     `json:"daily_request_count"`
	DailyRequestLimit *int64    `json:"daily_request_limit"`
	UsageDate         string    `json:"usage_date"`
	ResetsAt          time.Time `json:"resets_at"`
}

// PutOutboundBindingDailyRequestBudgetRequest sets a binding's daily request
// limit; null removes the app-specific limit.
type PutOutboundBindingDailyRequestBudgetRequest struct {
	DailyRequestLimit *int64 `json:"daily_request_limit"`
}

// OutboundBindingUsageResponse reports UTC-day admissions for one app binding.
type OutboundBindingUsageResponse struct {
	DailyRequestCount int64     `json:"daily_request_count"`
	DailyRequestLimit *int64    `json:"daily_request_limit"`
	UsageDate         string    `json:"usage_date"`
	ResetsAt          time.Time `json:"resets_at"`
}

type OutboundIntegrationOfferList struct {
	Items []OutboundIntegrationOffer `json:"items"`
}

type OutboundAppBinding struct {
	Integration         OutboundIntegrationOffer `json:"integration"`
	AppID               string                   `json:"app_id"`
	AllowedMethods      []string                 `json:"allowed_methods"`
	AllowedPathPrefixes []string                 `json:"allowed_path_prefixes"`
	DailyRequestLimit   *int64                   `json:"daily_request_limit"`
	CreatedAt           time.Time                `json:"created_at"`
	// AIDailyTokenLimit is this app's own UTC-day AI token limit (ADR-990).
	AIDailyTokenLimit *int64 `json:"ai_daily_token_limit"`
	// Per-app AI guardrails (ADR-991); null means unrestricted.
	AIRequestsPerMinute *int64   `json:"ai_requests_per_minute"`
	AITokensPerMinute   *int64   `json:"ai_tokens_per_minute"`
	AIAllowedModels     []string `json:"ai_allowed_models"`
	AIMaxOutputTokens   *int64   `json:"ai_max_output_tokens"`
}

// UpdateOutboundBindingPolicyRequest narrows an app's route access within
// the integration policy. It cannot change the provider origin or widen it.
type UpdateOutboundBindingPolicyRequest struct {
	AllowedMethods      []string `json:"allowed_methods"`
	AllowedPathPrefixes []string `json:"allowed_path_prefixes"`
}

type OutboundAppBindingList struct {
	Items []OutboundAppBinding `json:"items"`
}
