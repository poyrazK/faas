//go:build linux || darwin

package runtimequota

// adr: 632
import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func readKeyFile(path string, private bool, bound int) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, ErrTrust
	}
	// Operator paths only; no symlink following, FIFO blocking, or unchecked read.
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("runtime quota: open key material: %w", err)
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return nil, err
	}
	mode := uint32(st.Mode) & 0o7777
	if uint32(st.Mode)&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Size <= 0 || st.Size > int64(bound) ||
		(private && mode != 0o400 && mode != 0o440) || (!private && (mode&^uint32(0o444) != 0 || mode&0o400 == 0)) {
		return nil, ErrTrust
	}
	raw, err := io.ReadAll(io.LimitReader(f, int64(bound)+1))
	if err != nil || len(raw) > bound {
		return nil, ErrInvalid
	}
	return raw, nil
}

func writeKeyFile(path string, raw []byte, mode os.FileMode) error {
	if !filepath.IsAbs(path) {
		return ErrInvalid
	}
	//nolint:forbidigo // operator-selected absolute path, O_EXCL; existing files and links refused.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("runtime quota: create key material: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err = f.Write(raw); err != nil {
		return err
	}
	if err = f.Chmod(mode); err != nil {
		return err
	}
	return f.Sync()
}

func syncKeyDirectory(path string) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	return unix.Fsync(fd)
}
