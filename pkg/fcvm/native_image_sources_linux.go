//go:build linux

package fcvm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

type linuxNativeImageSources struct{ base, diskStagingRoot string }

func newNativeImageSourceBackend(base string) nativeImageSourceBackend {
	return linuxNativeImageSources{base: base}
}

type linuxNativeImagePreparation struct {
	source         *os.File
	root           *os.File
	identity       nativeLoopIdentity
	namespace      nativeLoopIdentity
	link           bool
	owner          nativeLaunchRecord
	staging        string // original epoch owns this temporary link before creation
	diskRoot       string
	diskClaim      *nativeDiskImageClaim
	restoreBacking *nativeSnapshotBackingImage
	// Verified restore clones retain their immutable claim after dropping the
	// temporary link. Only their original anchor retirement removes it.
	restoreClone bool
	// Live capture attaches these original disk outputs to a namespace that
	// already exists. Keep their owned link until that joined handoff finishes.
	retainDiskClaim bool
}

func (p *linuxNativeImagePreparation) Identity() nativeLoopIdentity { return p.identity }
func (p *linuxNativeImagePreparation) Metadata() (nativeImageMetadata, error) {
	identity, metadata, err := nativeImageFileMetadata(p.source)
	if err != nil || identity != p.identity {
		return nativeImageMetadata{}, errors.Join(err, errors.New("native image source: pinned source identity changed"))
	}
	return metadata, nil
}
func (p *linuxNativeImagePreparation) Namespace() nativeLoopIdentity { return p.namespace }
func (p *linuxNativeImagePreparation) PreferLink() bool              { return p.link }
func (p *linuxNativeImagePreparation) Close() error {
	var err error
	if p.diskClaim != nil {
		if p.retainDiskClaim {
			err = checkRetainedNativeDiskImageClaim(p.diskRoot, *p.diskClaim)
		} else if p.restoreClone {
			err = finishNativeRestoreDiskImageHandoff(p.diskRoot, *p.diskClaim)
		} else {
			err = retireNativeDiskImageClaim(p.diskRoot, *p.diskClaim)
		}
	} else if p.staging != "" {
		err = removeNativeImageStagingSource(p.staging, p.identity)
	}
	return errors.Join(err, p.source.Close(), p.root.Close())
}

func nativeImageFileMetadata(file *os.File) (nativeLoopIdentity, nativeImageMetadata, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return nativeLoopIdentity{}, nativeImageMetadata{}, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o7000 != 0 {
		return nativeLoopIdentity{}, nativeImageMetadata{}, errors.New("native image source: input is not a regular image without special mode bits")
	}
	return nativeLoopIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}, nativeImageMetadata{Mode: stat.Mode & 0o777, UID: stat.Uid, GID: stat.Gid}, nil
}

func nativeImageFDPath(file *os.File) string {
	return "/proc/self/fd/" + strconv.FormatUint(uint64(file.Fd()), 10)
}

func checkNativeImageRoot(root string, owner nativeLaunchRecord) error {
	marker := filepath.Join(root, nativeImageRootMarker)
	recorded, err := readNativeLaunchRecord(marker, owner.Lease.Instance)
	if err != nil {
		return err
	}
	if recorded.Generation != owner.Generation || recorded.KernelBootID != owner.KernelBootID || !sameNativePhysicalLease(recorded.Lease, owner.Lease) || recorded.Authorized || recorded.Revoked {
		return errors.New("native image source: jail root belongs to another producer")
	}
	return nil
}

func createNativeImageRootMarker(root string, owner nativeLaunchRecord) error {
	marker := filepath.Join(root, nativeImageRootMarker)
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return err
		}
		return checkNativeImageRoot(root, owner)
	}
	// The parent VM lock excludes another image producer or root deletion.
	return writeNativeLaunchRecord(marker, owner)
}

