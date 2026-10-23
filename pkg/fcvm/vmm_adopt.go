package fcvm

// vmmd restart adoption ADR §2 "Mounts", slice 2. The image binds the
// previous vmmd made exist only in its own mount namespace and in
// Firecracker's jailer-unshared copy; the successor cannot see or unmount
// them. It rebuilds their bookkeeping from the journal so teardown removes
// the targets, restores the shared sources' modes through the same
// reference counts, and retires the assets, once no process still holds the
// namespace that made the mounts.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
)

// adoptedOwner is the app identity an adopted guest's log ring carries, as
// registerLeaseRing gives a booted one: its lines count as the app's and
// reach the app's log observers (ADR-743–752).
type adoptedOwner struct {
	AppID, DeploymentID string
}

// adoptGuestProcess registers process as l's running Firecracker (adoption
// ADR §2). process must already have passed the adoption proof; it is
// checked again around opening its handles. The exit code of an adopted
// process is unknown (-1); guest-reported causes stay authoritative.
func (v *JailerVMM) adoptGuestProcess(l Lease, process resourceProcessIdentity, owner adoptedOwner, jails, binds, files []resourceAsset) error {
	if v.nativeRecovery != nil {
		return errors.New("vmm: adoption is not available with native process recovery")
	}
	v.mu.Lock()
	_, taken := v.proc[l.Instance]
	v.mu.Unlock()
	if taken {
		return fmt.Errorf("vmm: adopt %s: instance already has a process", l.Instance)
	}
	p, wait, err := openAdoptedProcess(process)
	if err != nil {
		return fmt.Errorf("vmm: adopt %s: %w", l.Instance, err)
	}
	// Every step that can fail runs before the guest is registered, and
	// undoes itself, so a refused guest leaves nothing behind.
	fail := func(err error) error {
		v.dropAdoptedBinds(l.Instance)
		v.untrackCgroupFence(l.Instance)
		_ = p.Release()
		go wait() // closes the poll handle once the process exits
		return err
	}
	// Staged files (a reflinked writable layer, a materialised copy) are
	// removed at teardown only while they are still the recorded files.
	for _, a := range files {
		if a.File == nil {
			return fail(fmt.Errorf("vmm: adopt %s: %s %s has no recorded identity", l.Instance, a.Kind, a.Path))
		}
		present, err := resourceFileMatches(a.Path, *a.File)
		if err != nil {
			return fail(fmt.Errorf("vmm: adopt %s: check %s %s against the recorded file: %w", l.Instance, a.Kind, a.Path, err))
		}
		if !present {
			return fail(fmt.Errorf("vmm: adopt %s: %s %s is not the recorded file", l.Instance, a.Kind, a.Path))
		}
	}
	if err := v.adoptGuestBinds(l.Instance, binds); err != nil {
		return fail(err)
	}
	if err := v.adoptTunSourceBind(l.Instance); err != nil {
		return fail(fmt.Errorf("vmm: adopt %s: TUN source bind: %w", l.Instance, err))
	}
	if !l.IsBuilder {
		if err := v.trackCgroupFence(l); err != nil {
			return fail(fmt.Errorf("vmm: adopt %s: cgroup fence: %w", l.Instance, err))
		}
		// Write the steady fence now: a startup CPU boost the predecessor
		// left running would otherwise keep its raised cpu.max, because the
		// boost's restore timer died with it.
		if err := v.applyAdoptedFence(l.Instance); err != nil {
			return fail(fmt.Errorf("vmm: adopt %s: cgroup fence: %w", l.Instance, err))
		}
	}
	cmd := &exec.Cmd{Process: p}
	rec := &instanceRecord{cmd: cmd, consolePath: v.consoleLogPath(l.Instance), isBuilder: l.IsBuilder, done: make(chan struct{}), fileConsole: true}
	v.mu.Lock()
	if _, taken := v.proc[l.Instance]; taken {
		v.mu.Unlock()
		return fail(fmt.Errorf("vmm: adopt %s: instance already has a process", l.Instance))
	}
	v.proc[l.Instance] = cmd
	v.recs[l.Instance] = rec
	v.mu.Unlock()
	for _, a := range jails {
		v.rememberJail(l.Instance, a)
	}
	for _, a := range files {
		v.trackMaterialised(l.Instance, a.Path)
		v.mu.Lock()
		if v.materialisedIdentity == nil {
			v.materialisedIdentity = make(map[string]resourceFileIdentity)
		}
		v.materialisedIdentity[a.Path] = *a.File
		v.mu.Unlock()
	}
	var ring *logbuf.Ring
	if l.IsBuilder {
		ring = v.registerRing(l.Instance) // build output is never the app's (ADR-746)
	} else {
		ring = v.registerDeploymentRing(l.Instance, owner.AppID, owner.DeploymentID)
	}
	if ring != nil {
		// Lines written while no vmmd ran stay in the file; the ring carries
		// on from what is there now.
		var from int64
		if info, err := os.Stat(rec.consolePath); err == nil {
			from = info.Size()
		}
		w := &customerConsoleWriter{ring: ring}
		go tailConsole(rec.consolePath, from, w, w.Flush, rec.done)
	}
	go v.watchAdoptedProcess(l, cmd, rec, wait)
	return nil
}

