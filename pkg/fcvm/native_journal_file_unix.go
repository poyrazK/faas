//go:build linux || darwin

package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func checkNativeJournalPath(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm()&0o077 != 0 || directory && !info.IsDir() || !directory && !info.Mode().IsRegular() {
		return fmt.Errorf("native journal: %s is not a private owned path", path)
	}
	return nil
}

func checkNativeEffectDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() || !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("native lifecycle: %s is not a protected owned directory", path)
	}
	return nil
}

func openNativeJournalFile(path string, flags int) (*os.File, error) {
	return os.OpenFile(path, flags|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
}

func lockNativeJournalFile(ctx context.Context, path string) (*os.File, error) {
	return lockNativeJournalFileMode(ctx, path, false)
}

func lockNativeJournalFileShared(ctx context.Context, path string) (*os.File, error) {
	return lockNativeJournalFileMode(ctx, path, true)
}

func lockNativeJournalFileMode(ctx context.Context, path string, shared bool) (*os.File, error) {
	file, err := openNativeJournalFile(path, os.O_CREATE|os.O_RDWR)
	if err != nil {
		return nil, err
	}
	if err := checkNativeJournalPath(path, false); err != nil {
		_ = file.Close()
		return nil, err
	}
	mode := unix.LOCK_EX
	if shared {
		mode = unix.LOCK_SH
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, err
		}
		if err := unix.Flock(int(file.Fd()), mode|unix.LOCK_NB); err == nil {
			opened, statErr := file.Stat()
			current, pathErr := os.Lstat(path)
			if statErr != nil || pathErr != nil || !os.SameFile(opened, current) {
				_ = file.Close()
				return nil, errors.Join(statErr, pathErr, errors.New("native journal: lock path changed while acquiring ownership"))
			}
			return file, nil
		} else if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EINTR) {
			_ = file.Close()
			return nil, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
