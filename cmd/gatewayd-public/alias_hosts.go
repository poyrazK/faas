package main

import (
	"net"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid"
	"github.com/onebox-faas/faas/pkg/hostidentity"
)

// aliasHosts maps a request for an app host under an alias apps domain to
// the same label under the primary apps domain (ADR-20261026100000), before
// any routing, policy or host classification runs.
//
// In serve mode the request continues with the canonical Host, so every
// edge rule, fence, cache and traffic-policy snapshot downstream (all keyed
// by the request host) applies to the alias exactly as to the canonical
// host: a WAF, IP or JWT rule cannot be bypassed through an alias. The host
// the client asked for travels in X-Forwarded-Host.
//
// In redirect mode the request is answered with a permanent redirect to the
// canonical host and never reaches tenant code. 308 keeps the method and
// body, so API clients that follow redirects replay the request.
//
// A platform-reserved label (s3, ai, registry, www, ...) is never an app, and
// after a move the old apps domain is also the platform zone that serves
// those hosts, so they pass through untouched.
func aliasHosts(next http.Handler, domains hostidentity.Domains) http.Handler {
	if len(domains.AppAliases) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := apid.HostWithoutPort(r.Host)
		canonical, ok := domains.CanonicalAppHost(host)
		if label, _, _ := strings.Cut(host, "."); !ok || canonical == host || api.IsPlatformReservedSlug(label) {
			next.ServeHTTP(w, r)
			return
		}
		if domains.AliasRedirect {
			w.Header().Set("Location", "https://"+canonical+r.URL.RequestURI())
			w.Header().Set("Cache-Control", "public, max-age=3600")
			w.WriteHeader(http.StatusPermanentRedirect)
			return
		}
		original := r.Host
		r = r.Clone(r.Context())
		if _, port, err := net.SplitHostPort(original); err == nil && port != "" {
			canonical = net.JoinHostPort(canonical, port)
		}
		r.Host = canonical
		r.Header.Set("X-Forwarded-Host", original)
		next.ServeHTTP(w, r)
	})
}