func (b linuxNativeImageSources) Prepare(owner nativeLaunchRecord, root, source, name string, preferLink bool) (prepared nativeImagePreparation, err error) {
	if !filepath.IsAbs(source) || filepath.Clean(source) != source {
		return nil, errors.New("native image source: image placement differs from the original jail")
	}
	rootFile, rootStat, err := b.prepareRoot(owner, root, name)
	if err != nil {
		return nil, err
	}
	defer func() {
		if prepared == nil {
			err = errors.Join(err, rootFile.Close())
		}
	}()
	input, err := os.OpenFile(source, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	identity, _, err := nativeImageFileMetadata(input)
	if err != nil {
		return nil, errors.Join(err, input.Close())
	}
	namespace, err := nativeLoopNamespaceIdentity()
	if err != nil {
		return nil, errors.Join(err, input.Close())
	}
	return &linuxNativeImagePreparation{source: input, root: rootFile, identity: identity, namespace: namespace, link: preferLink && uint64(rootStat.Dev) == identity.Device, owner: owner}, nil
}

func nativeImageRootPlacement(base string, owner nativeLaunchRecord, root, name string) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, "\\\x00") || filepath.Base(filepath.Dir(root)) != owner.Lease.Instance || filepath.Base(root) != "root" || filepath.Dir(filepath.Dir(filepath.Dir(root))) != base || !nativeExecutableName(filepath.Base(filepath.Dir(filepath.Dir(root))), "firecracker") {
		return errors.New("native image source: image placement differs from the original jail")
	}
	return nil
}

func (b linuxNativeImageSources) prepareRoot(owner nativeLaunchRecord, root, name string) (file *os.File, stat unix.Stat_t, err error) {
	if err := nativeImageRootPlacement(b.base, owner, root, name); err != nil {
		return nil, stat, err
	}
	file, err = os.OpenFile(root, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, stat, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, file.Close())
			file = nil
		}
	}()
	if err = unix.Fstat(int(file.Fd()), &stat); err != nil {
		return file, stat, err
	}
	if stat.Uid != uint32(os.Geteuid()) || stat.Mode&0o022 != 0 {
		return file, stat, errors.New("native image source: prepared jail is not controlled by vmmd")
	}
	if _, err := os.Lstat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
		return file, stat, errors.Join(err, errors.New("native image source: target already exists"))
	}
	return file, stat, createNativeImageRootMarker(root, owner)
}

func nativeImagePlaceholder(path string) (nativeLoopIdentity, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nativeLoopIdentity{}, err
	}
	identity, _, statErr := nativeImageFileMetadata(file)
	return identity, errors.Join(statErr, file.Sync(), file.Close(), syncNativeImageParent(path))
}

func syncNativeImageParent(path string) error {
	parent, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(parent.Sync(), parent.Close())
}

func (p *linuxNativeImagePreparation) CreateAnchor(point string, publish func(nativeLoopIdentity) error) (uint64, error) {
	// stageOwned already durably owns this source inode and epoch. Linux
	// cannot attach an unlinked mount root; retain a journal-owned link until
	// both binds have been acknowledged, then remove it in Close.
	if err := p.linkAnonymousSource(point); err != nil {
		return 0, err
	}
	identity, err := nativeImagePlaceholder(point)
	if err != nil {
		return 0, err
	}
	if err := publish(identity); err != nil {
		return 0, err
	}
	if err := unix.Mount(nativeImageFDPath(p.source), point, "", unix.MS_BIND, ""); err != nil {
		return 0, fmt.Errorf("native image source: bind original anchor: %w", err)
	}
	return nativeImageMountID(point)
}

func (p *linuxNativeImagePreparation) CreateReference(ref nativeImageReference, publish func(nativeLoopIdentity) error) (nativeImageReference, error) {
	if err := checkNativeImageRoot(ref.Root, p.owner); err != nil {
		return ref, err
	}
	if ref.Link {
		// vmmd's capability bound excludes CAP_DAC_READ_SEARCH. linkat(2)
		// documents this procfs form for linking a pinned FD without that cap.
		if err := unix.Linkat(unix.AT_FDCWD, nativeImageFDPath(p.source), int(p.root.Fd()), ref.Name, unix.AT_SYMLINK_FOLLOW); err != nil {
			return ref, err
		}
		ref.Target = p.identity
		return ref, p.root.Sync()
	}
	point := filepath.Join(ref.Root, ref.Name)
	identity, err := nativeImagePlaceholder(point)
	if err != nil {
		return ref, err
	}
	ref.Target = identity
	if err := publish(identity); err != nil {
		return ref, err
	}
	if err := unix.Mount(nativeImageFDPath(p.source), point, "", unix.MS_BIND, ""); err != nil {
		return ref, fmt.Errorf("native image source: bind original jail reference: %w", err)
	}
	attributes := uint64(unix.MOUNT_ATTR_NODEV | unix.MOUNT_ATTR_NOSUID | unix.MOUNT_ATTR_NOEXEC)
	if ref.ReadOnly {
		attributes |= unix.MOUNT_ATTR_RDONLY
	}
	// Native mode has no external remount helper or inherited-flag fallback.
	if err := unix.MountSetattr(unix.AT_FDCWD, point, 0, &unix.MountAttr{Attr_set: attributes}); err != nil {
		return ref, err
	}
	ref.MountID, err = nativeImageMountID(point)
	if err != nil || ref.MountID == 0 {
		return ref, errors.Join(err, errors.New("native image source: binding has no kernel mount identity"))
	}
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return ref, err
	}
	return ref, checkNativeImageMountFlags(data, point, ref.ReadOnly)
}

