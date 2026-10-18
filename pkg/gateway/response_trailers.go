package gateway

import (
	"net/http"
	"strings"
)

// copyBufferedResponseTrailers propagates fields added after a buffering
// writer copied its initial headers. Declared trailers replace any earlier
// values; other existing headers may contain gateway mutations and must stay
// intact. Undeclared trailers use Go's http.TrailerPrefix convention.
func copyBufferedResponseTrailers(dst, src http.Header) {
	for _, declaration := range dst.Values("Trailer") {
		for _, name := range strings.Split(declaration, ",") {
			key := http.CanonicalHeaderKey(strings.TrimSpace(name))
			if key == "" {
				continue
			}
			if values, ok := src[key]; ok {
				dst[key] = append([]string(nil), values...)
			}
		}
	}
	for key, values := range src {
		if _, present := dst[key]; present && !strings.HasPrefix(key, http.TrailerPrefix) {
			continue
		}
		dst[key] = append([]string(nil), values...)
	}
}
