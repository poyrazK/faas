package outbound

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/trace"
)

const (
	TokenHeader = "X-Gregale-Outbound-Token"
	AppHeader   = "X-Gregale-App-ID"
	Prefix      = "/i/"

	defaultMaxBodyBytes           int64 = 25 << 20
	defaultMaxResponseBytes       int64 = 25 << 20
	defaultMaxResponseHeaderBytes int64 = 64 << 10
	defaultMaxResponseHeaders           = 128
)

var errResponseBodyTooLarge = errors.New("outbound response body exceeds gateway limit")

var hopByHopHeaders = map[string]struct{}{
	"Connection": {}, "Keep-Alive": {}, "Proxy-Authenticate": {},
	"Proxy-Authorization": {}, "Te": {}, "Trailer": {},
	"Transfer-Encoding": {}, "Upgrade": {},
}

// Handler is an explicit request-aware outbound gateway. A request to
// /i/{integrationID}/path is sent only to that integration's configured origin.
type Handler struct {
	SimulationHandler         http.Handler
	Resolver                  Resolver
	Backend                   Backend
	Client                    *http.Client
	Metrics                   *Metrics
	MaxBodyBytes              int64
	MaxResponseBytes          int64
	MaxResponseHeaderBytes    int64
	MaxResponseHeaders        int
	IdentityVerifier          IdentityVerifier
	ExecutionIdentityVerifier ExecutionIdentityVerifier
	ExecutionAuthorizer       ExecutionAuthorizer
	WorkflowAuthorizer        WorkflowAuthorizer
	workflowClient            *http.Client
	CredentialResolver        ManagedCredentialResolver
	// Routes resolves AI fallback routes (ADR-990); nil disables /r/.
	Routes               RouteResolver
	managedAuthorization map[string]string
	responseCache        *outboundResponseCache
	// vertexTokens caches exchanged Google access tokens (ADR-991).
	vertexTokens vertexTokenCache
	// keyPools holds local key cooldowns and round-robin positions.
	keyPools keyPoolState
	// AIGatewayEnabled is the AI gateway release gate (ADR-991). While it
	// is off, AI integrations and routes are refused rather than served
	// without their budgets and accounting. NewHandler turns it on; the
	// daemon sets it from its configuration, where it defaults to off.
	AIGatewayEnabled bool
	// AIGatewayRollout limits customer AI integrations to listed accounts
	// during the staged preview rollout; nil admits every account
	// (ADR-998).
	AIGatewayRollout AIGatewayRollout
	// AIGatewayFlags, when set, is the live ai_gateway platform flag
	// (ADR-1700). It replaces AIGatewayEnabled and AIGatewayRollout, so an
	// operator change applies to the next call without a restart.
	AIGatewayFlags *platformflags.Set
	// aiContainers charges each code-interpreter container once per
	// session window (ADR-1433).
	aiContainers aiContainerSessions
	// promptPrefixes remembers repeated prompt prefixes for automatic
	// prompt caching (ADR-1437).
	promptPrefixes promptPrefixes
	// routeRandom replaces the routing draw in tests (ADR-1434).
	routeRandom func(int) int
}

// aiGatewayOn reports the AI gateway kill switch.
func (h *Handler) aiGatewayOn() bool {
	if h.AIGatewayFlags != nil {
		return h.AIGatewayFlags.Snapshot().GloballyEnabled(platformflags.AIGateway)
	}
	return h.AIGatewayEnabled
}

// ManagedCredentialResolver supplies a customer-sealed Authorization value
// only inside outboundd. It must never return this value to the caller app.
type ManagedCredentialResolver interface {
	Authorization(context.Context, string) (string, error)
}

// SetManagedAuthorizations configures provider Authorization values held by
// outboundd. Call it before serving requests. Values are copied so callers
// cannot change a live handler's credentials through the input map.
func (h *Handler) SetManagedAuthorizations(values map[string]string) error {
	if h == nil {
		return errors.New("outbound handler is nil")
	}
	copyValues := make(map[string]string, len(values))
	for integrationID, value := range values {
		if strings.TrimSpace(integrationID) == "" || !ValidManagedAuthorization(value) {
			return errors.New("outbound managed authorization has an invalid integration ID or value")
		}
		copyValues[integrationID] = value
	}
	h.managedAuthorization = copyValues
	return nil
}

