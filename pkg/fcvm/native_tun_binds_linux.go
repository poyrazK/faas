//go:build linux

package fcvm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type linuxNativeTunBinds struct{ base string }

func newNativeTunBindBackend(base string) nativeTunBindBackend {
	return linuxNativeTunBinds{base: base}
}

func (b linuxNativeTunBinds) validateRoot(owner nativeLaunchRecord, root string) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || filepath.Base(root) != "root" || filepath.Base(filepath.Dir(root)) != owner.Lease.Instance || filepath.Dir(filepath.Dir(filepath.Dir(root))) != b.base || !nativeExecutableName(filepath.Base(filepath.Dir(filepath.Dir(root))), "firecracker") {
		return errors.New("native TUN bind: target differs from the original jail")
	}
	return nil
}

type linuxNativeTunPreparation struct {
	source, root *os.File
	device       nativeTunSource
	namespace    nativeLoopIdentity
	owner        nativeLaunchRecord
	rootPath     string
}

func (p *linuxNativeTunPreparation) Source() nativeTunSource       { return p.device }
func (p *linuxNativeTunPreparation) Namespace() nativeLoopIdentity { return p.namespace }
func (p *linuxNativeTunPreparation) Close() error {
	return errors.Join(p.source.Close(), p.root.Close())
}

func nativeTunFileSource(file *os.File) (nativeTunSource, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return nativeTunSource{}, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFCHR || uint64(stat.Rdev) != nativeTunRdev || stat.Mode&0o7000 != 0 || stat.Mode&0o006 != 0o006 {
		return nativeTunSource{}, errors.New("native TUN bind: input is not the accessible host TUN character device")
	}
	return nativeTunSource{Identity: nativeLoopIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}, Rdev: uint64(stat.Rdev), Mode: stat.Mode & 0o777}, nil
}

func (b linuxNativeTunBinds) Prepare(owner nativeLaunchRecord, root string) (prepared nativeTunPreparation, err error) {
	if err := b.validateRoot(owner, root); err != nil {
		return nil, err
	}
	rootFile, err := os.OpenFile(root, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() {
		if prepared == nil {
			err = errors.Join(err, rootFile.Close())
		}
	}()
	var stat unix.Stat_t
	if err := unix.Fstat(int(rootFile.Fd()), &stat); err != nil {
		return nil, err
	}
	// Boot hands the directory to this exact jail UID before carrying TUN in.
	if stat.Uid != uint32(os.Geteuid()) && stat.Uid != uint32(owner.Lease.UID) || stat.Mode&0o022 != 0 {
		return nil, errors.New("native TUN bind: original root ownership changed")
	}
	if _, err := os.Lstat(filepath.Join(root, nativeTunTargetName)); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.Join(err, errors.New("native TUN bind: target already exists"))
	}
	if err := createNativeImageRootMarker(nativeImageFDPath(rootFile), owner); err != nil {
		return nil, err
	}
	// O_PATH pins the device inode without opening a TUN session.
	source, err := os.OpenFile("/dev/net/tun", unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	device, statErr := nativeTunFileSource(source)
	namespace, nsErr := nativeLoopNamespaceIdentity()
	if err := errors.Join(statErr, nsErr); err != nil {
		return nil, errors.Join(err, source.Close())
	}
	return &linuxNativeTunPreparation{source: source, root: rootFile, device: device, namespace: namespace, owner: owner, rootPath: root}, nil
}

func (p *linuxNativeTunPreparation) Bind(publish func(nativeLoopIdentity) error) (uint64, error) {
	device, err := nativeTunFileSource(p.source)
	if err != nil || device != p.device {
		return 0, errors.Join(err, errors.New("native TUN bind: pinned source changed"))
	}
	if err := checkNativeTunNamespace(p.namespace); err != nil {
		return 0, err
	}
	if err := checkNativeImageRoot(p.rootPath, p.owner); err != nil {
		return 0, err
	}
	pinned, err := p.root.Stat()
	if err != nil {
		return 0, err
	}
	actual, err := os.Stat(p.rootPath)
	if err != nil || !os.SameFile(pinned, actual) {
		return 0, errors.Join(err, errors.New("native TUN bind: pinned jail root changed"))
	}
	point := filepath.Join(p.rootPath, nativeTunTargetName)
	identity, err := nativeImagePlaceholder(filepath.Join(nativeImageFDPath(p.root), nativeTunTargetName))
	if err != nil {
		return 0, err
	}
	if err := publish(identity); err != nil {
		return 0, err
	}
	if err := unix.Mount(nativeImageFDPath(p.source), point, "", unix.MS_BIND, ""); err != nil {
		return 0, err
	}
	// TUN must be device-capable and writable, unlike regular image mounts.
	// mount_setattr changes this bind only; no chmod/chown touches host TUN.
	attributes := unix.MountAttr{Attr_set: unix.MOUNT_ATTR_NOSUID | unix.MOUNT_ATTR_NOEXEC, Attr_clr: unix.MOUNT_ATTR_NODEV | unix.MOUNT_ATTR_RDONLY}
	if err := unix.MountSetattr(unix.AT_FDCWD, point, 0, &attributes); err != nil {
		return 0, err
	}
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return 0, err
	}
	id, err := parseNativeTunMountID(data, point)
	if err != nil {
		return 0, err
	}
	return id, checkNativeTunMountFlags(data, point)
}

