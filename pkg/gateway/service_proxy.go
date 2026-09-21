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

// ServiceProxyAuthorizer enforces the tenant boundary between caller and
// target apps. A nil authorizer is treated as a wiring error and fails closed.
type ServiceProxyAuthorizer func(ctx context.Context, callerAppID, targetAppID string) error

// ServiceProxyCallerResolver binds the caller header to the network identity
// observed by the node-local listener. When it is configured, the resolved
// identity is authoritative and the caller header becomes an optional
// compatibility assertion. This lets guest requests omit a spoofable
// platform header while preserving the header contract for trusted callers.
type ServiceProxyCallerResolver func(ctx context.Context, remoteAddr string) (appID string, err error)

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
	Wake        ServiceProxyWaker
	EndpointTTL time.Duration
	Now         func() time.Time
	Log         *slog.Logger
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
	rawForward    func(Target) http.Handler
	wake          ServiceProxyWaker
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
		rawForward:    cfg.RawForward,
		wake:          cfg.Wake,
		endpointTTL:   ttl,
		now:           now,
		log:           log,
		snapshots:     make(map[string]serviceProxySnapshot),
		next:          make(map[string]uint64),
		quarantined:   make(map[string]time.Time),
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
	target, err := p.resolveTarget(r.Context(), service)
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
	if err := p.authorize(r.Context(), caller, target.AppID); err != nil {
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
	endpoints, served := p.routableEndpoints(w, r, target.AppID)
	if !served {
		return
	}
	p.dispatch(w, r, targetPath, target, endpoints) //nolint:contextcheck // the inbound request carries the canonical ctx; forwardOnce wraps it with request-local stale-target state rather than taking a separate ctx parameter.
}

// dispatch picks the guest bridge for the resolved target (ADR-197).
//
// An Upgrade request needs the verbatim-bytes path: the ordinary forwarder
// strips Connection/Upgrade as hop-by-hop headers (RFC 7230 §6.1), which
// turns a WebSocket handshake into a confusing upstream error. It also
// cannot be buffered or retried, because the response is a hijacked
// connection rather than a body.
func (p *ServiceProxy) dispatch(w http.ResponseWriter, r *http.Request, targetPath string, target ServiceTarget, endpoints []ServiceEndpoint) {
	if isUpgradeRequest(r) {
		if !target.WebSocketEnabled {
			serviceProxyProblem(w, http.StatusNotImplemented, "target service does not accept upgrade requests")
			return
		}
		if p.rawForward == nil {
			serviceProxyProblem(w, http.StatusNotImplemented, "raw-bytes bridge is not enabled on this node")
			return
		}
		p.forwardUpgrade(w, r, targetPath, target, endpoints)
		return
	}
	p.forwardOnce(w, r, targetPath, target, endpoints, serviceProxyRetryable(r))
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
func (p *ServiceProxy) routableEndpoints(w http.ResponseWriter, r *http.Request, appID string) ([]ServiceEndpoint, bool) {
	endpoints, err := p.endpoints(r.Context(), appID)
	if err != nil {
		serviceProxyProblem(w, http.StatusServiceUnavailable, "service endpoint registry is unavailable")
		return nil, false
	}
	if len(endpoints) > 0 {
		return endpoints, true
	}
	endpoints, err = p.wakeAndRefresh(r.Context(), appID)
	if err != nil {
		p.writeWakeFailure(w, appID, err)
		return nil, false
	}
	if len(endpoints) == 0 {
		serviceProxyProblem(w, http.StatusServiceUnavailable, "service has no healthy replicas")
		return nil, false
	}
	return endpoints, true
}

// writeWakeFailure maps a wake error onto the caller-facing response.
func (p *ServiceProxy) writeWakeFailure(w http.ResponseWriter, appID string, err error) {
	var full *WakeQueueFullError
	if errors.As(err, &full) {
		// Mirror the public edge: a saturated wake queue is a bounded,
		// retryable condition, not a failure of the service. Hand the caller
		// the same Retry-After the edge would so a peer workload can back off
		// instead of hot-looping on a restoring dependency.
		w.Header().Set("Retry-After", retryAfterSeconds(full.RetryAfter))
		serviceProxyProblem(w, http.StatusServiceUnavailable, "service is waking and its wake queue is full")
		return
	}
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
	if err := p.wake(ctx, appID); err != nil {
		return nil, err
	}
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
func (p *ServiceProxy) guestRequest(r *http.Request, targetPath string, target ServiceTarget) *http.Request {
	request := r.Clone(r.Context())
	request.URL.Path = targetPath
	request.URL.RawPath = ""
	request.RequestURI = ""
	request.Header = r.Header.Clone()
	request.Header.Del(ServiceProxyCallerAppHeader)
	requestID := request.Header.Get(api.RequestIDHeader)
	api.PlatformIdentity{RequestID: requestID, AppID: target.AppID}.ApplyGuestHeaders(request.Header)
	request.Header.Set("x-faas-protocol", serviceGuestProtocol(target))
	return request
}

// forwardUpgrade carries an Upgrade request to the guest over the raw-bytes
// bridge. There is no retry and no response buffering: the response is a
// hijacked connection, so the first endpoint chosen is the only one, and a
// stale-target signal cannot be acted on after bytes have flowed.
func (p *ServiceProxy) forwardUpgrade(w http.ResponseWriter, r *http.Request, targetPath string, target ServiceTarget, endpoints []ServiceEndpoint) {
	endpoint, ok := p.pick(target.AppID, endpoints)
	if !ok {
		serviceProxyProblem(w, http.StatusServiceUnavailable, "service has no healthy replicas")
		return
	}
	request := p.guestRequest(r, targetPath, target)
	api.PlatformIdentity{
		RequestID:  request.Header.Get(api.RequestIDHeader),
		AppID:      target.AppID,
		InstanceID: endpoint.InstanceID,
		NodeID:     endpoint.NodeID,
	}.ApplyGuestHeaders(request.Header)
	// Mirrors the public edge (ADR-080): the wake-timeline vocabulary marks a
	// raw-bytes session so observability does not have to re-derive it from
	// the Connection/Upgrade pair.
	request.Header.Set("x-faas-upgrade", "true")
	p.rawForward(Target{NodeID: endpoint.NodeID, InstanceID: endpoint.InstanceID, Port: endpoint.Port}).ServeHTTP(w, request)
}

func (p *ServiceProxy) forwardOnce(w http.ResponseWriter, r *http.Request, targetPath string, target ServiceTarget, endpoints []ServiceEndpoint, retry bool) {
	appID := target.AppID
	request := p.guestRequest(r, targetPath, target)
	requestID := request.Header.Get(api.RequestIDHeader)
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
		if retry && !buffer.committed && signal.stale.Load() && attempt+1 < ServiceProxyMaxAttempts {
			continue
		}
		buffer.commit()
		buffer.commitTrailers()
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
