package gateway

// Service-to-service proxying for the container networking contract.
//
// The proxy deliberately sits above the existing ForwardHTTPStream bridge:
// endpoint discovery chooses a live instance, while the vmmd transport keeps
// the network-namespace and mTLS boundary in one place. This file owns only
// service-name resolution, caller authorization, endpoint leases, and stale
// target retry policy.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/circuit"
	"github.com/onebox-faas/faas/pkg/dependencytrace"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
)

const (
	// ServiceProxyCallerAppHeader is the platform-owned caller identity header
	// used on the trusted node-local hop. The authorizer must validate it before
	// a request is forwarded; the header is stripped before the guest hop.
	ServiceProxyCallerAppHeader = "X-Faas-Caller-App"

	// ServiceDiscoveryDomain is the private DNS suffix exposed to workloads.
	// The node-local resolver answers <slug>.svc.gregale with the tenant
	// bridge address; the HTTP proxy then authorizes the slug before forwarding.
	ServiceDiscoveryDomain = "svc.gregale"

	// ServiceCallerEnvHeader tells the target guest which environment the
	// calling workload belongs to. Only set when it is not production, so a
	// normal call carries nothing new.
	ServiceCallerEnvHeader = "X-Faas-Caller-Env"

	// ServiceCallerPreviewOfHeader names the production app the calling
	// preview was created from. A service receiving it is being exercised by
	// a PR preview, not by production traffic — useful for skipping
	// side effects, tagging writes, or refusing the call outright.
	ServiceCallerPreviewOfHeader = "X-Faas-Caller-Preview-Of"

	serviceProxyDefaultEndpointTTL = 5 * time.Second
	serviceProxyErrorBodyLimit     = 64 * 1024
)

var (
	// ErrServiceProxyUnavailable is returned by injected resolvers when the
	// service registry or node identity source cannot answer a request.
	ErrServiceProxyUnavailable = errors.New("service proxy unavailable")
	// ErrServiceProxyNotFound distinguishes an unknown service name from a
	// transient registry failure so callers receive a stable 404.
	ErrServiceProxyNotFound = errors.New("service proxy service not found")
	// ErrServiceProxyDenied is the stable authorization failure sentinel.
	ErrServiceProxyDenied = errors.New("service proxy access denied")
	// ErrServiceProxyPreviewProductionDenied is the customer-owned policy
	// verdict for a preview caller crossing into a production dependency.
	// Keep it distinct from ErrServiceProxyDenied: cross-account access is an
	// identity failure, while this one has an actionable project setting.
	ErrServiceProxyPreviewProductionDenied = errors.New("preview-to-production service call denied")
	// ErrServiceProxyBindingDenied is returned after the same-account boundary
	// succeeds when a strict caller has not declared the target service.
	ErrServiceProxyBindingDenied = errors.New("service proxy binding denied")
	// ErrServiceProxyPreviewDenied is returned when a production target does
	// not accept calls from preview apps.
	ErrServiceProxyPreviewDenied = errors.New("service proxy preview call denied")
)

// ServiceTarget is the resolved routing identity of a named service. It
// carries the target's wire-protocol posture alongside its app id so the
// proxy can pick the guest bridge (ADR-197) without a second store read on
// the request path.
type ServiceTarget struct {
	AppID string
	// AppProtocol mirrors apps.app_protocol (ADR-124): http1, http2, or grpc.
	// Empty is treated as http1, which preserves the behaviour of every
	// caller written before the protocol became part of this contract.
	AppProtocol string
	// WebSocketEnabled mirrors apps.websocket_enabled. It gates the raw-bytes
	// Upgrade bridge for internal callers exactly as it does at the public
	// edge, so a customer who turned WebSockets off does not silently get
	// them back through the service mesh.
	WebSocketEnabled bool
}

// ServiceProxyResolver maps a service name to its routing identity. The
// context is part of the contract so production implementations can use the
// request deadline for the app/account lookup.
type ServiceProxyResolver func(ctx context.Context, service string) (target ServiceTarget, ok bool, err error)

// ServiceCaller is what the authorizer learned about the calling workload
// while checking the tenant boundary. It is returned rather than discarded so
// the hop does not need a third store read for facts already in hand.
type ServiceCaller struct {
	AppID string
	// PreviewOfSlug is non-empty when the caller is a PR preview app, naming
	// the production app it previews.
	//
	// Service names resolve with no environment scope, and previews are
	// created one app per PR, so a preview has no sibling copy of its
	// dependencies: its internal calls reach the production services. That is
	// the current, documented behaviour — this field exists so the hop can
	// say so instead of doing it silently.
	PreviewOfSlug string
	// AccountID and InstanceID are carried for the ADR-206 assertion claims.
	// The authorizer already loaded the caller row to check the tenant
	// boundary, so these cost nothing extra.
	AccountID  string
	InstanceID string
}

// ServiceProxyAuthorizer enforces the tenant boundary and any caller-side
// declared-binding policy. A nil authorizer is a wiring error and fails closed.
type ServiceProxyAuthorizer func(ctx context.Context, callerAppID, targetAppID string) (ServiceCaller, error)

// ServiceProxyCallerResolver binds the caller header to the network identity
// observed by the node-local listener. When it is configured, the resolved
// identity is authoritative and the caller header becomes an optional
// compatibility assertion. This lets guest requests omit a spoofable
// platform header while preserving the header contract for trusted callers.
type ServiceProxyCallerResolver func(ctx context.Context, remoteAddr string) (appID string, err error)

// ServiceCallerMintInput is what the proxy knows about a call it has already
// authorized, handed to the minter so pkg/gateway does not import the token
// library or care about its key material.
type ServiceCallerMintInput struct {
	CallerAppID      string
	TargetAppID      string
	AccountID        string
	CallerInstanceID string
	CallerEnv        string
}

