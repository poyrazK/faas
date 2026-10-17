package apidsource

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"testing"
)

func TestCanonicalGitContextSpoolPublishesCompleteBytesAndCleansFailures(t *testing.T) {
	var source bytes.Buffer
	gz := gzip.NewWriter(&source)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "repo/src/app.js", Typeflag: tar.TypeReg, Mode: 0o644, Size: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("app")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	spool := t.TempDir()
	name, n, err := CanonicalGitBuildContext(bytes.NewReader(source.Bytes()), spool, "src", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(name)
	if err != nil || n != info.Size() || info.Mode().Perm() != 0o640 {
		t.Fatalf("spool receipt/mode mismatch: bytes=%d info=%v error=%v", n, info, err)
	}
	if _, _, err := CanonicalGitBuildContext(bytes.NewReader([]byte("invalid gzip")), spool, "src", 1<<20); err == nil {
		t.Fatal("invalid archive published")
	}
	files, err := os.ReadDir(spool)
	if err != nil || len(files) != 1 {
		t.Fatal("failed canonicalization leaked a partial spool", files, err)
	}
}
