//go:build metal && linux

package builderd

// adr: 20261030122204

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// buildkitSeedLabel is the ext4 label of a seed drive and its copies.
const buildkitSeedLabel = "faas-buildkit"

// buildKitSeedKey is the key of the seed this node's builds can use now.
func (d *VMMDriver) buildKitSeedKey(snapshotter string) (string, error) {
	env, err := currentBuildEnvironment(d)
	if err != nil {
		return "", err
	}
	return buildkitSeedKey(env, snapshotter)
}

// prepareBuildKitSeedDrive returns the build's drive2, or "" for none, and
// marks the manifest to match. A seed run gets an empty drive. An eligible
// build gets a copy of the ready seed; without one it runs as today, so only a
// seed run fails here.
func (d *VMMDriver) prepareBuildKitSeedDrive(ctx context.Context, req VMRequest, m *api.BuildManifest, instance string) (string, error) {
	dst := filepath.Join(d.driveDir, buildkitSeedCopyName(instance))
	if req.BuildKitSeedOnly {
		if d.seeds == nil {
			return "", errors.New("builderd: BuildKit seed run while the seed is off")
		}
		if err := createEmptyBuildKitSeedDrive(ctx, dst); err != nil {
			_ = os.Remove(dst)
			return "", fmt.Errorf("builderd: create BuildKit seed drive: %w", err)
		}
		m.BuildKitSeedOnly = true
		return dst, nil
	}
	if d.seeds == nil || !buildKitSeedEligible(req) {
		return "", nil
	}
	key, err := d.buildKitSeedKey(req.BuildKitSnapshotter)
	if err == nil {
		err = d.seeds.copyTo(key, dst)
	}
	if err != nil {
		if !errors.Is(err, errNoReadySeed) {
			slog.Warn("builderd: build runs without the BuildKit seed", "build_id", req.BuildID, "err", err)
		}
		return "", nil
	}
	m.BuildKitSeed = true
	return dst, nil
}

// createEmptyBuildKitSeedDrive makes a seed run's drive2: a sparse ext4 as
// large as drive1, since BuildKit keeps all its state on it.
func createEmptyBuildKitSeedDrive(ctx context.Context, dst string) error {
	//nolint:forbidigo // dst is a builderd-named drive in its own drive directory.
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := f.Truncate(BuildDriveSizeBytes); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if out, err := exec.CommandContext(ctx, buildMkfs, "-L", buildkitSeedLabel, "-F", "-q", dst).CombinedOutput(); err != nil {
		return fmt.Errorf("mkfs: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// CreateBuildKitSeed makes the node's seed when it has none for the current
// key and the build disk has room for it. It boots a builder VM that builds
// the platform-owned seed app on an empty drive2 and keeps that drive once
// the run reports BuildKit ran on it. The caller holds the builder slot.
// It reports whether it made a seed.
func (d *VMMDriver) CreateBuildKitSeed(ctx context.Context, buildID, snapshotter string) (bool, error) {
	if d == nil || d.seeds == nil {
		return false, nil
	}
	if buildID == "" {
		return false, errors.New("builderd: empty BuildKit seed run ID")
	}
	key, err := d.buildKitSeedKey(snapshotter)
	if err != nil {
		return false, fmt.Errorf("builderd: BuildKit seed key: %w", err)
	}
	if _, ok := d.seeds.ready(key); ok {
		return false, nil
	}
	if err := os.MkdirAll(d.driveDir, 0o755); err != nil {
		return false, fmt.Errorf("builderd: mkdir drive dir: %w", err)
	}
	available, err := driveFilesystemAvailable(d.driveDir)
	if err != nil {
		return false, err
	}
	if available < uint64(BuildDriveMinFreeBytes)+uint64(api.BuildKitSeedReserveBytes) {
		return false, nil
	}

	source, err := buildkitSeedSourceTar()
	if err != nil {
		return false, fmt.Errorf("builderd: BuildKit seed source: %w", err)
	}
	src, err := os.CreateTemp("", "faas-buildkit-seed-*.tar.gz")
	if err != nil {
		return false, fmt.Errorf("builderd: BuildKit seed source: %w", err)
	}
	defer func() { _ = os.Remove(src.Name()) }()
	_, writeErr := src.Write(source)
	if err := errors.Join(writeErr, src.Close()); err != nil {
		return false, fmt.Errorf("builderd: write BuildKit seed source: %w", err)
	}
	runtimeBase, err := resolveBuildRuntimeBaseRef("", FrameworkNode, os.Getenv)
	if err != nil {
		return false, fmt.Errorf("builderd: BuildKit seed runtime base: %w", err)
	}
	sum := sha256.Sum256(source)
	h, err := d.Spawn(ctx, VMRequest{
		BuildID:             buildID,
		SourcePath:          src.Name(),
		SourceSHA256:        hex.EncodeToString(sum[:]),
		Framework:           FrameworkNode,
		RuntimeBaseRef:      runtimeBase,
		BuildKitSnapshotter: snapshotter,
		RAMMB:               api.BuildVMRAMMB,
		TimeoutSec:          api.BuildTimeoutSeconds,
		// Builder VMs are never metered; vmmd only requires a valid plan.
		Plan:             string(api.PlanFree),
		BuildKitSeedOnly: true,
	})
	if err != nil {
		return false, fmt.Errorf("builderd: spawn BuildKit seed run: %w", err)
	}
	// install moves the drive, so this removes it only when the run failed.
	defer func() {
		_ = os.RemoveAll(h.ExportDir)
		_ = os.Remove(h.HostSeedDrive)
	}()
	out, err := d.WaitForCompletion(ctx, h)
	if err != nil {
		return false, fmt.Errorf("builderd: BuildKit seed run: %w", err)
	}
	if out.ExitCode != 0 || !out.BuildKitSeeded {
		return false, fmt.Errorf("builderd: BuildKit seed run exited %d (%s, seeded %v): %s",
			out.ExitCode, out.FailureClass, out.BuildKitSeeded, tailString(out.LogTail, 1024))
	}
	if err := d.seeds.install(key, h.HostSeedDrive); err != nil {
		return false, fmt.Errorf("builderd: %w", err)
	}
	return true, nil
}

// tailString returns at most the last n bytes of s.
func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// The seed loop finds the capability by type assertion; keep it compiled.
var _ BuildKitSeeder = (*VMMDriver)(nil)
