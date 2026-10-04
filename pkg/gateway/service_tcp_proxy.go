package gateway

// The service TCP proxy is the raw-bytes sibling of ServiceProxy (ADR-530).
// A guest dials a private service address on a natural port; the host DNATs
// the connection onto this listener, and the original destination names the
// target. Identity, authorization, release pinning, endpoint leases, wake and
// the endpoint breaker are shared with the HTTP proxy so both transports see
// one consistent view of a service.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tcpmetrics"
)

// ErrServiceTCPTargetUnavailable reports a target that exists but refuses
// new sessions (maintenance, account hold, deleted account).
var ErrServiceTCPTargetUnavailable = errors.New("service target is not accepting connections")

// ServiceTCPTarget is the routing identity of one private service address.
type ServiceTCPTarget struct {
	AppID     string
	AccountID string
	Plan      api.Plan
	// TCPPorts are the target's declared TCP listeners; nothing else is
	// reachable through its service address.
	TCPPorts []int
	// IdleTimeout ends a quiet session, so an idle pooled connection keeps
	// the target awake no longer than an idle HTTP app would stay up.
	IdleTimeout time.Duration
}

// ServiceTCPTargetResolver maps a caller and the service address it dialed
// to a target in the caller's account. ok=false means no such service for
// this caller; ErrServiceTCPTargetUnavailable means the target refuses
// sessions.
type ServiceTCPTargetResolver func(ctx context.Context, callerAppID string, address netip.Addr) (target ServiceTCPTarget, ok bool, err error)

// ServiceTCPForward forwards one authorized session. It must behave like
// TCPForwarder.ServeConnAwaitingDial: an error wrapping
// ErrTCPGuestUnreachable leaves conn open and unread for a retry.
type ServiceTCPForward func(ctx context.Context, conn net.Conn, target Target, idle time.Duration) error

// ServiceTCPProxyConfig wires the proxy. Services is required: it is the
// guest ServiceProxy whose identity, authorizer and endpoint state the TCP
// path shares.
type ServiceTCPProxyConfig struct {
	Services            *ServiceProxy
	Resolve             ServiceTCPTargetResolver
	Forward             ServiceTCPForward
	OriginalDestination func(net.Conn) (netip.AddrPort, error)
	AddressCIDR         netip.Prefix
	ReservedPorts       []int
	// SessionsPerAccount is the per-node concurrent session cap for an
	// account's plan. Zero for a plan refuses its sessions.
	SessionsPerAccount func(api.Plan) int
	MaxSessions        int
	WakeTimeout        time.Duration
	Metrics            *tcpmetrics.Metrics
	Log                *slog.Logger
}

// ServiceTCPProxy accepts DNATed guest connections and forwards them to the
// declared TCP listener of an authorized same-account service.
type ServiceTCPProxy struct {
	cfg      ServiceTCPProxyConfig
	slots    chan struct{}
	accounts *serviceTCPAccountSessions
}

// NewServiceTCPProxy validates the wiring; a missing seam is a startup error
// rather than a fail-open path.
func NewServiceTCPProxy(cfg ServiceTCPProxyConfig) (*ServiceTCPProxy, error) {
	switch {
	case cfg.Services == nil || cfg.Services.authorize == nil || cfg.Services.provider == nil:
		return nil, errors.New("service TCP proxy needs the guest service proxy with an authorizer and endpoint provider")
	case cfg.Services.resolveCallerIdentity == nil:
		return nil, errors.New("service TCP proxy needs source-address caller identity")
	case cfg.Resolve == nil || cfg.Forward == nil || cfg.OriginalDestination == nil || cfg.SessionsPerAccount == nil:
		return nil, errors.New("service TCP proxy is missing a resolver, forwarder, destination lookup or session cap")
	case !cfg.AddressCIDR.IsValid() || !cfg.AddressCIDR.Addr().Is4():
		return nil, fmt.Errorf("service TCP proxy address block %s is not an IPv4 prefix", cfg.AddressCIDR)
	case cfg.MaxSessions <= 0 || cfg.WakeTimeout <= 0:
		return nil, errors.New("service TCP proxy needs a positive session cap and wake timeout")
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &ServiceTCPProxy{
		cfg:      cfg,
		slots:    make(chan struct{}, cfg.MaxSessions),
		accounts: &serviceTCPAccountSessions{current: make(map[string]int)},
	}, nil
}

// Serve accepts until ln fails or ctx ends. Cancellation closes the listener
// and every accepted connection, then waits for the sessions to unwind.
func (p *ServiceTCPProxy) Serve(ctx context.Context, ln net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		active = make(map[net.Conn]struct{})
	)
	go func() {
		<-ctx.Done()
		_ = ln.Close()
		mu.Lock()
		for conn := range active {
			_ = conn.Close()
		}
		mu.Unlock()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			wg.Wait()
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("service TCP proxy accept: %w", err)
		}
		mu.Lock()
		active[conn] = struct{}{}
		mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.ServeConn(ctx, conn)
			mu.Lock()
			delete(active, conn)
			mu.Unlock()
		}()
	}
}

