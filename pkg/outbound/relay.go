package outbound

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound/relaywire"
)

// RelayIdentityVerifier authenticates the connection-scoped assertion vmmd
// attaches to an app (ADR-989) or Run (ADR-990) relay connection.
type RelayIdentityVerifier interface {
	VerifyRelay(rawToken string) (WorkloadIdentity, error)
	VerifyExecutionRelay(rawToken string) (ExecutionIdentity, error)
}

// relayPrincipal is the verified caller of one relay connection: an app, or
// a Run with the integrations its scheduler grant named.
type relayPrincipal struct {
	recorderRequestID     string
	rehearsalRunID        string
	app                   WorkloadIdentity
	execution             *ExecutionIdentity
	executionIntegrations map[string]struct{}
}

func (p relayPrincipal) accountID() string {
	if p.execution != nil {
		return p.execution.AccountID
	}
	return p.app.AccountID
}

type relayPrincipalKey struct{}

func withRelayPrincipal(ctx context.Context, principal relayPrincipal) context.Context {
	return context.WithValue(ctx, relayPrincipalKey{}, principal)
}

func relayPrincipalFromContext(ctx context.Context) (relayPrincipal, bool) {
	principal, ok := ctx.Value(relayPrincipalKey{}).(relayPrincipal)
	return principal, ok && (principal.execution != nil || principal.app.AppID != "")
}

const (
	relayPreambleTimeout = 5 * time.Second
	// relayMaxPendingHandshakes bounds connections that have not yet sent a
	// verified preamble, so a slow peer cannot exhaust goroutines.
	relayMaxPendingHandshakes = 256
)

func withRelayIdentity(ctx context.Context, identity WorkloadIdentity) context.Context {
	return withRelayPrincipal(ctx, relayPrincipal{app: identity})
}

// relayIdentityFromContext returns the verified app for a relay request.
// Requests on the ordinary listener and Run relays never carry one.
func relayIdentityFromContext(ctx context.Context) (WorkloadIdentity, bool) {
	principal, ok := relayPrincipalFromContext(ctx)
	return principal.app, ok && principal.execution == nil && principal.rehearsalRunID == ""
}

// relayExecutionFromContext returns the verified Run of a relay request.
func relayExecutionFromContext(ctx context.Context) (ExecutionIdentity, map[string]struct{}, bool) {
	principal, ok := relayPrincipalFromContext(ctx)
	if !ok || principal.execution == nil {
		return ExecutionIdentity{}, nil, false
	}
	return *principal.execution, principal.executionIntegrations, true
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
				return withRelayPrincipal(ctx, relay.principal)
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
	principal relayPrincipal
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
	principal, err := readRelayPrincipal(conn, l.verifier)
	if err != nil {
		l.metrics.ObserveRelayConnection("rejected")
		l.log.Debug("outbound relay: rejected connection", "remote", conn.RemoteAddr().String(), "err", err)
		_ = conn.Close()
		return
	}
	l.metrics.ObserveRelayConnection("accepted")
	select {
	case l.conns <- &relayConn{Conn: conn, principal: principal}:
	case <-l.done:
		_ = conn.Close()
	}
}

// readRelayPrincipal reads the preamble under a deadline. For a TLS listener
// this first Read also completes the mTLS handshake and node verification.
// An app assertion carries no grant list; a Run assertion must carry its
// granted integrations (ADR-990).
func readRelayPrincipal(conn net.Conn, verifier RelayIdentityVerifier) (relayPrincipal, error) {
	if err := conn.SetDeadline(time.Now().Add(relayPreambleTimeout)); err != nil {
		return relayPrincipal{}, err
	}
	preamble, err := relaywire.Read(conn)
	if err != nil {
		return relayPrincipal{}, err
	}
	var principal relayPrincipal
	if rehearsal, ok := verifier.(RehearsalRelayIdentityVerifier); ok && len(preamble.ExecutionIntegrations) == 0 {
		if identity, run, verifyErr := rehearsal.VerifyRehearsalRelay(preamble.Assertion); verifyErr == nil {
			id, err := uuid.Parse(preamble.RecorderRequestID)
			if err != nil || id == uuid.Nil {
				return relayPrincipal{}, errors.New("outbound relay: a rehearsal needs a recorder request")
			}
			if err := conn.SetDeadline(time.Time{}); err != nil {
				return relayPrincipal{}, err
			}
			return relayPrincipal{app: identity, rehearsalRunID: run, recorderRequestID: id.String()}, nil
		}
	}
	if preamble.RecorderRequestID != "" {
		return relayPrincipal{}, errors.New("outbound relay: recorder requests require rehearsal identity")
	}
	if identity, appErr := verifier.VerifyRelay(preamble.Assertion); appErr == nil && len(preamble.ExecutionIntegrations) == 0 {
		principal = relayPrincipal{app: identity}
	} else {
		execution, execErr := verifier.VerifyExecutionRelay(preamble.Assertion)
		if execErr != nil {
			return relayPrincipal{}, errors.Join(appErr, execErr)
		}
		granted, normalizeErr := api.NormalizeExecutionIntegrationIDs(preamble.ExecutionIntegrations)
		if normalizeErr != nil || len(granted) == 0 {
			return relayPrincipal{}, errors.New("outbound relay: a Run relay needs its granted integrations")
		}
		principal = relayPrincipal{execution: &execution, executionIntegrations: make(map[string]struct{}, len(granted))}
		for _, id := range granted {
			principal.executionIntegrations[id] = struct{}{}
		}
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return relayPrincipal{}, err
	}
	return principal, nil
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
