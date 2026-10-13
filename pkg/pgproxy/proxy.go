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
	if cfg.TouchInterval <= 0 {
		cfg.TouchInterval = 30 * time.Second
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
	return &Proxy{cfg: cfg, creds: creds, waker: waker, dialer: dialer, log: log, tlsCfg: tlsCfg}, nil
}

// Serve accepts clients until ln closes.
func (p *Proxy) Serve(ctx context.Context, ln net.Listener) error {
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
	_ = raw.SetDeadline(time.Now().Add(p.cfg.HandshakeTimeout))
	conn, err := p.negotiateTLS(raw)
	if err != nil {
		p.cfg.Metrics.result("tls")
		return
	}
	defer func() { _ = conn.Close() }()
	client := pgproto3.NewBackend(conn, conn)
	session, databaseID, err := p.establish(ctx, conn, client)
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
	defer func() { _ = session.Close() }()
	p.cfg.Metrics.result("ok")
	p.cfg.Metrics.sessionStarted()
	defer p.cfg.Metrics.sessionEnded()
	_ = conn.SetDeadline(time.Time{})
	p.splice(ctx, conn, session, databaseID)
}

// negotiateTLS answers SSLRequest (or accepts direct TLS) and completes the
// handshake. Plaintext clients are refused: credentials only travel inside
// TLS.
func (p *Proxy) negotiateTLS(raw net.Conn) (*tls.Conn, error) {
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
		switch binary.BigEndian.Uint32(hdr[4:]) {
		case sslRequestCode:
			if _, err := raw.Write([]byte{'S'}); err != nil {
				return nil, err
			}
			return p.handshake(pc)
		case gssEncRequestCode:
			if _, err := raw.Write([]byte{'N'}); err != nil {
				return nil, err
			}
		case cancelRequestCode:
			return nil, errors.New("pgproxy: cancel requests are not supported")
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

// establish authenticates the client, wakes the database and logs in to
// it, returning a session ready for splicing.
func (p *Proxy) establish(ctx context.Context, conn *tls.Conn, client *pgproto3.Backend) (io.ReadWriteCloser, string, error) {
	msg, err := client.ReceiveStartupMessage()
	if err != nil {
		return nil, "", err
	}
	startup, ok := msg.(*pgproto3.StartupMessage)
	if !ok {
		return nil, "", &fatal{"08P01", "expected a startup message"}
	}
	user := startup.Parameters["user"]
	databaseID, err := p.route(conn.ConnectionState().ServerName, startup.Parameters["options"])
	if err != nil {
		return nil, "", &fatal{"08004", err.Error()}
	}
	verifier := p.verifier(ctx, databaseID, user)
	clientKey, serverFinal, err := authenticateClient(client, verifier)
	if err != nil {
		p.creds.Forget(databaseID, user)
		return nil, databaseID, &fatal{"28P01", fmt.Sprintf("password authentication failed for user %q", user)}
	}
	wakeCtx, cancel := context.WithTimeout(ctx, p.cfg.WakeTimeout)
	defer cancel()
	wakeStarted := time.Now()
	target, err := p.waker.Wake(wakeCtx, databaseID)
	p.cfg.Metrics.woke(time.Since(wakeStarted))
	if err != nil {
		return nil, databaseID, &fatal{"57P03", "the database is starting or unavailable; retry shortly"}
	}
	session, err := p.dialer.Dial(wakeCtx, target, databaseID)
	if err != nil {
		return nil, databaseID, &fatal{"57P03", "the database is starting or unavailable; retry shortly"}
	}
	backend := pgproto3.NewFrontend(session, session)
	if err := loginBackend(backend, forwardParams(startup.Parameters), clientKey, verifier.ServerKey); err != nil {
		_ = session.Close()
		var f *fatal
		if errors.As(err, &f) {
			if f.code == "28P01" {
				p.creds.Forget(databaseID, user)
			}
			return nil, databaseID, f
		}
		return nil, databaseID, &fatal{"57P03", "the database is starting or unavailable; retry shortly"}
	}
	client.Send(&pgproto3.AuthenticationSASLFinal{Data: serverFinal})
	client.Send(&pgproto3.AuthenticationOk{})
	if err := relayUntilReady(backend, client); err != nil {
		_ = session.Close()
		return nil, databaseID, err
	}
	return session, databaseID, nil
}

// route finds the database id in the SNI name, or in an "endpoint=<id>"
// startup option for clients that send no SNI.
func (p *Proxy) route(sni, options string) (string, error) {
	if id, ok := strings.CutSuffix(strings.ToLower(sni), "."+p.cfg.HostSuffix); ok {
		if _, err := uuid.Parse(id); err == nil {
			return id, nil
		}
	}
	for _, field := range strings.Fields(options) {
		if id, ok := strings.CutPrefix(field, "endpoint="); ok {
			if _, err := uuid.Parse(id); err == nil {
				return strings.ToLower(id), nil
			}
		}
	}
	return "", fmt.Errorf("unknown database endpoint: connect to <database-id>.%s with TLS", p.cfg.HostSuffix)
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
// plain byte stream.
func relayUntilReady(backend *pgproto3.Frontend, client *pgproto3.Backend) error {
	for {
		msg, err := backend.Receive()
		if err != nil {
			return err
		}
		if e, ok := msg.(*pgproto3.ErrorResponse); ok {
			return &fatal{e.Code, e.Message}
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
