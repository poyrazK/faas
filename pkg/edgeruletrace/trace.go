// Package edgeruletrace contains the deterministic, read-only edge-rule
// request simulator shared by gregale and the dashboard.
package edgeruletrace

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgevalidate"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/redact"
)

const (
	// MaxTraceBodyBytes bounds request payloads accepted by the CLI and
	// dashboard simulator. It is intentionally lower than gateway limits so a
	// trace cannot consume unbounded memory or schema-validation time.
	MaxTraceBodyBytes = 1 << 20

	Scope = "Host/method/path/header matching is simulated. Per-rule rows show standalone matches against the submitted request; the sequential simulation composes deterministic actions in gateway phase order. Credential-like header values and recognizable token patterns are redacted from trace output after evaluation, so redaction does not affect rule matching. A supplied body is limited to 1 MiB and is evaluated only for validate, limit, and async-route eligibility; its contents are never included in the result. Limit rules use the supplied body size and app plan cap; when buffered and streaming caps would produce different outcomes, the trace stops as incomplete because gateway streaming context is unavailable. Inline and preset-backed edge-rule CORS and per-app default CORS are simulated from Origin, preflight request headers, app settings, and supplied preset data; preset-backed rules remain incomplete when preset data is unavailable or invalid, and default CORS is incomplete when app settings are unavailable. App-level maintenance is evaluated after routing and earlier gateway gates, before per-rule maintenance; it is incomplete when app metadata is unavailable. Declared-route policy is simulated from explicit routes or the app's imported OpenAPI document, using the original public path and method after CORS preflight handling. When a project and environment are selected, the trace uses that workload's effective declared-route policy and environment-owned headers/CORS edge-rule replacement; the separate per-app default CORS setting remains app-owned. Its URL host must be the environment workload URL or a verified environment domain. Without an environment selection, only app-owned policy is used. Cache-rule traces show the configured freshness/stale windows and Vary dimensions and identify deterministic method or credential bypasses; a possible lookup stops as incomplete because authentication, async/pinned-deployment context, and live cache contents determine the runtime result. When app budget metadata is available, budget traces report the matching rule or app/plan baseline, override-header handling, and plan ceiling; they do not predict elapsed time or a deadline outcome because the budget starts only after upload, wake, routing, and admission. Throttle-rule traces report configured rate, burst, keying, and plan/app/account limits when available, but stop before guessing identity resolution or the live token-bucket admission result. Retry-rule traces report effective attempts, backoff, request-budget floor, aggregate replay budget, and the deterministic method/idempotency-key guard; they do not predict a replay because the operator gate, transport failure, body replayability, remaining budget, healthy sibling, and live aggregate budget are runtime state. Ingress/auth policy, target-app rules after routing, circuit-breaker state, async enqueue state, wake, and backend response are not simulated. The trace also stops as incomplete where other runtime state or unavailable request context is required. A completed 'continue' outcome means inspected edge-rule phases did not terminate the request, not that the app will return successfully. IP and geo use supplied client_ip/country directly; trusted-proxy validation and live geo lookup are not performed. Equal-priority candidates have no guaranteed order. Results are limited to the named app."
)

const circuitBreakerScope = "Circuit-breaker rule traces report effective per-instance thresholds and open/backoff durations, but do not consult the operator feature gate or live per-instance breaker counters/state; target selection and half-open probe outcomes are not predicted."

const asyncRouteScope = "Async-route traces show effective retry/deadline policy, plan and workload gates, and supplied-body size/JSON eligibility without outputting body contents or idempotency-key values. Authentication, rate-limit admission, durable enqueue/version resolution, idempotency conflicts, queue capacity, dispatch, and callbacks are runtime-only; a candidate does not imply HTTP 202 or duplicate handling."

const jwtPolicyScope = "JWT-rule traces report bearer-token presence and a value-redacted policy summary. They never output the token, issuer, audience, JWKS URL, or required-claim values; JWKS retrieval, signature/claim verification, and earlier app authentication remain runtime-only. A missing bearer token is shown as a conditional 401 only if earlier gates let the request reach the JWT rule."

const redactedHeaderValue = "[REDACTED]"

var traceOutputRedactor = redact.New(1 << 20)

// Input is the request context that can be simulated without contacting the
// gateway or app runtime. Call NormalizeInput before Simulate.
type Input struct {
	Project     string
	Environment string
	App         string
	Host        string
	Path        string
	Method      string
	ClientIP    string
	Country     string
	Headers     http.Header
	Body        []byte
	// CorsPresets supplies caller-resolved presets for preset-backed CORS
	// rules. Missing or cross-account presets remain incomplete instead of
	// being guessed.
	CorsPresets []api.CorsPresetResponse
	// EdgeRuleLists supplies the account lists (with items) that rule
	// conditions reference through in_list (ADR-907). A referenced list
	// missing here never matches, as on the gateway.
	EdgeRuleLists []api.EdgeRuleListResponse
	// AppCORSDefaultsLoaded distinguishes a known-disabled app setting from
	// app metadata that was not available to the caller. When a request has an
	// Origin but no matching edge-rule CORS rule, missing app settings stop the
	// trace as incomplete instead of silently skipping the gateway fallback.
	AppCORSDefaultsLoaded bool
	CORSDefaultEnabled    *bool
	CORSDefaultOrigins    []string
	// AppMaintenanceLoaded distinguishes a known-disabled maintenance gate
	// from app metadata that was not available to the caller. Unlike CORS, this
	// app-wide gate applies to every request and is evaluated before edge-rule
	// maintenance and later edge-rule phases.
	AppMaintenanceLoaded bool
	AppMaintenanceMode   bool
	// OnlyAllowDeclaredRoutes enables the same pre-auth route gate used by the
	// gateway. Explicit routes take precedence over the imported OpenAPI doc.
	OnlyAllowDeclaredRoutes bool
	DeclaredRoutes          []api.DeclaredRoute
	// DeclaredRoutesLoaded distinguishes a known empty route allowlist from an
	// app policy that may still need its imported OpenAPI document.
	DeclaredRoutesLoaded bool
	// DeclaredRouteDocumentLoaded distinguishes a loaded OpenAPI document from
	// a caller that could not retrieve it. A missing document is tracked
	// separately because the gateway treats that configured policy as a 503.
	DeclaredRouteDocumentLoaded  bool
	DeclaredRouteDocumentMissing bool
	DeclaredRouteOpenAPIDoc      []byte
	// BodyProvided distinguishes an intentionally empty body from omitted
	// request-body context. Validate rules remain incomplete when omitted.
	BodyProvided bool
	// RequestBodyMaxBytes is the app's effective plan cap. Zero selects the
	// platform maximum for callers that do not have app metadata.
	RequestBodyMaxBytes int64
	// AppRequestBudgetLoaded distinguishes a known app budget envelope from a
	// caller that did not load the app's effective limits. RequestBudgetMS is
	// the type-aware plan baseline; RequestTimeoutS, when positive, overrides
	// that baseline and remains subject to RequestBudgetMaxMS.
	AppRequestBudgetLoaded bool
	RequestBudgetMS        int64
	RequestBudgetMaxMS     int64
	RequestTimeoutS        int
	// AppThrottleContextLoaded distinguishes known app/account request-rate
	// ceilings from callers that could not load effective app metadata.
	AppThrottleContextLoaded bool
	AppRequestRateRPS        int
	AppRequestRateBurst      int
	AccountRequestRateRPM    int
	// ThrottlePlanLimitsLoaded marks the plan validation ceiling available to
	// the trace. These limits constrain configured per-route throttle rules but
	// do not reveal the request's live token-bucket state.
	ThrottlePlanLimitsLoaded bool
	ThrottlePlanMaxRPS       int
	ThrottlePlanMaxBurst     int
	// AsyncPlanLimitsLoaded distinguishes a known plan gate and async caps
	// from a caller that could not resolve the account's current plan.
	AsyncPlanLimitsLoaded   bool
	AsyncPlan               api.Plan
	AsyncInvokeAllowed      bool
	AsyncMaxPayloadBytes    int
	AsyncMaxQueueAttempts   int
	AsyncMaxDeadlineSeconds int
	// AsyncWorkloadContextLoaded marks the app workload mode needed to know
	// whether it accepts HTTP-style request invocations.
	AsyncWorkloadContextLoaded     bool
	AsyncRequestInvocationsEnabled bool
	AsyncAppRetryPolicyLoaded      bool
	AsyncAppRetryPolicy            *api.RetryPolicyDTO
}

type Result struct {
	Project      string              `json:"project,omitempty"`
	Environment  string              `json:"environment,omitempty"`
	App          string              `json:"app"`
	Host         string              `json:"host"`
	Path         string              `json:"path"`
	Method       string              `json:"method"`
	ClientIP     string              `json:"client_ip,omitempty"`
	Country      string              `json:"country,omitempty"`
	Headers      map[string][]string `json:"headers,omitempty"`
	BodyProvided bool                `json:"body_provided"`
	BodyBytes    int                 `json:"body_bytes,omitempty"`
	Scope        string              `json:"scope"`
	Rules        []RuleRow           `json:"rules"`
	Simulation   Simulation          `json:"simulation"`
}

type Simulation struct {
	Status            string                 `json:"status"`
	Outcome           string                 `json:"outcome"`
	ProblemCode       string                 `json:"problem_code,omitempty"`
	FinalPath         string                 `json:"final_path"`
	RequestHeaders    map[string][]string    `json:"request_headers,omitempty"`
	ResponseHeaderOps []api.EdgeRuleHeaderOp `json:"response_header_ops,omitempty"`
	StatusCode        int                    `json:"status_code,omitempty"`
	Location          string                 `json:"location,omitempty"`
	RedirectHeaders   map[string]string      `json:"redirect_headers,omitempty"`
	RetryAfterSeconds int                    `json:"retry_after_seconds,omitempty"`
	Message           string                 `json:"message,omitempty"`
	TargetApp         string                 `json:"target_app,omitempty"`
	Body              json.RawMessage        `json:"body,omitempty"`
	StoppedAt         string                 `json:"stopped_at,omitempty"`
	Reason            string                 `json:"reason"`
	Steps             []SimulationStep       `json:"steps"`
}

type SimulationStep struct {
	Phase                string                       `json:"phase"`
	RuleID               string                       `json:"rule_id,omitempty"`
	Kind                 string                       `json:"kind,omitempty"`
	Outcome              string                       `json:"outcome"`
	PathBefore           string                       `json:"path_before,omitempty"`
	PathAfter            string                       `json:"path_after,omitempty"`
	StatusCode           int                          `json:"status_code,omitempty"`
	ProblemCode          string                       `json:"problem_code,omitempty"`
	Location             string                       `json:"location,omitempty"`
	RedirectHeaders      map[string]string            `json:"redirect_headers,omitempty"`
	RetryAfterSeconds    int                          `json:"retry_after_seconds,omitempty"`
	Message              string                       `json:"message,omitempty"`
	TargetApp            string                       `json:"target_app,omitempty"`
	ValidationField      string                       `json:"validation_field,omitempty"`
	ValidationKeyword    string                       `json:"validation_keyword,omitempty"`
	RequestOps           []api.EdgeRuleHeaderOp       `json:"request_header_ops,omitempty"`
	ResponseOps          []api.EdgeRuleHeaderOp       `json:"response_header_ops,omitempty"`
	CachePolicy          *CachePolicyPreview          `json:"cache_policy,omitempty"`
	BudgetPolicy         *BudgetPolicyPreview         `json:"budget_policy,omitempty"`
	ThrottlePolicy       *ThrottlePolicyPreview       `json:"throttle_policy,omitempty"`
	RetryPolicy          *RetryPolicyPreview          `json:"retry_policy,omitempty"`
	CircuitBreakerPolicy *CircuitBreakerPolicyPreview `json:"circuit_breaker_policy,omitempty"`
	AsyncPolicy          *AsyncPolicyPreview          `json:"async_policy,omitempty"`
	JWTPolicy            *JWTPolicyPreview            `json:"jwt_policy,omitempty"`
	Reason               string                       `json:"reason"`
}

type RuleRow struct {
	ID            string            `json:"id"`
	Kind          string            `json:"kind"`
	Priority      int               `json:"priority"`
	MatchHost     string            `json:"match_host"`
	MatchPath     string            `json:"match_path"`
	MatchMethods  []string          `json:"match_methods"`
	MatchHeaders  map[string]string `json:"match_headers,omitempty"`
	Status        string            `json:"status"`
	Reason        string            `json:"reason"`
	Outcome       string            `json:"outcome"`
	OutcomeReason string            `json:"outcome_reason"`
	ActionPreview *ActionPreview    `json:"action_preview,omitempty"`
	PrecededBy    string            `json:"preceded_by,omitempty"`
}

type ActionPreview struct {
	Type                 string                       `json:"type"`
	TargetApp            string                       `json:"target_app,omitempty"`
	Path                 string                       `json:"path,omitempty"`
	StatusCode           int                          `json:"status_code,omitempty"`
	Location             string                       `json:"location,omitempty"`
	RedirectHeaders      map[string]string            `json:"redirect_headers,omitempty"`
	RequestHeaderOps     []api.EdgeRuleHeaderOp       `json:"request_header_ops,omitempty"`
	ResponseHeaderOps    []api.EdgeRuleHeaderOp       `json:"response_header_ops,omitempty"`
	RetryAfterSeconds    int                          `json:"retry_after_seconds,omitempty"`
	Message              string                       `json:"message,omitempty"`
	ValidationField      string                       `json:"validation_field,omitempty"`
	ValidationKeyword    string                       `json:"validation_keyword,omitempty"`
	Body                 json.RawMessage              `json:"body,omitempty"`
	CachePolicy          *CachePolicyPreview          `json:"cache_policy,omitempty"`
	BudgetPolicy         *BudgetPolicyPreview         `json:"budget_policy,omitempty"`
	ThrottlePolicy       *ThrottlePolicyPreview       `json:"throttle_policy,omitempty"`
	RetryPolicy          *RetryPolicyPreview          `json:"retry_policy,omitempty"`
	CircuitBreakerPolicy *CircuitBreakerPolicyPreview `json:"circuit_breaker_policy,omitempty"`
	AsyncPolicy          *AsyncPolicyPreview          `json:"async_policy,omitempty"`
	JWTPolicy            *JWTPolicyPreview            `json:"jwt_policy,omitempty"`
}

// JWTPolicyPreview exposes only safe, non-value policy metadata. It never
// includes bearer-token or configured issuer/audience/JWKS/claim values.
type JWTPolicyPreview struct {
	BearerTokenPresent                       bool     `json:"bearer_token_present"`
	IssuerConfigured                         bool     `json:"issuer_configured"`
	JWKSConfigured                           bool     `json:"jwks_configured"`
	AudienceCount                            int      `json:"audience_count"`
	Algorithms                               []string `json:"algorithms,omitempty"`
	RequiredClaimNames                       []string `json:"required_claim_names,omitempty"`
	PlatformTenantExternalRefClaimConfigured bool     `json:"platform_tenant_external_ref_claim_configured"`
}

