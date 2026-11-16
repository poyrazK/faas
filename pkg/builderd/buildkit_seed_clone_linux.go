//go:build linux

package builderd

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// cloneFile creates dst as a reflink copy of src (FICLONE), which shares the
// source's blocks until either file writes them. dst must not exist.
func cloneFile(src, dst string) error {
	//nolint:forbidigo // src is a seed builderd itself wrote under its seed directory.
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	//nolint:forbidigo // dst is a builderd-named copy in its own drive directory.
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := unix.IoctlFileClone(int(out.Fd()), int(in.Fd())); err != nil {
		_ = out.Close()
		return fmt.Errorf("reflink copy: %w", err)
	}
	return out.Close()
}
