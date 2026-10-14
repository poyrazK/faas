package main

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func readCopyOutTar(t *testing.T, data []byte) map[string]string {
	t.Helper()
	entries := map[string]string{}
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return entries
		}
		if err != nil {
			t.Fatal(err)
		}
		switch header.Typeflag {
		case tar.TypeReg:
			body, _ := io.ReadAll(tr)
			entries[header.Name] = "file:" + string(body)
		case tar.TypeDir:
			entries[header.Name] = "dir"
		case tar.TypeSymlink:
			entries[header.Name] = "link:" + header.Linkname
		}
	}
}

func TestWriteCopyOutTarDirectory(t *testing.T) {
	root := t.TempDir()
	dist := filepath.Join(root, "dist")
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dist, "index.html"), []byte("<html>"), 0o644)
	_ = os.WriteFile(filepath.Join(dist, "assets", "app.js"), []byte("js"), 0o600)
	_ = os.Symlink("/etc/passwd", filepath.Join(dist, "escape"))
	var buf bytes.Buffer
	if err := writeCopyOutTar(&buf, dist); err != nil {
		t.Fatal(err)
	}
	got := readCopyOutTar(t, buf.Bytes())
	want := map[string]string{
		"dist/": "dir", "dist/assets/": "dir", "dist/assets/app.js": "file:js",
		"dist/index.html": "file:<html>", "dist/escape": "link:/etc/passwd",
	}
	if len(got) != len(want) {
		keys := make([]string, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Fatalf("entries = %v", keys)
	}
	for name, value := range want {
		if got[name] != value {
			t.Fatalf("%s = %q, want %q", name, got[name], value)
		}
	}
}

func TestWriteCopyOutTarSingleFileAndRootSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "release-1")
	_ = os.Mkdir(target, 0o755)
	_ = os.WriteFile(filepath.Join(target, "VERSION"), []byte("1"), 0o644)
	_ = os.Symlink(target, filepath.Join(root, "current"))

	var file bytes.Buffer
	if err := writeCopyOutTar(&file, filepath.Join(target, "VERSION")); err != nil {
		t.Fatal(err)
	}
	if got := readCopyOutTar(t, file.Bytes()); len(got) != 1 || got["VERSION"] != "file:1" {
		t.Fatalf("single file = %v", got)
	}
	var linked bytes.Buffer
	if err := writeCopyOutTar(&linked, filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	if got := readCopyOutTar(t, linked.Bytes()); got["current/VERSION"] != "file:1" {
		t.Fatalf("root symlink is followed and named after the link: %v", got)
	}
}

func TestWriteCopyOutTarMissingPath(t *testing.T) {
	if err := writeCopyOutTar(io.Discard, filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("missing path accepted")
	}
	if code := runCopyOutHelper(nil); code != 2 {
		t.Fatalf("helper without args = %d", code)
	}
}