// CachePolicyPreview contains the deterministic request-side cache policy
// that can be shown without consulting gateway cache state. A lookup
// candidate is not a predicted hit: authentication, async/pinned-deployment
// context, and the live cache store can still alter runtime behavior.
type CachePolicyPreview struct {
	Methods                     []string `json:"methods"`
	MaxAgeSeconds               int      `json:"max_age_seconds"`
	StaleWhileRevalidateSeconds int      `json:"stale_while_revalidate_seconds"`
	StaleIfErrorSeconds         int      `json:"stale_if_error_seconds"`
	VaryOn                      []string `json:"vary_on,omitempty"`
	RequestGate                 string   `json:"request_gate"`
}

// BudgetPolicyPreview reports the configured and effective request budget
// without claiming that guest execution will actually consume the full
// duration. The runtime starts this deadline only after upload, wake, routing,
// and per-VM admission have completed.
type BudgetPolicyPreview struct {
	ConfiguredMS   int64  `json:"configured_ms"`
	BudgetMS       int64  `json:"budget_ms"`
	PlanMaxMS      int64  `json:"plan_max_ms"`
	Source         string `json:"source"`
	OverrideHeader string `json:"override_header,omitempty"`
	OverrideStatus string `json:"override_status"`
}

// ThrottlePolicyPreview reports configured per-route throttle behavior and
// known outer rate ceilings. The trace does not resolve authenticated or
// trusted-geolocation identities and never consults or consumes a live bucket.
type ThrottlePolicyPreview struct {
	RequestsPerSecond float64  `json:"requests_per_second"`
	Burst             int      `json:"burst"`
	GatewayRateRPS    float64  `json:"gateway_rate_rps"`
	GatewayBurst      int      `json:"gateway_burst"`
	KeyBy             string   `json:"key_by"`
	JWTClaimName      string   `json:"jwt_claim_name,omitempty"`
	MaxKeysPerRule    int      `json:"max_keys_per_rule"`
	MaxKeysSource     string   `json:"max_keys_source"`
	MissingKeyPolicy  string   `json:"missing_key_policy"`
	KeyFields         []string `json:"key_fields,omitempty"`
	CountStatuses     []int    `json:"count_statuses,omitempty"`
	PlanCeilingStatus string   `json:"plan_ceiling_status"`
	PlanMaxRPS        int      `json:"plan_max_rps,omitempty"`
	PlanMaxBurst      int      `json:"plan_max_burst,omitempty"`
	AppRequestRPS     int      `json:"app_request_rps,omitempty"`
	AppRequestBurst   int      `json:"app_request_burst,omitempty"`
	AccountRequestRPM int      `json:"account_request_rpm,omitempty"`
}

// RetryPolicyPreview reports a matching rule's effective replay policy and
// the deterministic HTTP-method/idempotency-key guard. It never predicts a
// replay: only transport failures can arm one, and the operator gate, live
// budget, body replayability, and healthy-target state are outside the trace.
type RetryPolicyPreview struct {
	MaxAttempts           int    `json:"max_attempts"`
	MaxReplays            int    `json:"max_replays"`
	MaxAttemptsSource     string `json:"max_attempts_source"`
	AllowNonIdempotent    bool   `json:"allow_non_idempotent"`
	MethodEligibility     string `json:"method_eligibility"`
	IdempotencyKeyPresent bool   `json:"idempotency_key_present"`
	MinRemainingMS        int    `json:"min_remaining_ms"`
	BackoffMS             int    `json:"backoff_ms"`
	BudgetPercent         int    `json:"budget_percent"`
	BudgetMinRetries      int    `json:"budget_min_retries"`
}

// CircuitBreakerPolicyPreview reports the effective configured policy for a
// matching edge rule. The operator gate and per-instance breaker state are
// runtime-only, so this preview never predicts target selection or a probe.
type CircuitBreakerPolicyPreview struct {
	FailureThreshold float64 `json:"failure_threshold"`
	MinRequests      int     `json:"min_requests"`
	WindowSeconds    int     `json:"window_seconds"`
	OpenSeconds      int     `json:"open_seconds"`
	MaxOpenSeconds   int     `json:"max_open_seconds"`
}

// AsyncPolicyPreview describes the configured durable-route policy and the
// request facts available to the simulator. A candidate still requires live
// auth/rate-limit gates and a successful durable enqueue before it can return
// 202.
type AsyncPolicyPreview struct {
	OnSuccessConfigured     bool    `json:"on_success_configured"`
	OnFailureConfigured     bool    `json:"on_failure_configured"`
	PlanGate                string  `json:"plan_gate"`
	WorkloadGate            string  `json:"workload_gate"`
	PayloadStatus           string  `json:"payload_status"`
	MaxPayloadBytes         int     `json:"max_payload_bytes,omitempty"`
	ConfiguredMaxAgeSeconds int     `json:"configured_max_age_seconds"`
	EffectiveMaxAgeSeconds  int     `json:"effective_max_age_seconds,omitempty"`
	PlanMaxAgeSeconds       int     `json:"plan_max_age_seconds,omitempty"`
	MaxAgeSource            string  `json:"max_age_source"`
	RetryPolicySource       string  `json:"retry_policy_source"`
	ConfiguredMaxAttempts   int     `json:"configured_max_attempts,omitempty"`
	EffectiveMaxAttempts    int     `json:"effective_max_attempts,omitempty"`
	MaxReplays              int     `json:"max_replays,omitempty"`
	PlanMaxAttempts         int     `json:"plan_max_attempts,omitempty"`
	MaxAttemptsStatus       string  `json:"max_attempts_status"`
	RetryBaseSeconds        float64 `json:"retry_base_seconds,omitempty"`
	RetryMaxSeconds         float64 `json:"retry_max_seconds,omitempty"`
	RetryJitterSeconds      float64 `json:"retry_jitter_seconds,omitempty"`
	IdempotencyKeyPresent   bool    `json:"idempotency_key_present"`
	RequestGate             string  `json:"request_gate"`
}

// NormalizeInput validates user-supplied request context and canonicalizes
// only the same fields the CLI has historically normalized (method, host,
// client IP, and country). Header value comparisons remain exact.
func NormalizeInput(input Input) (Input, error) {
	input.Project = strings.TrimSpace(input.Project)
	input.Environment = strings.TrimSpace(input.Environment)
	if (input.Project == "") != (input.Environment == "") {
		return Input{}, fmt.Errorf("project and environment must be supplied together")
	}
	if input.Project != "" && !api.ValidProjectSlug(input.Project) {
		return Input{}, fmt.Errorf("project slug is invalid")
	}
	if input.Environment != "" && !api.ValidProjectEnvironmentSlug(input.Environment) {
		return Input{}, fmt.Errorf("environment slug is invalid")
	}
	input.App = strings.TrimSpace(input.App)
	if input.App == "" || len(input.App) > 40 {
		return Input{}, fmt.Errorf("app slug is required and must be at most 40 characters")
	}
	input.Host = strings.ToLower(strings.TrimSpace(input.Host))
	if !validRequestHost(input.Host) {
		return Input{}, fmt.Errorf("host must be a hostname or IP address without a port")
	}
	if input.Path == "" {
		input.Path = "/"
	}
	if len(input.Body) > MaxTraceBodyBytes {
		return Input{}, fmt.Errorf("request body must not exceed %d bytes", MaxTraceBodyBytes)
	}
	if len(input.Body) > 0 {
		input.BodyProvided = true
	}
	if input.RequestBodyMaxBytes <= 0 {
		input.RequestBodyMaxBytes = api.MaxRequestBodyBytes
	}
	if !strings.HasPrefix(input.Path, "/") || len(input.Path) > 2048 {
		return Input{}, fmt.Errorf("path must start with / and be at most 2048 characters")
	}
	input.Method = strings.ToUpper(strings.TrimSpace(input.Method))
	if input.Method == "" {
		input.Method = http.MethodGet
	}
	if _, err := http.NewRequest(input.Method, "http://trace.invalid/", nil); err != nil {
		return Input{}, fmt.Errorf("invalid HTTP method")
	}
	if input.ClientIP != "" {
		ip := net.ParseIP(strings.TrimSpace(input.ClientIP))
		if ip == nil {
			return Input{}, fmt.Errorf("client IP must be an IPv4 or IPv6 address")
		}
		input.ClientIP = ip.String()
	}
	input.Country = strings.ToUpper(strings.TrimSpace(input.Country))
	if input.Country != "" && !validCountry(input.Country) {
		return Input{}, fmt.Errorf("country must be a two-letter ISO country code")
	}
	headers, err := normalizeRequestHeaders(input.Headers)
	if err != nil {
		return Input{}, err
	}
	input.Headers = headers
	return input, nil
}

// ApplyEnvironmentRoutePolicy overlays an explicitly selected environment's
// route contract on the app metadata used by the trace. The environment
// contract owns even an empty allowlist; it must not fall through to the
// app's imported OpenAPI document.
func ApplyEnvironmentRoutePolicy(input Input, policy api.ProjectEnvironmentRoutePolicyResponse) (Input, error) {
	switch policy.Ownership {
	case "application":
		return input, nil
	case "environment":
		input.OnlyAllowDeclaredRoutes = policy.OnlyAllowDeclaredRoutes
		input.DeclaredRoutes = append([]api.DeclaredRoute(nil), policy.DeclaredRoutes...)
		input.DeclaredRoutesLoaded = true
		input.DeclaredRouteDocumentLoaded = false
		input.DeclaredRouteDocumentMissing = false
		input.DeclaredRouteOpenAPIDoc = nil
		return input, nil
	default:
		return Input{}, fmt.Errorf("environment route policy ownership %q is unsupported", policy.Ownership)
	}
}

// ApplyEnvironmentEdgePolicy overlays environment-owned headers/CORS rules on
// app rules, matching gateway replacement semantics. Other edge-rule kinds
// remain app-owned. The selected request host must belong to this environment
// workload so a trace cannot accidentally apply staging policy to production.
func ApplyEnvironmentEdgePolicy(input Input, rules []api.EdgeRuleResponse, policy api.ProjectEnvironmentEdgePolicyResponse, workloadURL string, domains []api.ProjectEnvironmentDomainResponse) ([]api.EdgeRuleResponse, error) {
	if input.Project == "" || input.Environment == "" {
		return nil, fmt.Errorf("project and environment are required to apply environment policy")
	}
	if !environmentHostMatches(input.Host, workloadURL, domains) {
		return nil, fmt.Errorf("request host %q is not the stable URL or a verified custom domain for project %q environment %q app %q", input.Host, input.Project, input.Environment, input.App)
	}
	switch policy.Ownership {
	case "application":
		return append([]api.EdgeRuleResponse(nil), rules...), nil
	case "environment":
		out := make([]api.EdgeRuleResponse, 0, len(rules)+len(policy.Rules))
		for _, rule := range rules {
			if rule.Kind != "headers" && rule.Kind != "cors" {
				out = append(out, rule)
			}
		}
		for i, rule := range policy.Rules {
			if rule.Kind != "headers" && rule.Kind != "cors" {
				return nil, fmt.Errorf("environment policy contains unsupported edge-rule kind %q", rule.Kind)
			}
			out = append(out, api.EdgeRuleResponse{
				ID:      fmt.Sprintf("environment/%s/%s/%d", input.Environment, input.App, i+1),
				Enabled: rule.Enabled, Kind: rule.Kind, MatchHost: input.Host,
				MatchPath: rule.MatchPath, MatchMethods: append([]string(nil), rule.MatchMethods...),
				MatchHeaders: cloneStringMap(rule.MatchHeaders), Priority: rule.Priority,
				Action: append(json.RawMessage(nil), rule.Action...),
			})
		}
		return out, nil
	default:
		return nil, fmt.Errorf("environment edge-policy ownership %q is unsupported", policy.Ownership)
	}
}

func environmentHostMatches(host, workloadURL string, domains []api.ProjectEnvironmentDomainResponse) bool {
	if workloadURL != "" {
		parsed, err := url.Parse(workloadURL)
		if err == nil && parsed != nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && strings.EqualFold(parsed.Hostname(), host) {
			return true
		}
	}
	for _, domain := range domains {
		if !domain.Verified {
			continue
		}
		candidate := strings.ToLower(strings.TrimSuffix(domain.Domain, "."))
		requestHost := strings.ToLower(host)
		if candidate == requestHost {
			return true
		}
		if strings.HasPrefix(candidate, "*.") {
			suffix := candidate[1:]
			if requestHost != candidate[2:] && strings.HasSuffix(requestHost, suffix) {
				return true
			}
		}
	}
	return false
}

// ParseRequestHeaders parses repeated Name:Value inputs. It trims HTTP optional
// whitespace around each field value as a wire parser does, retains repeated
// values, and compares the resulting bytes exactly as edge-rule selectors do.
func ParseRequestHeaders(items []string) (http.Header, error) {
	headers := make(http.Header)
	for _, raw := range items {
		index := strings.IndexByte(raw, ':')
		if index < 1 {
			return nil, fmt.Errorf("%q: expected Name:Value", raw)
		}
		name, value := raw[:index], raw[index+1:]
		value = strings.Trim(value, " \t")
		normalized, err := api.NormalizeEdgeRuleMatchHeaders(map[string]string{name: value})
		if err != nil {
			return nil, err
		}
		for headerName := range normalized {
			headers.Add(headerName, value)
		}
	}
	return headers, nil
}

// Simulate evaluates the named app's edge rules without side effects.
func Simulate(input Input, rules []api.EdgeRuleResponse) (Result, error) {
	normalized, err := NormalizeInput(input)
	if err != nil {
		return Result{}, err
	}
	return previewNormalized(normalized, rules), nil
}

// RequiresCorsPresetData reports whether any CORS rule references a preset so
// callers can avoid fetching preset configuration for inline-only traces.
func RequiresCorsPresetData(rules []api.EdgeRuleResponse) bool {
	for _, rule := range rules {
		if !rule.Enabled || rule.Kind != "cors" {
			continue
		}
		action, ok := decodeAction[api.EdgeRuleCORSAction](rule.Action, "cors")
		if ok && action.CorsPresetID != nil {
			return true
		}
	}
	return false
}

// RequiresAppCORSDefaultData reports whether a trace request includes an
// Origin header. The app-level CORS fallback only has an effect for such a
// request, so callers need not load app metadata for ordinary requests.
func RequiresAppCORSDefaultData(headers http.Header) bool {
	return headers.Get("Origin") != ""
}

