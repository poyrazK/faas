// Whitebox tests for the SBOM populator (issue #299 / ADR-038
// Phase 3). The helper at writeBuildSBOM is the imaged-side seam
// builderd relies on to populate build_provenance.sbom_storage_key;
// the test pins the storage-key shape (BuildSBOMKey) and the
// fail-closed posture so a future refactor that silently changed
// either would fail loudly.
//
// Pattern: narrowly-scoped per the whitebox-test-file convention
// (memory: whitebox-test-file-pattern). One test for the helper,
// one for BuildSBOMKey, and one for the syftRun injection seam.
package imaged

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// TestBuildSBOMKey pins the canonical storage key shape so a future
// refactor that re-arranged the prefix or filename suffix breaks
// the test instead of the apid GET /v1/builds/{id}/sbom handler.
func TestBuildSBOMKey(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	got := BuildSBOMKey(id)
	want := "sboms/" + id + ".cdx.json"
	if got != want {
		t.Errorf("BuildSBOMKey(%q) = %q, want %q", id, got, want)
	}
}

// TestWriteBuildSBOM_HappyPath: a Handler constructed with a custom
// syftRun stub returning canned CycloneDX JSON writes that JSON
// verbatim to the storage backend at BuildSBOMKey(buildID). Confirms
// the storage round-trip is byte-exact (no JSON re-marshalling that
// could lose precise-formatting). The test exercises the new handler
// field (syftRun) and the writeBuildSBOM helper in one go.
func TestWriteBuildSBOM_HappyPath(t *testing.T) {
	const buildID = "0123456789abcdef0123456789abcd11"
	outDir := t.TempDir()
	rootDir := t.TempDir()

	const sbom = `{"bomFormat":"CycloneDX","specVersion":"1.5","version":1,"components":[{"name":"node22-runtime","version":"22.4.0"}]}`

	h := &Handler{
		store:    state.NewMemStore(),
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		appsRoot: rootDir,
		syftRun: func(_ context.Context, _ string) ([]byte, error) {
			return []byte(sbom), nil
		},
	}

	// writeBuildSBOM resolves the storage backend via storageFor,
	// which builds a per-appsRoot LocalStorageBackend. After Put,
	// the file should exist at rootDir/sboms/<buildID>.cdx.json with
	// the byte-exact content.
	key, err := h.writeBuildSBOM(context.Background(), "", buildID, outDir)
	if err != nil {
		t.Fatalf("writeBuildSBOM: %v", err)
	}
	if key != "sboms/"+buildID+".cdx.json" {
		t.Errorf("returned key = %q, want sboms/%s.cdx.json", key, buildID)
	}

	onDisk := filepath.Join(rootDir, "sboms", buildID+".cdx.json")
	got, err := os.ReadFile(onDisk)
	if err != nil {
		t.Fatalf("read %q: %v", onDisk, err)
	}
	if string(got) != sbom {
		t.Errorf("on-disk SBOM mismatch:\n got=%s\nwant=%s", got, sbom)
	}
}

// TestWriteBuildSBOM_FailClosed: a syftRun that returns an error
// must NOT write anything to storage AND must NOT panic. The build
// should still be considered successful — observability-only. Pins
// the "best-effort" posture documented in pkg/imaged/sbom.go.
func TestWriteBuildSBOM_FailClosed(t *testing.T) {
	const buildID = "0123456789abcdef0123456789abcd22"
	outDir := t.TempDir()
	rootDir := t.TempDir()

	h := &Handler{
		store:    state.NewMemStore(),
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		appsRoot: rootDir,
		syftRun: func(_ context.Context, _ string) ([]byte, error) {
			return nil, errFakeSyft("syft not on PATH")
		},
	}

	key, err := h.writeBuildSBOM(context.Background(), "", buildID, outDir)
	if err != nil {
		t.Fatalf("writeBuildSBOM should swallow syft errors; got err = %v", err)
	}
	if key != "" {
		t.Errorf("expected empty key on syft error, got %q", key)
	}

	// The expected on-disk file should NOT exist.
	want := filepath.Join(rootDir, "sboms", buildID+".cdx.json")
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Errorf("expected file %q to NOT exist after syft error; stat err = %v", want, err)
	}
}

// TestWriteBuildSBOM_RejectsInvalidJSON: syft can in some
// configurations (bad layer extract, partial pull) produce
// non-JSON output. The parser rejects it BEFORE persisting so a
// broken SBOM doesn't get handed to the customer as "valid".
func TestWriteBuildSBOM_RejectsInvalidJSON(t *testing.T) {
	const buildID = "0123456789abcdef0123456789abcd33"
	outDir := t.TempDir()
	rootDir := t.TempDir()

	h := &Handler{
		store:    state.NewMemStore(),
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		appsRoot: rootDir,
		syftRun: func(_ context.Context, _ string) ([]byte, error) {
			return []byte("not-json {{{ broken"), nil
		},
	}

	key, err := h.writeBuildSBOM(context.Background(), "", buildID, outDir)
	if err != nil {
		t.Fatalf("writeBuildSBOM should swallow invalid JSON; got err = %v", err)
	}
	if key != "" {
		t.Errorf("expected empty key on invalid JSON; got %q", key)
	}
}

// TestSyftRun_DefaultSubprocessShellsOut confirms the default
// runner shells out to syft on PATH. Skipped when syft is not
// installed on the test machine — the helper is best-effort and
// the unit-test seam (WithSyftRun) is what production should NOT
// rely on. Exists so a reviewer can flip FAAS_RUN_SYFT_TESTS=1 to
// exercise the subprocess path on a workstation that has syft.
func TestSyftRun_DefaultSubprocessShellsOut(t *testing.T) {
	if os.Getenv("FAAS_RUN_SYFT_TESTS") == "" {
		t.Skip("FAAS_RUN_SYFT_TESTS not set; default syft subprocess requires syft on PATH")
	}
	ctx := context.Background()
	out, err := defaultSyftRun(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("defaultSyftRun: %v", err)
	}
	if !strings.HasPrefix(string(out), "{") {
		t.Errorf("defaultSyftRun output does not start with '{'; got first byte = %q", string(out)[:1])
	}
}

// errFakeSyft is a minimal error type used by the fail-closed test.
// Lives here (not in sbom.go) because the test is the only caller.
type errFakeSyft string

func (e errFakeSyft) Error() string { return string(e) }

// (compile-time interface assertion that storage is referenced —
// keeps the import honest when the test file is the only place
// that pulls it.)
var _ storage.StorageBackend = (*storage.LocalStorageBackend)(nil) //nolint:unused
