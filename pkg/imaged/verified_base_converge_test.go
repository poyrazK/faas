package imaged

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func readStored(t *testing.T, be storage.StorageBackend, key string) []byte {
	t.Helper()
	rc, err := be.Get(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	raw, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// H10-101: on production-us both compute nodes stage bases through their own
// cache in front of shared storage. A node whose cache still held its own
// earlier build saw the current producer's bytes as "not reusable", rebuilt
// and republished; the other node then did the same. The producers swapped on
// every stage, in-flight scan publications went stale and failed deploys, and
// every swap invalidated the snapshots on that base. A node must adopt the
// canonical publication instead, and the publisher must record its bytes for
// ADR-567 convergence.
func TestVerifiedBaseAdoptsCanonicalPublicationOverStaleCache(t *testing.T) {
	h, _, _, _, ref := verifiedBaseFixture(t, "complete")
	parent, err := h.storageFor()
	if err != nil {
		t.Fatal(err)
	}
	const baseKey, digestKey = "base/verified.ext4", "base/verified.digest"
	rootA := filepath.Join(t.TempDir(), "cache-a")
	cacheA, err := storage.NewLocalCacheBackend(parent, rootA, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	store, ok := h.store.(state.BaseImageProducerStore)
	if !ok {
		t.Fatal("fixture store lacks base producers")
	}
	if _, err := h.WithStorage(cacheA).EnsureBaseExt4(t.Context(), ref, baseKey, digestKey, "", "", ""); err != nil {
		t.Fatalf("publish on node A: %v", err)
	}
	published, err := store.GetCurrentBaseImageProducer(t.Context(), baseKey)
	if err != nil {
		t.Fatal(err)
	}
	canonical := readStored(t, parent, baseKey)

	var content baseContent
	if err := json.Unmarshal(readStored(t, parent, baseContentKey(baseKey)), &content); err != nil {
		t.Fatalf("publisher wrote no ADR-567 content sidecar: %v", err)
	}
	sum := sha256.Sum256(canonical)
	if content.SHA256 != "sha256:"+hex.EncodeToString(sum[:]) || content.Size != int64(len(canonical)) {
		t.Fatalf("content sidecar %+v does not describe the published bytes", content)
	}

	// Node B's cache holds its own earlier build of the same key.
	rootB := filepath.Join(t.TempDir(), "cache-b")
	cacheB, err := storage.NewLocalCacheBackend(parent, rootB, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	staleFile := storage.CacheFileForKey(rootB, baseKey)
	if err := os.MkdirAll(filepath.Dir(staleFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staleFile, []byte("node B's earlier, different ext4 bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta, err := os.ReadFile(storage.CacheFileForKey(rootA, baseKey) + ".meta")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staleFile+".meta", meta, 0o644); err != nil {
		t.Fatal(err)
	}
	if checkStoredBaseArtifact(t.Context(), cacheB, published.Input.Artifact) == nil {
		t.Fatal("fixture: node B's cache must start stale")
	}

	if _, err := h.WithStorage(cacheB).EnsureBaseExt4(t.Context(), ref, baseKey, digestKey, "", "", ""); err != nil {
		t.Fatalf("stage on node B: %v", err)
	}
	current, err := store.GetCurrentBaseImageProducer(t.Context(), baseKey)
	if err != nil {
		t.Fatal(err)
	}
	if current.ID != published.ID {
		t.Fatalf("node B republished the base (producer %s, want %s)", current.ID, published.ID)
	}
	if got := readStored(t, cacheB, baseKey); !bytes.Equal(got, canonical) {
		t.Fatal("node B kept its stale cached copy instead of the canonical publication")
	}
	if !imagechain.ValidBaseKey(baseKey) {
		t.Fatal("fixture key must be a valid base key")
	}
}

// H10-102: function deploys stage a private runtime release through the
// verified base path, whose producer accepts only base/<name>.ext4. The old
// nested base/release-staging/ key failed every function deploy with
// "pin function runtime: state: invalid argument".
func TestRuntimeReleaseStagingKeyIsAValidBaseKey(t *testing.T) {
	for _, runtime := range []string{"node22", "node24", "python312", "python313", "go124", "go124-alpine"} {
		for _, arch := range []string{"amd64", "arm64"} {
			key := runtimeReleaseStagingKey(runtime, arch, "0f8fad5b-d9cb-469f-a165-70867728950e")
			if !imagechain.ValidBaseKey(key) {
				t.Fatalf("staging key %q is not a valid base key", key)
			}
		}
	}
}
