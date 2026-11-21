package agentproxy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/net/publicsuffix"
)

// Proxy is the politeness proxy. nftables on the gateway redirects agent
// TCP 80 and 443 to its listeners; it reads where each connection was going,
// names the destination, applies the caps, and dials it from one of the
// agent addresses.
type Proxy struct {
	Registry  *Registry
	Blocklist *Blocklist
	// Addresses are the agent public addresses; a VM always leaves from
	// the same one.
	Addresses []netip.Addr
	// OriginalDestination is where a redirected connection was going.
	OriginalDestination func(net.Conn) (netip.AddrPort, error)
	// Dial connects to dst from local.
	Dial        func(ctx context.Context, local netip.Addr, dst netip.AddrPort) (net.Conn, error)
	PeekTimeout time.Duration
	// ConnLog receives one line per connection, accepted or refused
	// (ADR-20261117185346 §5).
	ConnLog *slog.Logger
	Metrics *Metrics
	// now is the clock; nil is time.Now.
	now func() time.Time

	limiter *limiter
}

// Configure sets the caps; it must run before Serve.
func (p *Proxy) Configure(caps Caps) { p.limiter = newLimiter(caps) }

// Serve accepts connections from ln until ctx ends or ln fails.
func (p *Proxy) Serve(ctx context.Context, ln net.Listener) error {
	if p.limiter == nil || p.Registry == nil || len(p.Addresses) == 0 || p.OriginalDestination == nil || p.Dial == nil {
		return errors.New("agentproxy: proxy is not configured")
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil //nolint:nilerr // the listener closed because ctx ended: a clean stop
			}
			return err
		}
		go p.handle(ctx, conn)
	}
}

// handle serves one agent connection and records its outcome.
func (p *Proxy) handle(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	rec := ConnRecord{Start: p.clock()}
	rec.Source, _ = netip.ParseAddrPort(conn.RemoteAddr().String())
	rec.Source = netip.AddrPortFrom(rec.Source.Addr().Unmap(), rec.Source.Port())
	rec.Outcome = p.serve(ctx, conn, &rec)
	if p.Metrics != nil {
		p.Metrics.connections.WithLabelValues(rec.Outcome).Inc()
	}
	logConn(ctx, p.ConnLog, &rec, p.clock())
}

func (p *Proxy) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

// serve carries conn and fills rec as it learns who and where; it returns
// the outcome.
func (p *Proxy) serve(ctx context.Context, conn net.Conn, rec *ConnRecord) string {
	if !rec.Source.IsValid() {
		return OutcomeUnregistered
	}
	reg, ok := p.Registry.Lookup(rec.Source.Addr())
	if !ok {
		return OutcomeUnregistered
	}
	rec.Node, rec.AccountID, rec.SandboxID = reg.Node, reg.AccountID, reg.SandboxID
	dst, err := p.OriginalDestination(conn)
	if err != nil {
		return OutcomeBadDestination
	}
	rec.Dest = dst
	if !publicV4(dst.Addr()) || (dst.Port() != 80 && dst.Port() != 443) {
		return OutcomeBadDestination
	}
	_ = conn.SetReadDeadline(time.Now().Add(p.PeekTimeout))
	var host string
	var prefix []byte
	if dst.Port() == 443 {
		host, prefix, err = peekTLS(conn)
	} else {
		host, prefix, err = peekHTTP(conn)
	}
	_ = conn.SetReadDeadline(time.Time{})
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if len(host) <= 253 {
		rec.Host = host
	}
	if err != nil || !validHostname(host) {
		return OutcomeNoSNI
	}
	domain, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return OutcomeNoSNI
	}
	if p.Blocklist.Blocks(host) {
		return OutcomeBlocklist
	}
	release, refused := p.limiter.acquire(reg.Addr, reg.AccountID, domain)
	if refused != "" {
		return refused
	}
	defer release()
	rec.Egress = p.addressFor(reg.Addr)
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	upstream, err := p.Dial(dialCtx, rec.Egress, dst)
	cancel()
	if err != nil {
		return OutcomeUpstream
	}
	defer func() { _ = upstream.Close() }()
	n, err := upstream.Write(prefix)
	rec.BytesUp = int64(n)
	if err != nil {
		return OutcomeUpstream
	}
	if p.Metrics != nil {
		p.Metrics.active.Inc()
		defer p.Metrics.active.Dec()
	}
	up, down := splice(conn, upstream)
	rec.BytesUp += up
	rec.BytesDown = down
	return OutcomeAccepted
}

// addressFor is the agent address vm leaves from, the same for its life.
func (p *Proxy) addressFor(vm netip.Addr) netip.Addr { return AddressFor(p.Addresses, vm) }

// AddressFor picks vm's agent address from its low bits: index
// vm & (size-1), where size is the smallest power of two holding every
// address, modulo the count. The gateway's nftables source NAT
// (agent-egress-gw.nft.j2) makes the same choice for traffic the proxy does
// not carry, so a VM leaves from one address whatever the port.
func AddressFor(addresses []netip.Addr, vm netip.Addr) netip.Addr {
	n := len(addresses)
	size := 1
	for size < n {
		size <<= 1
	}
	b := vm.As4()
	return addresses[(int(b[3])&(size-1))%n]
}

// splice copies both ways until each side has finished sending, and
// returns the bytes carried from a to b and from b to a.
func splice(a, b net.Conn) (aToB, bToA int64) {
	var wg sync.WaitGroup
	wg.Add(2)
	half := func(dst, src net.Conn, n *int64) {
		defer wg.Done()
		*n, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		} else {
			_ = dst.Close()
		}
	}
	go half(a, b, &bToA)
	go half(b, a, &aToB)
	wg.Wait()
	return aToB, bToA
}

// publicV4 is a public unicast IPv4 address: never a private, shared,
// loopback, link-local, benchmarking or reserved one. The proxy dials from
// the gateway itself, so the gateway's forward deny does not cover it.
func publicV4(a netip.Addr) bool {
	if !a.Is4() || !a.IsGlobalUnicast() || a.IsPrivate() {
		return false
	}
	for _, p := range deniedV4 {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

var deniedV4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/3"),
}

// validHostname is a lowercase DNS name of at least two labels, not an
// address.
func validHostname(h string) bool {
	if len(h) == 0 || len(h) > 253 || !strings.Contains(h, ".") {
		return false
	}
	if _, err := netip.ParseAddr(h); err == nil {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' {
				return false
			}
		}
	}
	return true
}

// Metrics are the proxy's series (ADR-20261117185346 §Metrics).
type Metrics struct {
	connections *prometheus.CounterVec
	active      prometheus.Gauge
}

// NewMetrics registers the proxy's series on reg.
func NewMetrics(reg prometheus.Registerer, registry *Registry) *Metrics {
	m := &Metrics{
		connections: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agent_egress_connections_total",
			Help: "Agent egress connections by outcome: accepted, or why the proxy refused them."}, []string{"outcome"}),
		active: prometheus.NewGauge(prometheus.GaugeOpts{Name: "agent_egress_active_connections",
			Help: "Agent egress connections the proxy is carrying."}),
	}
	reg.MustRegister(m.connections, m.active, prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "agent_egress_registrations",
		Help: "Agent VMs registered with the proxy."}, func() float64 { return float64(registry.Len()) }))
	return m
}
