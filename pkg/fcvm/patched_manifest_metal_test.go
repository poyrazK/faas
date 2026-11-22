//go:build metal

package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalPatchedManifestLayerBoots covers ADR-20261112170911 amendment 2:
// imaged republishes a recorded app layer with only /etc/faas/app.json
// replaced (debugfs, no mount) when a redeploy changes runtime settings. The
// patched ext4 must still boot: guest-init mounts it as drive1, reads the new
// manifest and the app reaches readiness.
func TestMetalPatchedManifestLayerBoots(t *testing.T) {
	kernel, base, layer := metalImages(t)
	for _, tool := range []string{"debugfs", "e2fsck"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	t.Setenv("FAAS_BASE_TMP_ROOT", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// The fixture's embedded manifest, rewritten in the form a build writes,
	// is the "old" manifest of the record.
	old, path := fixtureLayerManifest(t, layer)
	layer = normalizedFixtureLayer(t, layer, path, old)
	var oldBytes bytes.Buffer
	if err := api.WriteManifest(&oldBytes, old); err != nil {
		t.Fatal(err)
	}
	updated := old
	updated.Env = map[string]string{}
	for k, v := range old.Env {
		updated.Env[k] = v
	}
	updated.Env["FAAS_TEST_PATCHED_MANIFEST"] = "1"

	f, err := os.Open(layer) //nolint:forbidigo // the metal fixture layer path from FAAS_TEST_LAYER_ROOTFS.
	if err != nil {
		t.Fatal(err)
	}
	id, err := rootfs.ReadArtifactIdentity(ctx, f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const key = "apps/patched/dep-2.ext4"
	start := time.Now()
	patched, _, err := rootfs.NewBuilder(wire.ExecRunner{}).RepublishWithManifest(ctx, be, key, rootfs.ManifestPatch{
		Source: rootfs.PublishedLayer{Path: layer, Digest: id.Digest, Bytes: id.Bytes},
		Old:    old, New: updated,
	})
	if err != nil {
		t.Fatalf("RepublishWithManifest on the fixture layer (manifest at %s): %v", path, err)
	}
	t.Logf("patched %d-byte layer in %s (digest %s -> %s)", id.Bytes, time.Since(start).Round(time.Millisecond), id.Digest, patched.Digest)
	resolver, ok := any(be).(storage.LocalPathResolver)
	if !ok {
		t.Fatal("local storage has no local paths")
	}
	patchedPath, _, err := resolver.LocalPath(key)
	if err != nil {
		t.Fatal(err)
	}

	m := newMetalManager(t, kernel)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	const instance = "patched-manifest"
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: instance, Plan: "pro", BaseKey: base, LayerKey: patchedPath, VcpuCount: 2, MemSizeMiB: 128,
	}); err != nil {
		t.Fatalf("cold boot from the patched layer: %v", err)
	}
	if err := m.Destroy(ctx, instance); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	leakcheck.AssertZero(t)
}

// fixtureLayerManifest reads the app manifest embedded in a layer ext4 and
// skips when the fixture has none in the form a build writes.
func fixtureLayerManifest(t *testing.T, layer string) (api.AppManifest, string) {
	t.Helper()
	for _, p := range []string{"/upper" + api.AppManifestPath, api.AppManifestPath} {
		out := filepath.Join(t.TempDir(), "app.json")
		if err := exec.Command("debugfs", "-R", "dump "+p+" "+out, layer).Run(); err != nil {
			continue
		}
		raw, err := os.ReadFile(out)
		if err != nil || len(raw) == 0 {
			continue
		}
		var m api.AppManifest
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Skipf("fixture %s at %s is not an app manifest: %v", layer, p, err)
		}
		return m, p
	}
	t.Skipf("fixture %s has no embedded app manifest", layer)
	return api.AppManifest{}, ""
}

// normalizedFixtureLayer copies layer and rewrites its manifest at p as
// api.WriteManifest writes m, which is what a build embeds and what a reuse
// record describes. Older fixtures were written by other tooling.
func normalizedFixtureLayer(t *testing.T, layer, p string, m api.AppManifest) string {
	t.Helper()
	dir := t.TempDir()
	copyPath := filepath.Join(dir, "layer.ext4")
	src, err := os.ReadFile(layer) //nolint:forbidigo // the metal fixture layer path from FAAS_TEST_LAYER_ROOTFS.
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, src, 0o644); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	if err := api.WriteManifest(&body, m); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "app.json")
	if err := os.WriteFile(manifest, body.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "normalize.debugfs")
	if err := os.WriteFile(script, []byte("cd "+filepath.Dir(p)+"\nrm app.json\nwrite "+manifest+" app.json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("debugfs", "-w", "-f", script, copyPath).CombinedOutput(); err != nil {
		t.Fatalf("normalize fixture manifest: %v: %s", err, out)
	}
	return copyPath
}
