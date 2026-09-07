package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// CustomDomainWildcardStore is the optional read seam used by the gateway
// when resolving a hostname beneath a wildcard custom domain. It returns the
// most-specific row even before verification so an unverified reservation can
// safely shadow a broader route. It is intentionally outside Store so narrow
// test doubles remain source-compatible.
type CustomDomainWildcardStore interface {
	WildcardDomainForHost(ctx context.Context, host string) (CustomDomain, error)
}

// TenantSurfaceHostnameStore is the optional global read seam used when
// reserving a wildcard. Tenant hostnames are globally unique, so the overlap
// check must consider surfaces owned by other accounts too.
type TenantSurfaceHostnameStore interface {
	ListTenantSurfaceHostnames(ctx context.Context) ([]string, error)
}

// ValidateCustomDomainName accepts an ordinary DNS hostname or a single
// left-most wildcard label ("*.example.com"). Wildcards are deliberately
// limited to one label: this is the shape covered by an ACME wildcard
// certificate and avoids ambiguous nested matches.
func ValidateCustomDomainName(domain string) error {
	if domain == "" || len(domain) > 253 || strings.HasSuffix(domain, ".") {
		return errors.New("domain must be a non-empty DNS name without a trailing dot")
	}
	if strings.ContainsAny(domain, " \t\r\n") {
		return errors.New("domain must not contain whitespace")
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return errors.New("domain must contain at least two labels")
	}
	if labels[0] == "*" {
		if len(labels) < 3 {
			return errors.New("wildcard domain must cover a zone with at least two labels")
		}
		labels = labels[1:]
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("invalid DNS label %q", label)
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') &&
				(r < '0' || r > '9') && r != '-' {
				return fmt.Errorf("invalid character in DNS label %q", label)
			}
		}
	}
	if strings.Contains(domain, "*") && !strings.HasPrefix(domain, "*.") {
		return errors.New("wildcard must be the left-most label")
	}
	return nil
}

// IsWildcardCustomDomain reports whether domain is in the canonical wildcard
// form. Callers should validate input before persisting it.
func IsWildcardCustomDomain(domain string) bool {
	return strings.HasPrefix(strings.ToLower(domain), "*.")
}

// WildcardDomainSuffix returns the covered DNS suffix for a canonical
// wildcard ("*.example.com" → "example.com").
func WildcardDomainSuffix(domain string) (string, bool) {
	if !IsWildcardCustomDomain(domain) {
		return "", false
	}
	suffix := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), "*.")
	if suffix == "" {
		return "", false
	}
	return suffix, true
}

// WildcardMatchesHost reports whether host is a strict subdomain of domain.
// The apex itself is intentionally excluded because *.example.com does not
// cover example.com in either DNS wildcard semantics or an ACME certificate.
func WildcardMatchesHost(domain, host string) bool {
	suffix, ok := WildcardDomainSuffix(domain)
	if !ok {
		return false
	}
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	return !strings.Contains(host, "*") && host != suffix && strings.HasSuffix(host, "."+suffix)
}

// CustomDomainChallengeName is the TXT owner name used by the DNS verifier.
// A wildcard is proved at its zone, never at the syntactically invalid
// "_faas-verify.*.example.com" name.
func CustomDomainChallengeName(domain string) string {
	if suffix, ok := WildcardDomainSuffix(domain); ok {
		return "_faas-verify." + suffix
	}
	return "_faas-verify." + domain
}

// WildcardProbeHost returns a deterministic concrete host for DNS/TLS probes
// of a wildcard row. It exercises the customer's wildcard record without
// querying the literal '*' label.
func WildcardProbeHost(domain string) (string, bool) {
	suffix, ok := WildcardDomainSuffix(domain)
	if !ok {
		return domain, false
	}
	return "www." + suffix, true
}

var _ CustomDomainWildcardStore = (*PgStore)(nil)
var _ CustomDomainWildcardStore = (*MemStore)(nil)
var _ TenantSurfaceHostnameStore = (*PgStore)(nil)
var _ TenantSurfaceHostnameStore = (*MemStore)(nil)
