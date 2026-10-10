//go:build linux

// adr: 568 — disk names require persistent cleanup authority before linkat.
package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"

	"golang.org/x/sys/unix"
)

// Source is the immutable, exclusive pre-anchor intent. Unlike the tmpfs
// launch journal, this claim survives reboot. It never grants launch, lease,
// permission-transition, output publication or capture authority.
type nativeDiskImageClaim struct {
	Version   int                         `json:"version"`
	Directory nativeLoopIdentity          `json:"directory"`
	JailBase  string                      `json:"jail_base"`
	Anchor    string                      `json:"anchor"`
	Source    nativeImageSourceRecord     `json:"source"`
	Backing   *nativeSnapshotBackingImage `json:"backing,omitempty"`
}

func (c *nativeDiskImageClaim) UnmarshalJSON(data []byte) error {
	var profile struct {
		Version int             `json:"version"`
		Backing json.RawMessage `json:"backing"`
	}
	if err := json.Unmarshal(data, &profile); err != nil {
		return err
	}
	names := []string{"version", "directory", "jail_base", "anchor", "source"}
	if profile.Version == 2 || profile.Version == 3 && len(profile.Backing) != 0 {
		names = append(names, "backing")
	}
	if profile.Version == 3 && bytes.Equal(bytes.TrimSpace(profile.Backing), []byte("null")) {
		return errors.New("native disk staging: null restore backing witness")
	}
	fields, err := nativeJournalObjectFields(data, names)
	if err != nil {
		return err
	}
	if _, err := nativeJournalObjectFields(fields["directory"], []string{"device", "inode"}); err != nil {
		return err
	}
	type plain nativeDiskImageClaim
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode((*plain)(c))
}

func nativeDiskImageClaimPath(root, epoch string) string { return filepath.Join(root, epoch+".json") }
func nativeDiskImageSourcePath(root, epoch string) string {
	return filepath.Join(root, epoch+nativeImageStagingSuffix)
}

func (b linuxNativeImageSources) withDiskStagingRoot(root string) nativeImageSourceBackend {
	b.diskStagingRoot = root
	return b
}

func (b linuxNativeImageSources) DiskStagingRequired() bool { return b.diskStagingRoot != "" }

func nativeDiskImageRootIdentity(root string) (nativeLoopIdentity, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" {
		return nativeLoopIdentity{}, errors.New("native disk staging: original absolute private root is required")
	}
	// Check every component; O_NOFOLLOW on the leaf alone does not reject
	// redirection through a parent symlink.
	for path := root; path != "/"; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() {
			return nativeLoopIdentity{}, errors.Join(err, errors.New("native disk staging: root traverses a missing directory or symlink"))
		}
	}
	if err := checkNativeJournalPath(root, true); err != nil {
		return nativeLoopIdentity{}, err
	}
	file, err := os.OpenFile(root, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nativeLoopIdentity{}, err
	}
	var stat unix.Stat_t
	var filesystem unix.Statfs_t
	if err := errors.Join(unix.Fstat(int(file.Fd()), &stat), unix.Fstatfs(int(file.Fd()), &filesystem), file.Close()); err != nil {
		return nativeLoopIdentity{}, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o077 != 0 || stat.Uid != uint32(os.Geteuid()) {
		return nativeLoopIdentity{}, errors.New("native disk staging: opened root lost its private owner")
	}
	if !nativeCloneFilesystemSupported(filesystem.Type) {
		return nativeLoopIdentity{}, errors.New("native disk staging: persistent ext4, XFS or Btrfs disk is required")
	}
	return nativeLoopIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}, nil
}

func (b linuxNativeImageSources) LockDiskStaging(ctx context.Context) (*os.File, error) {
	if b.diskStagingRoot == "" {
		return nil, nil
	}
	if _, err := nativeDiskImageRootIdentity(b.diskStagingRoot); err != nil {
		return nil, err
	}
	if b.diskStagingRoot == b.base || strings.HasPrefix(b.diskStagingRoot, b.base+string(os.PathSeparator)) {
		return nil, errors.New("native disk staging: persistent root must be outside the host-lifetime jail")
	}
	return lockNativeJournalFile(ctx, filepath.Join(b.diskStagingRoot, ".daemon-owner.lock"))
}