// ValidManagedAuthorization accepts one bounded HTTP Authorization value.
// Never include the value in an error: it is a provider credential.
func ValidManagedAuthorization(value string) bool {
	if value == "" || len(value) > ManagedAuthorizationMaxBytes || strings.TrimSpace(value) != value {
		return false
	}
	for i := range len(value) {
		if value[i] < 0x20 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func NewHandler(resolver Resolver, backend Backend, client *http.Client) (*Handler, error) {
	if resolver == nil || backend == nil {
		return nil, errors.New("outbound resolver and backend are required")
	}
	if client == nil {
		defaultTransport, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			defaultTransport = &http.Transport{}
		}
		transport := defaultTransport.Clone()
		// Do not inherit HTTP(S)_PROXY from the daemon environment: a proxy
		// would resolve the destination outside this process's public-IP guard.
		transport.Proxy = nil
		transport.DialContext = NewPublicDestinationDialer().DialContext
		transport.DialTLSContext = nil
		transport.TLSClientConfig = nil
		client = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	} else {
		clone := *client
		// A shared cookie jar could leak one integration's provider session to
		// another. The gateway is token/header based, so cookies are disabled.
		clone.Jar = nil
		// Redirects would turn a fixed-origin policy into an origin hop and
		// can replay a request body. The gateway owns this invariant even when
		// a caller supplies a custom HTTP client.
		clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &clone
	}
	if client.Transport == nil {
		client.Transport = http.DefaultTransport
	}
	workflowClient := newWorkflowHTTPClient(client)
	client.Transport = newDependencyTransport(client.Transport)
	responseCache, err := newOutboundResponseCache()
	if err != nil {
		return nil, errors.New("outbound response cache key could not be initialized")
	}
	return &Handler{
		Resolver:               resolver,
		Backend:                backend,
		Client:                 client,
		workflowClient:         workflowClient,
		responseCache:          responseCache,
		MaxBodyBytes:           defaultMaxBodyBytes,
		MaxResponseBytes:       defaultMaxResponseBytes,
		MaxResponseHeaderBytes: defaultMaxResponseHeaderBytes,
		MaxResponseHeaders:     defaultMaxResponseHeaders,
		AIGatewayEnabled:       true,
	}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if principal, ok := relayPrincipalFromContext(r.Context()); ok && principal.rehearsalRunID != "" {
		if h.SimulationHandler == nil {
			writeProblem(w, http.StatusServiceUnavailable, "rehearsal_simulation_unavailable", "Rehearsal simulation unavailable", "")
			return
		}
		h.SimulationHandler.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/readyz" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeProblem(w, http.StatusMethodNotAllowed, "outbound_method_not_allowed", "Only GET and HEAD are supported for readiness", "")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, "ok\n")
		}
		return
	}
	if r.Method == http.MethodConnect {
		writeProblem(w, http.StatusMethodNotAllowed, "outbound_method_not_allowed", "CONNECT is not supported", "")
		return
	}
	if strings.HasPrefix(r.URL.EscapedPath(), RoutePrefix) {
		h.serveRoute(w, r)
		return
	}
	// Split before unescaping so an encoded slash remains part of a provider
	// path segment instead of becoming a different resource hierarchy.
	id, path, ok := parsePath(r.URL.EscapedPath())
	id, pathErr := url.PathUnescape(id)
	if !ok || pathErr != nil {
		writeProblem(w, http.StatusNotFound, "outbound_integration_not_found", "Outbound integration not found", "")
		return
	}
	integration, err := h.Resolver.Integration(r.Context(), id)
	if errors.Is(err, ErrIntegrationNotFound) {
		writeProblem(w, http.StatusNotFound, "outbound_integration_not_found", "Outbound integration not found", "")
		return
	}
	if errors.Is(err, ErrIntegrationDisabled) {
		writeProblem(w, http.StatusServiceUnavailable, "outbound_integration_disabled", "Outbound integration is disabled", "60")
		return
	}
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "outbound_integration_unavailable", "Outbound integration is unavailable", "1")
		return
	}
	metricIntegrationID := integration.MetricLabel()
	if integration.OwnerKind == IntegrationOwnerCustomer && accountSuspended(integration.AccountStatus) {
		// ADR 20261027132936: a suspended account's integrations call nothing,
		// from its apps or from outside with an access token.
		h.Metrics.ObserveRejection(metricIntegrationID, "account_suspended")
		writeProblem(w, http.StatusForbidden, "outbound_account_suspended", "The account is suspended", "")
		return
	}
	if integration.AIProvider != "" && !h.aiGatewayOn() {
		h.Metrics.ObserveRejection(metricIntegrationID, "ai_gateway_disabled")
		writeProblem(w, http.StatusServiceUnavailable, "outbound_ai_gateway_disabled", "The AI gateway is not enabled on this platform", "")
		return
	}
	if integration.AIProvider != "" && !h.aiGatewayAdmitsAccount(integration) {
		// ADR-998: the staged rollout and the account's plan.
		h.Metrics.ObserveRejection(metricIntegrationID, "ai_gateway_not_enabled_for_account")
		writeProblem(w, http.StatusForbidden, "outbound_ai_gateway_unavailable", "The AI gateway is not enabled for this account", "")
		return
	}
	// ADR-997: the replay worker calls in process for an account's own
	// customer AI integrations; nothing else can carry its principal.
	replay, isReplay := replayPrincipalFromContext(r.Context())
	if isReplay && !replayAllowed(integration, replay) {
		writeProblem(w, http.StatusForbidden, "outbound_replay_not_authorized", "The replay cannot use this outbound integration", "")
		return
	}
	// ADR-1340, ADR-1431: the gateway's own checks, evaluations and batch
	// reads call in process for the account's own customer AI
	// integrations, like a replay.
	internal, isInternal := internalPrincipalFromContext(r.Context())
	if isInternal && !internalAllowed(integration, internal) {
		writeProblem(w, http.StatusForbidden, "outbound_internal_not_authorized", "The gateway's own call cannot use this outbound integration", "")
		return
	}
	// ADR-20261019152416: the gateway's own probe of an enforced candidate's
	// binding calls in process as the candidate's app.
	qualification, isQualification := qualificationPrincipalFromContext(r.Context())
	if isQualification && !qualificationAllowed(integration, qualification, r.Method, path) {
		writeProblem(w, http.StatusForbidden, "outbound_qualification_not_authorized", "The qualification probe cannot use this outbound integration", "")
		return
	}
	var workflowIdentity *WorkflowIdentity
	var executionIdentity ExecutionIdentity
	var isExecution bool
	if !isReplay && !isInternal && !isQualification {
		if workflowIdentity, ok = h.callerWorkflowIdentity(w, r, integration, path); !ok {
			return
		}
		if executionIdentity, isExecution, ok = h.callerExecutionIdentity(w, r, integration); !ok {
			return
		}
	}
	appID := ""
	// callerInstance is the calling app instance a verified workload
	// identity names, which attributes AI calls to its deployment (ADR
	// 20261022153155).
	callerInstance := ""
	principalID := ""
	if isReplay {
		principalID = "replay:" + replay.replayID
	} else if isInternal {
		principalID = internal.caller + ":" + internal.accountID
	} else if isQualification {
		appID = qualification.appID
		principalID = "qualification:" + qualification.requestID
	} else if isExecution {
		if h.ExecutionAuthorizer == nil {
			writeProblem(w, http.StatusServiceUnavailable, "outbound_execution_authorization_unavailable", "Runs outbound authorization is unavailable", "1")
			return
		}
		allowed, authorizeErr := h.ExecutionAuthorizer.AuthorizeExecution(r.Context(), executionIdentity, integration.ID)
		if authorizeErr != nil {
			writeProblem(w, http.StatusServiceUnavailable, "outbound_execution_authorization_unavailable", "Runs outbound authorization is unavailable", "1")
			return
		}
		if !allowed {
			writeProblem(w, http.StatusForbidden, "outbound_execution_not_authorized", "This Run is not authorized for the outbound integration", "")
			return
		}
		principalID = "execution:" + executionIdentity.ExecutionID
	} else if workflowIdentity != nil {
		appID = workflowIdentity.AppID
		principalID = "workflow:" + workflowIdentity.RunID
	} else {
		appID, callerInstance, ok = h.callerAppID(w, r, integration)
		if !ok {
			return
		}
		if !integration.AllowsApp(appID) {
			writeProblem(w, http.StatusForbidden, "outbound_app_not_attached", "The app is not attached to this outbound integration", "")
			return
		}
		principalID = appID
	}
	if integration.AIProvider != "" && path == AIFeedbackPath && !isReplay && !isInternal {
		h.serveAIFeedback(w, r, integration, appID)
		return
	}
	var allowedRoute bool
	if isExecution || isReplay || isInternal {
		allowedRoute = integration.AllowsRequest(r.Method, path)
	} else {
		allowedRoute = integration.AllowsAppRequest(appID, r.Method, path)
	}
	if !allowedRoute || (integration.ProviderAuthMode == ProviderAuthManaged && hasMethodOverride(r)) {
		writeProblem(w, http.StatusForbidden, "outbound_route_not_allowed", "Outbound method or path is not allowed", "")
		return
	}
	aiEndUser, aiAffinity, ok := aiCallerKeys(r, integration)
	if !ok {
		h.Metrics.ObserveRejection(metricIntegrationID, "outbound_ai_user_invalid")
		writeProblem(w, http.StatusBadRequest, "outbound_ai_user_invalid",
			"X-Gregale-AI-User and X-Gregale-AI-Session must each be one ID of 1 to 64 letters, digits or . _ : @ + = / | -", "")
		return
	}
	aiTags, ok := aiCallTags(r, integration)
	if !ok {
		h.Metrics.ObserveRejection(metricIntegrationID, "outbound_ai_tags_invalid")
		writeProblem(w, http.StatusBadRequest, "outbound_ai_tags_invalid",
			"X-Gregale-AI-Tags must be one header of at most 5 key=value tags with distinct keys: keys of 1 to 32 lowercase "+
				"letters, digits, _ . or - starting with a letter, values of 1 to 64 letters, digits or . _ : @ + / -", "")
		return
	}
	// ADR 20261020202035: the call's AI trace, kept when the integration
	// records traces.
	aiTrace, ok := aiCallTrace(r, integration)
	if !ok {
		h.Metrics.ObserveRejection(metricIntegrationID, "outbound_ai_trace_invalid")
		writeProblem(w, http.StatusBadRequest, "outbound_ai_trace_invalid",
			"X-Gregale-AI-Trace must be one ID of 1 to 64 letters, digits or . _ : @ + = / | -", "")
		return
	}
	if !integration.recordsAITraces() {
		aiTrace = ""
	}
	// ADR 20261019072722: a WebSocket upgrade on an AI integration is a
	// realtime session, metered or let through unmetered, or refused.
	realtime, realtimeRejection := planRealtime(r, integration, path)
	if realtimeRejection != nil {
		h.Metrics.ObserveRejection(metricIntegrationID, realtimeRejection.code)
		writeProblem(w, realtimeRejection.status, realtimeRejection.code, realtimeRejection.title, "")
		return
	}
	var managedAuthorization string
	var poolKeys []ManagedKey
	if integration.ProviderAuthMode == ProviderAuthManaged && integration.CredentialSource != CredentialSourceCustomerSealed {
		managedAuthorization = h.managedAuthorization[integration.ID]
		if !ValidManagedAuthorization(managedAuthorization) {
			writeProblem(w, http.StatusServiceUnavailable, "outbound_provider_credential_unavailable", "Outbound provider credential is unavailable", "1")
			return
		}
	}
	if h.MaxBodyBytes > 0 && r.ContentLength > h.MaxBodyBytes {
		writeProblem(w, http.StatusRequestEntityTooLarge, "outbound_request_too_large", "Outbound request body exceeds the gateway limit", "")
		return
	}
	if h.MaxBodyBytes > 0 && r.Body != nil && r.Body != http.NoBody {
		// ContentLength is -1 for chunked requests. Always wrap the body so
		// the same cap applies when the caller omits a length or lies about it.
		r.Body = http.MaxBytesReader(w, r.Body, h.MaxBodyBytes)
	}
	aiReq, aiForm, err := readAIRequest(r, integration, path)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeProblem(w, http.StatusRequestEntityTooLarge, "outbound_request_too_large", "Outbound request body exceeds the gateway limit", "")
			return
		}
		writeProblem(w, http.StatusBadRequest, "outbound_request_unreadable", "Outbound request body could not be read", "")
		return
	}
	bindingAppID := ""
	var bindingDailyRequestLimit, bindingDailyAITokenLimit *int64
	_, explicitlyBound := integration.BindingAppIDs[appID]
	if appID != "" && (integration.OwnerKind == IntegrationOwnerCustomer || explicitlyBound) {
		bindingAppID = appID
		bindingDailyRequestLimit = integration.BindingDailyRequestLimits[appID]
		bindingDailyAITokenLimit = integration.BindingDailyAITokenLimits[appID]
	}
	// ADR-991: guardrails run before admission; the plan carries the call's
	// reservation into it.
	aiPlan, rejection := planAICall(r, integration, bindingAppID, path, aiReq, aiForm)
	if aiPlan != nil {
		h.observeContentPolicy(w, metricIntegrationID, aiPlan.content)
		if aiPlan.unmetered {
			// ADR-1432: the AI policy lets this write through outside the caps.
			w.Header().Set(AIUnmeteredHeader, "allowed")
			h.Metrics.ObserveAIUnmetered(metricIntegrationID)
		}
	}
	if rejection == nil {
		// ADR 20261027132936: a cost cap cannot count a model with no price.
		rejection = checkAICallPriced(integration, aiPlan, aiEndUser, aiTags, aiTrace)
	}
	if rejection != nil {
		h.Metrics.ObserveRejection(metricIntegrationID, rejection.code)
		writeProblem(w, rejection.status, rejection.code, rejection.title, "")
		return
	}
	if marks := h.applyPromptCache(r, integration, aiPlan, aiReq); len(marks) > 0 {
		// ADR-1437: the breakpoints the gateway added for the provider's cache.
		w.Header().Set(AIPromptCacheHeader, strings.Join(marks, ","))
		for _, mark := range marks {
			h.Metrics.ObserveAIPromptCacheMark(metricIntegrationID, mark)
		}
	}
	// ADR-1431: a batch creation is admitted at the batch's estimate.
	var batch *AIBatch
	if aiPlan != nil && aiPlan.endpoint.batch {
		if batch, ok = h.planBatch(w, r, integration, aiPlan, aiReq, path, appID, bindingAppID, metricIntegrationID); !ok {
			return
		}
	}
	var reserve aiReservation
	var guard AIGuardrails
	if aiPlan != nil {
		reserve, guard = aiPlan.reserve, aiPlan.guard
	}
	spec := AdmissionSpec{
		IntegrationID: integration.ID, RatePerSecond: integration.RatePerSecond,
		Burst: integration.Burst, MaxInFlight: integration.MaxInFlight,
		DailyRequestLimit:              integration.DailyRequestLimit,
		DailyAITokenLimit:              copyInt64Pointer(integration.DailyAITokenLimit),
		BindingDailyAITokenLimit:       copyInt64Pointer(bindingDailyAITokenLimit),
		DailyAICostLimit:               copyInt64Pointer(integration.DailyAICostLimit),
		MonthlyAICostLimit:             copyInt64Pointer(integration.MonthlyAICostLimit),
		AIReservedTokens:               reserve.tokens,
		AIReservedCostMillicents:       reserve.cost,
		BindingRequestsPerMinute:       copyInt64Pointer(guard.RequestsPerMinute),
		BindingTokensPerMinute:         copyInt64Pointer(guard.TokensPerMinute),
		AIEndUser:                      aiEndUser,
		UserDailyAITokenLimit:          copyInt64Pointer(integration.AIUserDailyTokenLimit),
		UserDailyAICostLimit:           copyInt64Pointer(integration.AIUserDailyCostLimit),
		AITags:                         aiTags,
		AITagDailyCostLimits:           tagDailyCostLimits(integration, aiTags),
		AITraceID:                      aiTrace,
		AITraceLimits:                  integration.traceLimits(),
		AITraceNamed:                   aiTrace != "" && r.Header.Get(api.OutboundAITraceHeader) != "",
		BindingAppID:                   bindingAppID,
		BindingDailyRequestLimit:       copyInt64Pointer(bindingDailyRequestLimit),
		CircuitBreakerFailureThreshold: integration.CircuitBreakerFailureThreshold,
		CircuitBreakerOpenSeconds:      integration.CircuitBreakerOpenSeconds,
		RetryBudgetPerMinute:           integration.RetryBudgetPerMinute,
		LeaseTTL:                       integration.RequestTimeout,
		PolicyRevision:                 integration.PolicyRevision,
	}
	decision, err := h.Backend.Admit(r.Context(), spec)
	if err != nil {
		h.Metrics.ObserveAdmission(metricIntegrationID, "error")
		h.Metrics.ObserveRejection(metricIntegrationID, "backend_unavailable")
		writeProblem(w, http.StatusServiceUnavailable, "outbound_admission_unavailable", "Outbound admission is temporarily unavailable", "1")
		return
	}
	if !decision.Granted {
		h.Metrics.ObserveAdmission(metricIntegrationID, "rejected")
		h.Metrics.ObserveRejection(metricIntegrationID, decision.Reason)
		if decision.Reason == ReasonAppNotAttached {
			writeProblem(w, http.StatusForbidden, "outbound_app_not_attached", "The app is not attached to this outbound integration", "")
			return
		}
		retry := retryAfterSeconds(decision.RetryAfter)
		if traceRejection(decision.Reason) {
			retry = ""
		}
		w.Header().Set("X-Gregale-Outbound-Rejection", decision.Reason)
		writeProblem(w, http.StatusTooManyRequests, "outbound_budget_exhausted", "Outbound integration budget is exhausted", retry)
		return
	}
	h.Metrics.ObserveAdmission(metricIntegrationID, "granted")
	h.Metrics.IncInFlight(metricIntegrationID)
	var aiCallID string
	if aiPlan != nil {
		// ADR 20261021171712: the call's ID, which feedback names.
		aiCallID = uuid.NewString()
		w.Header().Set(api.OutboundAICallHeader, aiCallID)
	}

	ctx := r.Context()
	requestTimeout := decision.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = integration.RequestTimeout
	}
	// ADR 20261027132936: a billable AI call's request timeout bounds the
	// wait for its response headers; its answer may take longer.
	var aiCall *aiCallContext
	if aiPlan != nil && aiPlan.endpoint.billable() && batch == nil && realtime == realtimeNone {
		aiCall = newAICallContext(ctx, requestTimeout, time.Now())
		defer aiCall.Close()
		ctx = aiCall //nolint:contextcheck // aiCall is derived from ctx through context.WithDeadline and WithCancelCause.
		defer h.renewAILease(integration.ID, decision.LeaseID, requestTimeout)()
	} else if requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, requestTimeout)
		defer cancel()
	}
	defer func() {
		h.Metrics.DecInFlight(metricIntegrationID)
		_ = h.Backend.Release(context.WithoutCancel(ctx), integration.ID, decision.LeaseID)
	}()
	if batch != nil {
		// The batch's reservation is stored before the provider sees the
		// batch, so no batch the provider runs is ever uncounted.
		if !h.reserveBatch(ctx, w, *batch, metricIntegrationID) {
			return
		}
		defer h.dropUnconfirmedBatch(ctx, batch)
	}
	var moderation aiModerationOutcome
	if integration.ProviderAuthMode == ProviderAuthManaged && integration.CredentialSource == CredentialSourceCustomerSealed {
		if h.CredentialResolver == nil {
			writeProblem(w, http.StatusServiceUnavailable, "outbound_provider_credential_unavailable", "Outbound provider credential is unavailable", "1")
			return
		}
		if pool, ok := h.CredentialResolver.(ManagedKeyPoolResolver); ok && h.aiGatewayOn() {
			// ADR-991: a pool of keys rotates per call and on 429s.
			keys, poolErr := pool.Authorizations(ctx, integration.ID)
			if keys = validManagedKeys(keys); poolErr == nil && len(keys) > 0 {
				poolKeys = h.keyPools.order(integration.ID, keys, time.Now(), aiAffinity)
				managedAuthorization = poolKeys[0].Value
			}
		} else {
			managedAuthorization, err = h.CredentialResolver.Authorization(ctx, integration.ID)
			if err != nil {
				managedAuthorization = ""
			}
		}
		if !ValidManagedAuthorization(managedAuthorization) {
			writeProblem(w, http.StatusServiceUnavailable, "outbound_provider_credential_unavailable", "Outbound provider credential is unavailable", "1")
			return
		}
	}
	if workflowIdentity != nil && workflowIdentity.Attempt > 1 && !h.consumeRetryBudget(ctx, integration.ID, metricIntegrationID, decision.RetryBudgetPerMinute) {
		writeProblem(w, http.StatusTooManyRequests, "outbound_retry_budget_exhausted", "Outbound retry budget is exhausted", "60")
		return
	}
	upstreamStarted := time.Now()
	upstreamURL, err := targetURL(integration.Origin, path, r.URL.RawQuery)
	if err != nil {
		h.Metrics.ObserveUpstreamError(metricIntegrationID, time.Since(upstreamStarted))
		writeProblem(w, http.StatusBadGateway, "outbound_target_invalid", "Outbound integration target is invalid", "")
		return
	}
	dependencyCtx, dependencySpan := trace.StartSpan(ctx, dependencySpanName(integration), dependencySpanAttributes(integration, appID)...)
	defer dependencySpan.End()
	// ADR 20261027132936: a billable call the provider received settles
	// even when its answer never comes back.
	var requestSent atomic.Bool
	if aiCall != nil {
		dependencyCtx = httptrace.WithClientTrace(dependencyCtx, &httptrace.ClientTrace{
			WroteRequest: func(info httptrace.WroteRequestInfo) {
				if info.Err == nil {
					requestSent.Store(true)
				}
			},
		})
	}
	upstreamReq, err := http.NewRequestWithContext(dependencyCtx, r.Method, upstreamURL, r.Body)
	if err != nil {
		dependencySpan.RecordError(err)
		dependencySpan.SetStatus(codes.Error, "request construction failed")
		h.Metrics.ObserveUpstreamError(metricIntegrationID, time.Since(upstreamStarted))
		writeProblem(w, http.StatusBadGateway, "outbound_request_invalid", "Outbound request could not be constructed", "")
		return
	}
	upstreamReq.Header = forwardedHeaders(r.Header)
	if integration.AIProvider != "" {
		// ADR-1435: the transport negotiates compression and decompresses, so
		// usage and response checks read plain bytes.
		upstreamReq.Header.Del("Accept-Encoding")
	}
	// NewRequest cannot infer the length of a server-side ReadCloser (or
	// MaxBytesReader). Preserve known lengths and the explicit empty-body
	// sentinel so providers do not unexpectedly receive chunked uploads.
	upstreamReq.ContentLength = r.ContentLength
	if r.ContentLength == 0 {
		upstreamReq.Body = http.NoBody
	}
	var cacheBody []byte
	if aiReq != nil {
		// ADR-990: a buffered AI body is replayable for retries and fallback.
		setReplayableBody(upstreamReq, aiReq.body)
		cacheBody = aiReq.body
	}
	cacheEligible := workflowIdentity == nil && batch == nil && !isQualification && (outboundCacheRequestEligible(upstreamReq, integration.ResponseCacheTTLSeconds) ||
		aiCacheEligible(upstreamReq, aiReq, integration.ResponseCacheTTLSeconds))
	cacheKey := ""
	if cacheEligible {
		// Keyed before the credential is applied: every key of a pool, and
		// every signed or exchanged token, shares one entry (ADR-991).
		cacheKey = h.responseCache.key(integration.ID, principalID, integration.PolicyRevision, integration.ResponseCacheTTLSeconds, upstreamReq, cacheBody)
	}
	if integration.ProviderAuthMode == ProviderAuthManaged {
		// The guest may send an Authorization header, but it cannot replace
		// or read the provider credential held by outboundd.
		if err := h.applyProviderCredential(dependencyCtx, upstreamReq, integration, managedAuthorization, cacheBody); err != nil {
			dependencySpan.SetStatus(codes.Error, "provider credential unusable")
			writeProblem(w, http.StatusServiceUnavailable, "outbound_provider_credential_unavailable", "Outbound provider credential is unavailable", "1")
			return
		}
	}
	if realtime != realtimeNone {
		// The upgrade itself was admitted like a call, with no reservation;
		// each response is admitted with its own (ADR 20261019072722).
		h.serveRealtime(w, r, &realtimeSession{h: h, integration: integration, appID: appID, instanceID: callerInstance, metricID: metricIntegrationID,
			metered: realtime == realtimeMetered, spec: spec, endUser: aiEndUser, tags: aiTags, traceID: aiTrace,
			model: realtimeURLModel(r.URL.Query()), live: livePath(integration.AIProvider, path)}, upstreamReq)
		return
	}
	var resp *http.Response
	attempts := 0
	cacheHit := false
	if cacheEligible {
		if cached, ok := h.responseCache.get(cacheKey, time.Now()); ok {
			resp = cached
			cacheHit = true
			h.Metrics.ObserveCache(metricIntegrationID, "hit")
		} else {
			h.Metrics.ObserveCache(metricIntegrationID, "miss")
		}
	}
	dependencySpan.SetAttributes(attribute.Bool("gregale.outbound.cache_hit", cacheHit))
	// ADR-1340: the moderation check runs after admission, so a call its
	// caps refuse never pays for one. Replays and checks are not checked.
	// It runs only on a cache miss: a hit matched a call already checked
	// under the same policy revision (ADR 20261027132936).
	if !cacheHit && aiPlan != nil && aiPlan.endpoint.billable() && !isReplay && !isInternal && integration.AIContentPolicy.Moderation.Active() {
		var proceed bool
		if moderation, proceed = h.moderateAICall(ctx, w, integration, aiReq, metricIntegrationID); !proceed {
			return
		}
		upstreamStarted = time.Now()
	}
	if !cacheHit {
		cooldown, ok := h.Backend.(ProviderCooldownBackend)
		if !ok {
			h.Metrics.ObserveProviderCooldown(metricIntegrationID, "unavailable")
		} else {
			// ADR-1120: admission read the cooldown under this policy
			// revision; otherwise the cooldown backend resets a stale one.
			var gate ProviderCooldownDecision
			var gateErr error
			if decision.ProviderGate != nil {
				gate = *decision.ProviderGate
			} else {
				gate, gateErr = cooldown.AllowProviderRequest(r.Context(), integration.ID, integration.PolicyRevision)
			}
			if gateErr != nil {
				h.Metrics.ObserveProviderCooldown(metricIntegrationID, "state_error")
				h.Metrics.ObserveRejection(metricIntegrationID, "provider_cooldown_unavailable")
				w.Header().Set("X-Gregale-Outbound-Rejection", "provider_cooldown_unavailable")
				writeProblem(w, http.StatusServiceUnavailable, "outbound_provider_cooldown_unavailable", "Outbound provider cooldown state is unavailable", "1")
				return
			}
			if !gate.Allowed {
				h.Metrics.ObserveProviderCooldown(metricIntegrationID, "blocked")
				h.Metrics.ObserveRejection(metricIntegrationID, ReasonProviderCooldown)
				dependencySpan.SetStatus(codes.Error, "provider requested cooldown")
				w.Header().Set("X-Gregale-Outbound-Rejection", ReasonProviderCooldown)
				writeProblem(w, http.StatusTooManyRequests, "outbound_provider_cooldown", "Outbound provider requested a cooldown", retryAfterSeconds(gate.RetryAfter))
				return
			}
		}
		var breaker CircuitBreakerBackend
		breakerProbe := false
		if decision.CircuitBreakerFailureThreshold > 0 {
			var ok bool
			breaker, ok = h.Backend.(CircuitBreakerBackend)
			if !ok {
				h.Metrics.ObserveRejection(metricIntegrationID, "circuit_breaker_unavailable")
				writeProblem(w, http.StatusServiceUnavailable, "outbound_circuit_breaker_unavailable", "Outbound circuit breaker is unavailable", "1")
				return
			}
			// ADR-1120: a breaker admission saw closed needs no second check.
			gate := CircuitBreakerDecision{Allowed: true}
			var gateErr error
			if !decision.CircuitClosed {
				gate, gateErr = breaker.AllowCircuit(r.Context(), integration.ID, decision.LeaseID,
					decision.CircuitBreakerFailureThreshold, decision.CircuitBreakerOpenSeconds)
			}
			if gateErr != nil {
				h.Metrics.ObserveRejection(metricIntegrationID, "circuit_breaker_unavailable")
				h.Metrics.ObserveCircuit(metricIntegrationID, "state_error")
				writeProblem(w, http.StatusServiceUnavailable, "outbound_circuit_breaker_unavailable", "Outbound circuit breaker is unavailable", "1")
				return
			}
			if !gate.Allowed {
				h.Metrics.ObserveRejection(metricIntegrationID, ReasonCircuitOpen)
				h.Metrics.ObserveCircuit(metricIntegrationID, "open_rejected")
				retry := retryAfterSeconds(gate.RetryAfter)
				w.Header().Set("X-Gregale-Outbound-Rejection", ReasonCircuitOpen)
				writeProblem(w, http.StatusServiceUnavailable, "outbound_circuit_breaker_open", "Outbound provider is temporarily unavailable", retry)
				return
			}
			breakerProbe = gate.Probe
			if breakerProbe {
				h.Metrics.ObserveCircuit(metricIntegrationID, "probe")
			} else {
				h.Metrics.ObserveCircuit(metricIntegrationID, "closed")
			}
		}
		maxRetries := integration.MaxRetries
		client := h.Client
		var keys *credentialRing
		if workflowIdentity != nil {
			maxRetries = 0
			client = h.workflowClient
		} else if len(poolKeys) > 1 {
			keys = &credentialRing{
				integrationID: integration.ID, metricIntegrationID: metricIntegrationID, keys: poolKeys, pools: &h.keyPools, metrics: h.Metrics,
				apply: func(req *http.Request, credential string) error {
					return h.applyProviderCredential(dependencyCtx, req, integration, credential, cacheBody)
				},
			}
		}
		resp, attempts, err = h.doWithRetries(dependencyCtx, client, upstreamReq, integration.ID, metricIntegrationID,
			integration.PolicyRevision, maxRetries, decision.RetryBudgetPerMinute, keys)
		if breaker != nil {
			outcome := outboundCircuitOutcome(resp, err)
			if outcome != CircuitOutcomeNeutral {
				h.Metrics.ObserveCircuit(metricIntegrationID, string(outcome))
			}
			observeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			if recordErr := breaker.RecordCircuitOutcome(observeCtx, integration.ID, decision.LeaseID,
				decision.CircuitBreakerFailureThreshold, decision.CircuitBreakerOpenSeconds, outcome); recordErr != nil {
				h.Metrics.ObserveCircuit(metricIntegrationID, "state_error")
			}
			cancel()
		}
		if err == nil && cacheEligible {
			h.responseCache.storeResponse(cacheKey, resp, integration.ResponseCacheTTLSeconds, time.Now(), h.MaxResponseHeaderBytes, h.MaxResponseHeaders, h.MaxResponseBytes)
		}
	}
	dependencySpan.SetAttributes(attribute.Int("gregale.outbound.attempt_count", attempts))
	if aiCall != nil {
		aiCall.headersArrived()
	}
	// aiSettlementFor is the settlement of this call's AI usage.
	aiSettlementFor := func(status int, check *aiResponseCheck) aiSettlement {
		settlement := aiSettlement{
			status: status, attempts: attempts, started: upstreamStarted, routeID: routeIDFromContext(r.Context()),
			endUser: aiEndUser, tags: aiTags, traceID: aiTrace, leaseID: decision.LeaseID, callID: aiCallID,
			instanceID: callerInstance,
			route:      routeAttemptFromContext(r.Context()),
			moderation: moderation,
			response:   check, cacheHit: cacheHit,
		}
		if aiPlan != nil {
			settlement.reserve, settlement.billable = aiPlan.reserve, aiPlan.endpoint.billable()
			settlement.media, settlement.characters = aiPlan.endpoint.media, aiPlan.facts.characters
			settlement.videoSeconds, settlement.videoResolution = aiPlan.facts.videoSeconds, aiPlan.facts.videoResolution
			settlement.codeGB = aiPlan.facts.tools.codeGB
			settlement.requestModel = aiPlan.facts.model
		}
		return settlement
	}
	if err != nil {
		dependencySpan.RecordError(err)
		dependencySpan.SetStatus(codes.Error, "upstream request failed")
		if status, unanswered := unansweredAIStatus(err); aiCall != nil && unanswered && requestSent.Load() {
			h.settleUnansweredAICall(ctx, integration, appID, aiSettlementFor(status, nil), dependencySpan, metricIntegrationID)
		}
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeProblem(w, http.StatusRequestEntityTooLarge, "outbound_request_too_large", "Outbound request body exceeds the gateway limit", "")
			return
		}
		writeProblem(w, http.StatusBadGateway, "outbound_upstream_unavailable", "Outbound provider could not be reached", "1")
		return
	}
	dependencySpan.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
	if resp.StatusCode >= http.StatusBadRequest {
		dependencySpan.SetStatus(codes.Error, http.StatusText(resp.StatusCode))
	}
	defer func() { _ = resp.Body.Close() }()
	if !responseHeadersWithinBounds(resp.Header, h.MaxResponseHeaderBytes, h.MaxResponseHeaders, resp.Trailer) {
		h.Metrics.ObserveUpstreamError(metricIntegrationID, time.Since(upstreamStarted))
		writeProblem(w, http.StatusBadGateway, "outbound_response_headers_too_large", "Outbound provider response headers exceed the gateway limit", "")
		return
	}
	if r.Method != http.MethodHead && resp.StatusCode != http.StatusNotModified && h.MaxResponseBytes > 0 && resp.ContentLength > h.MaxResponseBytes {
		h.Metrics.ObserveUpstreamError(metricIntegrationID, time.Since(upstreamStarted))
		writeProblem(w, http.StatusBadGateway, "outbound_response_too_large", "Outbound provider response exceeds the gateway limit", "")
		return
	}
	if batch != nil {
		h.finishBatchCreation(ctx, w, resp, batch, metricIntegrationID)
		return
	}
	if aiCall != nil {
		// ADR 20261027132936: a streamed answer ends after a long silence,
		// and the answer may be written until the call's end.
		if streamingMediaType(resp.Header.Get("Content-Type")) {
			resp.Body = boundStreamIdle(resp.Body, aiCall, aiStreamIdleBound)
		}
		if deadline, ok := aiCall.Deadline(); ok {
			_ = http.NewResponseController(w).SetWriteDeadline(deadline)
		}
	}
	var responseCheck *aiResponseCheck
	if responseCheckApplies(integration, r, aiPlan, resp, isReplay, isInternal) {
		responseCheck = &aiResponseCheck{}
	}
	if aiObserver := observeAIResponse(integration, r, resp, cacheHit, aiReq, aiPlan); aiObserver != nil {
		// Deferred so an aborted stream still settles the tokens it used.
		defer h.settleAIUsage(ctx, integration, appID, aiSettlementFor(resp.StatusCode, responseCheck), aiObserver, dependencySpan, metricIntegrationID)
	}
	if responseCheck != nil && !h.applyResponseCheck(ctx, w, integration, aiPlan, resp, responseCheck, metricIntegrationID) {
		return
	}
	if workflowIdentity != nil {
		writeWorkflowOutboundResponse(w, r, resp, managedAuthorization)
		return
	}
	for k, values := range resp.Header {
		if isHopByHop(resp.Header, k) {
			continue
		}
		for _, value := range values {
			w.Header().Add(k, value)
		}
	}
	allowTrailers := r.Method != http.MethodHead && resp.StatusCode >= 200 && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotModified
	if allowTrailers {
		declareProviderTrailers(w, resp)
	}
	streamController := prepareStreamingResponse(ctx, w, resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	var downstream io.Writer = w
	if h.MaxResponseBytes > 0 {
		downstream = &responseBodyCapWriter{ResponseWriter: w, limit: h.MaxResponseBytes}
	}
	if streamController != nil {
		_ = streamController.Flush()
		downstream = &flushingWriter{dst: downstream, flush: streamController.Flush}
	}
	_, copyErr := io.Copy(downstream, resp.Body)
	if copyErr == nil && allowTrailers {
		copyErr = finishProviderTrailers(w, resp, h.MaxResponseHeaderBytes, h.MaxResponseHeaders)
	}
	if copyErr != nil {
		// The provider status is already committed. Returning normally would
		// mark a truncated chunked response as a complete success. Abort the
		// downstream stream, as ReverseProxy does on copy failures, so callers
		// can detect the incomplete response without a second error envelope.
		h.Metrics.ObserveUpstreamError(metricIntegrationID, time.Since(upstreamStarted))
		dependencySpan.RecordError(copyErr)
		dependencySpan.SetStatus(codes.Error, "incomplete provider response")
		panic(http.ErrAbortHandler)
	}
}