func previewNormalized(input Input, rules []api.EdgeRuleResponse) Result {
	sorted := append([]api.EdgeRuleResponse(nil), rules...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Priority != sorted[j].Priority {
			return sorted[i].Priority < sorted[j].Priority
		}
		if !sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
			return sorted[i].CreatedAt.Before(sorted[j].CreatedAt)
		}
		return sorted[i].ID < sorted[j].ID
	})
	// The gateway does not load expired rules; evaluate them as disabled
	// (the per-rule row still reports the expiry as the skip reason).
	now := time.Now()
	for i := range sorted {
		if sorted[i].ExpiresAt != nil && !now.Before(*sorted[i].ExpiresAt) {
			sorted[i].Enabled = false
		}
	}
	result := Result{
		Project: input.Project, Environment: input.Environment,
		App: input.App, Host: input.Host, Path: input.Path, Method: input.Method,
		ClientIP: input.ClientIP, Country: input.Country,
		BodyProvided: input.BodyProvided, BodyBytes: len(input.Body),
		Headers: headerSnapshot(input.Headers), Scope: Scope + " " + circuitBreakerScope + " " + asyncRouteScope + " " + jwtPolicyScope,
		Rules: make([]RuleRow, 0, len(sorted)),
	}
	firstByKind := make(map[string]int)
	for _, rule := range sorted {
		matchMethod := input.Method
		requestedMethod := ""
		if rule.Kind == "cors" {
			matchMethod, requestedMethod = corsMatchMethod(input.Method, input.Headers)
		}
		row := RuleRow{
			ID: rule.ID, Kind: rule.Kind, Priority: rule.Priority,
			MatchHost: rule.MatchHost, MatchPath: rule.MatchPath,
			MatchMethods: rule.MatchMethods, MatchHeaders: cloneStringMap(rule.MatchHeaders),
		}
		switch {
		case rule.ExpiresAt != nil && !time.Now().Before(*rule.ExpiresAt):
			row.Status, row.Reason = "skipped", "rule expired at "+rule.ExpiresAt.UTC().Format(time.RFC3339)
		case !rule.Enabled:
			row.Status, row.Reason = "skipped", "rule is disabled"
		case !HostMatches(rule.MatchHost, input.Host):
			row.Status, row.Reason = "skipped", fmt.Sprintf("host %q does not match %q", input.Host, rule.MatchHost)
		case !ruleMethodMatches(rule.Kind, rule.MatchMethods, matchMethod):
			row.Status, row.Reason = "skipped", fmt.Sprintf("method %q is not in %s", matchMethod, strings.Join(rule.MatchMethods, ", "))
		case !api.EdgeRuleRequestHeadersMatch(rule.MatchHeaders, input.Headers):
			row.Status, row.Reason = "skipped", headerMismatch(rule.MatchHeaders, input.Headers)
		case !traceConditionMatches(rule, input, input.Path, matchMethod, input.Headers):
			row.Status, row.Reason = "skipped", "match condition is false for this request"
		default:
			matched, matchErr := true, error(nil)
			if rule.MatchPath != "" && rule.MatchPath != "*" {
				matched, matchErr = api.MatchEdgeRuleKindPath(rule.Kind, rule.MatchPath, input.Path)
			}
			switch {
			case matchErr != nil:
				row.Status, row.Reason = "skipped", fmt.Sprintf("invalid path glob %q: %v", rule.MatchPath, matchErr)
			case !matched:
				row.Status, row.Reason = "skipped", fmt.Sprintf("path %q does not match %q", input.Path, rule.MatchPath)
			case rule.Mode == api.EdgeRuleModeLog:
				// ADR-904: matched and counted, but never enforced and never
				// a candidate that shadows enforced rules of its kind.
				row.Status, row.Reason = "logged", matchedSelectors(rule)+"; log mode: counted, not enforced"
			default:
				if firstIndex, seen := firstByKind[rule.Kind]; seen {
					first := &result.Rules[firstIndex]
					if first.Priority == rule.Priority {
						if first.Status != "tied_candidate" {
							first.Status = "tied_candidate"
							first.Reason = strings.TrimSuffix(first.Reason, "; first candidate of this kind") + "; equal-priority candidates have no guaranteed order"
						}
						row.Status, row.Reason = "tied_candidate", matchedSelectors(rule)+"; equal-priority candidates have no guaranteed order"
					} else {
						row.Status, row.PrecededBy = "later_candidate", first.ID
						row.Reason = fmt.Sprintf("%s; higher-priority %s candidate %s has precedence", matchedSelectors(rule), rule.Kind, row.PrecededBy)
					}
				} else {
					row.Status, row.Reason = "first_candidate", matchedSelectors(rule)+"; first candidate of this kind"
					firstByKind[rule.Kind] = len(result.Rules)
				}
			}
		}
		if rule.Kind == "cors" && row.Status != "skipped" && requestedMethod != "" {
			row.Reason += fmt.Sprintf("; preflight requests %s", requestedMethod)
		}
		row.Outcome, row.OutcomeReason, row.ActionPreview = previewAction(rule, row, input, input.Path)
		result.Rules = append(result.Rules, row)
	}
	result.Simulation = simulateRequest(input, sorted)
	redactTraceOutput(&result)
	return result
}

func simulateRequest(input Input, rules []api.EdgeRuleResponse) Simulation {
	simulation := Simulation{
		Status: "complete", Outcome: "continue", FinalPath: input.Path,
		RequestHeaders: headerSnapshot(cloneHeaders(input.Headers)),
		Reason:         "no simulated edge-rule action terminated the request; downstream app and gateway behavior is outside this simulation",
		Steps:          make([]SimulationStep, 0, 7),
	}
	workingHeaders := cloneHeaders(input.Headers)
	requestPath := input.Path
	stop := func(status, outcome, phase, reason string, rule *api.EdgeRuleResponse) Simulation {
		simulation.Status = status
		simulation.Outcome = outcome
		simulation.StoppedAt = phase
		simulation.Reason = reason
		step := SimulationStep{Phase: phase, Outcome: outcome, Reason: reason}
		if rule != nil {
			step.RuleID, step.Kind = rule.ID, rule.Kind
		}
		simulation.Steps = append(simulation.Steps, step)
		simulation.FinalPath = requestPath
		simulation.RequestHeaders = headerSnapshot(workingHeaders)
		return simulation
	}

	phases := []string{"route", "app_maintenance", "maintenance", "redirect", "rewrite", "headers", "cors", "declared_routes", "jwt", "ip", "geo", "limit", "throttle", "validate", "respond", "async", "cache", "budget", "circuit_breaker", "retry"}
	for _, phase := range phases {
		if phase == "app_maintenance" {
			if !input.AppMaintenanceLoaded {
				stopped := stop("incomplete", "needs_app_maintenance", phase, "app maintenance settings were not loaded, so the gateway-wide gate cannot be evaluated", nil)
				stopped.Steps[len(stopped.Steps)-1].Kind = phase
				return stopped
			}
			if input.AppMaintenanceMode {
				retryAfter := api.EdgeRuleMaintenanceRetryAfterSeconds
				problem := api.ErrAppMaintenanceMode(retryAfter, input.App)
				message := problem.Detail
				reason := fmt.Sprintf("when this gate is reached after earlier routing and gateway gates, app-wide maintenance would return HTTP %d with Retry-After: %d before per-rule maintenance and later edge-rule phases", problem.Status, retryAfter)
				step := SimulationStep{
					Phase: phase, Kind: phase, Outcome: phase,
					PathBefore: requestPath, PathAfter: requestPath,
					StatusCode: problem.Status, ProblemCode: problem.Code,
					RetryAfterSeconds: retryAfter, Message: message, Reason: reason,
				}
				simulation.Status, simulation.Outcome = "complete", phase
				simulation.ProblemCode = problem.Code
				simulation.StatusCode, simulation.RetryAfterSeconds = problem.Status, retryAfter
				simulation.Message, simulation.StoppedAt, simulation.Reason = message, phase, reason
				simulation.Steps = append(simulation.Steps, step)
				simulation.FinalPath, simulation.RequestHeaders = requestPath, headerSnapshot(workingHeaders)
				return simulation
			}
			continue
		}
		if phase == "declared_routes" {
			if !input.OnlyAllowDeclaredRoutes {
				continue
			}
			allowed, unavailable, loaded := declaredRouteAllowed(input, input.Path, input.Method)
			if !loaded {
				stopped := stop("incomplete", "needs_declared_route_policy", phase, "declared-route policy data was not loaded, so the gateway route gate cannot be evaluated", nil)
				stopped.Steps[len(stopped.Steps)-1].Kind = phase
				return stopped
			}
			if unavailable {
				message := "the configured OpenAPI document or route list could not be loaded"
				reason := fmt.Sprintf("the gateway would return HTTP %d because the enabled declared-route policy is unavailable", http.StatusServiceUnavailable)
				stopped := stop("complete", "declared_route_policy_unavailable", phase, reason, nil)
				stopped.ProblemCode, stopped.StatusCode, stopped.Message = api.CodeDeclaredRoutePolicyUnavailable, http.StatusServiceUnavailable, message
				step := &stopped.Steps[len(stopped.Steps)-1]
				step.Kind, step.StatusCode, step.ProblemCode, step.Message = phase, http.StatusServiceUnavailable, api.CodeDeclaredRoutePolicyUnavailable, message
				return stopped
			}
			if !allowed {
				message := fmt.Sprintf("%s %s is not declared for this app", input.Method, input.Path)
				reason := fmt.Sprintf("the original public request does not match the enabled app route contract; the gateway would return HTTP %d before authentication or wake", http.StatusNotFound)
				stopped := stop("complete", "undeclared_route", phase, reason, nil)
				stopped.ProblemCode, stopped.StatusCode, stopped.Message = api.CodeUndeclaredRoute, http.StatusNotFound, message
				step := &stopped.Steps[len(stopped.Steps)-1]
				step.Kind, step.PathBefore, step.PathAfter = phase, input.Path, requestPath
				step.StatusCode, step.ProblemCode, step.Message = http.StatusNotFound, api.CodeUndeclaredRoute, message
				return stopped
			}
			simulation.Steps = append(simulation.Steps, SimulationStep{
				Phase: phase, Kind: phase, Outcome: "allowed",
				PathBefore: input.Path, PathAfter: requestPath,
				Reason: "the original public request matches the declared route contract; request continues to authentication and later gateway gates",
			})
			continue
		}
		matchMethod := input.Method
		if phase == "cors" {
			matchMethod, _ = corsMatchMethod(input.Method, workingHeaders)
		}
		rule, tied := firstPhaseRule(rules, phase, input, requestPath, matchMethod, workingHeaders)
		if tied {
			return stop("incomplete", "ambiguous", phase, "equal-priority matching rules have no guaranteed evaluation order", rule)
		}
		if rule == nil {
			if phase == "budget" && hasAppRequestBudget(input) {
				policy := resolveBudgetPolicy(input, nil, workingHeaders)
				reason := budgetPolicyReason(policy)
				simulation.Steps = append(simulation.Steps, SimulationStep{
					Phase: phase, Kind: phase, Outcome: "budget_candidate",
					PathBefore: requestPath, PathAfter: requestPath,
					BudgetPolicy: &policy, Reason: reason,
				})
				simulation.Reason = "deterministic policy checks did not terminate the request; the reported budget applies only if the request reaches guest forwarding, and no elapsed-time or deadline outcome is predicted"
				continue
			}
			if phase == "budget" {
				// Budget policy is app-scoped, so callers that do not load
				// app metadata cannot report the plan/app fallback. If a
				// budget rule did match, the rule branch below stops as
				// incomplete rather than guessing its plan ceiling.
				continue
			}
			if phase == "cors" && RequiresAppCORSDefaultData(workingHeaders) {
				if !input.AppCORSDefaultsLoaded {
					stopped := stop("incomplete", "needs_app_cors_defaults", phase, "app CORS defaults were not loaded, so the gateway fallback cannot be evaluated", nil)
					stopped.Steps[len(stopped.Steps)-1].Kind = "app_default_cors"
					return stopped
				}
				step, responseOps := previewAppCORSDefault(input, workingHeaders)
				step.PathBefore, step.PathAfter = requestPath, requestPath
				simulation.Steps = append(simulation.Steps, step)
				simulation.ResponseHeaderOps = append(simulation.ResponseHeaderOps, responseOps...)
			}
			continue
		}
		row := RuleRow{Status: "first_candidate"}
		actionInput := input
		actionInput.Headers = workingHeaders
		outcome, reason, preview := previewAction(*rule, row, actionInput, requestPath)
		step := SimulationStep{Phase: phase, RuleID: rule.ID, Kind: phase, Outcome: outcome, PathBefore: requestPath, Reason: reason}
		if preview != nil {
			step.StatusCode, step.Location, step.TargetApp = preview.StatusCode, preview.Location, preview.TargetApp
			step.RedirectHeaders = cloneStringMap(preview.RedirectHeaders)
			step.RetryAfterSeconds, step.Message = preview.RetryAfterSeconds, preview.Message
			step.ValidationField, step.ValidationKeyword = preview.ValidationField, preview.ValidationKeyword
			step.RequestOps = append([]api.EdgeRuleHeaderOp(nil), preview.RequestHeaderOps...)
			step.ResponseOps = append([]api.EdgeRuleHeaderOp(nil), preview.ResponseHeaderOps...)
			if preview.CachePolicy != nil {
				cachePolicy := *preview.CachePolicy
				cachePolicy.Methods = append([]string(nil), preview.CachePolicy.Methods...)
				cachePolicy.VaryOn = append([]string(nil), preview.CachePolicy.VaryOn...)
				step.CachePolicy = &cachePolicy
			}
			if preview.BudgetPolicy != nil {
				budgetPolicy := *preview.BudgetPolicy
				step.BudgetPolicy = &budgetPolicy
			}
			if preview.ThrottlePolicy != nil {
				throttlePolicy := *preview.ThrottlePolicy
				step.ThrottlePolicy = &throttlePolicy
			}
			if preview.RetryPolicy != nil {
				retryPolicy := *preview.RetryPolicy
				step.RetryPolicy = &retryPolicy
			}
			if preview.CircuitBreakerPolicy != nil {
				circuitBreakerPolicy := *preview.CircuitBreakerPolicy
				step.CircuitBreakerPolicy = &circuitBreakerPolicy
			}
			if preview.AsyncPolicy != nil {
				asyncPolicy := *preview.AsyncPolicy
				step.AsyncPolicy = &asyncPolicy
			}
			if preview.JWTPolicy != nil {
				step.JWTPolicy = cloneJWTPolicyPreview(preview.JWTPolicy)
			}
		}
		if phase == "async" && step.AsyncPolicy != nil && outcome != "unavailable" {
			step.PathAfter = requestPath
			simulation.Steps = append(simulation.Steps, step)
			simulation.FinalPath = requestPath
			simulation.RequestHeaders = headerSnapshot(workingHeaders)
			simulation.StoppedAt = phase
			simulation.Status, simulation.Outcome = "incomplete", "needs_async_runtime_context"
			simulation.Reason = reason
			switch step.AsyncPolicy.RequestGate {
			case "plan_gated", "workload_unsupported", "payload_too_large", "invalid_json":
				simulation.Reason = "if earlier authentication and live rate-limit gates let the request reach async routing, " + reason + "; those preceding runtime gates are not simulated"
			}
			return simulation
		}

		switch phase {
		case "route":
			if outcome != "route" {
				return stop("incomplete", "unknown", phase, reason, rule)
			}
			simulation.Status, simulation.Outcome = "incomplete", "route"
			simulation.TargetApp, simulation.StoppedAt = preview.TargetApp, phase
			simulation.Reason = "request would be routed to another app; that app's rules are not loaded by this app-scoped trace"
			step.Reason = simulation.Reason
			simulation.Steps = append(simulation.Steps, step)
			simulation.FinalPath = requestPath
			simulation.RequestHeaders = headerSnapshot(workingHeaders)
			return simulation
		case "maintenance":
			if outcome != "maintenance" {
				return stop("incomplete", "unknown", phase, reason, rule)
			}
			simulation.Status, simulation.Outcome, simulation.StatusCode = "complete", "maintenance", preview.StatusCode
			simulation.RetryAfterSeconds, simulation.Message = preview.RetryAfterSeconds, preview.Message
			simulation.StoppedAt, simulation.Reason = phase, reason
			simulation.Steps = append(simulation.Steps, step)
			simulation.FinalPath, simulation.RequestHeaders = requestPath, headerSnapshot(workingHeaders)
			return simulation
		case "redirect":
			if outcome != "redirect" {
				return stop("incomplete", "unknown", phase, reason, rule)
			}
			simulation.Status, simulation.Outcome = "complete", "redirect"
			simulation.StatusCode, simulation.Location = preview.StatusCode, preview.Location
			simulation.RedirectHeaders = cloneStringMap(preview.RedirectHeaders)
			simulation.StoppedAt, simulation.Reason = phase, reason
			simulation.Steps = append(simulation.Steps, step)
			simulation.FinalPath, simulation.RequestHeaders = requestPath, headerSnapshot(workingHeaders)
			return simulation
		case "rewrite":
			if outcome != "rewrite" && outcome != "not_applied" {
				return stop("incomplete", "unknown", phase, reason, rule)
			}
			if outcome == "rewrite" {
				requestPath = preview.Path
			}
			step.PathAfter = requestPath
			simulation.Steps = append(simulation.Steps, step)
		case "headers":
			if outcome != "headers" {
				return stop("incomplete", "unknown", phase, reason, rule)
			}
			applyHeaderOps(workingHeaders, preview.RequestHeaderOps)
			simulation.ResponseHeaderOps = append(simulation.ResponseHeaderOps, preview.ResponseHeaderOps...)
			step.PathAfter = requestPath
			simulation.Steps = append(simulation.Steps, step)
		case "cors":
			switch outcome {
			case "cors_applied", "cors_no_origin", "cors_method_not_allowed", "cors_origin_not_allowed":
				if outcome == "cors_applied" {
					simulation.ResponseHeaderOps = append(simulation.ResponseHeaderOps, preview.ResponseHeaderOps...)
				}
				step.PathAfter = requestPath
				simulation.Steps = append(simulation.Steps, step)
			case "cors_preflight":
				simulation.Status, simulation.Outcome = "complete", "cors_preflight"
				simulation.StatusCode, simulation.StoppedAt = http.StatusNoContent, phase
				simulation.ResponseHeaderOps = append(simulation.ResponseHeaderOps, preview.ResponseHeaderOps...)
				simulation.Reason = reason
				simulation.Steps = append(simulation.Steps, step)
				simulation.FinalPath, simulation.RequestHeaders = requestPath, headerSnapshot(workingHeaders)
				return simulation
			case "needs_cors_preset", "invalid_cors_preset_policy":
				return stop("incomplete", outcome, phase, reason, rule)
			default:
				return stop("incomplete", outcome, phase, reason, rule)
			}
		case "jwt":
			switch outcome {
			case "missing_bearer_token":
				simulation.Status, simulation.Outcome = "incomplete", outcome
				simulation.StatusCode, simulation.ProblemCode = http.StatusUnauthorized, api.CodeUnauthorized
				simulation.StoppedAt = phase
				simulation.Reason = "if earlier app authentication and runtime gates let the request reach this matching JWT rule, the gateway would return HTTP 401 because no non-empty Bearer token is present"
				step.StatusCode, step.ProblemCode, step.Reason = http.StatusUnauthorized, api.CodeUnauthorized, simulation.Reason
				simulation.Steps = append(simulation.Steps, step)
				simulation.FinalPath, simulation.RequestHeaders = requestPath, headerSnapshot(workingHeaders)
				return simulation
			case "jwt_verification_candidate":
				simulation.Status, simulation.Outcome = "incomplete", "needs_jwt_runtime_context"
				simulation.StoppedAt = phase
				simulation.Reason = "a non-empty Bearer token is present, but live JWKS retrieval and cryptographic/claim verification are not performed; no authentication outcome is inferred"
				step.Reason = simulation.Reason
				simulation.Steps = append(simulation.Steps, step)
				simulation.FinalPath, simulation.RequestHeaders = requestPath, headerSnapshot(workingHeaders)
				return simulation
			default:
				return stop("incomplete", outcome, phase, reason, rule)
			}
		case "ip", "geo":
			switch outcome {
			case "allow":
				simulation.Steps = append(simulation.Steps, step)
			case "block":
				simulation.Status, simulation.Outcome, simulation.StatusCode = "complete", "blocked", http.StatusForbidden
				simulation.StoppedAt, simulation.Reason = phase, reason
				step.StatusCode = http.StatusForbidden
				simulation.Steps = append(simulation.Steps, step)
				simulation.FinalPath, simulation.RequestHeaders = requestPath, headerSnapshot(workingHeaders)
				return simulation
			default:
				return stop("incomplete", outcome, phase, reason, rule)
			}
		case "limit":
			switch outcome {
			case "within_limit":
				step.PathAfter = requestPath
				simulation.Steps = append(simulation.Steps, step)
			case "body_too_large":
				simulation.Status, simulation.Outcome, simulation.StatusCode = "complete", outcome, preview.StatusCode
				simulation.StoppedAt, simulation.Reason = phase, reason
				simulation.Steps = append(simulation.Steps, step)
				simulation.FinalPath, simulation.RequestHeaders = requestPath, headerSnapshot(workingHeaders)
				return simulation
			default:
				return stop("incomplete", outcome, phase, reason, rule)
			}
		case "respond":
			if outcome != "fixed_response" {
				return stop("incomplete", "unknown", phase, reason, rule)
			}
			simulation.Status, simulation.Outcome = "incomplete", "fixed_response"
			simulation.StatusCode, simulation.Body = preview.StatusCode, append(json.RawMessage(nil), preview.Body...)
			simulation.StoppedAt, simulation.Reason = phase, "edge rule would return this fixed response if prior app authentication and runtime gates pass"
			step.Reason = simulation.Reason
			simulation.Steps = append(simulation.Steps, step)
			simulation.FinalPath, simulation.RequestHeaders = requestPath, headerSnapshot(workingHeaders)
			return simulation
		case "cache":
			switch outcome {
			case "cache_bypassed_method", "cache_bypassed_credentials":
				step.PathAfter = requestPath
				simulation.Steps = append(simulation.Steps, step)
			case "cache_lookup_candidate":
				simulation.Status, simulation.Outcome = "incomplete", "needs_cache_runtime_context"
				simulation.StoppedAt = phase
				simulation.Reason = "the request passes the deterministic cache method and credential checks, but app authentication, async/pinned-deployment context, and live cache contents are unavailable; hit, miss, and stale outcomes are not inferred"
				step.Reason = simulation.Reason
				simulation.Steps = append(simulation.Steps, step)
				simulation.FinalPath, simulation.RequestHeaders = requestPath, headerSnapshot(workingHeaders)
				return simulation
			default:
				return stop("incomplete", outcome, phase, reason, rule)
			}
		case "throttle":
			if outcome != "throttle_policy_candidate" || step.ThrottlePolicy == nil {
				return stop("incomplete", outcome, phase, reason, rule)
			}
			simulation.Status, simulation.Outcome = "incomplete", "needs_throttle_runtime_context"
			simulation.StoppedAt = phase
			simulation.Reason = "the matched route-throttle policy is shown, but authentication/geolocation identity resolution, live token-bucket balance, and app/account live counters are unavailable; admission and HTTP 429 are not predicted"
			step.Reason = simulation.Reason
			simulation.Steps = append(simulation.Steps, step)
			simulation.FinalPath, simulation.RequestHeaders = requestPath, headerSnapshot(workingHeaders)
			return simulation
		case "budget":
			if !hasAppRequestBudget(input) {
				return stop("incomplete", "needs_app_request_budget", phase, "a matching budget rule was found, but the app's effective request budget and plan ceiling were not loaded", rule)
			}
			if outcome == "unavailable" {
				return stop("incomplete", outcome, phase, reason, rule)
			}
			policy := resolveBudgetPolicy(input, rule, workingHeaders)
			step.Outcome = "budget_candidate"
			step.BudgetPolicy = &policy
			step.PathAfter = requestPath
			step.Reason = budgetPolicyReason(policy)
			simulation.Steps = append(simulation.Steps, step)
			simulation.Reason = "deterministic policy checks did not terminate the request; the reported budget applies only if the request reaches guest forwarding, and no elapsed-time or deadline outcome is predicted"
		case "circuit_breaker":
			if outcome != "circuit_breaker_policy_candidate" || step.CircuitBreakerPolicy == nil {
				return stop("incomplete", outcome, phase, reason, rule)
			}
			simulation.Status, simulation.Outcome = "incomplete", "needs_circuit_breaker_runtime_context"
			simulation.StoppedAt = phase
			simulation.Reason = circuitBreakerRuntimeReason(*step.CircuitBreakerPolicy)
			step.Reason = simulation.Reason
			simulation.Steps = append(simulation.Steps, step)
			simulation.FinalPath, simulation.RequestHeaders = requestPath, headerSnapshot(workingHeaders)
			return simulation
		case "retry":
			if outcome != "retry_policy_candidate" || step.RetryPolicy == nil {
				return stop("incomplete", outcome, phase, reason, rule)
			}
			simulation.Status, simulation.Outcome = "incomplete", "needs_retry_runtime_context"
			simulation.StoppedAt = phase
			simulation.Reason = retryPolicyRuntimeReason(*step.RetryPolicy)
			step.Reason = simulation.Reason
			simulation.Steps = append(simulation.Steps, step)
			simulation.FinalPath, simulation.RequestHeaders = requestPath, headerSnapshot(workingHeaders)
			return simulation
		case "async":
			// Invalid legacy actions are omitted by gateway compilation, so
			// they do not intercept the request.
			step.PathAfter = requestPath
			simulation.Steps = append(simulation.Steps, step)
		case "validate":
			switch outcome {
			case "validated", "validation_failed_observe", "validation_failed_warn":
				simulation.ResponseHeaderOps = append(simulation.ResponseHeaderOps, preview.ResponseHeaderOps...)
				step.PathAfter = requestPath
				simulation.Steps = append(simulation.Steps, step)
			case "validation_failed", "unsupported_media_type", "body_too_large":
				simulation.Status, simulation.Outcome = "complete", outcome
				simulation.StatusCode, simulation.StoppedAt, simulation.Reason = preview.StatusCode, phase, reason
				simulation.Steps = append(simulation.Steps, step)
				simulation.FinalPath, simulation.RequestHeaders = requestPath, headerSnapshot(workingHeaders)
				return simulation
			default:
				return stop("incomplete", outcome, phase, reason, rule)
			}
		default:
			return stop("incomplete", "needs_runtime_context", phase, "matching rule depends on gateway runtime state or request data that this trace does not collect", rule)
		}
	}
	simulation.FinalPath = requestPath
	simulation.RequestHeaders = headerSnapshot(workingHeaders)
	return simulation
}

