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
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// ServiceProxyCallerAppHeader is the platform-owned caller identity header
	// used on the trusted node-local hop. The authorizer must validate it before
	// a request is forwarded; the header is stripped before the guest hop.
	ServiceProxyCallerAppHeader = "X-Faas-Caller-App"

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

	mu          sync.Mutex
	snapshots   map[string]serviceProxySnapshot
	next        map[string]uint64
	quarantined map[string]time.Time
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
	return &ServiceProxy{
		provider:      cfg.Provider,
		resolve:       cfg.Resolve,
		authorize:     cfg.Authorize,
		resolveCaller: cfg.ResolveCaller,
		forward:       cfg.Forward,
		endpointTTL:   ttl,
		now:           now,
		log:           log,
		snapshots:     make(map[string]serviceProxySnapshot),
		next:          make(map[string]uint64),
		quarantined:   make(map[string]time.Time),
	}
}

// ServeHTTP accepts /v1/internal/services/{service}[/{path...}]. The service
// segment is resolved to an app; the remaining path is forwarded unchanged.
func (p *ServiceProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	service, targetPath, ok := parseServiceProxyPath(r.URL.Path)
	if !ok {
		serviceProxyProblem(w, http.StatusNotFound, "service path must match /v1/internal/services/<name>[/<path>]")
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
	request.Header.Set("X-Faas-App", appID)
	for attempt := 0; attempt < ServiceProxyMaxAttempts; attempt++ {
		endpoint, ok := p.pick(appID, endpoints)
		if !ok {
			serviceProxyProblem(w, http.StatusServiceUnavailable, "service has no healthy replicas")
			return
		}
		request.Header.Set("X-Faas-Instance", endpoint.InstanceID)
		signal := &staleTargetSignal{onStale: func() { p.quarantine(appID, endpoint.InstanceID) }}
		buffer := newServiceProxyResponseWriter(w)
		// withStaleTargetSignal intentionally inherits the inbound request
		// context so the bridge can report a stale target to this retry loop.
		forwardReq := request.WithContext(withStaleTargetSignal(request.Context(), signal)) //nolint:contextcheck // request context is deliberately wrapped with request-local stale-target state.
		p.forward(Target{NodeID: endpoint.NodeID, InstanceID: endpoint.InstanceID, Port: endpoint.Port}).ServeHTTP(buffer, forwardReq)
		if retry && !buffer.committed && signal.stale.Load() && attempt+1 < ServiceProxyMaxAttempts {
			continue
		}
		buffer.commit()
		return
	}
}

func (p *ServiceProxy) pick(appID string, endpoints []ServiceEndpoint) (ServiceEndpoint, bool) {
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	start := p.next[appID]
	p.next[appID]++
	for i := 0; i < len(endpoints); i++ {
		endpoint := endpoints[(int(start)+i)%len(endpoints)]
		until := p.quarantined[serviceProxyEndpointKey(appID, endpoint.InstanceID)]
		if until.IsZero() || !now.Before(until) {
			if !until.IsZero() {
				delete(p.quarantined, serviceProxyEndpointKey(appID, endpoint.InstanceID))
			}
			return endpoint, true
		}
	}
	return ServiceEndpoint{}, false
}

func (p *ServiceProxy) quarantine(appID, instanceID string) {
	p.mu.Lock()
	p.quarantined[serviceProxyEndpointKey(appID, instanceID)] = p.now().Add(p.endpointTTL)
	p.mu.Unlock()
	p.log.Warn("gateway: service proxy quarantined stale endpoint", "app", appID, "instance", instanceID)
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
