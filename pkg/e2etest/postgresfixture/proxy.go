package postgresfixture

import (
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Proxy forwards PostgreSQL bytes without terminating TLS. It exposes only the
// disposable test cluster, on an explicitly prepared test-host interface.
type Proxy struct {
	Host  string
	Port  uint16
	state *proxyState
}

type proxyState struct {
	mu                  sync.Mutex
	connections         map[net.Conn]struct{}
	closed, unavailable bool
	connectDelay        time.Duration
	nextConnectDelay    time.Duration
}

// ConnectionCount observes live downstream sessions without decoding TLS or
// exposing credentials. Acceptance uses it to prove checkpoint pool draining.
func (p Proxy) ConnectionCount() int {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	return len(p.state.connections)
}

// Disconnect retires existing sessions while allowing new TLS sessions.
func (p Proxy) Disconnect() {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	for conn := range p.state.connections {
		_ = conn.Close()
	}
}

// SetConnectDelay delays new upstream connections without interrupting existing
// TLS sessions. It models provider connection latency within restore deadlines.
func (p Proxy) SetConnectDelay(delay time.Duration) {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	p.state.connectDelay = max(delay, 0)
}

// DelayNextConnection models one slow provider handshake. The fault is consumed
// when accepted, so a cold-boot replacement can use recovered SQL independently.
func (p Proxy) DelayNextConnection(delay time.Duration) {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	p.state.nextConnectDelay = max(delay, 0)
}

// SetAvailable controls an explicit SQL outage; it never changes the cluster.
func (p Proxy) SetAvailable(available bool) {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	p.state.unavailable = !available
	if !available {
		for conn := range p.state.connections {
			_ = conn.Close()
		}
	}
}

func OpenProxy(t *testing.T, cluster *pgxpool.Pool, listenIP string, port uint16) Proxy {
	return OpenProxyWithDeadline(t, cluster, listenIP, port, 30*time.Second)
}

func OpenProxyWithDeadline(t *testing.T, cluster *pgxpool.Pool, listenIP string, port uint16, deadline time.Duration) Proxy {
	t.Helper()
	listener, err := net.Listen("tcp4", net.JoinHostPort(listenIP, strconv.Itoa(int(port))))
	if err != nil {
		t.Fatal("cannot listen for disposable SQL TLS proxy")
	}
	addr := listener.Addr().(*net.TCPAddr)
	host := cluster.Config().ConnConfig.Host
	if strings.HasPrefix(host, "/") {
		host = "127.0.0.1"
	}
	target := net.JoinHostPort(host, strconv.Itoa(int(cluster.Config().ConnConfig.Port)))
	ctx, cancel := context.WithCancel(context.Background())
	state := &proxyState{connections: make(map[net.Conn]struct{})}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			state.mu.Lock()
			if state.closed || state.unavailable {
				state.mu.Unlock()
				_ = client.Close()
				continue
			}
			delay := state.connectDelay
			if state.nextConnectDelay > 0 {
				delay = state.nextConnectDelay
				state.nextConnectDelay = 0
			}
			state.connections[client] = struct{}{}
			state.mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { _ = client.Close(); state.mu.Lock(); delete(state.connections, client); state.mu.Unlock() }()
				if delay > 0 {
					timer := time.NewTimer(delay)
					defer timer.Stop()
					select {
					case <-timer.C:
					case <-ctx.Done():
						return
					}
				}
				upstream, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", target)
				if err != nil {
					return
				}
				defer func() { _ = upstream.Close() }()
				// Retained application pools must survive idle periods. Faults
				// and fixture cleanup close sessions explicitly, not by age.
				done := make(chan struct{})
				go func() { _, _ = io.Copy(upstream, client); _ = upstream.Close(); close(done) }()
				_, _ = io.Copy(client, upstream)
				_ = client.Close()
				<-done
			}()
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		state.mu.Lock()
		state.closed = true
		for conn := range state.connections {
			_ = conn.Close()
		}
		state.mu.Unlock()
		wg.Wait()
	})
	return Proxy{Host: addr.IP.String(), Port: uint16(addr.Port), state: state}
}
