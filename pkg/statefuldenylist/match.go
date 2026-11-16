package statefuldenylist

import (
	"strings"
)

// Match returns (hint, true) when any path segment of the resolved
// image name (after stripping the registry hostname and tag/digest)
// is in Set, else ("", false).
//
// ref is the full OCI reference; the function is identical to
// pkg/imaged/base.go::StatefulDenyListMatch — duplicated here so
// pkg/api can call it without taking a dependency on pkg/imaged
// (which itself imports pkg/api, an import cycle).
//
// The stripping rules:
//
//  1. Strip the registry hostname — the first slash-separated
//     segment, but ONLY if it looks like a hostname (contains `.`
//     or `:`, or is `localhost`). This is critical: a bare `:tag`
//     (port-less) on `localhost:5000/...` would otherwise be
//     confused with the image tag.
//  2. From the remaining path, strip any trailing `:tag` or
//     `@sha256:…`.
//  3. Split on `/` and lowercase every segment.
//  4. Return the FIRST segment that matches a Set key.
//
// Returns ("", false) on any parse failure — fail-open at the
// matcher so a malformed ref never blocks a deploy (the customer's
// other failures will still fire).
func Match(ref string) (string, bool) {
	if ref == "" {
		return "", false
	}
	for _, seg := range pathSegmentsAfterRegistry(ref) {
		hint, ok := Set[strings.ToLower(seg)]
		if ok {
			return hint, true
		}
	}
	return "", false
}

// pathSegmentsAfterRegistry strips the registry hostname from an OCI
// image ref and returns the remaining path segments (post tag/digest
// strip). Mirrors pkg/imaged/base.go::pathSegmentsAfterRegistry
// verbatim so the apid-side gate and the imaged runtime gate agree
// on every reference shape (one of the cheapest invariants to keep
// load-bearing).
//
//	docker.io/library/postgres:16      → ["library", "postgres"]
//	docker.io/postgres:16              → ["postgres"]
//	ghcr.io/me/myapp:abc1234           → ["me", "myapp"]
//	postgres:16                        → ["postgres"]
//	postgres@sha256:deadbeef           → ["postgres"]
//	localhost:5000/myrepo/postgres:dev → ["myrepo", "postgres"]
//	myreg.example.com/x/y/z:tag        → ["x", "y", "z"]
func pathSegmentsAfterRegistry(ref string) []string {
	slash := strings.Index(ref, "/")
	hasRegistry := false
	if slash > 0 {
		head := ref[:slash]
		if head == "localhost" || strings.ContainsAny(head, ".:") {
			hasRegistry = true
		}
	}
	if hasRegistry {
		ref = ref[slash+1:]
	}
	// Strip trailing tag / digest.
	if at := strings.Index(ref, "@"); at >= 0 {
		ref = ref[:at]
	}
	// A digest may follow a tag (`redis:7@sha256:…`), so strip the tag too;
	// pkg/imaged strips both the same way.
	if colon := strings.LastIndex(ref, ":"); colon > strings.LastIndex(ref, "/") {
		// The last colon sits AFTER the last slash (a port-style or
		// tag-style suffix), not a registry port that we already
		// stripped. Trim it.
		ref = ref[:colon]
	}
	if ref == "" {
		return nil
	}
	parts := strings.Split(ref, "/")
	out := parts[:0:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
