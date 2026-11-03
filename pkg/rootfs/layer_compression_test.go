package rootfs_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/rootfs"
)

func layerTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func gzipLayer(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zstdLayer(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, err := rootfs.NewLayerWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDetectLayerCompression(t *testing.T) {
	raw := layerTar(t, map[string]string{"app/a": "a"})
	for _, tc := range []struct {
		name string
		head []byte
		want rootfs.LayerCompression
	}{
		{"gzip", gzipLayer(t, raw), rootfs.LayerGzip},
		{"zstd", zstdLayer(t, raw), rootfs.LayerZstd},
		{"plain tar", raw, ""},
		{"empty", nil, ""},
		{"short gzip prefix", []byte{0x1f}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := rootfs.DetectLayerCompression(tc.head); got != tc.want {
				t.Fatalf("DetectLayerCompression = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOpenCompressedLayerRoundTrips(t *testing.T) {
	raw := layerTar(t, map[string]string{"app/server.js": "console.log(1)", "app/package.json": "{}"})
	for _, tc := range []struct {
		name string
		blob []byte
	}{
		{"gzip", gzipLayer(t, raw)},
		{"zstd", zstdLayer(t, raw)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc, err := rootfs.OpenCompressedLayer(bytes.NewReader(tc.blob))
			if err != nil {
				t.Fatalf("OpenCompressedLayer: %v", err)
			}
			got, err := io.ReadAll(rc)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if err := rc.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}
			if !bytes.Equal(got, raw) {
				t.Fatal("decompressed layer differs from the original tar")
			}
		})
	}
}

func TestOpenCompressedLayerRejectsUncompressedAndEmpty(t *testing.T) {
	raw := layerTar(t, map[string]string{"app/a": "a"})
	for name, blob := range map[string][]byte{"plain tar": raw, "empty": nil, "one byte": {0x28}} {
		t.Run(name, func(t *testing.T) {
			if _, err := rootfs.OpenCompressedLayer(bytes.NewReader(blob)); !errors.Is(err, rootfs.ErrUnsupportedLayerCompression) {
				t.Fatalf("OpenCompressedLayer error = %v, want ErrUnsupportedLayerCompression", err)
			}
		})
	}
}

func TestApplyLayerGzAcceptsZstdAndGzip(t *testing.T) {
	raw := layerTar(t, map[string]string{"app/server.js": "zstd or gzip"})
	for _, tc := range []struct {
		name string
		blob []byte
	}{
		{"gzip", gzipLayer(t, raw)},
		{"zstd", zstdLayer(t, raw)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dst := t.TempDir()
			if err := rootfs.ApplyLayerGz(dst, bytes.NewReader(tc.blob)); err != nil {
				t.Fatalf("ApplyLayerGz: %v", err)
			}
			got, err := os.ReadFile(filepath.Join(dst, "app", "server.js"))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "zstd or gzip" {
				t.Fatalf("applied content = %q", got)
			}
		})
	}
}

func TestApplyLayerGzRejectsTrailingBytesAfterZstd(t *testing.T) {
	raw := layerTar(t, map[string]string{"app/a": "a"})
	blob := append(zstdLayer(t, raw), []byte("not a zstd frame")...)
	if err := rootfs.ApplyLayerGz(t.TempDir(), bytes.NewReader(blob)); err == nil {
		t.Fatal("ApplyLayerGz accepted bytes after the final zstd frame")
	}
}

func TestApplyLayersGzWithinBudgetsZstdExpansion(t *testing.T) {
	raw := layerTar(t, map[string]string{"app/big": string(bytes.Repeat([]byte("x"), 64<<10))})
	err := rootfs.ApplyLayersGzWithin(t.TempDir(), []io.Reader{bytes.NewReader(zstdLayer(t, raw))}, 1<<10)
	var budget *rootfs.ErrLayerExtractionExceedsBudget
	if !errors.As(err, &budget) {
		t.Fatalf("ApplyLayersGzWithin error = %v, want the extraction budget error", err)
	}
}
