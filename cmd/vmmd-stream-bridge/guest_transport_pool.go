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
)

type guestTransportEntry struct {
	port     uint16
	lastUsed time.Time
	h1       *http.Transport
	h2c      *http2.Transport
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

func (p *guestTransportPool) h2c(port uint16) *http2.Transport {
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
		entry.h2c = newGuestH2CTransportFn(p.guestIP, port)
	}
	return entry.h2c
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
		e.h2c.CloseIdleConnections()
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
