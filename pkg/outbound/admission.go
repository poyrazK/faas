// Package outbound implements explicit, integration-scoped admission for
// third-party HTTP requests. Callers opt in by sending requests through the
// gateway; Gregale never attempts to inspect arbitrary encrypted egress.
package outbound

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrIntegrationNotFound = errors.New("outbound integration not found")
	ErrIntegrationDisabled = errors.New("outbound integration disabled")
	ErrUnauthorized        = errors.New("outbound integration token is invalid")
	ErrAppNotAttached      = errors.New("app is not attached to outbound integration")
	ErrInvalidIntegration  = errors.New("invalid outbound integration")
)

const (
	ReasonRate             = "rate_limit"
	ReasonConcurrency      = "concurrency_limit"
	ReasonDailyLimit       = "daily_request_limit"
	ReasonAppNotAttached   = "app_not_attached"
	ReasonCircuitOpen      = "circuit_breaker_open"
	ReasonProviderCooldown = "provider_cooldown"
	ReasonDailyTokenLimit  = "daily_token_limit"
	ReasonDailyCostLimit   = "daily_cost_limit"
	ReasonMonthlyCostLimit = "monthly_cost_limit"
	// Per-app per-minute AI limits (ADR-991).
	ReasonRequestsPerMinute = "requests_per_minute_limit"
	ReasonTokensPerMinute   = "tokens_per_minute_limit"
	// Per-end-user UTC-day AI limits (ADR-994).
	ReasonUserDailyTokenLimit = "user_daily_token_limit"
	ReasonUserDailyCostLimit  = "user_daily_cost_limit"
	// ReasonTagDailyCostLimit refuses a call one of whose tags reached its
	// daily cost limit (ADR-1436).
	ReasonTagDailyCostLimit = "tag_daily_cost_limit"
	// ReasonUserTrackingFull refuses a new end user once the day already
	// tracks the maximum number of end users and per-user limits are set
	// (ADR-996).
	ReasonUserTrackingFull = "user_tracking_full"
	// Per-trace limits (ADR 20261020202035): a trace's calls, its cost, its
	// streak of identical tool requests, and a new trace past the day's
	// recorded traces. A trace never resets, so none carries a retry.
	ReasonTraceCallLimit = "trace_call_limit"
	ReasonTraceCostLimit = "trace_cost_limit"
	ReasonTraceToolLoop  = "trace_tool_loop"
	ReasonTraceUntracked = "trace_untracked"

	ProviderAuthApplication        = "application"
	ProviderAuthManaged            = "managed"
	CredentialSourceOperatorEnv    = "operator_env"
	CredentialSourceCustomerSealed = "customer_sealed"
	IntegrationOwnerOperator       = "operator"
	IntegrationOwnerCustomer       = "customer"
)

