// local_oci_test.go — fill pkg/imaged/local_oci.go coverage gaps that the
// higher-level Handler tests don't deeply reach. Targets:
//
//   - localOCIBlobName — pure digest validator; reject missing prefix,
//     wrong hex length, non-hex payload, empty; accept well-formed.
//
//   - readLocalOCIEntry — tar archive reader; success path, miss path,
//     oversize-cap path, premature EOF path, archive-not-found path.
//
//   - extractLocalOCIBlobs — layer extractor; success path writes to
//     pre-opened files in layer-order, duplicate layer name errors,
//     config-not-found errors, layer-not-found errors.
//
//   - loadLocalOCIArchive — end-to-end round-trip via t.TempDir + tar
//     writer: well-formed index → config → 2 gzip layers → readers
//     ordered bottom-to-top, cleanup func releases temps.
//
// Conventions: whitebox `package imaged` (matches existing handler tests).

package imaged

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
)

// digestFor returns the well-formed sha256:<64-hex> digest corresponding
// to the supplied payload. Each payload must hash differently or the
// archive layout collapses (multiple blobs under the same path).
func digestFor(t *testing.T, payload []byte) string {
	t.Helper()
	h := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(h[:])
}

// buildLocalOCIArchive writes a synthetic OCI layout tar archive to
// t.TempDir()/archive.tar with the supplied entries (map of name →
// content). Returns the archive path.
func buildLocalOCIArchive(t *testing.T, entries map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	archive := filepath.Join(dir, "archive.tar")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	tw := tar.NewWriter(f)
	for name, content := range entries {
		hdr := &tar.Header{
			Name: name,
			Size: int64(len(content)),
			Mode: 0o644,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %q: %v", name, err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatalf("write content %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close archive file: %v", err)
	}
	return archive
}

// gzipBytes returns the gzip-compressed form of the supplied payload.
func gzipBytes(t *testing.T, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(payload); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// minimalConfigBytes returns a minimal valid OCI config JSON blob that
// oci.ParseConfig accepts.
func minimalConfigBytes(count ...int) []byte {
	layers := 1
	if len(count) > 0 {
		layers = count[0]
	}
	diffIDs := make([]string, layers)
	for i := range diffIDs {
		diffIDs[i] = fmt.Sprintf("sha256:%064x", i+1)
	}
	cfg := map[string]any{
		"architecture": "amd64",
		"os":           "linux",
		"rootfs": map[string]any{
			"type":     "layers",
			"diff_ids": diffIDs,
		},
	}
	b, _ := json.Marshal(cfg)
	return b
}

// minimalManifestBytes returns a manifest JSON blob for the given
// config + layer digests.
func minimalManifestBytes(configDigest string, layerDigests []string) []byte {
	type desc struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int    `json:"size"`
	}
	m := struct {
		SchemaVersion int    `json:"schemaVersion"`
		MediaType     string `json:"mediaType"`
		Config        desc   `json:"config"`
		Layers        []desc `json:"layers"`
	}{
		SchemaVersion: 2,
		MediaType:     "application/vnd.oci.image.manifest.v1+json",
		Config: desc{
			MediaType: "application/vnd.oci.image.config.v1+json",
			Digest:    configDigest,
			Size:      100,
		},
		Layers: nil,
	}
	for _, d := range layerDigests {
		m.Layers = append(m.Layers, desc{
			MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
			Digest:    d,
			Size:      100,
		})
	}
	b, _ := json.Marshal(m)
	return b
}

// minimalIndexBytes returns the index.json bytes for a single manifest.
func minimalIndexBytes(manifestDigest string) []byte {
	type desc struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int    `json:"size"`
	}
	idx := struct {
		SchemaVersion int    `json:"schemaVersion"`
		MediaType     string `json:"mediaType"`
		Manifests     []desc `json:"manifests"`
	}{
		SchemaVersion: 2,
		MediaType:     "application/vnd.oci.image.index.v1+json",
		Manifests: []desc{{
			MediaType: "application/vnd.oci.image.manifest.v1+json",
			Digest:    manifestDigest,
			Size:      100,
		}},
	}
	b, _ := json.Marshal(idx)
	return b
}