// watchAdoptedProcess is the adopted guest's exit watchdog, the counterpart
// of startJailer's Wait goroutine.
func (v *JailerVMM) watchAdoptedProcess(l Lease, cmd *exec.Cmd, rec *instanceRecord, wait func()) {
	wait()
	var sink func(string, int)
	var attemptSink func(string, uint64, int)
	v.mu.Lock()
	rec.exitCode = -1
	if current, ok := v.proc[l.Instance]; ok && current == cmd {
		delete(v.proc, l.Instance)
		if !rec.isBuilder && !rec.stopping {
			sink = v.processExitSink
			attemptSink = v.processExitAttemptSink
		}
	}
	rec.exited = true
	close(rec.done)
	v.mu.Unlock()
	if attemptSink != nil {
		attemptSink(l.Instance, l.processGeneration, -1)
	} else if sink != nil {
		sink(l.Instance, -1)
	}
}

// adoptGuestBinds rebuilds bindMounts and bindSourceModes for one adopted
// guest from its journaled bind assets. Each source is reopened and must
// still be the recorded inode; its reference pins it for teardown.
func (v *JailerVMM) adoptGuestBinds(instance string, binds []resourceAsset) error {
	for _, a := range binds {
		if a.Kind != "bind" || a.SourceFile == nil || a.Mount == nil || a.Target == nil {
			return fmt.Errorf("vmm: adopt %s: bind %s lacks its checkpoint", instance, a.Path)
		}
		f, err := os.OpenFile(a.Source, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return fmt.Errorf("vmm: adopt %s: bind source %s: %w", instance, a.Source, err)
		}
		info, err := f.Stat()
		if err == nil {
			var id resourceFileIdentity
			id, err = resourceFileID(info)
			if err == nil && id != *a.SourceFile {
				err = errors.New("source was replaced")
			}
		}
		if err != nil {
			_ = f.Close()
			return fmt.Errorf("vmm: adopt %s: bind source %s: %w", instance, a.Source, err)
		}
		mode := os.FileMode(a.OriginalMode).Perm()
		mount, target := *a.Mount, *a.Target
		v.mu.Lock()
		handle, err := v.retainBindSourceLocked(bindSourceKey{path: a.Source, file: *a.SourceFile}, mode, f)
		if err == nil {
			if v.bindMounts == nil {
				v.bindMounts = make(map[string][]ephemeralBind)
			}
			v.bindMounts[instance] = append(v.bindMounts[instance], ephemeralBind{
				source: a.Source, mountpoint: a.Path, mode: mode, file: *a.SourceFile,
				mount: &mount, target: &target, tracked: true, adopted: true,
			})
		}
		v.mu.Unlock()
		if err != nil {
			_ = f.Close()
			return fmt.Errorf("vmm: adopt %s: bind source %s: %w", instance, a.Source, err)
		}
		if handle != f {
			_ = f.Close()
		}
	}
	return nil
}

// adoptTunSourceBind registers the TUN source bind (bindTunSourceForOwner)
// for teardown when it is still mounted in this vmmd's namespace. The bind
// is not journaled. Under systemd (ProtectSystem gives each start its own
// mount namespace) it went with the previous vmmd's namespace; where vmmd
// shares the host's namespace, as in the e2e harness, it outlives the
// previous vmmd, and an unregistered one fails the jail removal.
func (v *JailerVMM) adoptTunSourceBind(instance string) error {
	target := filepath.Join(v.chrootRoot(instance), nativeTunTargetName)
	present, err := hostMountPresent(target)
	if err != nil || !present {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.bindMounts == nil {
		v.bindMounts = make(map[string][]ephemeralBind)
	}
	v.bindMounts[instance] = append(v.bindMounts[instance], ephemeralBind{source: "/dev/net/tun", mountpoint: target})
	return nil
}

// dropAdoptedBinds undoes adoptGuestBinds for a guest whose adoption
// failed: its references go, but no source mode is restored, because the
// mounts still exist in the previous vmmd's namespace and Firecracker's.
func (v *JailerVMM) dropAdoptedBinds(instance string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, b := range v.bindMounts[instance] {
		if !b.adopted {
			continue
		}
		key := bindSourceKey{path: b.source, file: b.file}
		state, ok := v.bindSourceModes[key]
		if !ok {
			continue
		}
		if state.refs > 1 {
			state.refs--
			v.bindSourceModes[key] = state
			continue
		}
		if state.handle != nil {
			_ = state.handle.Close()
		}
		delete(v.bindSourceModes, key)
	}
	delete(v.bindMounts, instance)
}

// mountNamespaceHeld reports whether any process is still in the mount
// namespace with inode ns. Processes exiting during the scan are skipped.
//
// nsfs inode numbers are reused once a namespace is freed (fsn-4, V-A1c,
// 2026-10-17): a successor started after its predecessor died can get the
// dead namespace's number. Two live namespaces never share one, so a match
// with this process's own namespace means the recorded one is gone.
func mountNamespaceHeld(procRoot string, ns uint64) (bool, error) {
	if own, err := statIdentity(filepath.Join(procRoot, "self", "ns", "mnt")); err == nil && own.Inode == ns {
		return false, nil
	}
	entries, err := os.ReadDir(procRoot)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		id, err := statIdentity(filepath.Join(procRoot, e.Name(), "ns", "mnt"))
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err != nil {
			return false, err
		}
		if id.Inode == ns {
			return true, nil
		}
	}
	return false, nil
}
