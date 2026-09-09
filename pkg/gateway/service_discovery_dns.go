package gateway

// The guest service resolver is deliberately a small node-local DNS handler.
// It owns only the private service suffix; all other names are forwarded to
// the operator's configured upstream resolvers. A DNS answer never grants
// access by itself: the HTTP service proxy still resolves the slug, binds the
// caller to its source identity, and enforces the same-account boundary.

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

// ServiceDiscoveryDNSHandler answers the private service suffix on a compute
// node's tenant bridge and forwards ordinary DNS queries upstream.
type ServiceDiscoveryDNSHandler struct {
	bridgeIP netip.Addr
	upstream []string
	log      *slog.Logger
}

// NewServiceDiscoveryDNSHandler constructs a resolver for a private bridge
// address. Upstreams may be empty; the public Cloudflare pair is used as a
// bounded fallback so an app's ordinary egress lookups keep working on hosts
// without a readable resolver configuration.
func NewServiceDiscoveryDNSHandler(bridgeIP netip.Addr, upstream []string, log *slog.Logger) (*ServiceDiscoveryDNSHandler, error) {
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
	return &ServiceDiscoveryDNSHandler{bridgeIP: bridgeIP, upstream: clean, log: log}, nil
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
	for _, question := range req.Question {
		if serviceDiscoveryName(question.Name) == "" {
			h.forward(w, req)
			return
		}
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

func (h *ServiceDiscoveryDNSHandler) forward(w dns.ResponseWriter, req *dns.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), serviceDiscoveryTimeout)
	defer cancel()
	client := &dns.Client{Timeout: serviceDiscoveryTimeout}
	for _, upstream := range h.upstream {
		resp, _, err := client.ExchangeContext(ctx, req, upstream)
		if err != nil {
			continue
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
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	suffix := ServiceDiscoveryDomain
	if !strings.HasSuffix(name, "."+suffix) {
		return ""
	}
	service := strings.TrimSuffix(name, "."+suffix)
	if !validServiceDNSLabel(service) {
		return ""
	}
	return service
}
