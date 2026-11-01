// Package pgproxy is the database proxy (ADR-1027): the only public listener
// on a database node. It terminates TLS, routes by SNI to a native database,
// authenticates the client with SCRAM against the role's stored verifier
// before anything is woken, wakes the database through schedd, opens a
// session to its data port through vmmd and logs in with the key the client
// proved. It never sees or stores a password.
package pgproxy

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgproto3"

	"github.com/onebox-faas/faas/pkg/dbcreds"
	"github.com/onebox-faas/faas/pkg/pgscram"
)

// PostgreSQL protocol request codes sent in place of a startup message.
const (
	sslRequestCode    = 80877103
	gssEncRequestCode = 80877104
	cancelRequestCode = 80877102
	tlsHandshakeByte  = 0x16
)

// ErrNoVerifier reports that a database has no such role.
var ErrNoVerifier = dbcreds.ErrNoVerifier

// Credentials looks up a role's SCRAM verifier.
type Credentials interface {
	Verifier(ctx context.Context, databaseID, role string) (pgscram.Verifier, error)
	// Forget drops a cached verifier after a failed login. A rotated
	// password has a new salt, so a stale entry cannot be retried within
	// the handshake; the next attempt reads the current verifier.
	Forget(databaseID, role string)
}

// Waker wakes a database through schedd and records activity on it.
type Waker interface {
	// Wake returns the vmmd target of the node running the database.
	Wake(ctx context.Context, databaseID string) (string, error)
	// Touch records activity and the bytes sent to the database's clients
	// since the last touch (the egress meter). The bytes are reported at
	// most once: a failed touch drops them.
	Touch(ctx context.Context, databaseID string, egressBytes int64) error
}

// SessionDialer opens a byte stream to a database VM's data port.
type SessionDialer interface {
	Dial(ctx context.Context, target, databaseID string) (io.ReadWriteCloser, error)
}

// Config tunes the proxy.
type Config struct {
	// TLS carries the certificate for *.<HostSuffix>.
	TLS *tls.Config
	// HostSuffix is the SNI suffix after the database id, e.g.
	// "db.gregale.dev" for <uuid>.db.gregale.dev.
	HostSuffix string
	// DecoySecret keys the decoy verifiers used for unknown databases and
	// roles, so their failures look like a wrong password.
	DecoySecret      []byte
	HandshakeTimeout time.Duration
	WakeTimeout      time.Duration
	TouchInterval    time.Duration
	// MaxConnections caps open client connections; MaxHandshakes caps
	// those not yet authenticated, and MaxHandshakesPerSource those from
	// one address. Unauthenticated clients cost a TLS handshake and a
	// verifier lookup each, so they are bounded well below sessions.
	// Connections past a cap are closed at once.
	MaxConnections         int
	MaxHandshakes          int
	MaxHandshakesPerSource int
	// Transaction pooling (pool.go): PoolMaxServers caps each database's
	// pooled server connections (default 10, of a native database's 25);
	// PoolWait bounds a transaction's wait for one (default 30 s);
	// PoolServerIdle closes servers idle that long (default 60 s), so a
	// database nobody uses can park.
	PoolMaxServers int
	PoolWait       time.Duration
	PoolServerIdle time.Duration
	// Query cancellation (cancel.go): InstanceID tags the cancel keys this
	// proxy issues; CancelPeers are the other instances' listeners by id,
	// reached over the overlay; CancelTimeout bounds one cancel (default
	// 5 s).
	InstanceID    uint8
	CancelPeers   map[uint8]string
	CancelTimeout time.Duration
	// Metrics records session outcomes; nil records nothing.
	Metrics *Metrics
}

// Proxy serves PostgreSQL clients.
type Proxy struct {
	cfg    Config
	creds  Credentials
	waker  Waker
	dialer SessionDialer
	log    *slog.Logger
	tlsCfg *tls.Config

	mu          sync.Mutex
	open        int
	handshaking map[string]int // by source address
	handshakes  int

	pools *poolSet

	cancelMu sync.Mutex
	cancels  map[cancelKey]canceler
}

