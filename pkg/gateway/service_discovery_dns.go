package gateway

// The guest service resolver is deliberately a small node-local DNS handler.
// It owns the legacy service suffix and binding-scoped .internal aliases;
// all other names are forwarded to the operator's configured upstream
// resolvers. A DNS answer never grants access by itself: the HTTP service
// proxy still resolves the slug, binds the caller to its source identity,
// and enforces the same-account boundary.

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/miekg/dns"
)

const (
	ServiceDiscoveryDNSPort = 53
	serviceDiscoveryTTL     = 5
	serviceDiscoveryTimeout = 2 * time.Second
)

// ServiceDiscoveryDNSHandler answers private service names on a compute
// node's tenant bridge and forwards ordinary/unbound DNS queries upstream.
type ServiceDiscoveryDNSHandler struct {
	bridgeIP      netip.Addr
	upstream      []string
	log           *slog.Logger
	resolveCaller ServiceProxyCallerResolver
	allowAlias    ServiceAliasAllowed
	blocklist     *DNSBlocklist
	onBlocked     func(category string)
	onResolved    ResolvedEgressHook
}

// WithBlocklist makes the resolver answer NXDOMAIN for names on the ADR-373
// blocklist instead of forwarding them. onBlocked, if set, is called once
// per refused query with the matched category.
func (h *ServiceDiscoveryDNSHandler) WithBlocklist(b *DNSBlocklist, onBlocked func(category string)) *ServiceDiscoveryDNSHandler {
	h.blocklist, h.onBlocked = b, onBlocked
	return h
}

// refuseBlocked answers NXDOMAIN when any question names a blocked domain.
// The caller is resolved for the log only, so abuse can be attributed.
func (h *ServiceDiscoveryDNSHandler) refuseBlocked(w dns.ResponseWriter, req *dns.Msg) bool {
	for _, question := range req.Question {
		category, blocked := h.blocklist.Match(question.Name)
		if !blocked {
			continue
		}
		caller, remote := "", ""
		if addr := w.RemoteAddr(); addr != nil {
			remote = addr.String()
		}
		if h.resolveCaller != nil {
			ctx, cancel := context.WithTimeout(context.Background(), serviceDiscoveryTimeout)
			caller, _ = h.resolveCaller(ctx, remote)
			cancel()
		}
		h.log.Warn("guest DNS lookup blocked", "name", question.Name, "category", category, "app", caller, "remote", remote)
		if h.onBlocked != nil {
			h.onBlocked(category)
		}
		h.writeRcode(w, req, dns.RcodeNameError)
		return true
	}
	return false
}

// ResolvedEgressHook is told the addresses an upstream answer gave a guest
// before the resolver replies (ADR-370 DNS-gated egress). remoteAddr is the
// query's source; ttl is the answer's smallest record TTL.
type ResolvedEgressHook func(ctx context.Context, remoteAddr string, addrs []netip.Addr, ttl time.Duration) error

// WithResolvedEgressHook installs the ADR-370 hook. A hook error is logged
// and the answer is still returned; the guest's connection then fails
// closed at the egress gate instead of the lookup failing.
func (h *ServiceDiscoveryDNSHandler) WithResolvedEgressHook(hook ResolvedEgressHook) *ServiceDiscoveryDNSHandler {
	h.onResolved = hook
	return h
}

// answerAddresses returns the A and AAAA addresses in an answer and the
// smallest TTL among them.
func answerAddresses(resp *dns.Msg) ([]netip.Addr, time.Duration) {
	var addrs []netip.Addr
	var ttl uint32
	for _, rr := range resp.Answer {
		var ip net.IP
		switch r := rr.(type) {
		case *dns.A:
			ip = r.A
		case *dns.AAAA:
			ip = r.AAAA
		default:
			continue
		}
		a, ok := netip.AddrFromSlice(ip)
		if !ok {
			continue
		}
		if len(addrs) == 0 || rr.Header().Ttl < ttl {
			ttl = rr.Header().Ttl
		}
		addrs = append(addrs, a.Unmap())
	}
	return addrs, time.Duration(ttl) * time.Second
}

// NewServiceDiscoveryDNSHandler constructs a resolver for a private bridge
// address. Upstreams may be empty; the public Cloudflare pair is used as a
// bounded fallback so an app's ordinary egress lookups keep working on hosts
// without a readable resolver configuration.
func NewServiceDiscoveryDNSHandler(bridgeIP netip.Addr, upstream []string, log *slog.Logger, resolveCaller ServiceProxyCallerResolver, allowAlias ServiceAliasAllowed) (*ServiceDiscoveryDNSHandler, error) {
	if !bridgeIP.IsValid() || !bridgeIP.Is4() || bridgeIP.IsLoopback() || bridgeIP.IsUnspecified() {
		return nil, fmt.Errorf("service discovery DNS bridge address must be a private IPv4 address")
	}
	if !bridgeIP.IsPrivate() {
		return nil, fmt.Errorf("service discovery DNS bridge address %s is not private", bridgeIP)
	}
	if log == nil {
		log = slog.Default()
	}
	clean := make([]string, 0, len(upstream))
	for _, server := range upstream {
		server = strings.TrimSpace(server)
		if server == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(server); err != nil {
			if ip := net.ParseIP(server); ip != nil {
				server = net.JoinHostPort(server, "53")
			} else {
				continue
			}
		}
		clean = append(clean, server)
	}
	if len(clean) == 0 {
		clean = []string{"1.1.1.1:53", "1.0.0.1:53"}
	}
	return &ServiceDiscoveryDNSHandler{bridgeIP: bridgeIP, upstream: clean, log: log, resolveCaller: resolveCaller, allowAlias: allowAlias}, nil
}

