package s3gateway

import (
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/objectstorage"
	"golang.org/x/net/http/httpguts"
)

// Apply only after signature verification: SDKs can put these fields in the
// signed query rather than SignedHeaders. Keep the URL intact, and feed the
// authenticated values through the ordinary metadata and integrity checks.
func applyPresignedObjectHeaders(r *http.Request) error {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return objectstorage.ErrInvalid
	}
	headers := make(http.Header)
	for name, values := range query {
		lower := strings.ToLower(name)
		if !presignedObjectHeader(lower) {
			continue
		}
		canonical := http.CanonicalHeaderKey(name)
		if len(values) != 1 || headers[canonical] != nil || !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(values[0]) || values[0] == "" && !strings.HasPrefix(lower, "x-amz-meta-") {
			return objectstorage.ErrInvalid
		}
		for field, existing := range r.Header {
			if strings.EqualFold(field, name) && (len(existing) != 1 || existing[0] != values[0]) {
				return objectstorage.ErrInvalid
			}
		}
		headers.Set(name, values[0])
	}
	for name, values := range headers {
		r.Header[name] = values
	}
	return nil
}

func presignedObjectHeader(lower string) bool {
	return strings.HasPrefix(lower, "x-amz-meta-") || slices.Contains(awsChecksumHeaderNames, lower) || lower == "x-amz-sdk-checksum-algorithm" || lower == "x-amz-tagging" || lower == "x-amz-tagging-directive"
}
