package tcpd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tcpmetrics"
)

// EnabledListenerSource supplies the durable listener identities tcpd should
// expose. It is deliberately narrower than state.TCPListenerStore so a read-
// only edge process cannot accidentally mutate control-plane state.
type EnabledListenerSource interface {
	ListEnabledTCPListeners(ctx context.Context) ([]state.TCPListener, error)
}

// Supervisor binds one listener per enabled app-owned TCP endpoint and keeps
// that set synchronized with durable state. A port is only opened after the
// control plane has enabled its listener, so disabled/deleted endpoints fail
// closed without requiring a process restart.
type Supervisor struct {
	BindHost        string
	Source          EnabledListenerSource
	Routes          RouteResolver
	Targets         TargetResolver
	Forwarder       Forwarder
	Certificates    CertificateProvider
	Observations    TLSObservationPublisher
	EdgeID          string
	RefreshInterval time.Duration
	MaxConnections  int
	// MaxConnectionsPerAccount bounds concurrent sessions for one account on
	// this gateway. Zero disables the account-scoped cap.
	MaxConnectionsPerAccount int
	Metrics                  *tcpmetrics.Metrics
	OnError                  func(error)
	// OnReady runs after the initial listener snapshot has been validated and
	// every enabled public socket has been bound. Later reconciliation errors
	// are reported through OnError without revoking initial readiness.
	OnReady func()
	Listen  func(network, address string) (net.Listener, error)

	lifecycleMu sync.Mutex
	drainCh     chan struct{}
	drainOnce   sync.Once
	serveDone   chan struct{}
	serveCancel context.CancelFunc
}

type supervisedListener struct {
	route    Route
	listener net.Listener
	cancel   context.CancelFunc
	stopping *atomic.Bool
}

func (l supervisedListener) stop() {
	l.stopping.Store(true)
	// TLS and forwarding cancellation can close sessions immediately. Release
	// the accepting socket first so a closed session also proves port release.
	_ = l.listener.Close()
	l.cancel()
}

