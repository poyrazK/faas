package imaged

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
)

type localOCIIntegrityFixture struct {
	config, layer, manifest []byte
	entries                 map[string][]byte
}

func newLocalOCIIntegrityFixture(t *testing.T) *localOCIIntegrityFixture {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	body := []byte("approved-code")
	if err := tw.WriteHeader(&tar.Header{Name: "app/server", Mode: 0755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	f := &localOCIIntegrityFixture{config: minimalConfigBytes(raw.Bytes()), layer: gzipBytes(t, raw.Bytes()), entries: map[string][]byte{}}
	f.rebind(t)
	return f
}

func (f *localOCIIntegrityFixture) rebind(t *testing.T) {
	t.Helper()
	f.manifest = minimalManifestBytes(f.config, f.layer)
	f.entries = map[string][]byte{
		"index.json":                       minimalIndexBytes(f.manifest),
		blobPath(digestFor(t, f.manifest)): f.manifest,
		blobPath(digestFor(t, f.config)):   f.config,
		blobPath(digestFor(t, f.layer)):    f.layer,
	}
}

func (f *localOCIIntegrityFixture) changeManifest(t *testing.T, mutate func(*oci.Manifest)) {
	t.Helper()
	var manifest oci.Manifest
	if err := json.Unmarshal(f.manifest, &manifest); err != nil {
		t.Fatal(err)
	}
	mutate(&manifest)
	delete(f.entries, blobPath(digestFor(t, f.manifest)))
	f.manifest, _ = json.Marshal(manifest)
	f.entries[blobPath(digestFor(t, f.manifest))] = f.manifest
	f.entries["index.json"] = minimalIndexBytes(f.manifest)
}

func TestLocalOCIIntegrityRejectsContentSubstitution(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		mutate     func(*testing.T, *localOCIIntegrityFixture)
	}{
		{"manifest", "does not match its descriptor", func(t *testing.T, f *localOCIIntegrityFixture) {
			f.entries[blobPath(digestFor(t, f.manifest))] = bytes.Replace(f.manifest, []byte(`"schemaVersion":2`), []byte(`"schemaVersion":1`), 1)
		}},
		{"config", "digest does not match its descriptor", func(t *testing.T, f *localOCIIntegrityFixture) {
			f.entries[blobPath(digestFor(t, f.config))] = bytes.Replace(f.config, []byte("amd64"), []byte("arm64"), 1)
		}},
		{"layer", "digest does not match its descriptor", func(t *testing.T, f *localOCIIntegrityFixture) {
			changed := bytes.Clone(f.layer)
			changed[len(changed)/2] ^= 1
			f.entries[blobPath(digestFor(t, f.layer))] = changed
		}},
		{"invented DiffID", "uncompressed DiffID", func(t *testing.T, f *localOCIIntegrityFixture) {
			f.config = minimalConfigBytes([]byte("different-uncompressed-code"))
			f.rebind(t)
		}},
		{"gzip checksum", "invalid checksum", func(t *testing.T, f *localOCIIntegrityFixture) {
			f.layer[len(f.layer)-8] ^= 1
			f.rebind(t) // a matching compressed digest is insufficient
		}},
		{"gzip truncated", "unexpected EOF", func(t *testing.T, f *localOCIIntegrityFixture) {
			f.layer = f.layer[:len(f.layer)-3]
			f.rebind(t)
		}},
		{"gzip trailing content", "gzip", func(t *testing.T, f *localOCIIntegrityFixture) {
			f.layer = append(f.layer, []byte("unsigned-trailing-bytes")...)
			f.rebind(t)
		}},
		{"missing layer", "not found", func(t *testing.T, f *localOCIIntegrityFixture) {
			delete(f.entries, blobPath(digestFor(t, f.layer)))
		}},
		{"missing config", "not found", func(t *testing.T, f *localOCIIntegrityFixture) {
			delete(f.entries, blobPath(digestFor(t, f.config)))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLocalOCIIntegrityFixture(t)
			tc.mutate(t, f)
			_, readers, cleanup, err := loadLocalOCIArchive(buildLocalOCIArchive(t, f.entries))
			cleanup()
			if err == nil || !strings.Contains(err.Error(), tc.want) || len(readers) != 0 {
				t.Fatalf("readers=%d err=%v, want refusal containing %q", len(readers), err, tc.want)
			}
		})
	}
}

func TestLocalOCIIntegrityRejectsInvalidDescriptors(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		mutate     func(*oci.Manifest)
	}{
		{"config size too small", "exceeds", func(m *oci.Manifest) { m.Config.Size-- }},
		{"config size too large", "size does not match", func(m *oci.Manifest) { m.Config.Size++ }},
		{"negative config size", "size limit", func(m *oci.Manifest) { m.Config.Size = -1 }},
		{"oversize config", "size limit", func(m *oci.Manifest) { m.Config.Size = api.LocalOCIMaxConfigBytes + 1 }},
		{"layer size too small", "exceeds", func(m *oci.Manifest) { m.Layers[0].Size-- }},
		{"layer size too large", "size does not match", func(m *oci.Manifest) { m.Layers[0].Size++ }},
		{"negative layer size", "size limit", func(m *oci.Manifest) { m.Layers[0].Size = -1 }},
		{"oversize layers", "size limit", func(m *oci.Manifest) { m.Layers[0].Size = api.LocalOCIMaxCompressedLayerBytes + 1 }},
		{"uncompressed layer type", "media type", func(m *oci.Manifest) { m.Layers[0].MediaType = "application/vnd.oci.image.layer.v1.tar" }},
		{"uppercase digest", "noncanonical", func(m *oci.Manifest) {
			m.Config.Digest = "sha256:" + strings.ToUpper(strings.TrimPrefix(m.Config.Digest, "sha256:"))
		}},
		{"schema", "format", func(m *oci.Manifest) { m.SchemaVersion = 1 }},
		{"manifest type", "format", func(m *oci.Manifest) { m.MediaType = "application/vnd.oci.image.index.v1+json" }},
		{"config type", "media type", func(m *oci.Manifest) { m.Config.MediaType = "application/octet-stream" }},
		{"too many layers", "count limit", func(m *oci.Manifest) { m.Layers = make([]oci.Descriptor, api.LocalOCIMaxLayers+1) }},
		{"aggregate compressed size", "size limit", func(m *oci.Manifest) {
			m.Layers[0].Size = api.LocalOCIMaxCompressedLayerBytes
			m.Layers = append(m.Layers, m.Layers[0])
		}},
		{"conflicting repeated descriptor", "conflicting", func(m *oci.Manifest) {
			m.Layers = append(m.Layers, m.Layers[0])
			m.Layers[1].Size++
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLocalOCIIntegrityFixture(t)
			f.changeManifest(t, tc.mutate)
			_, _, cleanup, err := loadLocalOCIArchive(buildLocalOCIArchive(t, f.entries))
			cleanup()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want refusal containing %q", err, tc.want)
			}
		})
	}
}

