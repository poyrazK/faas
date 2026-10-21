//go:build !linux

package fcvm

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

func reflinkFile(_, _ string) error { return errors.New("reflink clone requires Linux") }

func lastUse(info os.FileInfo) time.Time { return info.ModTime() }

// pmemBaseFileIdentity names the base file by device and inode; see the Linux
// variant.
func pmemBaseFileIdentity(base string, info os.FileInfo) (string, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("%s: no file identity", base)
	}
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino), nil
}