func nativeTunMountID(point string) (uint64, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return 0, err
	}
	return parseNativeTunMountID(data, point)
}

func checkNativeTunNamespace(expected nativeLoopIdentity) error {
	actual, err := nativeLoopNamespaceIdentity()
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("native TUN bind: original mount namespace is unavailable")
	}
	return nil
}

func nativeTunRootPresent(record nativeTunBindRecord) (bool, error) {
	present, err := nativeImageOriginalRootPresent(nativeImageReference{Owner: record.Owner, Root: record.Root, Removed: record.Removed})
	if err != nil || present {
		return present, err
	}
	if _, err := os.Lstat(record.Root); errors.Is(err, os.ErrNotExist) {
		data, err := os.ReadFile("/proc/self/mountinfo")
		if err != nil {
			return false, err
		}
		mounts, err := nativeMountsBelow(data, record.Root)
		if err != nil || len(mounts) != 0 {
			return false, errors.Join(err, errors.New("native TUN bind: original root disappeared with surviving mounts"))
		}
	} else if err != nil {
		return false, err
	}
	return false, nil
}

func checkNativeTunDevice(point string, expected nativeTunSource) (err error) {
	file, err := os.OpenFile(point, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	actual, statErr := nativeTunFileSource(file)
	if err := errors.Join(statErr, file.Close()); err != nil {
		return err
	}
	if actual != expected {
		return errors.New("native TUN bind: original device identity or permissions changed")
	}
	return nil
}

func (b linuxNativeTunBinds) Check(record nativeTunBindRecord, removed bool) error {
	if err := b.validateRoot(record.Owner, record.Root); err != nil {
		return err
	}
	if err := checkNativeTunNamespace(record.Namespace); err != nil {
		return err
	}
	present, err := nativeTunRootPresent(record)
	if err != nil {
		return err
	}
	if !present {
		if removed {
			return nil
		}
		return errors.New("native TUN bind: live reference lost its original root")
	}
	point := filepath.Join(record.Root, nativeTunTargetName)
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	id, err := parseNativeTunMountID(data, point)
	if err != nil {
		return err
	}
	if removed {
		if _, err := os.Lstat(point); !errors.Is(err, os.ErrNotExist) || id != 0 {
			return errors.Join(err, errors.New("native TUN bind: retired binding reappeared"))
		}
		return nil
	}
	if !record.Ready || record.Removed || id == 0 || id != record.MountID {
		return errors.New("native TUN bind: attachment lacks complete publication")
	}
	if err := checkNativeTunMountFlags(data, point); err != nil {
		return err
	}
	return checkNativeTunDevice(point, record.Source)
}

func (b linuxNativeTunBinds) Retire(record nativeTunBindRecord) error {
	if err := b.validateRoot(record.Owner, record.Root); err != nil {
		return err
	}
	if err := checkNativeTunNamespace(record.Namespace); err != nil {
		return err
	}
	present, err := nativeTunRootPresent(record)
	if err != nil || !present {
		return err
	}
	point := filepath.Join(record.Root, nativeTunTargetName)
	id, err := nativeTunMountID(point)
	if err != nil {
		return err
	}
	if id != 0 {
		if record.Placeholder.Inode == 0 || record.MountID != 0 && id != record.MountID {
			return errors.New("native TUN bind: original mount authority changed")
		}
		if err := checkNativeTunDevice(point, record.Source); err != nil {
			return err
		}
		if err := unix.Unmount(point, 0); err != nil {
			return err
		}
		if id, err := nativeTunMountID(point); err != nil || id != 0 {
			return errors.Join(err, errors.New("native TUN bind: mount survived retirement"))
		}
	}
	return removeNativeImagePlaceholder(point, record.Placeholder)
}

func (b linuxNativeTunBinds) inspectPending(record nativeTunBindRecord) error {
	if err := b.validateRoot(record.Owner, record.Root); err != nil {
		return err
	}
	if err := checkNativeTunNamespace(record.Namespace); err != nil {
		return err
	}
	if record.Removed {
		return b.Check(record, true)
	}
	present, err := nativeTunRootPresent(record)
	if err != nil {
		return err
	}
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	point := filepath.Join(record.Root, nativeTunTargetName)
	id, err := parseNativeTunMountID(data, point)
	if err != nil {
		return err
	}
	if !present {
		if id != 0 {
			return errors.New("native TUN bind: attachment outlived its original root")
		}
		return nil
	}
	if id != 0 {
		if record.Placeholder.Inode == 0 || record.MountID != 0 && id != record.MountID {
			return errors.New("native TUN bind: recovery mount authority changed")
		}
		if err := checkNativeTunDevice(point, record.Source); err != nil {
			return err
		}
		if record.Ready {
			return checkNativeTunMountFlags(data, point)
		}
		return nil
	}
	// Absence can be an interrupted retirement. A remaining placeholder must
	// still be the published inode, or the pre-publication empty private file.
	file, err := os.OpenFile(point, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	identity, metadata, statErr := nativeImageFileMetadata(file)
	info, infoErr := file.Stat()
	if err := errors.Join(statErr, infoErr, file.Close()); err != nil {
		return err
	}
	if record.Placeholder.Inode != 0 && identity != record.Placeholder || record.Placeholder.Inode == 0 && (metadata.UID != uint32(os.Geteuid()) || metadata.Mode != 0o600 || info.Size() != 0) {
		return errors.New("native TUN bind: recovery placeholder changed")
	}
	return nil
}

func (b linuxNativeTunBinds) Inventory(records []nativeTunBindRecord) error {
	known := make(map[string]bool, len(records))
	for _, record := range records {
		if err := b.inspectPending(record); err != nil {
			return err
		}
		known[filepath.Join(record.Root, nativeTunTargetName)] = !record.Removed && record.Placeholder.Inode != 0
	}
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	below, err := nativeMountsBelow(data, b.base)
	if err != nil {
		return err
	}
	for _, point := range below {
		if strings.HasSuffix(point, "/"+nativeTunTargetName) || strings.Contains(point, "/"+nativeTunTargetName+"/") || strings.HasSuffix(point, "/"+nativeTunTargetName+" (deleted)") {
			if !known[point] {
				return fmt.Errorf("native TUN bind: unowned device mount %s", point)
			}
		}
	}
	return nil
}