// callerAppID returns the calling app and, when a verified workload
// identity names it, the calling instance.
func (h *Handler) callerAppID(w http.ResponseWriter, r *http.Request, integration Integration) (string, string, bool) {
	if token, ok := accessTokenFromContext(r.Context()); ok {
		// ADR-1431: an access token acts as its app, on AI integrations only.
		if !token.allows(integration) {
			writeProblem(w, http.StatusForbidden, "outbound_access_token_not_allowed", "This access token cannot use this outbound integration", "")
			return "", "", false
		}
		return token.AppID, "", true
	}
	if identity, ok := relayIdentityFromContext(r.Context()); ok {
		// ADR-989: the relay connection's platform identity is the caller.
		// Token-authenticated integrations stay unreachable through it.
		if integration.ProviderAuthMode != ProviderAuthManaged {
			writeProblem(w, http.StatusForbidden, "outbound_relay_not_supported", "This outbound integration is not available through the app relay", "")
			return "", "", false
		}
		return identity.AppID, identity.InstanceID, true
	}
	if integration.ProviderAuthMode == ProviderAuthManaged {
		if h.IdentityVerifier == nil {
			writeProblem(w, http.StatusServiceUnavailable, "outbound_identity_unavailable", "Outbound workload identity is unavailable", "1")
			return "", "", false
		}
		identity, err := h.IdentityVerifier.Verify(r.Header.Get(WorkloadIdentityHeader), integration.ID)
		if err != nil {
			writeProblem(w, http.StatusUnauthorized, "outbound_unauthorized", "Outbound workload identity is invalid", "")
			return "", "", false
		}
		return identity.AppID, identity.InstanceID, true
	}
	if !validToken(r.Header.Get(TokenHeader), integration.TokenHash) {
		writeProblem(w, http.StatusUnauthorized, "outbound_unauthorized", "Outbound token is invalid", "")
		return "", "", false
	}
	return r.Header.Get(AppHeader), "", true
}

