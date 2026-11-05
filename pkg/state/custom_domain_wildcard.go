package state

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// CustomDomainWildcardStore is the optional read seam used by the gateway
// when resolving a hostname beneath a wildcard custom domain. It returns the
// most-specific row even before verification so an unverified reservation can
// safely shadow a broader route. It is intentionally outside Store so narrow
// test doubles remain source-compatible.
type CustomDomainWildcardStore interface {
	WildcardDomainForHost(ctx context.Context, host string) (CustomDomain, error)
}

// unicode.IsSpace's whitespace set, also supplied to SQL btrim so legacy
// suffixes use the same normalization as strings.TrimSpace.
const customDomainTrimCharacters = "\t\n\v\f\r \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000"

func normalizeWildcardDomainHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
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
	if domain == "" || len(domain) > api.TrafficPolicyMaxHostnameBytes || strings.HasSuffix(domain, ".") {
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
	host = normalizeWildcardDomainHost(host)
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

// WildcardProbeLabel is the label DNS/TLS probes of a wildcard row query. Only
// the customer's wildcard record answers it. Probing "www" revoked verified
// wildcards whose zone kept www on another host, because an explicit www
// record overrides the wildcard (bug hunt 32 M4).
const WildcardProbeLabel = "gregale-wildcard-check"

// WildcardProbeHost returns a deterministic concrete host for DNS/TLS probes
// of a wildcard row. It exercises the customer's wildcard record without
// querying the literal '*' label.
func WildcardProbeHost(domain string) (string, bool) {
	suffix, ok := WildcardDomainSuffix(domain)
	if !ok {
		return domain, false
	}
	return WildcardProbeLabel + "." + suffix, true
}

// ForeignVerifiedWildcard returns the verified wildcard custom domain, held
// by a tenant other than app's, that covers domain (an exact name, or every
// name below a wildcard), or "" when there is none. A verified wildcard
// proves control of its zone (ADR-167). A reservation below it needs no DNS
// proof, yet while pending it shadows the wildcard's route and certificate,
// so another tenant must not make one. Tenants are compared with
// AppTenantID, so a company's wildcard covers names for every company app,
// whichever member created the app (ADR-167 amendment 2).
func ForeignVerifiedWildcard(ctx context.Context, store interface {
	DomainByName(context.Context, string) (CustomDomain, error)
	AppByID(context.Context, string) (App, error)
	OrgByID(context.Context, string) (Org, error)
}, domain string, app App) (string, error) {
	tenant, err := AppTenantID(ctx, store, app)
	if err != nil {
		return "", fmt.Errorf("load app tenant: %w", err)
	}
	base := strings.ToLower(strings.TrimSpace(domain))
	if suffix, ok := WildcardDomainSuffix(base); ok {
		base = suffix
	}
	for {
		_, parent, ok := strings.Cut(base, ".")
		if !ok || !strings.Contains(parent, ".") {
			return "", nil
		}
		base = parent
		wildcard, err := store.DomainByName(ctx, "*."+parent)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("load wildcard domain %q: %w", "*."+parent, err)
		}
		if !wildcard.Verified() {
			continue
		}
		holder, err := store.AppByID(ctx, wildcard.AppID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("load wildcard domain app: %w", err)
		}
		holderTenant, err := AppTenantID(ctx, store, holder)
		if err != nil {
			return "", fmt.Errorf("load wildcard domain tenant: %w", err)
		}
		if holderTenant != tenant {
			return wildcard.Domain, nil
		}
	}
}

// AppTenantID identifies the tenant that holds app's names: its shared
// organization, else the account that created it (an app with no
// organization, or one in its creator's personal organization). It is the
// boundary for names, unlike AppResourceAccount, which keeps a company's
// legacy billing on the creator until the company adopts its resources.
func AppTenantID(ctx context.Context, store interface {
	OrgByID(context.Context, string) (Org, error)
}, app App) (string, error) {
	if app.OrgID == "" {
		return "account:" + app.AccountID, nil
	}
	org, err := store.OrgByID(ctx, app.OrgID)
	if err != nil {
		return "", err
	}
	if org.Personal {
		return "account:" + app.AccountID, nil
	}
	return "org:" + org.ID, nil
}

var _ CustomDomainWildcardStore = (*PgStore)(nil)
var _ CustomDomainWildcardStore = (*MemStore)(nil)
var _ TenantSurfaceHostnameStore = (*PgStore)(nil)
var _ TenantSurfaceHostnameStore = (*MemStore)(nil)