// declaredRouteAllowed mirrors gatewayd-internal's declared route matcher.
// The public path/method are supplied separately from the rewritten path so
// edge rewrites cannot change the app's public contract.
func declaredRouteAllowed(input Input, requestPath, requestMethod string) (allowed, unavailable, loaded bool) {
	if input.DeclaredRoutesLoaded || len(input.DeclaredRoutes) > 0 {
		allowed, err := matchDeclaredRoutes(input.DeclaredRoutes, requestPath, requestMethod)
		return allowed, err != nil, true
	}
	if input.DeclaredRouteDocumentMissing {
		return false, true, true
	}
	if !input.DeclaredRouteDocumentLoaded {
		return false, false, false
	}
	routes, err := declaredRoutesFromOpenAPI(input.DeclaredRouteOpenAPIDoc)
	if err != nil {
		return false, true, true
	}
	allowed, err = matchDeclaredRoutes(routes, requestPath, requestMethod)
	return allowed, err != nil, true
}

func declaredRoutesFromOpenAPI(doc []byte) ([]api.DeclaredRoute, error) {
	spec, err := openapidiff.LoadBytes(doc)
	if err != nil {
		return nil, err
	}
	routes := make([]api.DeclaredRoute, 0, len(spec.Paths))
	for routePath, item := range spec.Paths {
		if item == nil {
			continue
		}
		methods := make([]string, 0, len(item.Methods))
		for method := range item.Methods {
			methods = append(methods, strings.ToUpper(method))
		}
		routes = append(routes, api.DeclaredRoute{Path: routePath, Methods: methods})
	}
	return routes, nil
}

func matchDeclaredRoutes(routes []api.DeclaredRoute, requestPath, requestMethod string) (bool, error) {
	type compiledRoute struct {
		path    string
		methods map[string]struct{}
	}
	compiled := make([]compiledRoute, 0, len(routes))
	for _, route := range routes {
		routePath := normalizeDeclaredPath(route.Path)
		if routePath == "" {
			return false, fmt.Errorf("declared route path must start with '/': %q", route.Path)
		}
		methods := make(map[string]struct{}, len(route.Methods))
		for _, method := range route.Methods {
			method = strings.ToUpper(strings.TrimSpace(method))
			if method != "" {
				methods[method] = struct{}{}
			}
		}
		if len(methods) == 0 {
			return false, fmt.Errorf("declared route %q has no HTTP methods", routePath)
		}
		compiled = append(compiled, compiledRoute{path: routePath, methods: methods})
	}
	requestPath = normalizeDeclaredPath(requestPath)
	if requestPath == "" {
		return false, nil
	}
	method := strings.ToUpper(strings.TrimSpace(requestMethod))
	for _, route := range compiled {
		if _, ok := route.methods[method]; !ok {
			// HEAD is implicitly allowed wherever GET is declared, matching
			// gateway behavior for ordinary net/http handlers.
			if method != http.MethodHead {
				continue
			}
			if _, ok := route.methods[http.MethodGet]; !ok {
				continue
			}
		}
		if declaredPathMatches(route.path, requestPath) {
			return true, nil
		}
	}
	return false, nil
}

func normalizeDeclaredPath(routePath string) string {
	routePath = strings.TrimSpace(routePath)
	if routePath == "" || !strings.HasPrefix(routePath, "/") || strings.ContainsAny(routePath, "?#") {
		return ""
	}
	if routePath != "/" {
		routePath = strings.TrimRight(routePath, "/")
	}
	return routePath
}

func declaredPathMatches(template, request string) bool {
	templateParts := splitDeclaredPath(template)
	requestParts := splitDeclaredPath(request)
	if len(templateParts) != len(requestParts) {
		return false
	}
	for i, segment := range templateParts {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") && len(segment) > 2 {
			if requestParts[i] == "" {
				return false
			}
			continue
		}
		if segment != requestParts[i] {
			return false
		}
	}
	return true
}

func splitDeclaredPath(routePath string) []string {
	if routePath == "/" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(routePath, "/"), "/")
}

