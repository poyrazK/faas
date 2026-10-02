package runtimeadmission

import (
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func artifactSourceFixture() []ArtifactSource {
	digest := "sha256:" + strings.Repeat("a", 64)
	return []ArtifactSource{{Kind: "base-image", StorageKey: "base/a.ext4", Digest: digest, Bytes: 10}, {Kind: "app-layer", StorageKey: "rootfs/main.ext4", Digest: digest, Bytes: 10}, {Kind: "sidecar-layer", WorkloadName: "cache", StorageKey: "rootfs/cache.ext4", Digest: digest, Bytes: 10}}
}

func TestArtifactSourcesRequireCompleteDistinctDriveSet(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func([]ArtifactSource) []ArtifactSource
	}{
		{"missing base", func(a []ArtifactSource) []ArtifactSource { return a[1:] }},
		{"missing sidecar", func(a []ArtifactSource) []ArtifactSource { return a[:2] }},
		{"duplicate role", func(a []ArtifactSource) []ArtifactSource { a[1] = a[0]; return a }},
		{"duplicate key", func(a []ArtifactSource) []ArtifactSource { a[1].StorageKey = a[0].StorageKey; return a }},
		{"unknown kind", func(a []ArtifactSource) []ArtifactSource { a[1].Kind = "future"; return a }},
		{"wrong drive", func(a []ArtifactSource) []ArtifactSource { a[1].StorageKey = "rootfs/other.ext4"; return a }},
		{"wrong workload", func(a []ArtifactSource) []ArtifactSource { a[2].WorkloadName = "other"; return a }},
		{"empty artifact", func(a []ArtifactSource) []ArtifactSource { a[1].Bytes = 0; return a }},
		{"unbounded artifact", func(a []ArtifactSource) []ArtifactSource {
			a[1].Bytes = api.ApplicationStandardBaseMaxArtifactBytes + 1
			return a
		}},
		{"invalid digest", func(a []ArtifactSource) []ArtifactSource { a[1].Digest = "sha256:bad"; return a }},
		{"absolute path", func(a []ArtifactSource) []ArtifactSource { a[1].StorageKey = "/rootfs/main.ext4"; return a }},
		{"traversal", func(a []ArtifactSource) []ArtifactSource { a[1].StorageKey = "rootfs/../main.ext4"; return a }},
		{"noncanonical", func(a []ArtifactSource) []ArtifactSource { a[1].StorageKey = "rootfs//main.ext4"; return a }},
		{"directory", func(a []ArtifactSource) []ArtifactSource { a[1].StorageKey = "."; return a }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := CheckArtifactSources(test.mutate(artifactSourceFixture()), "base/a.ext4", "rootfs/main.ext4", map[string]string{"cache": "rootfs/cache.ext4"}); err == nil {
				t.Fatal("incomplete or substituted source set accepted")
			}
		})
	}
	valid := artifactSourceFixture()
	slices.Reverse(valid)
	if err := CheckArtifactSources(valid, "base/a.ext4", "rootfs/main.ext4", map[string]string{"cache": "rootfs/cache.ext4"}); err != nil {
		t.Fatal(err)
	}
}
