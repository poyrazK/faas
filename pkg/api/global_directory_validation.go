package api

// adr: 613
import (
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

func ValidGlobalAppSlug(slug string) bool {
	if len(slug) < GlobalDirectorySlugMinBytes || len(slug) > GlobalDirectorySlugMaxBytes || slug[0] == '-' || slug[len(slug)-1] == '-' {
		return false
	}
	for _, ch := range slug {
		if ch != '-' && (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') {
			return false
		}
	}
	return !IsReservedAppSlug(slug)
}

// API origins are operator-owned HTTPS origins, without paths or credentials.
// Canonical spelling makes generation changes and SDK resolution unambiguous.
func ValidGlobalDirectoryAPIBase(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
		u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.String() != origin ||
		len(origin) > GlobalDirectoryAPIOriginMaxBytes || strings.ToLower(u.Host) != u.Host {
		return false
	}
	for _, ch := range u.Host {
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && !strings.ContainsRune(".:-[]", ch) {
			return false
		}
	}
	if strings.HasSuffix(u.Host, ":") {
		return false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > GlobalDirectoryOriginPortMax || strconv.Itoa(n) != port {
			return false
		}
	}
	host := u.Hostname()
	if strings.HasPrefix(u.Host, "[") {
		ip, err := netip.ParseAddr(host)
		return err == nil && ip.Is6() && ip.String() == host
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Is4() && ip.String() == host
	}
	if len(host) > GlobalDirectoryOriginHostMaxBytes || strings.ContainsAny(host, ":[]") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > GlobalDirectoryOriginLabelMaxBytes || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
	}
	return true
}