// previewAppCORSDefault mirrors the gateway's soft app-level CORS fallback:
// it runs only after an edge-rule CORS miss, stamps response headers when the
// origin is allowed, and never short-circuits an OPTIONS preflight.
func previewAppCORSDefault(input Input, headers http.Header) (SimulationStep, []api.EdgeRuleHeaderOp) {
	step := SimulationStep{Phase: "cors", Kind: "app_default_cors"}
	if input.CORSDefaultEnabled == nil || !*input.CORSDefaultEnabled || len(input.CORSDefaultOrigins) == 0 {
		step.Outcome = "cors_default_disabled"
		step.Reason = "no enabled per-app default CORS allowlist is configured; request continues without default CORS headers"
		return step, nil
	}
	origin := headers.Get("Origin")
	allowedOrigin := api.MatchEdgeRuleCORSOrigin(input.CORSDefaultOrigins, origin)
	if allowedOrigin == "" {
		step.Outcome = "cors_default_origin_not_allowed"
		step.Reason = fmt.Sprintf("Origin %q is not in the per-app default CORS allowlist; request continues without default CORS headers", origin)
		return step, nil
	}
	responseOps := []api.EdgeRuleHeaderOp{
		{Action: "set", Name: "Access-Control-Allow-Origin", Value: allowedOrigin},
		{Action: "set", Name: "Access-Control-Allow-Methods", Value: "GET, POST, OPTIONS"},
		{Action: "set", Name: "Access-Control-Allow-Headers", Value: "*"},
		{Action: "set", Name: "Access-Control-Expose-Headers", Value: "Streaming-Status, Streaming-Status-Accept-Hint"},
	}
	step.Outcome = "cors_default_applied"
	step.ResponseOps = append([]api.EdgeRuleHeaderOp(nil), responseOps...)
	step.Reason = fmt.Sprintf("would stamp %d per-app default CORS response-header operation(s) and continue the request to the app", len(responseOps))
	if input.Method == http.MethodOptions {
		step.Reason += "; OPTIONS is not short-circuited by the app-level default"
	}
	return step, responseOps
}

func firstPhaseRule(rules []api.EdgeRuleResponse, kind string, input Input, requestPath, method string, headers http.Header) (*api.EdgeRuleResponse, bool) {
	var first *api.EdgeRuleResponse
	for i := range rules {
		rule := &rules[i]
		if rule.Kind != kind || !rule.Enabled || !HostMatches(rule.MatchHost, input.Host) || !ruleMethodMatches(rule.Kind, rule.MatchMethods, method) || !api.EdgeRuleRequestHeadersMatch(rule.MatchHeaders, headers) {
			continue
		}
		if rule.Mode == api.EdgeRuleModeLog || !traceConditionMatches(*rule, input, requestPath, method, headers) {
			continue
		}
		matched, err := true, error(nil)
		if rule.MatchPath != "" && rule.MatchPath != "*" {
			matched, err = api.MatchEdgeRuleKindPath(rule.Kind, rule.MatchPath, requestPath)
		}
		if err != nil || !matched {
			continue
		}
		if first == nil {
			first = rule
			continue
		}
		return first, first.Priority == rule.Priority
	}
	return first, false
}

