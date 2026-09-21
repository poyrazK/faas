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
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/circuit"
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

	// ServiceProxyMaxAttempts bounds transport retries. Only idempotent,
	// bodyless requests may use the second attempt.
	ServiceProxyMaxAttempts = 2

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
)

// ServiceProxyResolver maps a service name to its app identity. The context
// is part of the contract so production implementations can use the request
// deadline for the app/account lookup.
type ServiceProxyResolver func(ctx context.Context, service string) (appID string, ok bool, err error)

// ServiceProxyAuthorizer enforces the tenant boundary between caller and
// target apps. A nil authorizer is treated as a wiring error and fails closed.
type ServiceProxyAuthorizer func(ctx context.Context, callerAppID, targetAppID string) error

// ServiceProxyCallerResolver binds the caller header to the network identity
// observed by the node-local listener. When it is configured, the resolved
// identity is authoritative and the caller header becomes an optional
// compatibility assertion. This lets guest requests omit a spoofable
// platform header while preserving the header contract for trusted callers.
type ServiceProxyCallerResolver func(ctx context.Context, remoteAddr string) (appID string, err error)

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
	EndpointTTL   time.Duration
	Now           func() time.Time
	Log           *slog.Logger
	// Breaker is the endpoint health breaker (ADR-197 §2). Nil installs
	// circuit.LegacyQuarantineConfig, which reproduces the fixed-TTL
	// quarantine this field replaced: one failure benches an endpoint for
	// EndpointTTL with no backoff growth. cmd/gatewayd-internal passes a
	// DefaultConfig group when FAAS_GATEWAY_CIRCUIT_BREAKER is on.
	Breaker *circuit.Group
	// Metrics receives breaker transitions (ADR-197 §2). Optional — nil
	// keeps the breaker fully working and simply publishes nothing, the
	// posture every other metrics hook in this package takes.
	Metrics *Metrics
}

