package edgetopology

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/miekg/dns"
	"github.com/onebox-faas/faas/pkg/api"
)

// DNSAuthority is a reviewed name-to-literal-TCP-endpoint declaration. Neither
// DNS discovery nor a plaintext DNS response authenticates that declaration.
type DNSAuthority struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`
}

type DNSQuestion struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// ServedDNSReview selects concrete, exact DNS-only A/AAAA/CNAME RRsets. Other
// records, wildcard expansion, proxied/flattened origins and external aliases
// cannot acquire coverage by omission. All reviewed parent/child endpoints are
// required; missing or unreachable endpoints never disappear through probing.
type ServedDNSReview struct {
	ConfigSHA256 string         `json:"config_sha256"`
	ParentZone   string         `json:"parent_zone"`
	Parents      []DNSAuthority `json:"parents"`
	Authorities  []DNSAuthority `json:"authorities"`
	Questions    []DNSQuestion  `json:"questions"`
}

type DNSGlue struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type DNSWitness struct {
	Authority      string    `json:"authority"`
	Address        string    `json:"address"`
	Name           string    `json:"name"`
	Type           string    `json:"type"`
	Authoritative  bool      `json:"authoritative"`
	Values         []string  `json:"values"`
	Glue           []DNSGlue `json:"glue"`
	MinimumTTL     uint32    `json:"minimum_ttl"`
	ResponseSHA256 string    `json:"response_sha256"`
}

// ServedDNSObservation reports only the explicit endpoints and selected RRsets
// observed twice. Neither TTL nor SOA serial stability grants a future lease,
// DNSSEC authentication, cache expiry or predecessor retirement authority.
type ServedDNSObservation struct {
	Provider  CloudflareDNSInventory `json:"provider"`
	Review    ServedDNSReview        `json:"review"`
	Rounds    [][]DNSWitness         `json:"rounds"`
	CheckedAt time.Time              `json:"checked_at"`
}

func (p *CloudflareDNSProbe) ObserveServed(ctx context.Context, review ServedDNSReview) (ServedDNSObservation, error) {
	frozen, err := freezeServedDNSReview(p.zone.Name, review)
	if err != nil {
		return ServedDNSObservation{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.RuntimeUpgradeServedDNSTotalTimeout)
	defer cancel()
	before, err := p.Collect(ctx)
	if err != nil {
		return ServedDNSObservation{}, err
	}
	expected, err := selectedDNSValues(before, frozen)
	if err != nil {
		return ServedDNSObservation{}, err
	}
	first, err := observeDNSRound(ctx, before, frozen, expected)
	if err != nil {
		return ServedDNSObservation{}, err
	}
	second, err := observeDNSRound(ctx, before, frozen, expected)
	if err != nil {
		return ServedDNSObservation{}, err
	}
	after, err := p.Collect(ctx)
	if err != nil {
		return ServedDNSObservation{}, err
	}
	if before.ConfigSHA256 != after.ConfigSHA256 || !sameDNSRound(first, second) || ctx.Err() != nil {
		return ServedDNSObservation{}, fmt.Errorf("%w: provider or served scope changed during observation", ErrDNSUnverified)
	}
	return ServedDNSObservation{Provider: before, Review: frozen, Rounds: [][]DNSWitness{first, second}, CheckedAt: time.Now().UTC()}, nil
}

func freezeServedDNSReview(zone string, review ServedDNSReview) (ServedDNSReview, error) {
	if !canonicalDigest(review.ConfigSHA256) || (review.ParentZone != "." && (!dnsName(review.ParentZone, false) || review.ParentZone == zone || !strings.HasSuffix(zone, "."+review.ParentZone))) || len(review.Questions) < 1 || len(review.Questions) > api.RuntimeUpgradeServedDNSQuestionLimit {
		return ServedDNSReview{}, fmt.Errorf("%w: bounded reviewed configuration, parent and questions required", ErrDNSUnverified)
	}
	frozen := ServedDNSReview{ConfigSHA256: review.ConfigSHA256, ParentZone: review.ParentZone, Questions: slices.Clone(review.Questions)}
	var err error
	addresses := make(map[string]bool)
	if frozen.Parents, err = freezeDNSAuthorities(review.Parents, addresses); err != nil {
		return ServedDNSReview{}, err
	}
	if frozen.Authorities, err = freezeDNSAuthorities(review.Authorities, addresses); err != nil {
		return ServedDNSReview{}, err
	}
	seen := make(map[DNSQuestion]bool)
	for _, q := range frozen.Questions {
		if !dnsName(q.Name, true) || strings.Contains(q.Name, "*") || (q.Name != zone && !strings.HasSuffix(q.Name, "."+zone)) || dnsQuestionType(q.Type) == 0 || seen[q] {
			return ServedDNSReview{}, fmt.Errorf("%w: distinct exact in-zone A/AAAA/CNAME questions required", ErrDNSUnverified)
		}
		seen[q] = true
	}
	slices.SortFunc(frozen.Questions, func(a, b DNSQuestion) int { return strings.Compare(a.Name+"/"+a.Type, b.Name+"/"+b.Type) })
	child, parent := authorityEndpointCount(frozen.Authorities), authorityEndpointCount(frozen.Parents)
	if len(addresses) > api.RuntimeUpgradeServedDNSEndpointLimit || 2*(child*(len(frozen.Questions)+2)+parent*3) > api.RuntimeUpgradeServedDNSExchangeLimit {
		return ServedDNSReview{}, fmt.Errorf("%w: served DNS endpoint or exchange bound exceeded", ErrDNSUnverified)
	}
	return frozen, nil
}

func freezeDNSAuthorities(input []DNSAuthority, addresses map[string]bool) ([]DNSAuthority, error) {
	if len(input) < 1 || len(input) > api.RuntimeUpgradeDNSNameServerLimit {
		return nil, fmt.Errorf("%w: bounded complete reviewed authority set required", ErrDNSUnverified)
	}
	output := make([]DNSAuthority, 0, len(input))
	names := make(map[string]bool)
	for _, a := range input {
		if !dnsName(a.Name, false) || names[a.Name] || len(a.Addresses) < 1 || len(a.Addresses) > api.RuntimeUpgradeServedDNSEndpointLimit {
			return nil, fmt.Errorf("%w: canonical distinct nameservers and literal endpoints required", ErrDNSUnverified)
		}
		names[a.Name] = true
		copy := DNSAuthority{Name: a.Name, Addresses: slices.Clone(a.Addresses)}
		for _, address := range copy.Addresses {
			if !dnsEndpoint(address) || addresses[address] {
				return nil, fmt.Errorf("%w: distinct canonical literal DNS endpoints required", ErrDNSUnverified)
			}
			addresses[address] = true
		}
		slices.Sort(copy.Addresses)
		output = append(output, copy)
	}
	slices.SortFunc(output, func(a, b DNSAuthority) int { return strings.Compare(a.Name, b.Name) })
	return output, nil
}

func authorityEndpointCount(authorities []DNSAuthority) int {
	count := 0
	for _, a := range authorities {
		count += len(a.Addresses)
	}
	return count
}

func authorityNames(authorities []DNSAuthority) []string {
	names := make([]string, 0, len(authorities))
	for _, a := range authorities {
		names = append(names, a.Name)
	}
	return names // frozen authorities are already sorted
}

func selectedDNSValues(inventory CloudflareDNSInventory, review ServedDNSReview) (map[DNSQuestion][]string, error) {
	if inventory.ConfigSHA256 != review.ConfigSHA256 || !slices.Equal(inventory.NameServers, authorityNames(review.Authorities)) {
		return nil, fmt.Errorf("%w: provider digest or complete child nameserver set differs from review", ErrDNSUnverified)
	}
	values := make(map[DNSQuestion][]string)
	for _, q := range review.Questions {
		for _, r := range inventory.Records {
			if r.Name != q.Name {
				continue
			}
			if r.Proxied != nil && *r.Proxied {
				return nil, fmt.Errorf("%w: proxied name cannot verify a configured origin", ErrDNSUnverified)
			}
			if r.Type == q.Type {
				if r.Target == "" || r.Proxied == nil || *r.Proxied {
					return nil, fmt.Errorf("%w: explicit DNS-only static RRset required", ErrDNSUnverified)
				}
				values[q] = append(values[q], r.Target)
			}
		}
		if len(values[q]) < 1 || len(values[q]) > api.RuntimeUpgradeServedDNSRRLimit {
			return nil, fmt.Errorf("%w: complete bounded configured RRset required", ErrDNSUnverified)
		}
		slices.Sort(values[q])
		values[q] = slices.Compact(values[q])
	}
	return values, nil
}

func observeDNSRound(ctx context.Context, inventory CloudflareDNSInventory, review ServedDNSReview, expected map[DNSQuestion][]string) ([]DNSWitness, error) {
	var witnesses []DNSWitness
	for _, parent := range review.Parents {
		for _, address := range parent.Addresses {
			for _, kind := range []string{"SOA", "NS", "delegation"} {
				w, err := observeDNSControl(ctx, parent.Name, address, review.ParentZone, inventory.Zone.Name, kind, review)
				if err != nil {
					return nil, err
				}
				witnesses = append(witnesses, w)
			}
		}
	}
	for _, child := range review.Authorities {
		for _, address := range child.Addresses {
			for _, kind := range []string{"SOA", "NS"} {
				w, err := observeDNSControl(ctx, child.Name, address, inventory.Zone.Name, inventory.Zone.Name, kind, review)
				if err != nil {
					return nil, err
				}
				witnesses = append(witnesses, w)
			}
			for _, q := range review.Questions {
				w, err := observeDNSRecord(ctx, child.Name, address, q, inventory.Zone.Name, review.Authorities, expected[q])
				if err != nil {
					return nil, err
				}
				witnesses = append(witnesses, w)
			}
		}
	}
	return witnesses, nil
}

func sameDNSRound(before, after []DNSWitness) bool {
	if len(before) != len(after) {
		return false
	}
	for i, a := range before {
		b := after[i]
		if a.Authority != b.Authority || a.Address != b.Address || a.Name != b.Name || a.Type != b.Type || a.Authoritative != b.Authoritative || !slices.Equal(a.Values, b.Values) || !slices.Equal(a.Glue, b.Glue) {
			return false
		}
	}
	return true // TTLs and fresh transaction IDs are observations, never leases
}

func dnsQuestionType(kind string) uint16 {
	switch kind {
	case "A":
		return dns.TypeA
	case "AAAA":
		return dns.TypeAAAA
	case "CNAME":
		return dns.TypeCNAME
	}
	return 0
}
