//go:build !linux && !darwin

package storage

import (
	"errors"
	"os"
)

func cacheDiskUsage(info os.FileInfo) int64 { return info.Size() }

func cacheDiskSpace(string) (free, total int64, err error) {
	return 0, 0, errors.New("storage: filesystem space unsupported on this platform")
}
