package main

// Guest-side transport pooling for the per-instance stream bridge.
//
// The bridge process already has per-instance lifetime. Keeping the guest
// transport with that same lifetime removes a TCP connect and, for H2C, the
// HTTP/2 preface + SETTINGS exchange from every request. H1 and H2C use
// separate transports because they have different wire contracts, while a
// port-keyed entry keeps deployments that override the guest port isolated.

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/net/http2"
)

const (
	// guestTransportMaxPorts bounds the number of distinct guest ports that a
	// single bridge can retain. Ports are normally fixed for an instance; the
	// bound prevents malformed or future metadata from retaining an unbounded
	// number of transport objects. Eviction only closes idle connections, so
	// in-flight requests remain safe.
	guestTransportMaxPorts = 8

	guestTransportMaxIdleConnsPerHost = 32

	// h1IdleConnTimeout bounds an idle keep-alive connection to an H1 guest.
	// It is shorter than the bridge's process lifetime so parked instances do
	// not retain guest-side sockets until the outer bridge reaper runs.
	h1IdleConnTimeout = 30 * time.Second

	// The bridge is started as soon as vmmd creates the instance network,
	// before Firecracker has finished restoring the guest. Retry the H2C
	// handshake during that overlap so the first customer request can reuse a
	// live connection instead of paying for TCP + SETTINGS after the wake.
	guestH2CPrewarmTimeout = 5 * time.Second
	guestH2CPrewarmRetry   = 5 * time.Millisecond
)

// guestH2CRoundTripper owns the connection established by the bridge's
// background prewarm. http2.Transport.NewClientConn writes the client preface
// and SETTINGS immediately; Ping proves the guest has consumed them without
// invoking a customer handler. Once published, ClientConn is safe for
// concurrent RoundTrip calls.
type guestH2CRoundTripper struct {
	base    *http2.Transport
	guestIP string
	port    uint16

	mu      sync.RWMutex
	prewarm *http2.ClientConn
	// prewarmDone is non-nil while the restore-time H2C handshake is in
	// progress. The first customer request waits on it instead of opening a
	// second connection and racing the handshake that is already underway.
	prewarmDone chan struct{}
	prewarming  bool
	closed      bool
}

func newGuestH2CRoundTripper(guestIP string, port uint16) *guestH2CRoundTripper {
	base := newGuestH2CTransportFn(guestIP, port)
	// Initialize x/net/http2's transport adapter before NewClientConn. This is
	// a no-op for connections, but Go 1.27's stdlib-backed implementation
	// otherwise has no underlying net/http transport registered yet.
	base.CloseIdleConnections()
	return &guestH2CRoundTripper{
		base:    base,
		guestIP: guestIP,
		port:    port,
	}
}

