package oci

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// Public addresses ipAllowed accepts; the fakes below never touch the network.
const (
	testV6A = "2606:4700:4700::1111"
	testV6B = "2606:4700:4700::1001"
	testV4A = "1.1.1.1"
	testV4B = "8.8.8.8"
)

var errNoRoute = errors.New("connect: no route to host")

// fakeConn is what the fake dial returns; target says which address connected.
type fakeConn struct {
	net.Conn
	target string
	closed chan struct{}
	once   sync.Once
}

func (c *fakeConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

// fakeEgress is a resolver and a dialer in one: lookup returns ips, dial
// records every target and fails or connects per behave.
type fakeEgress struct {
	t       *testing.T
	ips     []string
	behave  func(target string) (*fakeConn, error)
	mu      sync.Mutex
	lookups int
	dialed  []string
}

func (f *fakeEgress) lookup(_ context.Context, _ string) ([]net.IPAddr, error) {
	f.mu.Lock()
	f.lookups++
	f.mu.Unlock()
	out := make([]net.IPAddr, 0, len(f.ips))
	for _, s := range f.ips {
		out = append(out, net.IPAddr{IP: net.ParseIP(s)})
	}
	return out, nil
}

func (f *fakeEgress) dial(_ context.Context, _, target string) (net.Conn, error) {
	f.mu.Lock()
	f.dialed = append(f.dialed, target)
	f.mu.Unlock()
	c, err := f.behave(target)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (f *fakeEgress) conn(target string) *fakeConn {
	a, b := net.Pipe()
	f.t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	return &fakeConn{Conn: a, target: target, closed: make(chan struct{})}
}

func (f *fakeEgress) dialedTargets() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.dialed...)
}

func hostPort(ip, port string) string { return net.JoinHostPort(ip, port) }

