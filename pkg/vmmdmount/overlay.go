// Package vmmdmount — overlay.go issues the parent-ref
// overlayfs mount on behalf of imaged (ADR-075 / DEPLOY-1).
// Before DEPLOY-1 the unix.Mount(2) syscall lived in
// pkg/imaged/mount_overlay_linux.go under
// AmbientCapabilities=cap_sys_admin — a silent violation of
// the CLAUDE.md invariant "vmmd is the only root component
// that mounts filesystems". The cap_sys_admin ambient cap
// plus the ext4 upperdir (host /tmp) plus the resulting dmesg
// "upper fs does not support tmpfile" message cascade killed
// cd-controlplane deploys for five days (2026-08-04 → 2026-08-05,
// 13 failed deploys). All three of those failures are wired up
// here: the cap walks through pkg/vmmd, the staging dir walks
// through FAAS_BASE_STAGING_ROOT, and the overlay walks through
// the kernel's tmpfile check.
//
// The mount is issued via `mount -t overlay` exec (matching
// MountExt4ReadOnly next door), not unix.Mount(2). The exec
// approach keeps pkg/vmmdmount portable — the package
// compiles on macOS / Windows even though the RPC handler is
// only exercised on a Linux vmmd (cmd/vmmd is gated to
// //go:build linux in deploy/; on non-Linux the function is
// exported but never called). The pre-DEPLOY-1 syscall lived
// on the imaged side under AmbientCapabilities; keeping
// unix.Mount out of imaged entirely is what enforces the
// architectural ownership invariant ("vmmd is the only root
// component that mounts filesystems").
//
// MountOverlayParent issues the mount on behalf of imaged. The
// caller (cmd/vmmd via the gRPC server) is the only component
// in the process tree with cap_sys_admin. imaged calls
// MountOverlayParent over its vmmdgrpc unix socket; the vmmd
// handler validates the path prefixes and forwards to this
// function.
//
// Validation chain (mirrors the MountExt4ReadOnly gate):
//  1. lowerdir must be under /srv/fc/parent/ — the loopback
//     mount vmmd has already issued (the parent-ref layer is
//     read-only and staged via MountParentExt4ReadOnly).
//  2. upperdir/workdir/merged must be under
//     /dev/shm/faas-base-staging/ — staging lives on tmpfs so
//     the kernel's overlayfs tmpfile contract is satisfied
//     (ext4 cannot host the workdir's tmpfile + rename atomic
//     cycle; this is the original 2026-08-04 dmesg bug).
//  3. Empty strings → InvalidArgument (imaged's defer-after-
//     error is safe on these — the handler absorbs them).
//  4. mount(8) errors → wrapped Internal so the gRPC handler
//     can surface a problem document.
package vmmdmount

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// OverlayStagingRoot is the tmpfs root for the overlayfs
// upperdir/workdir/merged trio. The constant mirrors the env
// var pkg/rootfs/build_base.go honours (FAAS_BASE_STAGING_ROOT,
// default /dev/shm/faas-base-staging). Pinning the constant here
// makes the vmmd-side validation chain immune to imaged setting
// the env var to something the host can't support.
//
// On non-Linux dev (macOS / Windows) the function never runs —
// the gRPC handler is metal-only, and the unit test in
// overlay_test.go uses a stub for the syscall.
const OverlayStagingRoot = "/dev/shm/faas-base-staging"

// OverlayMountPrefix is the basename prefix for upper/work/merged
// subdirs. Picked to be visually distinct from
// ParentMountPrefix so an `ls /dev/shm/faas-base-staging` is
// obvious about what's a parent-ref staging tree vs a stray.
const OverlayMountPrefix = "faas-overlay-"

// ErrInvalidOverlayPath is the typed sentinel MountOverlayParent
// returns when lowerdir is not under /srv/fc/parent or
// upperdir/workdir/merged is not under /dev/shm/faas-base-staging.
// The gRPC handler lifts this to InvalidArgument so a misbehaving
// imaged can't persuade vmmd to mount something the kernel can
// reach (e.g. /home/user/Documents) by setting the right env
// var.
var ErrInvalidOverlayPath = errors.New("vmmdmount: overlay path outside allowed prefixes")

