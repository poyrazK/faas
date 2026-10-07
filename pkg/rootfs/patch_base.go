package rootfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/storage"
)

// ErrBasePatchUnsupported reports that a base image cannot have its guest-init
// replaced in place. Callers fall back to a full BuildBase.
var ErrBasePatchUnsupported = errors.New("rootfs: base guest-init patch unsupported")

// basePatchFakeTime pins every timestamp debugfs writes (the new inode and
// the superblock write time). Two nodes that patch the same published bytes
// with the same guest-init therefore publish identical bytes, so ADR-567
// convergence has nothing to download and ADR-510 keeps restoring snapshots
// across nodes.
const basePatchFakeTime = "946684800" // 2000-01-01T00:00:00Z

// BasePatchInput replaces PID 1 in an already-published base image.
type BasePatchInput struct {
	// SourceImage is the local path of the current base ext4. It is only read.
	SourceImage string
	// GuestInitPath is the guest-init binary to install as /sbin/init.
	GuestInitPath string
	// GuestInitSHA256 is the lowercase hex SHA-256 of GuestInitPath. The
	// patched image must hold exactly these bytes as PID 1.
	GuestInitSHA256 string
	// Storage and StorageKey name where the patched image is published.
	Storage    storage.StorageBackend
	StorageKey string
}

// outputRunner is the stdout-capturing runner debugfs needs. wire.ExecRunner
// implements it.
type outputRunner interface {
	Output(ctx context.Context, argv []string) ([]byte, error)
}

// PatchBaseGuestInit publishes a copy of in.SourceImage whose /sbin/init is
// replaced by in.GuestInitPath.
//
// guest-init is the only base content that changes on most releases: the
// OCI images are pinned by digest, but PID 1 is rebuilt from this repository
// every time. BuildBase would re-extract and re-mkfs the whole userland to
// refresh that one file. This path copies the image, swaps the file with
// debugfs (no mount, no root), proves the result holds exactly the new bytes
// and passes a read-only e2fsck, then publishes and signs it like BuildBase.
//
// The caller must already have proven that the source image was built from
// the same OCI content and base layout. Any image this path cannot handle
// returns an error so the caller rebuilds from the layers.
func (b *Builder) PatchBaseGuestInit(ctx context.Context, in BasePatchInput) (BaseBuildResult, error) {
	run, ok := b.run.(outputRunner)
	if !ok {
		return BaseBuildResult{}, fmt.Errorf("%w: runner %T cannot capture output", ErrBasePatchUnsupported, b.run)
	}
	if err := validateBasePatchInput(in); err != nil {
		return BaseBuildResult{}, err
	}
	tmpPath, err := createBaseTemp("faas-base-patch-*.ext4")
	if err != nil {
		return BaseBuildResult{}, err
	}
	defer func() { _ = os.Remove(tmpPath) }()
	if err := copyBaseImage(in.SourceImage, tmpPath); err != nil {
		return BaseBuildResult{}, err
	}

	dir, current, err := baseInitDir(ctx, run, tmpPath)
	if err != nil {
		return BaseBuildResult{}, err
	}
	initPath := path.Join(dir, "init")
	if err := writeBaseInit(ctx, run, tmpPath, dir, current, in.GuestInitPath); err != nil {
		return BaseBuildResult{}, err
	}
	if err := verifyBaseInit(ctx, run, tmpPath, initPath, current, in.GuestInitSHA256); err != nil {
		return BaseBuildResult{}, err
	}
	if _, err := run.Output(ctx, []string{"e2fsck", "-fn", tmpPath}); err != nil {
		return BaseBuildResult{}, fmt.Errorf("rootfs: patched base failed e2fsck: %w", err)
	}

	info, err := os.Stat(tmpPath)
	if err != nil {
		return BaseBuildResult{}, fmt.Errorf("rootfs: stat patched base: %w", err)
	}
	identity, err := b.publishBaseFile(ctx, in.Storage, in.StorageKey, tmpPath)
	if err != nil {
		return BaseBuildResult{}, err
	}
	return BaseBuildResult{ImageKey: in.StorageKey, SizeBytes: info.Size(), ArtifactDigest: identity.Digest, ArtifactBytes: identity.Bytes, GuestInitDigest: "sha256:" + in.GuestInitSHA256}, nil
}

