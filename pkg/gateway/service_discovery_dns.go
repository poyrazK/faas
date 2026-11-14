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
	"sync"
	"time"

	"github.com/miekg/dns"
)

const (
	ServiceDiscoveryDNSPort = 53
	// RehearsalServiceDiscoveryDNSPort mirrors netns's private-only DNS port.
	// The vmmd protocol test keeps this agreement across the ownership boundary.
	RehearsalServiceDiscoveryDNSPort = 5354
	// SandboxRegistriesDNSPort mirrors netns's registries-only DNS port for
	// sandbox sessions (ADR-20261027090013 §6); the same vmmd test keeps it.
	SandboxRegistriesDNSPort = 5356
	serviceDiscoveryTTL      = 5
	serviceDiscoveryTimeout  = 2 * time.Second
)

// ServiceDiscoveryDNSHandler answers private service names on a compute
// node's tenant bridge and forwards ordinary/unbound DNS queries upstream.
type ServiceDiscoveryDNSHandler struct {
	bridgeIP        netip.Addr
	upstream        []string
	log             *slog.Logger
	resolveCaller   ServiceProxyCallerResolver
	allowAlias      ServiceAliasAllowed
	blocklist       *DNSBlocklist
	onBlocked       func(category string)
	onResolved      ResolvedEgressHook
	prepareResolved ResolvedEgressPreparation
	egressWarns     guestDNSWarnLimiter
	serviceAddr     ServiceAddressLookup
	scenarioChaos   *serviceDiscoveryDNSChaos
	database        databaseRoute
	objectRelay     string
	privateOnly     bool
	// allowedHost, when set, is the only names the resolver answers: it
	// forwards one question for an allowed name and refuses everything else,
	// private service names included.
	allowedHost func(name string) bool
}

// databaseRoute answers <database-id>.<suffix> with the platform database
// route address (ADR-1027).
type databaseRoute struct {
	suffix string
	addr   netip.Addr
	ttl    uint32
}

// WithDatabaseRoute answers every <label>.<suffix> A query with addr, the
// managed-database route the host DNATs to pgproxyd, and AAAA with nothing.
// The answer grants nothing by itself: pgproxyd authenticates every session
// before it wakes a database. Deeper names and the suffix itself are still
// forwarded upstream.
func (h *ServiceDiscoveryDNSHandler) WithDatabaseRoute(suffix string, addr netip.Addr, ttl time.Duration) (*ServiceDiscoveryDNSHandler, error) {
	suffix = strings.Trim(strings.ToLower(strings.TrimSpace(suffix)), ".")
	if suffix == "" || !addr.Is4() {
		return nil, fmt.Errorf("service discovery DNS: invalid database route %q -> %s", suffix, addr)
	}
	for _, label := range strings.Split(suffix, ".") {
		if !validServiceDNSLabel(label) {
			return nil, fmt.Errorf("service discovery DNS: invalid database route suffix %q", suffix)
		}
	}
	h.database = databaseRoute{suffix: suffix, addr: addr, ttl: uint32(ttl / time.Second)}
	return h, nil
}

func (h *ServiceDiscoveryDNSHandler) databaseRouteName(name string) bool {
	return h.database.suffix != "" && privateServiceName(name, h.database.suffix) != ""
}

// WithObjectRelayName answers name with this node's bridge address: the
// bridge object relay contained environment branch guests dial for object
// storage (ADR-20261031193635 slice 6c). The name follows a restored guest to
// any node, where an address baked into its environment would not.
func (h *ServiceDiscoveryDNSHandler) WithObjectRelayName(name string) *ServiceDiscoveryDNSHandler {
	h.objectRelay = strings.Trim(strings.ToLower(strings.TrimSpace(name)), ".")
	return h
}

func (h *ServiceDiscoveryDNSHandler) objectRelayName(name string) bool {
	return h.objectRelay != "" && strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".") == h.objectRelay
}

// WithPrivateServiceNamesOnly configures the dedicated rehearsal listener.
// It never forwards upstream, resolves aliases or returns direct service
// addresses, including before a caller has published its runtime identity.
func (h *ServiceDiscoveryDNSHandler) WithPrivateServiceNamesOnly() *ServiceDiscoveryDNSHandler {
	h.privateOnly = true
	return h
}

// WithAllowedNamesOnly configures the listener for registries-mode sandbox
// sessions (ADR-20261027090013 §6). It forwards a single A or AAAA question
// for a name allowed reports true, so the ADR-373 hook admits only addresses
// answered for allowed names, and refuses every other query, including
// private service names.
func (h *ServiceDiscoveryDNSHandler) WithAllowedNamesOnly(allowed func(name string) bool) *ServiceDiscoveryDNSHandler {
	h.allowedHost = allowed
	return h
}

