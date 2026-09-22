package sourcedelta

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

func TestCreateApplyRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.tar.gz")
	targetPath := filepath.Join(dir, "target.tar.gz")
	deltaPath := filepath.Join(dir, "delta.tar.gz")
	outputPath := filepath.Join(dir, "output.tar.gz")
	writeTestArchive(t, basePath, map[string]string{"app/a.txt": "old", "app/delete.txt": "gone", "app/same.txt": "same"})
	writeTestArchive(t, targetPath, map[string]string{"app/a.txt": "new", "app/new.txt": "hello", "app/same.txt": "same"})
	baseFile := openTestArchive(t, basePath, false)
	targetFile := openTestArchive(t, targetPath, false)
	deltaFile := openTestArchive(t, deltaPath, true)
	outputFile := openTestArchive(t, outputPath, true)
	limits := Limits{MaxEntries: 100, MaxCompressedBytes: 1 << 20}
	base, err := Inspect(baseFile, limits)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Create(base, targetFile, deltaFile, limits)
	if err != nil {
		t.Fatal(err)
	}
	if result.ChangedFiles != 2 || len(result.Deleted) != 1 || result.Deleted[0] != "app/delete.txt" {
		t.Fatalf("unexpected delta result: %+v", result)
	}
	got, err := Apply(baseFile, deltaFile, outputFile, base.Revision, result.Target.Revision, result.Deleted, limits)
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
	baseFile := openTestArchive(t, basePath, false)
	deltaFile := openTestArchive(t, deltaPath, false)
	outputFile := openTestArchive(t, filepath.Join(dir, "out.tar.gz"), true)
	_, err := Apply(baseFile, deltaFile, outputFile, string(make([]byte, 64)), string(make([]byte, 64)), nil, Limits{MaxEntries: 10, MaxCompressedBytes: 1 << 20})
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
			archive := openTestArchive(t, filename, false)
			if _, err := Inspect(archive, Limits{MaxEntries: 10, MaxCompressedBytes: 1 << 20}); err == nil {
				t.Fatal("Inspect succeeded, want rejection")
			}
		})
	}
}

func TestInspectRejectsExpandedArchiveOverLimit(t *testing.T) {
	t.Parallel()
	filename := filepath.Join(t.TempDir(), "source.tar.gz")
	writeTestArchive(t, filename, map[string]string{"large.txt": "three"})
	archive := openTestArchive(t, filename, false)
	if _, err := Inspect(archive, Limits{MaxEntries: 10, MaxCompressedBytes: 1 << 20, MaxExpandedBytes: 3}); err == nil {
		t.Fatal("Inspect succeeded above expanded-byte limit")
	}
}

func TestApplyPreflightsMergedLimitsBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits Limits
		want   string
	}{
		{"expanded bytes", Limits{MaxEntries: 10, MaxExpandedBytes: 5}, "expanded reconstructed source exceeds"},
		{"entry count", Limits{MaxEntries: 1}, "reconstructed source contains more than"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			basePath, deltaPath := filepath.Join(dir, "base.gz"), filepath.Join(dir, "delta.gz")
			writeTestArchive(t, basePath, map[string]string{"a": "four"})
			writeTestArchive(t, deltaPath, map[string]string{"b": "four"})
			base := openTestArchive(t, basePath, false)
			delta := openTestArchive(t, deltaPath, false)
			manifest, err := Inspect(base, tc.limits)
			if err != nil {
				t.Fatal(err)
			}
			// Read-only output proves rejection precedes reconstruction.
			output := openTestArchive(t, basePath, false)
			_, err = Apply(base, delta, output, manifest.Revision, strings.Repeat("a", 64), nil, tc.limits)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want merged %s limit before output I/O", err, tc.want)
			}
		})
	}
}

func TestInspectRejectsCorruptGzipTrailer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.gz")
	writeTestArchive(t, path, map[string]string{"a": "valid payload"})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-8] ^= 0xff // corrupt CRC32, after tar's end markers
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(openTestArchive(t, path, false), Limits{MaxEntries: 10}); err == nil {
		t.Fatal("accepted gzip with corrupt checksum")
	}
}

func TestInspectBoundsAndValidatesTrailingPadding(t *testing.T) {
	for _, tc := range []struct {
		name    string
		padding []byte
		wantErr bool
	}{
		{"record padding", make([]byte, 9*1024), false},
		{"excessive padding", make([]byte, 10*1024+1), true},
		{"hidden payload", []byte("ignored source"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.gz")
			f := openTestArchive(t, path, true)
			gz := gzip.NewWriter(f)
			tw := tar.NewWriter(gz)
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := gz.Write(tc.padding); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			_, err := Inspect(f, Limits{MaxEntries: 10})
			if (err != nil) != tc.wantErr {
				t.Fatalf("Inspect error = %v", err)
			}
		})
	}
}

func TestApplyWrongTargetDoesNotTouchOutput(t *testing.T) {
	dir := t.TempDir()
	basePath, deltaPath := filepath.Join(dir, "base.gz"), filepath.Join(dir, "delta.gz")
	writeTestArchive(t, basePath, map[string]string{"a": "same"})
	writeTestArchive(t, deltaPath, nil)
	base, delta := openTestArchive(t, basePath, false), openTestArchive(t, deltaPath, false)
	output := openTestArchive(t, filepath.Join(dir, "output.gz"), true)
	if _, err := output.Write([]byte("untouched")); err != nil {
		t.Fatal(err)
	}
	limits := Limits{MaxEntries: 10}
	manifest, err := Inspect(base, limits)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Apply(base, delta, output, manifest.Revision, strings.Repeat("a", 64), nil, limits)
	if !errors.Is(err, ErrTargetRevision) {
		t.Fatalf("error = %v", err)
	}
	data, err := os.ReadFile(output.Name())
	if err != nil || !bytes.Equal(data, []byte("untouched")) {
		t.Fatalf("output = %q, err=%v; target preflight must not write", data, err)
	}
}

func openTestArchive(t *testing.T, filename string, writable bool) *os.File {
	t.Helper()
	flags := os.O_RDONLY
	if writable {
		flags = os.O_CREATE | os.O_TRUNC | os.O_RDWR
	}
	f, err := os.OpenFile(filename, flags, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
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