func validateNativeDiskImageClaimRoot(root string, claim nativeDiskImageClaim) error {
	identity, err := nativeDiskImageRootIdentity(root)
	if err != nil {
		return err
	}
	r := claim.Source
	if err := r.validate(r.KernelBoot); err != nil {
		return err
	}
	if claim.Version != 1 && claim.Version != 2 && claim.Version != 3 || claim.Version == 1 && claim.Backing != nil || !canonicalNativeHelperID(r.KernelBoot) || identity != claim.Directory || identity.Device != r.Identity.Device || !filepath.IsAbs(claim.JailBase) || filepath.Clean(claim.JailBase) != claim.JailBase || claim.JailBase == "/" || claim.Anchor != filepath.Join(claim.JailBase, ".native-processes", "image-sources", "points", r.Epoch) || len(r.References) != 1 || r.Ready || r.Removed || r.Placeholder != (nativeLoopIdentity{}) || r.MountID != 0 {
		return errors.New("native disk staging: persistent claim identity or initial intent changed")
	}
	ref := r.References[0]
	if ref.Link || ref.Ready || ref.Removed || ref.TargetRemoved || ref.Target != (nativeLoopIdentity{}) || ref.MountID != 0 || filepath.Dir(filepath.Dir(filepath.Dir(ref.Root))) != claim.JailBase || !nativeExecutableName(filepath.Base(filepath.Dir(filepath.Dir(ref.Root))), "firecracker") {
		return errors.New("native disk staging: claim does not own one original anonymous private source")
	}
	backing := false
	if claim.Version == 2 || claim.Backing != nil {
		image := claim.Backing
		if image == nil || image.validate() != nil || !ref.ReadOnly || image.Name != ref.Name || image.Epoch == r.Epoch || image.ReferenceID == ref.ID || image.Identity == r.Identity {
			return errors.New("native disk staging: backing clone lost its distinct captured image evidence")
		}
		backing = true
	}
	if claim.Version == 3 && !backing && (!nativeRestoreImageName(ref.Name) || ref.ReadOnly != (ref.Name != layerImageName)) {
		return errors.New("native disk staging: restore claim lost its fixed input profile")
	}
	// v1 retains its fixed receipt-input names. v2 permits only an explicitly
	// verified captured backing name; both profiles remain exclusive clones.
	if ref.ReadOnly && (!backing && ref.Name != memSnapshotName && ref.Name != vmstateSnapshotName || ref.AddPerms != 0o044 || r.Original.Mode != 0o600 || r.Original.UID != uint32(os.Geteuid())) {
		return errors.New("native disk staging: read-only claim is not one private snapshot input clone")
	}
	return nil
}

func readNativeDiskImageClaim(root, epoch string) (claim nativeDiskImageClaim, err error) {
	path := nativeDiskImageClaimPath(root, epoch)
	if err := checkNativeJournalPath(path, false); err != nil {
		return claim, err
	}
	file, err := openNativeJournalFile(path, os.O_RDONLY)
	if err != nil {
		return claim, err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o077 != 0 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || stat.Size <= 0 || stat.Size > api.NativeSnapshotPublicationRecordMaxBytes {
		return claim, errors.Join(err, file.Close(), errors.New("native disk staging: claim must be one bounded private regular file"))
	}
	decoder := json.NewDecoder(file)
	decodeErr := decoder.Decode(&claim)
	if decodeErr == nil {
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			decodeErr = errors.New("native disk staging: trailing claim data")
		}
	}
	if err := errors.Join(decodeErr, file.Close()); err != nil {
		return claim, err
	}
	if claim.Source.Epoch != epoch {
		return claim, errors.New("native disk staging: claim filename changed original epoch")
	}
	return claim, validateNativeDiskImageClaimRoot(root, claim)
}

