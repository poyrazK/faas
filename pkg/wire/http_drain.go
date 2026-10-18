package wire

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// HTTPDrain stops an http.Server without dropping requests that raced the
// stop. net/http drops a request it reads after Shutdown began: a connection
// accepted just before the listener closed, whose request was still in
// flight, was closed with no response and the client saw EOF. Under load
// every restart lost a few such requests
// (docs/adr/2026101801-zero-downtime-daemon-restarts.md).
//
// Shutdown therefore first closes the listeners, then waits until every
// accepted connection has delivered its first request, closes any that sent
// nothing within quiesceWait (a preconnect), and only then calls
// http.Server.Shutdown, which lets those requests finish.
type HTTPDrain struct {
	mu        sync.Mutex
	fresh     map[net.Conn]struct{} // accepted, first request not read yet
	listeners []net.Listener
	wake      chan struct{}
	stopping  atomic.Bool
}

// quiesceWait bounds the wait for accepted connections' first requests.
const quiesceWait = time.Second

// quiesceSettle covers the gap between net/http marking a connection active
// and its shutdown check for the request it just read.
const quiesceSettle = 10 * time.Millisecond

// NewHTTPDrain tracks server's new connections. Call it before Serve; it
// chains any ConnState hook already set.
func NewHTTPDrain(server *http.Server) *HTTPDrain {
	d := &HTTPDrain{fresh: map[net.Conn]struct{}{}, wake: make(chan struct{}, 1)}
	prev := server.ConnState
	server.ConnState = func(c net.Conn, state http.ConnState) {
		d.observe(c, state)
		if prev != nil {
			prev(c, state)
		}
	}
	return d
}

func (d *HTTPDrain) observe(c net.Conn, state http.ConnState) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if state == http.StateNew {
		d.fresh[c] = struct{}{}
		return
	}
	if _, ok := d.fresh[c]; ok {
		delete(d.fresh, c)
		select {
		case d.wake <- struct{}{}:
		default:
		}
	}
}

func (d *HTTPDrain) pending() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.fresh)
}

// Listener registers l for Shutdown to close first. Serve the returned
// listener; it may be closed more than once.
func (d *HTTPDrain) Listener(l net.Listener) net.Listener {
	wrapped := &closeOnceListener{Listener: l}
	d.mu.Lock()
	d.listeners = append(d.listeners, wrapped)
	d.mu.Unlock()
	return wrapped
}

// Shutdown closes the registered listeners, waits for accepted connections'
// first requests, then shuts server down within ctx. Serve returns
// net.ErrClosed rather than http.ErrServerClosed once its listener closes.
func (d *HTTPDrain) Shutdown(ctx context.Context, server *http.Server) error {
	d.mu.Lock()
	listeners := append([]net.Listener(nil), d.listeners...)
	d.mu.Unlock()
	d.stopping.Store(true)
	for _, l := range listeners {
		_ = l.Close()
	}
	d.quiesce(ctx)
	return server.Shutdown(ctx)
}

// ServeErr filters the error Serve returned: nil when it ended because of
// a shutdown (http.ErrServerClosed, or the listener this drain closed),
// otherwise err.
func (d *HTTPDrain) ServeErr(err error) error {
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	if d.stopping.Load() && errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

type closeOnceListener struct {
	net.Listener
	once sync.Once
	err  error
}

func (l *closeOnceListener) Close() error {
	l.once.Do(func() { l.err = l.Listener.Close() })
	return l.err
}

func (d *HTTPDrain) quiesce(ctx context.Context) {
	deadline := time.NewTimer(quiesceWait)
	defer deadline.Stop()
	for d.pending() > 0 {
		select {
		case <-d.wake:
		case <-deadline.C:
			d.closeSilent()
			return
		case <-ctx.Done():
			return
		}
	}
	select {
	case <-time.After(quiesceSettle):
	case <-ctx.Done():
	}
}

// closeSilent closes connections that sent nothing within quiesceWait, such
// as an upstream proxy's preconnects. http.Server.Shutdown would otherwise
// wait up to five seconds for each before treating it as idle.
func (d *HTTPDrain) closeSilent() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for c := range d.fresh {
		_ = c.Close()
		delete(d.fresh, c)
	}
}
