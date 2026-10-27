//go:build !no_pg

// adr: 570
package main

import (
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// cuttablePostgresProxy forwards a daemon's Postgres connections; cut makes
// Postgres unreachable for that daemon only, while the test keeps its pool.
type cuttablePostgresProxy struct {
	listener net.Listener
	target   string
	mu       sync.Mutex
	cut      bool
	conns    map[net.Conn]struct{}
}

func startCuttablePostgresProxy(t *testing.T, target string) *cuttablePostgresProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &cuttablePostgresProxy{listener: listener, target: target, conns: make(map[net.Conn]struct{})}
	t.Cleanup(func() { _ = listener.Close(); p.setCut(true) })
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			go p.serve(client)
		}
	}()
	return p
}

func (p *cuttablePostgresProxy) serve(client net.Conn) {
	if !p.track(client) {
		_ = client.Close()
		return
	}
	server, err := net.Dial("tcp", p.target)
	if err != nil || !p.track(server) {
		_ = client.Close()
		if server != nil {
			_ = server.Close()
		}
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(server, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, server); done <- struct{}{} }()
	<-done
	_ = client.Close()
	_ = server.Close()
}

func (p *cuttablePostgresProxy) track(conn net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cut {
		return false
	}
	p.conns[conn] = struct{}{}
	return true
}

// setCut(true) closes every open connection and refuses new ones.
func (p *cuttablePostgresProxy) setCut(cut bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cut = cut
	if cut {
		for conn := range p.conns {
			_ = conn.Close()
		}
		clear(p.conns)
	}
}

// ADR-20261027085556: an app that served in the last ten minutes keeps
// serving from the gateway's routing view while the control-plane Postgres is
// unreachable, until the outage grace, and is refused after it. Two other
// per-request Postgres reads are outside that decision and off here: central
// rate and retry counters (process-local counters instead), and the financial
// budget admission, which re-reads each app every second and fails closed.
func TestTrafficFleetDaemonServesFromRoutingViewDuringPostgresOutage(t *testing.T) {
	f := newFleetDaemonFixture(t)
	config := f.pool.Config().ConnConfig
	proxy := startCuttablePostgresProxy(t, net.JoinHostPort(config.Host, strconv.Itoa(int(config.Port))))
	const grace = 4 * time.Second
	daemon := startFleetDaemonConfigured(t, f.pool, f.apps, f.nodes[0].ID, f.nodes[0].Name, f.usage, fleetDaemonOptions{
		LocalCounters: true, DatabaseAddr: proxy.listener.Addr().String(), RoutingGraceMS: int(grace / time.Millisecond),
		LogWarnings: true, CachedHostPolicy: true, BudgetAdmissionOff: true})
	t.Cleanup(func() {
		if t.Failed() {
			daemon.stop(t)
			t.Logf("daemon stderr:\n%s", &daemon.stderr)
		}
	})
	app := f.apps[2]
	fleetDaemonCall(t, daemon, app, "/view", http.StatusOK)
	// Let one refresh land, so the outage starts from a refreshed view.
	time.Sleep(1500 * time.Millisecond)
	fleetDaemonCall(t, daemon, app, "/view", http.StatusOK)
	proxy.setCut(true)
	cut := time.Now()
	served := 0
	var refused fleetDaemonResponse
	var refusedAt time.Time
	for time.Since(cut) < grace+5*time.Second {
		response, err := requestFleetDaemon(t.Context(), daemon.ready.Endpoint, app.Host, "/view")
		if err != nil {
			t.Fatal(err)
		}
		if response.status != http.StatusOK {
			refused, refusedAt = response, time.Now()
			break
		}
		if response.body != "guest served" {
			t.Fatalf("outage response body=%q", response.body)
		}
		served++
		time.Sleep(200 * time.Millisecond)
	}
	if refusedAt.IsZero() {
		t.Fatalf("the routing view admitted %d requests past its grace", served)
	}
	// The last successful refresh is at most one interval before the cut.
	if elapsed := refusedAt.Sub(cut); elapsed < grace-1500*time.Millisecond || served < 5 {
		t.Fatalf("refused %s after the cut with %d served (status=%d body=%q); want service until the %s grace",
			elapsed, served, refused.status, refused.body, grace)
	}
	if refused.status != http.StatusServiceUnavailable || !strings.Contains(refused.body, "traffic_policy_unavailable") {
		t.Fatalf("past the grace: status=%d body=%q; want 503 traffic_policy_unavailable", refused.status, refused.body)
	}
	t.Logf("served %d requests for %s with Postgres unreachable, refused after", served, refusedAt.Sub(cut).Round(time.Millisecond))
	proxy.setCut(false)
	for until := time.Now().Add(10 * time.Second); ; {
		response, err := requestFleetDaemon(t.Context(), daemon.ready.Endpoint, app.Host, "/view")
		if err == nil && response.status == http.StatusOK {
			break
		}
		if time.Now().After(until) {
			t.Fatalf("no recovery after Postgres returned: %+v err=%v", response, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
