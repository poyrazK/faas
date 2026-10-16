package hosting

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"net"
	"strings"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

// NormalizeDomain admits DNS-validated CNAME subdomains, never platform hosts,
// wildcard names, IPs, apex domains, or private/public-suffix-only names.
func NormalizeDomain(host string, reserved []string) (string, error) {
	host, err := idna.Lookup.ToASCII(strings.ToLower(strings.TrimSpace(host)))
	root, rootErr := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil || rootErr != nil || host == root || len(host) > api.HostingDomainHostnameMaxBytes || net.ParseIP(host) != nil {
		return "", errors.New("use a public CNAME subdomain outside the platform domains")
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("invalid domain label")
		}
		for _, c := range label {
			if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return "", errors.New("invalid domain label")
			}
		}
	}
	for _, domain := range append(append([]string{}, reserved...), "cloudflareaccess.com", "cloudflarestorage.com", "workers.dev") {
		r, e := publicsuffix.EffectiveTLDPlusOne(strings.ToLower(strings.TrimSuffix(domain, ".")))
		if e == nil && root == r {
			return "", errors.New("platform and hosting domains cannot be claimed")
		}
	}
	return host, nil
}

type DomainDNS interface {
	LookupTXT(context.Context, string) ([]string, error)
	LookupCNAME(context.Context, string) (string, error)
}

func VerifyDomainDNS(ctx context.Context, dns DomainDNS, host, challenge, target string) bool {
	values, err := dns.LookupTXT(ctx, "_gregale-hosting."+host)
	if err != nil {
		return false
	}
	owned := false
	for _, value := range values {
		owned = owned || value == "gregale-hosting="+challenge
	}
	if !owned {
		return false
	}
	cname, err := dns.LookupCNAME(ctx, host)
	return err == nil && strings.EqualFold(strings.TrimSuffix(cname, "."), target)
}
