package imaged

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/storage"
)

const memoLayerKey = "apps/app/dep1.ext4"

// stagedCopyBackend has local files but is not the in-place local backend, as
// the production node cache in front of GCS is: the scan reads a staged copy.
type stagedCopyBackend struct{ *storage.LocalStorageBackend }

// remoteOnlyBackend keeps nothing in a local file.
type remoteOnlyBackend struct{ storage.StorageBackend }

func newMemoBackend(t *testing.T) *storage.LocalStorageBackend {
	t.Helper()
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return be
}

func putLayer(t *testing.T, be storage.StorageBackend, key, body string) {
	t.Helper()
	if err := be.Put(context.Background(), key, strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}
}

func localLayerPath(t *testing.T, be *storage.LocalStorageBackend, key string) string {
	t.Helper()
	path, ok, err := be.LocalPath(key)
	if err != nil || !ok {
		t.Fatalf("LocalPath(%q) = %q, %v, %v", key, path, ok, err)
	}
	return path
}

func TestPublishedLayerDigests(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, be *storage.LocalStorageBackend)
		want   bool
	}{
		{name: "the published file", want: true},
		{name: "a file published again", change: func(t *testing.T, be *storage.LocalStorageBackend) {
			putLayer(t, be, memoLayerKey, "layer v1")
		}},
		{name: "a file grown in place", change: func(t *testing.T, be *storage.LocalStorageBackend) {
			f, err := os.OpenFile(localLayerPath(t, be, memoLayerKey), os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			if _, err := f.WriteString("more"); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a file removed", change: func(t *testing.T, be *storage.LocalStorageBackend) {
			if err := os.Remove(localLayerPath(t, be, memoLayerKey)); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			be := newMemoBackend(t)
			putLayer(t, be, memoLayerKey, "layer v1")
			m := &publishedLayerDigests{}
			m.record(be, memoLayerKey, "sha256:recorded")
			if tc.change != nil {
				tc.change(t, be)
			}
			digest, _, ok := m.take(be, memoLayerKey)
			if ok != tc.want || (ok && digest != "sha256:recorded") {
				t.Fatalf("take = %q, %v; want hit %v", digest, ok, tc.want)
			}
			if _, _, again := m.take(be, memoLayerKey); again {
				t.Fatal("an entry was used twice")
			}
		})
	}
}

func TestPublishedLayerDigestsRecordOnlyLocalFiles(t *testing.T) {
	be := newMemoBackend(t)
	putLayer(t, be, memoLayerKey, "layer v1")
	remote := remoteOnlyBackend{be}
	m := &publishedLayerDigests{}
	m.record(remote, memoLayerKey, "sha256:recorded")
	if _, _, ok := m.take(be, memoLayerKey); ok {
		t.Fatal("recorded a layer the backend keeps in no local file")
	}

	var none *publishedLayerDigests
	none.record(be, memoLayerKey, "sha256:recorded")
	if _, _, ok := none.take(be, memoLayerKey); ok {
		t.Fatal("a nil memo reported a hit")
	}
}

func TestPublishedLayerDigestsStayBounded(t *testing.T) {
	be := newMemoBackend(t)
	m := &publishedLayerDigests{}
	last := ""
	for i := 0; i <= api.ImagedPublishedLayerDigests; i++ {
		last = fmt.Sprintf("apps/app/dep%d.ext4", i)
		putLayer(t, be, last, "layer")
		m.record(be, last, "sha256:recorded")
	}
	if len(m.entries) != api.ImagedPublishedLayerDigests {
		t.Fatalf("%d entries, want the bound %d", len(m.entries), api.ImagedPublishedLayerDigests)
	}
	if _, _, ok := m.take(be, last); !ok {
		t.Fatal("the newest entry was evicted")
	}
}

// The recorded digest is deliberately not the bytes' hash, so a result equal
// to it proves the scan did not hash the file.
func TestScanArtifactDigestUsesTheRecordedDigestOnlyForTheStoredBytes(t *testing.T) {
	other := filepath.Join(t.TempDir(), "rootfs.ext4")
	if err := os.WriteFile(other, []byte("other bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	otherDigest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("other bytes")))
	for _, tc := range []struct {
		name   string
		staged bool
		scan   func(be *storage.LocalStorageBackend) string
		want   string
	}{
		{name: "a copy staged from the node cache", staged: true, scan: func(*storage.LocalStorageBackend) string { return filepath.Dir(other) }, want: "sha256:recorded"},
		{name: "the local backend's stored file in place", scan: func(be *storage.LocalStorageBackend) string { return localLayerPath(t, be, memoLayerKey) }, want: "sha256:recorded"},
		{name: "another file read in place", scan: func(*storage.LocalStorageBackend) string { return other }, want: otherDigest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local := newMemoBackend(t)
			var be storage.StorageBackend = local
			if tc.staged {
				be = stagedCopyBackend{local}
			}
			putLayer(t, be, memoLayerKey, "layer v1")
			h := &Handler{layerDigests: &publishedLayerDigests{}}
			h.layerDigests.record(be, memoLayerKey, "sha256:recorded")
			got, err := h.scanArtifactDigest(be, memoLayerKey, tc.scan(local))
			if err != nil || got != tc.want {
				t.Fatalf("scanArtifactDigest = %q, %v; want %q", got, err, tc.want)
			}
		})
	}

	h := &Handler{layerDigests: &publishedLayerDigests{}}
	if got, err := h.scanArtifactDigest(newMemoBackend(t), memoLayerKey, other); err != nil || got != otherDigest {
		t.Fatalf("without a recorded digest = %q, %v; want the file's hash %q", got, err, otherDigest)
	}
}

func TestBuildAppLayerRecordsThePublishedDigest(t *testing.T) {
	be := newMemoBackend(t)
	h := &Handler{builder: &fakeBuilder{}, layerDigests: &publishedLayerDigests{}}
	result, err := h.buildAppLayer(context.Background(), rootfs.BuildInput{Storage: be, StorageKey: memoLayerKey})
	if err != nil {
		t.Fatal(err)
	}
	digest, _, ok := h.layerDigests.take(be, memoLayerKey)
	if !ok || digest != result.ArtifactDigest {
		t.Fatalf("recorded %q, %v; want the build's %q", digest, ok, result.ArtifactDigest)
	}
	if stored, err := digestScanArtifact(localLayerPath(t, be, memoLayerKey)); err != nil || stored != result.ArtifactDigest {
		t.Fatalf("the build's digest %q is not the stored bytes' %q (%v)", result.ArtifactDigest, stored, err)
	}
}