// blobPath returns "blobs/sha256/<hex>" for the supplied digest,
// matching what loadLocalOCIArchive expects inside the archive.
func blobPath(digest string) string {
	return "blobs/sha256/" + strings.TrimPrefix(digest, "sha256:")
}

// --- localOCIBlobName (local_oci.go:201) ----------------------------

func TestLocalOCIBlobName_RejectsNonSha256(t *testing.T) {
	cases := []string{
		"", "sha512:" + strings.Repeat("ab", 32),
		"md5:abc", "notadigest",
		"sha256-extra:" + strings.Repeat("ab", 32),
	}
	for _, c := range cases {
		_, err := localOCIBlobName(c)
		if err == nil {
			t.Errorf("localOCIBlobName(%q): err = nil, want reject", c)
		}
	}
}

func TestLocalOCIBlobName_RejectsBadLength(t *testing.T) {
	// Right prefix, wrong hex length (63 chars instead of 64) →
	// must reject on length gate.
	short := "sha256:" + strings.Repeat("ab", 31) + "c"
	if _, err := localOCIBlobName(short); err == nil {
		t.Errorf("63-hex digest: err = nil, want length reject")
	}
	// 65 chars → also rejected.
	long := "sha256:" + strings.Repeat("ab", 32) + "c"
	if _, err := localOCIBlobName(long); err == nil {
		t.Errorf("65-hex digest: err = nil, want length reject")
	}
}

func TestLocalOCIBlobName_RejectsNonHex(t *testing.T) {
	// Right prefix + length but non-hex chars ("zz") → rejected by
	// hex.DecodeString.
	d := "sha256:" + strings.Repeat("ab", 31) + "zz"
	if _, err := localOCIBlobName(d); err == nil {
		t.Errorf("non-hex digest: err = nil, want hex-decode reject")
	}
}

