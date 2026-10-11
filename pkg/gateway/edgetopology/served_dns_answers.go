package edgetopology

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"

	"github.com/miekg/dns"
)

func observeDNSControl(ctx context.Context, authority, address, zone, child string, kind string, review ServedDNSReview) (DNSWitness, error) {
	q := DNSQuestion{Name: zone, Type: kind}
	refs := review.Parents
	if zone == child {
		refs = review.Authorities
	}
	if kind == "delegation" {
		q, refs = DNSQuestion{Name: child, Type: "NS"}, review.Authorities
	}
	reply, digest, err := exchangeServedDNS(ctx, address, q)
	if err != nil {
		return DNSWitness{}, err
	}
	section := reply.Answer
	if kind == "delegation" {
		if reply.Authoritative || len(reply.Answer) != 0 {
			return DNSWitness{}, fmt.Errorf("%w: exact parent referral required", ErrDNSUnverified)
		}
		section = reply.Ns
	} else if !reply.Authoritative || validateDNSAuthoritySection(reply.Ns, zone, refs) != nil {
		return DNSWitness{}, fmt.Errorf("%w: authoritative apex answer required", ErrDNSUnverified)
	}
	values, ttl, err := dnsRRValues(section, q)
	if err != nil || (q.Type == "NS" && !slices.Equal(values, authorityNames(refs))) {
		return DNSWitness{}, fmt.Errorf("%w: complete reviewed NS/SOA scope required", ErrDNSUnverified)
	}
	glue, err := reviewedDNSGlue(reply.Extra, refs)
	if err != nil {
		return DNSWitness{}, err
	}
	return DNSWitness{Authority: authority, Address: address, Name: q.Name, Type: q.Type, Authoritative: reply.Authoritative, Values: values, Glue: glue, MinimumTTL: ttl, ResponseSHA256: digest}, nil
}

func observeDNSRecord(ctx context.Context, authority, address string, q DNSQuestion, zone string, refs []DNSAuthority, expected []string) (DNSWitness, error) {
	reply, digest, err := exchangeServedDNS(ctx, address, q)
	if err != nil {
		return DNSWitness{}, err
	}
	values, ttl, err := dnsRRValues(reply.Answer, q)
	if err != nil || !reply.Authoritative || !slices.Equal(values, expected) || validateDNSAuthoritySection(reply.Ns, zone, refs) != nil {
		return DNSWitness{}, fmt.Errorf("%w: exact authoritative configured DNS-only RRset required", ErrDNSUnverified)
	}
	glue, err := reviewedDNSGlue(reply.Extra, refs)
	if err != nil {
		return DNSWitness{}, err
	}
	return DNSWitness{Authority: authority, Address: address, Name: q.Name, Type: q.Type, Authoritative: true, Values: values, Glue: glue, MinimumTTL: ttl, ResponseSHA256: digest}, nil
}

func validateDNSAuthoritySection(records []dns.RR, zone string, refs []DNSAuthority) error {
	if len(records) == 0 {
		return nil
	}
	values, _, err := dnsRRValues(records, DNSQuestion{Name: zone, Type: "NS"})
	if err != nil || !slices.Equal(values, authorityNames(refs)) {
		return ErrDNSUnverified
	}
	return nil
}

func wireDNSName(name string, owner bool) (string, bool) {
	if name == "." {
		return name, true
	}
	if !strings.HasSuffix(name, ".") {
		return "", false
	}
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	return name, dnsName(name, owner)
}

func dnsRRValues(records []dns.RR, q DNSQuestion) ([]string, uint32, error) {
	if len(records) < 1 || (q.Type == "SOA" && len(records) != 1) || (q.Type == "CNAME" && len(records) != 1) {
		return nil, 0, ErrDNSUnverified
	}
	values := make([]string, 0, len(records))
	ttl := records[0].Header().Ttl
	for _, rr := range records {
		h := rr.Header()
		owner, valid := wireDNSName(h.Name, true)
		if !valid || owner != q.Name || h.Class != dns.ClassINET || h.Rrtype != servedDNSQType(q.Type) {
			return nil, 0, ErrDNSUnverified
		}
		value, err := dnsRRValue(rr)
		if err != nil {
			return nil, 0, err
		}
		ttl = min(ttl, h.Ttl)
		values = append(values, value)
	}
	slices.Sort(values)
	if len(slices.Compact(slices.Clone(values))) != len(values) {
		return nil, 0, fmt.Errorf("%w: duplicate DNS records", ErrDNSUnverified)
	}
	return values, ttl, nil
}

func dnsRRValue(rr dns.RR) (string, error) {
	switch record := rr.(type) {
	case *dns.A:
		ip, err := netip.ParseAddr(record.A.String())
		if err == nil && ip.Is4() {
			return ip.String(), nil
		}
	case *dns.AAAA:
		ip, err := netip.ParseAddr(record.AAAA.String())
		if err == nil && ip.Is6() && !ip.Is4In6() {
			return ip.String(), nil
		}
	case *dns.NS:
		if target, ok := wireDNSName(record.Ns, false); ok && target != "." {
			return target, nil
		}
	case *dns.CNAME:
		if target, ok := wireDNSName(record.Target, false); ok && target != "." {
			return target, nil
		}
	case *dns.SOA:
		ns, nsOK := wireDNSName(record.Ns, false)
		mailbox, mailboxOK := wireDNSName(record.Mbox, true)
		if nsOK && ns != "." && mailboxOK {
			return fmt.Sprintf("%s %s %d %d %d %d %d", ns, mailbox, record.Serial, record.Refresh, record.Retry, record.Expire, record.Minttl), nil
		}
	}
	return "", fmt.Errorf("%w: unsupported or ambiguous served record", ErrDNSUnverified)
}

func reviewedDNSGlue(records []dns.RR, authorities []DNSAuthority) ([]DNSGlue, error) {
	allowed := make(map[string]map[string]bool)
	for _, a := range authorities {
		allowed[a.Name] = make(map[string]bool)
		for _, address := range a.Addresses {
			host, _, _ := net.SplitHostPort(address)
			allowed[a.Name][host] = true
		}
	}
	var glue []DNSGlue
	for _, rr := range records {
		h := rr.Header()
		name, valid := wireDNSName(h.Name, false)
		if !valid || h.Class != dns.ClassINET || (h.Rrtype != dns.TypeA && h.Rrtype != dns.TypeAAAA) {
			return nil, fmt.Errorf("%w: unsupported referral/additional record", ErrDNSUnverified)
		}
		value, err := dnsRRValue(rr)
		if err != nil || !allowed[name][value] {
			return nil, fmt.Errorf("%w: additional address differs from reviewed authority endpoints", ErrDNSUnverified)
		}
		glue = append(glue, DNSGlue{Name: name, Address: value})
	}
	slices.SortFunc(glue, func(a, b DNSGlue) int { return strings.Compare(a.Name+"/"+a.Address, b.Name+"/"+b.Address) })
	if len(slices.Compact(slices.Clone(glue))) != len(glue) {
		return nil, ErrDNSUnverified
	}
	return glue, nil
}
