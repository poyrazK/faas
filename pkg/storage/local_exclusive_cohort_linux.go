//go:build linux

// adr: 1039 — local retirement moves the original capture directory out of
// the public namespace before any receipt-bound file removal.
package storage

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

const localRetirementNamespace = ".capture-retirement"
const localRetirementSeal = ".sealed"

func (l *LocalStorageBackend) validateCaptureCohort(captureID string, objects [4]ExclusiveArtifactReceipt) (string, error) {
	id, err := uuid.Parse(captureID)
	if err != nil || id.String() != captureID {
		return "", ErrArtifactReceiptMismatch
	}
	directory := path.Dir(objects[0].ObjectKey)
	if path.Base(directory) != "v2" || path.Base(path.Dir(directory)) != captureID || path.Base(path.Dir(path.Dir(directory))) != "captures" {
		return "", ErrArtifactReceiptMismatch
	}
	for i, kind := range [...]string{"mem", "vmstate", "drive", "backing"} {
		r := objects[i]
		if err := r.Validate(); err != nil {
			return "", err
		}
		if r.Backend != "local" || r.Location != l.root || r.Key != r.ObjectKey || r.ObjectKey != path.Join(directory, kind) ||
			r.Local.RootDevice != objects[0].Local.RootDevice || r.Local.RootInode != objects[0].Local.RootInode ||
			r.Local.ParentDevice != objects[0].Local.ParentDevice || r.Local.ParentInode != objects[0].Local.ParentInode {
			return "", ErrArtifactReceiptMismatch
		}
	}
	return directory, nil
}

func (l *LocalStorageBackend) retirementRoot(ctx context.Context, create bool, objects [4]ExclusiveArtifactReceipt) (root, private *os.File, err error) {
	root, err = l.exclusiveParent(ctx, "root-probe", false)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, root.Close())
			if private != nil {
				err = errors.Join(err, private.Close())
			}
			root, private = nil, nil
		}
	}()
	var identity unix.Stat_t
	if err = unix.Fstat(int(root.Fd()), &identity); err != nil {
		return
	}
	if uint64(identity.Dev) != objects[0].Local.RootDevice || identity.Ino != objects[0].Local.RootInode {
		err = ErrArtifactReceiptMismatch
		return
	}
	if create {
		if err = unix.Mkdirat(int(root.Fd()), localRetirementNamespace, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
			return
		}
		if err = root.Sync(); err != nil {
			return
		}
	}
	var fd int
	fd, err = openExclusiveArtifactDirectory(root, localRetirementNamespace)
	if err != nil {
		return
	}
	private = os.NewFile(uintptr(fd), "capture-retirement-namespace")
	var stat unix.Stat_t
	if err = errors.Join(checkExclusiveArtifactDirectory(private), unix.Fstat(fd, &stat)); err != nil {
		return
	}
	if stat.Mode&0o7777 != 0o700 || stat.Dev != identity.Dev {
		err = ErrArtifactReceiptMismatch
	}
	return
}

func (l *LocalStorageBackend) CheckExclusiveArtifactCohortRetirement(ctx context.Context, captureID string, objects [4]ExclusiveArtifactReceipt) (result error) {
	_, err := l.validateCaptureCohort(captureID, objects)
	if err != nil {
		return err
	}
	root, private, err := l.retirementRoot(ctx, false, objects)
	if errors.Is(err, unix.ENOENT) {
		// The private namespace is created only after owner authorization.
		return l.verifyPublicCohort(ctx, objects)
	}
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, private.Close(), root.Close()) }()
	quarantined, err := openExclusiveArtifactDirectory(private, captureID)
	if errors.Is(err, unix.ENOENT) {
		return l.verifyPublicCohort(ctx, objects)
	}
	if err != nil {
		return err
	}
	cohort := os.NewFile(uintptr(quarantined), "quarantined-capture")
	defer func() { result = errors.Join(result, cohort.Close()) }()
	return verifyQuarantinedCohort(ctx, cohort, objects)
}