// Integration is the immutable policy used for one provider. TokenHash is a
// SHA-256 digest for application-auth integrations; managed integrations use
// workload identity and may leave it zero. Raw bearer tokens are never logged.
type Integration struct {
	ID string
	// AccountID and Name are loaded from the trusted integration row. They
	// let outboundd attribute its platform-owned dependency span without
	// trusting caller headers or exposing credentials.
	AccountID string
	Name      string
	// AccountPlan is the owning account's plan; customer AI integrations
	// need a plan entitled to the AI gateway (ADR-998).
	AccountPlan                    api.Plan
	Origin                         *url.URL
	TokenHash                      [32]byte
	AppIDs                         map[string]struct{}
	OperatorAppIDs                 map[string]struct{}
	BindingAppIDs                  map[string]struct{}
	CustomerAppRoutes              map[string]RoutePolicy
	RatePerSecond                  float64
	Burst                          int
	MaxInFlight                    int
	DailyRequestLimit              *int64
	BindingDailyRequestLimits      map[string]*int64
	RequestTimeout                 time.Duration
	MaxRetries                     int
	RetryBudgetPerMinute           int
	ResponseCacheTTLSeconds        int
	CircuitBreakerFailureThreshold int
	CircuitBreakerOpenSeconds      int
	// PolicyRevision invalidates process-local response entries after a
	// database-backed integration policy changes.
	PolicyRevision      int64
	ProviderAuthMode    string
	CredentialSource    string
	OwnerKind           string
	AllowedMethods      []string
	AllowedPathPrefixes []string
	Enabled             bool
	// AIProvider enables token usage extraction for "openai" (the
	// OpenAI-compatible wire format) or "anthropic" responses (ADR-988).
	AIProvider string
	// DailyAITokenLimit stops new admissions once the UTC day's settled
	// input plus output tokens reach it. It requires AIProvider.
	DailyAITokenLimit *int64
	// BindingDailyAITokenLimits are per-app UTC-day token limits (ADR-990).
	BindingDailyAITokenLimits map[string]*int64
	// DailyAICostLimit and MonthlyAICostLimit are millicent spend guards
	// priced from AIPrices, the price list current at resolution (ADR-990).
	DailyAICostLimit   *int64
	MonthlyAICostLimit *int64
	AIPrices           []AIPrice
	// BindingAIGuardrails are per-app per-minute limits, model allow-lists
	// and output ceilings (ADR-991), keyed by bound app ID.
	BindingAIGuardrails map[string]AIGuardrails
	// AIRequestLogEnabled records per-call metadata at settlement.
	AIRequestLogEnabled bool
	// AIRequestLogContent also keeps the start of each logged call's
	// request and response bodies (ADR-996).
	AIRequestLogContent bool
	// AIContentPolicy is checked and applied before admission (ADR-997);
	// its moderation check runs after admission (ADR-1340).
	AIContentPolicy api.OutboundAIContentPolicy
	// AILogExport writes each settled app call to the export outbox for the
	// app's log drains (ADR-1340).
	AILogExport bool
	// AIAllowUnmetered lets through writes the gateway can neither meter
	// nor knows to be free; otherwise they are refused (ADR-1432).
	AIAllowUnmetered bool
	// AIAutoPromptCache adds prompt-cache breakpoints to Claude requests
	// that carry none (ADR-1437).
	AIAutoPromptCache bool
	// AIUserDailyTokenLimit and AIUserDailyCostLimit cap each end user a
	// call names with X-Gregale-AI-User per UTC day (ADR-994).
	AIUserDailyTokenLimit *int64
	AIUserDailyCostLimit  *int64
	// AITagDailyCostLimits caps the UTC-day cost of exact usage tags
	// (ADR-1436).
	AITagDailyCostLimits map[string]int64
	// AITraceMaxCalls, AITraceMaxCostMillicents and
	// AITraceMaxRepeatedToolCalls cap each AI trace (ADR 20261020202035).
	AITraceMaxCalls             *int64
	AITraceMaxCostMillicents    *int64
	AITraceMaxRepeatedToolCalls *int64
	// AIOnlineEvalPercent samples this share of answered, captured app
	// calls for the online evaluation worker; zero is off
	// (ADR 20261021171712).
	AIOnlineEvalPercent int
}

// AIGuardrails are one app binding's AI restrictions; nil fields are unset.
type AIGuardrails struct {
	RequestsPerMinute *int64
	TokensPerMinute   *int64
	AllowedModels     []string
	MaxOutputTokens   *int64
}

// restrictsCalls reports a model allow-list or output ceiling, which need
// a readable body.
func (g AIGuardrails) restrictsCalls() bool {
	return g.AllowedModels != nil || g.MaxOutputTokens != nil
}

func copyAIGuardrails(in map[string]AIGuardrails) map[string]AIGuardrails {
	if in == nil {
		return nil
	}
	out := make(map[string]AIGuardrails, len(in))
	for appID, guard := range in {
		guarded := AIGuardrails{
			RequestsPerMinute: copyInt64Pointer(guard.RequestsPerMinute), TokensPerMinute: copyInt64Pointer(guard.TokensPerMinute),
			MaxOutputTokens: copyInt64Pointer(guard.MaxOutputTokens),
		}
		if guard.AllowedModels != nil {
			guarded.AllowedModels = append(make([]string, 0, len(guard.AllowedModels)), guard.AllowedModels...)
		}
		out[appID] = guarded
	}
	return out
}

