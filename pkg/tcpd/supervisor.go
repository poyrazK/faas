package tcpd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
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
	RefreshInterval time.Duration
	MaxConnections  int
	// MaxConnectionsPerAccount bounds concurrent sessions for one account on
	// this gateway. Zero disables the account-scoped cap.
	MaxConnectionsPerAccount int
	Metrics                  *tcpmetrics.Metrics
	OnError                  func(error)
	Listen                   func(network, address string) (net.Listener, error)
}

type supervisedListener struct {
	listener net.Listener
	cancel   context.CancelFunc
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
	limiter := NewConnectionLimiter(s.MaxConnectionsPerAccount)

	listeners := make(map[int]supervisedListener)
	var mu sync.Mutex
	closeAll := func() {
		mu.Lock()
		current := make([]supervisedListener, 0, len(listeners))
		for port, entry := range listeners {
			delete(listeners, port)
			current = append(current, entry)
		}
		mu.Unlock()
		for _, entry := range current {
			entry.cancel()
			_ = entry.listener.Close()
		}
	}
	defer closeAll()

	refresh := func() error {
		rows, err := s.Source.ListEnabledTCPListeners(ctx)
		if err != nil {
			return fmt.Errorf("list enabled TCP listeners: %w", err)
		}
		desired := make(map[int]Route, len(rows))
		for _, row := range rows {
			route := Route{
				PublicPort:   row.PublicPort,
				AppID:        row.AppID,
				ListenerName: row.ListenerName,
				GuestPort:    row.GuestPort,
				Protocol:     row.Protocol,
			}
			if err := ValidateRoute(route); err != nil {
				return err
			}
			if _, exists := desired[route.PublicPort]; exists {
				return fmt.Errorf("duplicate TCP listener public port %d", route.PublicPort)
			}
			desired[route.PublicPort] = route
		}

		mu.Lock()
		for port, entry := range listeners {
			if _, keep := desired[port]; !keep {
				delete(listeners, port)
				entry.cancel()
				_ = entry.listener.Close()
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
			childCtx, cancel := context.WithCancel(ctx)
			server := &Server{
				Listener:       listener,
				Routes:         s.Routes,
				Targets:        s.Targets,
				Forwarder:      s.Forwarder,
				Limiter:        limiter,
				Metrics:        s.Metrics,
				MaxConnections: s.MaxConnections,
				OnError: func(err error) {
					if s.OnError != nil {
						s.OnError(fmt.Errorf("TCP port %d: %w", port, err))
					}
				},
			}
			mu.Lock()
			listeners[port] = supervisedListener{listener: listener, cancel: cancel}
			mu.Unlock()
			go func(port int, route Route, srv *Server, childCtx context.Context) {
				if err := srv.Serve(childCtx); err != nil && childCtx.Err() == nil && s.OnError != nil {
					s.OnError(fmt.Errorf("serve TCP port %d (%s): %w", port, route.ListenerName, err))
				}
			}(port, route, server, childCtx)
		}
		return nil
	}

	if err := refresh(); err != nil {
		return err
	}
	ticker := time.NewTicker(s.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := refresh(); err != nil && s.OnError != nil {
				s.OnError(err)
			}
		}
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
	default:
		return nil
	}
}
