// Package domaindns implements the domain doctor's DNS checks that are not
// available through net.Resolver.
package domaindns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/miekg/dns"
	"github.com/onebox-faas/faas/pkg/api"
)

// LookupCAA uses the system's recursive resolvers and follows CNAMEs and
// parent domains to find the relevant CAA RRset (RFC 8659 section 3).
func LookupCAA(ctx context.Context, host string) ([]string, error) {
	config, err := dns.ClientConfigFromFile("/etc/resolv.conf")
	if err != nil {
		return nil, fmt.Errorf("CAA resolver configuration: %w", err)
	}
	servers := make([]string, len(config.Servers))
	for i, server := range config.Servers {
		servers[i] = net.JoinHostPort(server, config.Port)
	}
	return LookupCAAAt(ctx, host, servers)
}

// LookupCAAAt is LookupCAA with explicit recursive resolver endpoints.
func LookupCAAAt(ctx context.Context, host string, servers []string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, api.DomainDoctorDNSTimeout)
	defer cancel()
	queries := 0
	for parent := dns.Fqdn(strings.ToLower(host)); parent != "." && parent != ""; {
		records, err := lookupCAASet(ctx, parent, servers, &queries)
		if err != nil || len(records) > 0 {
			return records, err
		}
		// RFC 8659 climbs only the original name's parents. An alias's
		// parent's policy must not become a policy on the customer domain.
		_, parent, _ = strings.Cut(parent, ".")
	}
	return nil, nil
}

func lookupCAASet(ctx context.Context, name string, servers []string, queries *int) ([]string, error) {
	seen := make(map[string]bool)
	for *queries < api.DomainDoctorCAAMaxQueries {
		if seen[name] {
			return nil, errors.New("CAA alias loop")
		}
		seen[name] = true
		(*queries)++
		response, err := queryCAA(ctx, name, servers)
		if err != nil {
			return nil, err
		}
		var records []string
		var alias string
		for _, answer := range response.Answer {
			if !strings.EqualFold(answer.Header().Name, name) {
				continue
			}
			switch rr := answer.(type) {
			case *dns.CAA:
				records = append(records, fmt.Sprintf("%d %s %q", rr.Flag, rr.Tag, rr.Value))
			case *dns.CNAME:
				alias = strings.ToLower(rr.Target)
			}
		}
		if len(records) > 0 || alias == "" {
			return records, nil
		}
		name = alias
	}
	return nil, errors.New("CAA lookup exceeds query limit")
}

func queryCAA(ctx context.Context, name string, servers []string) (*dns.Msg, error) {
	question := new(dns.Msg).SetQuestion(name, dns.TypeCAA)
	var lastErr error
	for _, server := range servers {
		client := &dns.Client{Timeout: api.DomainDoctorDNSTimeout}
		response, _, err := client.ExchangeContext(ctx, question, server)
		if err == nil && response.Truncated {
			client.Net = "tcp"
			response, _, err = client.ExchangeContext(ctx, question, server)
		}
		if err == nil && (response.Rcode == dns.RcodeSuccess || response.Rcode == dns.RcodeNameError) {
			return response, nil
		}
		if err == nil {
			err = fmt.Errorf("DNS response code %d", response.Rcode)
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no DNS resolver configured")
	}
	return nil, fmt.Errorf("CAA lookup: %w", lastErr)
}

// CAAPermission describes concrete-host issuance with the configured challenges.
// Unknown means an issuer restriction (such as accounturi) cannot be checked
// without access to the edge's ACME account configuration.
type CAAPermission struct {
	Permits  bool
	Unknown  bool
	Observed []string
}

// PermitsLetsEncrypt checks a concrete certificate using any standard ACME
// challenge. Deployment diagnostics should use LetsEncryptPermission instead.
func PermitsLetsEncrypt(records []string) (bool, []string) {
	p := LetsEncryptPermission(records, []string{"dns-01", "http-01", "tls-alpn-01"})
	return p.Permits, p.Observed
}

// LetsEncryptPermission honors issue parameters for the challenges the edge can
// actually complete. Separate issue records are alternatives, not intersections.
func LetsEncryptPermission(records, challenges []string) CAAPermission {
	return letsEncryptPermission(records, challenges, false)
}

// LetsEncryptWildcardPermission uses issuewild when present, otherwise issue
// (RFC 8659). This applies to a wildcard certificate, not concrete leaf hosts
// served below a wildcard routing claim.
func LetsEncryptWildcardPermission(records, challenges []string) CAAPermission {
	return letsEncryptPermission(records, challenges, true)
}

func letsEncryptPermission(records, challenges []string, wildcard bool) CAAPermission {
	p := CAAPermission{}
	wanted := "issue"
	if wildcard {
		for _, record := range records {
			parts := strings.Fields(record)
			if len(parts) >= 3 && strings.EqualFold(parts[1], "issuewild") {
				if _, err := strconv.ParseUint(parts[0], 10, 8); err == nil {
					wanted = "issuewild"
					break
				}
			}
		}
	}
	hasIssue, unknown := false, false
	for _, record := range records {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		p.Observed = append(p.Observed, record)
		parts := strings.Fields(record)
		if len(parts) < 3 {
			continue
		}
		flags, err := strconv.ParseUint(parts[0], 10, 8)
		if err != nil {
			continue
		}
		tag := strings.ToLower(parts[1])
		if flags&128 != 0 && tag != "issue" && tag != "issuewild" && tag != "iodef" {
			p.Permits = false
			return p
		}
		if tag != wanted {
			continue
		}
		hasIssue = true
		value := strings.Join(parts[2:], " ")
		if strings.HasPrefix(value, `"`) {
			value, err = strconv.Unquote(value)
			if err != nil {
				continue
			}
		}
		allowed, uncertain := letsEncryptIssue(value, challenges)
		p.Permits = p.Permits || allowed
		unknown = unknown || uncertain
	}
	p.Permits = !hasIssue || p.Permits
	p.Unknown = !p.Permits && unknown
	return p
}

func letsEncryptIssue(value string, challenges []string) (bool, bool) {
	parts := strings.Split(value, ";")
	if !strings.EqualFold(strings.TrimSpace(parts[0]), "letsencrypt.org") {
		return false, false
	}
	seen, uncertain := make(map[string]bool), false
	for _, parameter := range parts[1:] {
		parameter = strings.TrimSpace(parameter)
		if parameter == "" {
			continue
		}
		key, value, ok := strings.Cut(parameter, "=")
		key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)
		if !ok || key == "" || value == "" || seen[key] {
			return false, false
		}
		seen[key] = true
		switch key {
		case "validationmethods":
			supported := false
			for _, method := range strings.Split(value, ",") {
				for _, challenge := range challenges {
					supported = supported || strings.TrimSpace(method) == challenge
				}
			}
			if !supported {
				return false, false
			}
		default:
			// accounturi and unknown issuer-specific restrictions must never
			// be reported as confirmed merely because the issuer name matches.
			uncertain = true
		}
	}
	return !uncertain, uncertain
}
