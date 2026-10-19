package fcvm

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
)

func TestScanNamesConfiguredBase(t *testing.T) {
	const digest = "sha256:f868200e3b44cabf4c755d3a2b5b7aa3b0a894fe08f774b59729412436d7b072"
	for _, tc := range []struct {
		name, scan, configured string
		want                   bool
	}{
		{"exact digest", "ghcr.io/p/base-minimal@" + digest, "ghcr.io/p/base-minimal@" + digest, true},
		{"another digest under a digest pin", "ghcr.io/p/base-minimal@sha256:other", "ghcr.io/p/base-minimal@" + digest, false},
		{"exact tag (refusal sidecar)", "ghcr.io/p/base-minimal:latest", "ghcr.io/p/base-minimal:latest", true},
		{"tag resolved to a digest", "ghcr.io/p/base-minimal@" + digest, "ghcr.io/p/base-minimal:latest", true},
		{"untagged repository resolved to a digest", "ghcr.io/p/base-minimal@" + digest, "ghcr.io/p/base-minimal", true},
		{"registry port kept", "localhost:5000/p/base@" + digest, "localhost:5000/p/base:latest", true},
		{"another repository", "ghcr.io/p/runner-node22@" + digest, "ghcr.io/p/base-minimal:latest", false},
		{"another tag", "ghcr.io/p/base-minimal:v2", "ghcr.io/p/base-minimal:latest", false},
		{"not a sha256 digest", "ghcr.io/p/base-minimal@md5:abc", "ghcr.io/p/base-minimal:latest", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := scanNamesConfiguredBase(tc.scan, tc.configured); got != tc.want {
				t.Fatalf("scanNamesConfiguredBase(%q, %q) = %v, want %v", tc.scan, tc.configured, got, tc.want)
			}
		})
	}
}

// A dev or single-box node configured with a tag boots once imaged has
// scanned the digest that tag resolved to; it used to refuse every cold boot.
func TestBringUpScanCheck_AcceptsTheDigestATagResolvedTo(t *testing.T) {
	const baseKey = "base/base-amd64.ext4"
	be := &memStorage{}
	be.blobs = map[string][]byte{
		wire.ScanKeyForBaseKey(baseKey): []byte(`{"image":"ghcr.io/p/base-minimal@sha256:abc","findings":{"CRITICAL":0},"fix_available_findings":{"CRITICAL":0}}`),
	}
	m := &Manager{}
	m.WithStorage(be)
	m.WithBaseGenerations(map[string]string{baseKey: "ghcr.io/p/base-minimal:latest"})
	if err := m.bringUpScanCheck(context.Background(), baseKey); err != nil {
		t.Fatalf("bringUpScanCheck = %v, want the resolved digest accepted for a tag configuration", err)
	}
	m.WithBaseGenerations(map[string]string{baseKey: "ghcr.io/p/other:latest"})
	if err := m.bringUpScanCheck(context.Background(), baseKey); err == nil {
		t.Fatal("a scan of another repository was accepted")
	}
}
