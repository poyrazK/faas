package builderd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// makeTarball produces a tarball at path whose root contains the given
// filenames (used to seed detector fixtures). Empty content is fine.
func makeTarball(t *testing.T, path string, names []string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, n := range names {
		hdr := &tar.Header{Name: n, Mode: 0o644, Size: 0, Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetector_Node(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.tar.gz")
	makeTarball(t, path, []string{"package.json", "index.js", "lib/util.js"})

	d := NewDetector()
	got, err := d.Detect(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkNode {
		t.Errorf("framework = %s, want node", got)
	}
}

func TestDetector_Python(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.tar.gz")
	makeTarball(t, path, []string{"requirements.txt", "main.py"})

	d := NewDetector()
	got, err := d.Detect(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkPython {
		t.Errorf("framework = %s, want python", got)
	}
}

func TestDetector_DockerfileWins(t *testing.T) {
	// A Dockerfile at the root wins over package.json — matches the user
	// experience of `faas deploy --dockerfile`.
	dir := t.TempDir()
	path := filepath.Join(dir, "src.tar.gz")
	makeTarball(t, path, []string{"Dockerfile", "package.json"})

	d := NewDetector()
	got, err := d.Detect(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkDocker {
		t.Errorf("framework = %s, want docker", got)
	}
}

func TestDetector_Unknown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.tar.gz")
	makeTarball(t, path, []string{"README.md", "src/main.c"})

	d := NewDetector()
	if _, err := d.Detect(path); err == nil {
		t.Error("expected error on unrecognized source")
	}
}

func TestDetector_NestedEntriesIgnored(t *testing.T) {
	// package.json buried in a subdir is NOT a project-level package.json.
	dir := t.TempDir()
	path := filepath.Join(dir, "src.tar.gz")
	makeTarball(t, path, []string{"subdir/package.json", "requirements.txt"})

	d := NewDetector()
	got, err := d.Detect(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkPython {
		t.Errorf("framework = %s, want python (top-level wins)", got)
	}
}

func TestDetector_BadTarball(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.tar.gz")
	if err := os.WriteFile(path, []byte("not a tarball"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDetector().Detect(path); err == nil {
		t.Error("expected error on malformed tarball")
	}
}

func TestDetector_MissingFile(t *testing.T) {
	if _, err := NewDetector().Detect("/no/such/file.tar.gz"); err == nil {
		t.Error("expected error on missing file")
	}
}
