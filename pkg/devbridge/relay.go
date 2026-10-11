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
	upgradeLimits UpgradeLimits
	upgrades      map[string]map[*upgradeHandle]struct{}
}

// upgradeHandle is one admitted upgraded connection. close must unblock its
// tunnel; CloseSession calls it so revocation also ends long-lived sockets.
type upgradeHandle struct{ close func() }

func NewRelay(maxConcurrent int) *Relay {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &Relay{connections: make(map[string]*connection), maxConcurrent: maxConcurrent, upgrades: make(map[string]map[*upgradeHandle]struct{})}
}

// WithUpgradeLimits enables WebSocket forwarding (ADR-742). Without it every
// upgrade is refused, so an older relay configuration cannot silently widen
// what crosses the bridge.
func (h *Relay) WithUpgradeLimits(limits UpgradeLimits) *Relay {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.upgradeLimits = limits
	return h
}

func (h *Relay) limitsForUpgrade() UpgradeLimits {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.upgradeLimits
}

// admitUpgrade reserves one of the session's upgraded-connection slots. The
// budget is shared by scoped traffic and dependency upgrades.
func (h *Relay) admitUpgrade(id string, closeFn func()) (func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.upgradeLimits.MaxConnections < 1 {
		return nil, ErrUpgradeDisabled
	}
	set := h.upgrades[id]
	if len(set) >= h.upgradeLimits.MaxConnections {
		return nil, ErrUpgradeLimit
	}
	if set == nil {
		set = make(map[*upgradeHandle]struct{})
		h.upgrades[id] = set
	}
	handle := &upgradeHandle{close: closeFn}
	set[handle] = struct{}{}
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(set, handle)
		if len(set) == 0 && h.upgrades[id] != nil && len(h.upgrades[id]) == 0 {
			delete(h.upgrades, id)
		}
	}, nil
}

// Upgrades reports the session's open upgraded connections.
func (h *Relay) Upgrades(id string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.upgrades[id])
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
	handles := make([]*upgradeHandle, 0, len(h.upgrades[id]))
	for handle := range h.upgrades[id] {
		handles = append(handles, handle)
	}
	h.mu.Unlock()
	if c != nil {
		c.close()
	}
	for _, handle := range handles {
		handle.close()
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

// roundTripUpgrade opens the tunnel stream for an admitted upgrade. It does
// not consume a request slot: the session's upgrade budget bounds it, and the
// laptop accepts requests + upgrades concurrent streams. The returned channel
// closes when this laptop connection ends, fencing the tunnel to its owner.
func (h *Relay) roundTripUpgrade(id string, request *http.Request) (*http.Response, <-chan struct{}, error) {
	h.mu.Lock()
	c := h.connections[id]
	h.mu.Unlock()
	if c == nil || !time.Now().Before(c.expiresAt) {
		return nil, nil, ErrDisconnected
	}
	response, err := c.transport.RoundTrip(request)
	if err != nil {
		return nil, nil, ErrDisconnected
	}
	return response, c.done, nil
}
