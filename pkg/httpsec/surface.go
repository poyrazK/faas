package httpsec

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/hostidentity"
)

// Surface says who owns a response's security-header policy (ADR-830).
type Surface uint8

const (
	// SurfacePlatform is a Gregale-owned host: the five static headers are
	// forced, as before ADR-830.
	SurfacePlatform Surface = iota
	// SurfaceAppHost is a customer app on a host below the apps domain.
	SurfaceAppHost
	// SurfaceCustomDomain is a customer app on the customer's own domain.
	SurfaceCustomDomain
)

// CustomerOwned reports whether the app's own header values win.
func (s Surface) CustomerOwned() bool { return s != SurfacePlatform }

// ValueHSTSCustomDomain omits includeSubDomains: Gregale does not own the
// customer's other subdomains and must not pin them to HTTPS (ADR-830).
const ValueHSTSCustomDomain = "max-age=31536000"

// ClassifyHost maps a Host header to its Surface for a deployment whose
// platform and apps share one domain (the layout before
// ADR-20261026100000). An empty domain makes every host a platform host.
func ClassifyHost(rawHost, appsDomain string) Surface {
	return ClassifyHostIn(rawHost, hostidentity.Domains{Platform: appsDomain, Apps: appsDomain})
}

// ClassifyHostIn maps a Host header to its Surface. The platform apex, its
// api. and operations. hosts, localhost, IP literals, and every host when no
// platform domain is configured are platform-owned (mirrors
// apid.IsPlatformHost); other hosts under a domain the deployment owns
// (apps domains and their aliases, the deployment-preview domain, the
// platform zone) are app hosts; anything else is a custom domain.
func ClassifyHostIn(rawHost string, d hostidentity.Domains) Surface {
	host := strings.TrimSpace(rawHost)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	platform := strings.Trim(strings.ToLower(strings.TrimSpace(d.Platform)), ".")
	d.Platform = platform
	switch {
	case platform == "", host == "localhost", net.ParseIP(host) != nil, d.IsPlatformHost(host):
		return SurfacePlatform
	case d.Owns(host):
		return SurfaceAppHost
	default:
		return SurfaceCustomDomain
	}
}

type surfaceKey struct{}

// SurfaceFrom returns the Surface recorded by ForSurface, or SurfacePlatform
// when none was recorded (the pre-ADR-830 forced behaviour).
func SurfaceFrom(ctx context.Context) Surface {
	s, _ := ctx.Value(surfaceKey{}).(Surface)
	return s
}

// ForSurface replaces Static on listeners that serve customer apps. It
// records the request's Surface for the proxy hops and sets headers before
// the handler runs:
//
//   - platform: the forced static set, exactly as Static.
//   - customer, fillDefaults=false (gatewayd-internal): nothing, so the app's
//     own value is the only upstream copy.
//   - customer, fillDefaults=true (gatewayd-public): HSTS (without
//     includeSubDomains on custom domains), nosniff, and Referrer-Policy as
//     defaults. The proxy hop replaces a default with the app's value when
//     the app sent one. X-Frame-Options and Permissions-Policy get no
//     platform default on customer responses.
func ForSurface(classify func(*http.Request) Surface, fillDefaults bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		surface := classify(r)
		r = r.WithContext(context.WithValue(r.Context(), surfaceKey{}, surface))
		h := w.Header()
		switch {
		case !surface.CustomerOwned():
			setStatic(h)
		case fillDefaults:
			if hstsEnabled() {
				hsts := ValueHSTSMaxAge
				if surface == SurfaceCustomDomain {
					hsts = ValueHSTSCustomDomain
				}
				h.Set(HeaderStrictTransportSecurity, hsts)
			}
			h.Set(HeaderXContentTypeOptions, ValueXContentTypeOptions)
			h.Set(HeaderReferrerPolicy, ValueReferrerPolicy)
		}
		next.ServeHTTP(w, r)
	})
}

// CopyCustomerStaticHeader merges one upstream copy of a static header into
// dst for a customer-owned response: the first upstream copy replaces the
// platform default, later copies of the same header are appended. replaced
// tracks which headers have already replaced their default.
func CopyCustomerStaticHeader(dst http.Header, name string, values []string, replaced map[string]bool) {
	key := http.CanonicalHeaderKey(name)
	if !replaced[key] {
		dst.Del(key)
		replaced[key] = true
	}
	for _, v := range values {
		dst.Add(key, v)
	}
}
