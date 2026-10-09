//go:build linux

package ingress

import (
	"context"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

type linuxNativeEpochReader struct {
	proc    *os.Root
	machine *os.File
	pid     int
}

func captureNativeProcessEpoch(ctx context.Context) (NativeProcessEpoch, error) {
	if ctx.Err() != nil {
		return NativeProcessEpoch{}, ErrNativeStartupUnverified
	}
	var fs unix.Statfs_t
	if unix.Statfs("/proc", &fs) != nil || fs.Type != unix.PROC_SUPER_MAGIC {
		return NativeProcessEpoch{}, ErrNativeStartupUnverified
	}
	for _, path := range []string{"/", "/etc"} {
		var stat unix.Stat_t
		if unix.Lstat(path, &stat) != nil || stat.Uid != 0 || stat.Mode&0o022 != 0 || stat.Mode&unix.S_IFMT != unix.S_IFDIR {
			return NativeProcessEpoch{}, ErrNativeStartupUnverified
		}
	}
	proc, err := os.OpenRoot("/proc")
	if err != nil {
		return NativeProcessEpoch{}, ErrNativeStartupUnverified
	}
	defer func() { _ = proc.Close() }()
	//nolint:forbidigo // Fixed protected host identity path; retained fd metadata checked before and after each read, no customer path.
	machine, err := os.OpenFile("/etc/machine-id", os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return NativeProcessEpoch{}, ErrNativeStartupUnverified
	}
	defer func() { _ = machine.Close() }()
	reader := &linuxNativeEpochReader{proc: proc, machine: machine, pid: os.Getpid()}
	return stableNativeProcessEpoch(ctx, reader)
}

func (r *linuxNativeEpochReader) PID() int { return r.pid }

func (r *linuxNativeEpochReader) Read(ctx context.Context, name string) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, ErrNativeStartupUnverified
	}
	file := r.machine
	if name != "machine-id" {
		if name != "self/stat" && name != "sys/kernel/random/boot_id" {
			return nil, ErrNativeStartupUnverified
		}
		//nolint:forbidigo // Fixed allowlisted procfs metadata under a retained os.Root; no caller-supplied filesystem path.
		opened, err := r.proc.Open(name)
		if err != nil {
			return nil, ErrNativeStartupUnverified
		}
		defer func() { _ = opened.Close() }()
		file = opened
	}
	var before, after unix.Stat_t
	if unix.Fstat(int(file.Fd()), &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || (name == "machine-id" && (before.Uid != 0 || before.Mode&0o022 != 0)) {
		return nil, ErrNativeStartupUnverified
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, ErrNativeStartupUnverified
	}
	raw, err := io.ReadAll(io.LimitReader(file, api.RuntimeUpgradeNativeMetadataMaxBytes+1))
	if err != nil || len(raw) > api.RuntimeUpgradeNativeMetadataMaxBytes || unix.Fstat(int(file.Fd()), &after) != nil || (name == "machine-id" && (before.Dev != after.Dev || before.Ino != after.Ino || before.Mode != after.Mode || before.Uid != after.Uid || before.Gid != after.Gid || before.Size != after.Size || before.Mtim != after.Mtim || before.Ctim != after.Ctim)) || ctx.Err() != nil {
		return nil, ErrNativeStartupUnverified
	}
	return raw, nil
}

func (r *linuxNativeEpochReader) Link(name string) (string, error) {
	switch name {
	case "self/ns/pid", "self/ns/net":
		value, err := r.proc.Readlink(name)
		if err == nil && len(value) <= api.RuntimeUpgradeNativeMetadataMaxBytes {
			return value, nil
		}
	}
	return "", ErrNativeStartupUnverified
}
