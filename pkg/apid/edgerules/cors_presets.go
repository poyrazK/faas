package edgerules

import (
	"fmt"
	"strings"

	"golang.org/x/net/publicsuffix"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostidentity"
)

// SharedDomainCORSOriginProblem refuses a credentialed CORS policy whose
// allowed origins include a subdomain wildcard under a domain the platform
// owns, such as "https://*.<apps domain>" (ADR-091 amendment 2026-10-28).
// Every customer's app is served one label under the apps domain, so such an
// entry lets any other customer's app send the visitor's cookies and read the
// response. A wildcard directly under a public suffix ("https://*.com",
// "https://*.github.io", "https://*.vercel.app") is refused for the same
// reason: anyone can register a name there (ADR-091 amendment 2026-11-04,
// round 11). It applies to kind=cors rules and CORS presets; an origin under a
// customer's own domain, and an uncredentialed policy, are not affected.
func SharedDomainCORSOriginProblem(origins []string, allowCredentials bool) *api.Problem {
	if !allowCredentials {
		return nil
	}
	domains := hostidentity.Configured()
	for _, origin := range origins {
		_, rest, ok := strings.Cut(strings.ToLower(origin), "://")
		if !ok {
			continue
		}
		suffix, wildcard := strings.CutPrefix(rest, "*.")
		if !wildcard {
			continue
		}
		if host, _, hasPort := strings.Cut(suffix, ":"); hasPort {
			suffix = host
		}
		if domains.Owns(suffix) {
			return api.ErrValidation(fmt.Sprintf("allow_origins entry %q covers every app on the shared %s domain, so with allow_credentials any other customer's app could read responses with your visitors' cookies; list your app origins instead", origin, suffix))
		}
		if publicSuffix(suffix) {
			return api.ErrValidation(fmt.Sprintf("allow_origins entry %q covers every site registered under the public suffix %s, so with allow_credentials any of them could read responses with your visitors' cookies; list your own origins or a wildcard under your own domain instead", origin, suffix))
		}
	}
	return nil
}

// publicSuffix reports a domain anyone can register names directly under:
// an ICANN or private entry of the Public Suffix List, or an unlisted
// top-level label.
func publicSuffix(domain string) bool {
	domain = strings.TrimSuffix(domain, ".")
	if domain == "" {
		return true
	}
	ps, _ := publicsuffix.PublicSuffix(domain)
	return ps == domain
}