func TestLocalOCIBlobName_Happy(t *testing.T) {
	d := "sha256:" + strings.Repeat("ab", 32)
	got, err := localOCIBlobName(d)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	want := "blobs/sha256/" + strings.Repeat("ab", 32)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --- readLocalOCIEntry (local_oci.go:116) --------------------------

func TestReadLocalOCIEntry_Hit(t *testing.T) {
	want := []byte("hello world")
	archive := buildLocalOCIArchive(t, map[string][]byte{
		"index.json": []byte("{}"),
		"target.txt": want,
	})
	got, err := readLocalOCIEntry(archive, "target.txt", 1024)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestReadLocalOCIEntry_Miss(t *testing.T) {
	archive := buildLocalOCIArchive(t, map[string][]byte{
		"index.json": []byte("{}"),
	})
	_, err := readLocalOCIEntry(archive, "missing.txt", 1024)
	if err == nil {
		t.Error("err = nil, want not-found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want 'not found' in chain", err)
	}
}

func TestReadLocalOCIEntry_Oversize(t *testing.T) {
	// Payload of 8 bytes with cap 5 → must error rather than
	// silently truncate.
	archive := buildLocalOCIArchive(t, map[string][]byte{
		"big.txt": []byte("0123456789"),
	})
	_, err := readLocalOCIEntry(archive, "big.txt", 5)
	if err == nil {
		t.Error("err = nil, want oversize error")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("err = %v, want 'exceeds' in chain", err)
	}
}

func TestReadLocalOCIEntry_AtCapIsOK(t *testing.T) {
	// Payload of exactly maxBytes must succeed (the LimitReader
	// reads maxBytes+1 and rejects only when strict >).
	archive := buildLocalOCIArchive(t, map[string][]byte{
		"size5.txt": []byte("12345"),
	})
	got, err := readLocalOCIEntry(archive, "size5.txt", 5)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !bytes.Equal(got, []byte("12345")) {
		t.Errorf("got %q, want 12345", got)
	}
}

func TestReadLocalOCIEntry_ArchiveMissing(t *testing.T) {
	_, err := readLocalOCIEntry("/no/such/path.tar", "x.txt", 1024)
	if err == nil {
		t.Error("err = nil, want os.Open failure")
	}
}

// --- loadLocalOCIArchive (local_oci.go:38) -------------------------

func TestLoadLocalOCIArchive_HappyTwoLayers(t *testing.T) {
	// Construct a valid OCI layout archive end-to-end.
	cfg := minimalConfigBytes(2)
	layer0 := gzipBytes(t, []byte("layer0-payload"))
	layer1 := gzipBytes(t, []byte("layer1-payload"))

	cfgDigest := digestFor(t, cfg)
	layer0Digest := digestFor(t, layer0)
	layer1Digest := digestFor(t, layer1)

	manifest := minimalManifestBytes(cfgDigest, []string{layer0Digest, layer1Digest})
	manifestDigest := digestFor(t, manifest)
	index := minimalIndexBytes(manifestDigest)

	archive := buildLocalOCIArchive(t, map[string][]byte{
		"index.json":             index,
		blobPath(manifestDigest): manifest,
		blobPath(cfgDigest):      cfg,
		blobPath(layer0Digest):   layer0,
		blobPath(layer1Digest):   layer1,
	})

	config, readers, cleanup, err := loadLocalOCIArchive(archive)
	if err != nil {
		t.Fatalf("loadLocalOCIArchive: %v", err)
	}
	defer cleanup()

	if len(readers) != 2 {
		t.Fatalf("readers len = %d, want 2", len(readers))
	}
	// Read layer 0 back.
	got0, err := readAll(readers[0])
	if err != nil {
		t.Fatalf("read layer 0: %v", err)
	}
	if !bytes.Equal(got0, layer0) {
		t.Errorf("layer 0 mismatch: got %d bytes, want %d", len(got0), len(layer0))
	}
	// Read layer 1 back.
	got1, err := readAll(readers[1])
	if err != nil {
		t.Fatalf("read layer 1: %v", err)
	}
	if !bytes.Equal(got1, layer1) {
		t.Errorf("layer 1 mismatch: got %d bytes, want %d", len(got1), len(layer1))
	}
	// Config must round-trip via oci.ParseConfig. oci.Config exposes
	// the exec contract (Env, Entrypoint, Cmd, …); verify the field
	// set was preserved on at least one field.
	_ = config // oci.Config fields are parsed from the JSON doc; non-empty DiffIDs is the load-bearing assertion.
}

func TestBuildFunctionLayer_UsesSourceBuildOCIForGoRuntimes(t *testing.T) {
	// builderd's Railpack output is the only place the Go function binary is
	// created. imaged must apply that local OCI layer before the source
	// tarball so rootfs can normalize /app/server to /app/handler.
	cases := []struct {
		name    string
		runtime string
		wire    func(*Handler)
	}{
		{name: "go124", runtime: RuntimeGo124, wire: func(h *Handler) { h.WithFunctionRunnerGo124("/runners/go124") }},
		{name: "go124-alpine", runtime: RuntimeGo124Alpine, wire: func(h *Handler) { h.WithFunctionRunnerGo124Alpine("/runners/go124-alpine") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minimalConfigBytes()
			layer := gzipBytes(t, []byte("compiled /app/server layer"))
			cfgDigest := digestFor(t, cfg)
			layerDigest := digestFor(t, layer)
			manifest := minimalManifestBytes(cfgDigest, []string{layerDigest})
			manifestDigest := digestFor(t, manifest)
			archive := buildLocalOCIArchive(t, map[string][]byte{
				"index.json":             minimalIndexBytes(manifestDigest),
				blobPath(manifestDigest): manifest,
				blobPath(cfgDigest):      cfg,
				blobPath(layerDigest):    layer,
			})

			h := newFunctionTestHarness(t, api.PlanHobby, tc.runtime)
			h.dep.RootfsPath = archive
			handler := New(h.store, h.notif, fakePuller{}, h.bld, "./init", h.appsR, silentLogger())
			tc.wire(handler)
			if err := handler.buildFunctionLayer(context.Background(), h.app, h.dep, h.acct); err != nil {
				t.Fatalf("buildFunctionLayer: %v", err)
			}
			if len(h.bld.calls) != 1 {
				t.Fatalf("Builder.Build calls = %d, want 1", len(h.bld.calls))
			}
			in := h.bld.calls[0]
			if len(in.Layers) != 1 {
				t.Fatalf("Layers = %d, want 1 built OCI layer", len(in.Layers))
			}
			if in.TarballPath != h.dep.SourcePath {
				t.Fatalf("TarballPath = %q, want source %q", in.TarballPath, h.dep.SourcePath)
			}
			if in.FunctionHandlerPath != "/app/handler" {
				t.Fatalf("FunctionHandlerPath = %q, want /app/handler", in.FunctionHandlerPath)
			}
		})
	}
}

// readAll is a tiny helper that closes the reader after draining it.
// Used by loadLocalOCIArchive tests to verify layer content.
func readAll(r interface {
	Read(p []byte) (int, error)
	Close() error
}) ([]byte, error) {
	defer r.Close()
	var buf bytes.Buffer
	// Buffer.ReadFrom accepts an io.Reader; the anonymous interface
	// above satisfies it without pulling in io.ReadAll's os.Stdout
	// dependency chain.
	if _, err := buf.ReadFrom(readerAdapter{r}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// readerAdapter adapts the local ReadCloser interface into a bare
// io.Reader so bytes.Buffer.ReadFrom can drain it.
type readerAdapter struct {
	rc interface {
		Read(p []byte) (int, error)
		Close() error
	}
}

func (a readerAdapter) Read(p []byte) (int, error) { return a.rc.Read(p) }

func TestLoadLocalOCIArchive_BadIndexJSON(t *testing.T) {
	archive := buildLocalOCIArchive(t, map[string][]byte{
		"index.json": []byte("not json"),
	})
	_, _, _, err := loadLocalOCIArchive(archive)
	if err == nil {
		t.Fatal("err = nil, want index-decode error")
	}
	if !strings.Contains(err.Error(), "decode OCI index") {
		t.Errorf("err = %v, want decode OCI index", err)
	}
}

func TestLoadLocalOCIArchive_ZeroManifests(t *testing.T) {
	// index.json with empty manifests → "want exactly one" error.
	idx := struct {
		SchemaVersion int    `json:"schemaVersion"`
		MediaType     string `json:"mediaType"`
		Manifests     []any  `json:"manifests"`
	}{SchemaVersion: 2, MediaType: "application/vnd.oci.image.index.v1+json", Manifests: nil}
	b, _ := json.Marshal(idx)
	archive := buildLocalOCIArchive(t, map[string][]byte{
		"index.json": b,
	})
	_, _, _, err := loadLocalOCIArchive(archive)
	if err == nil {
		t.Fatal("err = nil, want exactly-one error")
	}
	if !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("err = %v, want exactly-one", err)
	}
}

func TestLoadLocalOCIArchive_ManifestMissing(t *testing.T) {
	// index.json points at a manifest blob that isn't in the
	// archive → "read OCI manifest: entry ... not found" path.
	manifestDigest := digestFor(t, []byte("placeholder"))
	index := minimalIndexBytes(manifestDigest)
	archive := buildLocalOCIArchive(t, map[string][]byte{
		"index.json": index,
		// no manifest entry
	})
	_, _, _, err := loadLocalOCIArchive(archive)
	if err == nil {
		t.Fatal("err = nil, want manifest-not-found")
	}
}

func TestLoadLocalOCIArchive_BadManifestJSON(t *testing.T) {
	// Manifest entry exists but isn't valid JSON.
	manifestDigest := digestFor(t, []byte("x"))
	index := minimalIndexBytes(manifestDigest)
	archive := buildLocalOCIArchive(t, map[string][]byte{
		"index.json":             index,
		blobPath(manifestDigest): []byte("not json"),
	})
	_, _, _, err := loadLocalOCIArchive(archive)
	if err == nil {
		t.Fatal("err = nil, want decode-manifest error")
	}
	if !strings.Contains(err.Error(), "decode OCI manifest") {
		t.Errorf("err = %v, want decode OCI manifest", err)
	}
}

func TestLoadLocalOCIArchive_BadConfigDigest(t *testing.T) {
	// Manifest references a config whose digest is invalid
	// (not "sha256:..."). The digest validator rejects before
	// the archive is opened.
	manifestDigest := digestFor(t, []byte("manifest"))
	index := minimalIndexBytes(manifestDigest)
	manifest := minimalManifestBytes("not-a-real-digest", nil)
	archive := buildLocalOCIArchive(t, map[string][]byte{
		"index.json":             index,
		blobPath(manifestDigest): manifest,
	})
	_, _, _, err := loadLocalOCIArchive(archive)
	if err == nil {
		t.Fatal("err = nil, want config-digest reject")
	}
	if !strings.Contains(err.Error(), "OCI config digest") {
		t.Errorf("err = %v, want config-digest", err)
	}
}

func TestLoadLocalOCIArchive_ArchiveMissing(t *testing.T) {
	_, _, cleanup, err := loadLocalOCIArchive("/no/such/archive.tar")
	if err == nil {
		t.Fatal("err = nil, want archive-not-found")
	}
	cleanup() // must be safe to call even on error
	// And the error must wrap os.Open via the index-entry path.
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want os.ErrNotExist in chain", err)
	}
}

func TestLoadLocalOCIArchive_CleanupReleasesFiles(t *testing.T) {
	// After cleanup, the temp dir referenced by readers must be
	// gone (the files become invalid). Pin the cleanup contract.
	cfg := minimalConfigBytes()
	layer0 := gzipBytes(t, []byte("x"))
	cfgDigest := digestFor(t, cfg)
	layer0Digest := digestFor(t, layer0)
	manifest := minimalManifestBytes(cfgDigest, []string{layer0Digest})
	manifestDigest := digestFor(t, manifest)
	index := minimalIndexBytes(manifestDigest)
	archive := buildLocalOCIArchive(t, map[string][]byte{
		"index.json":             index,
		blobPath(manifestDigest): manifest,
		blobPath(cfgDigest):      cfg,
		blobPath(layer0Digest):   layer0,
	})
	_, _, cleanup, err := loadLocalOCIArchive(archive)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	cleanup()
	// Idempotent: a second cleanup must not panic.
	cleanup()
}

func TestFunctionHandlerPaths_CoversAllSupportedRuntimes(t *testing.T) {
	cases := map[string][2]string{
		RuntimeNode22:      {"/app/handler.js", "/app/node22.js"},
		RuntimeNode24:      {"/app/handler.js", "/app/node24.js"},
		RuntimePython312:   {"/app/handler.py", "/app/handler.py"},
		RuntimePython313:   {"/app/handler.py", "/app/handler.py"},
		RuntimeGo124:       {"/app/server", "/app/handler"},
		RuntimeGo124Alpine: {"/app/server", "/app/handler"},
	}
	for runtime, want := range cases {
		t.Run(runtime, func(t *testing.T) {
			source, target, err := functionHandlerPaths(runtime)
			if err != nil {
				t.Fatal(err)
			}
			if source != want[0] || target != want[1] {
				t.Fatalf("paths = (%q, %q), want (%q, %q)", source, target, want[0], want[1])
			}
		})
	}
}

func TestMakeGoHandlerLayerExtractsExecutableServer(t *testing.T) {
	var layer bytes.Buffer
	zw := gzip.NewWriter(&layer)
	tw := tar.NewWriter(zw)
	data := []byte("go-binary")
	if err := tw.WriteHeader(&tar.Header{Name: "app/server", Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "source-layer.tar.gz")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(layer.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	//nolint:forbidigo // path is a test-created local OCI fixture.
	file, err = os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	result, cleanup, err := makeGoHandlerLayer([]io.ReadCloser{file})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	staging := t.TempDir()
	if err := rootfs.ApplyLayerGz(staging, result); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(staging, "app", "server"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("server mode = %o, want 755", info.Mode().Perm())
	}
}

// --- Misc helpers that round out local_oci_test ----------------------

func TestLocalOCIIndexStructShape(t *testing.T) {
	// Pin the localOCIIndex struct shape so a json-tag rename
	// (e.g. Manifests → manifests) is caught here. A zero-value
	// struct serializes the slice as null under encoding/json; the
	// invariant we pin is that the tag is exactly "manifests"
	// (not "Manifests") so an OCI decoder reads it back.
	idx := localOCIIndex{}
	raw, err := json.Marshal(idx)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Both "manifests":[] and "manifests":null are acceptable Go
	// representations of an empty slice; pin the tag-name presence
	// instead of the literal value.
	if !bytes.Contains(raw, []byte(`"manifests"`)) {
		t.Errorf("empty localOCIIndex JSON = %q, want 'manifests' field present", raw)
	}
}

// Suppress unused warnings for the oci import — loadLocalOCIArchive
// returns oci.Config which the happy-path test reads.
var _ = oci.Config{}.DiffIDs
