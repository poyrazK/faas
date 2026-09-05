package tarball

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func writeSourceArchive(t *testing.T, entries map[string]string, wrapped, pax bool) string {
	t.Helper()
	var body bytes.Buffer
	gz := gzip.NewWriter(&body)
	tw := tar.NewWriter(gz)
	if pax {
		if err := tw.WriteHeader(&tar.Header{
			Name:       "pax_global_header",
			Typeflag:   tar.TypeXGlobalHeader,
			PAXRecords: map[string]string{"comment": "github codeload metadata"},
		}); err != nil {
			t.Fatalf("write pax header: %v", err)
		}
	}
	if wrapped {
		if err := tw.WriteHeader(&tar.Header{Name: "project/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatalf("write project directory: %v", err)
		}
	}
	for name, content := range entries {
		if wrapped {
			name = "project/" + name
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))}); err != nil {
			t.Fatalf("write header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	path := filepath.Join(t.TempDir(), "source.tar.gz")
	if err := os.WriteFile(path, body.Bytes(), 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	return path
}

func TestRootPrefix_GitHubCodeloadPAX(t *testing.T) {
	path := writeSourceArchive(t, map[string]string{"package.json": "{}"}, true, true)
	got, err := RootPrefix(path)
	if err != nil {
		t.Fatalf("RootPrefix: %v", err)
	}
	if got != "project" {
		t.Fatalf("RootPrefix = %q, want project", got)
	}
}

func TestRootPrefix_FlatArchive(t *testing.T) {
	path := writeSourceArchive(t, map[string]string{"package.json": "{}"}, false, false)
	got, err := RootPrefix(path)
	if err != nil {
		t.Fatalf("RootPrefix: %v", err)
	}
	if got != "" {
		t.Fatalf("RootPrefix = %q, want empty for flat archive", got)
	}
}

func TestRootPrefix_MixedRootsAreNotStripped(t *testing.T) {
	path := writeSourceArchive(t, map[string]string{
		"project/package.json": "{}",
		"README.md":            "readme",
	}, false, false)
	got, err := RootPrefix(path)
	if err != nil {
		t.Fatalf("RootPrefix: %v", err)
	}
	if got != "" {
		t.Fatalf("RootPrefix = %q, want empty for mixed roots", got)
	}
}

func TestResolveSourceRoot_PrefersDirectFlatPath(t *testing.T) {
	path := writeSourceArchive(t, map[string]string{
		"apps/api/package.json": "{}",
	}, false, false)
	got, err := ResolveSourceRoot(path, "apps/api")
	if err != nil {
		t.Fatalf("ResolveSourceRoot: %v", err)
	}
	if got != "apps/api" {
		t.Fatalf("ResolveSourceRoot = %q, want apps/api", got)
	}
}

func TestResolveSourceRoot_UsesTransportWrapperWhenDirectPathAbsent(t *testing.T) {
	path := writeSourceArchive(t, map[string]string{
		"apps/api/package.json": "{}",
	}, true, true)
	got, err := ResolveSourceRoot(path, "apps/api")
	if err != nil {
		t.Fatalf("ResolveSourceRoot: %v", err)
	}
	if got != "project/apps/api" {
		t.Fatalf("ResolveSourceRoot = %q, want project/apps/api", got)
	}
}
