//go:build linux

// Package builderd — drive1 preparation for ephemeral builder VMs.
//
// CreateBuildDrive1 materialises the per-VM ext4 that the builder VM boots
// with. It writes a 28 GiB image, formats it ext4 from an unprivileged
// staging directory, writes /etc/faas/build.json (the BuildManifest
// guest-init reads to know it's a build VM), and copies the customer source
// tarball in at /build/src.tar (issue #54). The same binary runs in app VMs
// with a different manifest (api.AppManifest); guest-init branches on which
// file exists at boot.
//
// vmmd is the only component that touches block devices (spec §11). The
// builderd service is intentionally unprivileged, so this path must not call
// mount(8) or require CAP_SYS_ADMIN. mke2fs's -d root-directory mode writes
// the staged tree directly into the ext4 image and preserves that privilege
// boundary.

package builderd

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

// BuildDriveSizeBytes is the drive1 image size for builder VMs (M6). 28 GiB
// gives rootless native BuildKit enough room for Railpack's builder/runtime
// images, dependency layers, and the native local exporter; the previous
// 24 GiB budget exhausted while copying those layers. The produced app layer
// (in /build/out) is typically
// < 500 MB; the rest is transient build scratch.
const BuildDriveSizeBytes = 28 << 30

// BuildDriveMinWorkingSetBytes is the conservative host working-set budget for
// a Railpack solve. The ext4 image itself is sparse; requiring the full 28 GiB
// image size would reject healthy hosts even though the image only materialises
// the blocks BuildKit actually writes.
const BuildDriveMinWorkingSetBytes = 16 << 30

// BuildDriveHostReserveBytes keeps room for the host-side export directory,
// logs, and filesystem metadata. Without this preflight, a nearly-full host
// lets the guest run until virtio reports ENOSPC, which is misclassified as an
// opaque infrastructure timeout.
const BuildDriveHostReserveBytes = 4 << 30

// BuildDriveMinFreeBytes is the minimum filesystem space required before a
// builder drive is materialised.
const BuildDriveMinFreeBytes = BuildDriveMinWorkingSetBytes + BuildDriveHostReserveBytes

// mkfs utility + label used for the build drive1 image.
const (
	buildMkfs  = "mkfs.ext4"
	buildLabel = "faas-build"
)

// CreateBuildDrive1 writes a 28 GiB ext4 image at dest containing the
// BuildManifest at /etc/faas/build.json and (when sourcePath is non-empty)
// the customer's source tarball copied to /build/src.tar. Idempotent on
// host filesystem blocks — overwrites dest. Returns immediately on permission
// errors so unit tests that lack loopback rights can skip the path via
// os.Geteuid.
//
// sourcePath is required for any builder VM that runs a real build
// (tarball/dockerfile deploys). image: deploys never reach builderd at all,
// so passing "" here is a programmer error and is rejected explicitly — an
// empty drive1 would silently boot guest-init into `tar -xaf /build/src.tar`
// against a missing file and produce a no-op build.
func CreateBuildDrive1(ctx context.Context, dest string, m api.BuildManifest, sourcePath string) error {
	_, err := createBuildDrive1(ctx, dest, m, sourcePath, "")
	return err
}

// createBuildDrive1 optionally seeds BuildKit's local cache into /build/cache.
// Cache errors degrade to a cold build: source integrity and drive creation are
// still authoritative, while the cache is explicitly disposable.
func createBuildDrive1(ctx context.Context, dest string, m api.BuildManifest, sourcePath, dependencyCache string) (bool, error) {
	if dest == "" {
		return false, fmt.Errorf("builderd: empty drive1 path")
	}
	if m.BuildID == "" {
		return false, fmt.Errorf("builderd: empty build_id")
	}
	if sourcePath == "" {
		return false, fmt.Errorf("builderd: empty source_path for build %s (image deploys must not reach builderd)", m.BuildID)
	}
	srcSum, err := fileSHA256(sourcePath)
	if err != nil {
		return false, fmt.Errorf("builderd: stat source %s: %w", sourcePath, err)
	}
	if err := checkBuildDriveCapacity(dest); err != nil {
		return false, err
	}

	// 1. Truncate the host file to BuildDriveSizeBytes.
	f, err := os.Create(dest)
	if err != nil {
		return false, fmt.Errorf("builderd: create drive1: %w", err)
	}
	if err := f.Truncate(BuildDriveSizeBytes); err != nil {
		_ = f.Close()
		return false, fmt.Errorf("builderd: truncate drive1: %w", err)
	}
	if err := f.Close(); err != nil {
		return false, fmt.Errorf("builderd: close drive1: %w", err)
	}

	// 2. Stage the guest-visible tree as plain files. This avoids a loopback
	// mount, which would require CAP_SYS_ADMIN and is unavailable to the
	// hardened faas-builderd systemd unit.
	mp, err := os.MkdirTemp("", "faas-buildstage-")
	if err != nil {
		return false, fmt.Errorf("builderd: mktemp staging: %w", err)
	}
	defer func() { _ = os.RemoveAll(mp) }()

	cacheRestored := false
	m.DependencyCacheImport = false
	if m.DependencyCache && dependencyCache != "" {
		cacheTarget := filepath.Join(mp, "build", "cache")
		if cacheErr := copyDependencyCache(dependencyCache, cacheTarget, dependencyCacheMaxBytes); cacheErr == nil {
			cacheRestored = true
			m.DependencyCacheImport = true
		} else {
			_ = os.RemoveAll(cacheTarget)
		}
	}
	if err := writeBuildManifest(mp, m); err != nil {
		return false, fmt.Errorf("builderd: write manifest: %w", err)
	}
	if err := copySourceTarball(mp, sourcePath); err != nil {
		return false, fmt.Errorf("builderd: copy source: %w", err)
	}
	if err := writeBuildEntropy(mp); err != nil {
		return false, fmt.Errorf("builderd: write entropy seed: %w", err)
	}
	// guest-init mounts drive1 at /overlay and uses /overlay/upper as the
	// overlay upperdir. Put the build manifest and source below upper/ so they
	// are visible from the merged root inside the builder VM.
	if err := wrapBuildUpper(mp); err != nil {
		return false, fmt.Errorf("builderd: wrap drive1 upper: %w", err)
	}
	// Sanity: confirm the bytes that landed on disk match the host source.
	// Catches a torn copy / quota-hit / ENOSPC that would otherwise surface
	// as a silent truncated tarball inside the VM.
	gotSum, err := fileSHA256(filepath.Join(mp, "upper", "build", "src.tar"))
	if err != nil {
		return false, fmt.Errorf("builderd: re-stat staged tarball: %w", err)
	}
	if gotSum != srcSum {
		return false, fmt.Errorf("builderd: staged tarball sha256 mismatch: got %s, want %s", gotSum, srcSum)
	}

	// 3. Build the ext4 image directly from the staged tree. mke2fs creates
	// the filesystem and copies the tree in one operation; the image remains
	// sparse and keeps the existing 28 GiB builder scratch budget.
	if out, err := exec.CommandContext(ctx, buildMkfs, "-L", buildLabel, "-F", "-d", mp, dest).CombinedOutput(); err != nil {
		return false, fmt.Errorf("builderd: mkfs: %w (%s)", err, string(out))
	}
	return cacheRestored, nil
}

