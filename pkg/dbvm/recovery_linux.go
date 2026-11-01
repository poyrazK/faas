//go:build linux

package dbvm

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// consoleFIFO is the named pipe Firecracker writes its console to. A pipe
// from vmmd's own process would close with vmmd; the FIFO stays, and the
// next vmmd reads it again. While no vmmd reads it, Firecracker's writes
// fail (it ignores SIGPIPE) and that output is lost.
const consoleFIFO = "console.fifo"

// startConsole makes the VM's console FIFO and starts copying it into the
// capped console log. It returns the write end for Firecracker, which the
// caller closes once Firecracker holds it.
func (r *Runtime) startConsole(vm *databaseVM) (*os.File, error) {
	path := filepath.Join(vm.dir, consoleFIFO)
	_ = os.Remove(path)
	if err := unix.Mkfifo(path, 0o600); err != nil {
		return nil, fmt.Errorf("dbvm: console fifo: %w", err)
	}
	copyConsole, err := r.openConsole(vm, false)
	if err != nil {
		return nil, err
	}
	// The read end is open, so this does not block; the copy starts once a
	// writer exists, so its end of file means every writer is gone.
	w, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		_ = vm.console.Close()
		return nil, fmt.Errorf("dbvm: console fifo: %w", err)
	}
	copyConsole()
	return w, nil
}

// openConsole opens the console FIFO's read end and console.log, and
// returns what copies one into the other until every writer is gone. A
// new boot starts the log over; an adopted VM appends to it.
func (r *Runtime) openConsole(vm *databaseVM, appendLog bool) (func(), error) {
	fifo, err := os.OpenFile(filepath.Join(vm.dir, consoleFIFO), os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("dbvm: console fifo: %w", err)
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if appendLog {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	console, err := os.OpenFile(filepath.Join(vm.dir, "console.log"), flags, 0o600)
	if err != nil {
		_ = fifo.Close()
		return nil, fmt.Errorf("dbvm: console log: %w", err)
	}
	capped := &consoleLog{f: console, max: consoleMaxBytes}
	if info, err := console.Stat(); err == nil {
		capped.written = info.Size()
	}
	vm.console = fifo
	return func() {
		go func() {
			_, _ = io.Copy(capped, fifo)
			_ = fifo.Close()
			_ = console.Close()
		}()
	}, nil
}

// watchExit closes exited when the process behind pidfd exits; vmmd is
// not its parent after a restart, so it cannot wait for it.
func watchExit(pidfd int, exited chan struct{}) {
	defer close(exited)
	defer func() { _ = unix.Close(pidfd) }()
	fds := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}} //nolint:gosec // an fd
	for {
		n, err := unix.Poll(fds, -1)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil || n > 0 {
			return
		}
	}
}

// adoptJail takes over the VM running in jail jailID at dir, as its
// record describes. It returns the database id and the VM, or an error
// when there is nothing to adopt and the jail should be cleaned up.
func (r *Runtime) adoptJail(jailID, dir string) (string, *databaseVM, error) {
	rec, err := readRecord(dir)
	if err != nil {
		return "", nil, err
	}
	if rec.Spare {
		return "", nil, errors.New("a spare no database adopted")
	}
	pids := jailProcesses(r.cfg.ParentCgroup, jailID)
	if len(pids) != 1 {
		return "", nil, fmt.Errorf("%d Firecracker processes", len(pids))
	}
	pid := pids[0]
	pidfd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return "", nil, fmt.Errorf("pidfd: %w", err)
	}
	// The pidfd pins the process: check it is still the jail's.
	uid, err := processUID(pid)
	if err != nil || !cmdlineIsJail(pid, jailID) {
		_ = unix.Close(pidfd)
		return "", nil, errors.New("the process is gone")
	}
	lease, err := r.cfg.Leases.Adopt(jailID, rec.Slot)
	if err != nil {
		_ = unix.Close(pidfd)
		return "", nil, err
	}
	if lease.UID != uid {
		_ = r.cfg.Leases.Release(jailID)
		_ = unix.Close(pidfd)
		return "", nil, fmt.Errorf("process uid %d, slot %d uid %d", uid, rec.Slot, lease.UID)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = r.cfg.Leases.Release(jailID)
		_ = unix.Close(pidfd)
		return "", nil, err
	}
	vm := &databaseVM{
		jailID: jailID, lease: lease, dir: dir, root: filepath.Join(dir, "root"), exited: make(chan struct{}),
		proc: proc, volume: rec.Volume, spareDir: rec.SpareDir, base: rec.Base, record: rec,
		binds: mountsUnder(dir),
	}
	go watchExit(pidfd, vm.exited)
	if copyConsole, err := r.openConsole(vm, true); err != nil {
		r.logWarn("dbvm: adopted VM without its console", "database_id", rec.DatabaseID, "err", err)
	} else {
		copyConsole()
	}
	boot := BootRequest{ID: rec.DatabaseID, RestoreSource: rec.RestoreSource, RestoreWindowSeconds: rec.RestoreWindowSeconds}
	if err := r.startArchiveRelay(vm, rec.DatabaseID, boot); err != nil {
		return rec.DatabaseID, vm, err
	}
	if rec.PrimaryNode != "" {
		if err := r.startReplicationRelay(vm, r.lateReplicationDialer(rec.DatabaseID, rec.PrimaryNode, rec.PrimaryTarget)); err != nil {
			return rec.DatabaseID, vm, err
		}
	}
	return rec.DatabaseID, vm, nil
}

// processUID reads a process's real uid.
func processUID(pid int) (int, error) {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return 0, err
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		if rest, ok := strings.CutPrefix(scanner.Text(), "Uid:"); ok {
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				return strconv.Atoi(fields[0])
			}
		}
	}
	return 0, errors.New("no uid")
}

func cmdlineIsJail(pid int, jailID string) bool {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	return err == nil && cmdlineHasJailID(raw, jailID)
}
