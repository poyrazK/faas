package scanview

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func viewFile(t *testing.T, root, name, value string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func viewLink(t *testing.T, root, name, target string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
}

func guestLinkFixture(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	viewFile(t, src, "usr/lib/module.txt", "guest library")
	viewFile(t, src, "usr/other", "guest parent")
	viewFile(t, src, "other", "wrong lexical parent")
	viewLink(t, src, "bin/module", "/usr/lib/module.txt")
	viewLink(t, src, "jump", "/usr/lib")
	viewLink(t, src, "probe", "jump/../other")
	viewLink(t, src, "clamped", "../../../usr/lib/module.txt")
	viewLink(t, src, "missing-prefix", "missing/../usr/lib/module.txt")
	viewLink(t, src, "file-as-dir", "usr/lib/module.txt/")
	viewLink(t, src, "cycle-a", "cycle-b")
	viewLink(t, src, "cycle-b", "cycle-a")
	viewLink(t, src, "root", "/")
	return src
}

func TestCopyPreservesVirtualGuestLinksWithoutHostTraversal(t *testing.T) {
	src := guestLinkFixture(t)
	dst := t.TempDir()
	host := t.TempDir()
	viewFile(t, host, "secret", "host-only secret")
	viewLink(t, src, "host-secret", filepath.Join(host, "secret"))
	before, err := Snapshot(t.Context(), src)
	if err != nil {
		t.Fatal(err)
	}
	after, err := Copy(t.Context(), src, dst, before)
	if err != nil {
		t.Fatal(err)
	}
	if before.ProjectionDigest != after.ProjectionDigest || before.Digest == after.Digest {
		t.Fatal("projection changed visibility or omitted raw-link identity")
	}
	for name, want := range map[string]string{"bin/module": "guest library", "probe": "guest parent", "clamped": "guest library"} {
		got, err := os.ReadFile(filepath.Join(dst, name))
		if err != nil || string(got) != want {
			t.Fatal("guest link resolution changed", name, string(got), err)
		}
	}
	for _, name := range []string{"host-secret", "missing-prefix", "file-as-dir", "cycle-a", "cycle-b"} {
		if _, err := os.ReadFile(filepath.Join(dst, name)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatal("broken or host-only link became readable", name, err)
		}
	}
	info, err := os.Stat(filepath.Join(dst, "root"))
	if err != nil || !info.IsDir() {
		t.Fatal("virtual-root link changed", err)
	}
	again, err := Snapshot(t.Context(), dst)
	if err != nil || again != after {
		t.Fatal("actual copied tree was not retained", err)
	}
}

func TestTreeDetectsChangedBytesAndRawBrokenLinks(t *testing.T) {
	src := t.TempDir()
	viewFile(t, src, "file", "one")
	viewLink(t, src, "broken", "absent-a")
	before, err := Snapshot(t.Context(), src)
	if err != nil {
		t.Fatal(err)
	}
	viewFile(t, src, "file", "two")
	changed, err := Snapshot(t.Context(), src)
	if err != nil || changed.Digest == before.Digest || changed.ProjectionDigest == before.ProjectionDigest {
		t.Fatal("same-size byte mutation disappeared", err)
	}
	viewFile(t, src, "file", "one")
	if err := os.Remove(filepath.Join(src, "broken")); err != nil {
		t.Fatal(err)
	}
	viewLink(t, src, "broken", "absent-b")
	changed, err = Snapshot(t.Context(), src)
	if err != nil || changed.Digest == before.Digest || changed.ProjectionDigest != before.ProjectionDigest {
		t.Fatal("raw broken-link mutation was not distinguished", err)
	}
	dst := t.TempDir()
	if _, err := Copy(t.Context(), src, dst, before); !errors.Is(err, ErrChanged) {
		t.Fatal("changed input acquired old proof", err)
	}
	if entries, err := os.ReadDir(dst); err != nil || len(entries) != 0 {
		t.Fatal("refused preflight changed destination", err)
	}
}

func TestTreeSafetyBoundsAndCancellation(t *testing.T) {
	src := t.TempDir()
	viewFile(t, src, "file", "payload")
	r, err := os.OpenRoot(src)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, limits := range []viewLimits{{1, 100, 100}, {100, 2, 100}, {100, 100, 3}} {
		if _, err := snapshotRoot(t.Context(), r, limits); !errors.Is(err, ErrLimit) {
			t.Fatal("incomplete bounded tree reported success", limits, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Snapshot(ctx, src); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled snapshot reported success", err)
	}
	view, err := Snapshot(t.Context(), src)
	if err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	viewFile(t, dst, "owned", "existing")
	if _, err := Copy(t.Context(), src, dst, view); !errors.Is(err, ErrInvalid) {
		t.Fatal("nonempty destination was accepted", err)
	}
	if got, err := os.ReadFile(filepath.Join(dst, "owned")); err != nil || string(got) != "existing" {
		t.Fatal("foreign contents were removed", err)
	}
}

func TestCopyCleanupRemovesOnlyCreatedNames(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	viewFile(t, src, "a", "copied")
	viewFile(t, src, "b", "collision")
	r, err := os.OpenRoot(src)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	d, err := os.OpenRoot(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	view, err := snapshotRoot(t.Context(), r, defaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	c, err := newProjectionCopy(t.Context(), r, d, view)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(r.FS(), ".")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.node(t.Context(), "a", entries[0]); err != nil {
		t.Fatal(err)
	}
	viewFile(t, dst, "b", "another writer")
	if err := c.node(t.Context(), "b", entries[1]); !errors.Is(err, fs.ErrExist) {
		t.Fatal("existing node overwritten", err)
	}
	if err := c.cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "a")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("partial copy retained", err)
	}
	if got, err := os.ReadFile(filepath.Join(dst, "b")); err != nil || string(got) != "another writer" {
		t.Fatal("foreign collision was removed", err)
	}
}

func TestTreeRefusesRootSymlinkAndOverlongLink(t *testing.T) {
	src := t.TempDir()
	viewFile(t, src, "file", "payload")
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(src, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := Snapshot(t.Context(), alias); !errors.Is(err, ErrInvalid) {
		t.Fatal("caller root symlink accepted", err)
	}
	r, err := os.OpenRoot(src)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := virtualLink(t.Context(), r, "link", strings.Repeat("x", 8192)); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversized link accepted", err)
	}
}
