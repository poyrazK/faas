//go:build linux || darwin

package storage

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// fileDataExtents lists f's data ranges with SEEK_DATA/SEEK_HOLE (ADR-1135).
// A filesystem that does not report holes describes the whole file as data,
// which still encodes correctly, just without the saving.
func fileDataExtents(f *os.File, size int64) ([]dataExtent, error) {
	fd := int(f.Fd())
	var extents []dataExtent
	for off := int64(0); off < size; {
		data, err := unix.Seek(fd, off, unix.SEEK_DATA)
		if errors.Is(err, unix.ENXIO) {
			break // only a hole remains
		}
		if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) {
			return []dataExtent{{off: 0, length: size}}, nil
		}
		if err != nil {
			return nil, err
		}
		hole, err := unix.Seek(fd, data, unix.SEEK_HOLE)
		if err != nil {
			return nil, err
		}
		hole = min(hole, size)
		if hole <= data {
			break
		}
		extents = append(extents, dataExtent{off: data, length: hole - data})
		off = hole
	}
	return extents, nil
}
