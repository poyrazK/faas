package runtimequalification

// adr: 601

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStagedAssetRejectsChangedTruncatedAndOversizedBytes(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		limit            int64
		good             bool
	}{{"exact", "native", "native", 6, true}, {"changed", "other", "native", 6, false}, {"empty", "", "", 6, false}, {"oversized", "native!", "native!", 6, false}, {"truncated", "nativ", "native", 6, false}} {
		t.Run(tc.name, func(t *testing.T) {
			err := stageNativeAsset(t.Context(), io.NopCloser(strings.NewReader(tc.body)), filepath.Join(t.TempDir(), "asset"), SHA256([]byte(tc.want)), tc.limit)
			if (err == nil) != tc.good {
				t.Fatal(err)
			}
		})
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := stageNativeAsset(canceled, io.NopCloser(strings.NewReader("native")), filepath.Join(t.TempDir(), "asset"), SHA256([]byte("native")), 6); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNativeSourceArchiveCannotInjectLinksOrEscape(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		kind       byte
		mode       int64
		duplicate  bool
		limit      int64
		good       bool
	}{
		{name: "regular", path: "pkg/native.go", kind: tar.TypeReg, mode: 0o644, limit: 4096, good: true},
		{name: "escape", path: "../outside", kind: tar.TypeReg, mode: 0o644, limit: 4096},
		{name: "absolute", path: "/outside", kind: tar.TypeReg, mode: 0o644, limit: 4096},
		{name: "symlink", path: "link", kind: tar.TypeSymlink, mode: 0o777, limit: 4096},
		{name: "hardlink", path: "link", kind: tar.TypeLink, mode: 0o644, limit: 4096},
		{name: "device", path: "device", kind: tar.TypeChar, mode: 0o644, limit: 4096},
		{name: "setuid", path: "pkg/native.go", kind: tar.TypeReg, mode: 0o4644, limit: 4096},
		{name: "duplicate", path: "pkg/native.go", kind: tar.TypeReg, mode: 0o644, duplicate: true, limit: 8192},
		{name: "archive budget", path: "pkg/native.go", kind: tar.TypeReg, mode: 0o644, limit: 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw bytes.Buffer
			writer := tar.NewWriter(&raw)
			times := 1
			if tc.duplicate {
				times = 2
			}
			for range times {
				h := &tar.Header{Name: tc.path, Typeflag: tc.kind, Mode: tc.mode, Linkname: "../outside"}
				if tc.kind == tar.TypeReg {
					h.Size = 6
				}
				if err := writer.WriteHeader(h); err != nil {
					t.Fatal(err)
				}
				if h.Size > 0 {
					if _, err := writer.Write([]byte("native")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			err := extractNativeSource(t.Context(), bytes.NewReader(raw.Bytes()), dir, tc.limit)
			if (err == nil) != tc.good {
				t.Fatal(err)
			}
			if tc.good {
				if got, err := os.ReadFile(filepath.Join(dir, tc.path)); err != nil || string(got) != "native" {
					t.Fatal(err, string(got))
				}
			}
		})
	}
}
