package devbridge

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/http2"
)

var ErrDisconnected = errors.New("dev bridge laptop disconnected")
var ErrBusy = errors.New("dev bridge concurrency limit reached")

type connection struct {
	transport *http2.ClientConn
	socket    net.Conn
	done      chan struct{}
	slots     chan struct{}
	expiresAt time.Time
	once      sync.Once
}

func (c *connection) close() {
	c.once.Do(func() { close(c.done); _ = c.transport.Close(); _ = c.socket.Close() })
}

// Relay owns only live connections. Durable intent and authorization remain
// with apid. Pointer identity fences old disconnect cleanup after reconnect.
type Relay struct {
	mu            sync.Mutex
	connections   map[string]*connection
	maxConcurrent int
}

func NewRelay(maxConcurrent int) *Relay {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &Relay{connections: make(map[string]*connection), maxConcurrent: maxConcurrent}
}

// Attach serves a single authorized laptop connection until disconnect, expiry
// or revocation. Calling CloseSession revokes immediately; the owner must also
// revalidate persisted authorization before dispatching every request.
func (h *Relay) Attach(ctx context.Context, session Session, socket net.Conn) error {
	transport, err := (&http2.Transport{ReadIdleTimeout: 20 * time.Second, PingTimeout: 10 * time.Second}).NewClientConn(socket)
	if err != nil {
		_ = socket.Close()
		return err
	}
	c := &connection{transport: transport, socket: socket, done: make(chan struct{}), slots: make(chan struct{}, h.maxConcurrent), expiresAt: session.ExpiresAt}
	h.mu.Lock()
	old := h.connections[session.ID]
	h.connections[session.ID] = c
	h.mu.Unlock()
	if old != nil {
		old.close()
	}
	defer func() {
		c.close()
		h.mu.Lock()
		if h.connections[session.ID] == c {
			delete(h.connections, session.ID)
		}
		h.mu.Unlock()
	}()
	timer := time.NewTimer(time.Until(session.ExpiresAt))
	defer timer.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.done:
			return ErrDisconnected
		case <-timer.C:
			return ErrUnauthorized
		case <-ticker.C:
			if transport.State().Closed {
				return ErrDisconnected
			}
		}
	}
}

func (h *Relay) CloseSession(id string) {
	h.mu.Lock()
	c := h.connections[id]
	delete(h.connections, id)
	h.mu.Unlock()
	if c != nil {
		c.close()
	}
}

func (h *Relay) Connected(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.connections[id]
	return c != nil && time.Now().Before(c.expiresAt) && !c.transport.State().Closed
}

// RoundTrip never retries a request against a new connection or remote VM:
// the local application may already have executed a side effect.
func (h *Relay) RoundTrip(id string, request *http.Request) (*http.Response, error) {
	h.mu.Lock()
	c := h.connections[id]
	h.mu.Unlock()
	if c == nil || !time.Now().Before(c.expiresAt) {
		return nil, ErrDisconnected
	}
	select {
	case c.slots <- struct{}{}:
	default:
		return nil, ErrBusy
	}
	response, err := c.transport.RoundTrip(request)
	if err != nil {
		<-c.slots
		return nil, ErrDisconnected
	}
	response.Body = &releaseBody{ReadCloser: response.Body, release: func() { <-c.slots }}
	return response, nil
}