// NewIntegration validates and constructs an integration from a raw token.
// The raw token is only held by the caller and is hashed immediately.
func NewIntegration(id, origin, token string, appIDs []string, ratePerSecond float64, burst, maxInFlight int, requestTimeout time.Duration) (Integration, error) {
	if token == "" {
		return Integration{}, fmt.Errorf("%w: token is required", ErrInvalidIntegration)
	}
	u, err := url.Parse(origin)
	if err != nil {
		return Integration{}, fmt.Errorf("%w: origin: %w", ErrInvalidIntegration, err)
	}
	apps := make(map[string]struct{}, len(appIDs))
	for _, appID := range appIDs {
		if strings.TrimSpace(appID) != "" {
			apps[appID] = struct{}{}
		}
	}
	sum := sha256.Sum256([]byte(token))
	i := Integration{
		ID:               id,
		Origin:           u,
		TokenHash:        sum,
		AppIDs:           apps,
		RatePerSecond:    ratePerSecond,
		Burst:            burst,
		MaxInFlight:      maxInFlight,
		RequestTimeout:   requestTimeout,
		ProviderAuthMode: ProviderAuthApplication,
		OwnerKind:        IntegrationOwnerOperator,
		Enabled:          true,
	}
	if err := i.Validate(); err != nil {
		return Integration{}, err
	}
	return i, nil
}

