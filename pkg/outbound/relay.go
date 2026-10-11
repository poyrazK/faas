package outbound

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/outbound/relaywire"
)

// RelayIdentityVerifier authenticates the connection-scoped assertion vmmd
// attaches to an app relay connection (ADR-981).
type RelayIdentityVerifier interface {
	VerifyRelay(rawToken string) (WorkloadIdentity, error)
}

const (
	relayPreambleTimeout = 5 * time.Second
	// relayMaxPendingHandshakes bounds connections that have not yet sent a
	// verified preamble, so a slow peer cannot exhaust goroutines.
	relayMaxPendingHandshakes = 256
)

type relayIdentityKey struct{}

func withRelayIdentity(ctx context.Context, identity WorkloadIdentity) context.Context {
	return context.WithValue(ctx, relayIdentityKey{}, identity)
}

// relayIdentityFromContext returns the verified app for a relay request.
// Requests on the ordinary listener never carry one.
func relayIdentityFromContext(ctx context.Context) (WorkloadIdentity, bool) {
	identity, ok := ctx.Value(relayIdentityKey{}).(WorkloadIdentity)
	return identity, ok && identity.AppID != ""
}

// relayIdentityHeaders are caller-controlled identity channels. A relay
// connection's platform identity replaces all of them.
var relayIdentityHeaders = []string{TokenHeader, AppHeader, WorkloadIdentityHeader, ExecutionIdentityHeader, WorkflowIdentityHeader}

// NewRelayServer serves the gateway handler on relay connections. Each
// connection is authenticated once by its preamble; every request on it is
// attributed to that app. Callers pass the daemon's timeouts.
func NewRelayServer(handler http.Handler, verifier RelayIdentityVerifier, readHeaderTimeout, writeTimeout, idleTimeout time.Duration, log *slog.Logger) (*http.Server, error) {
	if handler == nil || verifier == nil {
		return nil, errors.New("outbound relay: handler and identity verifier are required")
	}
	if log == nil {
		log = slog.Default()
	}
	return &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, name := range relayIdentityHeaders {
				r.Header.Del(name)
			}
			handler.ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelDebug),
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			if relay, ok := conn.(*relayConn); ok {
				return withRelayIdentity(ctx, relay.identity)
			}
			return ctx
		},
	}, nil
}

// NewRelayListener wraps inner so Accept returns only connections whose
// preamble carried a valid relay assertion. Preambles are read concurrently
// with a deadline; failures close the connection without a response.
func NewRelayListener(inner net.Listener, verifier RelayIdentityVerifier, metrics *Metrics, log *slog.Logger) (net.Listener, error) {
	if inner == nil || verifier == nil {
		return nil, errors.New("outbound relay: listener and identity verifier are required")
	}
	if log == nil {
		log = slog.Default()
	}
	l := &relayListener{
		inner: inner, verifier: verifier, metrics: metrics, log: log,
		conns: make(chan net.Conn), errs: make(chan error, 1), done: make(chan struct{}),
		pending: make(chan struct{}, relayMaxPendingHandshakes),
	}
	go l.acceptLoop()
	return l, nil
}

type relayConn struct {
	net.Conn
	identity WorkloadIdentity
}

type relayListener struct {
	inner     net.Listener
	verifier  RelayIdentityVerifier
	metrics   *Metrics
	log       *slog.Logger
	conns     chan net.Conn
	errs      chan error
	done      chan struct{}
	pending   chan struct{}
	closeOnce sync.Once
}

func (l *relayListener) acceptLoop() {
	for {
		conn, err := l.inner.Accept()
		if err != nil {
			select {
			case l.errs <- err:
			case <-l.done:
			}
			return
		}
		select {
		case l.pending <- struct{}{}:
			go l.handshake(conn)
		default:
			l.metrics.ObserveRelayConnection("overload")
			_ = conn.Close()
		}
	}
}

func (l *relayListener) handshake(conn net.Conn) {
	defer func() { <-l.pending }()
	identity, err := readRelayIdentity(conn, l.verifier)
	if err != nil {
		l.metrics.ObserveRelayConnection("rejected")
		l.log.Debug("outbound relay: rejected connection", "remote", conn.RemoteAddr().String(), "err", err)
		_ = conn.Close()
		return
	}
	l.metrics.ObserveRelayConnection("accepted")
	select {
	case l.conns <- &relayConn{Conn: conn, identity: identity}:
	case <-l.done:
		_ = conn.Close()
	}
}

// readRelayIdentity reads the preamble under a deadline. For a TLS listener
// this first Read also completes the mTLS handshake and node verification.
func readRelayIdentity(conn net.Conn, verifier RelayIdentityVerifier) (WorkloadIdentity, error) {
	if err := conn.SetDeadline(time.Now().Add(relayPreambleTimeout)); err != nil {
		return WorkloadIdentity{}, err
	}
	preamble, err := relaywire.Read(conn)
	if err != nil {
		return WorkloadIdentity{}, err
	}
	identity, err := verifier.VerifyRelay(preamble.Assertion)
	if err != nil {
		return WorkloadIdentity{}, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return WorkloadIdentity{}, err
	}
	return identity, nil
}

func (l *relayListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case err := <-l.errs:
		return nil, err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *relayListener) Close() error {
	var err error
	l.closeOnce.Do(func() {
		close(l.done)
		err = l.inner.Close()
	})
	return err
}

func (l *relayListener) Addr() net.Addr { return l.inner.Addr() }