// ServiceCallerMinter produces the ADR-206 assertion attesting the caller the
// proxy verified. nil attaches nothing, which is the default: the assertion
// has no consumer yet, so an operator without a verifier should not pay for a
// signature on every call.
type ServiceCallerMinter func(ServiceCallerMintInput) (string, error)

// ServiceCallerAssertionHeader carries the ADR-206 assertion to the target. It
// is deliberately not Authorization: that header belongs to the customer's own
// scheme, and overwriting it would break an app that authenticates its callers
// itself.
const ServiceCallerAssertionHeader = "X-Faas-Caller-Assertion"

// servicecallerEnvPreview mirrors servicecaller.EnvPreview without importing
// the token package into the request path.
const servicecallerEnvPreview = "preview"

// ServiceProxyWaker holds the caller while the scheduler brings a parked
// target service back (ADR-196). It returns nil once the wake attempt has
// finished — successfully or at capacity — and the proxy then re-reads the
// endpoint registry to decide whether a replica is actually routable.
//
// A non-nil error is a real admission failure (no headroom, scheduler
// unreachable, store error) and is surfaced as 503. nil disables
// wake-on-demand entirely, restoring the pre-ADR-196 fail-fast behaviour for
// wiring that has no scheduler seam (tests, single-box dev without schedd).
type ServiceProxyWaker func(ctx context.Context, appID string) error

// ServiceProxyConfig wires the narrow seams around ServiceProxy. Forward is
// normally gateway.ForwardingReverseProxyWithEvents(...); tests inject a
// small handler factory so selection and retry behavior can be exercised
// without a live gRPC server.
type ServiceProxyConfig struct {
	Provider      ServiceEndpointProvider
	Resolve       ServiceProxyResolver
	Authorize     ServiceProxyAuthorizer
	ResolveCaller ServiceProxyCallerResolver
	Forward       func(Target) http.Handler
	// RawForward is the optional verbatim-bytes bridge used for Upgrade
	// traffic (ADR-197). nil rejects internal upgrade requests with 501
	// rather than letting the ordinary forwarder strip the handshake.
	RawForward func(Target) http.Handler
	// Wake is the optional wake-on-demand seam (ADR-196). nil keeps the
	// legacy fail-fast behaviour for a parked target.
	Wake ServiceProxyWaker
	// Metrics observes internal call outcomes, cold-path wake latency, and
	// ADR-201 §2 breaker transitions. nil is allowed and every observation
	// is a no-op — the breaker keeps working and simply publishes nothing.
	Metrics     *Metrics
	EndpointTTL time.Duration
	Now         func() time.Time
	Log         *slog.Logger
	// MintCallerAssertion attaches a verifiable statement of who called
	// (ADR-206). nil attaches nothing.
	MintCallerAssertion ServiceCallerMinter
	// LocalNodeID is this gateway's compute node. When set, endpoint
	// selection prefers a replica on this node before crossing the network
	// (ADR-168 refinement). Empty preserves flat round-robin.
	LocalNodeID string
	// Breaker is the endpoint health breaker (ADR-201 §2). Nil installs
	// circuit.LegacyQuarantineConfig, which reproduces the fixed-TTL
	// quarantine this field replaced: one failure benches an endpoint for
	// EndpointTTL with no backoff growth. cmd/gatewayd-internal passes a
	// DefaultConfig group when FAAS_GATEWAY_CIRCUIT_BREAKER is on.
	Breaker *circuit.Group
	// RetryPolicy applies the same attempt, idempotency, request-deadline,
	// and aggregate-budget contract as public edge retries. The zero value
	// preserves the historical service-proxy default of one replay.
	RetryPolicy RetryPolicy
	// RetryBudget may be shared with the public handler so all platform-
	// generated retries for an app draw from the same aggregate allowance.
	RetryBudget *RetryBudget
}

// ServiceProxy is an HTTP service-name router backed by the gateway's live
// endpoint registry. It caches registry snapshots for a short lease, avoids
// endpoints that recently failed transport, and retries one alternate target
// for safe idempotent requests.
type ServiceProxy struct {
	mintAssertion ServiceCallerMinter
	localNodeID   string
	provider      ServiceEndpointProvider
	resolve       ServiceProxyResolver
	authorize     ServiceProxyAuthorizer
	resolveCaller ServiceProxyCallerResolver
	forward       func(Target) http.Handler
	rawForward    func(Target) http.Handler
	wake          ServiceProxyWaker
	metrics       *Metrics
	endpointTTL   time.Duration
	now           func() time.Time
	log           *slog.Logger

	breaker     *circuit.Group
	retryPolicy RetryPolicy
	retryBudget *RetryBudget

	mu        sync.Mutex
	snapshots map[string]serviceProxySnapshot
	next      map[string]uint64
}

type serviceProxySnapshot struct {
	fetchedAt time.Time
	endpoints []ServiceEndpoint
}