func (i Integration) Validate() error {
	if strings.TrimSpace(i.ID) == "" || i.Origin == nil {
		return fmt.Errorf("%w: id and origin are required", ErrInvalidIntegration)
	}
	if i.TokenHash == ([32]byte{}) && i.ProviderAuthMode != ProviderAuthManaged {
		return fmt.Errorf("%w: token hash is required", ErrInvalidIntegration)
	}
	if i.Origin.Scheme != "https" || i.Origin.Host == "" || i.Origin.User != nil || i.Origin.RawQuery != "" || i.Origin.Fragment != "" {
		return fmt.Errorf("%w: origin must be an https URL without credentials, query, or fragment", ErrInvalidIntegration)
	}
	if math.IsNaN(i.RatePerSecond) || math.IsInf(i.RatePerSecond, 0) || i.RatePerSecond <= 0 || i.Burst < 1 || i.MaxInFlight < 1 {
		return fmt.Errorf("%w: rate, burst, and max_in_flight must be positive", ErrInvalidIntegration)
	}
	if i.DailyRequestLimit != nil && (*i.DailyRequestLimit < 1 || *i.DailyRequestLimit > api.MaxOutboundRequestsPerDay) {
		return fmt.Errorf("%w: daily request limit is outside the supported range", ErrInvalidIntegration)
	}
	if !api.ValidOutboundAIPolicy(i.AIProvider, i.DailyAITokenLimit) ||
		!api.ValidOutboundAICostLimits(i.AIProvider, i.DailyAICostLimit, i.MonthlyAICostLimit) {
		return fmt.Errorf("%w: AI provider, token limit or cost limit is invalid", ErrInvalidIntegration)
	}
	for appID, limit := range i.BindingDailyAITokenLimits {
		if !i.AllowsApp(appID) || (limit != nil && (*limit < 1 || *limit > api.MaxOutboundAITokensPerDay)) {
			return fmt.Errorf("%w: binding daily token limit is invalid", ErrInvalidIntegration)
		}
	}
	for appID, guard := range i.BindingAIGuardrails {
		request := api.PutOutboundBindingAIGuardrailsRequest{
			RequestsPerMinute: guard.RequestsPerMinute, TokensPerMinute: guard.TokensPerMinute,
			AllowedModels: guard.AllowedModels, MaxOutputTokens: guard.MaxOutputTokens,
		}
		if !i.AllowsApp(appID) || i.AIProvider == "" || !request.Valid() {
			return fmt.Errorf("%w: binding AI guardrails are invalid", ErrInvalidIntegration)
		}
	}
	if i.AIRequestLogEnabled && i.AIProvider == "" {
		return fmt.Errorf("%w: the AI request log requires an AI provider", ErrInvalidIntegration)
	}
	if i.AILogExport && i.AIProvider == "" {
		return fmt.Errorf("%w: AI log export requires an AI provider", ErrInvalidIntegration)
	}
	if i.AIAllowUnmetered && i.AIProvider == "" {
		return fmt.Errorf("%w: allowing unmetered AI calls requires an AI provider", ErrInvalidIntegration)
	}
	if i.AIAutoPromptCache && i.AIProvider == "" {
		return fmt.Errorf("%w: automatic prompt caching requires an AI provider", ErrInvalidIntegration)
	}
	if i.AIRequestLogContent && !i.AIRequestLogEnabled {
		return fmt.Errorf("%w: AI content capture requires the request log", ErrInvalidIntegration)
	}
	if !i.AIContentPolicy.Valid() || (i.AIContentPolicy.Active() && i.AIProvider == "") {
		return fmt.Errorf("%w: the AI content policy is invalid", ErrInvalidIntegration)
	}
	if !api.ValidOutboundAIUserLimits(i.AIProvider, i.AIUserDailyTokenLimit, i.AIUserDailyCostLimit) {
		return fmt.Errorf("%w: per-end-user AI limits are invalid", ErrInvalidIntegration)
	}
	if !api.ValidOutboundAITraceLimits(i.AIProvider, i.AITraceMaxCalls, i.AITraceMaxCostMillicents, i.AITraceMaxRepeatedToolCalls) {
		return fmt.Errorf("%w: per-trace AI limits are invalid", ErrInvalidIntegration)
	}
	for appID, limit := range i.BindingDailyRequestLimits {
		if strings.TrimSpace(appID) == "" || !i.AllowsApp(appID) || (limit != nil && (*limit < 1 || *limit > api.MaxOutboundRequestsPerDay)) {
			return fmt.Errorf("%w: binding daily request limit is invalid", ErrInvalidIntegration)
		}
	}
	for appID := range i.BindingAppIDs {
		if strings.TrimSpace(appID) == "" || !i.AllowsApp(appID) {
			return fmt.Errorf("%w: binding app ID is invalid", ErrInvalidIntegration)
		}
	}
	// An operator may provision an integration with no initial app. Customer
	// attachments live in apid-owned outbound_app_bindings and are loaded by
	// the resolver at request time.
	if i.RequestTimeout <= 0 {
		return fmt.Errorf("%w: request timeout must be positive", ErrInvalidIntegration)
	}
	if i.MaxRetries < 0 || i.MaxRetries > api.MaxOutboundRetries {
		return fmt.Errorf("%w: max_retries must be between 0 and %d", ErrInvalidIntegration, api.MaxOutboundRetries)
	}
	if i.RetryBudgetPerMinute < 0 || i.RetryBudgetPerMinute > api.MaxOutboundRetryBudgetPerMinute {
		return fmt.Errorf("%w: retry_budget_per_minute must be between 0 and %d", ErrInvalidIntegration, api.MaxOutboundRetryBudgetPerMinute)
	}
	if i.ResponseCacheTTLSeconds < 0 || i.ResponseCacheTTLSeconds > api.MaxOutboundResponseCacheTTLSeconds {
		return fmt.Errorf("%w: response_cache_ttl_seconds must be between 0 and %d", ErrInvalidIntegration, api.MaxOutboundResponseCacheTTLSeconds)
	}
	if !api.ValidOutboundCircuitBreakerPolicy(i.CircuitBreakerFailureThreshold, i.CircuitBreakerOpenSeconds) {
		return fmt.Errorf("%w: circuit breaker threshold and open seconds must both be zero or within their supported ranges", ErrInvalidIntegration)
	}
	if i.ProviderAuthMode != "" && i.ProviderAuthMode != ProviderAuthApplication && i.ProviderAuthMode != ProviderAuthManaged {
		return fmt.Errorf("%w: provider authentication mode is invalid", ErrInvalidIntegration)
	}
	if i.CredentialSource != "" && i.CredentialSource != CredentialSourceOperatorEnv && i.CredentialSource != CredentialSourceCustomerSealed {
		return fmt.Errorf("%w: credential source is invalid", ErrInvalidIntegration)
	}
	if i.OwnerKind != "" && i.OwnerKind != IntegrationOwnerOperator && i.OwnerKind != IntegrationOwnerCustomer {
		return fmt.Errorf("%w: integration owner is invalid", ErrInvalidIntegration)
	}
	if i.CredentialSource == CredentialSourceCustomerSealed && i.ProviderAuthMode != ProviderAuthManaged {
		return fmt.Errorf("%w: customer credential source requires managed authentication", ErrInvalidIntegration)
	}
	if i.OwnerKind == IntegrationOwnerCustomer && (i.ProviderAuthMode != ProviderAuthManaged || i.CredentialSource != CredentialSourceCustomerSealed) {
		return fmt.Errorf("%w: customer-owned integration requires managed customer credentials", ErrInvalidIntegration)
	}
	return i.validateRoutePolicy()
}

