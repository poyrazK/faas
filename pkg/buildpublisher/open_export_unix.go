//go:build unix

package buildpublisher

import (
	"os"
	"syscall"
)

func openExportFile(path string) (*os.File, error) {
	// Completed builder output or its cache lease, never a customer source path.
	// The caller checks the opened descriptor before reading. Nonblocking open
	// refuses special-file hangs; no-follow refuses a swapped final symlink.
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}
