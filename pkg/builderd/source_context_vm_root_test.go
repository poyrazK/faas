package builderd

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func writeSourceArchive(t *testing.T, names ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "src.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, name := range names {
		body := []byte("x")
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
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
	return path
}

// The builder VM extracts the archive as delivered, so a non-default root
// resolves through a transport wrapper; a GitOps subdirectory build without
// this ran Railpack in an empty directory beside the wrapped source.
func TestVMSourceRootResolvesTransportWrapper(t *testing.T) {
	t.Parallel()
	gitops := writeSourceArchive(t, "gregale-source/jobs/report/package.json", "gregale-source/jobs/report/index.js")
	direct := writeSourceArchive(t, "jobs/report/package.json", "README.md")
	for _, tc := range []struct {
		name, archive, root, want, workdir string
	}{
		{"gitops wrapper", gitops, "jobs/report", "gregale-source/jobs/report", "/build/src/gregale-source/jobs/report"},
		{"unwrapped workspace", direct, "jobs/report", "jobs/report", "/build/src/jobs/report"},
		{"default root keeps guest flatten", gitops, "", "", "/build/src"},
		{"dot root keeps guest flatten", gitops, ".", ".", "/build/src"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vmSourceRoot(tc.archive, tc.root)
			if err != nil || got != tc.want {
				t.Fatalf("vmSourceRoot = %q, %v; want %q", got, err, tc.want)
			}
			workdir, err := buildWorkdir(got)
			if err != nil || workdir != tc.workdir {
				t.Fatalf("workdir = %q, %v; want %q", workdir, err, tc.workdir)
			}
		})
	}
	if _, err := vmSourceRoot(filepath.Join(t.TempDir(), "missing.tar.gz"), "jobs/report"); err == nil {
		t.Fatal("a missing archive resolved a root")
	}
}
