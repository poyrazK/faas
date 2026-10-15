package pgproxy

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
)

// Query cancellation (ADR-1027). A client cancels its running query on a
// new connection that carries the key the server sent at login
// (BackendKeyData). pgproxyd gives every client a key of its own and
// keeps, per key, the PostgreSQL backend running that client's queries:
// the session's backend on the direct endpoint, or the server a pooled
// client has borrowed for its transaction. Most drivers send the cancel in
// plaintext; libpq 17 and later send it after TLS. The cancel may reach
// another pgproxyd: the key's top byte names the instance that issued it,
// and a cancel for another instance is forwarded to it over the overlay.
// The answer is always a closed connection, as from PostgreSQL.

// forwardedCancelCode marks a cancel one pgproxyd forwards to the instance
// that issued its key. That instance handles it locally and never forwards
// it again, so a misconfigured peer list cannot loop.
const forwardedCancelCode = 1234<<16 | 5690

// cancelKey is a client's key: ProcessID then the 4-byte secret.
type cancelKey [8]byte

func keyOf(pid uint32, secret []byte) (cancelKey, bool) {
	var k cancelKey
	if len(secret) != 4 {
		return k, false
	}
	binary.BigEndian.PutUint32(k[:4], pid)
	copy(k[4:], secret)
	return k, true
}

// canceler cancels whatever its client is running.
type canceler interface {
	cancel(ctx context.Context) error
}

// backendKey reaches one PostgreSQL backend: the node's vmmd and the key
// the backend sent at login.
type backendKey struct {
	target, databaseID string
	pid                uint32
	secret             []byte
}

// directCanceler cancels a direct session's backend.
type directCanceler struct {
	p   *Proxy
	key backendKey
}

func (d directCanceler) cancel(ctx context.Context) error { return d.p.cancelBackend(ctx, d.key) }

// pooledCanceler cancels the server a pooled client holds. The client
// returns a server only under mu, so a cancel in flight finishes before
// the server can run another client's transaction.
type pooledCanceler struct {
	p   *Proxy
	mu  sync.Mutex
	srv *serverConn
}

func (c *pooledCanceler) cancel(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.srv == nil {
		return nil // between transactions: nothing runs
	}
	return c.p.cancelBackend(ctx, c.srv.key)
}

func (c *pooledCanceler) hold(srv *serverConn) {
	c.mu.Lock()
	c.srv = srv
	c.mu.Unlock()
}

// register gives c a new client key, tagged with this instance.
func (p *Proxy) register(c canceler) (*pgproto3.BackendKeyData, func()) {
	p.cancelMu.Lock()
	defer p.cancelMu.Unlock()
	for {
		var raw [8]byte
		_, _ = rand.Read(raw[:])
		raw[0] = p.cfg.InstanceID
		k := cancelKey(raw)
		if _, taken := p.cancels[k]; taken {
			continue
		}
		p.cancels[k] = c
		key := &pgproto3.BackendKeyData{ProcessID: binary.BigEndian.Uint32(raw[:4]), SecretKey: append([]byte(nil), raw[4:]...)}
		return key, func() {
			p.cancelMu.Lock()
			delete(p.cancels, k)
			p.cancelMu.Unlock()
		}
	}
}

// handleCancel serves one cancel request. A key of another instance is
// forwarded to it unless the request was itself forwarded.
func (p *Proxy) handleCancel(ctx context.Context, pid uint32, secret []byte, forwarded bool) {
	ctx, cancel := context.WithTimeout(ctx, p.cfg.CancelTimeout)
	defer cancel()
	k, ok := keyOf(pid, secret)
	if !ok {
		p.cfg.Metrics.cancelled("unknown")
		return
	}
	if instance := k[0]; instance != p.cfg.InstanceID {
		addr, known := p.cfg.CancelPeers[instance]
		if forwarded || !known {
			p.cfg.Metrics.cancelled("unknown")
			return
		}
		if err := p.forwardCancel(ctx, addr, k); err != nil {
			p.cfg.Metrics.cancelled("failed")
			p.log.Warn("pgproxy: forward cancel failed", "instance", instance, "err", err)
			return
		}
		p.cfg.Metrics.cancelled("forwarded")
		return
	}
	p.cancelMu.Lock()
	c, found := p.cancels[k]
	p.cancelMu.Unlock()
	if !found {
		p.cfg.Metrics.cancelled("unknown")
		return
	}
	if err := c.cancel(ctx); err != nil {
		p.cfg.Metrics.cancelled("failed")
		p.log.Warn("pgproxy: cancel failed", "err", err)
		return
	}
	p.cfg.Metrics.cancelled("ok")
}

// cancelBackend sends PostgreSQL a cancel for the backend over a new
// session stream and waits for PostgreSQL to close it, by which time the
// backend has been signalled. It never wakes a database.
func (p *Proxy) cancelBackend(ctx context.Context, b backendKey) error {
	if b.target == "" || len(b.secret) == 0 {
		return fmt.Errorf("pgproxy: no backend key")
	}
	session, err := p.dialer.Dial(ctx, b.target, b.databaseID)
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = session.Close() })
	defer stop()
	buf, err := (&pgproto3.CancelRequest{ProcessID: b.pid, SecretKey: b.secret}).Encode(nil)
	if err != nil {
		return err
	}
	if _, err := session.Write(buf); err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, session)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// forwardCancel hands a cancel to the instance that issued its key.
func (p *Proxy) forwardCancel(ctx context.Context, addr string, k cancelKey) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	buf := make([]byte, 16)
	binary.BigEndian.PutUint32(buf[0:], 16)
	binary.BigEndian.PutUint32(buf[4:], forwardedCancelCode)
	copy(buf[8:], k[:])
	if _, err := conn.Write(buf); err != nil {
		return err
	}
	// The peer closes once it has handled the cancel.
	_, _ = io.Copy(io.Discard, conn)
	return nil
}

// readCancelBody reads a cancel packet's key after its 8-byte header.
func readCancelBody(r io.Reader, length uint32) (uint32, []byte, error) {
	if length < 12 || length > 264 {
		return 0, nil, fmt.Errorf("pgproxy: cancel request of %d bytes", length)
	}
	body := make([]byte, length-8)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	return binary.BigEndian.Uint32(body), body[4:], nil
}

// defaultCancelTimeout bounds one cancel, forwarding included.
const defaultCancelTimeout = 5 * time.Second