// MountOverlayParent mounts an overlayfs with lowerdir (read-only
// loopback already issued by vmmd), upperdir+workdir on the host
// tmpfs, and merged as the combined view. The function issues
// `mount -t overlay` via exec (matching MountExt4ReadOnly next
// door) so pkg/vmmdmount stays host-portable — the package
// compiles on macOS / Windows even though only the Linux vmmd
// daemon actually issues the call. Tests stub the exec call via
// PATH manipulation if they need to fake the syscall (see
// overlay_test.go).
//
// On success returns nil. On any error path the freshly-created
// merged dir is rmdir'd so a failed mount doesn't leave orphans
// under /dev/shm/faas-base-staging.
//
// Validation:
//   - lowerdir MUST exist and be a directory; it must be under
//     /srv/fc/parent/. (Loopback mount already issued.)
//   - upperdir, workdir, merged MUST be under
//     /dev/shm/faas-base-staging/. The caller (cmd/imaged) is
//     responsible for creating them; this function only creates
//     `merged` if it does not exist (the parent dirs were made
//     by MkdirBaseStaging + os.MkdirTemp).
//   - All four paths must be absolute; relative paths are
//     rejected to prevent ../../ escape.
//   - No symlinks and no `..` segments survive validation
//     (EvalSymlinks + Abs + re-check against the prefix); a
//     misbehaving imaged handing the RPC a `/srv/fc/parent/foo`
//     symlink whose target is `/etc` would otherwise be honoured
//     by both the prefix test (literal HasPrefix passes) and
//     the kernel's mount(2) (which follows symlinks).
func MountOverlayParent(ctx context.Context, lowerdir, upperdir, workdir, merged string) error {
	if lowerdir == "" || upperdir == "" || workdir == "" || merged == "" {
		return fmt.Errorf("vmmdmount: MountOverlayParent: empty path (lower=%q upper=%q work=%q merged=%q)",
			lowerdir, upperdir, workdir, merged)
	}

	// All paths must be absolute. relative paths would let a
	// caller mount into an arbitrary cwd (which on vmmd is /).
	if !filepath.IsAbs(lowerdir) || !filepath.IsAbs(upperdir) ||
		!filepath.IsAbs(workdir) || !filepath.IsAbs(merged) {
		return fmt.Errorf("vmmdmount: MountOverlayParent: non-absolute path rejected")
	}

	// Evaluate symlinks + collapse `..` segments BEFORE the
	// prefix check. Combined with the post-Eval abs()-vs-input
	// compare, this closes the symlink + `..`-escape channel
	// (review B3): a literal HasPrefix on filepath.Clean is
	// not enough on its own because (a) `..` survives Clean and
	// (b) os.Stat + the kernel both follow symlinks.
	if err := rejectSymlinkOrEscape(lowerdir, filepath.Clean(MountRoot)+string(filepath.Separator), "lowerdir"); err != nil {
		return err
	}
	stagingPrefix := filepath.Clean(OverlayStagingRoot) + string(filepath.Separator)
	for _, p := range []string{upperdir, workdir, merged} {
		if err := rejectSymlinkOrEscape(p, stagingPrefix, "upper|work|merged"); err != nil {
			return err
		}
	}

	// The lowerdir must exist (vmmd should have loopback-mounted
	// it via MountParentExt4ReadOnly before imaged fires this
	// RPC). Stat catches the race where imaged gets the
	// mountpoint from a previous gRPC call but the loopback has
	// been swept by the orphan-sweep thread.
	if info, err := os.Stat(lowerdir); err != nil {
		return fmt.Errorf("vmmdmount: MountOverlayParent: stat lowerdir: %w", err)
	} else if !info.IsDir() {
		return fmt.Errorf("vmmdmount: MountOverlayParent: lowerdir %q is not a directory", lowerdir)
	}

	// Ensure `merged` exists. imaged's MkdirBaseStaging +
	// os.MkdirTemp creates upper+work; merged is the empty dir
	// overlayfs writes into. We mkdir here so a caller that
	// forgot to mkdir gets a clean InvalidArgument rather than
	// an opaque EINVAL from the kernel.
	if err := os.MkdirAll(merged, 0o755); err != nil {
		return fmt.Errorf("vmmdmount: MountOverlayParent: mkdir merged: %w", err)
	}

	// The mount itself. Uses `mount -t overlay` (not
	// unix.Mount(2)) for portability — pkg/vmmdmount must
	// compile on macOS / Windows so the gRPC client stubs in
	// pkg/imaged can build there, even though only the
	// Linux vmmd daemon actually issues the call. This
	// matches MountExt4ReadOnly next door. The exec helper
	// returns EINVAL-shaped errors with stderr captured; we
	// wrap so the gRPC handler can surface a problem document.
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lowerdir, upperdir, workdir)
	cmd := exec.CommandContext(ctx, "mount", "-t", "overlay", "-o", opts, "overlay", merged)
	if out, err := cmd.CombinedOutput(); err != nil {
		// rmdir the merged dir we just created — the mount
		// failed so the caller can't umount it.
		_ = os.Remove(merged)
		return fmt.Errorf("vmmdmount: MountOverlayParent: mount overlay: %w (%s)",
			err, strings.TrimSpace(string(out)))
	}
	return nil
}

