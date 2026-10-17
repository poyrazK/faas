//go:build linux

package dbvm

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

const cgroupMount = "/sys/fs/cgroup"

// prepareJail creates the chroot and binds the kernel and base read-only
// and the volume read-write. Nothing is copied: the jail root may be tmpfs.
func (r *Runtime) prepareJail(vm *databaseVM, base string) error {
	if err := os.MkdirAll(vm.root, 0o755); err != nil {
		return fmt.Errorf("dbvm: jail root: %w", err)
	}
	for _, b := range []struct {
		src, name string
		readOnly  bool
	}{
		{r.cfg.KernelPath, jailKernel, true},
		{base, jailBase, true},
		{vm.volume, jailVolume, false},
	} {
		target := filepath.Join(vm.root, b.name)
		if err := bindFile(b.src, target, b.readOnly); err != nil {
			return fmt.Errorf("dbvm: bind %s: %w", b.name, err)
		}
		vm.binds = append(vm.binds, target)
	}
	// Firecracker runs as the jail uid; only it may open the volume.
	if err := os.Chown(vm.volume, vm.lease.UID, vm.lease.GID); err != nil {
		return fmt.Errorf("dbvm: chown volume: %w", err)
	}
	return os.Chmod(vm.volume, 0o600)
}

// prepareSpareJail binds the kernel and base like any VM's, and the spare's
// directory in place of a volume: a database's volume is linked into it
// when one adopts the spare.
func (r *Runtime) prepareSpareJail(vm *databaseVM, base string) error {
	if err := os.MkdirAll(vm.root, 0o755); err != nil {
		return fmt.Errorf("dbvm: jail root: %w", err)
	}
	for _, b := range []struct{ src, name string }{{r.cfg.KernelPath, jailKernel}, {base, jailBase}} {
		target := filepath.Join(vm.root, b.name)
		if err := bindFile(b.src, target, true); err != nil {
			return fmt.Errorf("dbvm: bind %s: %w", b.name, err)
		}
		vm.binds = append(vm.binds, target)
	}
	target := filepath.Join(vm.root, spareJailDir)
	if err := os.Mkdir(target, 0o700); err != nil {
		return fmt.Errorf("dbvm: spare dir in jail: %w", err)
	}
	if err := unix.Mount(vm.spareDir, target, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("dbvm: bind spare dir: %w", err)
	}
	vm.binds = append(vm.binds, target)
	return nil
}

func bindFile(src, target string, readOnly bool) error {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_ = f.Close()
	if err := unix.Mount(src, target, "", unix.MS_BIND, ""); err != nil {
		return err
	}
	if readOnly {
		flags := uintptr(unix.MS_BIND | unix.MS_REMOUNT | unix.MS_RDONLY | unix.MS_NOSUID | unix.MS_NODEV)
		if err := unix.Mount("", target, "", flags, ""); err != nil {
			_ = unix.Unmount(target, unix.MNT_DETACH)
			return err
		}
	}
	return nil
}

func unmountJail(targets []string) {
	for i := len(targets) - 1; i >= 0; i-- {
		_ = unix.Unmount(targets[i], unix.MNT_DETACH)
	}
}

func removeJailCgroup(parent, jailID string) {
	_ = os.Remove(filepath.Join(cgroupMount, parent, jailID))
}

// Recover cleans up database jails left by a previous vmmd process: it
// kills their Firecracker processes, detaches their binds and removes the
// jail directories and cgroups. Volumes are untouched; PostgreSQL recovers
// from its WAL on the next boot.
func (r *Runtime) Recover() error {
	kept, err := r.recoverJails()
	if err != nil {
		return err
	}
	// Spare directories hold only placeholders and links to volumes; only
	// adopted VMs that a spare served still use theirs.
	spares := filepath.Join(r.cfg.VolumeRoot, spareRoot)
	entries, err := os.ReadDir(spares)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("dbvm: recover spares: %w", err)
	}
	for _, e := range entries {
		dir := filepath.Join(spares, e.Name())
		if !kept[dir] {
			if err := os.RemoveAll(dir); err != nil {
				return fmt.Errorf("dbvm: recover spares: %w", err)
			}
		}
	}
	return nil
}

// recoverJails adopts every running database VM a previous vmmd left
// (recovery.go) and cleans up every other jail. It returns the spare
// directories the adopted VMs use.
func (r *Runtime) recoverJails() (map[string]bool, error) {
	kept := map[string]bool{}
	parent := filepath.Join(r.cfg.JailBase, filepath.Base(r.fcReal))
	entries, err := os.ReadDir(parent)
	if errors.Is(err, os.ErrNotExist) {
		return kept, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dbvm: recover: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), jailIDPrefix) {
			continue
		}
		dir := filepath.Join(parent, e.Name())
		id, vm, err := r.adoptJail(e.Name(), dir)
		if err == nil {
			r.mu.Lock()
			_, dup := r.vms[id]
			if !dup {
				r.vms[id] = vm
			}
			r.mu.Unlock()
			if !dup {
				if vm.spareDir != "" {
					kept[vm.spareDir] = true
				}
				continue
			}
			err = errors.New("a second VM for the database")
		}
		if vm != nil {
			// Adopted halfway: stop it like any VM of ours, cleanly first.
			r.logWarn("dbvm: recover: stopping a VM it could not adopt", "jail", e.Name(), "err", err)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = Control(ctx, r.udsPath(vm), dbguest.Request{Op: dbguest.OpStop})
			cancel()
			select {
			case <-vm.exited:
			case <-time.After(r.cfg.StopTimeout):
			}
			r.teardown(id, vm)
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			r.logWarn("dbvm: recover: cleaning up a jail", "jail", e.Name(), "reason", err)
		}
		killJailProcesses(r.cfg.ParentCgroup, e.Name())
		unmountJail(mountsUnder(dir))
		if err := os.RemoveAll(dir); err != nil {
			return nil, fmt.Errorf("dbvm: recover %s: %w", e.Name(), err)
		}
		removeJailCgroup(r.cfg.ParentCgroup, e.Name())
	}
	return kept, nil
}

// killJailProcesses kills the Firecracker process of a jail. Jailer pivots
// into a private mount namespace, so /proc/<pid>/root does not identify the
// jail from the host; the jail id on the command line, in the jail's own
// cgroup under parent, does. The cgroup keeps another runtime's VM of the
// same database (a primary and its standby on one test host) out.
func killJailProcesses(parent, jailID string) {
	for _, pid := range jailProcesses(parent, jailID) {
		_ = unix.Kill(pid, unix.SIGKILL)
	}
}

func jailProcesses(parent, jailID string) []int {
	var out []int
	want := "0::/" + strings.Trim(parent, "/") + "/" + jailID
	for _, pid := range jailIDProcesses(jailID) {
		raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cgroup"))
		if err == nil && slices.Contains(strings.Split(strings.TrimSpace(string(raw)), "\n"), want) {
			out = append(out, pid)
		}
	}
	return out
}

func jailIDProcesses(jailID string) []int {
	procs, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var pids []int
	for _, p := range procs {
		pid, err := strconv.Atoi(p.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", p.Name(), "cmdline"))
		if err == nil && cmdlineHasJailID(raw, jailID) {
			pids = append(pids, pid)
		}
	}
	return pids
}

// mountsUnder lists mount points below dir from /proc/self/mountinfo in
// lexical order; unmountJail detaches them in reverse, children first.
func mountsUnder(dir string) []string {
	raw, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	var out []string
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) > 4 && strings.HasPrefix(fields[4], dir+"/") {
			out = append(out, fields[4])
		}
	}
	sort.Strings(out)
	return out
}