// Preserve order and entry type to exercise tar ambiguity that a map fixture
// cannot represent. No host paths or link targets are extracted.
func localOCIIntegrityArchive(t *testing.T, f *localOCIIntegrityFixture, target string, duplicate bool, entryType byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact.tar")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(file)
	write := func(name string, body []byte, typ byte) {
		hdr := &tar.Header{Name: name, Mode: 0600, Typeflag: typ, Size: int64(len(body))}
		if typ == tar.TypeSymlink || typ == tar.TypeLink {
			hdr.Linkname, hdr.Size, body = "host-only-file", 0, nil
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range f.entries {
		typ := byte(tar.TypeReg)
		if name == target && !duplicate {
			typ = entryType
		}
		write(name, body, typ)
	}
	if duplicate {
		write(target, f.entries[target], tar.TypeReg)
	}
	if err := errors.Join(tw.Close(), file.Close()); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLocalOCIIntegrityRejectsArchiveAmbiguity(t *testing.T) {
	for _, component := range []string{"index", "manifest", "config", "layer"} {
		for _, mode := range []string{"duplicate", "symlink", "hardlink"} {
			t.Run(component+"/"+mode, func(t *testing.T) {
				f := newLocalOCIIntegrityFixture(t)
				target := map[string]string{"index": "index.json", "manifest": blobPath(digestFor(t, f.manifest)), "config": blobPath(digestFor(t, f.config)), "layer": blobPath(digestFor(t, f.layer))}[component]
				typ := byte(tar.TypeSymlink)
				if mode == "hardlink" {
					typ = tar.TypeLink
				}
				path := localOCIIntegrityArchive(t, f, target, mode == "duplicate", typ)
				_, _, cleanup, err := loadLocalOCIArchive(path)
				cleanup()
				want := "not a regular file"
				if mode == "duplicate" {
					want = "duplicate"
				}
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("err=%v, want refusal containing %q", err, want)
				}
			})
		}
	}
}

func TestLocalOCIIntegrityRepeatedLayerHasIndependentReaders(t *testing.T) {
	f := newLocalOCIIntegrityFixture(t)
	var cfg map[string]any
	if err := json.Unmarshal(f.config, &cfg); err != nil {
		t.Fatal(err)
	}
	root := cfg["rootfs"].(map[string]any)
	diffID := root["diff_ids"].([]any)[0]
	root["diff_ids"] = []any{diffID, diffID}
	f.config, _ = json.Marshal(cfg)
	f.rebind(t)
	f.changeManifest(t, func(m *oci.Manifest) { m.Layers = append(m.Layers, m.Layers[0]) })
	_, readers, cleanup, err := loadLocalOCIArchive(buildLocalOCIArchive(t, f.entries))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if len(readers) != 2 {
		t.Fatalf("readers=%d", len(readers))
	}
	for i, reader := range readers {
		body, err := io.ReadAll(reader)
		if err != nil || !bytes.Equal(body, f.layer) {
			t.Fatalf("layer %d=%d bytes err=%v", i, len(body), err)
		}
	}
}

func TestLocalOCIIntegrityRefusesBeforeRootfsConversion(t *testing.T) {
	f := newLocalOCIIntegrityFixture(t)
	f.entries[blobPath(digestFor(t, f.layer))] = bytes.Repeat([]byte("x"), len(f.layer))
	archive := buildLocalOCIArchive(t, f.entries)
	h := newFunctionTestHarness(t, api.PlanHobby, RuntimeGo124)
	h.dep.RootfsPath = archive
	handler := New(h.store, h.notif, fakePuller{}, h.bld, "./init", h.appsR, silentLogger())
	if err := handler.buildLocalOCIAppLayer(context.Background(), h.app, h.dep, h.acct); err == nil {
		t.Fatal("container conversion accepted corrupt source build")
	}
	for _, runtime := range []string{RuntimeGo124, RuntimeGo124Alpine, RuntimeNode22, RuntimePython312} {
		_, _, cleanup, err := handler.functionBuildArtifact(context.Background(), runtime, archive)
		cleanup()
		if err == nil {
			t.Fatalf("function runtime %s accepted corrupt source build", runtime)
		}
	}
	if len(h.bld.calls) != 0 {
		t.Fatal("rootfs builder consumed an unverified source artifact")
	}
}

func TestLocalOCIIntegrityBoundsUncompressedWorkAndCancellation(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 4096)
	file, err := os.CreateTemp(t.TempDir(), "layer-*.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(gzipBytes(t, payload)); err != nil {
		t.Fatal(err)
	}
	diffID := digestFor(t, payload)
	if err := verifyLocalOCIDiffIDs(context.Background(), []*os.File{file}, []string{diffID}, 128); err == nil || !strings.Contains(err.Error(), "uncompressed size limit") {
		t.Fatalf("gzip expansion accepted: %v", err)
	}
	if err := verifyLocalOCIDiffIDs(context.Background(), []*os.File{file, file}, []string{diffID, diffID}, int64(len(payload))); err == nil || !strings.Contains(err.Error(), "uncompressed size limit") {
		t.Fatalf("aggregate expansion accepted: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := verifyLocalOCIDiffIDs(ctx, []*os.File{file}, []string{diffID}, int64(len(payload))); !errors.Is(err, context.Canceled) {
		t.Fatalf("decompression ignored cancellation: %v", err)
	}
	f := newLocalOCIIntegrityFixture(t)
	_, _, cleanup, err := loadLocalOCIArchiveContext(ctx, buildLocalOCIArchive(t, f.entries))
	cleanup()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("archive verification ignored cancellation: %v", err)
	}
}

func TestLocalOCIIntegrityUsesOpenedArchiveAfterPathReplacement(t *testing.T) {
	f := newLocalOCIIntegrityFixture(t)
	path := buildLocalOCIArchive(t, f.entries)
	archive, err := openLocalOCIArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = archive.Close() }()
	index, err := readLocalOCIEntryFrom(context.Background(), archive, "index.json", api.LocalOCIMaxIndexBytes)
	if err != nil || !bytes.Equal(index, f.entries["index.json"]) {
		t.Fatalf("index err=%v", err)
	}
	replacement := buildLocalOCIArchive(t, map[string][]byte{"index.json": []byte("corrupt replacement")})
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	manifest, err := readLocalOCIDescriptor(context.Background(), archive, localOCITestDescriptor("application/vnd.oci.image.manifest.v1+json", f.manifest), api.LocalOCIMaxManifestBytes)
	if err != nil || !bytes.Equal(manifest, f.manifest) {
		t.Fatalf("opened archive changed: err=%v", err)
	}
}

func TestLocalOCIIntegrityRejectsOversizeArchiveBeforeRead(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "oversize-*.tar")
	if err != nil {
		t.Fatal(err)
	}
	// A sparse file exercises the declared archive bound without allocating
	// sixteen GiB of test disk or reading any of its bytes.
	if err := file.Truncate(api.LocalOCIMaxArchiveBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, cleanup, err := loadLocalOCIArchive(file.Name())
	cleanup()
	if err == nil || !strings.Contains(err.Error(), "archive exceeds size limit") {
		t.Fatalf("oversize archive accepted: %v", err)
	}
}