func previewAction(rule api.EdgeRuleResponse, row RuleRow, input Input, requestPath string) (string, string, *ActionPreview) {
	switch row.Status {
	case "skipped":
		return "not_applicable", "static selectors did not match", nil
	case "logged":
		return "logged", "log-mode rule: the gateway counts the match and does not act", nil
	case "later_candidate":
		return "not_evaluated", "a higher-priority matching candidate is considered first", nil
	case "tied_candidate":
		return "ambiguous", "equal-priority rules have no guaranteed evaluation order", nil
	case "first_candidate":
	default:
		return "unknown", "unrecognized trace status", nil
	}

	switch rule.Kind {
	case "route":
		action, ok := decodeAction[api.EdgeRuleRouteAction](rule.Action, "route")
		if !ok || action.TargetAppSlug == "" {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		preview := &ActionPreview{Type: "route", TargetApp: action.TargetAppSlug}
		return "route", fmt.Sprintf("would route to app %q", action.TargetAppSlug), preview
	case "rewrite":
		action, ok := decodeAction[api.EdgeRuleRewriteAction](rule.Action, "rewrite")
		if !ok {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		preview := &ActionPreview{Type: "rewrite", Path: requestPath}
		rewritten, applies := api.ApplyEdgeRuleRewritePath(requestPath, action.From, action.To)
		if !applies {
			return "not_applied", fmt.Sprintf("rewrite prefix %q does not match request path %q", action.From, requestPath), preview
		}
		preview.Path = rewritten
		if rewritten == requestPath {
			return "rewrite", fmt.Sprintf("rewrite leaves request path at %q", rewritten), preview
		}
		return "rewrite", fmt.Sprintf("would rewrite request path to %q", rewritten), preview
	case "redirect":
		action, ok := decodeAction[api.EdgeRuleRedirectAction](rule.Action, "redirect")
		if !ok || action.To == "" {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		status := action.StatusCode
		switch status {
		case http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		default:
			status = http.StatusFound
		}
		preview := &ActionPreview{Type: "redirect", StatusCode: status, Location: action.To, RedirectHeaders: action.Headers}
		return "redirect", fmt.Sprintf("would return HTTP %d redirect to %q", status, action.To), preview
	case "headers":
		action, ok := decodeAction[api.EdgeRuleHeadersAction](rule.Action, "headers")
		if !ok {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		preview := &ActionPreview{
			Type: "headers", RequestHeaderOps: append([]api.EdgeRuleHeaderOp(nil), action.RequestHeaders...),
			ResponseHeaderOps: append([]api.EdgeRuleHeaderOp(nil), action.ResponseHeaders...),
		}
		return "headers", fmt.Sprintf("would apply %d request-header and %d response-header operation(s)", len(action.RequestHeaders), len(action.ResponseHeaders)), preview
	case "cors":
		return previewCORSRule(rule, input)
	case "jwt":
		return previewJWTRule(rule, input)
	case "maintenance":
		action, ok := decodeAction[api.EdgeRuleMaintenanceAction](rule.Action, "maintenance")
		if !ok {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		retryAfter := action.RetryAfterSeconds
		if retryAfter <= 0 {
			retryAfter = api.EdgeRuleMaintenanceRetryAfterSeconds
		}
		preview := &ActionPreview{Type: "maintenance", StatusCode: http.StatusServiceUnavailable, RetryAfterSeconds: retryAfter, Message: action.Message}
		return "maintenance", fmt.Sprintf("would return HTTP 503 maintenance response (Retry-After %d)", retryAfter), preview
	case "respond":
		action, ok := decodeAction[api.EdgeRuleRespondAction](rule.Action, "respond")
		if !ok || action.StatusCode < http.StatusOK || action.StatusCode > 599 {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		preview := &ActionPreview{Type: "respond", StatusCode: action.StatusCode, Body: append(json.RawMessage(nil), action.Body...)}
		return "fixed_response", fmt.Sprintf("would return fixed HTTP %d response", action.StatusCode), preview
	case "ip":
		if input.ClientIP == "" {
			return "needs_context", "supply client_ip to evaluate this rule", nil
		}
		var envelope struct {
			IP *api.EdgeRuleIPAction `json:"ip"`
		}
		if err := json.Unmarshal(rule.Action, &envelope); err != nil || envelope.IP == nil {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		outcome, reason := evaluateIP(*envelope.IP, net.ParseIP(input.ClientIP))
		return outcome, reason, nil
	case "geo":
		if input.Country == "" {
			return "needs_context", "supply country; trace does not consult the live geo database", nil
		}
		var envelope struct {
			Geo *api.EdgeRuleGeoAction `json:"geo"`
		}
		if err := json.Unmarshal(rule.Action, &envelope); err != nil || envelope.Geo == nil {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		outcome, reason := evaluateGeo(*envelope.Geo, input.Country)
		return outcome, reason, nil
	case "validate":
		return previewValidateRule(rule, input)
	case "limit":
		return previewLimitRule(rule, input)
	case "cache":
		return previewCacheRule(rule, input)
	case "async":
		return previewAsyncRule(rule, input)
	case "throttle":
		return previewThrottleRule(rule, input)
	case "budget":
		return previewBudgetRule(rule, input)
	case "retry":
		return previewRetryRule(rule, input)
	case "circuit_breaker":
		return previewCircuitBreakerRule(rule)
	default:
		return "not_simulated", "this rule kind has runtime behavior outside the action preview", nil
	}
}

func previewAsyncRule(rule api.EdgeRuleResponse, input Input) (string, string, *ActionPreview) {
	action, ok := decodeAction[api.EdgeRuleAsyncAction](rule.Action, "async")
	if !ok || action.Validate() != nil {
		return "unavailable", "async action is missing or invalid; gateway compilation would drop it", nil
	}
	policy := &AsyncPolicyPreview{
		OnSuccessConfigured:     action.OnSuccess != "",
		OnFailureConfigured:     action.OnFailure != "",
		ConfiguredMaxAgeSeconds: action.MaxAgeSeconds,
		PlanGate:                "unavailable",
		WorkloadGate:            "unavailable",
		PayloadStatus:           "body_context_unavailable",
		MaxAgeSource:            "plan_cap_unavailable",
		RetryPolicySource:       "unavailable",
		MaxAttemptsStatus:       "unavailable",
		IdempotencyKeyPresent:   input.Headers.Get("Idempotency-Key") != "",
	}
	if input.AsyncPlanLimitsLoaded {
		policy.MaxPayloadBytes = input.AsyncMaxPayloadBytes
		policy.PlanMaxAttempts = input.AsyncMaxQueueAttempts
		policy.PlanMaxAgeSeconds = input.AsyncMaxDeadlineSeconds
		if input.AsyncInvokeAllowed {
			policy.PlanGate = "allowed"
		} else {
			policy.PlanGate = "blocked"
		}
		resolveAsyncAge(policy, action.MaxAgeSeconds)
	}
	if input.AsyncWorkloadContextLoaded {
		if input.AsyncRequestInvocationsEnabled {
			policy.WorkloadGate = "allowed"
		} else {
			policy.WorkloadGate = "blocked"
		}
	}
	if input.BodyProvided {
		switch {
		case input.AsyncPlanLimitsLoaded && len(input.Body) > input.AsyncMaxPayloadBytes:
			policy.PayloadStatus = "payload_too_large"
		case len(input.Body) == 0:
			policy.PayloadStatus = "empty_defaults_to_object"
		case json.Valid(input.Body):
			policy.PayloadStatus = "valid_json"
		default:
			policy.PayloadStatus = "invalid_json"
		}
	}
	resolveAsyncRetry(policy, action.RetryPolicy, input)
	policy.RequestGate = asyncRequestGate(policy, input)
	outcome, reason := asyncPolicyOutcome(policy)
	return outcome, reason, &ActionPreview{Type: "async", AsyncPolicy: policy}
}

func resolveAsyncAge(policy *AsyncPolicyPreview, requested int) {
	if policy.PlanMaxAgeSeconds <= 0 {
		policy.MaxAgeSource = "no_deadline"
		return
	}
	switch {
	case requested <= 0:
		policy.EffectiveMaxAgeSeconds = policy.PlanMaxAgeSeconds
		policy.MaxAgeSource = "plan_default"
	case requested > policy.PlanMaxAgeSeconds:
		policy.EffectiveMaxAgeSeconds = policy.PlanMaxAgeSeconds
		policy.MaxAgeSource = "plan_clamp"
	default:
		policy.EffectiveMaxAgeSeconds = requested
		policy.MaxAgeSource = "rule"
	}
}

func resolveAsyncRetry(preview *AsyncPolicyPreview, rulePolicy *api.RetryPolicyDTO, input Input) {
	policy := rulePolicy
	switch {
	case rulePolicy != nil:
		preview.RetryPolicySource = "rule"
	case input.AsyncAppRetryPolicyLoaded && input.AsyncAppRetryPolicy != nil:
		policy = input.AsyncAppRetryPolicy
		preview.RetryPolicySource = "app"
	case input.AsyncAppRetryPolicyLoaded:
		preview.RetryPolicySource = "plan_default"
	default:
		preview.RetryPolicySource = "unavailable"
	}
	if policy != nil {
		preview.ConfiguredMaxAttempts = policy.MaxAttempts
		preview.RetryBaseSeconds = policy.BaseSeconds
		preview.RetryMaxSeconds = policy.MaxSeconds
		preview.RetryJitterSeconds = policy.JitterSeconds
	}
	if preview.RetryPolicySource == "unavailable" || !input.AsyncPlanLimitsLoaded {
		preview.MaxAttemptsStatus = "plan_or_app_context_unavailable"
		return
	}
	preview.EffectiveMaxAttempts = api.EffectiveRetryMaxAttempts(preview.ConfiguredMaxAttempts, input.AsyncMaxQueueAttempts)
	preview.MaxReplays = preview.EffectiveMaxAttempts - 1
	preview.MaxAttemptsStatus = "effective"
}

func asyncRequestGate(policy *AsyncPolicyPreview, input Input) string {
	switch {
	case !input.AsyncPlanLimitsLoaded:
		return "needs_plan_context"
	case !input.AsyncInvokeAllowed:
		return "plan_gated"
	case !input.AsyncWorkloadContextLoaded:
		return "needs_workload_context"
	case !input.AsyncRequestInvocationsEnabled:
		return "workload_unsupported"
	case !input.BodyProvided:
		return "needs_request_body_context"
	case policy.PayloadStatus == "payload_too_large":
		return "payload_too_large"
	case policy.PayloadStatus == "invalid_json":
		return "invalid_json"
	default:
		return "enqueue_candidate"
	}
}

func asyncPolicyOutcome(policy *AsyncPolicyPreview) (string, string) {
	reason := fmt.Sprintf("async policy: retry source=%s with %s attempts, deadline source=%s, payload status=%s, and Idempotency-Key present=%t; %s", policy.RetryPolicySource, asyncAttemptsText(policy), policy.MaxAgeSource, policy.PayloadStatus, policy.IdempotencyKeyPresent, asyncGateReason(policy.RequestGate))
	if policy.OnSuccessConfigured || policy.OnFailureConfigured {
		reason += fmt.Sprintf("; terminal callback configured: success=%t failure=%t", policy.OnSuccessConfigured, policy.OnFailureConfigured)
	}
	switch policy.RequestGate {
	case "plan_gated":
		return "async_blocked_plan", reason
	case "workload_unsupported":
		return "async_blocked_workload", reason
	case "payload_too_large":
		return "async_payload_too_large", reason
	case "invalid_json":
		return "async_invalid_json", reason
	case "enqueue_candidate":
		return "async_enqueue_candidate", reason + "; durable enqueue and its HTTP response are not predicted"
	default:
		return "needs_async_context", reason
	}
}

func asyncAttemptsText(policy *AsyncPolicyPreview) string {
	if policy.MaxAttemptsStatus != "effective" {
		return "unresolved"
	}
	return strconv.Itoa(policy.EffectiveMaxAttempts)
}

func asyncGateReason(gate string) string {
	switch gate {
	case "plan_gated":
		return "the account plan disables async invocations"
	case "workload_unsupported":
		return "this worker/job workload has no request invocation listener"
	case "payload_too_large":
		return "the supplied body exceeds the plan's async payload limit"
	case "invalid_json":
		return "async routes require a valid JSON request body"
	case "enqueue_candidate":
		return "request and known policy gates allow evaluation to reach the durable enqueue"
	case "needs_plan_context":
		return "the account plan limits were not loaded"
	case "needs_workload_context":
		return "the app workload mode was not loaded"
	default:
		return "request-body context was not supplied"
	}
}

func previewCacheRule(rule api.EdgeRuleResponse, input Input) (string, string, *ActionPreview) {
	action, ok := decodeAction[api.EdgeRuleCacheAction](rule.Action, "cache")
	if !ok {
		return "unavailable", "cache action is missing or invalid; gateway compilation would drop it", nil
	}
	methods := append([]string(nil), action.Methods...)
	if len(methods) == 0 {
		methods = []string{http.MethodGet, http.MethodHead}
	}
	effectiveMethods := make([]string, 0, len(methods))
	for _, method := range methods {
		if (method == http.MethodGet || method == http.MethodHead) && ruleMethodMatches(rule.Kind, rule.MatchMethods, method) {
			effectiveMethods = append(effectiveMethods, method)
		}
	}
	methods = effectiveMethods
	varyOn := append([]string(nil), action.VaryOn...)
	sort.Strings(varyOn)
	policy := &CachePolicyPreview{
		Methods: methods, MaxAgeSeconds: action.MaxAgeSeconds,
		StaleWhileRevalidateSeconds: action.StaleWhileRevalidateSeconds,
		StaleIfErrorSeconds:         action.StaleIfErrorSeconds,
		VaryOn:                      varyOn,
	}
	preview := &ActionPreview{Type: "cache", CachePolicy: policy}

	methodAllowed := false
	for _, method := range methods {
		if method == input.Method {
			methodAllowed = true
			break
		}
	}
	if !methodAllowed || (input.Method != http.MethodGet && input.Method != http.MethodHead) {
		policy.RequestGate = "bypassed_method"
		return "cache_bypassed_method", fmt.Sprintf("request method %q is outside the cacheable method set %s; cache lookup is skipped", input.Method, strings.Join(methods, ", ")), preview
	}
	if input.Headers.Get("Authorization") != "" || requestHasCookie(input.Headers) {
		policy.RequestGate = "bypassed_credentials"
		return "cache_bypassed_credentials", "request carries Authorization or Cookie; credentialed requests bypass the cache", preview
	}
	policy.RequestGate = "lookup_candidate"
	return "cache_lookup_candidate", "request passes the deterministic cache method and credential checks; this is only a lookup candidate, not a predicted hit", preview
}

func previewBudgetRule(rule api.EdgeRuleResponse, input Input) (string, string, *ActionPreview) {
	action, ok := decodeAction[api.EdgeRuleBudgetAction](rule.Action, "budget")
	if !ok {
		return "unavailable", "budget action is missing or invalid; gateway compilation would drop it", nil
	}
	if !hasAppRequestBudget(input) {
		headerName := action.AllowOverrideHeader
		if headerName == "" {
			headerName = api.RequestBudgetDefaultOverrideHeader
		}
		policy := &BudgetPolicyPreview{
			ConfiguredMS: int64(action.BudgetMs), OverrideHeader: headerName,
			OverrideStatus: "unresolved", Source: "unavailable",
		}
		return "needs_app_request_budget", "a matching budget rule is configured, but the app's effective request budget and plan ceiling were not loaded", &ActionPreview{Type: "budget", BudgetPolicy: policy}
	}
	policy := resolveBudgetPolicy(input, &rule, input.Headers)
	return "budget_candidate", budgetPolicyReason(policy), &ActionPreview{Type: "budget", BudgetPolicy: &policy}
}

func previewThrottleRule(rule api.EdgeRuleResponse, input Input) (string, string, *ActionPreview) {
	action, ok := decodeAction[api.EdgeRuleThrottleAction](rule.Action, "throttle")
	if !ok {
		return "unavailable", "throttle action is missing or invalid; gateway compilation would drop it", nil
	}
	policy := &ThrottlePolicyPreview{
		RequestsPerSecond: action.RequestsPerSecond,
		Burst:             action.Burst,
		GatewayRateRPS:    max(action.RequestsPerSecond, 1),
		GatewayBurst:      max(action.Burst, 1),
		KeyBy:             action.KeyBy,
		JWTClaimName:      action.JWTClaimName,
		MaxKeysPerRule:    action.MaxKeysPerRule,
		MissingKeyPolicy:  action.MissingKeyPolicy,
		KeyFields:         action.KeyFields,
		CountStatuses:     action.CountStatuses,
		PlanCeilingStatus: "unavailable",
	}
	if policy.KeyBy == "" {
		policy.KeyBy = api.ThrottleKeyByNone
	}
	if policy.MissingKeyPolicy != api.ThrottleMissingKeyReject {
		policy.MissingKeyPolicy = api.ThrottleMissingKeyShared
	}
	if policy.MaxKeysPerRule <= 0 {
		policy.MaxKeysPerRule = api.ThrottleMaxKeysPerRuleDefault
		policy.MaxKeysSource = "platform_default"
	} else if policy.MaxKeysPerRule > api.ThrottleMaxKeysPerRuleDefault*10 {
		policy.MaxKeysPerRule = api.ThrottleMaxKeysPerRuleDefault * 10
		policy.MaxKeysSource = "platform_ceiling"
	} else {
		policy.MaxKeysSource = "rule"
	}
	if input.ThrottlePlanLimitsLoaded && input.ThrottlePlanMaxRPS > 0 && input.ThrottlePlanMaxBurst > 0 {
		policy.PlanMaxRPS, policy.PlanMaxBurst = input.ThrottlePlanMaxRPS, input.ThrottlePlanMaxBurst
		policy.PlanCeilingStatus = "within_plan"
		if action.RequestsPerSecond > float64(input.ThrottlePlanMaxRPS) || action.Burst > input.ThrottlePlanMaxBurst {
			policy.PlanCeilingStatus = "exceeds_plan"
		}
	}
	if input.AppThrottleContextLoaded {
		policy.AppRequestRPS = input.AppRequestRateRPS
		policy.AppRequestBurst = input.AppRequestRateBurst
		policy.AccountRequestRPM = input.AccountRequestRateRPM
	}
	return "throttle_policy_candidate", throttlePolicyReason(*policy), &ActionPreview{Type: "throttle", ThrottlePolicy: policy}
}

func throttlePolicyReason(policy ThrottlePolicyPreview) string {
	keying := fmt.Sprintf("key_by=%s", policy.KeyBy)
	if len(policy.KeyFields) > 0 {
		keying += fmt.Sprintf(" fields=%s", strings.Join(policy.KeyFields, "+"))
	}
	if policy.JWTClaimName != "" {
		keying += fmt.Sprintf(" claim=%q", policy.JWTClaimName)
	}
	if len(policy.CountStatuses) > 0 {
		codes := make([]string, len(policy.CountStatuses))
		for i, c := range policy.CountStatuses {
			codes[i] = strconv.Itoa(c)
		}
		keying += fmt.Sprintf(", charged only for responses %s", strings.Join(codes, "/"))
	}
	maxKeys := fmt.Sprintf("max_keys_per_rule=%d (%s)", policy.MaxKeysPerRule, policy.MaxKeysSource)
	reason := fmt.Sprintf("matched route-throttle rule has configured %.3g requests/s, burst %d (gateway effective %.3g requests/s and burst %d), %s, missing_key_policy=%s, %s", policy.RequestsPerSecond, policy.Burst, policy.GatewayRateRPS, policy.GatewayBurst, keying, policy.MissingKeyPolicy, maxKeys)
	if policy.PlanCeilingStatus == "within_plan" || policy.PlanCeilingStatus == "exceeds_plan" {
		reason += fmt.Sprintf("; plan ceiling is %d requests/s and burst %d (%s)", policy.PlanMaxRPS, policy.PlanMaxBurst, policy.PlanCeilingStatus)
	}
	if policy.AppRequestRPS > 0 && policy.AppRequestBurst > 0 {
		reason += fmt.Sprintf("; the separate app-wide cap is %d requests/s with burst %d", policy.AppRequestRPS, policy.AppRequestBurst)
	}
	if policy.AccountRequestRPM > 0 {
		reason += fmt.Sprintf(" and account-wide cap is %d requests/min", policy.AccountRequestRPM)
	}
	return reason + "; identity resolution and current bucket balance are runtime-only, so no admission or HTTP 429 is inferred"
}

func previewRetryRule(rule api.EdgeRuleResponse, input Input) (string, string, *ActionPreview) {
	action, ok := decodeAction[api.EdgeRuleRetryAction](rule.Action, "retry")
	if !ok {
		return "unavailable", "retry action is missing or invalid; gateway compilation would drop it", nil
	}
	if action.MaxAttempts < 2 {
		return "unavailable", "retry action max_attempts is below 2, so gateway compilation would drop this rule; the operator default may apply if retry is enabled", nil
	}

	policy := &RetryPolicyPreview{
		MaxAttempts:           action.MaxAttempts,
		MaxAttemptsSource:     "rule",
		AllowNonIdempotent:    action.AllowNonIdempotent,
		IdempotencyKeyPresent: strings.TrimSpace(input.Headers.Get("Idempotency-Key")) != "",
		MethodEligibility:     retryMethodEligibility(input.Method, action.AllowNonIdempotent, input.Headers),
	}
	if policy.MaxAttempts > api.EdgeRuleRetryMaxAttempts {
		policy.MaxAttempts = api.EdgeRuleRetryMaxAttempts
		policy.MaxAttemptsSource = "platform_ceiling"
	}
	policy.MaxReplays = policy.MaxAttempts - 1

	minRemaining := action.MinRemainingMs
	if minRemaining <= 0 || minRemaining > api.MaxEdgeRuleRetryMinRemainingMs {
		minRemaining = api.EdgeRuleRetryDefaultMinRemainingMs
	}
	policy.MinRemainingMS = minRemaining

	backoff := action.BackoffMs
	if backoff < 0 || backoff > api.MaxEdgeRuleRetryBackoffMs {
		backoff = 0
	}
	policy.BackoffMS = backoff

	budgetPercent := action.BudgetPercent
	if budgetPercent < 1 || budgetPercent > api.MaxEdgeRuleRetryBudgetPercent {
		budgetPercent = api.EdgeRuleRetryDefaultBudgetPercent
	}
	policy.BudgetPercent = budgetPercent

	budgetMinRetries := action.BudgetMinRetries
	if budgetMinRetries <= 0 || budgetMinRetries > api.MaxEdgeRuleRetryBudgetMin {
		budgetMinRetries = api.EdgeRuleRetryDefaultBudgetMin
	}
	policy.BudgetMinRetries = budgetMinRetries

	return "retry_policy_candidate", retryPolicyConfiguredReason(*policy), &ActionPreview{Type: "retry", RetryPolicy: policy}
}

func retryMethodEligibility(method string, allowNonIdempotent bool, headers http.Header) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace, http.MethodPut, http.MethodDelete:
		return "idempotent_method"
	case http.MethodPost, http.MethodPatch:
		if !allowNonIdempotent {
			return "non_idempotent_disabled"
		}
		if strings.TrimSpace(headers.Get("Idempotency-Key")) == "" {
			return "idempotency_key_required"
		}
		return "non_idempotent_allowed_with_key"
	default:
		return "unsupported_method"
	}
}

func retryPolicyConfiguredReason(policy RetryPolicyPreview) string {
	attempts := fmt.Sprintf("up to %d total attempts (%d replay(s))", policy.MaxAttempts, policy.MaxReplays)
	if policy.MaxAttemptsSource == "platform_ceiling" {
		attempts += " (clamped to the platform ceiling)"
	}
	reason := fmt.Sprintf("matched retry rule allows %s, with a %d ms minimum remaining request budget, %d ms backoff, and aggregate budget of %d%% or at least %d replay(s); method eligibility is %s", attempts, policy.MinRemainingMS, policy.BackoffMS, policy.BudgetPercent, policy.BudgetMinRetries, policy.MethodEligibility)
	if policy.AllowNonIdempotent {
		reason += "; POST/PATCH replay is opted in and requires an Idempotency-Key"
	} else {
		reason += "; POST/PATCH replay is disabled"
	}
	return reason
}

func retryPolicyRuntimeReason(policy RetryPolicyPreview) string {
	if policy.MethodEligibility == "non_idempotent_disabled" || policy.MethodEligibility == "idempotency_key_required" || policy.MethodEligibility == "unsupported_method" {
		return fmt.Sprintf("the configured retry method guard prevents replay for this request (%s); the overall result is still incomplete because the operator gate and downstream behavior are not simulated", policy.MethodEligibility)
	}
	return "the matching retry policy is shown, but a replay requires an enabled operator gate and a stale transport failure; body replayability, committed-response state, remaining request budget, healthy sibling availability, and live aggregate budget are unknown, so no replay or response outcome is predicted"
}

func previewCircuitBreakerRule(rule api.EdgeRuleResponse) (string, string, *ActionPreview) {
	action, ok := decodeAction[api.EdgeRuleCircuitBreakerAction](rule.Action, "circuit_breaker")
	if !ok {
		return "unavailable", "circuit-breaker action is missing or invalid; gateway compilation would drop this rule", nil
	}

	policy := &CircuitBreakerPolicyPreview{
		FailureThreshold: action.FailureThreshold,
		MinRequests:      action.MinRequests,
		WindowSeconds:    action.WindowSeconds,
		OpenSeconds:      action.OpenSeconds,
		MaxOpenSeconds:   action.MaxOpenSeconds,
	}
	if policy.FailureThreshold <= 0 || policy.FailureThreshold > 1 {
		policy.FailureThreshold = api.EdgeRuleCircuitDefaultFailureThreshold
	}
	if policy.MinRequests < 1 || policy.MinRequests > api.MaxEdgeRuleCircuitMinRequests {
		policy.MinRequests = api.EdgeRuleCircuitDefaultMinRequests
	}
	if policy.WindowSeconds < 1 || policy.WindowSeconds > api.MaxEdgeRuleCircuitWindowSeconds {
		policy.WindowSeconds = api.EdgeRuleCircuitDefaultWindowSeconds
	}
	if policy.OpenSeconds < 1 || policy.OpenSeconds > api.MaxEdgeRuleCircuitOpenSeconds {
		policy.OpenSeconds = api.EdgeRuleCircuitDefaultOpenSeconds
	}
	if policy.MaxOpenSeconds < policy.OpenSeconds || policy.MaxOpenSeconds > api.MaxEdgeRuleCircuitOpenSeconds {
		policy.MaxOpenSeconds = policy.OpenSeconds
	}
	return "circuit_breaker_policy_candidate", circuitBreakerPolicyReason(*policy), &ActionPreview{
		Type: "circuit_breaker", CircuitBreakerPolicy: policy,
	}
}

func circuitBreakerPolicyReason(policy CircuitBreakerPolicyPreview) string {
	return fmt.Sprintf("matched circuit-breaker rule uses a %.3g%% failure-ratio threshold after at least %d observations in a rolling %d-second window, with an initial %d-second open interval and backoff capped at %d seconds; live breaker state is not inferred", policy.FailureThreshold*100, policy.MinRequests, policy.WindowSeconds, policy.OpenSeconds, policy.MaxOpenSeconds)
}

func circuitBreakerRuntimeReason(_ CircuitBreakerPolicyPreview) string {
	return "the matched circuit-breaker policy is shown, but the operator feature gate, live per-instance failure window, open/half-open state, and probe result are unavailable; target selection and request outcome are not predicted"
}

func previewJWTRule(rule api.EdgeRuleResponse, input Input) (string, string, *ActionPreview) {
	action, ok := decodeAction[api.EdgeRuleJWTAction](rule.Action, "jwt")
	if !ok || action.Validate() != nil {
		return "unavailable", "JWT action is missing or invalid; gateway compilation may reject this rule", nil
	}
	policy := &JWTPolicyPreview{
		BearerTokenPresent:                       jwtBearerTokenPresent(input.Headers.Get("Authorization")),
		IssuerConfigured:                         strings.TrimSpace(action.Issuer) != "",
		JWKSConfigured:                           strings.TrimSpace(action.JWKSURL) != "",
		AudienceCount:                            len(action.Audience),
		Algorithms:                               append([]string(nil), action.Algorithms...),
		PlatformTenantExternalRefClaimConfigured: action.PlatformTenantExternalRefClaim != "",
	}
	for name := range action.RequiredClaims {
		policy.RequiredClaimNames = append(policy.RequiredClaimNames, name)
	}
	sort.Strings(policy.Algorithms)
	sort.Strings(policy.RequiredClaimNames)
	if !policy.BearerTokenPresent {
		return "missing_bearer_token", "Authorization does not contain a non-empty Bearer token; if this matching rule is reached, the gateway rejects the request before live verification", &ActionPreview{Type: "jwt", JWTPolicy: policy}
	}
	return "jwt_verification_candidate", "a non-empty Bearer token is present; the live JWKS, signature, issuer, audience, and required-claim checks are not evaluated", &ActionPreview{Type: "jwt", JWTPolicy: policy}
}

func jwtBearerTokenPresent(header string) bool {
	const prefix = "Bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	return strings.TrimSpace(header[len(prefix):]) != ""
}

func cloneJWTPolicyPreview(policy *JWTPolicyPreview) *JWTPolicyPreview {
	if policy == nil {
		return nil
	}
	cloned := *policy
	cloned.Algorithms = append([]string(nil), policy.Algorithms...)
	cloned.RequiredClaimNames = append([]string(nil), policy.RequiredClaimNames...)
	return &cloned
}

func hasAppRequestBudget(input Input) bool {
	return input.AppRequestBudgetLoaded && input.RequestBudgetMS > 0 && input.RequestBudgetMaxMS > 0
}

func resolveBudgetPolicy(input Input, rule *api.EdgeRuleResponse, headers http.Header) BudgetPolicyPreview {
	policy := BudgetPolicyPreview{
		PlanMaxMS: input.RequestBudgetMaxMS, OverrideStatus: "not_applicable",
	}
	configuredMS := input.RequestBudgetMS
	candidateMS := configuredMS
	source := "plan_default"
	if input.RequestTimeoutS > 0 {
		configuredMS = int64(input.RequestTimeoutS) * 1000
		candidateMS, source = configuredMS, "app"
	}
	if rule != nil {
		action, ok := decodeAction[api.EdgeRuleBudgetAction](rule.Action, "budget")
		if !ok {
			return BudgetPolicyPreview{PlanMaxMS: input.RequestBudgetMaxMS, Source: "unavailable", OverrideStatus: "unavailable"}
		}
		configuredMS = int64(action.BudgetMs)
		candidateMS, source = configuredMS, "rule"
		policy.OverrideHeader = action.AllowOverrideHeader
		if policy.OverrideHeader == "" {
			policy.OverrideHeader = api.RequestBudgetDefaultOverrideHeader
		}
		policy.OverrideStatus = "not_present"
		if value := headers.Get(policy.OverrideHeader); value != "" {
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || parsed <= 0 {
				policy.OverrideStatus = "ignored_invalid"
			} else {
				candidateMS, source = int64(parsed), "header_override"
				policy.OverrideStatus = "applied"
			}
		}
	}
	policy.ConfiguredMS = configuredMS
	platformMaxMS := api.RequestBudgetMax.Milliseconds()
	if rule != nil && (candidateMS <= 0 || candidateMS > platformMaxMS) {
		candidateMS, source = platformMaxMS, "ceiling_clamp"
		if policy.OverrideStatus == "applied" {
			policy.OverrideStatus = "applied_clamped"
		}
	}
	if candidateMS <= 0 || candidateMS > policy.PlanMaxMS {
		candidateMS, source = policy.PlanMaxMS, "ceiling_clamp"
		if policy.OverrideStatus == "applied" {
			policy.OverrideStatus = "applied_clamped"
		}
	}
	policy.BudgetMS, policy.Source = candidateMS, source
	return policy
}

func budgetPolicyReason(policy BudgetPolicyPreview) string {
	if policy.OverrideStatus == "unavailable" || policy.Source == "unavailable" {
		return "the effective request budget could not be resolved from the available app metadata"
	}
	if policy.OverrideStatus == "not_applicable" {
		return fmt.Sprintf("no budget rule matched; app/plan fallback would configure %d ms from %s (plan ceiling %d ms) if the request reaches guest forwarding", policy.BudgetMS, policy.Source, policy.PlanMaxMS)
	}
	reason := fmt.Sprintf("effective budget candidate is %d ms from %s (configured rule budget %d ms; plan ceiling %d ms)", policy.BudgetMS, policy.Source, policy.ConfiguredMS, policy.PlanMaxMS)
	switch policy.OverrideStatus {
	case "applied":
		reason += fmt.Sprintf("; request header %q overrides the rule", policy.OverrideHeader)
	case "applied_clamped":
		reason += fmt.Sprintf("; request header %q is accepted then clamped to the plan ceiling", policy.OverrideHeader)
	case "ignored_invalid":
		reason += fmt.Sprintf("; request header %q is not a positive integer and is ignored", policy.OverrideHeader)
	case "not_present":
		reason += fmt.Sprintf("; request header %q is absent", policy.OverrideHeader)
	}
	return reason + "; this deadline starts only after upload, wake, routing, and admission, and the trace does not predict when it expires"
}

func requestHasCookie(headers http.Header) bool {
	request := &http.Request{Header: headers}
	for _, cookie := range request.Cookies() {
		if cookie.Name != "" {
			return true
		}
	}
	return false
}

// corsMatchMethod mirrors applyEdgeRuleCORS: browser preflights match a CORS
// rule against Access-Control-Request-Method, but only when Origin is present.
func corsMatchMethod(method string, headers http.Header) (matchMethod, requestedMethod string) {
	matchMethod = method
	if method == http.MethodOptions && strings.TrimSpace(headers.Get("Origin")) != "" {
		requestedMethod = strings.TrimSpace(headers.Get("Access-Control-Request-Method"))
		if requestedMethod != "" {
			matchMethod = requestedMethod
		}
	}
	return matchMethod, requestedMethod
}

func previewCORSRule(rule api.EdgeRuleResponse, input Input) (string, string, *ActionPreview) {
	action, ok := decodeAction[api.EdgeRuleCORSAction](rule.Action, "cors")
	if !ok {
		return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
	}
	if action.CorsPresetID != nil {
		var outcome, reason string
		action, outcome, reason = resolveCORSPreset(rule, action, input.CorsPresets)
		if reason != "" {
			return outcome, reason, nil
		}
	}

	_, requestedMethod := corsMatchMethod(input.Method, input.Headers)
	preflightContext := ""
	if requestedMethod != "" {
		preflightContext = fmt.Sprintf("preflight requests %s; ", requestedMethod)
		allowed := false
		for _, method := range action.AllowMethods {
			if method == requestedMethod {
				allowed = true
				break
			}
		}
		if !allowed {
			return "cors_method_not_allowed", preflightContext + "requested method is not allowed, so the gateway adds no CORS headers and continues the request", &ActionPreview{Type: "cors"}
		}
	}

	origin := strings.TrimSpace(input.Headers.Get("Origin"))
	allowedOrigin := api.MatchEdgeRuleCORSOrigin(action.AllowOrigins, origin)
	if origin != "" && allowedOrigin == "" {
		return "cors_origin_not_allowed", preflightContext + "Origin is not allowed, so the gateway adds no CORS headers and continues the request", &ActionPreview{Type: "cors"}
	}

	preview := &ActionPreview{Type: "cors"}
	if allowedOrigin != "" {
		preview.ResponseHeaderOps = append(preview.ResponseHeaderOps, api.EdgeRuleHeaderOp{Action: "set", Name: "Access-Control-Allow-Origin", Value: allowedOrigin})
		if action.AllowCredentials {
			preview.ResponseHeaderOps = append(preview.ResponseHeaderOps, api.EdgeRuleHeaderOp{Action: "set", Name: "Access-Control-Allow-Credentials", Value: "true"})
		}
		if len(action.AllowMethods) > 0 {
			preview.ResponseHeaderOps = append(preview.ResponseHeaderOps, api.EdgeRuleHeaderOp{Action: "set", Name: "Access-Control-Allow-Methods", Value: strings.Join(action.AllowMethods, ", ")})
		}
		if len(action.AllowHeaders) > 0 {
			preview.ResponseHeaderOps = append(preview.ResponseHeaderOps, api.EdgeRuleHeaderOp{Action: "set", Name: "Access-Control-Allow-Headers", Value: strings.Join(action.AllowHeaders, ", ")})
		}
		if len(action.ExposeHeaders) > 0 {
			preview.ResponseHeaderOps = append(preview.ResponseHeaderOps, api.EdgeRuleHeaderOp{Action: "set", Name: "Access-Control-Expose-Headers", Value: strings.Join(action.ExposeHeaders, ", ")})
		}
		if action.MaxAgeSeconds > 0 {
			preview.ResponseHeaderOps = append(preview.ResponseHeaderOps, api.EdgeRuleHeaderOp{Action: "set", Name: "Access-Control-Max-Age", Value: fmt.Sprintf("%d", action.MaxAgeSeconds)})
		}
	}
	if input.Method == http.MethodOptions {
		preview.StatusCode = http.StatusNoContent
		return "cors_preflight", preflightContext + "would return HTTP 204 preflight response", preview
	}
	if origin == "" {
		return "cors_no_origin", "CORS rule matches but the request has no Origin header, so no CORS response headers are added", preview
	}
	return "cors_applied", fmt.Sprintf("would apply %d CORS response-header operation(s)", len(preview.ResponseHeaderOps)), preview
}

// resolveCORSPreset mirrors state.MergeCorsPresetIntoRule's zero-value
// fallback and account guard without coupling this shared simulator to state.
func resolveCORSPreset(rule api.EdgeRuleResponse, action *api.EdgeRuleCORSAction, presets []api.CorsPresetResponse) (*api.EdgeRuleCORSAction, string, string) {
	presetID := *action.CorsPresetID
	var preset *api.CorsPresetResponse
	for i := range presets {
		if presets[i].ID == presetID && presets[i].AccountID == rule.AccountID {
			preset = &presets[i]
			break
		}
	}
	if preset == nil {
		return nil, "needs_cors_preset", "referenced CORS preset is not available to this trace or is not visible to the rule's account"
	}

	resolved := *action
	if len(resolved.AllowOrigins) == 0 {
		resolved.AllowOrigins = append([]string(nil), preset.AllowOrigins...)
	}
	if len(resolved.AllowMethods) == 0 {
		resolved.AllowMethods = append([]string(nil), preset.AllowMethods...)
	}
	if len(resolved.AllowHeaders) == 0 {
		resolved.AllowHeaders = append([]string(nil), preset.AllowHeaders...)
	}
	if len(resolved.ExposeHeaders) == 0 {
		resolved.ExposeHeaders = append([]string(nil), preset.ExposeHeaders...)
	}
	if !resolved.AllowCredentials {
		resolved.AllowCredentials = preset.AllowCredentials
	}
	if resolved.MaxAgeSeconds == 0 {
		resolved.MaxAgeSeconds = preset.MaxAgeSeconds
	}
	if resolved.AllowCredentials {
		for _, origin := range resolved.AllowOrigins {
			if origin == "*" {
				return nil, "invalid_cors_preset_policy", "merged CORS preset policy combines wildcard origin * with credentials; gateway compilation drops this rule"
			}
		}
	}
	return &resolved, "", ""
}

func previewValidateRule(rule api.EdgeRuleResponse, input Input) (string, string, *ActionPreview) {
	action, ok := decodeAction[api.EdgeRuleValidateAction](rule.Action, "validate")
	if !ok || len(action.Schema) == 0 {
		return "unavailable", "rule schema is missing or invalid; gateway compilation would reject it", nil
	}
	preview := &ActionPreview{Type: "validate"}
	// Real HTTP parsers remove optional whitespace around field values. The
	// CLI/dashboard parser retains bytes for exact edge-rule header matches,
	// so trim OWS here to mirror the gateway's parsed Content-Type value.
	contentType := strings.TrimSpace(input.Headers.Get("Content-Type"))
	if len(action.ContentTypes) > 0 && !validateContentTypeAllowed(contentType, action.ContentTypes) {
		preview.StatusCode = http.StatusUnsupportedMediaType
		return "unsupported_media_type", "request Content-Type does not match the validation rule", preview
	}
	if !input.BodyProvided {
		return "needs_request_body", "supply --body-file or enable the request body field to evaluate this rule", preview
	}
	bodyLimit := input.RequestBodyMaxBytes
	if action.MaxBodyBytes > 0 && int64(action.MaxBodyBytes) < bodyLimit {
		bodyLimit = int64(action.MaxBodyBytes)
	}
	if int64(len(input.Body)) > bodyLimit {
		preview.StatusCode = http.StatusRequestEntityTooLarge
		return "body_too_large", fmt.Sprintf("request body is %d bytes; the effective validation cap is %d bytes", len(input.Body), bodyLimit), preview
	}
	compiled, err := edgevalidate.Compile(action.Schema, action.RejectOnUnknownFields)
	if err != nil {
		return "unavailable", "validation schema could not be compiled; check the stored edge rule", preview
	}
	fieldErr, err := compiled.Validate(input.Body)
	if err != nil {
		return "unavailable", "validation schema evaluation failed", preview
	}
	if fieldErr == nil {
		return "validated", "request body satisfies the JSON Schema", preview
	}
	preview.ValidationField = fieldErr.Field
	preview.ValidationKeyword = fieldErr.Expected
	mode := rule.ValidateMode
	if mode == "" {
		mode = action.ValidateMode
	}
	if mode == "" {
		mode = api.ValidateModeBlock
	}
	switch mode {
	case api.ValidateModeObserve:
		return "validation_failed_observe", validationFailureReason(fieldErr), preview
	case api.ValidateModeWarn:
		preview.ResponseHeaderOps = []api.EdgeRuleHeaderOp{{Action: "set", Name: "X-Validation-Warning", Value: rule.ID}}
		return "validation_failed_warn", validationFailureReason(fieldErr), preview
	default:
		preview.StatusCode = http.StatusUnprocessableEntity
		return "validation_failed", validationFailureReason(fieldErr), preview
	}
}

func previewLimitRule(rule api.EdgeRuleResponse, input Input) (string, string, *ActionPreview) {
	action, ok := decodeAction[api.EdgeRuleLimitAction](rule.Action, "limit")
	if !ok {
		return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
	}
	preview := &ActionPreview{Type: "limit"}
	if !input.BodyProvided {
		return "needs_request_body", "supply a request body to evaluate the limit rule", preview
	}

	bufferedCap := int64(action.MaxBodyBytes)
	if bufferedCap <= 0 || bufferedCap > api.MaxRequestBodyBytes {
		bufferedCap = api.MaxRequestBodyBytes
	}
	if input.RequestBodyMaxBytes < bufferedCap {
		bufferedCap = input.RequestBodyMaxBytes
	}

	streamingCap := int64(action.MaxBodyBytesStreaming)
	if streamingCap > 0 {
		if streamingCap > api.RawStreamMaxRequestBytes {
			streamingCap = api.RawStreamMaxRequestBytes
		}
		streamingPlanCap := input.RequestBodyMaxBytes
		if streamingPlanCap > api.RawStreamMaxRequestBytes {
			streamingPlanCap = api.RawStreamMaxRequestBytes
		}
		if streamingPlanCap < streamingCap {
			streamingCap = streamingPlanCap
		}
	}

	bodyBytes := int64(len(input.Body))
	if streamingCap <= 0 || streamingCap == bufferedCap {
		if bodyBytes > bufferedCap {
			preview.StatusCode = http.StatusRequestEntityTooLarge
			return "body_too_large", fmt.Sprintf("request body is %d bytes; the effective limit is %d bytes", bodyBytes, bufferedCap), preview
		}
		return "within_limit", fmt.Sprintf("request body is %d bytes; the effective limit is %d bytes", bodyBytes, bufferedCap), preview
	}
	if bodyBytes <= min(bufferedCap, streamingCap) {
		return "within_limit", fmt.Sprintf("request body is %d bytes; it is within both the buffered limit (%d bytes) and streaming limit (%d bytes)", bodyBytes, bufferedCap, streamingCap), preview
	}
	if bodyBytes > max(bufferedCap, streamingCap) {
		preview.StatusCode = http.StatusRequestEntityTooLarge
		return "body_too_large", fmt.Sprintf("request body is %d bytes; it exceeds both the buffered limit (%d bytes) and streaming limit (%d bytes)", bodyBytes, bufferedCap, streamingCap), preview
	}
	return "needs_streaming_context", fmt.Sprintf("request body is %d bytes; the buffered limit is %d bytes and streaming limit is %d bytes, so the outcome depends on unavailable gateway streaming context", bodyBytes, bufferedCap, streamingCap), preview
}

func validationFailureReason(fieldErr *edgevalidate.FieldError) string {
	if fieldErr == nil {
		return "request body does not match the JSON Schema"
	}
	if fieldErr.Field == "" {
		return fmt.Sprintf("request body does not match the JSON Schema (keyword %s)", fieldErr.Expected)
	}
	return fmt.Sprintf("request body does not match the JSON Schema at %s (keyword %s)", fieldErr.Field, fieldErr.Expected)
}

// validateContentTypeAllowed mirrors the gateway's content-type check:
// exact media type or the same media type with parameters such as charset.
func validateContentTypeAllowed(contentType string, allowed []string) bool {
	if contentType == "" {
		return false
	}
	for _, candidate := range allowed {
		if candidate == contentType {
			return true
		}
		if index := strings.IndexByte(contentType, ';'); index >= 0 && strings.TrimSpace(contentType[:index]) == candidate {
			return true
		}
	}
	return false
}

func decodeAction[T any](raw json.RawMessage, kind string) (*T, bool) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, false
	}
	encoded, ok := envelope[kind]
	if !ok {
		return nil, false
	}
	var action T
	if err := json.Unmarshal(encoded, &action); err != nil {
		return nil, false
	}
	return &action, true
}

func evaluateIP(action api.EdgeRuleIPAction, clientIP net.IP) (string, string) {
	deny, ok := parseCIDRs(action.Deny)
	if !ok {
		return "unavailable", "rule contains an invalid deny CIDR; gateway compilation would drop it"
	}
	allow, ok := parseCIDRs(action.Allow)
	if !ok {
		return "unavailable", "rule contains an invalid allow CIDR; gateway compilation would drop it"
	}
	for i, network := range deny {
		if network.Contains(clientIP) {
			return "block", fmt.Sprintf("client IP matches deny CIDR %q", action.Deny[i])
		}
	}
	if len(allow) > 0 {
		for i, network := range allow {
			if network.Contains(clientIP) {
				return "allow", fmt.Sprintf("client IP matches allow CIDR %q and no deny CIDR matched", action.Allow[i])
			}
		}
		return "block", "client IP matched no allow CIDR (implicit deny)"
	}
	return "allow", "no deny CIDR matched and the rule has no allowlist"
}

func parseCIDRs(cidrs []string) ([]*net.IPNet, bool) {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, false
		}
		nets = append(nets, network)
	}
	return nets, true
}

func evaluateGeo(action api.EdgeRuleGeoAction, country string) (string, string) {
	for _, denied := range action.Deny {
		if strings.EqualFold(denied, country) {
			return "block", fmt.Sprintf("country %s matches the deny list", country)
		}
	}
	if len(action.Allow) > 0 {
		for _, allowed := range action.Allow {
			if strings.EqualFold(allowed, country) {
				return "allow", fmt.Sprintf("country %s matches the allow list and no deny code matched", country)
			}
		}
		return "block", fmt.Sprintf("country %s matches no allow code (implicit deny)", country)
	}
	return "allow", fmt.Sprintf("country %s matches no deny code and the rule has no allowlist", country)
}

func validCountry(country string) bool {
	if len(country) != 2 {
		return false
	}
	for i := 0; i < len(country); i++ {
		if country[i] < 'A' || country[i] > 'Z' {
			return false
		}
	}
	return true
}

func matchedSelectors(rule api.EdgeRuleResponse) string {
	methods := strings.Join(rule.MatchMethods, ",")
	if methods == "" {
		methods = "*"
	}
	matchPath := rule.MatchPath
	if matchPath == "" {
		matchPath = "*"
	}
	selectors := fmt.Sprintf("host %q, method %q, path %q", rule.MatchHost, methods, matchPath)
	if len(rule.MatchHeaders) > 0 {
		headerNames := make([]string, 0, len(rule.MatchHeaders))
		for name := range rule.MatchHeaders {
			headerNames = append(headerNames, name)
		}
		sort.Strings(headerNames)
		var conditions []string
		for _, name := range headerNames {
			conditions = append(conditions, fmt.Sprintf("%s=%q", name, redactTraceHeaderValue(name, rule.MatchHeaders[name])))
		}
		selectors += ", headers [" + strings.Join(conditions, ", ") + "]"
	}
	return selectors + " match"
}

func headerSnapshot(headers http.Header) map[string][]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string][]string, len(headers))
	for name, values := range headers {
		key := strings.ToLower(name)
		for _, value := range values {
			out[key] = append(out[key], redactTraceHeaderValue(key, value))
		}
	}
	return out
}

