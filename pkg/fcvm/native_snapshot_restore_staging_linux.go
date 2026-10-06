//go:build linux

// adr: 568 — receipt inputs acquire names only through original native epochs.
package fcvm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/storage"
	"golang.org/x/sys/unix"
)

type nativeSnapshotRestoreImageBackend interface {
	PrepareRestoreInput(context.Context, nativeLaunchRecord, string, *os.File, string, storage.ExclusiveArtifactReceipt) (nativeImagePreparation, error)
}

// This is a staging primitive, not a qualification restore operation. Its
// caller must separately fence the original execution before using the staged
// files. It neither launches a VM nor grants readiness, fallback or activation.
// Every clone has the prepared target's image epoch and persistent disk claim;
// the publication cohort supplies bytes, never the target's native authority.
func (v *JailerVMM) stageNativeSnapshotRestoreInputs(ctx context.Context, owner nativeLaunchRecord, root string, inputs nativeSnapshotRestoreInputs, completed nativeQualificationCaptureRecord, receipts nativeSnapshotRestoreReceiptJournal) error {
	r := v.nativeRecovery
	if r == nil || r.journal == nil || receipts == nil || root != v.chrootRoot(owner.Lease.Instance) || owner.Lease.IsBuilder ||
		owner.Lease.Instance == completed.InstanceID || owner.Generation == completed.NativeGeneration {
		return errors.New("native snapshot restore: staging requires a separate original prepared target")
	}
	if _, ok := r.imageSources.(nativeSnapshotRestoreImageBackend); !ok {
		return errors.New("native snapshot restore: descriptor staging backend is unavailable")
	}
	if err := v.requireNativeRestoreStagingOwner(owner); err != nil {
		return err
	}
	if err := inputs.Cohort.validate(completed); err != nil {
		return err
	}
	// Refuse the entire malformed descriptor set before the first image effect.
	for i, file := range inputs.Files {
		if err := requireNativeRestoreDescriptor(file, inputs.Cohort.Objects[i].Object); err != nil {
			return err
		}
	}
	backing, err := readNativeRestoreBacking(inputs.Files[3])
	if err != nil || backing != inputs.Backing || backing != completed.Backing {
		return errors.Join(err, errors.New("native snapshot restore: staged inputs lost original backing identity"))
	}
	if err := verifyNativeRestoreInputDigest(ctx, inputs.Files[3], inputs.Cohort.Objects[3].Object); err != nil {
		return err
	}
	j := nativeImageSourceJournal{owner: r.journal, backend: r.imageSources, helperGroups: r.helperGroups}
	for i, name := range [...]string{memSnapshotName, vmstateSnapshotName, layerImageName} {
		if err := errors.Join(inputs.Cohort.require(ctx, receipts), v.requireNativeRestoreStagingOwner(owner)); err != nil {
			return err
		}
		if _, err := j.stageRestoreInput(ctx, owner, root, inputs.Files[i], name, inputs.Cohort.Objects[i].Object); err != nil {
			return err
		}
	}
	return errors.Join(inputs.Cohort.require(ctx, receipts), v.requireNativeRestoreStagingOwner(owner), j.require(ctx, owner, false), ctx.Err())
}

func (v *JailerVMM) requireNativeRestoreStagingOwner(owner nativeLaunchRecord) error {
	r := v.nativeRecovery
	if r == nil || r.journal == nil || owner.Generation == "" || owner.Generation != r.generation(owner.Lease.Instance) {
		return errors.New("native snapshot restore: original daemon producer is unavailable")
	}
	if err := r.checkDaemonOwnership(); err != nil {
		return err
	}
	current, err := r.journal.read(owner.Lease.Instance)
	if err != nil {
		return err
	}
	if current.Generation != owner.Generation || current.KernelBootID != owner.KernelBootID || !sameNativePhysicalLease(current.Lease, owner.Lease) ||
		current.Authorized || current.Revoked || current.ExitConfirmed || current.ResourcesRemoved {
		return errors.New("native snapshot restore: original prepared target changed")
	}
	return nil
}

func (j *nativeImageSourceJournal) stageRestoreInput(ctx context.Context, owner nativeLaunchRecord, root string, input *os.File, name string, receipt storage.ExclusiveArtifactReceipt) (string, error) {
	backend, ok := j.backend.(nativeSnapshotRestoreImageBackend)
	if !ok || owner.Lease.IsBuilder || owner.ExitConfirmed || !nativeRestoreImageName(name) {
		return "", errors.New("native snapshot restore: original descriptor producer is unavailable")
	}
	readOnly, perms := name != layerImageName, uint32(0)
	if readOnly {
		perms = 0o044
	}
	return j.stagePrepared(ctx, owner, root, name, readOnly, perms, func(original nativeLaunchRecord) (nativeImagePreparation, error) {
		return backend.PrepareRestoreInput(ctx, original, root, input, name, receipt)
	})
}

func nativeRestoreImageName(name string) bool {
	return name == memSnapshotName || name == vmstateSnapshotName || name == layerImageName
}

func requireNativeRestoreDescriptor(file *os.File, receipt storage.ExclusiveArtifactReceipt) error {
	if err := receipt.Validate(); err != nil {
		return err
	}
	return requireNativeSealedRestoreDescriptor(file, receipt.LogicalBytes)
}

