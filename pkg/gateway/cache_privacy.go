package gateway

import (
	"net/http"
	"strings"
)

// responseForbidsSharedCaching applies the origin's privacy veto to both
// cached bodies and the headers reused for anonymous parked HEAD responses.
func responseForbidsSharedCaching(header http.Header) bool {
	if len(header.Values("Set-Cookie")) != 0 {
		return true
	}
	for _, value := range header.Values("Cache-Control") {
		for _, directive := range cacheControlDirectives(value) {
			name, _, _ := strings.Cut(directive, "=")
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "no-cache", "no-store", "private":
				// There is no origin revalidation path. A no-cache response
				// cannot be replayed, even while the rule's TTL is fresh.
				return true
			}
		}
	}
	return false
}

func cacheControlDirectives(value string) []string {
	var result []string
	start := 0
	quoted, escaped := false, false
	for i := 0; i < len(value); i++ {
		if escaped {
			escaped = false
			continue
		}
		if quoted && value[i] == '\\' {
			escaped = true
			continue
		}
		if value[i] == '"' {
			quoted = !quoted
		}
		if value[i] == ',' && !quoted {
			result = append(result, value[start:i])
			start = i + 1
		}
	}
	return append(result, value[start:])
}
