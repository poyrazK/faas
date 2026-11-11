package s3gateway

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261106114500: a compute binding's key may be limited to object keys
// under one prefix. The gate below runs before any route, so it is an
// allowlist: an object request inside the prefix (and, for a copy, a source
// in the same bucket inside it), a listing whose prefix parameter is inside
// it, a bulk delete (each key is checked once the body is read), a bucket
// HEAD and GetBucketLocation. Everything else, including every bucket-wide
// setting and an unscoped listing, is refused.

// keyScopeListingQueries are the parameters of the listings a scoped key may
// run, by listing subresource ("" is ListObjects and ListObjectsV2).
var keyScopeListingQueries = map[string][]string{
	"":         {"list-type", "prefix", "delimiter", "max-keys", "continuation-token", "start-after", "fetch-owner", "encoding-type", "marker"},
	"versions": {"versions", "prefix", "delimiter", "max-keys", "key-marker", "version-id-marker", "encoding-type"},
	"uploads":  {"uploads", "prefix", "delimiter", "max-uploads", "key-marker", "upload-id-marker", "encoding-type"},
}

// keyInScope reports whether key is under the credential's prefix.
func keyInScope(credential state.ObjectS3Credential, key string) bool {
	return credential.KeyPrefix == "" || strings.HasPrefix(key, credential.KeyPrefix)
}

// keyScopeAllows reports whether a scoped credential may make this request.
// An unscoped credential may make any.
func keyScopeAllows(r *http.Request, credential state.ObjectS3Credential, bucket state.ObjectBucket) bool {
	if credential.KeyPrefix == "" {
		return true
	}
	bucketName, key, hasBucket, hasKey, err := parsePath(r.URL.EscapedPath())
	if err != nil || !hasBucket || bucketName != bucket.Name {
		return false
	}
	query := operationQuery(r.URL.Query())
	if hasKey {
		if !keyInScope(credential, key) {
			return false
		}
		if value := r.Header.Get("x-amz-copy-source"); value != "" {
			source, err := parseCopySource(value)
			if err != nil || source.Bucket != bucket.Name || !keyInScope(credential, source.Key) {
				return false
			}
		}
		return true
	}
	switch {
	case r.Method == http.MethodHead && len(query) == 0:
		return true
	case r.Method == http.MethodGet && queryKeysOnly(query, "location") && query.Has("location"):
		return true
	case r.Method == http.MethodPost && queryKeysOnly(query, "delete") && query.Has("delete"):
		return true
	case r.Method == http.MethodGet:
		subresource := ""
		if query.Has("versions") {
			subresource = "versions"
		} else if query.Has("uploads") {
			subresource = "uploads"
		}
		allowed := keyScopeListingQueries[subresource]
		return queryKeysOnly(query, allowed...) && listingPrefixInScope(credential, query)
	}
	return false
}

// listingPrefixInScope requires one prefix parameter inside the credential's
// prefix, so a listing never names a key outside it.
func listingPrefixInScope(credential state.ObjectS3Credential, query url.Values) bool {
	return len(query["prefix"]) == 1 && keyInScope(credential, query.Get("prefix"))
}

// deleteTargetsInScope reports whether every key of a bulk delete is inside
// the credential's prefix.
func deleteTargetsInScope(credential state.ObjectS3Credential, request deleteObjectsRequest) bool {
	for _, target := range request.Objects {
		if !keyInScope(credential, target.Key) {
			return false
		}
	}
	return true
}
