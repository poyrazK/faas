//go:build linux

// adr: 475
package fcvm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func resourcePlacementContext() (*resourceMountIdentity, error) {
	id, err := resourceMountNamespace()
	return &id, err
}

// A mounted regular-looking marker is insufficient: confirm nsfs and NEWNET.
func resourceNetworkNamespaceAt(name string) (*resourceAsset, error) {
	path := filepath.Join("/run/netns", name)
	if name == "" || filepath.Base(name) != name {
		return nil, errors.New("invalid namespace name")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var fs unix.Statfs_t
	if err := unix.Fstatfs(int(f.Fd()), &fs); err != nil {
		return nil, err
	}
	if fs.Type != unix.NSFS_MAGIC {
		return nil, errors.New("network namespace marker is not nsfs")
	}
	typeID, err := unix.IoctlRetInt(int(f.Fd()), unix.NS_GET_NSTYPE)
	if err != nil || typeID != unix.CLONE_NEWNET {
		return nil, errors.Join(errors.New("marker is not a network namespace"), err)
	}
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || s.Ino == 0 {
		return nil, errors.New("invalid network namespace inode")
	}
	mount, err := resourceMountAt(path)
	if err != nil || mount == nil {
		return nil, errors.Join(errors.New("namespace mount checkpoint missing"), err)
	}
	context := *mount
	context.MountID = 0
	return &resourceAsset{Kind: "netns", Path: path, File: &resourceFileIdentity{Device: uint64(s.Dev), Inode: s.Ino}, Namespace: &context, Mount: mount}, nil
}

func resourceMountTreeClear(path string) error {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	return resourceMountTreeClearInfo(data, path)
}

func resourceMountTreeClearInfo(data []byte, path string) error {
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || !strings.Contains(line, " - ") {
			return errors.New("invalid mount inventory during jail cleanup")
		}
		target := unescape.Replace(fields[4])
		if target == path || strings.HasPrefix(target, path+"/") {
			return fmt.Errorf("jail contains an unretired mount at %s", target)
		}
	}
	return nil
}