func headerMismatch(expected map[string]string, actual http.Header) string {
	names := make([]string, 0, len(expected))
	for name := range expected {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := expected[name]
		if !api.EdgeRuleRequestHeadersMatch(map[string]string{name: value}, actual) {
			return fmt.Sprintf("request header %q has no value equal to %q", name, redactTraceHeaderValue(name, value))
		}
	}
	return "request headers do not match"
}

func redactTraceOutput(result *Result) {
	if result == nil {
		return
	}
	result.Headers = redactHeaderSnapshot(result.Headers)
	for i := range result.Rules {
		row := &result.Rules[i]
		row.MatchHeaders = redactHeaderValues(row.MatchHeaders)
		row.Reason = redactTraceText(row.Reason)
		row.OutcomeReason = redactTraceText(row.OutcomeReason)
		if row.ActionPreview != nil {
			redactActionPreview(row.ActionPreview)
		}
	}
	redactSimulation(&result.Simulation)
}

func redactSimulation(simulation *Simulation) {
	if simulation == nil {
		return
	}
	simulation.RequestHeaders = redactHeaderSnapshot(simulation.RequestHeaders)
	simulation.ResponseHeaderOps = redactHeaderOps(simulation.ResponseHeaderOps)
	simulation.RedirectHeaders = redactHeaderValues(simulation.RedirectHeaders)
	simulation.Location = redactTraceText(simulation.Location)
	simulation.Message = redactTraceText(simulation.Message)
	simulation.Reason = redactTraceText(simulation.Reason)
	for i := range simulation.Steps {
		step := &simulation.Steps[i]
		step.RedirectHeaders = redactHeaderValues(step.RedirectHeaders)
		step.RequestOps = redactHeaderOps(step.RequestOps)
		step.ResponseOps = redactHeaderOps(step.ResponseOps)
		step.Location = redactTraceText(step.Location)
		step.Message = redactTraceText(step.Message)
		step.Reason = redactTraceText(step.Reason)
	}
}