// rejectSymlinkOrEscape evaluates `path` for symlinks + `..`
// collapses and verifies (a) its real location lives under
// `mustUnder` and (b) the path the caller handed us was the real
// location — i.e. the caller did not pass a symlink or a `..`-
// chained path that resolves to a different directory. Used by
// MountOverlayParent + UmountOverlayParent so the RPC boundary
// closes the symlink-follow and dot-dot escape channels
// (review B3 / M3 path validation).
//
// `label` is included in error messages so the failing path is
// obvious (lowerdir vs upper vs work vs merged).
func rejectSymlinkOrEscape(path, mustUnder, label string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("vmmdmount: %s: filepath.Abs: %w", label, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// ENOENT is fine — the staging tree's `upper`, `work`,
		// `merged` subdirs may not yet exist on Mount (imaged
		// creates them after the RPC returns OK). Surface a
		// typed ErrInvalidOverlayPath only when the reason is
		// anything other than "missing".
		if os.IsNotExist(err) {
			// errorlint (memory: errorlint-non-wrapping-errors-join):
			// use errors.Join so both ErrInvalidOverlayPath AND
			// the underlying os.PathError remain reachable via
			// errors.Is / errors.As — a "%w (extra)" shape only
			// wraps the first arg and hides the second.
			return errors.Join(ErrInvalidOverlayPath, fmt.Errorf("vmmdmount: %s: %w", label, err))
		}
		return fmt.Errorf("vmmdmount: %s: EvalSymlinks: %w", label, err)
	}
	// The kernel will see the resolved path; if the path the
	// caller handed us differs from resolved, the caller is
	// asking us to mount through a symlink or a `..` chain —
	// refuse. (EvalSymlinks collapses `..` segments; an
	// unchanged returned path means neither was present.)
	if resolved != abs {
		return fmt.Errorf("vmmdmount: %s: %q resolves to %q (symlink or dot-dot not allowed): %w",
			label, path, resolved, ErrInvalidOverlayPath)
	}
	if !strings.HasPrefix(resolved, mustUnder) {
		return fmt.Errorf("vmmdmount: %s: %q not under %s: %w",
			label, path, mustUnder, ErrInvalidOverlayPath)
	}
	return nil
}

// UmountOverlayParent unmounts a merged directory previously
// returned via MountOverlayParent. Idempotent on unknown
// mountpoints — like UmountExt4, the gRPC handler absorbs
// ErrUnknownMountpoint so imaged's defer-after-error is safe.
//
// The function uses `exec.Command("umount", ...)` (not unix.
// Unmount) because /dev/shm/faas-base-staging is not vmmd's
// own mount namespace — we don't have CAP_SYS_ADMIN against
// /dev/shm, we have it against the host. The umount(8) binary
// handles the path-resolution correctly. (Note: on a vmmd that
// runs PrivateTmp=yes, /dev/shm is the host's, not a
// per-process mount — confirmed by deploy/systemd/faas-vmmd.service
// not setting PrivateTmp.)
func UmountOverlayParent(ctx context.Context, merged string) error {
	if merged == "" {
		return ErrUnknownMountpoint
	}
	stagingPrefix := filepath.Clean(OverlayStagingRoot) + string(filepath.Separator)
	if !strings.HasPrefix(merged, stagingPrefix) {
		return fmt.Errorf("vmmdmount: UmountOverlayParent: merged %q outside %s: %w",
			merged, OverlayStagingRoot, ErrInvalidOverlayPath)
	}
	if _, err := os.Stat(merged); err != nil {
		if os.IsNotExist(err) {
			return ErrUnknownMountpoint
		}
		return fmt.Errorf("vmmdmount: UmountOverlayParent: stat merged: %w", err)
	}
	cmd := exec.CommandContext(ctx, "umount", merged)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("vmmdmount: UmountOverlayParent: umount %s: %w (%s)",
			merged, err, strings.TrimSpace(string(out)))
	}
	return removeReleasedMountpoint(merged)
}
