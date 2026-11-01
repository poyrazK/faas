// adr: 1027
package dbroute

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

var routeDestination = netip.MustParseAddrPort("198.18.0.1:5432")

// echoUpstream answers each connection with "got:" + everything it read
// before the client's half-close, then closes. hold keeps a connection
// open until the test ends instead.
func echoUpstream(t *testing.T, hold bool) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { close(done); _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				if hold {
					<-done
					return
				}
				data, _ := io.ReadAll(conn)
				_, _ = conn.Write(append([]byte("got:"), data...))
			}()
		}
	}()
	return ln.Addr().String()
}

// deadUpstream is an address nothing listens on.
func deadUpstream(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

type harness struct {
	addr  string
	dials atomic.Int32
}

func startForwarder(t *testing.T, cfg Config) *harness {
	t.Helper()
	h := &harness{}
	if cfg.OriginalDestination == nil {
		cfg.OriginalDestination = func(net.Conn) (netip.AddrPort, error) { return routeDestination, nil }
	}
	cfg.Destination = routeDestination
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = time.Second
	}
	if cfg.MaxSessions == 0 {
		cfg.MaxSessions = 16
	}
	if cfg.MaxSessionsPerSource == 0 {
		cfg.MaxSessionsPerSource = 16
	}
	var d net.Dialer
	cfg.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		h.dials.Add(1)
		return d.DialContext(ctx, network, address)
	}
	f, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- f.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-served:
			if err != nil {
				t.Errorf("Serve = %v after cancel", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return after cancel")
		}
	})
	h.addr = ln.Addr().String()
	return h
}

// roundTrip sends payload, half-closes and returns what came back.
func roundTrip(t *testing.T, addr, payload string) string {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(payload)); err != nil {
		return ""
	}
	_ = conn.(*net.TCPConn).CloseWrite()
	got, _ := io.ReadAll(conn)
	return string(got)
}

func TestForwarderRelaysWithHalfClose(t *testing.T) {
	h := startForwarder(t, Config{Upstreams: []string{echoUpstream(t, false)}})
	if got := roundTrip(t, h.addr, "SSLRequest"); got != "got:SSLRequest" {
		t.Fatalf("relayed %q", got)
	}
}

func TestForwarderRefusesOtherDestinations(t *testing.T) {
	cases := map[string]func(net.Conn) (netip.AddrPort, error){
		"other port":   func(net.Conn) (netip.AddrPort, error) { return netip.MustParseAddrPort("198.18.0.1:5433"), nil },
		"other target": func(net.Conn) (netip.AddrPort, error) { return netip.MustParseAddrPort("10.0.0.9:5432"), nil },
		"not DNAT'd":   func(net.Conn) (netip.AddrPort, error) { return netip.AddrPort{}, errors.New("no original destination") },
	}
	for name, orig := range cases {
		t.Run(name, func(t *testing.T) {
			h := startForwarder(t, Config{Upstreams: []string{echoUpstream(t, false)}, OriginalDestination: orig})
			if got := roundTrip(t, h.addr, "x"); got != "" {
				t.Fatalf("relayed %q", got)
			}
			if h.dials.Load() != 0 {
				t.Fatal("dialed pgproxyd for a connection that was not the route")
			}
		})
	}
}

func TestForwarderFailsOverToAReachableUpstream(t *testing.T) {
	live := echoUpstream(t, false)
	h := startForwarder(t, Config{Upstreams: []string{deadUpstream(t), live}})
	for i := range 4 { // every starting point in the rotation must succeed
		if got := roundTrip(t, h.addr, "q"); got != "got:q" {
			t.Fatalf("session %d relayed %q", i, got)
		}
	}

	down := startForwarder(t, Config{Upstreams: []string{deadUpstream(t), deadUpstream(t)}})
	if got := roundTrip(t, down.addr, "q"); got != "" {
		t.Fatalf("relayed %q with every upstream down", got)
	}
	if down.dials.Load() != 2 {
		t.Fatalf("dials = %d, want each upstream tried once", down.dials.Load())
	}
}

func TestForwarderCapsSessionsPerSource(t *testing.T) {
	held := echoUpstream(t, true)
	h := startForwarder(t, Config{Upstreams: []string{held}, MaxSessionsPerSource: 1, HalfCloseGrace: 100 * time.Millisecond})
	first, err := net.Dial("tcp", h.addr)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return h.dials.Load() == 1 })
	if got := roundTrip(t, h.addr, "q"); got != "" {
		t.Fatalf("a second session from the same source relayed %q", got)
	}
	if h.dials.Load() != 1 {
		t.Fatal("the refused session dialed pgproxyd")
	}
	_ = first.Close()
	// The upstream never answers the half-close; the grace frees the slot.
	waitFor(t, func() bool {
		conn, err := net.Dial("tcp", h.addr)
		if err != nil {
			return false
		}
		defer func() { _ = conn.Close() }()
		before := h.dials.Load()
		_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		_, _ = conn.Read(make([]byte, 1))
		return h.dials.Load() > before
	})
}

func TestNewValidatesConfig(t *testing.T) {
	orig := func(net.Conn) (netip.AddrPort, error) { return routeDestination, nil }
	valid := Config{Upstreams: []string{"10.0.0.1:5432"}, Destination: routeDestination, OriginalDestination: orig,
		DialTimeout: time.Second, MaxSessions: 1, MaxSessionsPerSource: 1}
	if _, err := New(valid); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"no upstreams":       func(c *Config) { c.Upstreams = nil },
		"upstream sans port": func(c *Config) { c.Upstreams = []string{"pgproxyd"} },
		"no destination":     func(c *Config) { c.Destination = netip.AddrPort{} },
		"no orig lookup":     func(c *Config) { c.OriginalDestination = nil },
		"zero session cap":   func(c *Config) { c.MaxSessions = 0 },
		"zero dial timeout":  func(c *Config) { c.DialTimeout = 0 },
	} {
		c := valid
		mutate(&c)
		if _, err := New(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
