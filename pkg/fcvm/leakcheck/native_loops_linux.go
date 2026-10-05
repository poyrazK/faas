//go:build linux && metal

package leakcheck

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// LOOP_GET_STATUS64's file_name stores the native protocol's unique marker.
// BACK-FILE from losetup/sysfs instead reports the backing pathname and cannot
// identify an attachment made before mount or one whose file was unlinked.
func nativeLoopLeaks() []error {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return []error{fmt.Errorf("inspect loop inventory: %w", err)}
	}
	var leaks []error
	for _, entry := range entries {
		value, loop := strings.CutPrefix(entry.Name(), "loop")
		if !loop {
			continue
		}
		number, err := strconv.Atoi(value)
		if err != nil || number < 0 || strconv.Itoa(number) != value {
			leaks = append(leaks, fmt.Errorf("invalid kernel loop name %q", entry.Name()))
			continue
		}
		file, err := os.OpenFile(filepath.Join("/dev", entry.Name()), os.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			leaks = append(leaks, fmt.Errorf("inspect loop %s: %w", entry.Name(), err))
			continue
		}
		info, statusErr := unix.IoctlLoopGetStatus64(int(file.Fd()))
		if err := file.Close(); err != nil {
			leaks = append(leaks, err)
		}
		if errors.Is(statusErr, unix.ENXIO) {
			continue
		} else if statusErr != nil {
			leaks = append(leaks, fmt.Errorf("inspect loop status %s: %w", entry.Name(), statusErr))
			continue
		}
		if nativeLoopTokenPresent(info.File_name[:]) {
			leaks = append(leaks, fmt.Errorf("native loop attachment %s", entry.Name()))
		}
	}
	return leaks
}
