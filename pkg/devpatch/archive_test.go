package devpatch

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tarEntry struct {
	name     string
	body     string
	mode     int64
	typeflag byte
	link     string
}

func tarGz(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		hdr := &tar.Header{Name: e.name, Mode: e.mode, Typeflag: typeflag, Linkname: e.link}
		if typeflag == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestBuildArchiveSelectsChangedFilesUnderRoot(t *testing.T) {
	source := tarGz(t,
		tarEntry{name: "apps/api/src/app.js", body: "new", mode: 0o664},
		tarEntry{name: "apps/api/bin/run", body: "#!/bin/sh", mode: 0o775},
		tarEntry{name: "apps/api/README.md", body: "unchanged", mode: 0o644},
		tarEntry{name: "apps/api/src", typeflag: tar.TypeDir, mode: 0o755},
	)
	include := map[string]bool{"apps/api/src/app.js": true, "apps/api/bin/run": true}
	archive, digest, err := BuildArchive(bytes.NewReader(source), "apps/api", include, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(digest) != 64 {
		t.Fatalf("digest = %q", digest)
	}
	dir := t.TempDir()
	result, err := Apply(dir, archive, nil, 1<<20)
	if err != nil || result.Written != 2 {
		t.Fatalf("Apply = %+v, %v", result, err)
	}
	if body, _ := os.ReadFile(filepath.Join(dir, "src", "app.js")); string(body) != "new" {
		t.Fatalf("src/app.js = %q", body)
	}
	for name, want := range map[string]os.FileMode{"src/app.js": 0o644, "bin/run": 0o755} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("%s mode = %v, %v; want %v", name, info.Mode().Perm(), err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); !os.IsNotExist(err) {
		t.Fatal("unchanged file was included in the patch")
	}
}

func TestBuildArchiveFailsWhenChangedFileIsMissing(t *testing.T) {
	source := tarGz(t, tarEntry{name: "a.js", body: "x", mode: 0o644})
	if _, _, err := BuildArchive(bytes.NewReader(source), "", map[string]bool{"a.js": true, "b.js": true}, 1<<20); err == nil {
		t.Fatal("BuildArchive succeeded with a missing changed file")
	}
}

func TestApplyReplacesAndDeletes(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"src/a.js": "old", "src/gone.js": "bye"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	archive := tarGz(t, tarEntry{name: "src/a.js", body: "new", mode: 0o644})
	result, err := Apply(dir, archive, []string{"src/gone.js", "src/never-existed.js", "src"}, 1<<20)
	if err != nil || result.Written != 1 || result.Deleted != 1 {
		t.Fatalf("Apply = %+v, %v", result, err)
	}
	if body, _ := os.ReadFile(filepath.Join(dir, "src", "a.js")); string(body) != "new" {
		t.Fatalf("src/a.js = %q", body)
	}
	if _, err := os.Stat(filepath.Join(dir, "src", "gone.js")); !os.IsNotExist(err) {
		t.Fatal("deleted file still exists")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "src"))
	for _, entry := range entries {
		if entry.Name() != "a.js" {
			t.Fatalf("unexpected leftover %q (staging file not cleaned up?)", entry.Name())
		}
	}
}

func TestApplyRejectsEscapes(t *testing.T) {
	outside := t.TempDir()
	cases := []struct {
		name    string
		archive func(t *testing.T) []byte
		deleted []string
		prepare func(t *testing.T, dir string)
	}{
		{name: "parent traversal", archive: func(t *testing.T) []byte { return tarGz(t, tarEntry{name: "../escape.js", body: "x"}) }},
		{name: "absolute path", archive: func(t *testing.T) []byte { return tarGz(t, tarEntry{name: "/etc/passwd", body: "x"}) }},
		{name: "unclean path", archive: func(t *testing.T) []byte { return tarGz(t, tarEntry{name: "src/../../x", body: "x"}) }},
		{name: "symlink entry", archive: func(t *testing.T) []byte {
			return tarGz(t, tarEntry{name: "link", typeflag: tar.TypeSymlink, link: "/etc/passwd"})
		}},
		{name: "hardlink entry", archive: func(t *testing.T) []byte {
			return tarGz(t, tarEntry{name: "hard", typeflag: tar.TypeLink, link: "/etc/passwd"})
		}},
		{name: "write through existing symlinked directory", archive: func(t *testing.T) []byte {
			return tarGz(t, tarEntry{name: "linked/owned.js", body: "x"})
		}, prepare: func(t *testing.T, dir string) {
			if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "delete traversal", archive: func(t *testing.T) []byte { return tarGz(t) }, deleted: []string{"../victim"}},
	}
	applies := map[string]func(string, []byte, []string, int64) (ApplyResult, error){"Apply": Apply, "ApplyInPlace": ApplyInPlace}
	for _, tc := range cases {
		for applyName, apply := range applies {
			t.Run(tc.name+"/"+applyName, func(t *testing.T) {
				dir := t.TempDir()
				if tc.prepare != nil {
					tc.prepare(t, dir)
				}
				if _, err := apply(dir, tc.archive(t), tc.deleted, 1<<20); err == nil {
					t.Fatalf("%s accepted an escaping patch", applyName)
				}
				if _, err := os.Stat(filepath.Join(outside, "owned.js")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("patch wrote outside the target directory")
				}
			})
		}
	}
}

// adr: 1170 — watch mode rewrites an existing file through its own inode,
// because node --watch stops seeing a file once a new inode is renamed over it.
func TestApplyInPlaceKeepsTheInode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "index.js")
	if err := os.WriteFile(target, []byte("v1 and a longer tail"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	archive := tarGz(t, tarEntry{name: "index.js", body: "v2", mode: 0o644}, tarEntry{name: "src/new.js", body: "new", mode: 0o644})
	result, err := ApplyInPlace(dir, archive, nil, 1<<20)
	if err != nil || result.Written != 2 {
		t.Fatalf("ApplyInPlace = %+v, %v", result, err)
	}
	after, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("ApplyInPlace replaced the inode of an existing file")
	}
	if body, _ := os.ReadFile(target); string(body) != "v2" || after.Mode().Perm() != 0o644 {
		t.Fatalf("index.js = %q mode %v, want \"v2\" 0644", body, after.Mode().Perm())
	}
	if body, _ := os.ReadFile(filepath.Join(dir, "src", "new.js")); string(body) != "new" {
		t.Fatalf("src/new.js = %q", body)
	}
	for _, sub := range []string{".", "src"} {
		entries, _ := os.ReadDir(filepath.Join(dir, sub))
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".gregale-patch-") {
				t.Fatalf("staging file %s/%s left behind", sub, entry.Name())
			}
		}
	}
	// Apply keeps ADR-740's staged rename: the restart that follows reads the
	// new inode, and a half-written file is never visible.
	if _, err := Apply(dir, tarGz(t, tarEntry{name: "index.js", body: "v3", mode: 0o644}), nil, 1<<20); err != nil {
		t.Fatal(err)
	}
	if renamed, err := os.Stat(target); err != nil || os.SameFile(after, renamed) {
		t.Fatalf("Apply kept the inode (err %v); want a staged rename", err)
	}
}

