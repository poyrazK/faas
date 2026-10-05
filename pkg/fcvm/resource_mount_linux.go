//go:build linux

// adr: 474
package fcvm

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func resourceMountNamespace() (resourceMountIdentity, error) {
	var identity resourceMountIdentity
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return identity, err
	}
	identity.BootID = strings.TrimSpace(string(boot))
	info, err := os.Stat("/proc/self/ns/mnt")
	if err != nil {
		return identity, err
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !looksLikeInstanceID(identity.BootID) || s.Ino == 0 {
		return identity, errors.New("invalid mount namespace identity")
	}
	identity.Namespace = s.Ino
	return identity, nil
}

func resourceMountAt(path string) (*resourceMountIdentity, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	candidates, err := parseResourceMountCandidates(data, path)
	if err != nil || len(candidates) == 0 {
		return nil, err
	}
	id := candidates[0]
	if len(candidates) > 1 {
		var stat unix.Statx_t
		if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_NO_AUTOMOUNT|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &stat); err != nil {
			return nil, fmt.Errorf("resolve active resource mount at %s: %w", path, err)
		}
		if stat.Mask&unix.STATX_MNT_ID == 0 {
			return nil, fmt.Errorf("resolve active resource mount at %s: mount id unavailable", path)
		}
		id, err = selectResourceMountID(candidates, stat.Mnt_id)
		if err != nil {
			return nil, fmt.Errorf("resolve active resource mount at %s: %w", path, err)
		}
	}
	identity, err := resourceMountNamespace()
	if err != nil {
		return nil, err
	}
	identity.MountID = id
	return &identity, nil
}