func checkBuildDriveCapacity(dest string) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(filepath.Dir(dest), &stat); err != nil {
		return fmt.Errorf("builderd: stat drive filesystem: %w", err)
	}
	available := uint64(stat.Bavail) * uint64(stat.Bsize)
	if available < BuildDriveMinFreeBytes {
		return fmt.Errorf("builderd: insufficient host free space for build drive: available=%d bytes, required=%d bytes", available, BuildDriveMinFreeBytes)
	}
	return nil
}

// writeBuildEntropy stages fresh host-generated entropy for the guest's cold
// boot. Firecracker snapshots the VM's early entropy state, and virtio-rng
// may not seed the guest before BuildKit's RSA proxy-CA generation calls
// crypto/rand. The seed is consumed and removed by guest-init before any
// customer build command runs.
func writeBuildEntropy(mountPoint string) error {
	dir := filepath.Join(mountPoint, filepath.Dir(api.BuildEntropyPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	seed := make([]byte, 256)
	if _, err := rand.Read(seed); err != nil {
		return fmt.Errorf("generate seed: %w", err)
	}
	if err := os.WriteFile(filepath.Join(mountPoint, api.BuildEntropyPath[1:]), seed, 0o600); err != nil {
		return fmt.Errorf("write seed: %w", err)
	}
	return nil
}

// wrapBuildUpper moves the staged drive contents under upper/, preserving the
// overlayfs layout guest-init expects while keeping build.json and src.tar on
// the same ext4 image as the overlay work directory.
func wrapBuildUpper(mountPoint string) error {
	upper := filepath.Join(mountPoint, "upper")
	if err := os.MkdirAll(upper, 0o755); err != nil {
		return fmt.Errorf("mkdir upper: %w", err)
	}
	entries, err := os.ReadDir(mountPoint)
	if err != nil {
		return fmt.Errorf("read drive: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() == "upper" {
			continue
		}
		if err := os.Rename(filepath.Join(mountPoint, entry.Name()), filepath.Join(upper, entry.Name())); err != nil {
			return fmt.Errorf("wrap %s in upper: %w", entry.Name(), err)
		}
	}
	return nil
}

// copySourceTarball copies the host source tarball at sourcePath into the
// mounted drive1 at /build/src.tar. Called from inside the same mount loop
// that writeBuildManifest runs in — no extra umount cycle.
func copySourceTarball(mountPoint, sourcePath string) error {
	buildDir := filepath.Join(mountPoint, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return fmt.Errorf("mkdir build: %w", err)
	}
	//nolint:forbidigo // sourcePath is the apid-spooled tarball that already passed apid's validateTarballShape (in cmd/apid/deploy_inputs.go) — same rationale as pkg/rootfs/build.go:ApplyTarball; symmetric validation chain.
	in, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(filepath.Join(buildDir, "src.tar"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create dst: %w", err)
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("sync dst: %w", err)
	}
	return nil
}

// fileSHA256 returns the hex sha256 of path, hex-encoded. Used to verify
// the host source and the staged copy on drive1 match byte-for-byte.
//
//nolint:forbidigo // path is the buildDir/src.tar written by copySourceTarball in this file (or the equivalent apid-spooled source) — builderd is the sole writer of buildDir in the local case, and in the spooled case apid's validateTarballShape has already run. Symlink-attack impossible.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// writeBuildManifest materialises /etc/faas/build.json inside an already-
// mounted build drive1. Split out so tests can drive it without a loopback
// mount (using a plain tmp dir as mount surrogate).
func writeBuildManifest(mountPoint string, m api.BuildManifest) error {
	dir := filepath.Join(mountPoint, "etc", "faas")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	target := filepath.Join(dir, "build.json")
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	return nil
}