func (h *Handler) callerExecutionIdentity(w http.ResponseWriter, r *http.Request, integration Integration) (ExecutionIdentity, bool, bool) {
	if identity, granted, ok := relayExecutionFromContext(r.Context()); ok {
		// ADR-990: a Run's relay connection names the integrations its grant
		// allowed; the execution authorizer below still checks the current
		// grant and lease on every request.
		_, allowed := granted[integration.ID]
		if !allowed || integration.OwnerKind != IntegrationOwnerCustomer || integration.ProviderAuthMode != ProviderAuthManaged || integration.CredentialSource != CredentialSourceCustomerSealed {
			writeProblem(w, http.StatusForbidden, "outbound_execution_not_authorized", "This Run is not authorized for the outbound integration", "")
			return ExecutionIdentity{}, true, false
		}
		return identity, true, true
	}
	rawToken := r.Header.Get(ExecutionIdentityHeader)
	if rawToken == "" {
		return ExecutionIdentity{}, false, true
	}
	if integration.OwnerKind != IntegrationOwnerCustomer || integration.ProviderAuthMode != ProviderAuthManaged || integration.CredentialSource != CredentialSourceCustomerSealed {
		writeProblem(w, http.StatusForbidden, "outbound_execution_not_authorized", "Runs access is unavailable for this outbound integration", "")
		return ExecutionIdentity{}, true, false
	}
	if r.Header.Get(WorkloadIdentityHeader) != "" {
		writeProblem(w, http.StatusUnauthorized, "outbound_unauthorized", "Outbound identity is ambiguous", "")
		return ExecutionIdentity{}, true, false
	}
	if h.ExecutionIdentityVerifier == nil {
		writeProblem(w, http.StatusServiceUnavailable, "outbound_identity_unavailable", "Runs workload identity is unavailable", "1")
		return ExecutionIdentity{}, true, false
	}
	identity, err := h.ExecutionIdentityVerifier.VerifyExecution(rawToken, integration.ID)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, "outbound_unauthorized", "Runs workload identity is invalid", "")
		return ExecutionIdentity{}, true, false
	}
	return identity, true, true
}