func TestApplyInPlaceStagesLinkedTargets(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.js")
	for path, body := range map[string]string{outside: "outside", filepath.Join(dir, "shared.js"): "shared"} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(filepath.Join(dir, "shared.js"), filepath.Join(dir, "hard.js")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "soft.js")); err != nil {
		t.Fatal(err)
	}
	archive := tarGz(t, tarEntry{name: "hard.js", body: "hard v2", mode: 0o644}, tarEntry{name: "soft.js", body: "soft v2", mode: 0o644})
	if _, err := ApplyInPlace(dir, archive, nil, 1<<20); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		filepath.Join(dir, "hard.js"): "hard v2", filepath.Join(dir, "shared.js"): "shared",
		filepath.Join(dir, "soft.js"): "soft v2", outside: "outside",
	} {
		if body, _ := os.ReadFile(path); string(body) != want {
			t.Fatalf("%s = %q, want %q", path, body, want)
		}
	}
	if info, err := os.Lstat(filepath.Join(dir, "soft.js")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("soft.js is still a symlink (err %v)", err)
	}
}

func TestApplyEnforcesSizeBudget(t *testing.T) {
	archive := tarGz(t, tarEntry{name: "big.bin", body: "0123456789"})
	if _, err := Apply(t.TempDir(), archive, nil, 5); err == nil {
		t.Fatal("Apply accepted a patch over its byte budget")
	}
}