// NewServiceProxy constructs a fail-closed service proxy.
func NewServiceProxy(cfg ServiceProxyConfig) *ServiceProxy {
	ttl := cfg.EndpointTTL
	if ttl <= 0 {
		ttl = serviceProxyDefaultEndpointTTL
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	breaker := cfg.Breaker
	if breaker == nil {
		// Flag-off equivalence: the legacy config is a single-failure,
		// flat-TTL bench, which is byte-for-byte what the quarantine map
		// did. Pinned by TestLegacyConfigMatchesQuarantine.
		legacy := circuit.LegacyQuarantineConfig()
		legacy.Window = ttl
		legacy.OpenDuration = ttl
		legacy.MaxOpenDuration = ttl
		breaker = circuit.NewGroup(legacy, now)
	}
	// Transitions are observed here rather than at each call site so the
	// metric cannot drift from the state machine: every state change goes
	// through the group, including the ones settled lazily inside Allow and
	// State.
	if cfg.Metrics != nil {
		breaker = breaker.WithTransitionObserver(func(key string, from, to circuit.State) {
			cfg.Metrics.IncCircuitTransition(string(from), string(to))
		})
	}
	retryPolicy := cfg.RetryPolicy
	if retryPolicy.MaxAttempts == 0 {
		retryPolicy.MaxAttempts = 2
		retryPolicy.MinRemaining = time.Duration(api.EdgeRuleRetryDefaultMinRemainingMs) * time.Millisecond
	}
	if retryPolicy.MaxAttempts < 1 {
		retryPolicy.MaxAttempts = 1
	}
	if retryPolicy.MaxAttempts > api.EdgeRuleRetryMaxAttempts {
		retryPolicy.MaxAttempts = api.EdgeRuleRetryMaxAttempts
	}
	retryPolicy.Enabled = retryPolicy.MaxAttempts >= 2
	if retryPolicy.BudgetPercent <= 0 {
		retryPolicy.BudgetPercent = api.EdgeRuleRetryDefaultBudgetPercent
	} else if retryPolicy.BudgetPercent > api.MaxEdgeRuleRetryBudgetPercent {
		retryPolicy.BudgetPercent = api.MaxEdgeRuleRetryBudgetPercent
	}
	if retryPolicy.BudgetMinRetries <= 0 {
		retryPolicy.BudgetMinRetries = api.EdgeRuleRetryDefaultBudgetMin
	} else if retryPolicy.BudgetMinRetries > api.MaxEdgeRuleRetryBudgetMin {
		retryPolicy.BudgetMinRetries = api.MaxEdgeRuleRetryBudgetMin
	}
	retryBudget := cfg.RetryBudget
	if retryBudget == nil {
		retryBudget = NewRetryBudget(0, now)
	}
	return &ServiceProxy{
		mintAssertion: cfg.MintCallerAssertion,
		localNodeID:   strings.TrimSpace(cfg.LocalNodeID),
		provider:      cfg.Provider,
		resolve:       cfg.Resolve,
		authorize:     cfg.Authorize,
		resolveCaller: cfg.ResolveCaller,
		forward:       cfg.Forward,
		rawForward:    cfg.RawForward,
		wake:          cfg.Wake,
		metrics:       cfg.Metrics,
		endpointTTL:   ttl,
		now:           now,
		log:           log,
		breaker:       breaker,
		retryPolicy:   retryPolicy,
		retryBudget:   retryBudget,
		snapshots:     make(map[string]serviceProxySnapshot),
		next:          make(map[string]uint64),
	}
}

// ServeHTTP accepts /v1/internal/services/{service}[/{path...}] and the
// guest-facing <service>.svc.gregale Host form. The service segment is
// resolved to an app; the remaining path is forwarded unchanged.
func (p *ServiceProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	service, targetPath, ok := parseServiceProxyRequest(r)
	if !ok {
		serviceProxyProblem(w, http.StatusNotFound, "service request must use /v1/internal/services/<name>[/<path>] or <name>.svc.gregale")
		return
	}
	// The guest-facing service-proxy listener is a standalone http.Server, not
	// wrapped by otelhttp. Extract W3C context here so a caller that forwards
	// its inbound traceparent joins the original request instead of always
	// starting a new service-call trace.
	parentCtx := propagation.TraceContext{}.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	dependencyCtx, dependencySpan := dependencytrace.StartClientSpan(parentCtx, serviceProxySpanName(service),
		attribute.String("gregale.dependency.type", "managed_binding"),
		attribute.String("gregale.dependency.kind", "service_proxy"),
		attribute.String("http.request.method", r.Method),
	)
	if validServiceDNSLabel(service) {
		dependencySpan.SetAttributes(attribute.String("gregale.service.name", service))
	}
	upgrade := isUpgradeRequest(r)
	traceWriter := &serviceProxyTraceResponseWriter{ResponseWriter: w}
	dispatchWriter := http.ResponseWriter(traceWriter)
	if upgrade {
		// A hijacked response needs the original writer. The ordinary trace
		// writer intentionally records HTTP response status only and does not
		// impersonate net.Hijacker.
		dispatchWriter = w
	}
	defer func() {
		if !upgrade {
			status := traceWriter.status
			if status == 0 {
				// net/http implicitly commits 200 when a handler returns without
				// writing a response.
				status = http.StatusOK
			}
			dependencySpan.SetAttributes(attribute.Int("http.response.status_code", status))
			if status >= http.StatusBadRequest {
				dependencySpan.SetStatus(codes.Error, strconv.Itoa(status))
			}
		}
		dependencySpan.End()
	}()
	r = r.WithContext(dependencyCtx)
	caller := strings.TrimSpace(r.Header.Get(ServiceProxyCallerAppHeader))
	if p.resolveCaller != nil {
		resolved, err := p.resolveCaller(dependencyCtx, r.RemoteAddr)
		if err != nil {
			p.metrics.IncServiceCall(ServiceCallUnauthenticated)
			serviceProxyProblem(dispatchWriter, http.StatusServiceUnavailable, "caller identity is unavailable")
			return
		}
		if resolved == "" {
			p.metrics.IncServiceCall(ServiceCallUnauthenticated)
			serviceProxyProblem(dispatchWriter, http.StatusForbidden, "caller identity is unknown")
			return
		}
		if caller != "" && resolved != caller {
			p.metrics.IncServiceCall(ServiceCallDenied)
			serviceProxyProblem(dispatchWriter, http.StatusForbidden, "caller identity does not match the node identity")
			return
		}
		caller = resolved
	}
	if caller == "" {
		p.metrics.IncServiceCall(ServiceCallUnauthenticated)
		serviceProxyProblem(dispatchWriter, http.StatusUnauthorized, "caller identity is required")
		return
	}
	target, err := p.resolveTarget(dependencyCtx, service)
	if err != nil {
		if errors.Is(err, ErrServiceProxyNotFound) {
			p.metrics.IncServiceCall(ServiceCallNotFound)
			serviceProxyProblem(dispatchWriter, http.StatusNotFound, "service is not registered")
			return
		}
		serviceProxyProblem(dispatchWriter, http.StatusServiceUnavailable, err.Error())
		return
	}
	if p.authorize == nil {
		serviceProxyProblem(dispatchWriter, http.StatusServiceUnavailable, "service proxy authorizer is not wired")
		return
	}
	dependencySpan.SetAttributes(attribute.String("gregale.service.target_app_id", target.AppID))
	callerInfo, err := p.authorize(dependencyCtx, caller, target.AppID)
	if err != nil {
		if errors.Is(err, ErrServiceProxyPreviewProductionDenied) {
			p.metrics.IncServiceCall(ServiceCallPreviewDenied)
			api.WriteProblem(dispatchWriter, api.NewProblem(
				http.StatusForbidden,
				api.CodePreviewProductionDependencyDenied,
				"Preview dependency denied",
				"this project blocks preview applications from calling production services; use an isolated preview dependency or explicitly set preview_service_policy to allow_marked",
			))
			return
		}
		if errors.Is(err, ErrServiceProxyBindingDenied) {
			p.metrics.IncServiceCall(ServiceCallBindingDenied)
			serviceProxyProblem(dispatchWriter, http.StatusForbidden, "caller has not declared this service binding")
			return
		}
		if errors.Is(err, ErrServiceProxyPreviewDenied) {
			p.metrics.IncServiceCall(ServiceCallPreviewDenied)
			serviceProxyProblem(dispatchWriter, http.StatusForbidden, "production service does not accept calls from preview apps")
			return
		}
		if errors.Is(err, ErrServiceProxyDenied) {
			p.metrics.IncServiceCall(ServiceCallDenied)
			serviceProxyProblem(dispatchWriter, http.StatusForbidden, "caller is not allowed to reach this service")
			return
		}
		serviceProxyProblem(dispatchWriter, http.StatusServiceUnavailable, "service authorization is unavailable")
		return
	}
	// Only the authorizer can establish the tenant identity. Stamp it after a
	// successful authorization so the in-process retained-span exporter can
	// route this platform-owned span to apid without a customer API key.
	if callerInfo.AccountID != "" {
		dependencySpan.SetAttributes(attribute.String(retainedSpanAccountIDAttribute, callerInfo.AccountID))
	}
	if p.provider == nil || p.forward == nil {
		serviceProxyProblem(dispatchWriter, http.StatusServiceUnavailable, "service proxy transport is not wired")
		return
	}
	endpoints, woken, served := p.routableEndpoints(dispatchWriter, r, target.AppID)
	if !served {
		return
	}
	p.dispatch(dispatchWriter, r, targetPath, target, callerInfo, endpoints, woken) //nolint:contextcheck // the inbound request carries the canonical ctx; forwardOnce wraps it with request-local stale-target state rather than taking a separate ctx parameter.
}

func serviceProxySpanName(service string) string {
	if validServiceDNSLabel(service) {
		return "service." + service
	}
	return "service.proxy"
}

// serviceProxyTraceResponseWriter records the final HTTP status while
// preserving streaming through http.Flusher and response-controller
// unwrapping. Upgrade requests bypass it because they need net.Hijacker.
type serviceProxyTraceResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *serviceProxyTraceResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *serviceProxyTraceResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *serviceProxyTraceResponseWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *serviceProxyTraceResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// dispatch picks the guest bridge for the resolved target (ADR-197).
//
// An Upgrade request needs the verbatim-bytes path: the ordinary forwarder
// strips Connection/Upgrade as hop-by-hop headers (RFC 7230 §6.1), which
// turns a WebSocket handshake into a confusing upstream error. It also
// cannot be buffered or retried, because the response is a hijacked
// connection rather than a body.
func (p *ServiceProxy) dispatch(w http.ResponseWriter, r *http.Request, targetPath string, target ServiceTarget, caller ServiceCaller, endpoints []ServiceEndpoint, woken bool) {
	if isUpgradeRequest(r) {
		if !target.WebSocketEnabled {
			p.metrics.IncServiceCall(ServiceCallUpgradeRejected)
			serviceProxyProblem(w, http.StatusNotImplemented, "target service does not accept upgrade requests")
			return
		}
		if p.rawForward == nil {
			p.metrics.IncServiceCall(ServiceCallUpgradeRejected)
			serviceProxyProblem(w, http.StatusNotImplemented, "raw-bytes bridge is not enabled on this node")
			return
		}
		p.countForward(woken)
		p.forwardUpgrade(w, r, targetPath, target, caller, endpoints)
		return
	}
	p.countForward(woken)
	p.forwardOnce(w, r, targetPath, target, caller, endpoints)
}

// countForward records a call that reached the guest bridge. The warm/cold
// split is the internal cold-start rate, which is the signal ADR-196 defers
// the depends_on wake-ahead decision on.
func (p *ServiceProxy) countForward(woken bool) {
	if woken {
		p.metrics.IncServiceCall(ServiceCallWoken)
		return
	}
	p.metrics.IncServiceCall(ServiceCallForwarded)
}

// parseServiceProxyRequest accepts the original explicit path form and the
// guest-facing DNS/Host form. The latter lets a workload use a normal URL,
// for example http://orders.svc.gregale:10080/health, without exposing node
// addresses or requiring a platform-owned caller header.
func parseServiceProxyRequest(r *http.Request) (service, targetPath string, ok bool) {
	if service, targetPath, ok = parseServiceProxyPath(r.URL.Path); ok {
		return service, targetPath, true
	}
	service, ok = parseServiceProxyHost(r.Host)
	if !ok {
		return "", "", false
	}
	targetPath = r.URL.Path
	if targetPath == "" {
		targetPath = "/"
	}
	return service, targetPath, true
}

func parseServiceProxyHost(host string) (string, bool) {
	host = strings.TrimSpace(host)
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	} else if strings.HasPrefix(host, "[") || strings.Count(host, ":") > 1 {
		return "", false
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	suffix := "." + ServiceDiscoveryDomain
	if !strings.HasSuffix(host, suffix) {
		return "", false
	}
	service := strings.TrimSuffix(host, suffix)
	if !validServiceDNSLabel(service) {
		return "", false
	}
	return service, true
}

func validServiceDNSLabel(service string) bool {
	if service == "" || len(service) > 63 || strings.HasPrefix(service, "-") || strings.HasSuffix(service, "-") {
		return false
	}
	for _, r := range service {
		if r < 'a' || r > 'z' {
			if r < '0' || r > '9' {
				if r != '-' {
					return false
				}
			}
		}
	}
	return true
}

func (p *ServiceProxy) resolveTarget(ctx context.Context, service string) (ServiceTarget, error) {
	if p.resolve == nil {
		return ServiceTarget{}, fmt.Errorf("service name resolver is not wired")
	}
	target, ok, err := p.resolve(ctx, service)
	if err != nil {
		return ServiceTarget{}, fmt.Errorf("service name lookup: %w", err)
	}
	if !ok || target.AppID == "" {
		return ServiceTarget{}, fmt.Errorf("%w: %s", ErrServiceProxyNotFound, service)
	}
	return target, nil
}

// serviceGuestProtocol maps the target's app_protocol onto the value vmmd
// reads to choose the guest bridge. It mirrors decideProtocol on the public
// path: http1 and http2/grpc select the H1 and H2C bridges respectively, and
// any value outside the column's closed set degrades to http1 rather than
// failing the call.
func serviceGuestProtocol(target ServiceTarget) string {
	switch target.AppProtocol {
	case api.AppProtocolHTTP2:
		return api.AppProtocolHTTP2
	case api.AppProtocolGRPC:
		return api.AppProtocolGRPC
	default:
		return api.AppProtocolHTTP1
	}
}

func (p *ServiceProxy) endpoints(ctx context.Context, appID string) ([]ServiceEndpoint, error) {
	now := p.now()
	p.mu.Lock()
	if cached, ok := p.snapshots[appID]; ok && now.Before(cached.fetchedAt.Add(p.endpointTTL)) {
		out := append([]ServiceEndpoint(nil), cached.endpoints...)
		p.mu.Unlock()
		return out, nil
	}
	p.mu.Unlock()
	snapshot, err := p.provider.ServiceEndpoints(ctx, appID)
	if err != nil {
		return nil, err
	}
	endpoints := validServiceEndpoints(snapshot.Endpoints)
	p.mu.Lock()
	p.snapshots[appID] = serviceProxySnapshot{fetchedAt: now, endpoints: append([]ServiceEndpoint(nil), endpoints...)}
	p.mu.Unlock()
	return endpoints, nil
}

// routableEndpoints resolves the endpoints the request can be forwarded to,
// waking a parked target on the way (ADR-196). It writes the error response
// itself and reports served=false when nothing is routable, so ServeHTTP
// stays within the handler-length convention.
func (p *ServiceProxy) routableEndpoints(w http.ResponseWriter, r *http.Request, appID string) (_ []ServiceEndpoint, woken, served bool) {
	endpoints, err := p.endpoints(r.Context(), appID)
	if err != nil {
		p.metrics.IncServiceCall(ServiceCallRegistryUnavailable)
		serviceProxyProblem(w, http.StatusServiceUnavailable, "service endpoint registry is unavailable")
		return nil, false, false
	}
	if len(endpoints) > 0 {
		return endpoints, false, true
	}
	endpoints, err = p.wakeAndRefresh(r.Context(), appID)
	if err != nil {
		p.writeWakeFailure(w, appID, err)
		return nil, false, false
	}
	if len(endpoints) == 0 {
		p.metrics.IncServiceCall(ServiceCallNoReplica)
		serviceProxyProblem(w, http.StatusServiceUnavailable, "service has no healthy replicas")
		return nil, false, false
	}
	return endpoints, true, true
}

// writeWakeFailure maps a wake error onto the caller-facing response.
func (p *ServiceProxy) writeWakeFailure(w http.ResponseWriter, appID string, err error) {
	var full *WakeQueueFullError
	if errors.As(err, &full) {
		p.metrics.IncServiceCall(ServiceCallWakeQueueFull)
		// Mirror the public edge: a saturated wake queue is a bounded,
		// retryable condition, not a failure of the service. Hand the caller
		// the same Retry-After the edge would so a peer workload can back off
		// instead of hot-looping on a restoring dependency.
		w.Header().Set("Retry-After", retryAfterSeconds(full.RetryAfter))
		serviceProxyProblem(w, http.StatusServiceUnavailable, "service is waking and its wake queue is full")
		return
	}
	p.metrics.IncServiceCall(ServiceCallWakeFailed)
	p.log.Warn("gateway: service proxy wake failed", "app", appID, "err", err)
	serviceProxyProblem(w, http.StatusServiceUnavailable, "service could not be woken")
}

// wakeAndRefresh holds the caller while a parked target service is restored
// (ADR-196), then re-reads the endpoint registry.
//
// The cached registry snapshot is invalidated before the re-read. The
// ordinary 5 s endpoint lease exists to keep the hot path off Postgres, but
// the wake has just changed the exact state that lease caches: serving the
// stale empty snapshot back would make every cold internal call a guaranteed
// 503 no matter how fast the restore was.
//
// A nil waker returns no endpoints and no error, so the caller falls through
// to the pre-ADR-196 "no healthy replicas" response.
func (p *ServiceProxy) wakeAndRefresh(ctx context.Context, appID string) ([]ServiceEndpoint, error) {
	if p.wake == nil {
		return nil, nil
	}
	start := p.now()
	if err := p.wake(ctx, appID); err != nil {
		return nil, err
	}
	p.metrics.ObserveServiceWakeLatency(p.now().Sub(start))
	p.invalidateEndpoints(appID)
	return p.endpoints(ctx, appID)
}

// invalidateEndpoints drops the cached registry lease for appID so the next
// read goes back to the authoritative provider.
func (p *ServiceProxy) invalidateEndpoints(appID string) {
	p.mu.Lock()
	delete(p.snapshots, appID)
	p.mu.Unlock()
}

// retryAfterSeconds renders a wake budget as an integer-second Retry-After
// value, floored at 1 so a sub-second budget never emits "0" (which clients
// read as "retry immediately" and turn into a hot loop).
func retryAfterSeconds(d time.Duration) string {
	seconds := int(d / time.Second)
	if d%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}

func validServiceEndpoints(in []ServiceEndpoint) []ServiceEndpoint {
	out := make([]ServiceEndpoint, 0, len(in))
	for _, endpoint := range in {
		if endpoint.InstanceID == "" || endpoint.NodeID == "" || endpoint.Port < 1 || endpoint.Port > 65535 {
			continue
		}
		out = append(out, endpoint)
	}
	return out
}

// guestRequest builds the outbound request for the guest hop: the caller
// header is stripped, inbound identity claims are cleared before the target
// identity this hop actually knows is applied, and the target's wire protocol
// is stamped so vmmd selects the H1 or H2C guest bridge (ADR-197).
//
// The request id is preserved so an internal hop stays correlated end to end.
func (p *ServiceProxy) guestRequest(r *http.Request, targetPath string, target ServiceTarget, caller ServiceCaller) *http.Request {
	request := r.Clone(r.Context())
	request.URL.Path = targetPath
	request.URL.RawPath = ""
	request.RequestURI = ""
	request.Header = r.Header.Clone()
	request.Header.Del(ServiceProxyCallerAppHeader)
	// Both caller-environment headers are platform-owned. Strip whatever the
	// guest sent before deciding: otherwise any workload could label its own
	// traffic, and a target that trusts the marker would be trusting the
	// caller's word for it.
	request.Header.Del(ServiceCallerEnvHeader)
	request.Header.Del(ServiceCallerPreviewOfHeader)
	request.Header.Del(ServiceCallerAssertionHeader)
	requestID := request.Header.Get(api.RequestIDHeader)
	api.PlatformIdentity{RequestID: requestID, AppID: target.AppID}.ApplyGuestHeaders(request.Header)
	request.Header.Set("x-faas-protocol", serviceGuestProtocol(target))
	// A preview app has no sibling copy of its dependencies, so this call is
	// crossing from a preview into a production service. Say so on the hop and
	// count it, rather than letting a PR quietly exercise production.
	callerEnv := ""
	if caller.PreviewOfSlug != "" {
		callerEnv = servicecallerEnvPreview
		request.Header.Set(ServiceCallerEnvHeader, callerEnv)
		request.Header.Set(ServiceCallerPreviewOfHeader, caller.PreviewOfSlug)
		p.metrics.IncServicePreviewToProduction()
	}
	p.attachCallerAssertion(request, target, caller, callerEnv)
	return request
}

// attachCallerAssertion adds the ADR-206 statement of who called. A mint
// failure is logged and dropped rather than failing the call: nothing verifies
// the assertion yet, so refusing traffic over a signing problem would trade a
// working mesh for a feature with no consumer.
func (p *ServiceProxy) attachCallerAssertion(request *http.Request, target ServiceTarget, caller ServiceCaller, callerEnv string) {
	if p.mintAssertion == nil || caller.AppID == "" {
		return
	}
	token, err := p.mintAssertion(ServiceCallerMintInput{
		CallerAppID:      caller.AppID,
		TargetAppID:      target.AppID,
		AccountID:        caller.AccountID,
		CallerInstanceID: caller.InstanceID,
		CallerEnv:        callerEnv,
	})
	if err != nil {
		p.log.Warn("gateway: service caller assertion mint failed; forwarding unsigned",
			"caller", caller.AppID, "target", target.AppID, "err", err)
		return
	}
	request.Header.Set(ServiceCallerAssertionHeader, token)
}

// forwardUpgrade carries an Upgrade request to the guest over the raw-bytes
// bridge. There is no retry and no response buffering: the response is a
// hijacked connection, so the first endpoint chosen is the only one, and a
// stale-target signal cannot be acted on after bytes have flowed.
func (p *ServiceProxy) forwardUpgrade(w http.ResponseWriter, r *http.Request, targetPath string, target ServiceTarget, caller ServiceCaller, endpoints []ServiceEndpoint) {
	endpoint, ok := p.pick(target.AppID, endpoints)
	if !ok {
		serviceProxyProblem(w, http.StatusServiceUnavailable, "service has no healthy replicas")
		return
	}
	request := p.guestRequest(r, targetPath, target, caller)
	applyServiceEndpointIdentity(request, target, endpoint, caller)
	// Mirrors the public edge (ADR-080): the wake-timeline vocabulary marks a
	// raw-bytes session so observability does not have to re-derive it from
	// the Connection/Upgrade pair.
	request.Header.Set("x-faas-upgrade", "true")
	p.rawForward(serviceEndpointTarget(target.AppID, endpoint)).ServeHTTP(w, request)
}

func (p *ServiceProxy) forwardOnce(w http.ResponseWriter, r *http.Request, targetPath string, target ServiceTarget, caller ServiceCaller, endpoints []ServiceEndpoint) {
	appID := target.AppID
	request := p.guestRequest(r, targetPath, target, caller)
	policy := p.retryPolicy
	retryable, skipReason := policy.retryable(request)
	maxAttempts := 1
	if policy.Enabled && retryable {
		maxAttempts = policy.MaxAttempts
	}
	if policy.Enabled && !retryable {
		p.metrics.IncRetryExhausted(skipReason)
	}
	hasBody := request.Body != nil && request.Body != http.NoBody
	if maxAttempts > 1 && hasBody && request.GetBody == nil {
		maxAttempts = 1
		p.metrics.IncRetryExhausted(RetrySkipBodyNotReplay)
	}
	if maxAttempts > 1 {
		p.retryBudget.ObserveOriginal(appID)
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		endpoint, ok := p.pick(appID, endpoints)
		if !ok {
			if attempt > 0 {
				p.metrics.IncRetryExhausted(RetrySkipNoTarget)
			}
			serviceProxyProblem(w, http.StatusServiceUnavailable, "service has no healthy replicas")
			return
		}
		forwardReq := request
		if attempt > 0 {
			var err error
			forwardReq, err = replayRequest(request)
			if err != nil {
				p.metrics.IncRetryExhausted(RetrySkipBodyNotReplay)
				return
			}
		}
		applyServiceEndpointIdentity(forwardReq, target, endpoint, caller)
		signal := &staleTargetSignal{onStale: func() { p.quarantine(appID, endpoint.InstanceID) }}
		buffer := newServiceProxyResponseWriter(w)
		// withStaleTargetSignal intentionally inherits the inbound request
		// context so the bridge can report a stale target to this retry loop.
		forwardReq = forwardReq.WithContext(withStaleTargetSignal(forwardReq.Context(), signal)) //nolint:contextcheck // request context is deliberately wrapped with request-local stale-target state.
		p.forward(serviceEndpointTarget(appID, endpoint)).ServeHTTP(buffer, forwardReq)
		if attempt > 0 && forwardReq.Body != nil {
			_ = forwardReq.Body.Close()
		}
		if maxAttempts > 1 {
			p.metrics.IncRetryAttempt(attemptOutcome(attempt, signal.stale.Load()))
		}
		if !signal.stale.Load() {
			// Report the healthy transport. Without this the breaker only
			// ever observes failures, the rolling ratio is a constant 1.0,
			// and one blip opens the circuit no matter how much good traffic
			// surrounds it. It also closes a half-open probe.
			p.healthy(appID, endpoint.InstanceID)
		}
		if !signal.stale.Load() {
			buffer.commit()
			buffer.commitTrailers()
			return
		}
		if buffer.committed {
			p.metrics.IncRetryExhausted(RetrySkipCommitted)
			buffer.commitTrailers()
			return
		}
		if attempt+1 >= maxAttempts {
			p.metrics.IncRetryExhausted(RetrySkipAttempts)
			buffer.commit()
			buffer.commitTrailers()
			return
		}
		if budget, ok := reqbudget.FromContext(request.Context()); ok && budget.Remaining(p.now()) < policy.MinRemaining {
			p.metrics.IncRetryExhausted(RetrySkipBudget)
			buffer.commit()
			buffer.commitTrailers()
			return
		}
		if !p.retryBudget.AllowRetry(appID, policy.BudgetPercent, policy.BudgetMinRetries) {
			p.metrics.IncRetryExhausted(RetrySkipAggregate)
			buffer.commit()
			buffer.commitTrailers()
			return
		}
		if policy.Backoff > 0 {
			timer := time.NewTimer(policy.Backoff)
			select {
			case <-timer.C:
			case <-request.Context().Done():
				timer.Stop()
				buffer.commit()
				buffer.commitTrailers()
				return
			}
			timer.Stop()
		}
	}
}

// applyServiceEndpointIdentity replaces guest-controlled identity claims with
// the authoritative target replica and the account already checked by the
// service authorizer. The same helper is used by HTTP and Upgrade paths so a
// raw-bytes bridge cannot silently lose deployment provenance.
func applyServiceEndpointIdentity(request *http.Request, target ServiceTarget, endpoint ServiceEndpoint, caller ServiceCaller) {
	if request == nil {
		return
	}
	identity := api.PlatformIdentity{
		RequestID:           request.Header.Get(api.RequestIDHeader),
		AppID:               target.AppID,
		DeploymentID:        endpoint.DeploymentID,
		TenantID:            caller.AccountID,
		InstanceID:          endpoint.InstanceID,
		NodeID:              endpoint.NodeID,
		Region:              endpoint.Region,
		CommitSHA:           endpoint.CommitSHA,
		DeploymentTag:       endpoint.DeploymentTag,
		DeploymentCreatedAt: endpoint.DeploymentCreatedAt,
		ImageDigest:         endpoint.ImageDigest,
	}
	identity.ApplyGuestHeaders(request.Header)
}

func serviceEndpointTarget(appID string, endpoint ServiceEndpoint) Target {
	return Target{
		AppID:               appID,
		NodeID:              endpoint.NodeID,
		InstanceID:          endpoint.InstanceID,
		DeploymentID:        endpoint.DeploymentID,
		Region:              endpoint.Region,
		CommitSHA:           endpoint.CommitSHA,
		DeploymentTag:       endpoint.DeploymentTag,
		DeploymentCreatedAt: endpoint.DeploymentCreatedAt,
		ImageDigest:         endpoint.ImageDigest,
		Port:                endpoint.Port,
	}
}

// pick walks the round-robin ring and returns the first endpoint whose
// breaker admits it. In half-open exactly one caller is admitted as a probe,
// so a recovering endpoint receives a single trial request rather than the
// full share the ring would otherwise hand it.
func (p *ServiceProxy) pick(appID string, endpoints []ServiceEndpoint) (ServiceEndpoint, bool) {
	p.mu.Lock()
	start := p.next[appID]
	p.next[appID]++
	p.mu.Unlock()
	// Prefer a replica on this node before crossing the network. The caller
	// is a workload on this box, so a local target keeps the whole exchange
	// inside one host: no inter-node hop, and no dependency on a peer node
	// staying reachable for a call between two same-account services that
	// both happen to live here.
	//
	// Round-robin still runs within each tier, so local replicas share load
	// evenly and a benched local endpoint falls through to a remote one
	// rather than failing the call.
	if endpoint, ok := p.pickFrom(appID, endpoints, start, true); ok {
		return endpoint, true
	}
	return p.pickFrom(appID, endpoints, start, false)
}

// pickFrom walks the rotation once, considering only endpoints that match the
// requested locality. localOnly=false considers every endpoint, so the second
// pass is a superset of the first and no target is unreachable.
func (p *ServiceProxy) pickFrom(appID string, endpoints []ServiceEndpoint, start uint64, localOnly bool) (ServiceEndpoint, bool) {
	if localOnly && p.localNodeID == "" {
		return ServiceEndpoint{}, false
	}
	for i := 0; i < len(endpoints); i++ {
		endpoint := endpoints[(int(start)+i)%len(endpoints)]
		if localOnly && endpoint.NodeID != p.localNodeID {
			continue
		}
		if p.breaker.Allow(serviceProxyEndpointKey(appID, endpoint.InstanceID)) {
			return endpoint, true
		}
	}
	return ServiceEndpoint{}, false
}

// quarantine reports a transport failure for an endpoint. The name is kept
// because every call site reads as "bench this endpoint"; the mechanism
// underneath is now the shared breaker, so repeated failures back off
// geometrically instead of re-admitting the endpoint every endpointTTL.
func (p *ServiceProxy) quarantine(appID, instanceID string) {
	p.breaker.Failure(serviceProxyEndpointKey(appID, instanceID))
	p.log.Warn("gateway: service proxy quarantined stale endpoint", "app", appID, "instance", instanceID)
}

// healthy reports a successful transport for an endpoint, and closes a
// half-open probe. Without it the breaker would only ever observe failures,
// the rolling ratio would be a constant 1.0, and one blip would open the
// circuit no matter how much good traffic surrounded it.
func (p *ServiceProxy) healthy(appID, instanceID string) {
	p.breaker.Success(serviceProxyEndpointKey(appID, instanceID))
}

func serviceProxyEndpointKey(appID, instanceID string) string { return appID + "\x00" + instanceID }

func parseServiceProxyPath(path string) (service, targetPath string, ok bool) {
	const prefix = "/v1/internal/services/"
	rest := strings.TrimPrefix(path, prefix)
	if rest == path || rest == "" || strings.HasPrefix(rest, "/") {
		return "", "", false
	}
	parts := strings.SplitN(rest, "/", 2)
	service = parts[0]
	if service == "" || strings.Contains(service, "?") {
		return "", "", false
	}
	targetPath = "/"
	if len(parts) == 2 && parts[1] != "" {
		targetPath = "/" + parts[1]
	}
	return service, targetPath, true
}

func serviceProxyProblem(w http.ResponseWriter, status int, detail string) {
	http.Error(w, detail, status)
}

type serviceProxyResponseWriter struct {
	dst       http.ResponseWriter
	header    http.Header
	status    int
	committed bool
	buffer    bytes.Buffer
}

func newServiceProxyResponseWriter(dst http.ResponseWriter) *serviceProxyResponseWriter {
	return &serviceProxyResponseWriter{dst: dst, header: make(http.Header)}
}

func (w *serviceProxyResponseWriter) Header() http.Header { return w.header }

func (w *serviceProxyResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	if status < http.StatusInternalServerError {
		w.commit()
	}
}

func (w *serviceProxyResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.committed {
		return w.dst.Write(p)
	}
	if w.buffer.Len()+len(p) > serviceProxyErrorBodyLimit {
		w.commit()
		return w.dst.Write(p)
	}
	return w.buffer.Write(p)
}

func (w *serviceProxyResponseWriter) Flush() {
	w.commit()
	if flusher, ok := w.dst.(http.Flusher); ok {
		flusher.Flush()
	}
}

// commitTrailers copies trailer entries the guest set after the header block
// was already flushed (ADR-197).
//
// commit() snapshots w.header once, but trailers are by definition written
// after the headers. Go records an undeclared trailer as an ordinary header
// key carrying http.TrailerPrefix, and a declared one appears as a new plain
// key after the response starts. Either way it lands in this buffer's own map
// and never reaches the client unless it is copied across afterwards --
// which for gRPC means the caller reads a complete stream carrying no
// grpc-status and has to infer success.
func (w *serviceProxyResponseWriter) commitTrailers() {
	if !w.committed {
		return
	}
	dst := w.dst.Header()
	for key, values := range w.header {
		if _, present := dst[key]; present && !strings.HasPrefix(key, http.TrailerPrefix) {
			continue
		}
		dst[key] = append([]string(nil), values...)
	}
}

func (w *serviceProxyResponseWriter) Unwrap() http.ResponseWriter { return w.dst }

func (w *serviceProxyResponseWriter) commit() {
	if w.committed {
		return
	}
	for key, values := range w.header {
		w.dst.Header()[key] = append([]string(nil), values...)
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.dst.WriteHeader(w.status)
	w.committed = true
	if w.buffer.Len() > 0 {
		_, _ = w.dst.Write(w.buffer.Bytes())
		w.buffer.Reset()
	}
}
