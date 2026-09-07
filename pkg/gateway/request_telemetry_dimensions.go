package gateway

// Privacy-preserving dimensions for customer request analytics. These helpers
// deliberately return bounded categories and never return raw request headers,
// IP addresses, or full referrer URLs.

import (
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

const (
	telemetryUnknownDimension = "__unknown__"
	telemetryNoReferrer       = "__none__"
	telemetryOtherDimension   = "other"
)

// requestTelemetryDimensions returns the only request attributes persisted by
// the analytics data plane. Country uses the same trusted X-Forwarded-For
// chain as the geo edge rule; no IP is retained after the lookup.
func (h *Handler) requestTelemetryDimensions(r *http.Request) (uaFamily, referrerHost, country string) {
	if r == nil {
		return telemetryUnknownDimension, telemetryNoReferrer, telemetryUnknownDimension
	}
	return normalizeTelemetryUserAgent(r.Header.Get("User-Agent")),
		normalizeTelemetryReferrer(r.Referer()),
		h.lookupTelemetryCountry(r)
}

// normalizeTelemetryUserAgent maps arbitrary User-Agent strings to a small,
// documented family set. The raw header must never cross the telemetry seam.
func normalizeTelemetryUserAgent(raw string) string {
	ua := strings.ToLower(strings.TrimSpace(raw))
	if ua == "" {
		return telemetryUnknownDimension
	}
	switch {
	case strings.Contains(ua, "bot"), strings.Contains(ua, "crawler"), strings.Contains(ua, "spider"), strings.Contains(ua, "headless"):
		return "bot"
	case strings.Contains(ua, "edg/") || strings.Contains(ua, "edge/"):
		return "edge"
	case strings.Contains(ua, "opr/") || strings.Contains(ua, "opera"):
		return "opera"
	case strings.Contains(ua, "chrome/") || strings.Contains(ua, "chromium/"):
		return "chrome"
	case strings.Contains(ua, "firefox/"):
		return "firefox"
	case strings.Contains(ua, "safari/"):
		return "safari"
	case strings.Contains(ua, "curl/"):
		return "curl"
	case strings.Contains(ua, "wget/"):
		return "wget"
	case strings.Contains(ua, "python-") || strings.Contains(ua, "python/"):
		return "python"
	case strings.Contains(ua, "go-http-client"):
		return "go"
	case strings.Contains(ua, "java/"):
		return "java"
	default:
		return telemetryOtherDimension
	}
}

// normalizeTelemetryReferrer stores only a lower-case hostname. A malformed,
// absent, or overlong value becomes a bounded sentinel; paths, queries, and
// fragments are intentionally discarded.
func normalizeTelemetryReferrer(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return telemetryNoReferrer
	}
	u, err := url.Parse(raw)
	if err != nil {
		return telemetryOtherDimension
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" {
		return telemetryOtherDimension
	}
	if len([]rune(host)) > 253 || strings.IndexFunc(host, func(r rune) bool {
		return unicode.IsSpace(r) || r == '/' || r == '?' || r == '#'
	}) >= 0 {
		return telemetryOtherDimension
	}
	return host
}

// lookupTelemetryCountry performs a best-effort country lookup using the
// existing geo resolver. A lookup failure is represented by __unknown__ and
// never changes request handling or persists the source IP.
func (h *Handler) lookupTelemetryCountry(r *http.Request) string {
	if h == nil || h.geoReader == nil {
		return telemetryUnknownDimension
	}
	ip, ok := clientIPFromTrustedXFF(r)
	if !ok {
		return telemetryUnknownDimension
	}
	country, found, err := h.geoReader.Lookup(ip)
	if err != nil || !found {
		return telemetryUnknownDimension
	}
	country = strings.ToUpper(strings.TrimSpace(country))
	if len(country) != 2 || country[0] < 'A' || country[0] > 'Z' || country[1] < 'A' || country[1] > 'Z' {
		return telemetryUnknownDimension
	}
	return country
}