// MetricLabel bounds Prometheus cardinality for customer-created integrations.
// Operator IDs remain configuration-owned and customer integrations share one
// label regardless of how many customers create.
func (i Integration) MetricLabel() string {
	if i.OwnerKind == IntegrationOwnerCustomer {
		return "customer_managed"
	}
	return i.ID
}

func (i Integration) AllowsApp(appID string) bool {
	_, ok := i.AppIDs[appID]
	return ok
}

// Resolver supplies the policy for an integration ID.
type Resolver interface {
	Integration(context.Context, string) (Integration, error)
}

// AdmissionSpec is the policy snapshot supplied to the shared admission
// backend. Backends must enforce it atomically across all gateway processes.
type AdmissionSpec struct {
	IntegrationID     string
	RatePerSecond     float64
	Burst             int
	MaxInFlight       int
	DailyRequestLimit *int64
	// DailyAITokenLimit rejects new admissions once the UTC day's settled AI
	// tokens reach it. The Postgres backend reads the stored value instead.
	DailyAITokenLimit *int64
	// BindingDailyAITokenLimit, DailyAICostLimit and MonthlyAICostLimit
	// follow the same rule for one app's tokens and the integration's cost
	// (ADR-990); the Postgres backend reads the stored values.
	BindingDailyAITokenLimit *int64
	DailyAICostLimit         *int64
	MonthlyAICostLimit       *int64
	// AIReservedTokens and AIReservedCostMillicents are this call's
	// admission-time reservation (ADR-991). They count against the AI limits
	// with every live lease's reservation and leave with the lease.
	AIReservedTokens         int64
	AIReservedCostMillicents int64
	// BindingRequestsPerMinute and BindingTokensPerMinute are the app's
	// per-minute limits; the Postgres backend reads the stored values.
	BindingRequestsPerMinute *int64
	BindingTokensPerMinute   *int64
	// AIEndUser is the call's X-Gregale-AI-User value (ADR-994). With it,
	// UserDailyAITokenLimit and UserDailyAICostLimit cap that end user's UTC
	// day; the Postgres backend reads the stored limits.
	AIEndUser             string
	UserDailyAITokenLimit *int64
	UserDailyAICostLimit  *int64
	// AITags are the call's usage tags (ADR-1436). AITagDailyCostLimits caps
	// a tag's UTC day; the Postgres backend reads the stored limits.
	AITags               []string
	AITagDailyCostLimits map[string]int64
	// AITraceID is the call's AI trace when its integration records traces
	// (ADR 20261020202035); its lease carries it. AITraceLimits caps the
	// trace; the Postgres backend reads the stored limits.
	AITraceID     string
	AITraceLimits aiTraceLimits
	// BindingAppID scopes an explicit app binding budget. It is set from
	// verified workload identity, never from a guest-controlled header.
	BindingAppID                   string
	BindingDailyRequestLimit       *int64
	CircuitBreakerFailureThreshold int
	CircuitBreakerOpenSeconds      int
	RetryBudgetPerMinute           int
	LeaseTTL                       time.Duration
	// PolicyRevision is the resolved policy's revision. A backend that reads
	// the provider cooldown while it admits compares it with the stored one.
	PolicyRevision int64
}

