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
	"os"
	"strconv"
	"sync"
	"time"

	"golang.org/x/net/http2"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bridgecompletion"
	"github.com/onebox-faas/faas/pkg/netns"
)

const (
	// guestTransportMaxPorts bounds the number of distinct guest ports that a
	// single bridge can retain. Ports are normally fixed for an instance; the
	// bound prevents malformed or future metadata from retaining an unbounded
	// number of transport objects. Eviction only closes idle connections, so
	// in-flight requests remain safe.
	guestTransportMaxPorts = 8

	guestTransportMaxIdleConnsPerHost = 32

	// guestHeadroomDialTimeout replaces dialTimeout when vmmd's
	// bridge_stream_headroom switch is on (ADR-20261026120000). The guest is
	// a few microseconds away on the instance's own tap, so a dial that has
	// not completed after two SYN retransmits (1 s, 3 s) is a stuck guest,
	// and holding the request's stream for 30 s only queues others behind it.
	guestHeadroomDialTimeout = 5 * time.Second

	// h1IdleConnTimeout bounds an idle keep-alive connection to an H1 guest.
	// It must stay below the keep-alive timeout of the app servers customers
	// run (gunicorn gthread 2 s; Node, uvicorn, hypercorn and actix 5 s):
	// when the app closes an idle connection just as the bridge reuses it,
	// the guest answers the request with a reset, and net/http retries only
	// replayable methods, so a POST became a 502 (prod 2026-11-22: a cron
	// delivery to a Node app at ~one request per 5 s). Closing first leaves
	// no race; a new connection to the guest on its own tap costs well under
	// a millisecond. It also keeps parked instances from retaining
	// guest-side sockets until the outer bridge reaper runs.
	h1IdleConnTimeout = time.Second

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

		if conn != nil && prewarmUsable(conn) {
			resp, err := conn.RoundTrip(req)
			if err == nil {
				return resp, nil
			}
			if prewarmUsable(conn) {
				return resp, err
			}
			t.dropPrewarm(conn)
			if !errNotEstablished(err) {
				return resp, err
			}
			// The guest closed the prewarmed connection before it carried a
			// request, so this request never left the bridge. Send it on a
			// fresh connection instead of failing it.
			return t.base.RoundTrip(req)
		}
		if conn != nil {
			t.dropPrewarm(conn)
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

// prewarmUsable reports whether the prewarmed connection can carry a request.
// CanTakeNewRequest alone is not enough: x/net/http2 keeps reporting true for
// a connection that closed before its first request ("let it take a request
// (which will fail)"), and the prewarm has only carried a Ping. A guest that
// resets that connection (guest-init resets every off-guest connection right
// after a restore) left the bridge failing every request with
// errClientConnNotEstablished for the instance's whole life.
func prewarmUsable(conn *http2.ClientConn) bool {
	return conn.CanTakeNewRequest() && !conn.State().Closed
}

// errNotEstablished matches x/net/http2's unexported
// errClientConnNotEstablished: the connection closed before this request was
// written, so a retry on a new connection cannot duplicate it.
func errNotEstablished(err error) bool {
	return err != nil && err.Error() == "http2: client conn could not be established"
}

// dropPrewarm unpublishes and closes a prewarmed connection that can no longer
// carry requests; later requests use the transport's own pool.
func (t *guestH2CRoundTripper) dropPrewarm(conn *http2.ClientConn) {
	t.mu.Lock()
	if t.prewarm == conn {
		t.prewarm = nil
	}
	t.mu.Unlock()
	_ = conn.Close()
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
		if t.prewarm != nil && prewarmUsable(t.prewarm) {
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
		conn, err := netns.DialBridgeContext(ctx, &dialer, "tcp", net.JoinHostPort(t.guestIP, strconv.FormatUint(uint64(t.port), 10)))
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
		} else if t.prewarm == nil || !prewarmUsable(t.prewarm) {
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
	idle := guestIdleConnsPerHost()
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: guestDialTimeout()}
			return netns.DialBridgeContext(ctx, &d, "tcp", net.JoinHostPort(guestIP, strconv.FormatUint(uint64(guestPort), 10)))
		},
		DisableCompression:  true,
		MaxIdleConns:        idle,
		MaxIdleConnsPerHost: idle,
		IdleConnTimeout:     h1IdleConnTimeout,
		// Outer cap; each request bounds its own wait (guestHeaderWait).
		ResponseHeaderTimeout: api.RequestBudgetPlanMax,
		ExpectContinueTimeout: time.Second,
	}
}

// guestHeadroom reports whether vmmd started this bridge with its
// bridge_stream_headroom switch on (ADR-20261026120000).
func guestHeadroom() bool {
	return os.Getenv("FAAS_BRIDGE_GUEST_HEADROOM") == "1"
}

// guestIdleConnsPerHost keeps, with headroom, one idle guest connection per
// stream the bridge accepts. Below that, more than 32 concurrent requests
// close a connection after every response and dial a new one: on prod at
// ~290 req/s that was ~90 new guest connections a second.
func guestIdleConnsPerHost() int {
	if guestHeadroom() {
		return h2cMaxConcurrentStreams
	}
	return guestTransportMaxIdleConnsPerHost
}

// guestDialTimeout bounds one TCP dial to the guest.
func guestDialTimeout() time.Duration {
	if guestHeadroom() {
		return guestHeadroomDialTimeout
	}
	return dialTimeout
}

// markGuestTransportError tags the bridge's own 502 for a failed guest
// connection with the exchange ID, so vmmd can tell it from a 502 the guest
// returned, log it, and replace a bridge whose guest connection keeps
// failing. A request that ran out of its own time (ctx done) is not a
// transport fault and stays untagged: a slow app must not cost the bridge.
func markGuestTransportError(w http.ResponseWriter, r *http.Request, ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	if id := r.Header.Get(bridgecompletion.ExchangeHeader); id != "" {
		w.Header().Set(bridgecompletion.GuestErrorHeader, id)
	}
}
