package devpatch

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
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
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.prepare != nil {
				tc.prepare(t, dir)
			}
			if _, err := Apply(dir, tc.archive(t), tc.deleted, 1<<20); err == nil {
				t.Fatal("Apply accepted an escaping patch")
			}
			if _, err := os.Stat(filepath.Join(outside, "owned.js")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("patch wrote outside the target directory")
			}
		})
	}
}

func TestApplyEnforcesSizeBudget(t *testing.T) {
	archive := tarGz(t, tarEntry{name: "big.bin", body: "0123456789"})
	if _, err := Apply(t.TempDir(), archive, nil, 5); err == nil {
		t.Fatal("Apply accepted a patch over its byte budget")
	}
}