// ServiceProxy is an HTTP service-name router backed by the gateway's live
// endpoint registry. It caches registry snapshots for a short lease, avoids
// endpoints that recently failed transport, and retries one alternate target
// for safe idempotent requests.
type ServiceProxy struct {
	provider      ServiceEndpointProvider
	resolve       ServiceProxyResolver
	authorize     ServiceProxyAuthorizer
	resolveCaller ServiceProxyCallerResolver
	forward       func(Target) http.Handler
	endpointTTL   time.Duration
	now           func() time.Time
	log           *slog.Logger

	breaker *circuit.Group

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
	return &ServiceProxy{
		provider:      cfg.Provider,
		resolve:       cfg.Resolve,
		authorize:     cfg.Authorize,
		resolveCaller: cfg.ResolveCaller,
		forward:       cfg.Forward,
		endpointTTL:   ttl,
		now:           now,
		log:           log,
		breaker:       breaker,
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
	caller := strings.TrimSpace(r.Header.Get(ServiceProxyCallerAppHeader))
	if p.resolveCaller != nil {
		resolved, err := p.resolveCaller(r.Context(), r.RemoteAddr)
		if err != nil {
			serviceProxyProblem(w, http.StatusServiceUnavailable, "caller identity is unavailable")
			return
		}
		if resolved == "" {
			serviceProxyProblem(w, http.StatusForbidden, "caller identity is unknown")
			return
		}
		if caller != "" && resolved != caller {
			serviceProxyProblem(w, http.StatusForbidden, "caller identity does not match the node identity")
			return
		}
		caller = resolved
	}
	if caller == "" {
		serviceProxyProblem(w, http.StatusUnauthorized, "caller identity is required")
		return
	}
	targetApp, err := p.resolveTargetApp(r.Context(), service)
	if err != nil {
		if errors.Is(err, ErrServiceProxyNotFound) {
			serviceProxyProblem(w, http.StatusNotFound, "service is not registered")
			return
		}
		serviceProxyProblem(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if p.authorize == nil {
		serviceProxyProblem(w, http.StatusServiceUnavailable, "service proxy authorizer is not wired")
		return
	}
	if err := p.authorize(r.Context(), caller, targetApp); err != nil {
		if errors.Is(err, ErrServiceProxyDenied) {
			serviceProxyProblem(w, http.StatusForbidden, "caller is not allowed to reach this service")
			return
		}
		serviceProxyProblem(w, http.StatusServiceUnavailable, "service authorization is unavailable")
		return
	}
	if p.provider == nil || p.forward == nil {
		serviceProxyProblem(w, http.StatusServiceUnavailable, "service proxy transport is not wired")
		return
	}
	endpoints, err := p.endpoints(r.Context(), targetApp)
	if err != nil {
		serviceProxyProblem(w, http.StatusServiceUnavailable, "service endpoint registry is unavailable")
		return
	}
	if len(endpoints) == 0 {
		serviceProxyProblem(w, http.StatusServiceUnavailable, "service has no healthy replicas")
		return
	}
	if !serviceProxyRetryable(r) {
		p.forwardOnce(w, r, targetPath, targetApp, endpoints, false)
		return
	}
	p.forwardOnce(w, r, targetPath, targetApp, endpoints, true)
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

func (p *ServiceProxy) resolveTargetApp(ctx context.Context, service string) (string, error) {
	if p.resolve == nil {
		return "", fmt.Errorf("service name resolver is not wired")
	}
	appID, ok, err := p.resolve(ctx, service)
	if err != nil {
		return "", fmt.Errorf("service name lookup: %w", err)
	}
	if !ok || appID == "" {
		return "", fmt.Errorf("%w: %s", ErrServiceProxyNotFound, service)
	}
	return appID, nil
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

func (p *ServiceProxy) forwardOnce(w http.ResponseWriter, r *http.Request, targetPath, appID string, endpoints []ServiceEndpoint, retry bool) {
	request := r.Clone(r.Context())
	request.URL.Path = targetPath
	request.URL.RawPath = ""
	request.RequestURI = ""
	request.Header = r.Header.Clone()
	request.Header.Del(ServiceProxyCallerAppHeader)
	// The service hop does not have the full deployment record, so clear all
	// inbound identity claims before adding the target identity it does know.
	// Preserve the request id for end-to-end correlation through the shared
	// platform identity renderer.
	requestID := request.Header.Get(api.RequestIDHeader)
	api.PlatformIdentity{RequestID: requestID, AppID: appID}.ApplyGuestHeaders(request.Header)
	for attempt := 0; attempt < ServiceProxyMaxAttempts; attempt++ {
		endpoint, ok := p.pick(appID, endpoints)
		if !ok {
			serviceProxyProblem(w, http.StatusServiceUnavailable, "service has no healthy replicas")
			return
		}
		identity := api.PlatformIdentity{
			RequestID:  requestID,
			AppID:      appID,
			InstanceID: endpoint.InstanceID,
			NodeID:     endpoint.NodeID,
		}
		identity.ApplyGuestHeaders(request.Header)
		signal := &staleTargetSignal{onStale: func() { p.quarantine(appID, endpoint.InstanceID) }}
		buffer := newServiceProxyResponseWriter(w)
		// withStaleTargetSignal intentionally inherits the inbound request
		// context so the bridge can report a stale target to this retry loop.
		forwardReq := request.WithContext(withStaleTargetSignal(request.Context(), signal)) //nolint:contextcheck // request context is deliberately wrapped with request-local stale-target state.
		p.forward(Target{NodeID: endpoint.NodeID, InstanceID: endpoint.InstanceID, Port: endpoint.Port}).ServeHTTP(buffer, forwardReq)
		if !signal.stale.Load() {
			// Report the healthy transport. Without this the breaker only
			// ever observes failures, the rolling ratio is a constant 1.0,
			// and one blip opens the circuit no matter how much good traffic
			// surrounds it. It also closes a half-open probe.
			p.healthy(appID, endpoint.InstanceID)
		}
		if retry && !buffer.committed && signal.stale.Load() && attempt+1 < ServiceProxyMaxAttempts {
			continue
		}
		buffer.commit()
		return
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
	for i := 0; i < len(endpoints); i++ {
		endpoint := endpoints[(int(start)+i)%len(endpoints)]
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

func serviceProxyRetryable(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0
	default:
		return false
	}
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
