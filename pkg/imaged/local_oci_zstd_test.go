package imaged

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
)

const (
	gzipLayerMediaType = "application/vnd.oci.image.layer.v1.tar+gzip"
	zstdLayerMediaType = "application/vnd.oci.image.layer.v1.tar+zstd"
)

func zstdBytes(t *testing.T, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, err := rootfs.NewLayerWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// localOCILayer is one layer of a synthetic build image: its uncompressed
// payload, the blob as stored, and the media type its descriptor declares.
type localOCILayer struct {
	raw       []byte
	blob      []byte
	mediaType string
}

// buildMixedLocalOCIArchive writes an OCI layout whose layers may each carry
// their own compression, as a BuildKit export with compression=zstd does when
// the base image's layers stay gzip.
func buildMixedLocalOCIArchive(t *testing.T, layers []localOCILayer) string {
	t.Helper()
	raws := make([][]byte, len(layers))
	for i, layer := range layers {
		raws[i] = layer.raw
	}
	cfg := minimalConfigBytes(raws...)
	m := oci.Manifest{
		SchemaVersion: 2,
		MediaType:     "application/vnd.oci.image.manifest.v1+json",
		Config:        localOCITestDescriptor("application/vnd.oci.image.config.v1+json", cfg),
	}
	entries := map[string][]byte{blobPath(digestFor(t, cfg)): cfg}
	for _, layer := range layers {
		m.Layers = append(m.Layers, localOCITestDescriptor(layer.mediaType, layer.blob))
		entries[blobPath(digestFor(t, layer.blob))] = layer.blob
	}
	manifest, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	entries[blobPath(digestFor(t, manifest))] = manifest
	entries["index.json"] = minimalIndexBytes(manifest)
	return buildLocalOCIArchive(t, entries)
}

func TestLoadLocalOCIArchiveAcceptsZstdAndMixedLayers(t *testing.T) {
	base := []byte("gzip base layer from the registry")
	app := []byte("zstd app layer from the builder")
	for _, tc := range []struct {
		name   string
		layers []localOCILayer
	}{
		{"all zstd", []localOCILayer{
			{raw: base, blob: zstdBytes(t, base), mediaType: zstdLayerMediaType},
			{raw: app, blob: zstdBytes(t, app), mediaType: zstdLayerMediaType},
		}},
		{"gzip base under zstd app", []localOCILayer{
			{raw: base, blob: gzipBytes(t, base), mediaType: gzipLayerMediaType},
			{raw: app, blob: zstdBytes(t, app), mediaType: zstdLayerMediaType},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, readers, cleanup, err := loadLocalOCIArchive(buildMixedLocalOCIArchive(t, tc.layers))
			if err != nil {
				t.Fatalf("loadLocalOCIArchive: %v", err)
			}
			defer cleanup()
			for i, r := range readers {
				rc, err := rootfs.OpenCompressedLayer(r)
				if err != nil {
					t.Fatalf("open layer %d: %v", i, err)
				}
				got, err := io.ReadAll(rc)
				_ = rc.Close()
				if err != nil {
					t.Fatalf("read layer %d: %v", i, err)
				}
				if !bytes.Equal(got, tc.layers[i].raw) {
					t.Fatalf("layer %d payload = %q", i, got)
				}
			}
		})
	}
}

func TestLoadLocalOCIArchiveRejectsCompressionMismatch(t *testing.T) {
	payload := []byte("layer bytes")
	for _, tc := range []struct {
		name  string
		layer localOCILayer
		want  string
	}{
		{"gzip bytes declared zstd", localOCILayer{raw: payload, blob: gzipBytes(t, payload), mediaType: zstdLayerMediaType}, "not compressed as its media type"},
		{"zstd bytes declared gzip", localOCILayer{raw: payload, blob: zstdBytes(t, payload), mediaType: gzipLayerMediaType}, "not compressed as its media type"},
		{"uncompressed tar type", localOCILayer{raw: payload, blob: payload, mediaType: "application/vnd.oci.image.layer.v1.tar"}, "unsupported OCI layer 0 media type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, cleanup, err := loadLocalOCIArchive(buildMixedLocalOCIArchive(t, []localOCILayer{tc.layer}))
			cleanup()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("loadLocalOCIArchive error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFilterAppLayersReadsZstdAndWritesZstd(t *testing.T) {
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for name, body := range map[string]string{"app/server.js": "ok", "root/.cache/x": "dropped"} {
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
	filtered, cleanup, err := makeFunctionAppLayers([]io.ReadCloser{io.NopCloser(bytes.NewReader(zstdBytes(t, raw.Bytes())))})
	if err != nil {
		t.Fatalf("makeFunctionAppLayers: %v", err)
	}
	defer cleanup()
	if len(filtered) != 1 {
		t.Fatalf("filtered layers = %d, want 1", len(filtered))
	}
	blob, err := io.ReadAll(filtered[0])
	if err != nil {
		t.Fatal(err)
	}
	if got := rootfs.DetectLayerCompression(blob); got != rootfs.LayerZstd {
		t.Fatalf("filtered layer compression = %q, want zstd", got)
	}
	staging := t.TempDir()
	if err := rootfs.ApplyLayerGz(staging, bytes.NewReader(blob)); err != nil {
		t.Fatalf("apply filtered layer: %v", err)
	}
}