// serveAllowedOnly answers a query on an allowed-names listener.
func (h *ServiceDiscoveryDNSHandler) serveAllowedOnly(w dns.ResponseWriter, req *dns.Msg) {
	if len(req.Question) != 1 {
		h.writeRcode(w, req, dns.RcodeRefused)
		return
	}
	q := req.Question[0]
	if (q.Qtype != dns.TypeA && q.Qtype != dns.TypeAAAA) || q.Qclass != dns.ClassINET || !h.allowedHost(q.Name) {
		h.writeRcode(w, req, dns.RcodeRefused)
		return
	}
	h.forward(w, req)
}

// ServiceAddressLookup returns the private service address (ADR-576) the
// caller at remoteAddr should dial for a <service>.svc.gregale name. ok=false
// keeps the tenant-bridge answer, which every caller can always use for HTTP;
// implementations fail safe to it on any doubt or error.
type ServiceAddressLookup func(ctx context.Context, remoteAddr, service string) (addr netip.Addr, ok bool)

// WithServiceAddressLookup answers <service>.svc.gregale with the target's
// service address when the lookup vouches for it (ADR-576). HTTP calls keep
// working because the host forwards the service ports on that address to
// the bridge HTTP proxy. Binding-scoped .internal aliases are unaffected.
func (h *ServiceDiscoveryDNSHandler) WithServiceAddressLookup(lookup ServiceAddressLookup) *ServiceDiscoveryDNSHandler {
	h.serviceAddr = lookup
	return h
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
		ctx, cancel := context.WithTimeout(context.Background(), serviceDiscoveryTimeout)
		h.writeBlockedDNS(ctx, w, req, question.Name, category)
		cancel()
		return true
	}
	return false
}

func (h *ServiceDiscoveryDNSHandler) writeBlockedDNS(ctx context.Context, w dns.ResponseWriter, req *dns.Msg, name, category string) {
	caller, remote := "", remoteAddress(w)
	if h.resolveCaller != nil {
		caller, _ = h.resolveCaller(ctx, remote)
	}
	h.log.Warn("guest DNS lookup blocked", "name", name, "category", category, "app", caller, "remote", remote)
	if h.onBlocked != nil {
		h.onBlocked(category)
	}
	h.writeRcode(w, req, dns.RcodeNameError)
}

// Check the complete upstream answer before returning any records or granting
// their addresses. An unblocked question must not hide a blocked alias target.
func (h *ServiceDiscoveryDNSHandler) refuseBlockedAnswer(ctx context.Context, w dns.ResponseWriter, req, resp *dns.Msg) bool {
	for _, rr := range resp.Answer {
		names := []string{rr.Header().Name}
		switch alias := rr.(type) {
		case *dns.CNAME:
			names = append(names, alias.Target)
		case *dns.DNAME:
			names = append(names, alias.Target)
		}
		for _, name := range names {
			if category, blocked := h.blocklist.Match(name); blocked {
				h.writeBlockedDNS(ctx, w, req, name, category)
				return true
			}
		}
	}
	return false
}

// ResolvedEgressHook is told the addresses an upstream answer gave a guest
// before the resolver replies (ADR-373 DNS-gated egress). remoteAddr is the
// query's source; name is the question, lower-cased without the trailing dot
// (ADR 20261026150000 matches it to bound databases); ttl is the answer's
// smallest record TTL.
type ResolvedEgressHook func(ctx context.Context, remoteAddr, name string, addrs []netip.Addr, ttl time.Duration) error

// ResolvedEgressPreparation captures the query's runtime network owner before
// an upstream exchange can outlive that owner. The returned hook is query-local.
type ResolvedEgressPreparation func(ctx context.Context, remoteAddr, name string) (ResolvedEgressHook, error)

func (h *ServiceDiscoveryDNSHandler) WithResolvedEgressPreparation(prepare ResolvedEgressPreparation) *ServiceDiscoveryDNSHandler {
	h.prepareResolved = prepare
	return h
}