func (p *linuxNativeImagePreparation) OwnAnonymousSource(record nativeImageSourceRecord, point string) error {
	if p.diskRoot == "" {
		return nil
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(p.source.Fd()), &stat); err != nil {
		return err
	}
	if stat.Nlink != 0 {
		return nil // Named immutable inputs do not need a disk claim.
	}
	identity, err := nativeDiskImageRootIdentity(p.diskRoot)
	if err != nil {
		return err
	}
	// The live image journal subsequently updates Target/MountID/Ready in
	// this slice. Retain the immutable pre-anchor claim by value, including
	// its references, rather than sharing that mutable backing array.
	record.References = append([]nativeImageReference(nil), record.References...)
	claim := nativeDiskImageClaim{Version: 1, Directory: identity, JailBase: filepath.Dir(filepath.Dir(filepath.Dir(p.root.Name()))), Anchor: point, Source: record}
	if p.restoreBacking != nil {
		image := *p.restoreBacking
		claim.Version, claim.Backing = 2, &image
	}
	if p.restoreClone {
		claim.Version = 3
	}
	if err := validateNativeDiskImageClaimRoot(p.diskRoot, claim); err != nil {
		return err
	}
	if record.Identity != p.identity || record.Namespace != p.namespace || record.References[0].Owner != p.owner || p.diskClaim != nil {
		return errors.New("native disk staging: original producer differs from disk claim")
	}
	for _, path := range []string{nativeDiskImageClaimPath(p.diskRoot, record.Epoch), nativeDiskImageSourcePath(p.diskRoot, record.Epoch)} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.Join(err, errors.New("native disk staging: original epoch path is already occupied"))
		}
	}
	// Record this even on fsync failure: no link may follow a failed write,
	// and Close must validate and retire any successfully renamed claim.
	p.diskClaim = &claim
	return writeNativeJournalValue(nativeDiskImageClaimPath(p.diskRoot, record.Epoch), claim)
}

func sameNativeDiskImageSource(claim nativeDiskImageClaim, current nativeImageSourceRecord) bool {
	original := claim.Source
	if original.Epoch != current.Epoch || original.KernelBoot != current.KernelBoot || original.Namespace != current.Namespace || original.Identity != current.Identity || original.Original != current.Original || len(current.References) != 1 {
		return false
	}
	a, b := original.References[0], current.References[0]
	return a.ID == b.ID && a.Owner == b.Owner && a.Root == b.Root && a.Name == b.Name && a.ReadOnly == b.ReadOnly && a.Link == b.Link && a.AddPerms == b.AddPerms
}

func inspectNativeDiskImageSource(root string, claim nativeDiskImageClaim) (err error) {
	return inspectNativeDiskImageSourceRecord(root, claim, nil)
}

