package imaged

import (
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// H10-91: production nodes stage bases through a local cache in front of
// shared storage. The verified-base path marked that cache with the
// multi-line digest sidecar value, which MarkGeneration rejects, so the first
// stage of every new base failed and stopped the rc.266 rollout on fsn-2. The
// generation must be the immutable ref vmmd expects for the base.
func TestVerifiedBaseMarksCacheGenerationWithRef(t *testing.T) {
	h, _, _, _, ref := verifiedBaseFixture(t, "complete")
	parent, err := h.storageFor()
	if err != nil {
		t.Fatal(err)
	}
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(t.TempDir(), "cache"), 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	h = h.WithStorage(cache)
	const baseKey, digestKey = "base/verified.ext4", "base/verified.digest"
	if _, err := h.EnsureBaseExt4(t.Context(), ref, baseKey, digestKey, "", "", ""); err != nil {
		t.Fatalf("stage through the node cache: %v", err)
	}
	for _, key := range []string{digestKey, wire.ScanKeyForBaseKey(baseKey), baseKey} {
		if got, ok, err := cache.CachedGeneration(key); err != nil || !ok || got != ref {
			t.Fatalf("CachedGeneration(%q) = %q, %t, %v; want %q", key, got, ok, err, ref)
		}
	}
}
