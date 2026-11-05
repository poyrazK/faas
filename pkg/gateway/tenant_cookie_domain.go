package gateway

import (
	"strings"

	"github.com/onebox-faas/faas/pkg/hostidentity"
)

// scopeTenantCookie makes a guest Set-Cookie host-only when its Domain
// attribute names a domain the deployment owns (the apps, deploy or platform
// domain, an alias, or a parent of one). Every tenant shares those domains and
// they are not on the Public Suffix List, so a cookie scoped to one was sent to
// every other tenant's app: cookie tossing that fixes sessions or injects
// state across customers. Dropping the attribute keeps the cookie working for
// the app that set it. A custom domain's own Domain attribute is unaffected.
func scopeTenantCookie(name, value string) string {
	if !strings.EqualFold(strings.TrimSpace(name), "Set-Cookie") {
		return value
	}
	return scopeTenantCookieTo(value, hostidentity.Configured().OwnedDomains())
}

func scopeTenantCookieTo(value string, owned []string) string {
	parts := strings.Split(value, ";")
	kept := parts[:1]
	changed := false
	for _, attr := range parts[1:] {
		key, val, _ := strings.Cut(strings.TrimSpace(attr), "=")
		if strings.EqualFold(strings.TrimSpace(key), "domain") && sharedCookieDomain(val, owned) {
			changed = true
			continue
		}
		kept = append(kept, attr)
	}
	if !changed {
		return value
	}
	return strings.Join(kept, ";")
}

func sharedCookieDomain(raw string, owned []string) bool {
	domain := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(raw), "."), "."))
	if domain == "" {
		return false
	}
	for _, d := range owned {
		if domain == d || strings.HasSuffix(d, "."+domain) {
			return true
		}
	}
	return false
}
