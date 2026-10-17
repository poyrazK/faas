package objectnode

import "golang.org/x/sys/unix"

func (s *Store) AvailableBytes() (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(s.root, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
