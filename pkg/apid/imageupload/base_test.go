package imageupload_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/imageupload"
	"github.com/onebox-faas/faas/pkg/ociarchive"
	"github.com/onebox-faas/faas/pkg/ociarchive/ociarchivetest"
)

// adr: 20261127120000

// writePart writes body as an upload session's .part file.
func writePart(t *testing.T, dir string, body []byte) string {
	t.Helper()
	path := filepath.Join(dir, "upload.part")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func inspect(t *testing.T, body []byte) ociarchive.Summary {
	t.Helper()
	s, err := ociarchive.Inspect(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newStore(t *testing.T) *imageupload.BaseStore {
	t.Helper()
	store, err := imageupload.OpenBaseStore(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// saveBase stores body as app's image base.
func saveBase(t *testing.T, store *imageupload.BaseStore, app string, body []byte) {
	t.Helper()
	path := writePart(t, t.TempDir(), body)
	if err := store.Save(context.Background(), app, path, inspect(t, body)); err != nil {
		t.Fatal(err)
	}
}

func thinOf(t *testing.T, full []byte, base api.ImageBase) []byte {
	t.Helper()
	reuse := map[string]int64{}
	for _, l := range base.Layers {
		reuse[l.Digest] = l.Size
	}
	var out bytes.Buffer
	if _, _, err := ociarchive.Thin(bytes.NewReader(full), &out, reuse); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestBaseStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	if _, err := store.Summary(ctx, "app1"); !errors.Is(err, imageupload.ErrNoBase) {
		t.Fatalf("summary before save: %v", err)
	}
	v1, _ := ociarchivetest.CanonicalLayers(t, "os", "runtime", "app v1")
	saveBase(t, store, "app1", v1)
	base, err := store.Summary(ctx, "app1")
	if err != nil || len(base.Layers) != 3 || base.ManifestDigest != inspect(t, v1).ManifestDigest {
		t.Fatalf("summary = %+v, %v", base, err)
	}

	v2, digest := ociarchivetest.CanonicalLayers(t, "os", "runtime", "app v2")
	thin := thinOf(t, v2, base)
	if len(thin) >= len(v2) {
		t.Fatalf("thin upload is %d bytes, the full image %d", len(thin), len(v2))
	}
	part := writePart(t, t.TempDir(), thin)
	size, prob := store.RebuildProblem(ctx, "app1", part, 1<<30)
	if prob != nil {
		t.Fatal(prob)
	}
	got, err := os.ReadFile(imageupload.FullPath(part))
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(v2)) || !bytes.Equal(got, v2) || inspect(t, got).ManifestDigest != digest {
		t.Fatal("the rebuilt archive is not the full image")
	}
	// The upload stays as sent, so its checksum still verifies and a
	// retried commit rebuilds the same archive again.
	if kept, _ := os.ReadFile(part); !bytes.Equal(kept, thin) {
		t.Fatal("the rebuild changed the uploaded bytes")
	}
	if _, prob := store.RebuildProblem(ctx, "app1", part, 1<<30); prob != nil {
		t.Fatalf("retry: %v", prob)
	}
	if again, _ := os.ReadFile(imageupload.FullPath(part)); !bytes.Equal(again, v2) {
		t.Fatal("the retried rebuild differs")
	}

	if err := store.Delete(ctx, "app1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Summary(ctx, "app1"); !errors.Is(err, imageupload.ErrNoBase) {
		t.Fatalf("summary after delete: %v", err)
	}
	if err := store.Delete(ctx, "app1"); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}

func TestBaseStoreRebuildRefuses(t *testing.T) {
	ctx := context.Background()
	v1, _ := ociarchivetest.CanonicalLayers(t, "os", "app v1")
	v2, _ := ociarchivetest.CanonicalLayers(t, "os", "app v2")
	store := newStore(t)
	saveBase(t, store, "app1", v1)
	base, err := store.Summary(ctx, "app1")
	if err != nil {
		t.Fatal(err)
	}
	thin := thinOf(t, v2, base)

	for name, tc := range map[string]struct {
		app      string
		setup    func()
		maxBytes int64
		code     string
	}{
		"no base": {app: "other", maxBytes: 1 << 30, code: api.CodeImageBaseChanged},
		"base replaced": {app: "app1", maxBytes: 1 << 30, code: api.CodeImageBaseChanged, setup: func() {
			other, _ := ociarchivetest.CanonicalLayers(t, "something else")
			saveBase(t, store, "app1", other)
		}},
		"over the cap": {app: "app1", maxBytes: int64(len(thin)), code: api.CodeSourceInvalid, setup: func() { saveBase(t, store, "app1", v1) }},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.setup != nil {
				tc.setup()
			}
			part := writePart(t, t.TempDir(), thin)
			if _, prob := store.RebuildProblem(ctx, tc.app, part, tc.maxBytes); prob == nil || prob.Code != tc.code {
				t.Fatalf("problem = %+v, want %s", prob, tc.code)
			}
			if got, _ := os.ReadFile(part); !bytes.Equal(got, thin) {
				t.Error("a refused rebuild changed the upload")
			}
		})
	}
}
