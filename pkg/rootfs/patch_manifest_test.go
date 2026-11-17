package rootfs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// manifestLayerFixture builds a small app layer ext4 with m embedded where
// a build puts it: under /upper for a two-drive layer.
func manifestLayerFixture(t *testing.T, root string, m api.AppManifest) (PublishedLayer, []byte) {
	t.Helper()
	staging := t.TempDir()
	dir := filepath.Join(staging, root, "etc", "faas")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := api.WriteManifest(&buf, m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.json"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, root, "app.js"), bytes.Repeat([]byte("console.log(1)\n"), 2000), 0o644); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(t.TempDir(), "layer.ext4")
	if err := (wire.ExecRunner{}).Run(context.Background(), MkfsCommand(staging, image, 16)); err != nil {
		t.Fatalf("mkfs: %v", err)
	}
	f, err := os.Open(image) //nolint:forbidigo // the test's own mkfs output in t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	id, err := ReadArtifactIdentity(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	return PublishedLayer{Path: image, Digest: id.Digest, Bytes: id.Bytes}, buf.Bytes()
}

func TestRepublishWithManifestReplacesOnlyTheManifest(t *testing.T) {
	requireE2fsprogs(t)
	t.Setenv("FAAS_BASE_TMP_ROOT", t.TempDir())
	old := api.AppManifest{Entrypoint: []string{"node", "app.js"}, Port: 8080, Env: map[string]string{"A": "1"}}
	updated := old
	updated.Env = map[string]string{"A": "2", "FEATURE": "on"}
	updated.Healthz = "/healthz"
	for _, root := range []string{"upper", "."} {
		t.Run(root, func(t *testing.T) {
			src, _ := manifestLayerFixture(t, root, old)
			before, err := os.ReadFile(src.Path)
			if err != nil {
				t.Fatal(err)
			}
			be, err := storage.NewLocalStorageBackend(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			b := NewBuilder(wire.ExecRunner{})
			id, delta, err := b.RepublishWithManifest(context.Background(), be, "apps/kroki/dep-2.ext4", ManifestPatch{Source: src, Old: old, New: updated})
			if err != nil {
				t.Fatalf("RepublishWithManifest: %v", err)
			}
			if id.Digest == src.Digest || id.Bytes != src.Bytes {
				t.Fatalf("identity = %+v, want new digest and same size as %+v", id, src)
			}
			var want bytes.Buffer
			_ = api.WriteManifest(&want, updated)
			var was bytes.Buffer
			_ = api.WriteManifest(&was, old)
			if delta != int64(want.Len()-was.Len()) {
				t.Fatalf("content delta = %d", delta)
			}
			if after, _ := os.ReadFile(src.Path); !bytes.Equal(after, before) {
				t.Fatal("source layer modified; the patch must work on a copy")
			}
			published := filepath.Join(t.TempDir(), "published.ext4")
			if err := os.WriteFile(published, readStored(t, be, "apps/kroki/dep-2.ext4"), 0o644); err != nil {
				t.Fatal(err)
			}
			manifestPath := api.AppManifestPath
			if root == "upper" {
				manifestPath = "/upper" + api.AppManifestPath
			}
			if got := dumpInit(t, published, manifestPath); !bytes.Equal(got, want.Bytes()) {
				t.Fatalf("published manifest = %s, want %s", got, want.Bytes())
			}
			appJS := "/app.js"
			if root == "upper" {
				appJS = "/upper/app.js"
			}
			if got := dumpInit(t, published, appJS); !bytes.Equal(got, bytes.Repeat([]byte("console.log(1)\n"), 2000)) {
				t.Fatal("app content changed")
			}
			if err := (wire.ExecRunner{}).Run(context.Background(), []string{"e2fsck", "-fn", published}); err != nil {
				t.Fatalf("published layer fails e2fsck: %v", err)
			}
		})
	}
}

func TestRepublishWithManifestIsDeterministic(t *testing.T) {
	requireE2fsprogs(t)
	t.Setenv("FAAS_BASE_TMP_ROOT", t.TempDir())
	old := api.AppManifest{Entrypoint: []string{"node", "app.js"}, Port: 8080}
	updated := old
	updated.Port = 9090
	src, _ := manifestLayerFixture(t, "upper", old)
	var digests []string
	for i := 0; i < 2; i++ {
		be, err := storage.NewLocalStorageBackend(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		id, _, err := NewBuilder(wire.ExecRunner{}).RepublishWithManifest(context.Background(), be, "apps/a/d.ext4", ManifestPatch{Source: src, Old: old, New: updated})
		if err != nil {
			t.Fatal(err)
		}
		digests = append(digests, id.Digest)
	}
	if digests[0] != digests[1] {
		t.Fatalf("two patches of the same layer differ: %v", digests)
	}
}

func TestRepublishWithManifestRefusesAnUnexpectedLayer(t *testing.T) {
	requireE2fsprogs(t)
	t.Setenv("FAAS_BASE_TMP_ROOT", t.TempDir())
	built := api.AppManifest{Entrypoint: []string{"node", "app.js"}, Port: 8080}
	recorded := built
	recorded.Port = 7000 // the record does not describe this layer
	src, _ := manifestLayerFixture(t, "upper", built)
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := NewBuilder(wire.ExecRunner{})
	if _, _, err := b.RepublishWithManifest(context.Background(), be, "k.ext4", ManifestPatch{Source: src, Old: recorded, New: built}); !errors.Is(err, ErrManifestPatchUnsupported) {
		t.Fatalf("err = %v, want ErrManifestPatchUnsupported for a manifest mismatch", err)
	}
	tampered := src
	tampered.Digest = "sha256:" + string(bytes.Repeat([]byte("0"), 64))
	if _, _, err := b.RepublishWithManifest(context.Background(), be, "k.ext4", ManifestPatch{Source: tampered, Old: built, New: built}); !errors.Is(err, ErrManifestPatchUnsupported) {
		t.Fatalf("err = %v, want ErrManifestPatchUnsupported for a changed source", err)
	}
	if _, err := os.Stat(filepath.Join(t.TempDir(), "k.ext4")); err == nil {
		t.Fatal("refused patch published something")
	}
}
