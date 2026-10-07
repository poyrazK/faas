//go:build linux

package fcvm

import "golang.org/x/sys/unix"

func snapshotStatStamp(st unix.Stat_t) snapshotInputFileStamp {
	return snapshotInputFileStamp{Device: uint64(st.Dev), Inode: st.Ino, Size: st.Size, Mode: st.Mode, ModifiedSec: st.Mtim.Sec, ModifiedNsec: st.Mtim.Nsec, ChangedSec: st.Ctim.Sec, ChangedNsec: st.Ctim.Nsec}
}