func redactActionPreview(preview *ActionPreview) {
	if preview == nil {
		return
	}
	preview.RedirectHeaders = redactHeaderValues(preview.RedirectHeaders)
	preview.RequestHeaderOps = redactHeaderOps(preview.RequestHeaderOps)
	preview.ResponseHeaderOps = redactHeaderOps(preview.ResponseHeaderOps)
	preview.Location = redactTraceText(preview.Location)
	preview.Message = redactTraceText(preview.Message)
}

func redactHeaderSnapshot(headers map[string][]string) map[string][]string {
	if len(headers) == 0 {
		return headers
	}
	out := make(map[string][]string, len(headers))
	for name, values := range headers {
		for _, value := range values {
			out[name] = append(out[name], redactTraceHeaderValue(name, value))
		}
	}
	return out
}

func redactHeaderValues(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return headers
	}
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		out[name] = redactTraceHeaderValue(name, value)
	}
	return out
}

func redactHeaderOps(ops []api.EdgeRuleHeaderOp) []api.EdgeRuleHeaderOp {
	if len(ops) == 0 {
		return ops
	}
	out := append([]api.EdgeRuleHeaderOp(nil), ops...)
	for i := range out {
		out[i].Value = redactTraceHeaderValue(out[i].Name, out[i].Value)
	}
	return out
}

func redactTraceHeaderValue(name, value string) string {
	if isSensitiveTraceHeader(name) && value != "" {
		return redactedHeaderValue
	}
	return redactTraceText(value)
}

func redactTraceText(value string) string {
	redacted, _ := traceOutputRedactor.Apply(value)
	return redacted
}

func isSensitiveTraceHeader(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "access-control-allow-credentials" {
		return false
	}
	compact := strings.NewReplacer("-", "", "_", "", ".", "").Replace(name)
	for _, marker := range []string{
		"auth", "cookie", "token", "apikey", "accesskey", "idempotencykey", "secret", "password", "passwd",
		"credential", "session", "signature", "privatekey", "clientkey", "refresh",
	} {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	return false
}

// RedactHeaderInputForDisplay masks credential-like request header values
// before submitted form data is echoed back into the dashboard.
func RedactHeaderInputForDisplay(raw string) string {
	if raw == "" {
		return ""
	}
	lines := strings.SplitAfter(raw, "\n")
	for i, line := range lines {
		newline := ""
		if strings.HasSuffix(line, "\n") {
			newline = "\n"
			line = strings.TrimSuffix(line, newline)
		}
		carriageReturn := ""
		if strings.HasSuffix(line, "\r") {
			carriageReturn = "\r"
			line = strings.TrimSuffix(line, carriageReturn)
		}
		if strings.TrimSpace(line) == "" {
			lines[i] = line + carriageReturn + newline
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			lines[i] = redactedHeaderValue + carriageReturn + newline
			continue
		}
		valueStart := colon + 1
		for valueStart < len(line) && (line[valueStart] == ' ' || line[valueStart] == '\t') {
			valueStart++
		}
		value := redactTraceHeaderValue(line[:colon], line[valueStart:])
		lines[i] = line[:valueStart] + value + carriageReturn + newline
	}
	return strings.Join(lines, "")
}

// traceConditionMatches evaluates a rule's ADR-906 match condition with the
// gateway's evaluator. The simulated client IP and country stand in for the
// trusted values the gateway would see; the trace takes no query string, so
// query fields are absent. A condition that does not compile never matches,
// as on the gateway.
func traceConditionMatches(rule api.EdgeRuleResponse, input Input, requestPath, method string, headers http.Header) bool {
	if rule.Match == nil {
		return true
	}
	program, err := api.CompileEdgeRuleMatchWithLists(rule.Match, traceEdgeRuleLists(rule.Match, input.EdgeRuleLists))
	if err != nil {
		return false
	}
	return program.Matches(api.EdgeRuleMatchInput{
		Method: method, Path: requestPath, Host: input.Host, Headers: headers,
		ClientIP: net.ParseIP(input.ClientIP), Country: input.Country,
	})
}

// ReferencedEdgeRuleLists returns the distinct list names the rules'
// conditions reference, so callers load only those (ADR-907).
func ReferencedEdgeRuleLists(rules []api.EdgeRuleResponse) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, r := range rules {
		for _, name := range api.EdgeRuleMatchListRefs(r.Match) {
			if _, ok := seen[name]; !ok {
				seen[name] = struct{}{}
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// traceEdgeRuleLists compiles the supplied lists that expr references.
func traceEdgeRuleLists(expr *api.EdgeRuleMatchExpr, supplied []api.EdgeRuleListResponse) api.EdgeRuleLists {
	names := api.EdgeRuleMatchListRefs(expr)
	if len(names) == 0 {
		return nil
	}
	out := make(api.EdgeRuleLists, len(names))
	for _, name := range names {
		for _, l := range supplied {
			if l.Name != name {
				continue
			}
			if compiled, err := api.CompileEdgeRuleList(l.Kind, l.Items); err == nil {
				out[name] = compiled
			}
		}
	}
	return out
}

// HostMatches is the gateway's match_host comparison (case-insensitive,
// "*" / "*.suffix" / exact / glob), shared through pkg/api so the simulator
// and the gateway cannot disagree on which hosts a rule covers.
func HostMatches(pattern, host string) bool {
	return api.EdgeRuleHostMatches(pattern, host)
}

// MethodMatches compares methods case-insensitively; an empty selector matches
// every method.
func MethodMatches(methods []string, method string) bool {
	if len(methods) == 0 {
		return true
	}
	for _, candidate := range methods {
		if strings.EqualFold(candidate, method) {
			return true
		}
	}
	return false
}

// ruleMethodMatches uses the gateway's case-sensitive lookup for CORS methods.
// The ordinary HTTP method has already been normalized; Access-Control-Request-Method
// is passed to the gateway matcher as submitted, without case normalization.
func ruleMethodMatches(kind string, methods []string, method string) bool {
	if kind != "cors" || len(methods) == 0 {
		return MethodMatches(methods, method)
	}
	for _, candidate := range methods {
		if candidate == method {
			return true
		}
	}
	return false
}

func validRequestHost(host string) bool {
	if host == "" || len(host) > 253 || strings.ContainsAny(host, ":/[]@?# \t\r\n") {
		return net.ParseIP(host) != nil
	}
	if net.ParseIP(host) != nil {
		return true
	}
	labels := strings.Split(strings.TrimSuffix(host, "."), ".")
	if len(labels) == 0 || labels[0] == "" {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

func normalizeRequestHeaders(headers http.Header) (http.Header, error) {
	normalizedHeaders := make(http.Header, len(headers))
	for name, values := range headers {
		for _, value := range values {
			normalized, err := api.NormalizeEdgeRuleMatchHeaders(map[string]string{name: value})
			if err != nil {
				return nil, err
			}
			for normalizedName := range normalized {
				normalizedHeaders.Add(normalizedName, value)
			}
		}
	}
	return normalizedHeaders, nil
}

func cloneHeaders(headers http.Header) http.Header {
	cloned := make(http.Header, len(headers))
	for name, values := range headers {
		cloned[name] = append([]string(nil), values...)
	}
	return cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func applyHeaderOps(headers http.Header, ops []api.EdgeRuleHeaderOp) {
	for _, op := range ops {
		switch op.Action {
		case "remove":
			headers.Del(op.Name)
		case "set":
			if op.Value == "" {
				headers.Del(op.Name)
			} else {
				headers.Set(op.Name, op.Value)
			}
		case "add":
			if op.Value != "" {
				headers.Add(op.Name, op.Value)
			}
		}
	}
}
