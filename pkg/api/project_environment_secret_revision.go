package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// ProjectEnvironmentSecretRevision contains only non-secret metadata used to
// bind a qualification to the revisions that its probes observed. It must
// never contain a secret value, ciphertext, or value hash.
type ProjectEnvironmentSecretRevision struct {
	Key                  string
	Version              int64
	ManagedBy            string
	BindingID            string
	CredentialGeneration int64
}

// ProjectEnvironmentSecretRevisionHash returns an opaque, deterministic
// fingerprint of one workload's secret revision metadata. Sorting and the
// versioned domain separator keep the value stable without hashing secret
// material or value hashes.
func ProjectEnvironmentSecretRevisionHash(revisions []ProjectEnvironmentSecretRevision) (string, error) {
	canonical := append([]ProjectEnvironmentSecretRevision(nil), revisions...)
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].Key < canonical[j].Key })
	for i, revision := range canonical {
		if revision.Key == "" || revision.Version < 0 || revision.CredentialGeneration < 0 {
			return "", fmt.Errorf("invalid secret revision metadata")
		}
		if revision.ManagedBy != "" && revision.ManagedBy != "managed_postgres" && revision.ManagedBy != "object_storage" {
			return "", fmt.Errorf("invalid managed secret owner")
		}
		if i > 0 && canonical[i-1].Key == revision.Key {
			return "", fmt.Errorf("duplicate secret revision key")
		}
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("encode secret revision metadata: %w", err)
	}
	payload := append([]byte("gregale-project-environment-secret-revisions-v1\n"), raw...)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
