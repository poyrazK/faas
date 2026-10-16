//go:build linux

package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Inspect the kernel lock table without taking a lock, opening for write, or
// creating a guard. Status must not make a concurrent watcher fail acquisition.
func inspectTestCampaignWatchGuard(path string) (string, int) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return "absent", 0
	}
	if err != nil {
		return "unverifiable", 0
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	named, nameErr := os.Lstat(path)
	if err != nil || nameErr != nil || !info.Mode().IsRegular() || !os.SameFile(info, named) {
		return "unverifiable", 0
	}
	marker, err := io.ReadAll(io.LimitReader(file, 128))
	if err != nil || string(marker) != testCampaignRecoveryGuardMagic {
		return "unverifiable", 0
	}
	if _, _, err := testCampaignProcessDomain(); err != nil {
		return "unverifiable", 0
	}
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil {
		return "unverifiable", 0
	}
	locks, err := os.Open("/proc/locks")
	if err != nil {
		return "unverifiable", 0
	}
	defer locks.Close()
	body, err := io.ReadAll(io.LimitReader(locks, 4*1024*1024+1))
	if err != nil || len(body) > 4*1024*1024 {
		return "unverifiable", 0
	}
	state, pid := "available", 0
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 || fields[1] != "FLOCK" || fields[2] != "ADVISORY" {
			continue
		}
		var major, minor, inode uint64
		if n, err := fmt.Sscanf(fields[5], "%x:%x:%d", &major, &minor, &inode); err != nil || n != 3 {
			return "unverifiable", 0
		}
		if major == uint64(unix.Major(stat.Dev)) && minor == uint64(unix.Minor(stat.Dev)) && inode == stat.Ino {
			pid, err = strconv.Atoi(fields[4])
			if err != nil || pid <= 0 {
				return "unverifiable", 0
			}
			state = "held"
			break
		}
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, after) {
		return "unverifiable", 0
	}
	return state, pid
}
