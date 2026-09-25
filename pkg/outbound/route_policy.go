package outbound

import (
	"fmt"
	"net/http"
	"strings"
)

const (
	maxAllowedPaths     = 32
	maxAllowedPrefixLen = 512
	maxRoutePathLen     = 2048
)

var allowedHTTPMethods = map[string]struct{}{
	http.MethodGet: {}, http.MethodHead: {}, http.MethodPost: {},
	http.MethodPut: {}, http.MethodPatch: {}, http.MethodDelete: {},
}

// validateRoutePolicy requires explicit HTTP route permissions for a managed
// credential. Empty permissions on an existing row fail closed after migration.
func (i Integration) validateRoutePolicy() error {
	if i.ProviderAuthMode != ProviderAuthManaged {
		if len(i.AllowedMethods) != 0 || len(i.AllowedPathPrefixes) != 0 {
			return fmt.Errorf("%w: route permissions require managed provider authentication", ErrInvalidIntegration)
		}
		return nil
	}
	if path := i.Origin.EscapedPath(); path != "" && !canonicalRoutePath(path) {
		return fmt.Errorf("%w: managed origin path is not canonical", ErrInvalidIntegration)
	}
	if len(i.AllowedMethods) == 0 || len(i.AllowedMethods) > len(allowedHTTPMethods) {
		return fmt.Errorf("%w: managed integration requires allowed methods", ErrInvalidIntegration)
	}
	seenMethods := make(map[string]struct{}, len(i.AllowedMethods))
	for _, method := range i.AllowedMethods {
		if _, ok := allowedHTTPMethods[method]; !ok {
			return fmt.Errorf("%w: unsupported allowed method", ErrInvalidIntegration)
		}
		if _, duplicate := seenMethods[method]; duplicate {
			return fmt.Errorf("%w: duplicate allowed method", ErrInvalidIntegration)
		}
		seenMethods[method] = struct{}{}
	}
	if len(i.AllowedPathPrefixes) == 0 || len(i.AllowedPathPrefixes) > maxAllowedPaths {
		return fmt.Errorf("%w: managed integration requires allowed path prefixes", ErrInvalidIntegration)
	}
	seenPaths := make(map[string]struct{}, len(i.AllowedPathPrefixes))
	for _, prefix := range i.AllowedPathPrefixes {
		if !canonicalRoutePath(prefix) || len(prefix) > maxAllowedPrefixLen || (prefix != "/" && strings.HasSuffix(prefix, "/")) {
			return fmt.Errorf("%w: allowed path prefix is not canonical", ErrInvalidIntegration)
		}
		if _, duplicate := seenPaths[prefix]; duplicate {
			return fmt.Errorf("%w: duplicate allowed path prefix", ErrInvalidIntegration)
		}
		seenPaths[prefix] = struct{}{}
	}
	return nil
}

// AllowsRequest checks the app-supplied suffix, before the fixed origin path
// is prepended and before admission or provider credentials are used. Reject
// ambiguous escaped or dot-segment paths rather than relying on provider URL
// normalization to agree with Gregale's prefix comparison.
func (i Integration) AllowsRequest(method, escapedPath string) bool {
	if i.ProviderAuthMode != ProviderAuthManaged {
		return true
	}
	if !canonicalRoutePath(escapedPath) {
		return false
	}
	methodAllowed := false
	for _, allowed := range i.AllowedMethods {
		if method == allowed {
			methodAllowed = true
			break
		}
	}
	if !methodAllowed {
		return false
	}
	for _, prefix := range i.AllowedPathPrefixes {
		if prefix == "/" || escapedPath == prefix || strings.HasPrefix(escapedPath, prefix+"/") {
			return true
		}
	}
	return false
}

func canonicalRoutePath(path string) bool {
	if path == "" || path[0] != '/' || len(path) > maxRoutePathLen || strings.ContainsAny(path, "%\\;#?") || strings.Contains(path, "//") {
		return false
	}
	for _, segment := range strings.Split(path[1:], "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	for i := 0; i < len(path); i++ {
		if path[i] < 0x21 || path[i] > 0x7e {
			return false
		}
	}
	return true
}

func hasMethodOverride(r *http.Request) bool {
	for _, name := range []string{"X-HTTP-Method-Override", "X-Method-Override", "X-HTTP-Method"} {
		if r.Header.Get(name) != "" {
			return true
		}
	}
	return r.URL.Query().Has("_method")
}
