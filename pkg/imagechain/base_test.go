package imagechain

// adr: 393

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBaseArtifactContractBoundsAndPaths(t *testing.T) {
	for _, key := range []string{"base/parent.ext4", "base/runner-node22-amd64.ext4"} {
		if !ValidBaseKey(key) {
			t.Fatalf("canonical key refused: %q", key)
		}
	}
	for _, key := range []string{"base/.ext4", "base/../escape.ext4", "base/nested/parent.ext4", "apps/app.ext4", "base/parent.ext4\n", "base/\\parent.ext4", "base/" + strings.Repeat("x", api.ApplicationStandardBaseMaxStorageKeyBytes) + ".ext4"} {
		if ValidBaseKey(key) {
			t.Fatalf("invalid key accepted: %q", key)
		}
	}
	artifact := BaseArtifact{StorageKey: "base/parent.ext4", Digest: Digest([]byte("complete bytes")), Bytes: 1}
	for _, size := range []int64{0, -1, api.ApplicationStandardBaseMaxArtifactBytes + 1} {
		candidate := artifact
		candidate.Bytes = size
		if candidate.Valid() {
			t.Fatalf("unbounded artifact size accepted: %d", size)
		}
	}
	materialization := ParentMaterialization{Artifact: artifact, TargetDir: "/dev/shm/faas-base-staging/child"}
	if !materialization.Valid() {
		t.Fatal("canonical parent identity refused")
	}
	for _, target := range []string{"relative", "/dev/shm/faas-base-staging/../child", "/tmp/child\x00", "/" + strings.Repeat("x", api.ApplicationStandardBaseMaxPathBytes)} {
		candidate := materialization
		candidate.TargetDir = target
		if candidate.Valid() {
			t.Fatalf("invalid materialization target accepted: %q", target)
		}
	}
}
