package imaged

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type keyRecordingBackend struct{ gets []string }

func (b *keyRecordingBackend) Put(context.Context, string, io.Reader) error { return nil }
func (b *keyRecordingBackend) Delete(context.Context, string) error         { return nil }
func (b *keyRecordingBackend) Get(_ context.Context, key string) (io.ReadCloser, error) {
	b.gets = append(b.gets, key)
	if key != "apps/web/source-dep.ext4" && key != "apps/old-web/before-rename.ext4" {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(strings.NewReader("ext4")), nil
}

// Hunt #10: a promoted deployment reuses its source's layer
// (rootfs_key apps/<slug>/<source>.ext4) but the scan read
// apps/<slug>/<promoted>.ext4, missed, and skipped every promotion's scan.
func TestStageScanReadsPromotedDeploymentLayer(t *testing.T) {
	h := newTestHandler(t)
	be := &keyRecordingBackend{}
	app := state.App{Slug: "web"}
	promoted := state.Deployment{ID: "promoted-dep", RootfsKey: "apps/web/source-dep.ext4"}
	dir, cleanup, err := h.stageScanExt4(t.Context(), be, app, promoted)
	if err != nil {
		t.Fatalf("stage promoted layer: %v (gets %v)", err, be.gets)
	}
	cleanup()
	if dir == "" || len(be.gets) != 1 || be.gets[0] != "apps/web/source-dep.ext4" {
		t.Fatalf("staged %q from %v, want the source layer", dir, be.gets)
	}

	for _, tc := range []struct{ key, wantSlug, wantID string }{
		{"", "web", "own-dep"},
		{"apps/web/own-dep.ext4", "web", "own-dep"},
		// The layer of a deployment built before a rename stays under the
		// old slug (bug hunt 2026-10-28 B31).
		{"apps/old-web/own-dep.ext4", "old-web", "own-dep"},
		{"apps/web/../x.ext4", "web", "own-dep"},
		{"apps/../x/own-dep.ext4", "web", "own-dep"},
		{"apps/web/source-dep.ext4", "web", "source-dep"},
	} {
		if slug, id := scanLayer("web", state.Deployment{ID: "own-dep", RootfsKey: tc.key}); slug != tc.wantSlug || id != tc.wantID {
			t.Errorf("scanLayer(%q) = %q, %q, want %q, %q", tc.key, slug, id, tc.wantSlug, tc.wantID)
		}
	}
	renamed := state.Deployment{ID: "before-rename", RootfsKey: "apps/old-web/before-rename.ext4"}
	be.gets = nil
	if _, cleanup, err := h.stageScanExt4(t.Context(), be, app, renamed); err != nil || len(be.gets) != 1 || be.gets[0] != "apps/old-web/before-rename.ext4" {
		t.Fatalf("stage after a rename = %v from %v, want the layer under the old slug", err, be.gets)
	} else {
		cleanup()
	}
	if _, _, err := h.stageScanExt4(t.Context(), be, app, state.Deployment{ID: "missing"}); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("missing own layer = %v, want ErrNotFound", err)
	}
}
