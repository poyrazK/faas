// Package region defines canonical locality identifiers and artifact namespaces.
package region

import (
	"fmt"
	"strings"
)

// Valid accepts a canonical region or empty placement for legacy workloads.
func Valid(id string) bool {
	if id == "" {
		return true
	}
	if len(id) > 63 || strings.HasPrefix(id, "-") || strings.HasSuffix(id, "-") {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// ArtifactKey binds a new artifact to validated, immutable workload placement.
// Empty placement retains the legacy key. Storage validates the complete key
// and requires an explicit route before serving a regional namespace.
func ArtifactKey(id, key string) string {
	if id == "" {
		return key
	}
	return "regions/" + id + "/" + key
}

// SignatureKey keeps attestations in the same store as their signed artifact.
func SignatureKey(key string) string {
	id, relative, err := SplitArtifactKey(key)
	if err != nil {
		return key // Preserve rejection by the storage boundary.
	}
	return ArtifactKey(id, "sigs/"+relative+".sig")
}

// SplitArtifactKey preserves legacy keys and rejects malformed reserved keys.
func SplitArtifactKey(key string) (id, relative string, err error) {
	if key != "regions" && !strings.HasPrefix(key, "regions/") {
		return "", key, nil
	}
	parts := strings.SplitN(key, "/", 3)
	if len(parts) != 3 || parts[1] == "" || !Valid(parts[1]) ||
		parts[2] == "regions" || strings.HasPrefix(parts[2], "regions/") {
		return "", "", fmt.Errorf("invalid regional artifact key")
	}
	return parts[1], parts[2], nil
}