// ServeConn handles one accepted connection and always closes it.
func (p *ServiceTCPProxy) ServeConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	session := p.cfg.Metrics.Begin()
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		session.Reject("global_limit")
		return
	}
	outcome, reason, err := p.serve(ctx, conn, session)
	switch {
	case reason != "":
		session.Reject(reason)
		p.cfg.Log.Debug("gateway: service TCP connection refused", "reason", reason, "remote", conn.RemoteAddr().String(), "err", err)
	case ctx.Err() != nil:
		session.Finish("canceled")
	default:
		session.Finish(outcome)
		if err != nil {
			p.cfg.Log.Debug("gateway: service TCP session ended with error", "err", err)
		}
	}
}

// serve returns a rejection reason (a bounded metric label) when the session
// never reached a guest, otherwise the forwarding outcome.
func (p *ServiceTCPProxy) serve(ctx context.Context, conn net.Conn, session *tcpmetrics.Session) (outcome, reason string, err error) {
	dst, err := p.cfg.OriginalDestination(conn)
	if err != nil {
		return "", "original_destination", err
	}
	if !p.cfg.AddressCIDR.Contains(dst.Addr()) {
		return "", "not_service_address", nil
	}
	if slices.Contains(p.cfg.ReservedPorts, int(dst.Port())) {
		return "", "reserved_port", nil
	}
	services := p.cfg.Services
	callerAppID, callerDeploymentID, err := services.resolveCallerIdentity(ctx, conn.RemoteAddr().String())
	switch {
	case err != nil:
		return "", "identity_unavailable", err
	case callerAppID == "":
		return "", "unknown_caller", nil
	}
	target, ok, err := p.cfg.Resolve(ctx, callerAppID, dst.Addr())
	switch {
	case errors.Is(err, ErrServiceTCPTargetUnavailable):
		return "", "target_unavailable", err
	case err != nil:
		return "", "registry_unavailable", err
	case !ok:
		return "", "unknown_service", nil
	}
	session.Bind(target.AccountID)
	if reason, err := p.authorize(ctx, callerAppID, target, int(dst.Port())); reason != "" {
		return "", reason, err
	}
	release, ok := p.accounts.acquire(target.AccountID, p.cfg.SessionsPerAccount(target.Plan))
	if !ok {
		return "", "account_limit", nil
	}
	defer release()
	deploymentID, err := p.releaseDeployment(ctx, callerAppID, callerDeploymentID, target.AppID)
	if err != nil {
		return "", "release", err
	}
	return p.forward(ctx, conn, target, deploymentID, int(dst.Port()))
}

