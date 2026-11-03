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
)

// Reasons lists every refusal reason, so a counter can start each at zero.
var Reasons = []string{ReasonOpenConnections, ReasonConnectionRate, ReasonHandshakes, ReasonRequestRate}

// ConnLimits bound one client address's connections to a listener.
type ConnLimits struct {
	// MaxOpenPerAddr is how many connections one address may hold open.
	MaxOpenPerAddr int
	// NewPerSecond and NewBurst are the token bucket for new connections.
	NewPerSecond float64
	NewBurst     int
	// TrackedAddrsMax bounds the addresses kept in memory.
	TrackedAddrsMax int
}

// Listener refuses a connection, with a TCP reset, when its address already
// holds MaxOpenPerAddr connections or has opened connections faster than its
// bucket allows. It wraps the raw TCP listener, before TLS, so a refused
// client costs no handshake.
type Listener struct {
	net.Listener
	limits  ConnLimits
	table   *table
	refused func(reason string)
}

// NewListener limits inner's connections. refused is called with the reason
// of each refusal.
func NewListener(inner net.Listener, limits ConnLimits, refused func(reason string), now func() time.Time) *Listener {
	return &Listener{
		Listener: inner,
		limits:   limits,
		table:    newTable(limits.NewPerSecond, limits.NewBurst, limits.TrackedAddrsMax, now),
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
		e, reason := l.admit(key)
		if reason != "" {
			l.refused(reason)
			reset(c)
			continue
		}
		return &limitedConn{Conn: c, release: func() { l.release(e) }}, nil
	}
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

// limitedConn gives its address's slot back when it closes.
type limitedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitedConn) Close() error {
	c.once.Do(c.release)
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