// adr: 021 — the name resolves once, every address is checked against the
// egress policy, and only checked addresses are dialled, each in turn until
// one connects.
func TestEgressDial_TriesEveryCheckedAddress(t *testing.T) {
	failV6 := func(f *fakeEgress) func(string) (*fakeConn, error) {
		return func(target string) (*fakeConn, error) {
			if strings.HasPrefix(target, "[") {
				return nil, errNoRoute
			}
			return f.conn(target), nil
		}
	}
	for _, tc := range []struct {
		name       string
		ips        []string
		network    string
		behave     func(f *fakeEgress) func(string) (*fakeConn, error)
		wantDialed []string // in order; dials run one after the other (no fallback delay)
		wantTarget string   // address that connected; "" when the dial must fail
		wantErr    string
		wantIs     []error
	}{
		{
			name: "IPv6 sorted first but unroutable connects over IPv4", ips: []string{testV6A, testV4A}, network: "tcp",
			behave:     failV6,
			wantDialed: []string{hostPort(testV6A, "443"), hostPort(testV4A, "443")}, wantTarget: hostPort(testV4A, "443"),
		},
		{
			name: "first address works, the rest are never dialled", ips: []string{testV4A, testV6A}, network: "tcp",
			behave: func(f *fakeEgress) func(string) (*fakeConn, error) {
				return func(t string) (*fakeConn, error) { return f.conn(t), nil }
			},
			wantDialed: []string{hostPort(testV4A, "443")}, wantTarget: hostPort(testV4A, "443"),
		},
		{
			name: "families alternate: v6 a, v4 a, v6 b, v4 b", ips: []string{testV6A, testV6B, testV4A, testV4B}, network: "tcp",
			behave: func(f *fakeEgress) func(string) (*fakeConn, error) {
				return func(t string) (*fakeConn, error) {
					if t == hostPort(testV4B, "443") {
						return f.conn(t), nil
					}
					return nil, errNoRoute
				}
			},
			wantDialed: []string{hostPort(testV6A, "443"), hostPort(testV4A, "443"), hostPort(testV6B, "443"), hostPort(testV4B, "443")},
			wantTarget: hostPort(testV4B, "443"),
		},
		{
			name: "every address fails: all tried, all errors kept", ips: []string{testV6A, testV4A}, network: "tcp",
			behave: func(*fakeEgress) func(string) (*fakeConn, error) {
				return func(string) (*fakeConn, error) { return nil, errNoRoute }
			},
			wantDialed: []string{hostPort(testV6A, "443"), hostPort(testV4A, "443")}, wantErr: "oci: egress: dial example.test", wantIs: []error{errNoRoute},
		},
		{
			name: "one address fails: its error comes back unwrapped", ips: []string{testV4A}, network: "tcp",
			behave: func(*fakeEgress) func(string) (*fakeConn, error) {
				return func(string) (*fakeConn, error) { return nil, errNoRoute }
			},
			wantDialed: []string{hostPort(testV4A, "443")}, wantIs: []error{errNoRoute},
		},
		{
			name: "tcp4 never dials IPv6", ips: []string{testV6A, testV4A}, network: "tcp4",
			behave: func(f *fakeEgress) func(string) (*fakeConn, error) {
				return func(t string) (*fakeConn, error) { return f.conn(t), nil }
			},
			wantDialed: []string{hostPort(testV4A, "443")}, wantTarget: hostPort(testV4A, "443"),
		},
		{
			name: "tcp6 with only IPv4 addresses fails without dialling", ips: []string{testV4A, testV4B}, network: "tcp6",
			behave:  func(*fakeEgress) func(string) (*fakeConn, error) { return nil },
			wantErr: "no tcp6 addresses for example.test",
		},
		{
			name: "one denied address refuses the whole dial", ips: []string{testV4A, "10.0.0.1"}, network: "tcp",
			behave:  func(*fakeEgress) func(string) (*fakeConn, error) { return nil },
			wantIs:  []error{ErrImageEgressDenied, ErrEgressDenied},
			wantErr: "10.0.0.1",
		},
		{
			name: "a denied IPv6 address refuses the whole dial", ips: []string{testV6A, "fe80::1", testV4A}, network: "tcp",
			behave: func(*fakeEgress) func(string) (*fakeConn, error) { return nil },
			wantIs: []error{ErrImageEgressDenied},
		},
		{
			name: "IPv4-mapped loopback is still loopback", ips: []string{"::ffff:127.0.0.1", testV4A}, network: "tcp",
			behave: func(*fakeEgress) func(string) (*fakeConn, error) { return nil },
			wantIs: []error{ErrImageEgressDenied},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeEgress{t: t, ips: tc.ips}
			f.behave = tc.behave(f)
			// A negative fallback delay waits for each failure, so the order is fixed.
			dial := newEgressDialContext(5*time.Second, -1, f.lookup, f.dial)
			conn, err := dial(context.Background(), tc.network, "example.test:443")
			if f.lookups != 1 {
				t.Errorf("lookups = %d, want 1 (resolve once)", f.lookups)
			}
			got := f.dialedTargets()
			if strings.Join(got, ",") != strings.Join(tc.wantDialed, ",") {
				t.Errorf("dialed %v, want %v", got, tc.wantDialed)
			}
			for _, target := range got {
				host, _, _ := net.SplitHostPort(target)
				if !ipAllowed(netip.MustParseAddr(host)) {
					t.Errorf("dialled %s, which the egress policy denies", target)
				}
			}
			if tc.wantTarget != "" {
				if err != nil {
					t.Fatalf("dial error: %v", err)
				}
				if c, ok := conn.(*fakeConn); !ok || c.target != tc.wantTarget {
					t.Fatalf("connected to %v, want %s", conn, tc.wantTarget)
				}
				return
			}
			if err == nil {
				_ = conn.Close()
				t.Fatal("dial succeeded, want an error")
			}
			if tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q lacks %q", err, tc.wantErr)
			}
			for _, want := range tc.wantIs {
				if !errors.Is(err, want) {
					t.Errorf("error %v is not %v", err, want)
				}
			}
		})
	}
}