func (t *guestH2CRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	for {
		t.mu.RLock()
		conn := t.prewarm
		prewarming := t.prewarming
		prewarmDone := t.prewarmDone
		closed := t.closed
		t.mu.RUnlock()

		if conn != nil && conn.CanTakeNewRequest() {
			resp, err := conn.RoundTrip(req)
			if err != nil && !conn.CanTakeNewRequest() {
				t.mu.Lock()
				if t.prewarm == conn {
					t.prewarm = nil
				}
				t.mu.Unlock()
			}
			return resp, err
		}
		if !prewarming || prewarmDone == nil || closed {
			return t.base.RoundTrip(req)
		}

		select {
		case <-prewarmDone:
			// Re-read the published connection under the lock. A failed
			// prewarm falls through to the normal transport on the next loop.
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
}

func (t *guestH2CRoundTripper) prewarmConnection(ctx context.Context) error {
	if t == nil {
		return nil
	}

	for {
		t.mu.Lock()
		if t.closed {
			t.mu.Unlock()
			return net.ErrClosed
		}
		if t.prewarm != nil && t.prewarm.CanTakeNewRequest() {
			t.mu.Unlock()
			return nil
		}
		if t.prewarming {
			prewarmDone := t.prewarmDone
			t.mu.Unlock()
			select {
			case <-prewarmDone:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		t.prewarming = true
		t.prewarmDone = make(chan struct{})
		t.mu.Unlock()
		break
	}

	var warmed *http2.ClientConn
	defer func() { t.finishPrewarm(warmed) }()
	for {
		dialer := net.Dialer{Timeout: 50 * time.Millisecond}
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(t.guestIP, strconv.FormatUint(uint64(t.port), 10)))
		if err == nil {
			clientConn, connErr := t.base.NewClientConn(conn)
			if connErr == nil {
				connErr = clientConn.Ping(ctx)
			}
			if connErr == nil {
				warmed = clientConn
				return nil
			} else {
				_ = conn.Close()
				err = connErr
			}
		}

		timer := time.NewTimer(guestH2CPrewarmRetry)
		select {
		case <-ctx.Done():
			timer.Stop()
			if err != nil {
				return err
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (t *guestH2CRoundTripper) finishPrewarm(warmed *http2.ClientConn) {
	var closeConn *http2.ClientConn
	var old *http2.ClientConn

	t.mu.Lock()
	if warmed != nil {
		if t.closed {
			closeConn = warmed
		} else if t.prewarm == nil || !t.prewarm.CanTakeNewRequest() {
			old = t.prewarm
			t.prewarm = warmed
		} else {
			closeConn = warmed
		}
	}
	prewarmDone := t.prewarmDone
	if t.prewarming {
		t.prewarming = false
		t.prewarmDone = nil
	} else {
		prewarmDone = nil
	}
	t.mu.Unlock()

	if prewarmDone != nil {
		close(prewarmDone)
	}
	if old != nil {
		_ = old.Close()
	}
	if closeConn != nil {
		_ = closeConn.Close()
	}
}

func (t *guestH2CRoundTripper) closeIdleConnections() {
	if t == nil {
		return
	}
	t.base.CloseIdleConnections()
	t.mu.Lock()
	t.closed = true
	conn := t.prewarm
	t.prewarm = nil
	prewarmDone := t.prewarmDone
	if t.prewarming {
		t.prewarming = false
		t.prewarmDone = nil
	} else {
		prewarmDone = nil
	}
	t.mu.Unlock()
	if prewarmDone != nil {
		close(prewarmDone)
	}
	if conn != nil {
		_ = conn.Close()
	}
}

type guestTransportEntry struct {
	port     uint16
	lastUsed time.Time
	h1       *http.Transport
	h2c      *guestH2CRoundTripper
}

type guestTransportPool struct {
	guestIP string

	mu      sync.Mutex
	entries map[uint16]*guestTransportEntry
	now     func() time.Time
	closed  bool
}

func newGuestTransportPool(guestIP string) *guestTransportPool {
	return &guestTransportPool{
		guestIP: guestIP,
		entries: make(map[uint16]*guestTransportEntry),
		now:     time.Now,
	}
}

func (p *guestTransportPool) entryLocked(port uint16) *guestTransportEntry {
	if p == nil {
		return nil
	}
	if p.closed {
		return nil
	}
	if entry := p.entries[port]; entry != nil {
		entry.lastUsed = p.now()
		return entry
	}

	var evicted *guestTransportEntry
	if len(p.entries) >= guestTransportMaxPorts {
		for _, candidate := range p.entries {
			if evicted == nil || candidate.lastUsed.Before(evicted.lastUsed) {
				evicted = candidate
			}
		}
		if evicted != nil {
			delete(p.entries, evicted.port)
			evicted.closeIdleConnections()
		}
	}

	entry := &guestTransportEntry{port: port, lastUsed: p.now()}
	p.entries[port] = entry
	return entry
}

func (p *guestTransportPool) h2c(port uint16) http.RoundTripper {
	if p == nil {
		return newGuestH2CTransportFn("", port)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := p.entryLocked(port)
	if entry == nil {
		return newGuestH2CTransportFn(p.guestIP, port)
	}
	if entry.h2c == nil {
		entry.h2c = newGuestH2CRoundTripper(p.guestIP, port)
	}
	return entry.h2c
}

func (p *guestTransportPool) prewarmH2C(ctx context.Context, port uint16) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	entry := p.entryLocked(port)
	if entry == nil {
		p.mu.Unlock()
		return nil
	}
	if entry.h2c == nil {
		entry.h2c = newGuestH2CRoundTripper(p.guestIP, port)
	}
	transport := entry.h2c
	p.mu.Unlock()
	return transport.prewarmConnection(ctx)
}

func (p *guestTransportPool) h1(port uint16) *http.Transport {
	if p == nil {
		return newGuestH1Transport("", port)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := p.entryLocked(port)
	if entry == nil {
		return newGuestH1Transport(p.guestIP, port)
	}
	if entry.h1 == nil {
		entry.h1 = newGuestH1Transport(p.guestIP, port)
	}
	return entry.h1
}

func (p *guestTransportPool) closeIdleConnections() {
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	entries := make([]*guestTransportEntry, 0, len(p.entries))
	for port, entry := range p.entries {
		delete(p.entries, port)
		entries = append(entries, entry)
	}
	for _, entry := range entries {
		entry.closeIdleConnections()
	}
	p.mu.Unlock()
}

func (e *guestTransportEntry) closeIdleConnections() {
	if e == nil {
		return
	}
	if e.h1 != nil {
		e.h1.CloseIdleConnections()
	}
	if e.h2c != nil {
		e.h2c.closeIdleConnections()
	}
}

// newGuestH1Transport is the keep-alive H1 counterpart to
// newGuestH2CTransport. The custom dialer pins the destination to the guest
// namespace address; the URL host is still used by net/http for Host and
// transport-pool identity.
func newGuestH1Transport(guestIP string, guestPort uint16) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: dialTimeout}
			return d.DialContext(ctx, "tcp", net.JoinHostPort(guestIP, strconv.FormatUint(uint64(guestPort), 10)))
		},
		DisableCompression:    true,
		MaxIdleConns:          guestTransportMaxIdleConnsPerHost,
		MaxIdleConnsPerHost:   guestTransportMaxIdleConnsPerHost,
		IdleConnTimeout:       h1IdleConnTimeout,
		ResponseHeaderTimeout: readHeaderTimeout,
		ExpectContinueTimeout: time.Second,
	}
}
