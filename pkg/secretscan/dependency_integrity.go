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
		alg, encoded, ok := strings.Cut(part, "-")
		if !ok {
			return false
		}
		length := map[string]int{"sha256": 32, "sha384": 48, "sha512": 64}[alg]
		digest, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if length == 0 || err != nil || len(digest) != length {
			return false
		}
	}
	return true
}