func nativeImageMountID(point string) (uint64, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return 0, err
	}
	return parseNativeImageMountID(data, point)
}

func parseNativeImageMountID(data []byte, point string) (uint64, error) {
	below, err := nativeMountsBelow(data, point)
	if err != nil {
		return 0, err
	}
	if len(below) == 0 {
		return 0, nil
	}
	if len(below) != 1 || below[0] != point {
		return 0, errors.New("native image source: unknown stacked or nested mounts")
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
				return 0, errors.New("native image source: invalid kernel mount ID")
			}
			return id, nil
		}
	}
	return 0, errors.New("native image source: mount identity disappeared")
}

func checkNativeImageMountFlags(data []byte, point string, readOnly bool) error {
	id, err := parseNativeImageMountID(data, point)
	if err != nil || id == 0 {
		return errors.Join(err, errors.New("native image source: required binding mount is absent"))
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
		access := "rw"
		if readOnly {
			access = "ro"
		}
		if !flags[access] || flags["ro"] && flags["rw"] || !flags["nodev"] || !flags["nosuid"] || !flags["noexec"] {
			return errors.New("native image source: binding mount permissions changed")
		}
		return nil
	}
	return errors.New("native image source: binding mount disappeared")
}

func checkNativeImageNamespace(record nativeImageSourceRecord) error {
	identity, err := nativeLoopNamespaceIdentity()
	if err != nil {
		return err
	}
	if identity != record.Namespace {
		return errors.New("native image source: original mount namespace is unavailable")
	}
	return nil
}

