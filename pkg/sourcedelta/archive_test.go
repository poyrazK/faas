package sourcedelta

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateApplyRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.tar.gz")
	targetPath := filepath.Join(dir, "target.tar.gz")
	deltaPath := filepath.Join(dir, "delta.tar.gz")
	outputPath := filepath.Join(dir, "output.tar.gz")
	writeTestArchive(t, basePath, map[string]string{"app/a.txt": "old", "app/delete.txt": "gone", "app/same.txt": "same"})
	writeTestArchive(t, targetPath, map[string]string{"app/a.txt": "new", "app/new.txt": "hello", "app/same.txt": "same"})
	limits := Limits{MaxEntries: 100, MaxCompressedBytes: 1 << 20}
	base, err := Inspect(basePath, limits)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Create(base, targetPath, deltaPath, limits)
	if err != nil {
		t.Fatal(err)
	}
	if result.ChangedFiles != 2 || len(result.Deleted) != 1 || result.Deleted[0] != "app/delete.txt" {
		t.Fatalf("unexpected delta result: %+v", result)
	}
	got, err := Apply(basePath, deltaPath, outputPath, base.Revision, result.Target.Revision, result.Deleted, limits)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != result.Target.Revision {
		t.Fatalf("revision = %s, want %s", got.Revision, result.Target.Revision)
	}
}

func TestApplyRejectsWrongBase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.tar.gz")
	deltaPath := filepath.Join(dir, "delta.tar.gz")
	writeTestArchive(t, basePath, map[string]string{"app/a": "a"})
	writeTestArchive(t, deltaPath, map[string]string{"app/b": "b"})
	_, err := Apply(basePath, deltaPath, filepath.Join(dir, "out.tar.gz"), string(make([]byte, 64)), string(make([]byte, 64)), nil, Limits{MaxEntries: 10, MaxCompressedBytes: 1 << 20})
	if !errors.Is(err, ErrBaseRevision) {
		t.Fatalf("error = %v, want ErrBaseRevision", err)
	}
}

func TestInspectRejectsUnsafeAndLinkEntries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		hdr  tar.Header
	}{
		{name: "escape", hdr: tar.Header{Name: "../escape", Typeflag: tar.TypeReg}},
		{name: "symlink", hdr: tar.Header{Name: "app/link", Typeflag: tar.TypeSymlink, Linkname: "target"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "source.tar.gz")
			f, err := os.Create(filename)
			if err != nil {
				t.Fatal(err)
			}
			gz := gzip.NewWriter(f)
			tw := tar.NewWriter(gz)
			if err := tw.WriteHeader(&tc.hdr); err != nil {
				t.Fatal(err)
			}
			_ = tw.Close()
			_ = gz.Close()
			_ = f.Close()
			if _, err := Inspect(filename, Limits{MaxEntries: 10, MaxCompressedBytes: 1 << 20}); err == nil {
				t.Fatal("Inspect succeeded, want rejection")
			}
		})
	}
}

func TestInspectRejectsExpandedArchiveOverLimit(t *testing.T) {
	t.Parallel()
	filename := filepath.Join(t.TempDir(), "source.tar.gz")
	writeTestArchive(t, filename, map[string]string{"large.txt": "three"})
	if _, err := Inspect(filename, Limits{MaxEntries: 10, MaxCompressedBytes: 1 << 20, MaxExpandedBytes: 3}); err == nil {
		t.Fatal("Inspect succeeded above expanded-byte limit")
	}
}

func writeTestArchive(t *testing.T, filename string, files map[string]string) {
	t.Helper()
	f, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