// New validates cfg and builds a Proxy.
func New(cfg Config, creds Credentials, waker Waker, dialer SessionDialer, log *slog.Logger) (*Proxy, error) {
	if cfg.TLS == nil || cfg.HostSuffix == "" || len(cfg.DecoySecret) < 32 || creds == nil || waker == nil || dialer == nil {
		return nil, errors.New("pgproxy: incomplete config")
	}
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = 15 * time.Second
	}
	if cfg.WakeTimeout <= 0 {
		cfg.WakeTimeout = time.Minute
	}
	if cfg.MaxConnections <= 0 {
		cfg.MaxConnections = 10000
	}
	if cfg.MaxHandshakes <= 0 {
		cfg.MaxHandshakes = 512
	}
	if cfg.MaxHandshakesPerSource <= 0 {
		cfg.MaxHandshakesPerSource = 32
	}
	if cfg.TouchInterval <= 0 {
		cfg.TouchInterval = 30 * time.Second
	}
	if cfg.PoolMaxServers <= 0 {
		cfg.PoolMaxServers = 10
	}
	if cfg.PoolWait <= 0 {
		cfg.PoolWait = 30 * time.Second
	}
	if cfg.PoolServerIdle <= 0 {
		cfg.PoolServerIdle = time.Minute
	}
	if cfg.CancelTimeout <= 0 {
		cfg.CancelTimeout = defaultCancelTimeout
	}
	if _, self := cfg.CancelPeers[cfg.InstanceID]; self {
		return nil, errors.New("pgproxy: cancel_peers lists this instance")
	}
	if log == nil {
		log = slog.Default()
	}
	tlsCfg := cfg.TLS.Clone()
	if tlsCfg.MinVersion < tls.VersionTLS12 {
		tlsCfg.MinVersion = tls.VersionTLS12
	}
	// PostgreSQL 17 direct TLS requires the "postgresql" ALPN protocol.
	tlsCfg.NextProtos = []string{"postgresql"}
	cfg.HostSuffix = strings.ToLower(strings.TrimPrefix(cfg.HostSuffix, "."))
	proxy := &Proxy{cfg: cfg, creds: creds, waker: waker, dialer: dialer, log: log, tlsCfg: tlsCfg, handshaking: map[string]int{}, cancels: map[cancelKey]canceler{}}
	proxy.pools = newPoolSet(proxy)
	return proxy, nil
}

// Serve accepts clients until ln closes.
func (p *Proxy) Serve(ctx context.Context, ln net.Listener) error {
	poolCtx, stopPools := context.WithCancel(ctx)
	defer stopPools()
	go p.pools.maintain(poolCtx)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go p.handle(ctx, conn)
	}
}

// fatal is a client-visible PostgreSQL error.
type fatal struct {
	code, msg string
}

func (f *fatal) Error() string { return f.code + ": " + f.msg }

func (p *Proxy) handle(ctx context.Context, raw net.Conn) {
	defer func() { _ = raw.Close() }()
	source := sourceOf(raw)
	if !p.admit(source) {
		p.cfg.Metrics.result("limit")
		return
	}
	defer p.closed()
	authenticated := false
	defer func() {
		if !authenticated {
			p.authenticated(source)
		}
	}()
	_ = raw.SetDeadline(time.Now().Add(p.cfg.HandshakeTimeout))
	conn, err := p.negotiateTLS(ctx, raw)
	if errors.Is(err, errCancelServed) {
		return
	}
	if err != nil {
		p.cfg.Metrics.result("tls")
		return
	}
	defer func() { _ = conn.Close() }()
	client := pgproto3.NewBackend(conn, conn)
	est, err := p.establish(ctx, conn, client)
	if errors.Is(err, errCancelServed) {
		return
	}
	databaseID := est.databaseID
	if err != nil {
		result := "error"
		var f *fatal
		if errors.As(err, &f) {
			client.Send(&pgproto3.ErrorResponse{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: f.code, Message: f.msg})
			_ = client.Flush()
			result = f.code
		}
		p.cfg.Metrics.result(result)
		p.log.Info("pgproxy: session refused", "database_id", databaseID, "err", err)
		return
	}
	authenticated = true
	p.authenticated(source)
	p.cfg.Metrics.result("ok")
	p.cfg.Metrics.sessionStarted()
	defer p.cfg.Metrics.sessionEnded()
	_ = conn.SetDeadline(time.Time{})
	defer est.release()
	if est.pool != nil {
		p.servePooled(ctx, conn, client, est.pool, est.canceler)
		return
	}
	defer func() { _ = est.session.Close() }()
	p.splice(ctx, conn, est.session, databaseID)
}