func (h *ServiceDiscoveryDNSHandler) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	if req == nil || len(req.Question) == 0 {
		if req != nil {
			msg := new(dns.Msg)
			msg.SetRcode(req, dns.RcodeFormatError)
			_ = w.WriteMsg(msg)
		}
		return
	}
	if h.refuseBlocked(w, req) {
		return
	}
	private := false
	external := false
	caller := ""
	callerResolved := false
	lookupCtx, cancelLookup := context.WithTimeout(context.Background(), serviceDiscoveryTimeout)
	defer cancelLookup()
	for _, question := range req.Question {
		if serviceDiscoveryName(question.Name) != "" {
			private = true
			continue
		}
		alias := serviceAliasName(question.Name)
		if alias == "" || h.resolveCaller == nil || h.allowAlias == nil {
			external = true
			continue
		}
		if !callerResolved {
			callerResolved = true
			remote := ""
			if addr := w.RemoteAddr(); addr != nil {
				remote = addr.String()
			}
			var err error
			caller, err = h.resolveCaller(lookupCtx, remote)
			if err != nil {
				h.writeRcode(w, req, dns.RcodeServerFailure)
				return
			}
		}
		if caller == "" {
			external = true
			continue
		}
		allowed, err := h.allowAlias(lookupCtx, caller, alias)
		if err != nil {
			h.writeRcode(w, req, dns.RcodeServerFailure)
			return
		}
		if allowed {
			private = true
		} else {
			external = true
		}
	}
	if external {
		if private {
			// Never forward a bound or legacy service name merely because a
			// multi-question packet also contained an external name.
			h.writeRcode(w, req, dns.RcodeFormatError)
			return
		}
		h.forward(w, req)
		return
	}
	// Every question belongs to our private suffix. Respond authoritatively so
	// resolvers do not leak unknown service names to a public upstream.
	msg := new(dns.Msg)
	msg.SetReply(req)
	msg.Authoritative = true
	msg.RecursionAvailable = false
	for _, question := range req.Question {
		switch question.Qtype {
		case dns.TypeA:
			msg.Answer = append(msg.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: question.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: serviceDiscoveryTTL},
				A:   h.bridgeIP.AsSlice(),
			})
		case dns.TypeAAAA, dns.TypeCNAME:
			// The v1 bridge is IPv4-only. An authoritative empty answer for
			// AAAA/CNAME prevents an external resolver from inventing a route.
		default:
			msg.Rcode = dns.RcodeNotImplemented
		}
	}
	if err := w.WriteMsg(msg); err != nil {
		h.log.Debug("service discovery DNS response failed", "err", err)
	}
}

func (h *ServiceDiscoveryDNSHandler) writeRcode(w dns.ResponseWriter, req *dns.Msg, code int) {
	msg := new(dns.Msg)
	msg.SetRcode(req, code)
	if err := w.WriteMsg(msg); err != nil {
		h.log.Debug("service discovery DNS response failed", "err", err)
	}
}

func (h *ServiceDiscoveryDNSHandler) forward(w dns.ResponseWriter, req *dns.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), serviceDiscoveryTimeout)
	defer cancel()
	client := &dns.Client{Timeout: serviceDiscoveryTimeout}
	for _, upstream := range h.upstream {
		resp, _, err := client.ExchangeContext(ctx, req, upstream)
		if err != nil {
			continue
		}
		if h.onResolved != nil {
			if addrs, ttl := answerAddresses(resp); len(addrs) > 0 {
				remote := ""
				if addr := w.RemoteAddr(); addr != nil {
					remote = addr.String()
				}
				if hookErr := h.onResolved(ctx, remote, addrs, ttl); hookErr != nil {
					h.log.Warn("guest DNS answer not registered for egress", "remote", remote, "addresses", len(addrs), "err", hookErr)
				}
			}
		}
		if err := w.WriteMsg(resp); err != nil {
			h.log.Debug("forwarded DNS response failed", "upstream", upstream, "err", err)
		}
		return
	}
	msg := new(dns.Msg)
	msg.SetRcode(req, dns.RcodeServerFailure)
	_ = w.WriteMsg(msg)
}

func serviceDiscoveryName(name string) string {
	return privateServiceName(name, ServiceDiscoveryDomain)
}

func serviceAliasName(name string) string {
	return privateServiceName(name, ServiceAliasDomain)
}

func privateServiceName(name, domain string) string {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	suffix := domain
	if !strings.HasSuffix(name, "."+suffix) {
		return ""
	}
	service := strings.TrimSuffix(name, "."+suffix)
	if !validServiceDNSLabel(service) {
		return ""
	}
	return service
}