func responseHeadersWithinBounds(headers http.Header, maxBytes int64, maxCount int, trailers ...http.Header) bool {
	var totalBytes int64
	var count int
	for _, trailer := range trailers {
		for name, values := range trailer {
			// Announced trailer names have nil values until the body reaches
			// EOF. Count those declarations as well as populated fields.
			fields := max(1, len(values))
			count += fields
			totalBytes += int64(len(name)) * int64(fields)
			for _, value := range values {
				totalBytes += int64(len(value))
			}
			if (maxCount > 0 && count > maxCount) || (maxBytes > 0 && totalBytes > maxBytes) {
				return false
			}
		}
	}
	for name, values := range headers {
		for _, value := range values {
			count++
			totalBytes += int64(len(name) + len(value))
			if (maxCount > 0 && count > maxCount) || (maxBytes > 0 && totalBytes > maxBytes) {
				return false
			}
		}
	}
	return true
}

type responseBodyCapWriter struct {
	http.ResponseWriter
	limit   int64
	written int64
}

func (w *responseBodyCapWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.written
	if remaining <= 0 {
		return 0, errResponseBodyTooLarge
	}
	if int64(len(p)) > remaining {
		n, err := w.ResponseWriter.Write(p[:remaining])
		w.written += int64(n)
		if err != nil {
			return n, err
		}
		return n, errResponseBodyTooLarge
	}
	n, err := w.ResponseWriter.Write(p)
	w.written += int64(n)
	return n, err
}