func (l *LocalStorageBackend) verifyPublicCohort(ctx context.Context, objects [4]ExclusiveArtifactReceipt) (result error) {
	parent, err := l.exclusiveParent(ctx, objects[0].ObjectKey, false)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, parent.Close()) }()
	if err := verifyQuarantinedCohort(ctx, parent, objects); err != nil {
		return err
	}
	for _, object := range objects {
		if err := l.verifyPublicObject(ctx, object); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (l *LocalStorageBackend) verifyPublicObject(ctx context.Context, object ExclusiveArtifactReceipt) error {
	reader, err := GetExclusiveArtifact(ctx, l, object)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(io.Discard, reader)
	return errors.Join(copyErr, reader.Close(), ctx.Err())
}

func (l *LocalStorageBackend) RetireExclusiveArtifactCohort(ctx context.Context, captureID string, objects [4]ExclusiveArtifactReceipt) (err error) {
	directory, err := l.validateCaptureCohort(captureID, objects)
	if err != nil {
		return err
	}
	if err = l.CheckExclusiveArtifactCohortRetirement(ctx, captureID, objects); err != nil {
		return err
	}
	root, private, err := l.retirementRoot(ctx, true, objects)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close(), private.Close()) }()
	quarantined, openErr := openExclusiveArtifactDirectory(private, captureID)
	if errors.Is(openErr, unix.ENOENT) {
		parent, parentErr := l.exclusiveParent(ctx, directory, false)
		if parentErr != nil {
			return parentErr
		}
		defer func() { err = errors.Join(err, parent.Close()) }()
		// A competing replacement can be moved here, but its inode fails the
		// post-move check. It is never unlinked as the original cohort.
		if err = unix.Renameat2(int(parent.Fd()), "v2", int(private.Fd()), captureID, unix.RENAME_NOREPLACE); err != nil {
			return err
		}
		if err = errors.Join(parent.Sync(), private.Sync()); err != nil {
			return err
		}
		quarantined, openErr = openExclusiveArtifactDirectory(private, captureID)
	}
	if openErr != nil {
		return openErr
	}
	cohort := os.NewFile(uintptr(quarantined), "quarantined-capture")
	defer func() { err = errors.Join(err, cohort.Close()) }()
	if err = verifyQuarantinedCohort(ctx, cohort, objects); err != nil {
		return err
	}
	for _, object := range objects {
		name := path.Base(object.ObjectKey)
		var stat unix.Stat_t
		if statErr := unix.Fstatat(int(cohort.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); errors.Is(statErr, unix.ENOENT) {
			continue // A previous authorized attempt already removed it.
		} else if statErr != nil {
			return statErr
		}
		if err = checkQuarantinedIdentity(stat, object); err != nil {
			return err
		}
		if err = unix.Unlinkat(int(cohort.Fd()), name, 0); err != nil {
			return err
		}
	}
	if err = cohort.Sync(); err != nil {
		return err
	}
	fd, sealErr := unix.Openat(int(cohort.Fd()), localRetirementSeal, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if errors.Is(sealErr, unix.EEXIST) {
		return verifyQuarantinedCohort(ctx, cohort, objects)
	}
	if sealErr != nil {
		return sealErr
	}
	seal := os.NewFile(uintptr(fd), "capture-retirement-seal")
	return errors.Join(seal.Sync(), seal.Close(), cohort.Sync(), ctx.Err())
}

func verifyQuarantinedCohort(ctx context.Context, cohort *os.File, objects [4]ExclusiveArtifactReceipt) error {
	if err := errors.Join(ctx.Err(), checkExclusiveArtifactDirectory(cohort)); err != nil {
		return err
	}
	var parent unix.Stat_t
	if err := unix.Fstat(int(cohort.Fd()), &parent); err != nil {
		return err
	}
	if uint64(parent.Dev) != objects[0].Local.ParentDevice || parent.Ino != objects[0].Local.ParentInode {
		return ErrArtifactReceiptMismatch
	}
	duplicate, err := unix.Openat2(int(cohort.Fd()), ".", &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV,
	})
	if err != nil {
		return err
	}
	entriesFile := os.NewFile(uintptr(duplicate), "capture-retirement-entries")
	entries, readErr := entriesFile.Readdirnames(-1)
	if err := errors.Join(readErr, entriesFile.Close()); err != nil {
		return err
	}
	allowed := map[string]ExclusiveArtifactReceipt{}
	for _, object := range objects {
		allowed[path.Base(object.ObjectKey)] = object
	}
	sealed := false
	for _, name := range entries {
		if name == localRetirementSeal {
			sealed = true
			continue
		}
		if _, ok := allowed[name]; !ok || sealed {
			return ErrArtifactReceiptMismatch
		}
	}
	if sealed {
		var seal unix.Stat_t
		if err := unix.Fstatat(int(cohort.Fd()), localRetirementSeal, &seal, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		if seal.Mode&unix.S_IFMT != unix.S_IFREG || seal.Mode&0o7777 != 0o600 || seal.Uid != uint32(os.Geteuid()) || seal.Nlink != 1 || seal.Size != 0 || len(entries) != 1 {
			return ErrArtifactReceiptMismatch
		}
		return nil
	}
	for _, object := range objects {
		name := path.Base(object.ObjectKey)
		fd, err := unix.Openat2(int(cohort.Fd()), name, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_NONBLOCK | unix.O_NOFOLLOW | unix.O_CLOEXEC, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV})
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return err
		}
		file := os.NewFile(uintptr(fd), "original-quarantined-artifact")
		var stat unix.Stat_t
		if err := unix.Fstat(fd, &stat); err != nil {
			_ = file.Close()
			return err
		}
		if err := checkQuarantinedIdentity(stat, object); err != nil {
			_ = file.Close()
			return err
		}
		reader := &verifiedArtifactReader{ctx: ctx, source: file, receipt: object, digest: sha256.New()}
		_, copyErr := io.Copy(io.Discard, reader)
		var after unix.Stat_t
		statErr := unix.Fstat(fd, &after)
		closeErr := reader.Close()
		if err := errors.Join(copyErr, statErr, closeErr); err != nil {
			return err
		}
		if err := checkQuarantinedIdentity(after, object); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func checkQuarantinedIdentity(stat unix.Stat_t, receipt ExclusiveArtifactReceipt) error {
	if uint64(stat.Dev) != receipt.Local.Device || stat.Ino != receipt.Local.Inode || stat.Mode&unix.S_IFMT != unix.S_IFREG ||
		stat.Mode&0o7777 != 0o644 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || stat.Size != receipt.LogicalBytes {
		return ErrArtifactReceiptMismatch
	}
	return nil
}
