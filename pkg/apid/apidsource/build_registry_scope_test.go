package apidsource

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func writeScopeArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "src.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []interface{ Close() error }{tw, gz, f} {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// Slice 3j-4: Enqueue records the repositories the source's Dockerfiles
// name, an empty scope without a Dockerfile, and none without a readable
// archive.
func TestSourceBuildRegistryScope(t *testing.T) {
	archive := writeScopeArchive(t, map[string]string{
		"Dockerfile":     "ARG BASE=ghcr.io/acme/base\nFROM ${BASE}\n",
		"build/web.file": "FROM quay.io/acme/web\n",
	})
	got := sourceBuildRegistryScope(EnqueueParams{AppID: "app", SourcePath: archive, DockerfilePath: "build/web.file",
		DockerfileBuild: &api.DockerfileBuildOptions{Args: map[string]string{"BASE": "ghcr.io/acme/override"}}})
	if want := []string{"ghcr.io/acme/override", "quay.io/acme/web"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scope = %v, want %v", got, want)
	}
	plain := writeScopeArchive(t, map[string]string{"main.go": "package main\n"})
	if got := sourceBuildRegistryScope(EnqueueParams{AppID: "app", SourcePath: plain}); got == nil || len(got) != 0 {
		t.Fatalf("no Dockerfile = %#v, want an empty recorded scope", got)
	}
	if got := sourceBuildRegistryScope(EnqueueParams{AppID: "app", SourcePath: filepath.Join(t.TempDir(), "missing.tar.gz")}); got != nil {
		t.Fatalf("unreadable archive = %#v, want unrecorded", got)
	}
	if got := sourceBuildRegistryScope(EnqueueParams{AppID: "app"}); got != nil {
		t.Fatalf("no archive = %#v, want unrecorded", got)
	}
}
