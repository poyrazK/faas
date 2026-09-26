package api

import "strings"

// MatchEdgeRuleCORSOrigin mirrors the gateway's allowlist behavior and
// returns the value to stamp in Access-Control-Allow-Origin. An empty string
// means the request origin is not allowed. Results are lower-cased, including
// literal entries, to preserve the gateway's existing behavior.
func MatchEdgeRuleCORSOrigin(allowList []string, origin string) string {
	if origin == "" {
		return ""
	}
	origin = strings.ToLower(origin)
	for _, raw := range allowList {
		allowed := strings.ToLower(raw)
		if allowed == "*" || allowed == origin {
			return allowed
		}
		allowScheme, allowHost, ok := splitCORSOriginScheme(allowed)
		if !ok {
			continue
		}
		requestScheme, requestHost, ok := splitCORSOriginScheme(origin)
		if !ok || allowScheme != requestScheme {
			continue
		}
		if strings.HasPrefix(allowHost, "*.") {
			suffix := allowHost[2:]
			if strings.HasSuffix(requestHost, "."+suffix) && strings.Count(requestHost, ".") == strings.Count(suffix, ".")+1 {
				return allowed
			}
		}
		if strings.HasSuffix(allowHost, ":*") {
			prefix := strings.TrimSuffix(allowHost, ":*")
			if strings.HasPrefix(requestHost, prefix+":") {
				return allowed
			}
		}
	}
	return ""
}

func splitCORSOriginScheme(origin string) (scheme, rest string, ok bool) {
	idx := strings.Index(origin, "://")
	if idx < 0 {
		return "", "", false
	}
	return origin[:idx], origin[idx+3:], true
}