// errCancelServed ends a connection that carried a cancel request.
var errCancelServed = errors.New("pgproxy: cancel request served")

// established is a client ready for its session: a backend session to
// splice, or a pool with the client's canceler. release drops the
// client's cancel key.
type established struct {
	session    io.ReadWriteCloser
	pool       *pool
	canceler   *pooledCanceler
	databaseID string
	release    func()
}

// negotiateTLS answers SSLRequest (or accepts direct TLS) and completes the
// handshake. Plaintext clients are refused: credentials only travel inside
// TLS. A plaintext cancel request carries none and is served
// (errCancelServed).
func (p *Proxy) negotiateTLS(ctx context.Context, raw net.Conn) (*tls.Conn, error) {
	br := bufio.NewReader(raw)
	pc := &prefixConn{Conn: raw, r: br}
	for {
		first, err := br.Peek(1)
		if err != nil {
			return nil, err
		}
		if first[0] == tlsHandshakeByte {
			return p.handshake(pc)
		}
		hdr := make([]byte, 8)
		if _, err := io.ReadFull(br, hdr); err != nil {
			return nil, err
		}
		switch code := binary.BigEndian.Uint32(hdr[4:]); code {
		case sslRequestCode:
			if _, err := raw.Write([]byte{'S'}); err != nil {
				return nil, err
			}
			return p.handshake(pc)
		case gssEncRequestCode:
			if _, err := raw.Write([]byte{'N'}); err != nil {
				return nil, err
			}
		case cancelRequestCode, forwardedCancelCode:
			pid, secret, err := readCancelBody(br, binary.BigEndian.Uint32(hdr[:4]))
			if err != nil {
				return nil, err
			}
			p.handleCancel(ctx, pid, secret, code == forwardedCancelCode)
			return nil, errCancelServed
		default:
			resp := (&pgproto3.ErrorResponse{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "08P01", Message: "TLS is required: connect with sslmode=require or stronger"})
			if buf, err := resp.Encode(nil); err == nil {
				_, _ = raw.Write(buf)
			}
			return nil, errors.New("pgproxy: plaintext client refused")
		}
	}
}

func (p *Proxy) handshake(c net.Conn) (*tls.Conn, error) {
	conn := tls.Server(c, p.tlsCfg)
	if err := conn.Handshake(); err != nil {
		return nil, err
	}
	return conn, nil
}

// prefixConn replays bytes already buffered while sniffing the request.
type prefixConn struct {
	net.Conn
	r io.Reader
}

func (c *prefixConn) Read(b []byte) (int, error) { return c.r.Read(b) }