func requireNativeSealedRestoreDescriptor(file *os.File, size int64) error {
	if file == nil {
		return errors.New("native snapshot restore: original anonymous input descriptor is required")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return err
	}
	flags, flagErr := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	descriptorFlags, descriptorErr := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
	if err := errors.Join(flagErr, descriptorErr); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o7777 != 0o400 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 0 ||
		stat.Size <= 0 || stat.Size != size || flags&unix.O_ACCMODE != unix.O_RDONLY || descriptorFlags&unix.FD_CLOEXEC == 0 {
		return errors.New("native snapshot restore: input is not the private sealed receipt-sized descriptor")
	}
	return nil
}

// A new anonymous clone keeps guest writes and permission changes away from
// the verified input. No caller-supplied path or canonical object lookup occurs.
// The sole writable clone descriptor joins before stageRestoreInput returns.
func (b linuxNativeImageSources) PrepareRestoreInput(ctx context.Context, owner nativeLaunchRecord, root string, input *os.File, name string, receipt storage.ExclusiveArtifactReceipt) (prepared nativeImagePreparation, result error) {
	if !nativeRestoreImageName(name) {
		return nil, errors.New("native snapshot restore: receipt input name is unsupported")
	}
	if err := receipt.Validate(); err != nil {
		return nil, err
	}
	return b.prepareVerifiedRestoreImage(ctx, owner, root, input, name, receipt.LogicalBytes, receipt.SHA256)
}

func (b linuxNativeImageSources) prepareVerifiedRestoreImage(ctx context.Context, owner nativeLaunchRecord, root string, input *os.File, name string, size int64, digest string) (prepared nativeImagePreparation, result error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if owner.Authorized || owner.Revoked || owner.ExitConfirmed || owner.ResourcesRemoved || owner.Lease.IsBuilder || b.diskStagingRoot == "" {
		return nil, errors.New("native snapshot restore: clone requires an original prepared target and persistent staging root")
	}
	if err := requireNativeSealedRestoreDescriptor(input, size); err != nil {
		return nil, err
	}
	directory, err := nativeDiskImageRootIdentity(b.diskStagingRoot)
	if err != nil || nativePublicationRootsOverlap(b.base, b.diskStagingRoot) {
		return nil, errors.Join(err, errors.New("native snapshot restore: disk staging must remain outside the jail"))
	}
	identity, _, err := nativeImageFileMetadata(input)
	if err != nil || identity.Device != directory.Device {
		return nil, errors.Join(err, errors.New("native snapshot restore: input and persistent staging require the same original disk"))
	}
	rootFile, _, err := b.prepareRoot(owner, root, name)
	if err != nil {
		return nil, err
	}
	p := &linuxNativeImagePreparation{root: rootFile, owner: owner, diskRoot: b.diskStagingRoot, restoreClone: true}
	defer func() {
		if prepared == nil || result != nil {
			result = errors.Join(result, rootFile.Close())
			if p.source != nil {
				result = errors.Join(result, p.source.Close())
			}
			prepared = nil
		}
	}()
	// Reopen only the still-open original FD, giving copy its own offset.
	// This procfs reference is never passed to an ordinary path resolver.
	fd, err := unix.Open(nativeImageFDPath(input), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	source := os.NewFile(uintptr(fd), "native-original-restore-input")
	defer func() { result = errors.Join(result, source.Close()) }()
	reopened, _, err := nativeImageFileMetadata(source)
	if err := errors.Join(err, requireNativeSealedRestoreDescriptor(source, size)); err != nil || reopened != identity {
		return nil, errors.Join(err, errors.New("native snapshot restore: original input descriptor changed"))
	}
	p.source, err = cloneNativeStagedImage(ctx, source, b.diskStagingRoot)
	if err != nil {
		return nil, err
	}
	// Copy completion alone is insufficient: recheck the clone's original
	// receipt digest before any source epoch, name, mount or metadata grant.
	if err := verifyNativeRestoreDigest(ctx, p.source, size, digest); err != nil {
		return nil, err
	}
	p.identity, _, err = nativeImageFileMetadata(p.source)
	if err != nil || p.identity == identity || p.identity.Device != directory.Device {
		return nil, errors.Join(err, errors.New("native snapshot restore: clone aliases or differs from original disk"))
	}
	p.namespace, err = nativeLoopNamespaceIdentity()
	if err != nil {
		return nil, err
	}
	if current, err := nativeDiskImageRootIdentity(b.diskStagingRoot); err != nil || current != directory {
		return nil, errors.Join(err, errors.New("native snapshot restore: persistent staging root changed during clone"))
	}
	return p, ctx.Err()
}

func verifyNativeRestoreInputDigest(ctx context.Context, file *os.File, receipt storage.ExclusiveArtifactReceipt) error {
	return verifyNativeRestoreDigest(ctx, file, receipt.LogicalBytes, receipt.SHA256)
}

func verifyNativeRestoreDigest(ctx context.Context, file *os.File, size int64, digest string) error {
	if file == nil {
		return storage.ErrArtifactReceiptMismatch
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || size <= 0 || info.Size() != size {
		return errors.Join(err, storage.ErrArtifactReceiptMismatch)
	}
	hash := sha256.New()
	n, err := io.CopyBuffer(hash, nativeImageCopyReader{ctx: ctx, source: io.NewSectionReader(file, 0, size)}, make([]byte, 128*1024))
	if err != nil || n != size || hex.EncodeToString(hash.Sum(nil)) != digest {
		return errors.Join(err, storage.ErrArtifactReceiptMismatch)
	}
	return ctx.Err()
}
