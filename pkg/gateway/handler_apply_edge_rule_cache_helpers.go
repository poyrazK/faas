package gateway

import (
	"crypto/sha256"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// computeVaryHash hashes the values of the headers listed in
// varyOn into a fixed-size [32]byte digest that fits the
// CacheKey.VaryHash field type. An empty varyOn list returns
// the SHA-256 of the empty string — every request for the
// same rule collapses into one entry, which is the desired
// behaviour for routes that don't vary.
//
// The closed varyOn vocab is enforced at the DTO layer
// (pkg/api/dto.go edgeRuleCacheVaryOnVocab); this function is
// the runtime consumer and trusts that input. Unknown header
// names are read verbatim — the platform neither errors nor
// falls back, since the closed vocab at the validator already
// rejects non-vocab names.
func computeVaryHash(r *http.Request, varyOn []string) [32]byte {
	// The request host is always part of the variant: one app serves many
	// hostnames (custom domains, platform-tenant surfaces), a "*" or
	// wildcard cache rule covers all of them under one rule ID, and the
	// key held only the app, rule, path and query — so one tenant's
	// cached page was served on another tenant's hostname. The Host
	// header is not in r.Header, so vary_on could not express it.
	host := ""
	if r != nil {
		host = hostname(r.Host)
	}
	if len(varyOn) == 0 {
		if host == "" {
			return hashStable("")
		}
		return hashStable("host\x00" + host)
	}
	// Sort so ["Accept-Language", "Accept-Encoding"] and
	// ["Accept-Encoding", "Accept-Language"] produce the same
	// hash for the same request — otherwise two rules with the
	// same semantic intent but different order would create
	// disjoint cache pools.
	sorted := make([]string, len(varyOn))
	copy(sorted, varyOn)
	sort.Strings(sorted)
	h := sha256.New()
	if host != "" {
		h.Write([]byte("host\x00" + host + "\x00\x00"))
	}
	for i, name := range sorted {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(strings.ToLower(name)))
		h.Write([]byte{0})
		// Both allowed Vary headers are HTTP lists. Include every field line;
		// Header.Get alone would discard later language/encoding preferences.
		h.Write([]byte(strings.Join(r.Header.Values(name), ", ")))
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// hashStable returns the SHA-256 of an arbitrary string as a
// fixed-size [32]byte. Used by both computeVaryHash (above) and
// by ResponseCache.HashCacheKey (response_cache.go) so callers
// can compose key prefixes without re-implementing the digest.
func hashStable(s string) [32]byte {
	h := sha256.New()
	h.Write([]byte(s))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// responseCacheKeyForRequest preserves the origin-visible URL. All serve,
// capture and refresh paths use the same key, including its escaping and
// query ordering; decoded paths remain available for rule matching and purge.
func responseCacheKeyForRequest(r *http.Request, appID, deploymentID string, rule *EdgeRuleCacheResolved) CacheKey {
	return CacheKey{
		AppID:          appID,
		DeploymentID:   deploymentID,
		RuleID:         rule.ID,
		Method:         r.Method,
		NormalizedPath: r.URL.Path,
		RawPath:        r.URL.RawPath,
		Query:          r.URL.RawQuery,
		ForceQuery:     r.URL.ForceQuery,
		VaryHash:       computeVaryHash(r, rule.VaryOn),
	}
}

// strconvItoa is a thin wrapper to keep the cache applier free
// of an explicit strconv import at every call site that wants
// to render an integer into a header value. It exists solely to
// mirror the single-purpose helper pattern used elsewhere in
// pkg/gateway (see public_auth_cache.go).
func strconvItoa(n int) string {
	return strconv.Itoa(n)
}
