//go:build linux

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

const (
	guestShmDir           = "/dev/shm"
	devptsDir             = "/dev/pts"
	devptsMountOptions    = "newinstance,ptmxmode=0666,mode=0620"
	guestHostsPath        = "/etc/hosts"
	guestTmpDir           = "/tmp"
	guestTmpStagingPrefix = ".guest-tmp-"
)

// prepareGuestRuntime gives every guest the process environment a container
// runtime provides (ADR-20261025111339) once the image root is in place. Each
// step only logs on failure: a guest that boots today keeps booting.
func prepareGuestRuntime(log *slog.Logger) {
	if err := mountGuestDevices(); err != nil {
		log.Warn("guest /dev mounts", "err", err)
	}
	if err := raiseOpenFileLimit(); err != nil {
		log.Warn("raise open file limit", "err", err)
	}
	hostname, err := ensureGuestHostname()
	if err != nil {
		log.Warn("set guest hostname", "err", err)
		return
	}
	if err := ensureGuestHostsFile(guestHostsPath, hostname); err != nil {
		log.Warn("guest /etc/hosts", "err", err)
	}
}

// mountGuestDevices adds the /dev entries a fresh devtmpfs lacks: a tmpfs
// for POSIX shared memory (Chromium, PostgreSQL and Python multiprocessing
// need /dev/shm) and a devpts instance for pseudo-terminals. Like /tmp,
// /dev/shm is the kernel's default size, half the guest's RAM, and what it
// holds counts against the guest's memory.
func mountGuestDevices() error {
	var errs []error
	if err := os.MkdirAll(guestShmDir, 0o755); err != nil {
		errs = append(errs, fmt.Errorf("create %s: %w", guestShmDir, err))
	} else if err := syscall.Mount("shm", guestShmDir, "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV, "mode=1777"); err != nil && !errors.Is(err, syscall.EBUSY) {
		errs = append(errs, fmt.Errorf("mount %s: %w", guestShmDir, err))
	}
	if err := ensureDevpts(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// ensureDevpts mounts a private devpts instance unless one is mounted. The
// kernel's /dev/ptmx opens the instance at the sibling /dev/pts.
func ensureDevpts() error {
	if info, err := os.Stat(devptsDir + "/ptmx"); err == nil && info.Mode()&os.ModeCharDevice != 0 {
		return nil
	}
	if err := os.MkdirAll(devptsDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", devptsDir, err)
	}
	err := syscall.Mount("devpts", devptsDir, "devpts", syscall.MS_NOSUID|syscall.MS_NOEXEC, devptsMountOptions)
	if err != nil && !errors.Is(err, syscall.EBUSY) {
		return fmt.Errorf("mount devpts: %w", err)
	}
	return nil
}

// mountGuestTmp mounts the guest's RAM-backed /tmp. An image that ships
// files under /tmp keeps them, as under Docker: the tmpfs becomes the
// writable upper of an overlay whose lower is the image's /tmp. An image
// with an empty /tmp gets the plain tmpfs it always had.
func mountGuestTmp() error {
	if err := os.MkdirAll(guestTmpDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", guestTmpDir, err)
	}
	if entries, err := os.ReadDir(guestTmpDir); err == nil && len(entries) > 0 {
		err := mountTmpOverImage(guestTmpDir, "/dev")
		if err == nil {
			return nil
		}
		slog.Default().Warn("image /tmp overlay unavailable; /tmp starts empty", "err", err)
	}
	return syscall.Mount("tmpfs", guestTmpDir, "tmpfs", 0, "")
}

// mountTmpOverImage stacks a tmpfs upper over the image directory dir. The
// tmpfs is staged under stagingParent (the guest's /dev) and detached once
// the overlay holds it, so no staging path stays visible to the workload.
func mountTmpOverImage(dir, stagingParent string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("stat %s: no owner", dir)
	}
	stage, err := os.MkdirTemp(stagingParent, guestTmpStagingPrefix)
	if err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	defer func() { _ = os.Remove(stage) }()
	if err := syscall.Mount("tmpfs", stage, "tmpfs", 0, ""); err != nil {
		return fmt.Errorf("mount staging tmpfs: %w", err)
	}
	// The overlay keeps its own reference to its upper layer.
	defer func() { _ = syscall.Unmount(stage, syscall.MNT_DETACH) }()
	upper, work := filepath.Join(stage, "upper"), filepath.Join(stage, "work")
	for _, d := range []string{upper, work} {
		if err := os.Mkdir(d, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	// The overlay's root takes its mode and owner from the upper directory.
	if err := os.Chown(upper, int(stat.Uid), int(stat.Gid)); err != nil {
		return fmt.Errorf("chown upper: %w", err)
	}
	if err := os.Chmod(upper, info.Mode()&(fs.ModePerm|fs.ModeSticky|fs.ModeSetgid|fs.ModeSetuid)); err != nil {
		return fmt.Errorf("chmod upper: %w", err)
	}
	opts := "lowerdir=" + dir + ",upperdir=" + upper + ",workdir=" + work
	if err := syscall.Mount("overlay", dir, "overlay", 0, opts); err != nil {
		return fmt.Errorf("mount overlay: %w", err)
	}
	return nil
}

// raiseOpenFileLimit sets the descriptor limit every workload inherits.
// syscall.Setrlimit, unlike unix.Setrlimit, also replaces the limit the Go
// runtime restores in the children it starts.
func raiseOpenFileLimit() error {
	return syscall.Setrlimit(syscall.RLIMIT_NOFILE, &syscall.Rlimit{
		Cur: api.GuestOpenFilesSoftLimit,
		Max: api.GuestOpenFilesHardLimit,
	})
}

// ensureGuestHostname names a guest the kernel left unnamed and returns the
// guest's hostname.
func ensureGuestHostname() (string, error) {
	var uts unix.Utsname
	if err := unix.Uname(&uts); err != nil {
		return "", fmt.Errorf("uname: %w", err)
	}
	if name := unix.ByteSliceToString(uts.Nodename[:]); name != "" && name != "(none)" {
		return name, nil
	}
	if err := unix.Sethostname([]byte(guestHostname)); err != nil {
		return "", fmt.Errorf("sethostname: %w", err)
	}
	return guestHostname, nil
}

// ensureGuestHostsFile adds the localhost and hostname entries the image's
// hosts file lacks, creating the file when the image has none. A symlinked
// hosts file is replaced by a regular one, as a runtime's bind mount hides it.
func ensureGuestHostsFile(path, hostname string) error {
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	updated, changed := hostsWithGuestEntries(string(current), hostname)
	if !changed {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".guest-init"
	if err := os.WriteFile(tmp, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// workloadHome is uid's passwd home in the guest root when uid:gid can write
// there, and "" otherwise; StampDefaultHomeEnv then falls back to /tmp.
func workloadHome(uid, gid uint32) string {
	home := imagePasswdHome(uid)
	if home == "/" {
		return ""
	}
	info, err := os.Stat(home)
	if err != nil {
		return ""
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !homeWritable(info, stat.Uid, stat.Gid, uid, gid) {
		return ""
	}
	return home
}
