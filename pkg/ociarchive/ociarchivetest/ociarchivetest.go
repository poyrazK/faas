// Package ociarchivetest builds small image archives for tests.
package ociarchivetest

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/ociarchive"
)

// DockerSave returns a legacy `docker save` tar of one image with one
// uncompressed layer, tagged tag, for arch, exposing port (0 for none).
func DockerSave(t testing.TB, tag, arch string, port int) []byte {
	t.Helper()
	layer := tarOf(t, "app/server", []byte("#!/bin/sh\necho hi\n"))
	sum := sha256.Sum256(layer)
	cfg := map[string]any{"Cmd": []string{"/app/server"}}
	if port > 0 {
		cfg["ExposedPorts"] = map[string]any{fmt.Sprintf("%d/tcp", port): map[string]any{}}
	}
	config, err := json.Marshal(map[string]any{
		"os": "linux", "architecture": arch, "config": cfg,
		"rootfs": map[string]any{"type": "layers", "diff_ids": []string{"sha256:" + hex.EncodeToString(sum[:])}},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal([]map[string]any{{"Config": "config.json", "RepoTags": []string{tag}, "Layers": []string{"layer/layer.tar"}}})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range []struct {
		name string
		body []byte
	}{{"config.json", config}, {"layer/layer.tar", layer}, {"manifest.json", manifest}} {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Canonical returns the canonical archive for a linux/amd64 image exposing
// port, and its manifest digest.
func Canonical(t testing.TB, port int) ([]byte, string) {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "save.tar")
	if err := os.WriteFile(in, DockerSave(t, "local/app:latest", "amd64", port), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	res, err := ociarchive.Convert(in, "", dir, &out)
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes(), res.ManifestDigest
}

func tarOf(t testing.TB, name string, body []byte) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
