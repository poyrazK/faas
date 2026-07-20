//go:build linux

package builderd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWriteBuildManifest_RoundTrip(t *testing.T) {
	mp := t.TempDir()
	m := api.BuildManifest{
		SchemaVersion: 1,
		BuildID:       "b-test",
		TenantID:      "t-1",
		DeploymentID:  "d-1",
		SourceTarPath: "/build/src.tar",
		Framework:     api.FrameworkRailpackNode,
		TimeoutSec:    600,
		LogTailBytes:  4096,
	}
	if err := writeBuildManifest(mp, m); err != nil {
		t.Fatalf("writeBuildManifest: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(mp, "etc", "faas", "build.json"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var got api.BuildManifest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.BuildID != m.BuildID || got.Framework != m.Framework || got.TimeoutSec != m.TimeoutSec {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, m)
	}
}

// TestCreateBuildDrive1_RejectsEmptyInputs covers the input-validation path,
// which doesn't touch the loopback mount and so runs unprivileged.
func TestCreateBuildDrive1_RejectsEmptyInputs(t *testing.T) {
	ctx := context.Background()
	// Empty dest → error.
	if err := CreateBuildDrive1(ctx, "", api.BuildManifest{BuildID: "x"}, "/some/source.tar"); err == nil {
		t.Error("expected error for empty dest")
	}
	// Empty BuildID → error.
	if err := CreateBuildDrive1(ctx, "/tmp/x", api.BuildManifest{}, "/some/source.tar"); err == nil {
		t.Error("expected error for empty BuildID")
	}
	// Empty sourcePath → error (issue #54: image deploys must never reach
	// builderd; reaching it without a tarball would silently no-op the build).
	if err := CreateBuildDrive1(ctx, "/tmp/x", api.BuildManifest{BuildID: "x"}, ""); err == nil {
		t.Error("expected error for empty sourcePath")
	}
	// Nonexistent sourcePath → error.
	if err := CreateBuildDrive1(ctx, "/tmp/x", api.BuildManifest{BuildID: "x"}, "/no/such/file.tar.gz"); err == nil {
		t.Error("expected error for missing sourcePath")
	}
}

// TestCopySourceTarball_RoundTrip exercises copySourceTarball against a
// plain tmpdir (no loopback mount) and verifies the staged file matches the
// host source byte-for-byte. This is the issue #54 acceptance check that
// runs without /dev/kvm.
func TestCopySourceTarball_RoundTrip(t *testing.T) {
	mp := t.TempDir()
	src := filepath.Join(t.TempDir(), "source.tar.gz")
	payload := []byte("hello-from-issue-54\nthis is a fake tarball\n")
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := copySourceTarball(mp, src); err != nil {
		t.Fatalf("copySourceTarball: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(mp, "build", "src.tar"))
	if err != nil {
		t.Fatalf("read staged: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("staged contents differ: got %q, want %q", got, payload)
	}
}

// TestCreateBuildDrive1_StagedTarballMatches is the end-to-end metal-side
// check (issue #54 acceptance: ls /build/src/ is non-empty inside the VM).
// It runs the real mkfs + loopback mount, copies the host tarball in, and
// re-mounts the result to assert sha256(source) == sha256(staged). Skipped
// on systems that lack /dev/loop-control or where losetup requires privileges
// the test runner doesn't have — the unit tests above cover the unmounted
// code paths in that case.
func TestCreateBuildDrive1_StagedTarballMatches(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("needs root for losetup+mount; covered by unit tests + Lima metal loop")
	}
	if _, err := os.Stat("/dev/loop-control"); err != nil {
		t.Skip("/dev/loop-control not available; covered by Lima metal loop")
	}

	ctx := context.Background()
	hostSrc := filepath.Join(t.TempDir(), "source.tar.gz")
	// 64 KiB of patterned bytes — bigger than mkfs's inode table cares about,
	// small enough to keep the test fast.
	payload := make([]byte, 64*1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	if err := os.WriteFile(hostSrc, payload, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	drive1 := filepath.Join(t.TempDir(), "drive1.ext4")
	m := api.BuildManifest{
		SchemaVersion: 1,
		BuildID:       "b-acceptance",
		TenantID:      "t-1",
		DeploymentID:  "d-1",
		SourceTarPath: "/build/src.tar",
		Framework:     api.FrameworkRailpackNode,
		TimeoutSec:    600,
		LogTailBytes:  4096,
	}
	if err := CreateBuildDrive1(ctx, drive1, m, hostSrc); err != nil {
		t.Fatalf("CreateBuildDrive1: %v", err)
	}

	// Re-mount the produced drive1 and verify /build/src.tar matches the
	// host source. This is what guest-init sees on first boot.
	mp := t.TempDir()
	if out, err := runMount(ctx, "-o", "loop,rw", drive1, mp); err != nil {
		t.Fatalf("remount: %v (%s)", err, out)
	}
	defer func() { _, _ = runUmount(mp) }()

	gotSum, err := fileSHA256(filepath.Join(mp, "build", "src.tar"))
	if err != nil {
		t.Fatalf("re-stat staged: %v", err)
	}
	wantSum, err := fileSHA256(hostSrc)
	if err != nil {
		t.Fatalf("re-stat source: %v", err)
	}
	if gotSum != wantSum {
		t.Fatalf("sha256 mismatch after staging: got %s, want %s", gotSum, wantSum)
	}
}