// Decision describes an admission or a deterministic rejection. A granted
// decision always has a lease ID which must be released exactly once.
type Decision struct {
	Granted                        bool
	LeaseID                        string
	RetryAfter                     time.Duration
	Reason                         string
	RequestTimeout                 time.Duration
	CircuitBreakerFailureThreshold int
	CircuitBreakerOpenSeconds      int
	RetryBudgetPerMinute           int
	// CircuitClosed reports that the configured circuit breaker was closed
	// under this policy when the call was admitted, so the call needs no
	// separate breaker check (ADR-1120). False means "ask the breaker".
	CircuitClosed bool
	// ProviderGate is the provider cooldown as admission read it under the
	// call's policy revision, or nil when the caller must ask the cooldown
	// backend (ADR-1120).
	ProviderGate *ProviderCooldownDecision
}

// Backend is the shared state boundary. Implementations must fail closed on
// storage errors: callers should not send an upstream request without a grant.
type Backend interface {
	Admit(context.Context, AdmissionSpec) (Decision, error)
	Release(context.Context, string, string) error
}

// CircuitBreakerDecision is the shared per-integration upstream gate result.
// Probe is true only for the single half-open request admitted after cool-down.
type CircuitBreakerDecision struct {
	Allowed    bool
	Probe      bool
	RetryAfter time.Duration
}

type CircuitBreakerOutcome string

const (
	CircuitOutcomeSuccess CircuitBreakerOutcome = "success"
	CircuitOutcomeFailure CircuitBreakerOutcome = "failure"
	CircuitOutcomeNeutral CircuitBreakerOutcome = "neutral"
)

// CircuitBreakerBackend coordinates breaker state across gateway replicas.
// A configured breaker fails closed if the selected backend does not support
// this contract.
type CircuitBreakerBackend interface {
	AllowCircuit(context.Context, string, string, int, int) (CircuitBreakerDecision, error)
	RecordCircuitOutcome(context.Context, string, string, int, int, CircuitBreakerOutcome) error
}

// RetryBudgetBackend atomically consumes one shared token before each
// additional provider attempt. Backends coordinate all outboundd replicas.
type RetryBudgetBackend interface {
	ConsumeRetryToken(context.Context, string, int) (bool, error)
}

// ProviderCooldownDecision describes the shared provider-directed gate. A
// denied request never reaches the provider; RetryAfter is the remaining
// cooldown, measured by the backend's clock.
type ProviderCooldownDecision struct {
	Allowed    bool
	RetryAfter time.Duration
}

// ProviderCooldownBackend shares provider Retry-After state across gateway
// replicas. Implementations should extend, never shorten, an active cooldown.
type ProviderCooldownBackend interface {
	AllowProviderRequest(context.Context, string, int64) (ProviderCooldownDecision, error)
	RecordProviderCooldown(context.Context, string, int64, time.Duration) error
}

// StaticResolver is useful for a dedicated gateway process configured at
// startup. The API/state integration can replace it without changing the
// request path.
type StaticResolver struct {
	items map[string]Integration
}

