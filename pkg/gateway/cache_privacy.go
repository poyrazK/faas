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
		low := strings.ToLower(value)
		if strings.Contains(low, "no-store") || strings.Contains(low, "private") {
			return true
		}
	}
	return false
}
