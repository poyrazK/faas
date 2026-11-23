package storagekey_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/storagekey"
)

func TestKernelForDigest(t *testing.T) {
	hexDigest := strings.Repeat("ab", 32)
	cases := []struct {
		name, version, digest, want string
	}{
		{"plain hex", "1.17.0", hexDigest, "kernel/1.17.0-" + hexDigest},
		{"prefixed and upper case", "1.17.0", "sha256:" + strings.ToUpper(hexDigest), "kernel/1.17.0-" + hexDigest},
		{"short digest", "1.17.0", "abcd", ""},
		{"not hex", "1.17.0", strings.Repeat("zz", 32), ""},
		{"no version", "", hexDigest, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := storagekey.KernelForDigest(tc.version, tc.digest); got != tc.want {
				t.Fatalf("KernelForDigest(%q, %q) = %q, want %q", tc.version, tc.digest, got, tc.want)
			}
		})
	}
}
