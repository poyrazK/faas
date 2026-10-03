package apihostingreceipt

import (
	"errors"
	"net/url"
	"strings"
	"unicode"
)

var ErrInvalidAPIRouteProbe = errors.New("invalid API route probe")

// ValidateAPIRouteProbe restricts contract checks to safe, static GET paths on
// the already configured app origin. It rejects query strings, fragments,
// authority overrides, path templates, traversal, and encoded separators.
func ValidateAPIRouteProbe(probe APIRouteProbe) error {
	if probe.Method != "GET" || len(probe.Path) == 0 || len(probe.Path) > 1024 ||
		!strings.HasPrefix(probe.Path, "/") || strings.HasPrefix(probe.Path, "//") ||
		strings.ContainsAny(probe.Path, "?#{}\\") || strings.Contains(strings.ToLower(probe.Path), "%2f") ||
		strings.Contains(strings.ToLower(probe.Path), "%5c") ||
		strings.IndexFunc(probe.Path, unicode.IsControl) >= 0 {
		return ErrInvalidAPIRouteProbe
	}
	parsed, err := url.ParseRequestURI(probe.Path)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return ErrInvalidAPIRouteProbe
	}
	decoded, err := url.PathUnescape(probe.Path)
	if err != nil || strings.Contains(decoded, "\\") {
		return ErrInvalidAPIRouteProbe
	}
	for _, segment := range strings.Split(decoded, "/") {
		if segment == "." || segment == ".." {
			return ErrInvalidAPIRouteProbe
		}
	}
	return nil
}