// authorize applies the HTTP mesh's tenant, binding, caller and preview
// policy, then the TCP-only rules: a method/path call scope cannot be
// enforced on raw bytes, and only declared TCP listeners are reachable.
func (p *ServiceTCPProxy) authorize(ctx context.Context, callerAppID string, target ServiceTCPTarget, port int) (string, error) {
	caller, err := p.cfg.Services.authorize(ctx, callerAppID, target.AppID)
	switch {
	case errors.Is(err, ErrServiceProxyBindingDenied):
		return "binding_denied", err
	case errors.Is(err, ErrServiceProxyCallerDenied):
		return "caller_denied", err
	case errors.Is(err, ErrServiceProxyPreviewDenied), errors.Is(err, ErrServiceProxyPreviewProductionDenied):
		return "preview_denied", err
	case errors.Is(err, ErrServiceProxyDenied):
		return "denied", err
	case err != nil:
		return "authorization_unavailable", err
	case caller.CallScope != nil:
		return "call_scope", nil
	case !slices.Contains(target.TCPPorts, port):
		return "undeclared_port", nil
	}
	return "", nil
}

// releaseDeployment keeps a caller inside its project release graph exactly
// as an HTTP call would. Empty means no pin.
func (p *ServiceTCPProxy) releaseDeployment(ctx context.Context, callerAppID, callerDeploymentID, targetAppID string) (string, error) {
	resolve := p.cfg.Services.resolveRelease
	if resolve == nil || callerDeploymentID == "" {
		return "", nil
	}
	_, deploymentID, err := resolve(ctx, callerAppID, callerDeploymentID, targetAppID, "")
	return deploymentID, err
}

// forward picks a replica, waking the target when none is routable, and
// tries one alternate replica only when the first could not be dialed before
// any client byte was read.
func (p *ServiceTCPProxy) forward(ctx context.Context, conn net.Conn, target ServiceTCPTarget, deploymentID string, port int) (string, string, error) {
	services := p.cfg.Services
	for attempt := 0; attempt < 2; attempt++ {
		endpoints, reason, err := p.routable(ctx, target.AppID, deploymentID)
		if reason != "" {
			return "", reason, err
		}
		endpoint, ok := services.pick(target.AppID, endpoints)
		if !ok {
			return "", "no_replica", nil
		}
		hop := serviceEndpointTarget(target.AppID, endpoint)
		hop.Port = port
		err = p.cfg.Forward(ctx, conn, hop, target.IdleTimeout)
		if errors.Is(err, ErrTCPGuestUnreachable) {
			services.quarantine(target.AppID, endpoint.InstanceID)
			services.invalidateEndpoints(target.AppID)
			continue
		}
		services.healthy(target.AppID, endpoint.InstanceID)
		if err != nil {
			return "error", "", err
		}
		return "success", "", nil
	}
	return "", "guest_unreachable", nil
}

// routable mirrors ServiceProxy.routableEndpoints without an HTTP writer.
func (p *ServiceTCPProxy) routable(ctx context.Context, appID, deploymentID string) ([]ServiceEndpoint, string, error) {
	services := p.cfg.Services
	endpoints, err := services.endpoints(ctx, appID)
	if err != nil {
		return nil, "registry_unavailable", err
	}
	if endpoints = serviceEndpointsForDeployment(endpoints, deploymentID); len(endpoints) > 0 {
		return endpoints, "", nil
	}
	wakeCtx, cancel := context.WithTimeout(ctx, p.cfg.WakeTimeout)
	defer cancel()
	endpoints, err = services.wakeAndRefresh(wakeCtx, appID, deploymentID)
	var full *WakeQueueFullError
	switch {
	case errors.As(err, &full):
		return nil, "wake_queue_full", err
	case err != nil:
		return nil, "wake_failed", err
	case len(endpoints) == 0:
		return nil, "no_replica", nil
	}
	return endpoints, "", nil
}

// serviceTCPAccountSessions is a per-account counter whose cap comes from
// each account's plan, unlike tcpd's single-cap limiter.
type serviceTCPAccountSessions struct {
	mu      sync.Mutex
	current map[string]int
}

func (s *serviceTCPAccountSessions) acquire(accountID string, limit int) (func(), bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if accountID == "" || limit <= 0 || s.current[accountID] >= limit {
		return func() {}, false
	}
	s.current[accountID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.current[accountID] <= 1 {
				delete(s.current, accountID)
				return
			}
			s.current[accountID]--
		})
	}, true
}
