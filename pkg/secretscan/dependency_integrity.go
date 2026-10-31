package secretscan

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
)

// Only genuine npm lockfile JSON can opt into checksum entropy suppression.
// Provider signatures still scan every value, including integrity fields.
func isNpmLockfile(path string, data []byte) bool {
	switch filepath.Base(path) {
	case "package-lock.json", "npm-shrinkwrap.json":
	default:
		return false
	}
	var doc struct {
		Version      int                        `json:"lockfileVersion"`
		Packages     map[string]json.RawMessage `json:"packages"`
		Dependencies map[string]json.RawMessage `json:"dependencies"`
	}
	return json.Unmarshal(data, &doc) == nil && doc.Version >= 1 && doc.Version <= 3 && (doc.Packages != nil || doc.Dependencies != nil)
}

func isIntegrityDigest(value []byte) bool {
	var literal string
	if json.Unmarshal(bytes.TrimSuffix(bytes.TrimSpace(value), []byte(",")), &literal) != nil {
		return false
	}
	parts := strings.Fields(literal)
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if !isSRIToken([]byte(part)) {
			return false
		}
	}
	return true
}

// isSRIToken reports whether token is one Subresource Integrity digest,
// "<sha256|sha384|sha512>-<base64 of exactly that digest length>". It is a
// public checksum of a public file (an HTML integrity attribute, a lockfile
// entry), never a credential, so it is exempt from the entropy heuristic.
func isSRIToken(token []byte) bool {
	alg, encoded, ok := strings.Cut(string(token), "-")
	if !ok {
		return false
	}
	length := map[string]int{"sha256": 32, "sha384": 48, "sha512": 64}[alg]
	digest, err := base64.StdEncoding.Strict().DecodeString(encoded)
	return length != 0 && err == nil && len(digest) == length
}
