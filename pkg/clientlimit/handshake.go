package clientlimit

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/netip"
	"sync"
	"time"
)

// HandshakeGate completes TLS handshakes before the HTTP server sees a
// connection, at most max at a time. A connection that arrives while every
// slot is taken is reset rather than queued, so a handshake flood costs the
// node a bounded share of its CPU and requests on open connections keep
// running. Accept returns the *tls.Conn itself, handshake done, so the server
// still treats it as TLS and reads the negotiated protocol.
type HandshakeGate struct {
	inner   net.Listener
	slots   chan struct{}
	timeout time.Duration
	refused func(reason string)
	failed  func(remote net.Addr, err error)

	ready     chan net.Conn
	errs      chan error
	done      chan struct{}
	closeOnce sync.Once
	// ctx and startOnce start the accept loop on the first Accept, so every
	// With* option set before it happens-before the loop reads it.
	ctx       context.Context
	startOnce sync.Once

	// settle, when set, decides a connection whose handshake completed:
	// false resets it instead of serving it (WithSettle).
	settle func(*tls.Conn) bool

	// helloTimeout, when set, is how long a connection may take to send
	// its ClientHello (WithClientHelloTimeout).
	helloTimeout time.Duration

	// perAddr and perProxy cap one client address's handshakes in
	// progress, proxies' addresses getting perProxy; zero is no cap
	// (WithPerAddress). inflight counts them by Key.
	perAddr, perProxy int
	proxies           []netip.Prefix
	mu                sync.Mutex
	inflight          map[netip.Prefix]int
}

// WithPerAddress caps the handshakes one client address (an IPv4 address or
// an IPv6 /64) may have in progress at perAddr, and an address in proxies at
// perProxy, so one address cannot hold every slot with connections that
// never finish (ADR-20261107180000). Connections from proxies count here
// even though the listener's own limits exempt them until they settle. Call
// before the first Accept.
func (g *HandshakeGate) WithPerAddress(perAddr, perProxy int, proxies []netip.Prefix) *HandshakeGate {
	g.perAddr, g.perProxy, g.proxies = perAddr, perProxy, proxies
	g.inflight = map[netip.Prefix]int{}
	return g
}

// WithClientHelloTimeout gives a connection d to send its ClientHello, less
// than the whole handshake's timeout: a connection that sends nothing frees
// its slot sooner. The listener's tls.Config must extend the deadline when
// the ClientHello arrives (ExtendOnClientHello). Call before the first
// Accept.
func (g *HandshakeGate) WithClientHelloTimeout(d time.Duration) *HandshakeGate {
	g.helloTimeout = d
	return g
}

// ExtendOnClientHello returns a copy of cfg whose GetConfigForClient first
// gives the connection full more time to finish its handshake, then defers
// to cfg's own. It pairs with WithClientHelloTimeout.
func ExtendOnClientHello(cfg *tls.Config, full time.Duration) *tls.Config {
	out := cfg.Clone()
	next := cfg.GetConfigForClient
	out.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		if hello.Conn != nil {
			_ = hello.Conn.SetDeadline(time.Now().Add(full))
		}
		if next != nil {
			return next(hello)
		}
		return nil, nil
	}
	return out
}

// claim takes one of addr's handshake places, or reports that addr holds
// all it may.
func (g *HandshakeGate) claim(addr net.Addr) (netip.Prefix, bool) {
	if g.inflight == nil {
		return netip.Prefix{}, true
	}
	key, ok := keyOf(addr)
	if !ok {
		return netip.Prefix{}, true
	}
	limit := g.perAddr
	for _, p := range g.proxies {
		if p.Contains(key.Addr()) {
			limit = g.perProxy
			break
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if limit > 0 && g.inflight[key] >= limit {
		return netip.Prefix{}, false
	}
	g.inflight[key]++
	return key, true
}

func (g *HandshakeGate) unclaim(key netip.Prefix) {
	if g.inflight == nil || !key.IsValid() {
		return
	}
	g.mu.Lock()
	if g.inflight[key]--; g.inflight[key] <= 0 {
		delete(g.inflight, key)
	}
	g.mu.Unlock()
}

// WithSettle makes the gate ask settle about each completed handshake before
// serving the connection; a false answer resets it. Call before the first
// Accept.
func (g *HandshakeGate) WithSettle(settle func(*tls.Conn) bool) *HandshakeGate {
	g.settle = settle
	return g
}

// NewHandshakeGate serves inner, a TLS listener, through at most max
// concurrent handshakes, each bounded by timeout and ended when ctx is.
// refused is called when a connection finds no free slot, failed for each
// handshake that fails.
func NewHandshakeGate(ctx context.Context, inner net.Listener, max int, timeout time.Duration, refused func(reason string), failed func(remote net.Addr, err error)) *HandshakeGate {
	g := &HandshakeGate{
		inner:   inner,
		slots:   make(chan struct{}, max),
		timeout: timeout,
		refused: refused,
		failed:  failed,
		ready:   make(chan net.Conn),
		errs:    make(chan error),
		done:    make(chan struct{}),
		ctx:     ctx,
	}
	return g
}

func (g *HandshakeGate) acceptLoop(ctx context.Context) {
	for {
		c, err := g.inner.Accept()
		if err != nil {
			select {
			case g.errs <- err:
			case <-g.done:
				return
			}
			var temporary interface{ Temporary() bool }
			if errors.As(err, &temporary) && temporary.Temporary() {
				continue
			}
			return
		}
		tc, ok := c.(*tls.Conn)
		if !ok {
			g.deliver(c)
			continue
		}
		key, ok := g.claim(tc.RemoteAddr())
		if !ok {
			g.refused(ReasonHandshakesPerAddress)
			reset(tc)
			continue
		}
		select {
		case g.slots <- struct{}{}:
			go g.handshake(ctx, tc, key)
		default:
			g.unclaim(key)
			g.refused(ReasonHandshakes)
			reset(tc)
		}
	}
}

func (g *HandshakeGate) handshake(ctx context.Context, tc *tls.Conn, key netip.Prefix) {
	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	deadline := g.timeout
	if g.helloTimeout > 0 && g.helloTimeout < deadline {
		deadline = g.helloTimeout
	}
	_ = tc.SetDeadline(time.Now().Add(deadline))
	err := tc.HandshakeContext(ctx)
	cancel()
	<-g.slots
	g.unclaim(key)
	if err != nil {
		g.failed(tc.RemoteAddr(), err)
		_ = tc.Close()
		return
	}
	_ = tc.SetDeadline(time.Time{})
	if g.settle != nil && !g.settle(tc) {
		reset(tc)
		return
	}
	g.deliver(tc)
}

// deliver hands c to Accept, or closes it once the gate is closed.
func (g *HandshakeGate) deliver(c net.Conn) {
	select {
	case g.ready <- c:
	case <-g.done:
		_ = c.Close()
	}
}

// Accept returns the next connection whose handshake completed.
func (g *HandshakeGate) Accept() (net.Conn, error) {
	g.startOnce.Do(func() { go g.acceptLoop(g.ctx) })
	select {
	case c := <-g.ready:
		return c, nil
	case err := <-g.errs:
		return nil, err
	case <-g.done:
		return nil, net.ErrClosed
	}
}

// Close stops accepting; handshakes in progress close their connections.
func (g *HandshakeGate) Close() error {
	g.closeOnce.Do(func() { close(g.done) })
	return g.inner.Close()
}

// Addr is the inner listener's address.
func (g *HandshakeGate) Addr() net.Addr { return g.inner.Addr() }