func openNativeImageAnchor(record nativeImageSourceRecord, point string) (file *os.File, err error) {
	if err := checkNativeImageNamespace(record); err != nil {
		return nil, err
	}
	id, err := nativeImageMountID(point)
	if err != nil {
		return nil, err
	}
	if id == 0 || record.MountID != 0 && id != record.MountID {
		return nil, errors.New("native image source: original anchor mount changed")
	}
	file, err = os.OpenFile(point, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	identity, _, statErr := nativeImageFileMetadata(file)
	if statErr != nil || identity != record.Identity {
		return nil, errors.Join(statErr, file.Close(), errors.New("native image source: anchor inode changed"))
	}
	return file, nil
}

// OpenSnapshotInput uses the retained anchor, never the original source path
// or the legacy in-memory bind map. The journal caller holds both VM and source
// locks through the complete consumer operation and descriptor close.
func (b linuxNativeImageSources) OpenSnapshotInput(record nativeImageSourceRecord, ref nativeImageReference, point string) (file *os.File, err error) {
	if err := record.validate(ref.Owner.KernelBootID); err != nil {
		return nil, err
	}
	retained := false
	for _, original := range record.References {
		retained = retained || original == ref
	}
	anchor := filepath.Join(b.base, ".native-processes", "image-sources", "points", record.Epoch)
	if !retained || point != anchor || record.Removed || !record.Ready || record.Applied != record.Desired || ref.Removed || !ref.Ready || ref.TargetRemoved || ref.ReadOnly || ref.Link || ref.Name != layerImageName || filepath.Dir(filepath.Dir(filepath.Dir(ref.Root))) != b.base || !nativeExecutableName(filepath.Base(filepath.Dir(filepath.Dir(ref.Root))), "firecracker") {
		return nil, errors.New("native snapshot input: original private writable binding is required")
	}
	if err := b.CheckReference(record, ref); err != nil {
		return nil, err
	}
	file, err = openNativeImageAnchor(record, point)
	if err != nil {
		return nil, err
	}
	_, metadata, statErr := nativeImageFileMetadata(file)
	if err := errors.Join(statErr, b.CheckAnchor(record, point)); err != nil || metadata != record.Applied {
		return nil, errors.Join(err, file.Close(), errors.New("native snapshot input: original source metadata changed"))
	}
	return file, nil
}

func (linuxNativeImageSources) ApplyMetadata(record nativeImageSourceRecord, point string) (err error) {
	file, err := openNativeImageAnchor(record, point)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	_, current, err := nativeImageFileMetadata(file)
	if err != nil {
		return err
	}
	intermediate := record.Applied
	intermediate.UID, intermediate.GID = record.Desired.UID, record.Desired.GID
	if current != record.Applied && current != record.Desired && current != intermediate {
		return errors.New("native image source: source metadata changed outside its declared transition")
	}
	if current.UID != record.Desired.UID || current.GID != record.Desired.GID {
		if err := file.Chown(int(record.Desired.UID), int(record.Desired.GID)); err != nil {
			return err
		}
	}
	if current.Mode != record.Desired.Mode {
		if err := file.Chmod(os.FileMode(record.Desired.Mode)); err != nil {
			return err
		}
	}
	_, actual, err := nativeImageFileMetadata(file)
	if err != nil || actual != record.Desired {
		return errors.Join(err, errors.New("native image source: metadata transition was not confirmed"))
	}
	return file.Sync()
}

func removeNativeImagePlaceholder(point string, identity nativeLoopIdentity) error {
	file, err := os.OpenFile(point, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	actual, metadata, statErr := nativeImageFileMetadata(file)
	info, infoErr := file.Stat()
	if err := errors.Join(statErr, infoErr, file.Close()); err != nil {
		return err
	}
	if identity.Inode != 0 && identity != actual || identity.Inode == 0 && (metadata.UID != uint32(os.Geteuid()) || metadata.Mode != 0o600 || info.Size() != 0) {
		return errors.New("native image source: placeholder was replaced")
	}
	if err := os.Remove(point); err != nil {
		return err
	}
	return syncNativeImageParent(point)
}

func (b linuxNativeImageSources) RetireAnchor(record nativeImageSourceRecord, point string) error {
	if err := checkNativeImageNamespace(record); err != nil {
		return err
	}
	if err := inspectNativeImageStagingSource(record, point); err != nil {
		return err
	}
	claim, err := b.diskStagingClaim(record, point)
	if err != nil {
		return err
	}
	id, err := nativeImageMountID(point)
	if err != nil {
		return err
	}
	if id != 0 {
		file, err := openNativeImageAnchor(record, point)
		if err != nil {
			return err
		}
		_, metadata, statErr := nativeImageFileMetadata(file)
		if err := errors.Join(statErr, file.Close()); err != nil {
			return err
		}
		if metadata != record.Original {
			return errors.New("native image source: last grant did not restore original metadata")
		}
		if err := unix.Unmount(point, 0); err != nil {
			return err
		}
		if id, err := nativeImageMountID(point); err != nil || id != 0 {
			return errors.Join(err, errors.New("native image source: anchor survived unmount"))
		}
	}
	if err := removeNativeImagePlaceholder(point, record.Placeholder); err != nil {
		return err
	}
	if err := removeNativeImageStagingSource(point+nativeImageStagingSuffix, record.Identity); err != nil {
		return err
	}
	if claim != nil {
		return retireNativeDiskImageClaim(b.diskStagingRoot, *claim)
	}
	return nil
}

func (b linuxNativeImageSources) CheckAnchor(record nativeImageSourceRecord, point string) (err error) {
	return b.checkAnchor(record, point, false)
}

// Only the original capture handoff may inspect an anchor whose temporary
// connected dentry is still required by move_mount. Ordinary IO requires its
// retirement. Both modes retain all inode, metadata and namespace checks.
func (b linuxNativeImageSources) checkAnchor(record nativeImageSourceRecord, point string, handoff bool) (err error) {
	claim, err := b.diskStagingClaim(record, point)
	if err != nil {
		return err
	}
	if claim != nil {
		if _, err := os.Lstat(nativeDiskImageSourcePath(b.diskStagingRoot, record.Epoch)); !handoff && !errors.Is(err, os.ErrNotExist) {
			return errors.Join(err, errors.New("native image source: disk staging producer has not retired its temporary link"))
		} else if handoff && err != nil {
			return err
		}
		if record.Removed {
			return errors.New("native image source: retired anchor retains a disk claim")
		}
	} else if handoff {
		return errors.New("native snapshot handoff: original output link claim is unavailable")
	}
	if _, err := os.Lstat(point + nativeImageStagingSuffix); !errors.Is(err, os.ErrNotExist) {
		return errors.Join(err, errors.New("native image source: staging producer has not retired its temporary link"))
	}
	if record.Removed {
		if err := checkNativeImageNamespace(record); err != nil {
			return err
		}
		if id, err := nativeImageMountID(point); err != nil || id != 0 {
			return errors.Join(err, errors.New("native image source: retired anchor reappeared"))
		}
		if _, err := os.Lstat(point); !errors.Is(err, os.ErrNotExist) {
			return errors.Join(err, errors.New("native image source: retired placeholder remains"))
		}
		return nil
	}
	file, err := openNativeImageAnchor(record, point)
	if err != nil {
		return err
	}
	_, metadata, statErr := nativeImageFileMetadata(file)
	if err := errors.Join(statErr, file.Close()); err != nil {
		return err
	}
	if !record.Ready || record.Applied != record.Desired || metadata != record.Applied {
		return errors.New("native image source: anchor has no complete metadata proof")
	}
	return nil
}

func nativeImageOriginalRootPresent(ref nativeImageReference) (bool, error) {
	if _, err := os.Lstat(ref.Root); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	marker, err := readNativeLaunchRecord(filepath.Join(ref.Root, nativeImageRootMarker), ref.Owner.Lease.Instance)
	if err != nil {
		return false, err
	}
	if marker.Generation != ref.Owner.Generation {
		// A later root carries another durable generation. An active old
		// reference may never borrow it; a completed old reference is absent.
		if ref.Removed {
			return false, nil
		}
		return false, errors.New("native image source: original root was replaced before reference retirement")
	}
	return true, checkNativeImageRoot(ref.Root, ref.Owner)
}

func (linuxNativeImageSources) RetireReference(record nativeImageSourceRecord, ref nativeImageReference) error {
	if ref.TargetRemoved {
		ref.Removed = true
		return (linuxNativeImageSources{}).CheckReference(record, ref)
	}
	if err := checkNativeImageNamespace(record); err != nil {
		return err
	}
	present, err := nativeImageOriginalRootPresent(ref)
	if err != nil || !present {
		return err
	}
	point := filepath.Join(ref.Root, ref.Name)
	id, err := nativeImageMountID(point)
	if err != nil {
		return err
	}
	if ref.Link && id != 0 || !ref.Link && id != 0 && ref.MountID != 0 && id != ref.MountID {
		return errors.New("native image source: binding mount identity changed")
	}
	if ref.Link || id != 0 {
		file, err := os.OpenFile(point, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(err, os.ErrNotExist) && ref.Link {
			return nil
		} else if err != nil {
			return err
		}
		identity, _, statErr := nativeImageFileMetadata(file)
		if err := errors.Join(statErr, file.Close()); err != nil {
			return err
		}
		if identity != record.Identity {
			return errors.New("native image source: binding no longer reaches its original inode")
		}
		if ref.Link {
			if err := os.Remove(point); err != nil {
				return err
			}
			return syncNativeImageParent(point)
		}
		if ref.Target.Inode == 0 {
			return errors.New("native image source: mount has no prior placeholder publication")
		}
		if err := unix.Unmount(point, 0); err != nil {
			return err
		}
		if id, err := nativeImageMountID(point); err != nil || id != 0 {
			return errors.Join(err, errors.New("native image source: binding survived unmount"))
		}
	}
	return removeNativeImagePlaceholder(point, ref.Target)
}

func (linuxNativeImageSources) CheckReference(record nativeImageSourceRecord, ref nativeImageReference) error {
	if err := checkNativeImageNamespace(record); err != nil {
		return err
	}
	present, err := nativeImageOriginalRootPresent(ref)
	if err != nil {
		return err
	}
	if !present {
		if !ref.Removed {
			return errors.New("native image source: live reference lost its original root")
		}
		return nil
	}
	point := filepath.Join(ref.Root, ref.Name)
	id, err := nativeImageMountID(point)
	if err != nil {
		return err
	}
	if ref.Removed {
		if _, err := os.Lstat(point); !errors.Is(err, os.ErrNotExist) || id != 0 {
			return errors.Join(err, errors.New("native image source: retired binding reappeared"))
		}
		return nil
	}
	if !ref.Ready || ref.TargetRemoved || ref.Link && id != 0 || !ref.Link && (id == 0 || id != ref.MountID) {
		return errors.New("native image source: binding lacks complete preparation proof")
	}
	if !ref.Link {
		data, err := os.ReadFile("/proc/self/mountinfo")
		if err != nil {
			return err
		}
		if err := checkNativeImageMountFlags(data, point, ref.ReadOnly); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(point, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	identity, _, statErr := nativeImageFileMetadata(file)
	if err := errors.Join(statErr, file.Close()); err != nil {
		return err
	}
	if identity != record.Identity {
		return errors.New("native image source: binding inode changed")
	}
	return nil
}

func (b linuxNativeImageSources) Inventory(root string, records []nativeImageSourceRecord) error {
	if err := b.inventoryDiskStaging(records); err != nil {
		return err
	}
	known := make(map[string]bool)
	for _, record := range records {
		point := filepath.Join(root, "points", record.Epoch)
		if err := inspectNativeImageStagingSource(record, point); err != nil {
			return err
		}
		if record.Removed {
			if err := b.CheckAnchor(record, point); err != nil {
				return err
			}
		} else if err := b.inspectPendingAnchor(record, point); err != nil {
			return err
		}
		known[point] = !record.Removed && record.Placeholder.Inode != 0
		for _, ref := range record.References {
			if err := b.inspectPendingReference(record, ref); err != nil {
				return err
			}
		}
	}
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	below, err := nativeMountsBelow(data, root)
	if err != nil {
		return err
	}
	for _, point := range below {
		if !known[point] {
			return fmt.Errorf("native image source: unowned anchor mount %s", point)
		}
	}
	return nil
}

func (b linuxNativeImageSources) inspectPendingReference(record nativeImageSourceRecord, ref nativeImageReference) (err error) {
	if ref.TargetRemoved {
		ref.Removed = true
		return b.CheckReference(record, ref)
	}
	present, err := nativeImageOriginalRootPresent(ref)
	if err != nil {
		return err
	}
	point := filepath.Join(ref.Root, ref.Name)
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	below, err := nativeMountsBelow(data, ref.Root)
	if err != nil {
		return err
	}
	for _, path := range below {
		if path == point+" (deleted)" {
			return errors.New("native image source: binding mount lost its target path")
		}
	}
	id, err := parseNativeImageMountID(data, point)
	if err != nil {
		return err
	}
	if !present && id != 0 {
		return errors.New("native image source: binding outlived its original jail root")
	}
	if !present {
		return nil
	}
	if id != 0 && (ref.Link || ref.Target.Inode == 0 || ref.MountID != 0 && id != ref.MountID) {
		return errors.New("native image source: recovery binding mount authority changed")
	}
	file, err := os.OpenFile(point, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, os.ErrNotExist) && id == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	identity, metadata, err := nativeImageFileMetadata(file)
	if err != nil {
		return err
	}
	if ref.Link || id != 0 {
		if identity != record.Identity {
			return errors.New("native image source: recovery reference inode changed")
		}
	} else if ref.Target.Inode != 0 {
		if identity != ref.Target {
			return errors.New("native image source: recovery placeholder was replaced")
		}
	} else {
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if metadata.UID != uint32(os.Geteuid()) || metadata.Mode != 0o600 || info.Size() != 0 {
			return errors.New("native image source: unpublished target is not an empty private placeholder")
		}
	}
	if id != 0 && ref.Ready {
		return checkNativeImageMountFlags(data, point, ref.ReadOnly)
	}
	return nil
}

func (linuxNativeImageSources) inspectPendingAnchor(record nativeImageSourceRecord, point string) (err error) {
	if err := checkNativeImageNamespace(record); err != nil {
		return err
	}
	id, err := nativeImageMountID(point)
	if err != nil {
		return err
	}
	if id == 0 {
		if record.Ready {
			allGone := true
			for _, ref := range record.References {
				allGone = allGone && ref.TargetRemoved
			}
			if !allGone || record.Applied != record.Original || record.Desired != record.Original {
				return errors.New("native image source: active anchor disappeared before permission restoration")
			}
		}
		return nil
	}
	if record.Placeholder.Inode == 0 {
		return errors.New("native image source: anchor lacks prior placeholder ownership")
	}
	file, err := openNativeImageAnchor(record, point)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	_, metadata, err := nativeImageFileMetadata(file)
	if err != nil {
		return err
	}
	intermediate := record.Applied
	intermediate.UID, intermediate.GID = record.Desired.UID, record.Desired.GID
	if !record.Ready && metadata != record.Original {
		return errors.New("native image source: unpublished anchor changed original metadata")
	}
	if metadata != record.Applied && metadata != record.Desired && metadata != intermediate {
		return errors.New("native image source: recovery anchor metadata changed outside its published transition")
	}
	return nil
}
