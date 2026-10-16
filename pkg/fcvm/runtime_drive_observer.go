package fcvm

// adr: 435. Native drive handles must refer to the measured staged inodes.

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func (v *JailerVMM) currentRuntimeDriveProcess(lease Lease) (*exec.Cmd, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	cmd, rec := v.proc[lease.Instance], v.recs[lease.Instance]
	if cmd == nil || cmd.Process == nil || rec == nil || rec.cmd != cmd || rec.exited {
		return nil, runtimeadmission.ErrStale
	}
	return cmd, nil
}

func (v *JailerVMM) observeApprovedRuntimeDrives(ctx context.Context, lease Lease) error {
	handoff, err := v.runtimeDriveHandoff(lease)
	if err != nil || handoff == nil {
		return err
	}
	if lease.UID <= 0 {
		return runtimeadmission.ErrInvalid
	}
	if runtime.GOOS != "linux" {
		return runtimeadmission.ErrUnavailable
	}
	handoff.mu.Lock()
	defer handoff.mu.Unlock()
	if handoff.closed || !runtimeadmission.ValidHash(handoff.observation.ConfigHash) {
		return runtimeadmission.ErrStale
	}
	cmd, err := v.currentRuntimeDriveProcess(lease)
	if err != nil {
		return err
	}
	start, err := observeRuntimeDriveHandles(ctx, "/proc", cmd.Process.Pid, lease.UID, handoff.drives)
	if err != nil {
		return err
	}
	current, err := v.currentRuntimeDriveProcess(lease)
	if err != nil || current != cmd {
		return errors.Join(runtimeadmission.ErrStale, err)
	}
	previous := handoff.observation
	if previous.ProcessPID != 0 && (previous.ProcessPID != cmd.Process.Pid || previous.ProcessStart != start) {
		return runtimeadmission.ErrStale
	}
	handoff.observation.ProcessPID, handoff.observation.ProcessStart = cmd.Process.Pid, start
	handoff.observation.Drives = make([]RuntimeDriveObservation, len(handoff.drives))
	for i, drive := range handoff.drives {
		handoff.observation.Drives[i] = drive.observation
	}
	return ctx.Err()
}

func observeRuntimeDriveHandles(ctx context.Context, procRoot string, pid, uid int, drives []pinnedRuntimeDrive) (string, error) {
	if pid <= 0 || uid < 0 || len(drives) < 2 {
		return "", runtimeadmission.ErrInvalid
	}
	process := filepath.Join(procRoot, strconv.Itoa(pid))
	start, err := runtimeDriveProcessIdentity(ctx, process, uid)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(filepath.Join(process, "fd"))
	if err != nil {
		return "", err
	}
	if len(entries) > 4096 {
		return "", runtimeadmission.ErrInvalid
	}
	seen := make([]bool, len(drives))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if fd, err := strconv.ParseUint(entry.Name(), 10, 32); err != nil || strconv.FormatUint(fd, 10) != entry.Name() {
			return "", runtimeadmission.ErrInvalid
		}
		if err := observeRuntimeDriveHandle(ctx, process, entry.Name(), drives, seen); err != nil {
			return "", err
		}
	}
	for _, found := range seen {
		if !found {
			return "", runtimeadmission.ErrInvalid
		}
	}
	current, err := runtimeDriveProcessIdentity(ctx, process, uid)
	if err != nil || start != current {
		return "", errors.Join(runtimeadmission.ErrStale, err)
	}
	return start, ctx.Err()
}

func observeRuntimeDriveHandle(ctx context.Context, process, fd string, drives []pinnedRuntimeDrive, seen []bool) error {
	info, err := os.Stat(filepath.Join(process, "fd", fd))
	if os.IsNotExist(err) {
		return nil // An unrelated transient descriptor may close during the walk.
	}
	if err != nil {
		return err
	}
	for i, drive := range drives {
		if !os.SameFile(info, drive.info) {
			continue
		}
		if !info.Mode().IsRegular() || info.Size() != drive.observation.Injected.Bytes {
			return runtimeadmission.ErrStale
		}
		readOnly, err := runtimeDriveDescriptorReadOnly(ctx, filepath.Join(process, "fdinfo", fd))
		if err != nil || readOnly != drive.observation.ReadOnly {
			return errors.Join(runtimeadmission.ErrInvalid, err)
		}
		current, err := os.Stat(filepath.Join(process, "fd", fd))
		if err != nil || !os.SameFile(info, current) || !current.Mode().IsRegular() || current.Size() != info.Size() {
			return errors.Join(runtimeadmission.ErrStale, err)
		}
		seen[i] = true
	}
	return nil
}

func runtimeDriveDescriptorReadOnly(ctx context.Context, path string) (bool, error) {
	body, err := readRuntimeDriveProcessRecord(ctx, path)
	if err != nil {
		return false, err
	}
	var modes []uint64
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "flags:" {
			continue
		}
		if len(fields) != 2 {
			return false, runtimeadmission.ErrInvalid
		}
		flags, err := strconv.ParseUint(fields[1], 8, 32)
		// Linux O_PATH descriptors name an inode but cannot perform drive I/O.
		if err != nil || flags&0o10000000 != 0 || flags&3 != 0 && flags&3 != 2 {
			return false, runtimeadmission.ErrInvalid
		}
		modes = append(modes, flags&3)
	}
	if len(modes) != 1 {
		return false, runtimeadmission.ErrInvalid
	}
	return modes[0] == 0, nil
}

func runtimeDriveProcessIdentity(ctx context.Context, process string, uid int) (string, error) {
	status, err := readRuntimeDriveProcessRecord(ctx, filepath.Join(process, "status"))
	if err != nil {
		return "", err
	}
	if err := checkRuntimeDriveProcessUID(status, uid); err != nil {
		return "", err
	}
	stat, err := readRuntimeDriveProcessRecord(ctx, filepath.Join(process, "stat"))
	if err != nil {
		return "", err
	}
	index := strings.LastIndex(string(stat), ")")
	pid, _, found := strings.Cut(string(stat), " ")
	if index < 0 || !found || pid != filepath.Base(process) {
		return "", runtimeadmission.ErrInvalid
	}
	fields := strings.Fields(string(stat[index+1:]))
	if len(fields) < 20 || fields[0] == "Z" || fields[0] == "X" {
		return "", runtimeadmission.ErrStale
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || start == 0 {
		return "", runtimeadmission.ErrInvalid
	}
	return strconv.FormatUint(start, 10), ctx.Err()
}

func checkRuntimeDriveProcessUID(status []byte, uid int) error {
	found := false
	for _, line := range strings.Split(string(status), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "Uid:" {
			continue
		}
		if found || len(fields) != 5 {
			return runtimeadmission.ErrInvalid
		}
		for _, value := range fields[1:] {
			if value != strconv.Itoa(uid) {
				return runtimeadmission.ErrStale
			}
		}
		found = true
	}
	if !found {
		return runtimeadmission.ErrInvalid
	}
	return nil
}

func readRuntimeDriveProcessRecord(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(file, 65537))
	err = errors.Join(err, file.Close(), ctx.Err())
	if len(body) > 65536 {
		err = errors.Join(err, runtimeadmission.ErrInvalid)
	}
	return body, err
}
