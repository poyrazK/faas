//go:build !linux && !darwin

package storage

import "os"

// fileDataExtents describes the whole file as data where SEEK_DATA is not
// available; the extent encoding stays correct, without the saving.
func fileDataExtents(_ *os.File, size int64) ([]dataExtent, error) {
	if size == 0 {
		return nil, nil
	}
	return []dataExtent{{off: 0, length: size}}, nil
}
