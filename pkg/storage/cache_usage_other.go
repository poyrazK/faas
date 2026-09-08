//go:build !linux && !darwin

package storage

import "os"

func cacheDiskUsage(info os.FileInfo) int64 { return info.Size() }
