package httpsec

import (
	"context"
	"net"
	"net/http"
	"strings"
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

// ClassifyHost maps a Host header to its Surface. The apps domain apex, the
// api. and operations. hosts, localhost, IP literals, and every host when no
// apps domain is configured are platform-owned (mirrors apid.IsPlatformHost);
// other hosts below the apps domain are app hosts; anything else is a custom
// domain.
func ClassifyHost(rawHost, appsDomain string) Surface {
	domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(appsDomain)), ".")
	host := strings.TrimSpace(rawHost)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	switch {
	case domain == "", host == "localhost", net.ParseIP(host) != nil,
		host == domain, host == "api."+domain, host == "operations."+domain:
		return SurfacePlatform
	case strings.HasSuffix(host, "."+domain):
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
