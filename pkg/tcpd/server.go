package tcpd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/tcpmetrics"
)

// Forwarder is the transport seam implemented by gateway.TCPForwarder.
type Forwarder interface {
	ServeConn(ctx context.Context, conn net.Conn, target gateway.Target) error
}

// Server accepts public TCP connections, resolves their local listener port,
// and forwards them to a selected workload instance.
type Server struct {
	Listener  net.Listener
	Routes    RouteResolver
	Targets   TargetResolver
	Forwarder Forwarder
	Limiter   *ConnectionLimiter
	Metrics   *tcpmetrics.Metrics

	// MaxConnections bounds concurrent sessions. Zero means unlimited.
	MaxConnections int
	// OnError receives per-connection errors. It is optional; connection
	// errors do not stop the accept loop.
	OnError func(error)
}

// Serve runs until the listener fails or ctx is canceled. Cancellation closes
// the listener and all accepted connections, then waits for forwarding goroutines
// to exit before returning nil.
func (s *Server) Serve(ctx context.Context) error {
	if err := s.validate(); err != nil {
		return err
	}
	if ctx == nil {
		return errors.New("tcpd server requires a non-nil context")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	active := newActiveConnections()
	go func() {
		<-ctx.Done()
		_ = s.Listener.Close()
		active.CloseAll()
	}()

	var slots chan struct{}
	if s.MaxConnections > 0 {
		slots = make(chan struct{}, s.MaxConnections)
	}
	for {
		conn, err := s.Listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				wg.Wait()
				return nil
			}
			if isTemporaryAcceptError(err) {
				if !waitAcceptBackoff(ctx) {
					wg.Wait()
					return nil
				}
				continue
			}
			wg.Wait()
			return fmt.Errorf("accept TCP connection: %w", err)
		}

		session := s.Metrics.Begin()
		if slots != nil {
			select {
			case slots <- struct{}{}:
			default:
				_ = conn.Close()
				session.Reject("global_limit")
				continue
			}
		}
		active.Add(conn)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer active.Remove(conn)
			if slots != nil {
				defer func() { <-slots }()
			}
			if err := s.handle(ctx, conn, session); err != nil {
				if session != nil && ctx.Err() != nil {
					session.Finish("canceled")
				} else if session != nil {
					session.Finish("error")
				}
				if ctx.Err() == nil {
					s.report(err)
				}
				return
			}
			session.Finish("success")
		}()
	}
}

func (s *Server) validate() error {
	switch {
	case s == nil:
		return errors.New("nil tcpd server")
	case s.Listener == nil:
		return errors.New("tcpd server has no listener")
	case s.Routes == nil:
		return errors.New("tcpd server has no route resolver")
	case s.Targets == nil:
		return errors.New("tcpd server has no target resolver")
	case s.Forwarder == nil:
		return errors.New("tcpd server has no forwarder")
	case s.MaxConnections < 0:
		return errors.New("tcpd server max connections cannot be negative")
	default:
		return nil
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn, session *tcpmetrics.Session) error {
	defer func() { _ = conn.Close() }()
	publicPort, err := localTCPPort(conn)
	if err != nil {
		session.Reject("route_error")
		return err
	}
	route, ok, err := s.Routes.Resolve(ctx, publicPort)
	if err != nil {
		session.Reject("route_error")
		return fmt.Errorf("resolve TCP route for public port %d: %w", publicPort, err)
	}
	if !ok {
		session.Reject("route_missing")
		return fmt.Errorf("%w for public port %d", ErrNoRoute, publicPort)
	}
	if err := ValidateRoute(route); err != nil {
		session.Reject("invalid_route")
		return err
	}
	accountID := route.AccountID
	if accountID == "" {
		accountID = route.AppID
	}
	session.Bind(accountID)
	if s.Limiter != nil {
		key := route.AccountID
		if key == "" {
			key = route.AppID
		}
		release, ok := s.Limiter.Acquire(key)
		if !ok {
			session.Reject("account_limit")
			return fmt.Errorf("%w for %q", ErrConnectionLimit, key)
		}
		defer release()
	}
	target, err := s.Targets.ResolveTarget(ctx, route)
	if err != nil {
		session.Reject("target_error")
		return fmt.Errorf("resolve target for app %q listener %q: %w", route.AppID, route.ListenerName, err)
	}
	if target.AppID != "" && target.AppID != route.AppID {
		session.Reject("invalid_route")
		return fmt.Errorf("%w: target app %q does not match route app %q", ErrInvalidRoute, target.AppID, route.AppID)
	}
	target.AppID = route.AppID
	target.Port = route.GuestPort
	return s.Forwarder.ServeConn(ctx, conn, target)
}

func (s *Server) report(err error) {
	if s.OnError != nil {
		s.OnError(err)
	}
}

func localTCPPort(conn net.Conn) (int, error) {
	localAddr := conn.LocalAddr()
	addr, ok := localAddr.(*net.TCPAddr)
	if !ok || addr == nil || addr.Port < 1 || addr.Port > 65535 {
		return 0, fmt.Errorf("accepted connection has non-TCP local address %v", localAddr)
	}
	return addr.Port, nil
}

func isTemporaryAcceptError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func waitAcceptBackoff(ctx context.Context) bool {
	timer := time.NewTimer(5 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type activeConnections struct {
	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

func newActiveConnections() *activeConnections {
	return &activeConnections{conns: make(map[net.Conn]struct{})}
}

func (a *activeConnections) Add(conn net.Conn) {
	a.mu.Lock()
	a.conns[conn] = struct{}{}
	a.mu.Unlock()
}

func (a *activeConnections) Remove(conn net.Conn) {
	a.mu.Lock()
	delete(a.conns, conn)
	a.mu.Unlock()
}

func (a *activeConnections) CloseAll() {
	a.mu.Lock()
	conns := make([]net.Conn, 0, len(a.conns))
	for conn := range a.conns {
		conns = append(conns, conn)
	}
	a.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}