// Serve runs the listener reconciliation loop until ctx is canceled.
func (s *Supervisor) Serve(ctx context.Context) error {
	if err := s.validate(); err != nil {
		return err
	}
	if ctx == nil {
		return errors.New("tcpd supervisor requires a non-nil context")
	}
	if s.RefreshInterval <= 0 {
		s.RefreshInterval = 2 * time.Second
	}
	if s.Listen == nil {
		s.Listen = net.Listen
	}
	if s.BindHost == "" {
		s.BindHost = "0.0.0.0"
	}
	serveCtx, serveCancel := context.WithCancel(ctx)
	drainCh := make(chan struct{})
	serveDone := make(chan struct{})
	s.lifecycleMu.Lock()
	s.drainCh = drainCh
	s.serveDone = serveDone
	s.serveCancel = serveCancel
	s.lifecycleMu.Unlock()
	defer func() {
		serveCancel()
		close(serveDone)
		s.lifecycleMu.Lock()
		if s.serveDone == serveDone {
			s.drainCh = nil
			s.serveDone = nil
			s.serveCancel = nil
		}
		s.lifecycleMu.Unlock()
	}()
	limiter := NewConnectionLimiter(s.MaxConnectionsPerAccount)
	var connectionSlots chan struct{}
	if s.MaxConnections > 0 {
		connectionSlots = make(chan struct{}, s.MaxConnections)
	}

	listeners := make(map[int]supervisedListener)
	var mu sync.Mutex
	var servers sync.WaitGroup
	draining := make(chan struct{})
	isDraining := func() bool {
		select {
		case <-draining:
			return true
		default:
			return false
		}
	}
	closeAll := func() {
		mu.Lock()
		current := make([]supervisedListener, 0, len(listeners))
		for port, entry := range listeners {
			delete(listeners, port)
			current = append(current, entry)
		}
		mu.Unlock()
		for _, entry := range current {
			entry.stop()
		}
	}
	defer func() {
		closeAll()
		servers.Wait()
		s.Metrics.SetTLSReadiness(0, 0, time.Time{})
	}()

	publication := tlsObservationPublication{saved: make(map[string]state.TCPListenerTLSObservation)}
	refresh := func() error {
		rows, err := s.Source.ListEnabledTCPListeners(serveCtx)
		if err != nil {
			return fmt.Errorf("list enabled TCP listeners: %w", err)
		}
		desired := make(map[int]Route, len(rows))
		readyTLS, notReadyTLS := 0, 0
		var earliestExpiry time.Time
		observations := make([]state.TCPListenerTLSObservation, 0, len(rows))
		for _, row := range rows {
			route, err := routeFromListener(row)
			if err != nil {
				return err
			}
			if _, exists := desired[route.PublicPort]; exists {
				return fmt.Errorf("duplicate TCP listener public port %d", route.PublicPort)
			}
			desired[route.PublicPort] = route
			if route.TLSHostname != "" {
				expiry := observeTLSCertificateExpiry(serveCtx, s.Certificates, route.TLSHostname)
				observedAt := time.Now()
				if !expiry.After(observedAt) {
					expiry = time.Time{}
				}
				if expiry.IsZero() {
					notReadyTLS++
				} else {
					readyTLS++
					if earliestExpiry.IsZero() || expiry.Before(earliestExpiry) {
						earliestExpiry = expiry
					}
				}
				observations = append(observations, state.TCPListenerTLSObservation{
					ListenerID: row.ID, EdgeID: s.EdgeID, Hostname: row.TLSHostname,
					IntentUpdatedAt: row.UpdatedAt, ObservedAt: observedAt, Ready: !expiry.IsZero(), NotAfter: expiry,
				})
			}
		}
		s.Metrics.SetTLSReadiness(readyTLS, notReadyTLS, earliestExpiry)
		if err := publication.publish(serveCtx, s.Observations, observations); err != nil && serveCtx.Err() == nil && s.OnError != nil {
			s.OnError(fmt.Errorf("publish TCP TLS certificate status: %w", err))
		}

		mu.Lock()
		for port, entry := range listeners {
			if route, keep := desired[port]; !keep || route != entry.route {
				delete(listeners, port)
				entry.stop()
			}
		}
		mu.Unlock()

		for port, route := range desired {
			mu.Lock()
			_, alreadyOpen := listeners[port]
			mu.Unlock()
			if alreadyOpen {
				continue
			}
			listener, err := s.Listen("tcp", net.JoinHostPort(s.BindHost, strconv.Itoa(port)))
			if err != nil {
				return fmt.Errorf("bind TCP listener %d: %w", port, err)
			}
			childCtx, cancel := context.WithCancel(serveCtx)
			stopping := new(atomic.Bool)
			server := &Server{
				Listener:        listener,
				BoundRoute:      &route,
				Routes:          s.Routes,
				Targets:         s.Targets,
				Forwarder:       s.Forwarder,
				Certificates:    s.Certificates,
				Limiter:         limiter,
				Metrics:         s.Metrics,
				MaxConnections:  s.MaxConnections,
				connectionSlots: connectionSlots,
				OnError: func(err error) {
					if s.OnError != nil && !isDraining() && !stopping.Load() {
						s.OnError(fmt.Errorf("TCP port %d: %w", port, err))
					}
				},
			}
			mu.Lock()
			listeners[port] = supervisedListener{listener: listener, cancel: cancel, route: route, stopping: stopping}
			mu.Unlock()
			servers.Add(1)
			go func(port int, route Route, srv *Server, childCtx context.Context) {
				defer servers.Done()
				if err := srv.Serve(childCtx); err != nil && childCtx.Err() == nil && s.OnError != nil && !isDraining() && !stopping.Load() {
					s.OnError(fmt.Errorf("serve TCP port %d (%s): %w", port, route.ListenerName, err))
				}
			}(port, route, server, childCtx)
		}
		return nil
	}

	if err := refresh(); err != nil {
		return err
	}
	if s.OnReady != nil {
		s.OnReady()
	}
	ticker := time.NewTicker(s.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-serveCtx.Done():
			closeAll()
			servers.Wait()
			return nil
		case <-drainCh:
			close(draining)
			closeListeners := func() {
				mu.Lock()
				current := make([]supervisedListener, 0, len(listeners))
				for _, entry := range listeners {
					current = append(current, entry)
				}
				mu.Unlock()
				for _, entry := range current {
					// Closing the listener stops new accepts but leaves each
					// child context alive so existing sessions can finish.
					_ = entry.listener.Close()
				}
			}
			closeListeners()
			servers.Wait()
			return nil
		case <-ticker.C:
			if err := refresh(); err != nil && s.OnError != nil {
				s.OnError(err)
			}
		}
	}
}

// Drain stops accepting new connections and waits for the already accepted
// sessions to finish. If ctx expires, the supervisor is canceled so active
// sessions are closed and the method returns ctx.Err(). A supervisor that has
// not started serving is treated as already drained.
func (s *Supervisor) Drain(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("tcpd supervisor drain requires a non-nil context")
	}

	s.lifecycleMu.Lock()
	drainCh := s.drainCh
	serveDone := s.serveDone
	serveCancel := s.serveCancel
	s.lifecycleMu.Unlock()
	if drainCh == nil || serveDone == nil {
		return nil
	}
	s.drainOnce.Do(func() { close(drainCh) })
	select {
	case <-serveDone:
		return nil
	case <-ctx.Done():
		if serveCancel != nil {
			serveCancel()
		}
		<-serveDone
		return ctx.Err()
	}
}

func (s *Supervisor) validate() error {
	switch {
	case s == nil:
		return errors.New("nil tcpd supervisor")
	case s.Source == nil:
		return errors.New("tcpd supervisor has no listener source")
	case s.Routes == nil:
		return errors.New("tcpd supervisor has no route resolver")
	case s.Targets == nil:
		return errors.New("tcpd supervisor has no target resolver")
	case s.Forwarder == nil:
		return errors.New("tcpd supervisor has no forwarder")
	case s.MaxConnections < 0:
		return errors.New("tcpd supervisor max connections cannot be negative")
	case s.MaxConnectionsPerAccount < 0:
		return errors.New("tcpd supervisor max connections per account cannot be negative")
	case s.Observations != nil && state.ValidateTCPListenerTLSEdgeID(s.EdgeID) != nil:
		return errors.New("tcpd supervisor requires a valid observation edge identity")
	default:
		return nil
	}
}
