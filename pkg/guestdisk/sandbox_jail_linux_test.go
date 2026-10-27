//go:build linux

package guestdisk

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// adr: 20261025090100
func TestSearchableBy(t *testing.T) {
	for _, tc := range []struct {
		name     string
		st       unix.Stat_t
		uid, gid int
		want     bool
	}{
		{"owner with x", unix.Stat_t{Uid: 20001, Gid: 1, Mode: unix.S_IFDIR | 0o700}, 20001, 20001, true},
		{"owner without x, other with x", unix.Stat_t{Uid: 20001, Gid: 1, Mode: unix.S_IFDIR | 0o601}, 20001, 20001, false},
		{"group", unix.Stat_t{Uid: 0, Gid: 20001, Mode: unix.S_IFDIR | 0o710}, 20001, 20001, true},
		{"other", unix.Stat_t{Uid: 0, Gid: 0, Mode: unix.S_IFDIR | 0o711}, 20001, 20001, true},
		{"root-only jail root", unix.Stat_t{Uid: 0, Gid: 0, Mode: unix.S_IFDIR | unix.S_ISVTX | 0o700}, 20001, 20001, false},
	} {
		if got := searchableBy(tc.st, tc.uid, tc.gid); got != tc.want {
			t.Errorf("%s: searchableBy = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestUnsearchableAncestorFindsTheTopmostClosedDirectory(t *testing.T) {
	base := t.TempDir()
	for _, dir := range []string{filepath.Dir(base), base} {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	jail := filepath.Join(base, "jail")
	image := filepath.Join(jail, "firecracker", "inst", "root", "layer.ext4")
	if err := os.MkdirAll(filepath.Dir(image), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(jail, 0o700); err != nil {
		t.Fatal(err)
	}
	other := os.Getuid() + 1
	// A closed directory above the test's temp dir (a private TMPDIR) is the
	// topmost one instead; the jail's own subdirectories never are.
	want, err := unsearchableAncestor(filepath.Join(base, "x"), other, other)
	if err != nil {
		t.Fatal(err)
	}
	if want == "" {
		want = jail
	}
	got, err := unsearchableAncestor(image, other, other)
	if err != nil || got != want {
		t.Fatalf("unsearchableAncestor = %q, %v; want %q", got, err, want)
	}
	if got, err := unsearchableAncestor(image, os.Getuid(), os.Getgid()); err != nil || got != "" {
		t.Fatalf("owner's ancestor = %q, %v; want none", got, err)
	}
}

// The jail root admits only root (/srv/fc/jail is a mode-1700 tmpfs), and the
// image and the session scratch live below it. The tool runs as the jail uid,
// so before exposeSessionPaths every sandboxed run failed with "landlock rule
// for .../layer.ext4: permission denied".
func TestSandboxedEditUnderRootOnlyJail(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to create namespaces and change uid")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	jail := filepath.Join(sandboxTestRoot(t), "jail")
	if err := os.Mkdir(jail, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(jail, os.ModeSticky|0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(jail, "firecracker", "inst", "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	img := moveImage(t, mkImage(t, func(r string) { write(t, filepath.Join(r, "upper/app/a"), "a") }), root)
	if err := os.Chown(img, 23456, 23456); err != nil {
		t.Fatal(err)
	}
	ed, err := New(Config{
		Helper:      exe,
		Debugfs:     e2fsTool(t, "debugfs"),
		E2fsck:      e2fsTool(t, "e2fsck"),
		ScratchRoot: filepath.Join(jail, "firecracker", ".guestdisk"),
		AllowOwner:  func(uid, gid int) bool { return uid == 23456 && gid == 23456 },
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := ed.Open(img)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if _, err := s.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteFiles(ctx, []File{{Path: "/upper/etc/faas/secrets.env", Data: []byte("s"), Mode: 0o400}}); err != nil {
		t.Fatal(err)
	}
	entries, err := s.Inspect(ctx, []string{"/upper/etc/faas/secrets.env"}, map[string]Read{"/upper/etc/faas/secrets.env": {Max: 16}})
	if err != nil {
		t.Fatal(err)
	}
	if e := entries["/upper/etc/faas/secrets.env"]; e.Kind != Regular || string(e.Data) != "s" {
		t.Fatalf("written file reads back as %+v", e)
	}
	fsckClean(t, img)
}