// establish authenticates the client, then either wakes the database and
// logs in to it, returning a session ready for splicing, or, on a pooled
// endpoint, returns the role's pool with the client logged in to it. Either
// way the client gets a cancel key of its own. A cancel request sent
// after TLS is served (errCancelServed).
func (p *Proxy) establish(ctx context.Context, conn *tls.Conn, client *pgproto3.Backend) (established, error) {
	none := established{release: func() {}}
	msg, err := client.ReceiveStartupMessage()
	if err != nil {
		return none, err
	}
	if c, ok := msg.(*pgproto3.CancelRequest); ok {
		p.handleCancel(ctx, c.ProcessID, c.SecretKey, false)
		return none, errCancelServed
	}
	startup, ok := msg.(*pgproto3.StartupMessage)
	if !ok {
		return none, &fatal{"08P01", "expected a startup message"}
	}
	user := startup.Parameters["user"]
	databaseID, pooled, err := p.route(conn.ConnectionState().ServerName, startup.Parameters["options"])
	if err != nil {
		return none, &fatal{"08004", err.Error()}
	}
	none.databaseID = databaseID
	var poolParams map[string]string
	if pooled {
		if poolParams, err = pooledParams(startup.Parameters); err != nil {
			return none, &fatal{"08P01", fmt.Sprintf("the pooled endpoint does not support startup parameter %v; use the direct endpoint", err)}
		}
	}
	verifier := p.verifier(ctx, databaseID, user)
	clientKey, serverFinal, err := authenticateClient(client, verifier)
	if err != nil {
		p.creds.Forget(databaseID, user)
		return none, &fatal{"28P01", fmt.Sprintf("password authentication failed for user %q", user)}
	}
	if pooled {
		pl, err := p.joinPool(ctx, poolKey{databaseID: databaseID, role: user, dbname: poolParams["database"], verifier: verifierKey(verifier)},
			clientKey, verifier.ServerKey, poolParams)
		if err != nil {
			return none, err
		}
		pl.mu.Lock()
		status := pl.status
		pl.mu.Unlock()
		c := &pooledCanceler{p: p}
		key, release := p.register(c)
		if err := greetPooled(client, conn, serverFinal, status, key); err != nil {
			release()
			pl.leave()
			return none, err
		}
		return established{pool: pl, canceler: c, databaseID: databaseID, release: release}, nil
	}
	wakeCtx, cancel := context.WithTimeout(ctx, p.cfg.WakeTimeout)
	defer cancel()
	wakeStarted := time.Now()
	target, err := p.waker.Wake(wakeCtx, databaseID)
	p.cfg.Metrics.woke(time.Since(wakeStarted))
	if err != nil {
		return none, &fatal{"57P03", "the database is starting or unavailable; retry shortly"}
	}
	session, err := p.dialer.Dial(wakeCtx, target, databaseID)
	if err != nil {
		return none, &fatal{"57P03", "the database is starting or unavailable; retry shortly"}
	}
	backend := pgproto3.NewFrontend(session, session)
	if err := loginBackend(backend, forwardParams(startup.Parameters), clientKey, verifier.ServerKey); err != nil {
		_ = session.Close()
		var f *fatal
		if errors.As(err, &f) {
			if f.code == "28P01" {
				p.creds.Forget(databaseID, user)
			}
			return none, f
		}
		return none, &fatal{"57P03", "the database is starting or unavailable; retry shortly"}
	}
	client.Send(&pgproto3.AuthenticationSASLFinal{Data: serverFinal})
	client.Send(&pgproto3.AuthenticationOk{})
	release := func() {}
	// The client gets its own key in place of the backend's; a cancel with
	// it reaches this backend through the node's vmmd.
	swapKey := func(k *pgproto3.BackendKeyData) *pgproto3.BackendKeyData {
		key, drop := p.register(directCanceler{p: p, key: backendKey{target: target, databaseID: databaseID, pid: k.ProcessID, secret: append([]byte(nil), k.SecretKey...)}})
		release = drop
		return key
	}
	if err := relayUntilReady(backend, client, swapKey); err != nil {
		release()
		_ = session.Close()
		return none, err
	}
	return established{session: session, databaseID: databaseID, release: release}, nil
}

// joinPool counts the client into its role's pool. A new pool logs a first
// server in, which wakes the database and proves the role may log in, and
// keeps the server's parameters for its clients.
func (p *Proxy) joinPool(ctx context.Context, key poolKey, clientKey, serverKey []byte, params map[string]string) (*pool, error) {
	pl := p.pools.join(key, clientKey, serverKey, params)
	pl.mu.Lock()
	ready := pl.status != nil
	pl.mu.Unlock()
	if ready {
		return pl, nil
	}
	srv, err := pl.acquire(ctx)
	if err != nil {
		pl.leave()
		var f *fatal
		if errors.As(err, &f) {
			if f.code == "28P01" {
				p.creds.Forget(key.databaseID, key.role)
			}
			return nil, f
		}
		return nil, &fatal{"57P03", "the database is starting or unavailable; retry shortly"}
	}
	pl.put(srv, true)
	return pl, nil
}

