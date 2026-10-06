package fcvm

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
)

// A deleted target cannot supply absence proof: its mount still survives in
// the original namespace even though pathname lookup no longer reaches it.
func parseNativeTunMountID(data []byte, point string) (uint64, error) {
	below, err := nativeMountsBelow(data, filepath.Dir(point))
	if err != nil {
		return 0, err
	}
	for _, path := range below {
		if path == point+" (deleted)" || strings.HasPrefix(path, point+" (deleted)/") {
			return 0, errors.New("native TUN bind: original target disappeared with a surviving mount")
		}
	}
	below, err = nativeMountsBelow(data, point)
	if err != nil {
		return 0, err
	}
	if len(below) == 0 {
		return 0, nil
	}
	if len(below) != 1 || below[0] != point {
		return 0, errors.New("native TUN bind: unknown stacked or nested mounts")
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		path, err := decodeNativeMountPath(fields[4])
		if err != nil {
			return 0, err
		}
		if path == point {
			id, err := strconv.ParseUint(fields[0], 10, 64)
			if err != nil || id == 0 {
				return 0, errors.New("native TUN bind: invalid kernel mount ID")
			}
			return id, nil
		}
	}
	return 0, errors.New("native TUN bind: mount identity disappeared")
}

func checkNativeTunMountFlags(data []byte, point string) error {
	id, err := parseNativeTunMountID(data, point)
	if err != nil || id == 0 {
		return errors.Join(err, errors.New("native TUN bind: required mount is absent"))
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		path, err := decodeNativeMountPath(fields[4])
		if err != nil {
			return err
		}
		if path != point {
			continue
		}
		flags := make(map[string]bool)
		for _, flag := range strings.Split(fields[5], ",") {
			flags[flag] = true
		}
		if !flags["rw"] || flags["ro"] || flags["nodev"] || !flags["nosuid"] || !flags["noexec"] {
			return errors.New("native TUN bind: device access flags changed")
		}
		return nil
	}
	return errors.New("native TUN bind: mount disappeared")
}
