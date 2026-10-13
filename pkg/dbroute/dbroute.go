// Package dbroute is the node-local half of the app route to native managed
// databases (ADR-1027). Guests dial api.DatabaseRouteAddress on the
// PostgreSQL port; the host DNATs the connection to this forwarder on the
// tenant bridge, which relays the bytes unchanged to any pgproxyd over the
// overlay. TLS, SNI routing and SCRAM stay end to end between the client and
// pgproxyd, so the forwarder holds no credentials and never sees a session's
// contents.
package dbroute

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/tcpmetrics"
)

// Rejection reasons on the tcpmetrics rejected counter.
const (
	rejectDestination = "destination"
	rejectCapacity    = "capacity"
	rejectUpstream    = "upstream_unavailable"
)

// Config wires the forwarder.
type Config struct {
	// Upstreams are pgproxyd listeners (host:port) reachable over the
	// overlay. Any of them serves any database: pgproxyd routes by SNI and
	// wakes the database through schedd.
	Upstreams []string
	// Destination is the only original destination accepted: the route's
	// address and port before the host DNAT.
	Destination netip.AddrPort
	// OriginalDestination recovers a DNAT'd connection's destination.
	OriginalDestination func(net.Conn) (netip.AddrPort, error)
	// Dial opens an upstream connection; nil uses a TCP dialer with
	// keepalives.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// DialTimeout bounds one upstream dial before the next is tried.
	DialTimeout time.Duration
	// MaxSessions caps concurrent sessions; MaxSessionsPerSource caps one
	// caller instance (the masqueraded source address).
	MaxSessions          int
	MaxSessionsPerSource int
	// HalfCloseGrace bounds how long the other direction may keep running
	// after one side ends. PostgreSQL never relies on half-close, so this
	// only frees sessions stuck behind a peer that stopped responding.
	// Zero means DefaultHalfCloseGrace.
	HalfCloseGrace time.Duration
	Metrics        *tcpmetrics.Metrics
	Log            *slog.Logger
}

// DefaultHalfCloseGrace is Config.HalfCloseGrace's default.
const DefaultHalfCloseGrace = 10 * time.Second

// Forwarder relays database sessions to pgproxyd.
type Forwarder struct {
	cfg  Config
	next atomic.Uint32

	mu        sync.Mutex
	active    int
	perSource map[netip.Addr]int
}

// New validates cfg.
func New(cfg Config) (*Forwarder, error) {
	if len(cfg.Upstreams) == 0 {
		return nil, errors.New("dbroute: at least one pgproxyd upstream is required")
	}
	for _, upstream := range cfg.Upstreams {
		if _, _, err := net.SplitHostPort(upstream); err != nil {
			return nil, fmt.Errorf("dbroute: upstream %q: %w", upstream, err)
		}
	}
	if !cfg.Destination.IsValid() || cfg.OriginalDestination == nil {
		return nil, errors.New("dbroute: destination and original-destination lookup are required")
	}
	if cfg.MaxSessions <= 0 || cfg.MaxSessionsPerSource <= 0 || cfg.DialTimeout <= 0 {
		return nil, errors.New("dbroute: session caps and dial timeout must be positive")
	}
	if cfg.Dial == nil {
		dialer := &net.Dialer{KeepAlive: 30 * time.Second}
		cfg.Dial = dialer.DialContext
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.HalfCloseGrace <= 0 {
		cfg.HalfCloseGrace = DefaultHalfCloseGrace
	}
	return &Forwarder{cfg: cfg, perSource: map[netip.Addr]int{}}, nil
}

// Serve accepts sessions until ctx ends or the listener fails. Open
// sessions end with ctx.
func (f *Forwarder) Serve(ctx context.Context, ln net.Listener) error {
	stop := context.AfterFunc(ctx, func() { _ = ln.Close() })
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return fmt.Errorf("dbroute: accept: %w", err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.handle(ctx, conn)
		}()
	}
}

func (f *Forwarder) handle(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	session := f.cfg.Metrics.Begin()
	dst, err := f.cfg.OriginalDestination(conn)
	if err != nil || dst != f.cfg.Destination {
		session.Reject(rejectDestination)
		return
	}
	source := remoteAddr(conn)
	if !f.acquire(source) {
		session.Reject(rejectCapacity)
		f.cfg.Log.Warn("dbroute: session refused at capacity", "source", source)
		return
	}
	defer f.release(source)
	upstream, err := f.dialUpstream(ctx)
	if err != nil {
		session.Reject(rejectUpstream)
		f.cfg.Log.Warn("dbroute: no pgproxyd reachable", "source", source, "err", err)
		return
	}
	defer func() { _ = upstream.Close() }()
	session.Finish(f.relay(ctx, conn, upstream))
}

// dialUpstream tries every upstream once, starting at the next in turn.
func (f *Forwarder) dialUpstream(ctx context.Context) (net.Conn, error) {
	n := len(f.cfg.Upstreams)
	start := int(f.next.Add(1)) % n
	var errs []error
	for i := range n {
		address := f.cfg.Upstreams[(start+i)%n]
		dialCtx, cancel := context.WithTimeout(ctx, f.cfg.DialTimeout)
		conn, err := f.cfg.Dial(dialCtx, "tcp", address)
		cancel()
		if err == nil {
			return conn, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", address, err))
		if ctx.Err() != nil {
			break
		}
	}
	return nil, errors.Join(errs...)
}

func (f *Forwarder) acquire(source netip.Addr) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active >= f.cfg.MaxSessions || f.perSource[source] >= f.cfg.MaxSessionsPerSource {
		return false
	}
	f.active++
	f.perSource[source]++
	return true
}

func (f *Forwarder) release(source netip.Addr) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active--
	if f.perSource[source]--; f.perSource[source] <= 0 {
		delete(f.perSource, source)
	}
}

// relay copies both directions, passing each half-close on, and returns the
// tcpmetrics outcome.
func (f *Forwarder) relay(ctx context.Context, client, upstream net.Conn) string {
	stop := context.AfterFunc(ctx, func() {
		_ = client.Close()
		_ = upstream.Close()
	})
	defer stop()
	errs := make(chan error, 2)
	pipe := func(dst, src net.Conn, direction string) {
		n, err := io.Copy(dst, src)
		f.cfg.Metrics.AddBytes(direction, n)
		closeWrite(dst)
		errs <- err
	}
	go pipe(upstream, client, "client_to_upstream")
	go pipe(client, upstream, "upstream_to_client")
	first := <-errs
	var second error
	select {
	case second = <-errs:
	case <-time.After(f.cfg.HalfCloseGrace):
		_ = client.Close()
		_ = upstream.Close()
		second = <-errs
	}
	switch {
	case ctx.Err() != nil:
		return "canceled"
	case first != nil && !errors.Is(first, net.ErrClosed), second != nil && !errors.Is(second, net.ErrClosed):
		return "error"
	default:
		return "success"
	}
}

func closeWrite(conn net.Conn) {
	if cw, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = conn.Close()
}

func remoteAddr(conn net.Conn) netip.Addr {
	if addr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		return addr.AddrPort().Addr().Unmap()
	}
	return netip.Addr{}
}