// route finds the database id in the SNI name, or in an "endpoint=<id>"
// startup option for clients that send no SNI. An id ending in -pooler
// names the database's pooled endpoint.
func (p *Proxy) route(sni, options string) (string, bool, error) {
	parse := func(label string) (string, bool, bool) {
		label = strings.ToLower(label)
		id, pooled := strings.CutSuffix(label, pooledSuffix)
		if _, err := uuid.Parse(id); err != nil || len(id) != 36 {
			return "", false, false
		}
		return id, pooled, true
	}
	if label, ok := strings.CutSuffix(strings.ToLower(sni), "."+p.cfg.HostSuffix); ok {
		if id, pooled, ok := parse(label); ok {
			return id, pooled, nil
		}
	}
	for _, field := range strings.Fields(options) {
		if label, ok := strings.CutPrefix(field, "endpoint="); ok {
			if id, pooled, ok := parse(label); ok {
				return id, pooled, nil
			}
		}
	}
	return "", false, fmt.Errorf("unknown database endpoint: connect to <database-id>.%s with TLS", p.cfg.HostSuffix)
}

// verifier returns the role's verifier, or a decoy that no password
// matches, so an unknown database or role fails exactly like a wrong
// password and never wakes anything.
func (p *Proxy) verifier(ctx context.Context, databaseID, role string) pgscram.Verifier {
	v, err := p.creds.Verifier(ctx, databaseID, role)
	if err == nil {
		return v
	}
	if !errors.Is(err, ErrNoVerifier) {
		p.log.Warn("pgproxy: verifier lookup failed", "database_id", databaseID, "err", err)
	}
	mac := func(label string) []byte {
		h := hmac.New(sha256.New, p.cfg.DecoySecret)
		h.Write([]byte(label + "\x00" + databaseID + "\x00" + role))
		return h.Sum(nil)
	}
	return pgscram.Verifier{Iterations: pgscram.DefaultIterations, Salt: mac("salt")[:16], StoredKey: mac("stored"), ServerKey: mac("server")}
}

// authenticateClient runs the server side of SCRAM with the client and
// returns the proven ClientKey and the server-final message, which is sent
// only after the backend accepts the login.
func authenticateClient(client *pgproto3.Backend, v pgscram.Verifier) ([]byte, []byte, error) {
	client.Send(&pgproto3.AuthenticationSASL{AuthMechanisms: []string{pgscram.Mechanism}})
	if err := client.Flush(); err != nil {
		return nil, nil, err
	}
	if err := client.SetAuthType(pgproto3.AuthTypeSASL); err != nil {
		return nil, nil, err
	}
	msg, err := client.Receive()
	if err != nil {
		return nil, nil, err
	}
	initial, ok := msg.(*pgproto3.SASLInitialResponse)
	if !ok || initial.AuthMechanism != pgscram.Mechanism {
		return nil, nil, pgscram.ErrMalformed
	}
	session := pgscram.NewServerSession(v)
	serverFirst, err := session.ClientFirst(initial.Data)
	if err != nil {
		return nil, nil, err
	}
	client.Send(&pgproto3.AuthenticationSASLContinue{Data: serverFirst})
	if err := client.Flush(); err != nil {
		return nil, nil, err
	}
	if err := client.SetAuthType(pgproto3.AuthTypeSASLContinue); err != nil {
		return nil, nil, err
	}
	if msg, err = client.Receive(); err != nil {
		return nil, nil, err
	}
	response, ok := msg.(*pgproto3.SASLResponse)
	if !ok {
		return nil, nil, pgscram.ErrMalformed
	}
	serverFinal, err := session.ClientFinal(response.Data)
	if err != nil {
		return nil, nil, err
	}
	return session.ClientKey(), serverFinal, nil
}

// forwardParams passes the client's startup parameters on, minus the
// routing option the proxy consumed.
func forwardParams(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if k == "options" {
			var kept []string
			for _, field := range strings.Fields(v) {
				if !strings.HasPrefix(field, "endpoint=") {
					kept = append(kept, field)
				}
			}
			if len(kept) == 0 {
				continue
			}
			v = strings.Join(kept, " ")
		}
		out[k] = v
	}
	return out
}