func parsePath(path string) (string, string, bool) {
	if !strings.HasPrefix(path, Prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, Prefix)
	id, suffix, found := strings.Cut(rest, "/")
	if !found {
		suffix = ""
	}
	if id == "" {
		return "", "", false
	}
	if suffix == "" {
		return id, "/", true
	}
	return id, "/" + suffix, true
}

func targetURL(origin *url.URL, path, rawQuery string) (string, error) {
	if origin == nil {
		return "", ErrInvalidIntegration
	}
	u := *origin
	u.RawPath = strings.TrimSuffix(origin.EscapedPath(), "/") + "/" + strings.TrimPrefix(path, "/")
	var err error
	u.Path, err = url.PathUnescape(u.RawPath)
	if err != nil {
		return "", ErrInvalidIntegration
	}
	u.RawQuery = rawQuery
	u.Fragment = ""
	return u.String(), nil
}

func validToken(token string, expected [32]byte) bool {
	got := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(got[:], expected[:]) == 1
}

func forwardedHeaders(in http.Header) http.Header {
	out := make(http.Header, len(in))
	for k, values := range in {
		canonical := http.CanonicalHeaderKey(k)
		if isHopByHop(in, k) || canonical == "Host" || canonical == TokenHeader || canonical == AppHeader || strings.HasPrefix(strings.ToLower(canonical), "x-gregale-") {
			continue
		}
		out[canonical] = append([]string(nil), values...)
	}
	return out
}