func inspectNativeDiskImageSourceRecord(root string, claim nativeDiskImageClaim, current *nativeImageSourceRecord) (err error) {
	if err := validateNativeDiskImageClaimRoot(root, claim); err != nil {
		return err
	}
	file, err := os.OpenFile(nativeDiskImageSourcePath(root, claim.Source.Epoch), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	identity, metadata, err := nativeImageFileMetadata(file)
	var stat unix.Stat_t
	if err := errors.Join(err, unix.Fstat(int(file.Fd()), &stat)); err != nil {
		return err
	}
	r := claim.Source
	if current != nil {
		r = *current
	}
	intermediate := r.Applied
	intermediate.UID, intermediate.GID = r.Desired.UID, r.Desired.GID
	if identity != r.Identity || stat.Nlink != 1 || current != nil && (r.Removed || !r.Ready && metadata != r.Original) || metadata != r.Applied && metadata != r.Desired && metadata != intermediate {
		return errors.New("native disk staging: temporary source was replaced, aliased or changed outside its original transition")
	}
	return nil
}

func checkRetainedNativeDiskImageClaim(root string, expected nativeDiskImageClaim) error {
	claim, err := readNativeDiskImageClaim(root, expected.Source.Epoch)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(claim, expected) {
		return errors.New("native disk staging: persistent claim changed before handoff")
	}
	return inspectNativeDiskImageSource(root, claim)
}

// The original staging capability can drop its exact temporary dentry while
// retaining immutable descriptor custody. This grants no replay or load permit.
func finishNativeRestoreDiskImageHandoff(root string, expected nativeDiskImageClaim) error {
	if expected.Version != 3 {
		return errors.New("native disk staging: retained restore custody requires its original profile")
	}
	if err := checkRetainedNativeDiskImageClaim(root, expected); err != nil {
		return err
	}
	if err := removeNativeImageStagingSource(nativeDiskImageSourcePath(root, expected.Source.Epoch), expected.Source.Identity); err != nil {
		return err
	}
	return checkRetainedNativeDiskImageClaim(root, expected)
}

func retireNativeDiskImageClaim(root string, expected nativeDiskImageClaim) error {
	claim, err := readNativeDiskImageClaim(root, expected.Source.Epoch)
	if errors.Is(err, os.ErrNotExist) {
		if _, err := os.Lstat(nativeDiskImageSourcePath(root, expected.Source.Epoch)); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return errors.New("native disk staging: source lost its persistent claim")
	} else if err != nil {
		return err
	}
	if !reflect.DeepEqual(claim, expected) {
		return errors.New("native disk staging: persistent claim changed during retirement")
	}
	if err := inspectNativeDiskImageSource(root, claim); err != nil {
		return err
	}
	if err := removeNativeImageStagingSource(nativeDiskImageSourcePath(root, claim.Source.Epoch), claim.Source.Identity); err != nil {
		return err
	}
	path := nativeDiskImageClaimPath(root, claim.Source.Epoch)
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncNativeImageParent(path)
}

func (b linuxNativeImageSources) diskStagingClaim(record nativeImageSourceRecord, point string) (*nativeDiskImageClaim, error) {
	if b.diskStagingRoot == "" {
		return nil, nil
	}
	claim, err := readNativeDiskImageClaim(b.diskStagingRoot, record.Epoch)
	if errors.Is(err, os.ErrNotExist) {
		if _, err := os.Lstat(nativeDiskImageSourcePath(b.diskStagingRoot, record.Epoch)); errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, errors.New("native disk staging: source has no durable claim")
	} else if err != nil {
		return nil, err
	}
	if claim.JailBase != b.base || claim.Anchor != point || !sameNativeDiskImageSource(claim, record) {
		return nil, errors.New("native disk staging: original host-lifetime image authority changed")
	}
	if err := inspectNativeDiskImageSourceRecord(b.diskStagingRoot, claim, &record); err != nil {
		return nil, err
	}
	return &claim, nil
}

// Startup holds both daemon ownership locks. Validate the full directory before
// changing it. Same-boot missing image authority is quarantined; only a different
// kernel boot proves every original process and bind has ceased to exist.
func (b linuxNativeImageSources) inventoryDiskStaging(records []nativeImageSourceRecord) error {
	if b.diskStagingRoot == "" {
		return nil
	}
	if _, err := nativeDiskImageRootIdentity(b.diskStagingRoot); err != nil {
		return err
	}
	boot, err := nativeKernelBootID()
	if err != nil || !canonicalNativeHelperID(boot) {
		return errors.Join(err, errors.New("native disk staging: current kernel boot is unavailable"))
	}
	entries, err := os.ReadDir(b.diskStagingRoot)
	if err != nil {
		return err
	}
	known := make(map[string]bool)
	var retire []nativeDiskImageClaim
	for _, entry := range entries {
		if entry.Name() == ".daemon-owner.lock" && entry.Type().IsRegular() {
			if err := checkNativeJournalPath(filepath.Join(b.diskStagingRoot, entry.Name()), false); err != nil {
				return err
			}
			known[entry.Name()] = true
			continue
		}
		epoch, jsonFile := strings.CutSuffix(entry.Name(), ".json")
		if !jsonFile {
			continue // Check against all validated claims below.
		}
		if !canonicalNativeHelperID(epoch) {
			return errors.New("native disk staging: unknown persistent claim path")
		}
		claim, err := readNativeDiskImageClaim(b.diskStagingRoot, epoch)
		if err != nil || claim.JailBase != b.base {
			return errors.Join(err, errors.New("native disk staging: claim belongs to another jail"))
		}
		if err := inspectNativeDiskImageSource(b.diskStagingRoot, claim); err != nil {
			return err
		}
		known[entry.Name()], known[epoch+nativeImageStagingSuffix] = true, true
		if claim.Source.KernelBoot != boot {
			retire = append(retire, claim)
			continue
		}
		var original *nativeImageSourceRecord
		for _, record := range records {
			if sameNativeDiskImageSource(claim, record) {
				if original != nil {
					return errors.New("native disk staging: original epoch has duplicate image authority")
				}
				original = &record
			}
		}
		if original == nil {
			return errors.New("native disk staging: same-boot claim lost its original image journal; quarantine required")
		}
		if err := inspectNativeDiskImageSourceRecord(b.diskStagingRoot, claim, original); err != nil {
			return err
		}
		if _, err := os.Lstat(nativeDiskImageSourcePath(b.diskStagingRoot, epoch)); errors.Is(err, os.ErrNotExist) && (claim.Version != 3 || original.Removed) {
			retire = append(retire, claim) // Crash after unlink, before claim removal.
		}
	}
	for _, entry := range entries {
		if !known[entry.Name()] {
			return errors.New("native disk staging: unowned entry prevents recovery")
		}
	}
	for _, claim := range retire {
		if err := retireNativeDiskImageClaim(b.diskStagingRoot, claim); err != nil {
			return err
		}
	}
	return nil
}
