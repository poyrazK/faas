package builderd

import (
	"fmt"
	"strings"
)

// hashAndVerifySource computes the digest of the local archive once and
// compares it with the digest stamped when the deployment was created. Empty
// expected digests are accepted for legacy deployments that predate the
// source-integrity column; the computed digest still feeds cache/provenance.
func hashAndVerifySource(path, expected string) (string, error) {
	actual, err := hashFile(path)
	if err != nil {
		return "", err
	}
	expected = strings.TrimSpace(expected)
	if expected != "" && actual != expected {
		return "", fmt.Errorf("source archive digest mismatch: expected %s, got %s", expected, actual)
	}
	return actual, nil
}
