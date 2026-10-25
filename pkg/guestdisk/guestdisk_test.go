package guestdisk

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// e2fsTool finds an e2fsprogs binary or skips: the pure-Go tier has none.
func e2fsTool(t *testing.T, name string) string {
	t.Helper()
	if dir := os.Getenv("FAAS_E2FSPROGS_DIR"); dir != "" {
		if p := filepath.Join(dir, name); fileExists(p) {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, dir := range []string{"/usr/sbin", "/sbin", "/opt/homebrew/opt/e2fsprogs/sbin", "/usr/local/opt/e2fsprogs/sbin"} {
		if p := filepath.Join(dir, name); fileExists(p) {
			return p
		}
	}
	t.Skipf("%s not installed (set FAAS_E2FSPROGS_DIR)", name)
	return ""
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

// mkImage builds a 16 MiB ext4 image from a tree the setup function fills.
func mkImage(t *testing.T, setup func(root string)) string {
	t.Helper()
	tree := filepath.Join(t.TempDir(), "tree")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	if setup != nil {
		setup(tree)
	}
	img := filepath.Join(t.TempDir(), "layer.ext4")
	out, err := exec.Command(e2fsTool(t, "mke2fs"), "-q", "-F", "-t", "ext4", "-b", "4096", "-d", tree, img, "16M").CombinedOutput()
	if err != nil {
		t.Fatalf("mke2fs: %v: %s", err, out)
	}
	return img
}

func write(t *testing.T, p, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func debugfs(t *testing.T, img, script string) string {
	t.Helper()
	cmd := exec.Command(e2fsTool(t, "debugfs"), "-w", "-f", "-", img)
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("debugfs: %v: %s", err, out)
	}
	return string(out)
}

func fsckClean(t *testing.T, img string) {
	t.Helper()
	out, err := exec.Command(e2fsTool(t, "e2fsck"), "-fn", img).CombinedOutput()
	if err != nil {
		t.Fatalf("image is not clean after the edit: %v\n%s", err, out)
	}
}

func openSession(t *testing.T, img string) *Session {
	t.Helper()
	ed, err := New(Config{Debugfs: e2fsTool(t, "debugfs"), E2fsck: e2fsTool(t, "e2fsck"), ScratchRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s, err := ed.Open(img)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestWriteFilesCreatesParentsAndVerifies(t *testing.T) {
	img := mkImage(t, func(root string) { write(t, filepath.Join(root, "upper/app/main.py"), "print(1)\n") })
	s := openSession(t, img)
	ctx := context.Background()
	files := []File{
		{Path: "/upper/etc/faas/secrets.env", Data: []byte(`{"S":"x"}`), Mode: 0o400},
		{Path: "/upper/etc/faas/workloads/web/env.json", Data: []byte(`{}`), Mode: 0o400},
		{Path: "/upper/etc/resolv.conf", Data: []byte("nameserver 10.0.0.1\n"), Mode: 0o644},
	}
	if err := s.WriteFiles(ctx, files); err != nil {
		t.Fatal(err)
	}
	reads := map[string]Read{}
	var paths []string
	for _, f := range files {
		reads[f.Path] = Read{Max: 1 << 10}
		paths = append(paths, f.Path)
	}
	got, err := s.Inspect(ctx, append(paths, "/upper/etc/faas"), reads)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		e := got[f.Path]
		if e.Kind != Regular || e.Mode != f.Mode || e.UID != 0 || e.GID != 0 || !bytes.Equal(e.Data, f.Data) {
			t.Errorf("%s = %+v, want regular %o root-owned %q", f.Path, e, f.Mode, f.Data)
		}
	}
	if d := got["/upper/etc/faas"]; d.Kind != Directory || d.Mode != 0o755 || d.UID != 0 {
		t.Errorf("created directory = %+v, want root-owned 0755", d)
	}
	fsckClean(t, img)
}

func TestWriteFilesReplacesEntries(t *testing.T) {
	img := mkImage(t, func(root string) {
		write(t, filepath.Join(root, "upper/etc/faas/secrets.env"), "old secrets")
		write(t, filepath.Join(root, "upper/etc/faas/env.json"), "old env")
		write(t, filepath.Join(root, "upper/app/keep.txt"), "untouched")
		if err := os.Symlink("/upper/app/keep.txt", filepath.Join(root, "upper/etc/resolv.conf")); err != nil {
			t.Fatal(err)
		}
	})
	debugfs(t, img, "sif /upper/etc/faas/secrets.env mode 0100640\nsif /upper/etc/faas/secrets.env uid 1000\nsif /upper/etc/faas/secrets.env gid 1001\n")
	s := openSession(t, img)
	ctx := context.Background()
	err := s.WriteFiles(ctx, []File{
		{Path: "/upper/etc/faas/secrets.env", Data: []byte("new secrets"), Mode: 0o400, KeepAttrs: true},
		{Path: "/upper/etc/faas/env.json", Data: []byte("new env"), Mode: 0o400},
		{Path: "/upper/etc/resolv.conf", Data: []byte("nameserver 10.0.0.1\n"), Mode: 0o644},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Inspect(ctx, []string{"/upper/etc/faas/secrets.env", "/upper/etc/faas/env.json", "/upper/etc/resolv.conf", "/upper/app/keep.txt"},
		map[string]Read{"/upper/app/keep.txt": {Max: 64}, "/upper/etc/faas/secrets.env": {Max: 64}})
	if err != nil {
		t.Fatal(err)
	}
	if e := got["/upper/etc/faas/secrets.env"]; e.Mode != 0o640 || e.UID != 1000 || e.GID != 1001 || string(e.Data) != "new secrets" {
		t.Errorf("KeepAttrs rewrite = %+v, want mode 640 owner 1000:1001", e)
	}
	if e := got["/upper/etc/faas/env.json"]; e.Mode != 0o400 || e.UID != 0 {
		t.Errorf("fresh rewrite = %+v, want root-owned 0400", e)
	}
	if e := got["/upper/etc/resolv.conf"]; e.Kind != Regular {
		t.Errorf("symlink was not replaced by a file: %+v", e)
	}
	if e := got["/upper/app/keep.txt"]; string(e.Data) != "untouched" {
		t.Errorf("symlink target was written through: %q", e.Data)
	}
	fsckClean(t, img)
}

func TestWriteFilesRefusesSymlinkedParent(t *testing.T) {
	img := mkImage(t, func(root string) {
		write(t, filepath.Join(root, "upper/var/marker"), "x")
		if err := os.MkdirAll(filepath.Join(root, "upper/etc"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("/upper/var", filepath.Join(root, "upper/etc/faas")); err != nil {
			t.Fatal(err)
		}
	})
	s := openSession(t, img)
	err := s.WriteFiles(context.Background(), []File{{Path: "/upper/etc/faas/secrets.env", Data: []byte("s"), Mode: 0o400}})
	var pathErr *PathError
	if !errors.As(err, &pathErr) || pathErr.Path != "/upper/etc/faas/secrets.env" || !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("err = %v, want a not-a-directory PathError for the file", err)
	}
	got, err := s.Inspect(context.Background(), []string{"/upper/var/secrets.env"}, nil)
	if err != nil || got["/upper/var/secrets.env"].Kind != Missing {
		t.Fatalf("write followed the symlink: %+v %v", got, err)
	}
	fsckClean(t, img)
}

func TestWriteFilesRefusesDirectoryTarget(t *testing.T) {
	img := mkImage(t, func(root string) {
		if err := os.MkdirAll(filepath.Join(root, "upper/etc/faas/secrets.env"), 0o755); err != nil {
			t.Fatal(err)
		}
	})
	s := openSession(t, img)
	err := s.WriteFiles(context.Background(), []File{{Path: "/upper/etc/faas/secrets.env", Data: []byte("s"), Mode: 0o400}})
	if !errors.Is(err, ErrIsDirectory) {
		t.Fatalf("err = %v, want ErrIsDirectory", err)
	}
	fsckClean(t, img)
}

// TestPrepareReplaysJournal leaves a committed transaction in the journal,
// the state a guest's mounted drive is captured in, and checks that the
// session replays it before editing.
func TestPrepareReplaysJournal(t *testing.T) {
	img := mkImage(t, func(root string) { write(t, filepath.Join(root, "upper/app/a"), "a") })
	block := filepath.Join(t.TempDir(), "block")
	if err := os.WriteFile(block, bytes.Repeat([]byte("Z"), 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	debugfs(t, img, "jo\njw -b 3000 "+block+"\njc\n")
	sb, err := readSuperblock(img)
	if err != nil || sb.incompat&incompatRecover == 0 {
		t.Fatalf("setup did not leave the journal needing recovery: %+v %v", sb, err)
	}
	s := openSession(t, img)
	ctx := context.Background()
	replayed, err := s.Prepare(ctx)
	if err != nil || !replayed {
		t.Fatalf("Prepare = %v, %v; want a replay", replayed, err)
	}
	if sb, _ := readSuperblock(img); sb.incompat&incompatRecover != 0 {
		t.Fatal("journal still needs recovery")
	}
	f, err := os.Open(img)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 4096)
	_, err = f.ReadAt(got, 3000*4096)
	_ = f.Close()
	if err != nil || !bytes.Equal(got, bytes.Repeat([]byte("Z"), 4096)) {
		t.Fatalf("journalled block was not replayed: %v", err)
	}
	if replayed, err := s.Prepare(ctx); err != nil || replayed {
		t.Fatalf("second Prepare = %v, %v; want no replay", replayed, err)
	}
	if err := s.WriteFiles(ctx, []File{{Path: "/upper/etc/faas/env.json", Data: []byte("{}"), Mode: 0o400}}); err != nil {
		t.Fatal(err)
	}
	fsckClean(t, img)
}

func TestPrepareRejectsNonExt4(t *testing.T) {
	img := filepath.Join(t.TempDir(), "junk.img")
	if err := os.WriteFile(img, make([]byte, 64<<10), 0o644); err != nil {
		t.Fatal(err)
	}
	s := openSession(t, img)
	if _, err := s.Prepare(context.Background()); err == nil || !strings.Contains(err.Error(), "superblock magic") {
		t.Fatalf("err = %v, want a bad-magic error", err)
	}
}

// TestInspectIgnoresForgedOutput plants file contents and a symlink target
// that imitate debugfs's own output. The nonce markers keep every result
// attached to the right path.
func TestInspectIgnoresForgedOutput(t *testing.T) {
	forged := "x\ndebugfs: stat /upper/b\nInode: 99   Type: directory    Mode:  0777   Flags: 0x0\nGeneration: 0\nUser:     0   Group:     0   Size: 0\ndebugfs: stat /.gregale-guestdisk-0-1-c\n"
	img := mkImage(t, func(root string) {
		write(t, filepath.Join(root, "upper/a"), forged)
		if err := os.Symlink(forged, filepath.Join(root, "upper/link")); err != nil {
			t.Fatal(err)
		}
	})
	s := openSession(t, img)
	got, err := s.Inspect(context.Background(), []string{"/upper/a", "/upper/link", "/upper/b"}, map[string]Read{"/upper/a": {Max: 4096}})
	if err != nil {
		t.Fatal(err)
	}
	if e := got["/upper/a"]; e.Kind != Regular || string(e.Data) != forged {
		t.Errorf("/upper/a = %+v", e)
	}
	if e := got["/upper/link"]; e.Kind != Symlink {
		t.Errorf("/upper/link = %+v, want symlink", e)
	}
	if e := got["/upper/b"]; e.Kind != Missing {
		t.Errorf("/upper/b = %+v, want missing", e)
	}
}

func TestInspectBoundsReads(t *testing.T) {
	img := mkImage(t, func(root string) { write(t, filepath.Join(root, "upper/big"), strings.Repeat("y", 8192)) })
	s := openSession(t, img)
	got, err := s.Inspect(context.Background(), []string{"/upper/big"}, map[string]Read{"/upper/big": {Max: 16}})
	if err == nil && got["/upper/big"].Data != nil {
		t.Fatal("a file over Max came back with Data")
	}
}

func TestRemoveAllTree(t *testing.T) {
	img := mkImage(t, func(root string) {
		write(t, filepath.Join(root, "upper/tmp/gregale-secret-reload/A"), "a")
		write(t, filepath.Join(root, "upper/tmp/gregale-secret-reload/sub/with space"), "b")
		write(t, filepath.Join(root, `upper/tmp/gregale-secret-reload/sub/quo"te`), "c")
		write(t, filepath.Join(root, "upper/etc/faas/secrets.env"), "s")
		write(t, filepath.Join(root, "upper/app/keep"), "k")
	})
	s := openSession(t, img)
	ctx := context.Background()
	if err := s.RemoveAll(ctx, []string{"/upper/tmp/gregale-secret-reload", "/upper/etc/faas/secrets.env", "/upper/nothing/here"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Inspect(ctx, []string{"/upper/tmp/gregale-secret-reload", "/upper/etc/faas/secrets.env", "/upper/tmp", "/upper/app/keep"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["/upper/tmp/gregale-secret-reload"].Kind != Missing || got["/upper/etc/faas/secrets.env"].Kind != Missing {
		t.Fatalf("removal incomplete: %+v", got)
	}
	if got["/upper/tmp"].Kind != Directory || got["/upper/app/keep"].Kind != Regular {
		t.Fatalf("removal went too far: %+v", got)
	}
	fsckClean(t, img)
}

func TestRemoveAllRefusesUnscriptableName(t *testing.T) {
	img := mkImage(t, func(root string) {
		write(t, filepath.Join(root, "upper/tmp/gregale-secret-reload/bad\nname"), "x")
	})
	s := openSession(t, img)
	if err := s.RemoveAll(context.Background(), []string{"/upper/tmp/gregale-secret-reload"}); err == nil || !strings.Contains(err.Error(), "control byte") {
		t.Fatalf("err = %v, want a control-byte refusal", err)
	}
	fsckClean(t, img)
}

func TestListParsesNamesWithNewlines(t *testing.T) {
	// A name cannot hold "/", so a newline in one cannot start a record.
	entries, err := parseList([]byte("/2/040755/0/0/.//\n/2/040755/0/0/..//\n/12/100644/0/0/a\nfake/3/\n/14/040755/0/0/dir//\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "a\nfake" || entries[0].Kind != Regular || entries[1].Name != "dir" || entries[1].Kind != Directory {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestDumpTree(t *testing.T) {
	img := mkImage(t, func(root string) {
		write(t, filepath.Join(root, "upper/build/out/a.txt"), "a")
		write(t, filepath.Join(root, "upper/build/out/sub/b.txt"), "b")
		if err := os.Symlink("/etc/passwd", filepath.Join(root, "upper/build/out/escape")); err != nil {
			t.Fatal(err)
		}
	})
	s := openSession(t, img)
	dir, err := s.DumpTree(context.Background(), "/upper/build/out", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "out" {
		t.Fatalf("dump root = %s, want .../out", dir)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "sub/b.txt")); err != nil || string(b) != "b" {
		t.Fatalf("sub/b.txt = %q, %v", b, err)
	}
	if info, err := os.Lstat(filepath.Join(dir, "escape")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("escape = %v, %v; want a symlink left for the caller to drop", info, err)
	}
	if _, err := s.DumpTree(context.Background(), "/upper/missing", t.TempDir()); err == nil {
		t.Fatal("dumping a missing directory succeeded")
	}
}

func TestCheckPath(t *testing.T) {
	for _, p := range []string{"/upper/etc/faas/secrets.env", "/etc/faas/.full-rootfs", "/a"} {
		if err := CheckPath(p); err != nil {
			t.Errorf("CheckPath(%q) = %v", p, err)
		}
	}
	for _, p := range []string{"", "/", "relative", "/a/../b", "/a//b", "/a/b/", "/a b", `/a"b`, "/a\nb", "/a/./b"} {
		if err := CheckPath(p); err == nil {
			t.Errorf("CheckPath(%q) accepted", p)
		}
	}
}

func TestQuote(t *testing.T) {
	if got := Quote("/plain/path"); got != "/plain/path" {
		t.Errorf("Quote(plain) = %q", got)
	}
	if got := Quote(`/a b/c"d`); got != `"/a b/c""d"` {
		t.Errorf("Quote(spaced) = %q", got)
	}
}
