package storage

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/region"
)

func TestOCIBuildExportRegionalRoundTrip(t *testing.T) {
	f := newFakeRegistry(t)
	defer f.srv.Close()
	pub, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	relative := region.BuildExportKey("", pub.String(), digest)
	be := f.client(t)
	repo, tag, err := be.plan(relative)
	if err != nil || repo != "build-exports" || len(tag) > 128 {
		t.Fatalf("plan: %s %s %v", repo, tag, err)
	}
	if key, ok := be.unplan(repo, tag); !ok || key != relative {
		t.Fatalf("unplan: %s %v", key, ok)
	}
	for _, key := range []string{
		strings.Replace(relative, digest, strings.ToUpper(digest), 1),
		strings.Replace(relative, pub.String(), uuid.NewString(), 1),
		relative + "/extra", strings.Replace(relative, ".oci.tar", ".tar", 1),
	} {
		if _, _, err := be.plan(key); !errors.Is(err, ErrInvalidKey) {
			t.Fatalf("malformed export: %s %v", key, err)
		}
	}
	legacy, err := NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRegionRouter(map[string]StorageBackend{"eu-export": be}, legacy)
	if err != nil {
		t.Fatal(err)
	}
	key := region.ArtifactKey("eu-export", relative)
	body := []byte("opaque OCI archive bytes")
	if err := router.Put(t.Context(), key, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	fresh, err := NewRegionRouter(map[string]StorageBackend{"eu-export": f.client(t)}, legacy)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := ListArtifactKeys(t.Context(), fresh, region.BuildExportPrefix)
	if err != nil || len(keys) != 1 || keys[0] != key {
		t.Fatalf("fresh inventory: %v %v", keys, err)
	}
	r, err := fresh.Get(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("roundtrip: %q %v", got, err)
	}
	if _, err := legacy.Get(t.Context(), relative); !errors.Is(err, ErrNotFound) {
		t.Fatalf("export leaked to legacy: %v", err)
	}
	if err := fresh.Delete(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.Get(t.Context(), key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted export: %v", err)
	}
}
