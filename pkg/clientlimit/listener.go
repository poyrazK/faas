package clientlimit

import (
	"crypto/tls"
	"errors"
	"net"
	"net/netip"
	"sync"
	"time"
)

// Reasons a connection or request is refused, the label values of the
// edge's refusal counter.
const (
	ReasonOpenConnections = "open_connections"
	ReasonConnectionRate  = "connection_rate"
	ReasonHandshakes      = "handshakes"
	ReasonRequestRate     = "request_rate"
	ReasonBodyRate        = "body_rate"
	ReasonForwardInFlight = "forward_in_flight"
	ReasonHostRequestRate = "host_request_rate"
)

// Reasons lists every refusal reason, so a counter can start each at zero.
var Reasons = []string{ReasonOpenConnections, ReasonConnectionRate, ReasonHandshakes, ReasonRequestRate, ReasonBodyRate, ReasonForwardInFlight, ReasonHostRequestRate}

// ConnLimits bound one client address's connections to a listener.
type ConnLimits struct {
	// MaxOpenPerAddr is how many connections one address may hold open.
	MaxOpenPerAddr int
	// NewPerSecond and NewBurst are the token bucket for new connections.
	NewPerSecond float64
	NewBurst     int
	// TrackedAddrsMax bounds the addresses kept in memory.
	TrackedAddrsMax int
	// Exempt are a proxy's addresses, each carrying many clients: their
	// connections are not limited or tracked. The proxy's clients are
	// limited per request by the address it forwards instead.
	//
	// The proxy's ranges also carry traffic the proxy does not vouch for
	// (another customer's zone, its VPN users), so an exempt connection is
	// only provisionally free: Settle decides after the TLS handshake
	// whether it stays exempt or is counted like any client
	// (ADR-20261104221012).
	Exempt []netip.Prefix
}

// Listener refuses a connection, with a TCP reset, when its address already
// holds MaxOpenPerAddr connections or has opened connections faster than its
// bucket allows. It wraps the raw TCP listener, before TLS, so a refused
// client costs no handshake.
type Listener struct {
	net.Listener
	limits  ConnLimits
	table   *table[netip.Prefix]
	refused func(reason string)
}

// NewListener limits inner's connections. refused is called with the reason
// of each refusal.
func NewListener(inner net.Listener, limits ConnLimits, refused func(reason string), now func() time.Time) *Listener {
	return &Listener{
		Listener: inner,
		limits:   limits,
		table:    newTable[netip.Prefix](limits.NewPerSecond, limits.NewBurst, limits.TrackedAddrsMax, now),
		refused:  refused,
	}
}

// Accept returns the next connection within its address's limits, resetting
// the ones that are not.
func (l *Listener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		key, ok := keyOf(c.RemoteAddr())
		if !ok {
			return c, nil
		}
		if l.exempt(key) {
			return &limitedConn{Conn: c, key: key, pending: true}, nil
		}
		e, reason := l.admit(key)
		if reason != "" {
			l.refused(reason)
			reset(c)
			continue
		}
		return &limitedConn{Conn: c, release: func() { l.release(e) }}, nil
	}
}

// exempt reports whether key belongs to an exempt proxy.
func (l *Listener) exempt(key netip.Prefix) bool {
	for _, p := range l.limits.Exempt {
		if p.Contains(key.Addr()) {
			return true
		}
	}
	return false
}

// admit counts a new connection from key, or names the limit it breaks.
func (l *Listener) admit(key netip.Prefix) (*entry, string) {
	t := l.table
	t.mu.Lock()
	defer t.mu.Unlock()
	e, own := t.lookup(key, t.now())
	// The shared overflow entry holds many addresses' connections, so only
	// its rate applies.
	if own && e.open >= l.limits.MaxOpenPerAddr {
		return nil, ReasonOpenConnections
	}
	if e.tokens < 1 {
		return nil, ReasonConnectionRate
	}
	e.tokens--
	e.open++
	return e, ""
}

func (l *Listener) release(e *entry) {
	l.table.mu.Lock()
	e.open--
	l.table.mu.Unlock()
}

// Settle decides a connection from an exempt address once its TLS handshake
// is done. vouched reports that the proxy proved itself (it presented the
// client certificate only our zone holds): the connection stays exempt.
// Otherwise it is admitted against its address's limits now, as if it had
// just arrived; the returned reason names the limit it breaks, and the caller
// refuses it. c is the raw connection under TLS (tls.Conn.NetConn). A
// connection Accept already limited, or one from an untracked address,
// passes unchanged.
func (l *Listener) Settle(c net.Conn, vouched bool) (reason string, ok bool) {
	lc, isLimited := c.(*limitedConn)
	if !isLimited {
		return "", true
	}
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if !lc.pending {
		return "", true
	}
	lc.pending = false
	if vouched {
		return "", true
	}
	e, reason := l.admit(lc.key)
	if reason != "" {
		return reason, false
	}
	lc.release = func() { l.release(e) }
	return "", true
}

// limitedConn gives its address's slot back when it closes. A pending
// connection is from an exempt address and holds no slot until Settle.
type limitedConn struct {
	net.Conn
	once    sync.Once
	mu      sync.Mutex
	key     netip.Prefix
	pending bool
	release func()
}

func (c *limitedConn) Close() error {
	c.once.Do(func() {
		c.mu.Lock()
		release := c.release
		c.mu.Unlock()
		if release != nil {
			release()
		}
	})
	return c.Conn.Close()
}

// SetLinger reaches the TCP connection, so a refusal further up can reset it.
func (c *limitedConn) SetLinger(sec int) error {
	if l, ok := c.Conn.(interface{ SetLinger(int) error }); ok {
		return l.SetLinger(sec)
	}
	return errors.ErrUnsupported
}

// reset closes c with a TCP reset rather than a FIN: the client learns at
// once, and the node keeps no TIME_WAIT for it.
func reset(c net.Conn) {
	if tc, ok := c.(*tls.Conn); ok {
		c = tc.NetConn()
	}
	if l, ok := c.(interface{ SetLinger(int) error }); ok {
		_ = l.SetLinger(0)
	}
	_ = c.Close()
}
