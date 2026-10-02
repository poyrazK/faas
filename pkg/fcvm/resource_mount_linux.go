//go:build linux

// adr: 400
package fcvm

import (
	"errors"
	"os"
	"strings"
	"syscall"
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
	id, exists, err := parseResourceMountInfo(data, path)
	if err != nil || !exists {
		return nil, err
	}
	identity, err := resourceMountNamespace()
	if err != nil {
		return nil, err
	}
	identity.MountID = id
	return &identity, nil
}