func isHopByHop(headers http.Header, name string) bool {
	canonical := http.CanonicalHeaderKey(name)
	if _, ok := hopByHopHeaders[canonical]; ok {
		return true
	}
	for _, connection := range headers.Values("Connection") {
		for _, token := range strings.Split(connection, ",") {
			if http.CanonicalHeaderKey(strings.TrimSpace(token)) == canonical {
				return true
			}
		}
	}
	return false
}

func retryAfterSeconds(d time.Duration) string {
	if d <= 0 {
		return "1"
	}
	seconds := int64((d + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return strconv.FormatInt(seconds, 10)
}

func writeProblem(w http.ResponseWriter, status int, code, title, retryAfter string) {
	p := api.NewProblem(status, code, title, title)
	p.Type = "https://gregale.dev/docs/errors/" + code
	if retryAfter != "" {
		p = p.WithHeader("Retry-After", retryAfter)
	}
	api.WriteProblem(w, p)
}

var _ http.Handler = (*Handler)(nil)

// aiGatewayAdmitsAccount applies the staged rollout and plan entitlement to a
// customer AI integration (ADR-998). Operator integrations are not
// customer-entitled and pass.
// accountSuspended reports an account status whose integrations refuse calls.
func accountSuspended(status string) bool {
	return status == "suspended" || status == "deleted_pending"
}

func (h *Handler) aiGatewayAdmitsAccount(integration Integration) bool {
	if integration.OwnerKind != IntegrationOwnerCustomer {
		return true
	}
	if !AIGatewayEntitled(integration.AccountPlan) {
		return false
	}
	if h.AIGatewayFlags != nil {
		return h.AIGatewayFlags.Enabled(platformflags.AIGateway, platformflags.Subject{
			AccountID: integration.AccountID, Plan: string(integration.AccountPlan),
		})
	}
	return h.AIGatewayRollout.Allows(integration.AccountID)
}
