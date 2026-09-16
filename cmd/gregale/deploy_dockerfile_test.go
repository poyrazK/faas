package main

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDockerfilePreviewArchive(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := gzip.NewWriter(f)
	tw := tar.NewWriter(z)
	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidateExplicitDockerfileArchive(t *testing.T) {
	withRoot := writeDockerfilePreviewArchive(t, map[string]string{
		"repo/apps/api/Dockerfile":   "FROM node:22\n",
		"repo/apps/api/package.json": "{}",
	})
	if err := validateExplicitDockerfileArchive(withRoot, "apps/api"); err != nil {
		t.Fatalf("nested Dockerfile rejected: %v", err)
	}

	without := writeDockerfilePreviewArchive(t, map[string]string{"package.json": "{}"})
	if err := validateExplicitDockerfileArchive(without, ""); err == nil || !strings.Contains(err.Error(), "no Dockerfile") {
		t.Fatalf("missing Dockerfile error = %v", err)
	}
}