// debugfsPathRE bounds every path interpolated into a debugfs request or
// script. debugfs splits commands on whitespace and interprets quotes.
var debugfsPathRE = regexp.MustCompile(`^/[A-Za-z0-9._+/-]*$`)

func debugfsSafePath(p string) bool {
	return debugfsPathRE.MatchString(p) && !strings.Contains(p, "//")
}

var sha256HexRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validateBasePatchInput(in BasePatchInput) error {
	switch {
	case in.Storage == nil || in.StorageKey == "":
		return errors.New("rootfs: PatchBaseGuestInit needs Storage and StorageKey")
	case !debugfsSafePath(in.SourceImage):
		return fmt.Errorf("%w: source image path %q", ErrBasePatchUnsupported, in.SourceImage)
	case !debugfsSafePath(in.GuestInitPath):
		return fmt.Errorf("%w: guest-init path %q", ErrBasePatchUnsupported, in.GuestInitPath)
	case !sha256HexRE.MatchString(in.GuestInitSHA256):
		return fmt.Errorf("rootfs: PatchBaseGuestInit: invalid guest-init digest %q", in.GuestInitSHA256)
	}
	return nil
}

func copyBaseImage(src, dst string) error {
	// nolint:forbidigo // src is this daemon's own storage path for a
	// platform base key, resolved by the caller. Not a customer path.
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("rootfs: open source base: %w", err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("rootfs: open patch scratch: %w", err)
	}
	// os.File.ReadFrom uses copy_file_range on Linux, which reflinks on
	// filesystems that support it and copies in-kernel elsewhere.
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("rootfs: copy source base: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("rootfs: close patch scratch: %w", err)
	}
	return nil
}

// debugfsInode is the subset of `debugfs stat` output the patch relies on.
type debugfsInode struct {
	Type     string
	Mode     string
	User     string
	Group    string
	Size     int64
	LinkDest string
}

var (
	debugfsTypeRE  = regexp.MustCompile(`Type:\s+(\S+)`)
	debugfsModeRE  = regexp.MustCompile(`Mode:\s+(0[0-7]+)`)
	debugfsOwnerRE = regexp.MustCompile(`User:\s+(\d+)\s+Group:\s+(\d+)`)
	debugfsSizeRE  = regexp.MustCompile(`Size:\s+(\d+)`)
	debugfsLinkRE  = regexp.MustCompile(`Fast link dest:\s+"([^"]*)"`)
)

func debugfsStat(ctx context.Context, run outputRunner, image, p string) (debugfsInode, error) {
	out, err := run.Output(ctx, []string{"debugfs", "-R", "stat " + p, image})
	if err != nil {
		return debugfsInode{}, fmt.Errorf("rootfs: debugfs stat %s: %w", p, err)
	}
	return parseDebugfsStat(string(out))
}

func parseDebugfsStat(out string) (debugfsInode, error) {
	var ino debugfsInode
	m := debugfsTypeRE.FindStringSubmatch(out)
	if m == nil {
		return ino, fmt.Errorf("%w: debugfs stat reported no inode", ErrBasePatchUnsupported)
	}
	ino.Type = m[1]
	if m := debugfsModeRE.FindStringSubmatch(out); m != nil {
		ino.Mode = m[1]
	}
	if m := debugfsOwnerRE.FindStringSubmatch(out); m != nil {
		ino.User, ino.Group = m[1], m[2]
	}
	if m := debugfsSizeRE.FindStringSubmatch(out); m != nil {
		ino.Size, _ = strconv.ParseInt(m[1], 10, 64)
	}
	if m := debugfsLinkRE.FindStringSubmatch(out); m != nil {
		ino.LinkDest = m[1]
	}
	return ino, nil
}