func NewStaticResolver(items []Integration) (*StaticResolver, error) {
	r := &StaticResolver{items: make(map[string]Integration, len(items))}
	for _, item := range items {
		if err := item.Validate(); err != nil {
			return nil, err
		}
		copyItem := item
		copyItem.Origin = cloneURL(item.Origin)
		copyItem.AppIDs = make(map[string]struct{}, len(item.AppIDs))
		for appID := range item.AppIDs {
			copyItem.AppIDs[appID] = struct{}{}
		}
		copyItem.OperatorAppIDs = copyStringSet(item.OperatorAppIDs)
		copyItem.BindingAppIDs = copyStringSet(item.BindingAppIDs)
		copyItem.CustomerAppRoutes = copyRoutePolicies(item.CustomerAppRoutes)
		copyItem.DailyRequestLimit = copyInt64Pointer(item.DailyRequestLimit)
		copyItem.DailyAITokenLimit = copyInt64Pointer(item.DailyAITokenLimit)
		copyItem.BindingDailyAITokenLimits = copyInt64PointerMap(item.BindingDailyAITokenLimits)
		copyItem.DailyAICostLimit = copyInt64Pointer(item.DailyAICostLimit)
		copyItem.MonthlyAICostLimit = copyInt64Pointer(item.MonthlyAICostLimit)
		copyItem.AIPrices = append([]AIPrice(nil), item.AIPrices...)
		copyItem.AIUserDailyTokenLimit = copyInt64Pointer(item.AIUserDailyTokenLimit)
		copyItem.AIUserDailyCostLimit = copyInt64Pointer(item.AIUserDailyCostLimit)
		copyItem.BindingAIGuardrails = copyAIGuardrails(item.BindingAIGuardrails)
		copyItem.BindingDailyRequestLimits = copyInt64PointerMap(item.BindingDailyRequestLimits)
		copyItem.AllowedMethods = append([]string(nil), item.AllowedMethods...)
		copyItem.AllowedPathPrefixes = append([]string(nil), item.AllowedPathPrefixes...)
		r.items[item.ID] = copyItem
	}
	return r, nil
}

func (r *StaticResolver) Integration(ctx context.Context, id string) (Integration, error) {
	if err := ctx.Err(); err != nil {
		return Integration{}, err
	}
	i, ok := r.items[id]
	if !ok {
		return Integration{}, ErrIntegrationNotFound
	}
	if !i.Enabled {
		return Integration{}, ErrIntegrationDisabled
	}
	i.Origin = cloneURL(i.Origin)
	i.AppIDs = copyStringSet(i.AppIDs)
	i.OperatorAppIDs = copyStringSet(i.OperatorAppIDs)
	i.BindingAppIDs = copyStringSet(i.BindingAppIDs)
	i.CustomerAppRoutes = copyRoutePolicies(i.CustomerAppRoutes)
	i.DailyRequestLimit = copyInt64Pointer(i.DailyRequestLimit)
	i.DailyAITokenLimit = copyInt64Pointer(i.DailyAITokenLimit)
	i.BindingDailyAITokenLimits = copyInt64PointerMap(i.BindingDailyAITokenLimits)
	i.DailyAICostLimit = copyInt64Pointer(i.DailyAICostLimit)
	i.MonthlyAICostLimit = copyInt64Pointer(i.MonthlyAICostLimit)
	i.AIPrices = append([]AIPrice(nil), i.AIPrices...)
	i.AIUserDailyTokenLimit = copyInt64Pointer(i.AIUserDailyTokenLimit)
	i.AIUserDailyCostLimit = copyInt64Pointer(i.AIUserDailyCostLimit)
	i.BindingAIGuardrails = copyAIGuardrails(i.BindingAIGuardrails)
	i.BindingDailyRequestLimits = copyInt64PointerMap(i.BindingDailyRequestLimits)
	i.AllowedMethods = append([]string(nil), i.AllowedMethods...)
	i.AllowedPathPrefixes = append([]string(nil), i.AllowedPathPrefixes...)
	return i, nil
}

func copyInt64Pointer(in *int64) *int64 {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func copyInt64PointerMap(in map[string]*int64) map[string]*int64 {
	if in == nil {
		return nil
	}
	out := make(map[string]*int64, len(in))
	for key, value := range in {
		out[key] = copyInt64Pointer(value)
	}
	return out
}

func cloneURL(in *url.URL) *url.URL {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func copyStringSet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for key := range in {
		out[key] = struct{}{}
	}
	return out
}

func copyRoutePolicies(in map[string]RoutePolicy) map[string]RoutePolicy {
	out := make(map[string]RoutePolicy, len(in))
	for appID, policy := range in {
		out[appID] = RoutePolicy{
			AllowedMethods:      append([]string(nil), policy.AllowedMethods...),
			AllowedPathPrefixes: append([]string(nil), policy.AllowedPathPrefixes...),
		}
	}
	return out
}
