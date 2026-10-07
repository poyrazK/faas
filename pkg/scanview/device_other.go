//go:build !linux && !darwin

package scanview

import (
	"io/fs"
	"os"
)

func sameDirectoryDevice(_, _ fs.FileInfo) bool          { return false }
func openRegular(_ *os.Root, _ string) (*os.File, error) { return nil, ErrInvalid }
