package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func writeDeploySourceArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		data := []byte(body)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}); err != nil {
			t.Fatalf("write archive header %q: %v", name, err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("write archive body %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	path := filepath.Join(t.TempDir(), "source.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	return path
}

func TestMaterializeDeployArchive_UsesArchiveRoot(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	if err := os.WriteFile(filepath.Join(cwd, "package.json"), []byte(`{"name":"cwd"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	archive := writeDeploySourceArchive(t, map[string]string{
		"archive-root/package.json": `{"name":"archive"}`,
		"archive-root/gregale.yaml": "triggers:\n  - kind: cron\n    schedule: 0 1 * * *\n",
	})

	snapshot, sourceDir, cleanup, err := materializeDeployArchive(archive)
	if err != nil {
		t.Fatalf("materialize archive: %v", err)
	}
	if filepath.Base(snapshot) != filepath.Base(archive) {
		t.Fatalf("snapshot basename = %q, want %q", filepath.Base(snapshot), filepath.Base(archive))
	}
	if sourceDir == cwd || filepath.Base(sourceDir) != "archive-root" {
		t.Fatalf("source dir = %q, want extracted archive root", sourceDir)
	}
	got, err := os.ReadFile(filepath.Join(sourceDir, "package.json"))
	if err != nil {
		t.Fatalf("read extracted package: %v", err)
	}
	if string(got) != `{"name":"archive"}` {
		t.Fatalf("extracted package = %q, want archive contents", got)
	}
	if _, err := os.Stat(filepath.Join(sourceDir, "gregale.yaml")); err != nil {
		t.Fatalf("archive manifest missing: %v", err)
	}
	cleanup()
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatalf("cleanup left snapshot at %q", snapshot)
	}
}

func TestMaterializeDeployArchive_FlatArchiveUsesExtractionRoot(t *testing.T) {
	archive := writeDeploySourceArchive(t, map[string]string{"package.json": "{}"})
	snapshot, sourceDir, cleanup, err := materializeDeployArchive(archive)
	if err != nil {
		t.Fatalf("materialize archive: %v", err)
	}
	defer cleanup()
	if filepath.Base(snapshot) != filepath.Base(archive) {
		t.Fatalf("snapshot basename = %q, want %q", filepath.Base(snapshot), filepath.Base(archive))
	}
	got, err := os.ReadFile(filepath.Join(sourceDir, "package.json"))
	if err != nil {
		t.Fatalf("read flat archive: %v", err)
	}
	if string(got) != "{}" {
		t.Fatalf("flat archive body = %q", got)
	}
}

func TestMaterializeDeployArchive_RejectsUnsafeEntries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry *tar.Header
	}{
		{name: "parent", entry: &tar.Header{Name: "../escape", Mode: 0o644, Size: 0}},
		{name: "symlink", entry: &tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			tw := tar.NewWriter(gz)
			if err := tw.WriteHeader(tc.entry); err != nil {
				t.Fatal(err)
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "unsafe.tar.gz")
			if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, _, err := materializeDeployArchive(path)
			if err == nil {
				t.Fatal("materialize succeeded for unsafe archive")
			}
		})
	}
}