// loginBackend logs in to PostgreSQL with the client's proven key.
func loginBackend(backend *pgproto3.Frontend, params map[string]string, clientKey, serverKey []byte) error {
	backend.Send(&pgproto3.StartupMessage{ProtocolVersion: pgproto3.ProtocolVersionNumber, Parameters: params})
	if err := backend.Flush(); err != nil {
		return err
	}
	session, err := pgscram.NewClientSession(clientKey, serverKey)
	if err != nil {
		return err
	}
	for {
		msg, err := backend.Receive()
		if err != nil {
			return err
		}
		switch m := msg.(type) {
		case *pgproto3.AuthenticationSASL:
			backend.Send(&pgproto3.SASLInitialResponse{AuthMechanism: pgscram.Mechanism, Data: session.ClientFirst()})
		case *pgproto3.AuthenticationSASLContinue:
			final, err := session.ServerFirst(m.Data)
			if err != nil {
				return err
			}
			backend.Send(&pgproto3.SASLResponse{Data: final})
		case *pgproto3.AuthenticationSASLFinal:
			if err := session.ServerFinal(m.Data); err != nil {
				return err
			}
			continue
		case *pgproto3.AuthenticationOk:
			return nil
		case *pgproto3.ErrorResponse:
			// The client is authenticated, so PostgreSQL's own error (for
			// example an unknown database name) can reach it.
			return &fatal{m.Code, m.Message}
		default:
			return fmt.Errorf("pgproxy: unexpected %T during backend login", msg)
		}
		if err := backend.Flush(); err != nil {
			return err
		}
	}
}

// relayUntilReady forwards PostgreSQL's post-login messages (parameters,
// backend key, notices) until ReadyForQuery, after which the session is a
// plain byte stream. swapKey replaces the backend's key with the client's.
func relayUntilReady(backend *pgproto3.Frontend, client *pgproto3.Backend, swapKey func(*pgproto3.BackendKeyData) *pgproto3.BackendKeyData) error {
	for {
		msg, err := backend.Receive()
		if err != nil {
			return err
		}
		if e, ok := msg.(*pgproto3.ErrorResponse); ok {
			return &fatal{e.Code, e.Message}
		}
		if k, ok := msg.(*pgproto3.BackendKeyData); ok {
			msg = swapKey(k)
		}
		client.Send(msg)
		if _, ok := msg.(*pgproto3.ReadyForQuery); ok {
			return client.Flush()
		}
	}
}

// splice copies bytes both ways and keeps the database awake while the
// session lasts. Every touch carries the bytes sent to the client since the
// previous one; the last goes out when the session ends.
func (p *Proxy) splice(ctx context.Context, client net.Conn, session io.ReadWriteCloser, databaseID string) {
	done := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(done) }) }
	var egress atomic.Int64
	go func() { _, _ = io.Copy(session, client); finish() }()
	go func() { _, _ = io.Copy(&countingWriter{w: client, n: &egress}, session); finish() }()
	ticker := time.NewTicker(p.cfg.TouchInterval)
	defer ticker.Stop()
	touch := func(ctx context.Context) {
		if err := p.waker.Touch(ctx, databaseID, egress.Swap(0)); err != nil {
			p.log.Warn("pgproxy: touch failed", "database_id", databaseID, "err", err)
		}
	}
	for {
		select {
		case <-done:
		case <-ctx.Done():
		case <-ticker.C:
			touch(ctx)
			continue
		}
		_ = session.Close()
		_ = client.Close()
		if egress.Load() > 0 {
			final, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			touch(final)
			cancel()
		}
		return
	}
}

// countingWriter counts the bytes written through it.
type countingWriter struct {
	w io.Writer
	n *atomic.Int64
}

func (c *countingWriter) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	c.n.Add(int64(n))
	return n, err
}

// admit counts a new connection from source against the caps.
func (p *Proxy) admit(source string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.open >= p.cfg.MaxConnections || p.handshakes >= p.cfg.MaxHandshakes || p.handshaking[source] >= p.cfg.MaxHandshakesPerSource {
		return false
	}
	p.open++
	p.handshakes++
	p.handshaking[source]++
	return true
}

// authenticated ends a connection's handshake phase.
func (p *Proxy) authenticated(source string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.handshakes--
	if p.handshaking[source]--; p.handshaking[source] <= 0 {
		delete(p.handshaking, source)
	}
}

func (p *Proxy) closed() {
	p.mu.Lock()
	p.open--
	p.mu.Unlock()
}

// sourceOf is a connection's remote address without its port.
func sourceOf(c net.Conn) string {
	host, _, err := net.SplitHostPort(c.RemoteAddr().String())
	if err != nil {
		return c.RemoteAddr().String()
	}
	return host
}
