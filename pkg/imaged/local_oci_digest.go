package imaged

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// localOCIManifestDigest verifies the manifest's content address rather than
// allowing an empty source-build identity or trusting the index's claim.
func localOCIManifestDigest(archive string) (string, error) {
	data, err := readLocalOCIEntry(archive, "index.json", 1<<20)
	if err != nil {
		return "", err
	}
	var index localOCIIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return "", err
	}
	if len(index.Manifests) != 1 {
		return "", fmt.Errorf("OCI index has %d manifests, want exactly one", len(index.Manifests))
	}
	digest := index.Manifests[0].Digest
	name, err := localOCIBlobName(digest)
	if err != nil {
		return "", err
	}
	manifest, err := readLocalOCIEntry(archive, name, 8<<20)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(manifest)
	if "sha256:"+hex.EncodeToString(hash[:]) != digest {
		return "", fmt.Errorf("OCI manifest content does not match its digest")
	}
	return digest, nil
}