// baseInitDir resolves the directory that holds PID 1. Usrmerged images ship
// /sbin as a symlink (to usr/sbin or usr/bin); InjectGuestInit followed it,
// so the binary lives in the target directory. debugfs does not follow a
// symlink in the final component of a path, so resolve that one hop here.
// The existing init must be a regular file: anything else was not produced
// by BuildBase.
func baseInitDir(ctx context.Context, run outputRunner, image string) (string, debugfsInode, error) {
	sbin, err := debugfsStat(ctx, run, image, "/sbin")
	if err != nil {
		return "", debugfsInode{}, err
	}
	dir := "/sbin"
	switch sbin.Type {
	case "directory":
	case "symlink":
		if sbin.LinkDest == "" {
			return "", debugfsInode{}, fmt.Errorf("%w: /sbin is not a fast symlink", ErrBasePatchUnsupported)
		}
		dir = path.Clean("/" + sbin.LinkDest)
		target, err := debugfsStat(ctx, run, image, dir)
		if err != nil {
			return "", debugfsInode{}, err
		}
		if target.Type != "directory" {
			return "", debugfsInode{}, fmt.Errorf("%w: /sbin -> %s is a %s", ErrBasePatchUnsupported, dir, target.Type)
		}
	default:
		return "", debugfsInode{}, fmt.Errorf("%w: /sbin is a %s", ErrBasePatchUnsupported, sbin.Type)
	}
	if !debugfsSafePath(dir) {
		return "", debugfsInode{}, fmt.Errorf("%w: init directory %q", ErrBasePatchUnsupported, dir)
	}
	current, err := debugfsStat(ctx, run, image, path.Join(dir, "init"))
	if err != nil {
		return "", debugfsInode{}, err
	}
	if current.Type != "regular" || current.User == "" || current.Group == "" {
		return "", debugfsInode{}, fmt.Errorf("%w: existing init is a %s", ErrBasePatchUnsupported, current.Type)
	}
	return dir, current, nil
}

// writeBaseInit replaces dir/init, keeping the owner BuildBase gave it and the
// 0755 mode InjectGuestInit writes. debugfs reports command failures on
// stderr but still exits 0, so verifyBaseInit checks the outcome.
func writeBaseInit(ctx context.Context, run outputRunner, image, dir string, current debugfsInode, guestInit string) error {
	script := strings.Join([]string{
		"cd " + dir,
		"rm init",
		"write " + guestInit + " init",
		"sif init mode 0100755",
		"sif init uid " + current.User,
		"sif init gid " + current.Group,
		"",
	}, "\n")
	scriptPath := image + ".debugfs"
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		return fmt.Errorf("rootfs: write debugfs script: %w", err)
	}
	defer func() { _ = os.Remove(scriptPath) }()
	if _, err := run.Output(ctx, []string{
		"env", "E2FSPROGS_FAKE_TIME=" + basePatchFakeTime,
		"debugfs", "-w", "-f", scriptPath, image,
	}); err != nil {
		return fmt.Errorf("rootfs: debugfs replace guest-init: %w", err)
	}
	return nil
}

func verifyBaseInit(ctx context.Context, run outputRunner, image, initPath string, current debugfsInode, wantSHA256 string) error {
	got, err := debugfsStat(ctx, run, image, initPath)
	if err != nil {
		return err
	}
	if got.Type != "regular" || got.Mode != "0755" || got.User != current.User || got.Group != current.Group {
		return fmt.Errorf("rootfs: patched init is %s mode %s owner %s:%s, want regular 0755 %s:%s",
			got.Type, got.Mode, got.User, got.Group, current.User, current.Group)
	}
	dumpPath := image + ".init"
	_ = os.Remove(dumpPath)
	defer func() { _ = os.Remove(dumpPath) }()
	if _, err := run.Output(ctx, []string{"debugfs", "-R", "dump " + initPath + " " + dumpPath, image}); err != nil {
		return fmt.Errorf("rootfs: debugfs dump patched init: %w", err)
	}
	// nolint:forbidigo // dumpPath is this function's own scratch file.
	f, err := os.Open(dumpPath)
	if err != nil {
		return fmt.Errorf("rootfs: open patched init: %w", err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("rootfs: hash patched init: %w", err)
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != wantSHA256 {
		return fmt.Errorf("rootfs: patched init sha256 %s, want %s", sum, wantSHA256)
	}
	return nil
}