// WithResolvedEgressHook installs the ADR-373 hook. A hook error is logged
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
	if h.allowedHost != nil {
		h.serveAllowedOnly(w, req)
		return
	}
	private := false
	external := false
	caller := ""
	callerResolved := false
	lookupCtx, cancelLookup := context.WithTimeout(context.Background(), serviceDiscoveryTimeout)
	defer cancelLookup()
	contained := h.privateOnly
	for _, question := range req.Question {
		if contained && serviceDiscoveryName(question.Name) == "" {
			h.writeRcode(w, req, dns.RcodeRefused)
			return
		}
		if serviceDiscoveryName(question.Name) != "" || h.databaseRouteName(question.Name) || h.objectRelayName(question.Name) {
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
	if h.applyScenarioDNSChaos(lookupCtx, w, req, remoteAddress(w)) {
		return
	}
	// Every question belongs to our private suffix. Respond authoritatively so
	// resolvers do not leak unknown service names to a public upstream.
	msg := new(dns.Msg)
	msg.SetReply(req)
	msg.Authoritative = true
	msg.RecursionAvailable = false
	remote := remoteAddress(w)
	responseCtx, cancelResponse := context.WithTimeout(context.Background(), serviceDiscoveryTimeout)
	defer cancelResponse()
	for _, question := range req.Question {
		switch question.Qtype {
		case dns.TypeA:
			if h.databaseRouteName(question.Name) {
				msg.Answer = append(msg.Answer, &dns.A{
					Hdr: dns.RR_Header{Name: question.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: h.database.ttl},
					A:   h.database.addr.AsSlice(),
				})
				continue
			}
			answer := h.bridgeIP
			if service := serviceDiscoveryName(question.Name); service != "" && h.serviceAddr != nil && !contained {
				if addr, ok := h.serviceAddr(responseCtx, remote, service); ok && addr.Is4() {
					answer = addr
				}
			}
			msg.Answer = append(msg.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: question.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: serviceDiscoveryTTL},
				A:   answer.AsSlice(),
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

func remoteAddress(w dns.ResponseWriter) string {
	if w == nil || w.RemoteAddr() == nil {
		return ""
	}
	return w.RemoteAddr().String()
}

func (h *ServiceDiscoveryDNSHandler) writeRcode(w dns.ResponseWriter, req *dns.Msg, code int) {
	h.writeRcodeWithAuthority(w, req, code, false)
}

func (h *ServiceDiscoveryDNSHandler) writeRcodeWithAuthority(w dns.ResponseWriter, req *dns.Msg, code int, authoritative bool) {
	msg := new(dns.Msg)
	msg.SetRcode(req, code)
	msg.Authoritative = authoritative
	if err := w.WriteMsg(msg); err != nil {
		h.log.Debug("service discovery DNS response failed", "err", err)
	}
}

func (h *ServiceDiscoveryDNSHandler) forward(w dns.ResponseWriter, req *dns.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), serviceDiscoveryTimeout)
	defer cancel()
	remote := remoteAddress(w)
	name := strings.TrimSuffix(strings.ToLower(req.Question[0].Name), ".")
	hook := h.onResolved
	if h.prepareResolved != nil {
		var err error
		hook, err = h.prepareResolved(ctx, remote, name)
		if err != nil {
			hook = nil
			if h.egressWarns.admit("capture", remote, time.Now()) {
				h.log.Warn("guest DNS owner capture failed", "remote", remote, "err", err)
			}
		}
	}
	for _, upstream := range h.upstream {
		resp, err := exchangeServiceDNS(ctx, req, upstream)
		if err != nil || resp == nil || resp.Truncated || resp.Rcode == dns.RcodeServerFailure {
			continue
		}
		if h.refuseBlockedAnswer(ctx, w, req, resp) {
			return
		}
		if resp.Rcode == dns.RcodeSuccess && hook != nil {
			if addrs, ttl := answerAddresses(resp); len(addrs) > 0 {
				if hookErr := hook(ctx, remote, name, addrs, ttl); hookErr != nil && h.egressWarns.admit("grant", remote, time.Now()) {
					h.log.Warn("guest DNS answer not registered for egress", "remote", remote, "name", name, "addresses", len(addrs), "err", hookErr)
				}
			}
		}
		h.writeForwardedDNS(w, req, resp, upstream)
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

// guestDNSWarnInterval is how often one guest's DNS egress warnings are
// logged: a guest retries a lookup whose answer it cannot reach many times.
const guestDNSWarnInterval = time.Minute

// guestDNSWarnSources bounds the guests guestDNSWarnLimiter remembers.
const guestDNSWarnSources = 4096

// guestDNSWarnLimiter admits one warning per kind and guest address per
// guestDNSWarnInterval. The zero value is ready to use.
type guestDNSWarnLimiter struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (l *guestDNSWarnLimiter) admit(kind, remote string, now time.Time) bool {
	host := remote
	if h, _, err := net.SplitHostPort(remote); err == nil {
		host = h
	}
	key := kind + " " + host
	l.mu.Lock()
	defer l.mu.Unlock()
	if last, ok := l.last[key]; ok && now.Sub(last) < guestDNSWarnInterval {
		return false
	}
	if len(l.last) >= guestDNSWarnSources {
		for k, last := range l.last {
			if now.Sub(last) >= guestDNSWarnInterval {
				delete(l.last, k)
			}
		}
		if len(l.last) >= guestDNSWarnSources {
			clear(l.last)
		}
	}
	if l.last == nil {
		l.last = make(map[string]time.Time)
	}
	l.last[key] = now
	return true
}
