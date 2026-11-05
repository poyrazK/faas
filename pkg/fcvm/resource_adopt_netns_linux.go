//go:build linux

package fcvm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// anchorAdoptedNetns makes /run/netns/<name> in this vmmd's mount namespace
// name the adopted guest's network namespace (adoption ADR §2 "Network
// namespace"). The previous vmmd's binding lived in its own mount namespace,
// so the successor may see only the empty mountpoint. Every later
// `ip netns exec` and the bridges need the binding. The namespace is the one
// the verified Firecracker process is in, and it must be the recorded one.
func anchorAdoptedNetns(name string, pid int, want resourceFileIdentity) (*resourceAsset, error) {
	if current, err := resourceNetworkNamespaceAt(name); err == nil && current != nil {
		if *current.File != want {
			return nil, errors.New("network namespace binding names another namespace")
		}
		return current, nil
	}
	source := filepath.Join("/proc", strconv.Itoa(pid), "ns", "net")
	got, err := statIdentity(source)
	if err != nil {
		return nil, err
	}
	if got != want {
		return nil, errors.New("guest is not in the recorded network namespace")
	}
	path := filepath.Join("/run/netns", name)
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDONLY|syscall.O_NOFOLLOW, 0o444)
		if err != nil {
			return nil, fmt.Errorf("create namespace mountpoint: %w", err)
		}
		_ = f.Close()
	case err != nil:
		return nil, err
	case !info.Mode().IsRegular() || info.Size() != 0:
		return nil, errors.New("namespace mountpoint is not an empty regular file")
	}
	if err := unix.Mount(source, path, "", unix.MS_BIND, ""); err != nil {
		return nil, fmt.Errorf("bind network namespace: %w", err)
	}
	anchored, err := resourceNetworkNamespaceAt(name)
	if err != nil || anchored == nil || *anchored.File != want {
		_ = unix.Unmount(path, unix.MNT_DETACH)
		return nil, errors.Join(errors.New("re-anchored namespace binding did not verify"), err)
	}
	return anchored, nil
}