// adr: 021 — a host whose first address black-holes does not hold up the
// next: it starts after the fallback delay and the stalled attempt is
// cancelled once another connects.
func TestEgressDial_FallbackDelayStartsNextAttempt(t *testing.T) {
	f := &fakeEgress{t: t, ips: []string{testV6A, testV4A}}
	stalled := make(chan struct{})
	f.behave = func(target string) (*fakeConn, error) { return f.conn(target), nil }
	dialFn := func(ctx context.Context, network, target string) (net.Conn, error) {
		if strings.HasPrefix(target, "[") {
			f.mu.Lock()
			f.dialed = append(f.dialed, target)
			f.mu.Unlock()
			<-ctx.Done() // black hole: only the cancellation ends it
			close(stalled)
			return nil, ctx.Err()
		}
		return f.dial(ctx, network, target)
	}
	dial := newEgressDialContext(5*time.Second, 20*time.Millisecond, f.lookup, dialFn)
	start := time.Now()
	conn, err := dial(context.Background(), "tcp", "example.test:443")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if c := conn.(*fakeConn); c.target != hostPort(testV4A, "443") {
		t.Fatalf("connected to %s, want the IPv4 address", c.target)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("dial took %v; the stalled IPv6 attempt held it up", took)
	}
	select {
	case <-stalled:
	case <-time.After(2 * time.Second):
		t.Fatal("the stalled attempt was not cancelled after the other connected")
	}
}

// adr: 021 — an attempt that connects after another won is closed, not leaked.
func TestEgressDial_ClosesLosingConnection(t *testing.T) {
	f := &fakeEgress{t: t, ips: []string{testV6A, testV4A}}
	release := make(chan struct{})
	loser := f.conn(hostPort(testV6A, "443"))
	dialFn := func(ctx context.Context, network, target string) (net.Conn, error) {
		if strings.HasPrefix(target, "[") {
			<-release // connects late, ignoring the cancellation
			return loser, nil
		}
		return f.conn(target), nil
	}
	dial := newEgressDialContext(5*time.Second, 10*time.Millisecond, f.lookup, dialFn)
	conn, err := dial(context.Background(), "tcp", "example.test:443")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if c := conn.(*fakeConn); c.target != hostPort(testV4A, "443") {
		t.Fatalf("connected to %s, want the IPv4 address", c.target)
	}
	close(release)
	select {
	case <-loser.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("the losing connection was never closed")
	}
}

// adr: 021 — the dial budget covers every attempt: with all addresses black
// holed the dial ends at the timeout with each attempt's error.
func TestEgressDial_TimeoutEndsEveryAttempt(t *testing.T) {
	f := &fakeEgress{t: t, ips: []string{testV6A, testV4A}}
	dialFn := func(ctx context.Context, _, target string) (net.Conn, error) {
		f.mu.Lock()
		f.dialed = append(f.dialed, target)
		f.mu.Unlock()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	dial := newEgressDialContext(100*time.Millisecond, 10*time.Millisecond, f.lookup, dialFn)
	start := time.Now()
	_, err := dial(context.Background(), "tcp", "example.test:443")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("dial took %v, want about the 100 ms budget", took)
	}
	if got := len(f.dialedTargets()); got != 2 {
		t.Fatalf("dialled %d addresses, want both", got)
	}
}

func TestInterleaveFamilies(t *testing.T) {
	a := netip.MustParseAddr
	for _, tc := range []struct {
		name     string
		in, want []netip.Addr
	}{
		{"empty", nil, nil},
		{"one", []netip.Addr{a(testV4A)}, []netip.Addr{a(testV4A)}},
		{"v6 first leads", []netip.Addr{a(testV6A), a(testV6B), a(testV4A), a(testV4B)}, []netip.Addr{a(testV6A), a(testV4A), a(testV6B), a(testV4B)}},
		{"v4 first leads", []netip.Addr{a(testV4A), a(testV4B), a(testV6A)}, []netip.Addr{a(testV4A), a(testV6A), a(testV4B)}},
		{"one family keeps its order", []netip.Addr{a(testV4B), a(testV4A)}, []netip.Addr{a(testV4B), a(testV4A)}},
		{"v6 between v4s moves back", []netip.Addr{a(testV4A), a(testV6A), a(testV4B)}, []netip.Addr{a(testV4A), a(testV6A), a(testV4B)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := interleaveFamilies(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}
