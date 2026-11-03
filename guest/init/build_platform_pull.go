package main

import (
	"bytes"
	"strings"
)

// platformImagePrefixes are the image repositories Gregale, not the customer,
// chooses for every build: Railpack's frontend, builder and runtime images.
// The manifest's RuntimeBaseRef is added per build.
var platformImagePrefixes = []string{"ghcr.io/railwayapp/railpack"}

// platformPullFailureMarkers are BuildKit's words for failing to reach or
// read a registry, as opposed to a build step failing.
var platformPullFailureMarkers = []string{
	"failed to resolve source metadata",
	"failed to do request",
	"no such host",
	"i/o timeout",
	"tls handshake timeout",
	"connection refused",
	"connection reset by peer",
	"unexpected status: 5",
	"429 too many requests",
}

// platformPullFailure reports whether build output shows BuildKit failing to
// resolve or pull a platform-owned image for a network or registry reason
// (bug hunt H105). Such a build exited 1 and was recorded as the customer's
// error: a registry or DNS outage during builds told customers to fix their
// code and never reached the build-success SLO.
func platformPullFailure(output []byte, runtimeBaseRef string) bool {
	prefixes := platformImagePrefixes
	if repo := imageRepository(runtimeBaseRef); repo != "" {
		prefixes = append(append([]string(nil), prefixes...), repo)
	}
	// Registry API URLs put /v2/ between the host and the repository
	// (https://ghcr.io/v2/railwayapp/...), so match that form too.
	var forms []string
	for _, prefix := range prefixes {
		prefix = strings.ToLower(prefix)
		forms = append(forms, prefix)
		if host, path, ok := strings.Cut(prefix, "/"); ok {
			forms = append(forms, host+"/v2/"+path)
		}
	}
	for _, line := range bytes.Split(output, []byte("\n")) {
		lower := strings.ToLower(string(line))
		if containsAny(lower, platformPullFailureMarkers) && containsAny(lower, forms) {
			return true
		}
	}
	return false
}

// imageRepository strips the tag and digest from an image reference.
func imageRepository(ref string) string {
	ref = strings.TrimSpace(ref)
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	return ref
}

func containsAny(s string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
