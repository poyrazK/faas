//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
)

// Guest device layout (spec §4.6): drive0 (vda) is the shared read-only base and
// the kernel root; drive1 (vdb) is the per-app writable layer. guest-init mounts
// vdb, builds an overlay with the base as the read-only lower and the layer as
// the writable upper, pivots into it, then execs the app.
const (
	layerDevice = "/dev/vdb"
	layerMount  = "/overlay"
	newRoot     = "/overlay/merged"
)

// main is guest PID 1. Any fatal error here panics the VM (panic=1 in boot args
// reboots it), which schedd observes as a failed wake.
func main() {
	if err := boot(); err != nil {
		fmt.Fprintf(os.Stderr, "guest-init: %v\n", err)
		os.Exit(1)
	}
}

func boot() error {
	if err := mountBasics(); err != nil {
		return fmt.Errorf("mount basics: %w", err)
	}
	if err := assembleOverlay(); err != nil {
		return fmt.Errorf("assemble overlay: %w", err)
	}
	if err := pivotInto(newRoot); err != nil {
		return fmt.Errorf("pivot_root: %w", err)
	}

	f, err := os.Open(api.AppManifestPath)
	if err != nil {
		return fmt.Errorf("open manifest: %w", err)
	}
	manifest, err := api.ReadManifest(f)
	_ = f.Close()
	if err != nil {
		return err
	}

	sup := Supervisor{
		Max:   MaxRestarts,
		Start: func() error { return runApp(manifest) },
		OnCrash: func(attempt int, err error) {
			fmt.Fprintf(os.Stderr, "guest-init: app crashed (restart %d/%d): %v\n", attempt, MaxRestarts, err)
		},
	}
	return sup.Run()
}

// mountBasics mounts the pseudo-filesystems every app expects.
func mountBasics() error {
	type m struct{ src, dst, fs string }
	for _, mnt := range []m{
		{"proc", "/proc", "proc"},
		{"sysfs", "/sys", "sysfs"},
		{"tmpfs", "/tmp", "tmpfs"},
		{"devtmpfs", "/dev", "devtmpfs"},
	} {
		_ = os.MkdirAll(mnt.dst, 0o755)
		if err := syscall.Mount(mnt.src, mnt.dst, mnt.fs, 0, ""); err != nil {
			// /dev may already be a devtmpfs from the kernel; tolerate EBUSY.
			if mnt.dst != "/dev" {
				return fmt.Errorf("mount %s: %w", mnt.dst, err)
			}
		}
	}
	return nil
}

// assembleOverlay mounts the app layer and stacks it over the read-only base.
func assembleOverlay() error {
	if err := os.MkdirAll(layerMount, 0o755); err != nil {
		return err
	}
	if err := syscall.Mount(layerDevice, layerMount, "ext4", 0, ""); err != nil {
		return fmt.Errorf("mount layer %s: %w", layerDevice, err)
	}
	for _, d := range []string{"upper", "work", "merged"} {
		if err := os.MkdirAll(layerMount+"/"+d, 0o755); err != nil {
			return err
		}
	}
	opts := "lowerdir=/,upperdir=" + layerMount + "/upper,workdir=" + layerMount + "/work"
	if err := syscall.Mount("overlay", newRoot, "overlay", 0, opts); err != nil {
		return fmt.Errorf("mount overlay: %w", err)
	}
	return nil
}

// pivotInto makes root the new root filesystem.
func pivotInto(root string) error {
	if err := os.MkdirAll(root+"/oldroot", 0o755); err != nil {
		return err
	}
	if err := syscall.PivotRoot(root, root+"/oldroot"); err != nil {
		return err
	}
	if err := syscall.Chdir("/"); err != nil {
		return err
	}
	// Detach the old root lazily.
	_ = syscall.Unmount("/oldroot", syscall.MNT_DETACH)
	return nil
}

// runApp execs the customer app as the app user and waits for it to exit.
func runApp(m api.AppManifest) error {
	argv := m.Entrypoint
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = m.EffectiveWorkingDir()
	cmd.Env = BuildEnv(os.Environ(), m)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if uid := lookupUID(m.EffectiveUser()); uid > 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(uid)},
		}
	}
	return cmd.Run()
}

// lookupUID resolves the app user name to a uid. The runner images create the
// app user at DefaultAppUID; unknown users fall back to that.
func lookupUID(user string) int {
	if user == api.DefaultAppUser {
		return api.DefaultAppUID
	}
	return api.DefaultAppUID
}
