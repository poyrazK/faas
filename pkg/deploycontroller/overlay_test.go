package deploycontroller

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/releasebundle"
)

const overlayID = "0123456789abcdef0123456789abcdef01234567"

var releaseTime = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

// overlayHost is a releases root with an active base release that runs two
// daemons, plus one staged replacement binary.
func overlayHost(t *testing.T) (root, current, staged string) {
	t.Helper()
	root = t.TempDir()
	base := filepath.Join(root, "base")
	for name, body := range map[string]string{"bin/apid": "apid-base", "bin/schedd": "schedd-base"} {
		path := filepath.Join(base, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := releasebundle.Build(base, "base", "commit-base", "linux/amd64", releaseTime)
	if err != nil {
		t.Fatal(err)
	}
	if err := releasebundle.Write(base, manifest); err != nil {
		t.Fatal(err)
	}
	// The host-owned KGV sidecar must not be carried into the overlay.
	if err := os.WriteFile(filepath.Join(base, "sbom-baseline.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	current = filepath.Join(root, "current")
	if err := os.Symlink(base, current); err != nil {
		t.Fatal(err)
	}
	staged = filepath.Join(t.TempDir(), "apid")
	if err := os.WriteFile(staged, []byte("apid-unit"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, current, staged
}

func overlayRequest(staged string) OverlayRequest {
	return OverlayRequest{ReleaseID: overlayID, ExpectedCurrent: "base", CommitSHA: "commit-unit",
		Files: map[string]string{"bin/apid": staged}}
}

func TestAssembleOverlayLinksTheActiveReleaseAndSwapsUnits(t *testing.T) {
	root, current, staged := overlayHost(t)
	controller := newController(t, root, current, &fakeRuntime{})

	got, err := controller.AssembleOverlay(overlayRequest(staged))
	if err != nil {
		t.Fatalf("AssembleOverlay: %v", err)
	}
	if got != filepath.Join(root, overlayID) {
		t.Fatalf("root = %q", got)
	}
	manifest, err := releasebundle.Read(got)
	if err != nil {
		t.Fatal(err)
	}
	if err := releasebundle.Verify(got, manifest); err != nil {
		t.Fatalf("overlay does not verify: %v", err)
	}
	if body, _ := os.ReadFile(filepath.Join(got, "bin", "apid")); string(body) != "apid-unit" {
		t.Fatalf("overlay apid = %q", body)
	}
	if body, _ := os.ReadFile(filepath.Join(root, "base", "bin", "apid")); string(body) != "apid-base" {
		t.Fatalf("base apid changed to %q: the overlay wrote through a hard link", body)
	}
	// Unchanged daemons share the active release's inode.
	if !sameInode(t, filepath.Join(got, "bin", "schedd"), filepath.Join(root, "base", "bin", "schedd")) {
		t.Fatal("schedd was copied, not linked")
	}
	if _, err := os.Stat(filepath.Join(got, "sbom-baseline.json")); !os.IsNotExist(err) {
		t.Fatalf("overlay carried the base's KGV sidecar: %v", err)
	}
	// Assembling never activates.
	if target, _ := os.Readlink(current); target != filepath.Join(root, "base") {
		t.Fatalf("current moved to %q", target)
	}
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".overlay-") {
			t.Fatalf("staging directory %s left behind", entry.Name())
		}
	}
}

func TestAssembleOverlayRerunReusesTheInstalledRelease(t *testing.T) {
	root, current, staged := overlayHost(t)
	controller := newController(t, root, current, &fakeRuntime{})
	first, err := controller.AssembleOverlay(overlayRequest(staged))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(filepath.Join(first, releasebundle.ManifestName))
	second, err := controller.AssembleOverlay(overlayRequest(staged))
	if err != nil || second != first {
		t.Fatalf("rerun = %q, %v", second, err)
	}
	after, _ := os.Stat(filepath.Join(first, releasebundle.ManifestName))
	if !os.SameFile(before, after) {
		t.Fatal("rerun rebuilt a verified overlay")
	}

	// A half-written overlay (here: a tampered binary) is rebuilt.
	if err := os.WriteFile(filepath.Join(first, "bin", "apid"), []byte("torn"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.AssembleOverlay(overlayRequest(staged)); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if body, _ := os.ReadFile(filepath.Join(first, "bin", "apid")); string(body) != "apid-unit" {
		t.Fatalf("rebuilt apid = %q", body)
	}
}

func TestAssembleOverlayRefuses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(req *OverlayRequest)
		want   string
	}{
		{name: "another release active", mutate: func(r *OverlayRequest) { r.ExpectedCurrent = "other" }, want: `built over "other", but the active release is "base"`},
		{name: "non-hex id", mutate: func(r *OverlayRequest) { r.ReleaseID = "../escape" }, want: "invalid overlay release id"},
		{name: "no files", mutate: func(r *OverlayRequest) { r.Files = nil }, want: "incomplete"},
		{name: "missing staged file", mutate: func(r *OverlayRequest) { r.Files = map[string]string{"bin/apid": "/nonexistent"} }, want: "stage bin/apid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, current, staged := overlayHost(t)
			req := overlayRequest(staged)
			tc.mutate(&req)
			_, err := newController(t, root, current, &fakeRuntime{}).AssembleOverlay(req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("AssembleOverlay error = %v, want %q", err, tc.want)
			}
			if _, err := os.Stat(filepath.Join(root, overlayID)); !os.IsNotExist(err) {
				t.Fatalf("refused overlay left a release directory: %v", err)
			}
		})
	}
}

func TestOverlayDeployRestartsOverTheBaseAndRollsBackToIt(t *testing.T) {
	root, current, staged := overlayHost(t)
	runtime := &fakeRuntime{healthyErr: os.ErrDeadlineExceeded}
	controller := newController(t, root, current, runtime)
	if _, err := controller.AssembleOverlay(overlayRequest(staged)); err != nil {
		t.Fatal(err)
	}
	err := controller.DeployOver(context.Background(), overlayID, "base")
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("DeployOver error = %v, want a rollback", err)
	}
	if target, _ := os.Readlink(current); target != filepath.Join(root, "base") {
		t.Fatalf("current = %q after rollback, want the base", target)
	}
}

func sameInode(t *testing.T, a, b string) bool {
	t.Helper()
	ai, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	bi, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	as, aok := ai.Sys().(*syscall.Stat_t)
	bs, bok := bi.Sys().(*syscall.Stat_t)
	return aok && bok && as.Ino == bs.Ino && as.Dev == bs.Dev
}

// computeHost is a releases root whose active release came from
// `gregalectl release install`: binaries and a canonical
// release-manifest.json, no deployment manifest, no systemd units.
func computeHost(t *testing.T) (root, current, staged string) {
	t.Helper()
	root = t.TempDir()
	base := filepath.Join(root, "00000000000000000000000000000000000000b0")
	for name, body := range map[string]string{
		"bin/gatewayd-internal":  "internal-base",
		"bin/vmmd":               "vmmd-base",
		"release-manifest.json":  `{"format_version":1,"git_sha":"00000000000000000000000000000000000000b0"}`,
		"sbom-baseline.json":     `{}`,
		"bin/runners/node22/run": "runner",
	} {
		path := filepath.Join(base, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	current = filepath.Join(root, "current")
	if err := os.Symlink(base, current); err != nil {
		t.Fatal(err)
	}
	staged = filepath.Join(t.TempDir(), "gatewayd-internal")
	if err := os.WriteFile(staged, []byte("internal-unit"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, current, staged
}

func TestAssembleOverlayOnAComputeHostRelease(t *testing.T) {
	root, current, staged := computeHost(t)
	base := filepath.Join(root, "00000000000000000000000000000000000000b0")
	runtime := &fakeRuntime{healthyErr: os.ErrDeadlineExceeded}
	controller := newController(t, root, current, runtime)
	got, err := controller.AssembleOverlay(OverlayRequest{
		ReleaseID: overlayID, ExpectedCurrent: filepath.Base(base), CommitSHA: "commit-unit",
		Files: map[string]string{"bin/gatewayd-internal": staged},
	})
	if err != nil {
		t.Fatalf("AssembleOverlay: %v", err)
	}
	// The base gained a deployment manifest of exactly its release files,
	// so it verifies as a rollback target; the SBOM sidecar stays outside.
	baseManifest, err := releasebundle.Read(base)
	if err != nil {
		t.Fatalf("base has no deployment manifest: %v", err)
	}
	if baseManifest.CommitSHA != filepath.Base(base) {
		t.Fatalf("base manifest commit = %q", baseManifest.CommitSHA)
	}
	for _, file := range baseManifest.Files {
		if file.Path == "sbom-baseline.json" {
			t.Fatal("base manifest lists the host's SBOM baseline sidecar")
		}
	}
	if err := verifyInstalledRelease(base, baseManifest); err != nil {
		t.Fatalf("base does not verify: %v", err)
	}
	manifest, err := releasebundle.Read(got)
	if err != nil {
		t.Fatal(err)
	}
	if err := releasebundle.Verify(got, manifest); err != nil {
		t.Fatalf("overlay does not verify: %v", err)
	}
	if !sameInode(t, filepath.Join(got, "bin", "vmmd"), filepath.Join(base, "bin", "vmmd")) {
		t.Fatal("vmmd was copied, not linked")
	}
	// A failed health check rolls the node back to its base release.
	err = controller.DeployOver(context.Background(), overlayID, filepath.Base(base))
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("DeployOver error = %v, want a rollback", err)
	}
	if target, _ := os.Readlink(current); target != base {
		t.Fatalf("current = %q after rollback, want the base", target)
	}
}

func TestBaseManifestRefusesAReleaseWithoutACommit(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := baseManifest(root); err == nil || !strings.Contains(err.Error(), "neither a deployment nor a canonical manifest") {
		t.Fatalf("baseManifest error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "release-manifest.json"), []byte(`{"git_sha":"nope"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := baseManifest(root); err == nil || !strings.Contains(err.Error(), "names no commit") {
		t.Fatalf("baseManifest error = %v", err)
	}
}
