//go:build metal && linux

package builderd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestReadBuildDonePrefersGuestExitCode(t *testing.T) {
	dir := t.TempDir()
	want := api.BuildDone{
		SchemaVersion: 1,
		BuildID:       "build-1",
		ExitCode:      0,
		OCIImagePath:  "/build/out/image.tar",
		LogTail:       "build complete",
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build-done.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := readBuildDone(dir)
	if !ok {
		t.Fatal("readBuildDone returned !ok")
	}
	if got.ExitCode != 0 || got.OCIImagePath != want.OCIImagePath || got.LogTail != want.LogTail {
		t.Fatalf("readBuildDone = %#v, want %#v", got, want)
	}
}

func TestWarmBuilderSnapshotKeysAreStableAndScopedToBuild(t *testing.T) {
	mem, vmstate := warmBuilderSnapshotKeys("build-1")
	if mem == "" || vmstate == "" || mem == vmstate {
		t.Fatalf("snapshot keys = %q/%q, want distinct non-empty keys", mem, vmstate)
	}
	if gotMem, gotVMState := warmBuilderSnapshotKeys("build-1"); gotMem != mem || gotVMState != vmstate {
		t.Fatalf("snapshot keys changed between calls: %q/%q then %q/%q", mem, vmstate, gotMem, gotVMState)
	}
	if otherMem, _ := warmBuilderSnapshotKeys("build-2"); otherMem == mem {
		t.Fatalf("different builds share memory snapshot key %q", mem)
	}
}

func TestBuildManifestForRequestCarriesWarmInputs(t *testing.T) {
	manifest, err := buildManifestForRequest(VMRequest{
		BuildID:            "build-1",
		TenantID:           "acct-1",
		DeploymentID:       "dep-1",
		SourceRoot:         "services/api",
		Framework:          FrameworkNode,
		Runtime:            "node22",
		RuntimeBaseRef:     "base-ref",
		DependencyCacheKey: "cache-key",
		KeepWarm:           true,
	}, 900)
	if err != nil {
		t.Fatalf("buildManifestForRequest: %v", err)
	}
	if !manifest.KeepWarm || !manifest.DependencyCache || manifest.TimeoutSec != 900 {
		t.Fatalf("manifest warm/cache/timeout = %v/%v/%d", manifest.KeepWarm, manifest.DependencyCache, manifest.TimeoutSec)
	}
	if !strings.HasSuffix(manifest.Workdir, "/services/api") {
		t.Fatalf("manifest workdir = %q, want services/api suffix", manifest.Workdir)
	}
}
