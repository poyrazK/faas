// adr: 20261106114500 — a bucket dependency's key_prefix is a literal object
// key prefix.
package gregalemanifest

import (
	"strings"
	"testing"
)

func TestBucketKeyPrefixIsALiteralPrefix(t *testing.T) {
	if err := (&Manifest{Buckets: []BucketDependency{{Bucket: "segments", KeyPrefix: "v1/L0/"}}}).Validate(); err != nil {
		t.Fatalf("literal prefix refused: %v", err)
	}
	for _, prefix := range []string{"v1/*/L0/", "/v1/L0/", "a?b", strings.Repeat("a", 513)} {
		err := (&Manifest{Buckets: []BucketDependency{{Bucket: "segments", KeyPrefix: prefix}}}).Validate()
		if err == nil || !strings.Contains(err.Error(), "key_prefix") {
			t.Errorf("key_prefix %.20q = %v, want a key_prefix error", prefix, err)
		}
	}
}
