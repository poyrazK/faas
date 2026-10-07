//go:build linux || darwin

package fcvm

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

type snapshotInputFileStamp struct {
	Device, Inode                                      uint64
	Size                                               int64
	Mode                                               uint32
	ModifiedSec, ModifiedNsec, ChangedSec, ChangedNsec int64
}

func snapshotFileStamp(f *os.File) (snapshotInputFileStamp, error) {
	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		return snapshotInputFileStamp{}, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Size <= 0 {
		return snapshotInputFileStamp{}, errors.New("snapshot input: nonempty regular file required")
	}
	return snapshotStatStamp(st), nil
}
