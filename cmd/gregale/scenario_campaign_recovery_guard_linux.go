//go:build linux

package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const testCampaignRecoveryGuardMagic = "gregale-recovery-flock-v1\n"

// Keep the guard inode. Old binaries using O_EXCL cannot enter recovery while
// this persistent path exists, and the kernel releases flock on process death.
// Publish the marker atomically so death during guard creation cannot leave an
// indistinguishable empty legacy guard behind.
func acquireTestCampaignRecoveryGuard(path string) (func(), error) {
	var file *os.File
	for attempt := 0; attempt < 2; attempt++ {
		fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
		if err == nil {
			file = os.NewFile(uintptr(fd), path)
			if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
				_ = file.Close()
				return nil, errors.New("another recovery process holds the guard")
			}
			break
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		temporary, err := os.CreateTemp(filepath.Dir(path), ".gregale-recovery-guard-")
		if err != nil {
			return nil, err
		}
		name := temporary.Name()
		publishErr := func() error {
			if err := syscall.Flock(int(temporary.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
				return err
			}
			if _, err := temporary.WriteString(testCampaignRecoveryGuardMagic); err != nil {
				return err
			}
			if err := temporary.Sync(); err != nil {
				return err
			}
			return os.Link(name, path)
		}()
		_ = os.Remove(name)
		if publishErr == nil {
			file = temporary
			break
		}
		_ = temporary.Close()
		if !os.IsExist(publishErr) {
			return nil, publishErr
		}
	}
	if file == nil {
		return nil, errors.New("recovery guard changed during acquisition")
	}
	fail := func(err error) (func(), error) { _ = file.Close(); return nil, err }
	info, err := file.Stat()
	named, nameErr := os.Lstat(path)
	if err != nil || nameErr != nil || !info.Mode().IsRegular() || !os.SameFile(info, named) {
		return fail(errors.New("recovery guard inode changed"))
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fail(err)
	}
	marker, err := io.ReadAll(io.LimitReader(file, 128))
	if err != nil || string(marker) != testCampaignRecoveryGuardMagic {
		return fail(errors.New("legacy recovery guard has no verifiable kernel-lock protocol"))
	}
	if err := testCampaignSyncDirectory(filepath.Dir(path)); err != nil {
		return fail(err)
	}
	return func() { _ = file.Close() }, nil
}
